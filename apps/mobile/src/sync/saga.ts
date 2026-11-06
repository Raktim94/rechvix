import type { Org, Progress, SalePayload } from "../db/repo";
import { num, round2 } from "../lib/money";

/** Replays one offline sale through the server's ordinary sales API: create draft -> add lines -> finalise -> receipt.
 *
 *  Why not copy rows to the server: invoices are ledger events. Finalising posts the journal, moves stock, assigns the
 *  next gap-free invoice number and computes tax. Only the server may do that, exactly once, with its own numbering.
 *
 *  Safe to interrupt and re-run at any point (app killed, signal lost mid-request):
 *   - the draft id is saved as soon as it exists;
 *   - on resume the server's own line count says which lines are still missing;
 *   - finalise only runs while the document is still a draft;
 *   - the receipt only covers whatever is still outstanding.
 *  The only gap is a draft created whose response never arrived: it is tagged `mobile:<localId>` in the customer
 *  reference so a person can find and delete the stray draft. It never posts to the ledger, so it cannot double-count. */

export interface SagaApi {
  get<T>(path: string): Promise<T>;
  post<T>(path: string, body?: unknown): Promise<T>;
}

interface Money { amount: string; currency: string }
interface DocView {
  document: { ID: string; Status: string; DocumentNumber: string; GrandTotalAmount: Money | null };
  lines: { ID: string }[] | null;
}
interface ReceiptList { receipts: { Amount: Money }[] | null }

export const customerReference = (localId: string): string => `mobile:${localId}`;

export async function runSale(
  api: SagaApi, org: Org, localId: string, payload: SalePayload, start: Progress, save: (p: Progress) => Promise<void>,
): Promise<{ progress: Progress; docNumber: string; total: string }> {
  const p: Progress = { ...start };

  if (!p.docId) {
    const created = await api.post<{ ID: string }>("/sales/documents", {
      legal_entity_id: org.legalEntityId,
      branch_id: org.branchId,
      warehouse_id: org.warehouseId,
      customer_party_id: payload.customerId,
      document_type: "TAX_INVOICE",
      customer_reference: customerReference(localId),
      ...(org.country === "IN" ? { place_of_supply_state_code: org.gstStateCode || "00" } : { customer_country: org.country }),
      currency_code: org.currency,
      base_currency_code: org.currency,
      exchange_rate: "1",
      pricing_mode: "EXCLUSIVE",
    });
    p.docId = created.ID;
    await save(p);
  }
  const docId = p.docId;

  let view = await api.get<DocView>(`/sales/documents/${docId}`);
  if (view.document.Status === "DRAFT") {
    // Lines are added strictly in order, so the number already on the server says where to carry on.
    for (let i = view.lines?.length ?? 0; i < payload.lines.length; i++) {
      const l = payload.lines[i]!;
      await api.post(`/sales/documents/${docId}/lines`, {
        product_variant_id: l.variantId, unit_id: l.unitId, quantity: l.qty, unit_price: l.price, line_discount_amount: l.discount || "0",
      });
    }
    await api.post(`/sales/documents/${docId}/finalize`);
    p.finalized = true;
    await save(p);
    view = await api.get<DocView>(`/sales/documents/${docId}`);
  } else {
    p.finalized = true;
  }

  const total = view.document.GrandTotalAmount?.amount ?? "0";
  if (payload.paymentMethod && !p.receipted) {
    const have = await api.get<ReceiptList>(`/accounting/sales-documents/${docId}/receipts`);
    const paid = (have.receipts ?? []).reduce((s, r) => s + num(r.Amount.amount), 0);
    const outstanding = round2(num(total) - paid);
    if (outstanding > 0.004) {
      await api.post("/accounting/receipts", {
        party_id: payload.customerId, sales_document_id: docId, amount: outstanding.toFixed(2),
        method: payload.paymentMethod, reference_number: customerReference(localId),
      });
    }
    p.receipted = true;
    await save(p);
  }
  return { progress: p, docNumber: view.document.DocumentNumber, total };
}
