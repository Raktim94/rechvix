import { ApiError, NetworkError, type Api } from "../api/client";
import { getPayment, getSale, mapGet, markPaymentFailed, markPaymentPosted, markReversalDone, markReversalFailed, pendingPaymentWork } from "../db/repo";
import type { Db } from "../db/types";

export interface PaymentSyncResult { posted: number; reversed: number; failed: number; waiting: number }

/** Sends payments (and reversals of payments) to the server.
 *
 *  Safe to repeat: every receipt carries `client_ref = mobile:pay:<payment id>`, and the server returns the receipt it already booked
 *  for that reference instead of booking the money again. So a dropped connection, a killed app or a double tap can only ever result
 *  in one receipt. A payment against an invoice that hasn't reached the server yet simply waits. */
export async function pushPayments(api: Api, db: Db): Promise<PaymentSyncResult> {
  const out: PaymentSyncResult = { posted: 0, reversed: 0, failed: 0, waiting: 0 };
  const work = await pendingPaymentWork(db);

  for (const p of work.post) {
    let docId: string | null = null;
    if (p.sale_local_id) {
      const sale = await getSale(db, p.sale_local_id);
      if (!sale || !sale.progress.finalized || !sale.progress.docId) { out.waiting++; continue; }
      docId = sale.progress.docId;
    } else if (p.server_doc_id) docId = p.server_doc_id;
    const party = await mapGet(db, "party", p.party_id);
    const isLocalParty = !party && !!(await db.first("SELECT 1 FROM parties WHERE id=? AND origin='local'", [p.party_id]));
    if (isLocalParty) { out.waiting++; continue; } // the customer hasn't been uploaded yet

    try {
      const rec = await api.post<{ ID: string }>("/accounting/receipts", {
        party_id: party?.serverId ?? p.party_id,
        ...(docId ? { sales_document_id: docId } : {}),
        amount: p.amount, method: p.method,
        ...(p.reference ? { reference_number: p.reference } : {}),
        received_at: new Date(p.paid_at).toISOString(),
        client_ref: `mobile:pay:${p.id}`,
      });
      const now = await getPayment(db, p.id);
      if (now && now.reversed_at != null) {
        // The seller reversed it while it was being sent: the server has it, so it needs its own reversal.
        await db.run("UPDATE payments SET state='synced', server_receipt_id=?, reversal_state='pending' WHERE id=?", [rec.ID, p.id]);
      } else await markPaymentPosted(db, p.id, rec.ID);
      out.posted++;
    } catch (e) {
      if (e instanceof NetworkError || (e instanceof ApiError && e.status === 401)) throw e;
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 408 && e.status !== 429) {
        await markPaymentFailed(db, p.id, e.message, p.attempts + 1);
        out.failed++;
      } else if (p.attempts + 1 >= 5) {
        await markPaymentFailed(db, p.id, e instanceof Error ? e.message : "Server error", p.attempts + 1);
        out.failed++;
      } else {
        await db.run("UPDATE payments SET attempts=attempts+1, error=? WHERE id=?", [e instanceof Error ? e.message : "Server error", p.id]);
        throw e;
      }
    }
  }

  for (const p of work.reverse) {
    try {
      await api.post(`/accounting/receipts/${p.server_receipt_id}/reverse`, { reason: p.reversal_reason ?? "Reversed from the mobile app" });
      await markReversalDone(db, p.id);
      out.reversed++;
    } catch (e) {
      if (e instanceof NetworkError || (e instanceof ApiError && e.status === 401)) throw e;
      if (e instanceof ApiError && e.status === 409 && e.code === "ALREADY_REVERSED") { await markReversalDone(db, p.id); continue; }
      if (e instanceof ApiError && e.status >= 400 && e.status < 500) { await markReversalFailed(db, p.id); out.failed++; continue; }
      throw e;
    }
  }
  return out;
}
