import { ApiError, type Api } from "../api/client";
import {
  adjustStock, getSale, markSaleCancelled, paymentsForSale, reversePayment,
} from "../db/repo";
import type { Db } from "../db/types";
import { parseMinor } from "../lib/decimal";
import { livePayments } from "../lib/sale";
import { pushPayments } from "./payments";

export class CorrectionError extends Error {}
export interface Cancelled { number: string; receivedMinor: bigint; method: string | null }

/** Correcting an invoice the server already holds, the audited way: its payments are reversed (each reversal is a dated ledger
 *  entry with a reason), the invoice is cancelled on the server (which puts its stock back), and then the seller issues a fresh
 *  invoice. Nothing is deleted or rewritten; the old invoice stays visible, marked cancelled.
 *
 *  Safe to repeat. Every step can be run again after a crash: reversals and the cancel are no-ops the second time. If anything
 *  fails part-way, the invoice is left exactly as far along as it got and the error says what to do. */
export async function cancelSyncedInvoice(api: Api, db: Db, localId: string, reason: string, now: number): Promise<Cancelled> {
  const why = reason.trim();
  if (!why) throw new CorrectionError("Say why this invoice is being corrected.");
  const sale = await getSale(db, localId);
  if (!sale) throw new CorrectionError("Invoice not found.");
  const docId = sale.progress.docId;
  if (!docId || !sale.progress.finalized) throw new CorrectionError("This invoice hasn't reached the server yet, so it can simply be edited.");
  const number = sale.doc_number ?? sale.local_number ?? "";
  const all = await paymentsForSale(db, localId);
  const live = livePayments(all);
  // Money that was received for this invoice, including what an interrupted earlier attempt already reversed, so a retry still knows it.
  const marker = `Invoice ${number} corrected:`;
  const counted = all.filter((p) => p.reversed_at == null ? p.state !== "void" : (p.reversal_reason ?? "").startsWith(marker));
  const received = counted.reduce((n, p) => n + parseMinor(p.amount), 0n);
  const method = counted[0]?.method ?? null;

  if (!sale.cancelled) {
    for (const p of live) await reversePayment(db, p.id, `${marker} ${why}`, now);
    // Reversals that exist only on the phone are done; the ones the server booked must be reversed there before the invoice goes.
    const r = await pushPayments(api, db);
    const stillOpen = (await paymentsForSale(db, localId)).filter((p) => p.reversal_state === "pending" || p.reversal_state === "failed");
    if (stillOpen.length > 0 || r.failed > 0) throw new CorrectionError("The server hasn't confirmed reversing the payments yet, so the invoice was not cancelled. Check the connection and try again; nothing was lost.");
    try {
      await api.post(`/sales/documents/${docId}/cancel`, {});
    } catch (e) {
      // Already cancelled (an earlier attempt got through but the answer was lost): that is the state we want.
      if (!(e instanceof ApiError && e.status === 409 && e.code === "DOCUMENT_NOT_FINALIZED")) throw e;
    }
    for (const l of sale.payload.lines) await adjustStock(db, l.variantId, Number(l.qty));
    await markSaleCancelled(db, localId);
  }
  return { number, receivedMinor: received, method };
}
