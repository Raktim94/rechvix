import { ApiError, type Api } from "../api/client";
import {
  adjustStock, creditNotesForSale, getCreditNote, getSale, insertCreditNote, setCreditDraft, setCreditIssued,
  type CreditNote, type SaleLine, type SalePayload,
} from "../db/repo";
import type { Db } from "../db/types";
import { dec, decToString, minorToString, parseMinor, toMinor } from "../lib/decimal";
import { calcInvoice, snapshot } from "../lib/tax";
import { payloadCalc } from "../lib/sale";

export class CreditNoteError extends Error {}
export interface ReturnPick { index: number; qty: string }
export interface Credited { number: string; totalMinor: bigint }

/** How much of each line of the invoice the server has already credited. A note still waiting to be issued does not count: it is resumed, not added. */
export function creditedQty(notes: CreditNote[]): Map<number, bigint> {
  const m = new Map<number, bigint>();
  for (const n of notes.filter((x) => x.state === "issued")) for (const l of n.payload.lines) if (l.lineIndex != null) m.set(l.lineIndex, (m.get(l.lineIndex) ?? 0n) + dec(l.qty));
  return m;
}

interface ServerDoc { document: { ID: string; Status: string; DocumentNumber: string; GrandTotalAmount: { amount: string } | null }; lines: { ID: string; LineNumber: number; Quantity: string }[] }

/** Returns goods (or credits money) against an invoice the server already holds, the audited way: the server issues a numbered
 *  document that reduces what the customer owes (and, for returned goods, puts the stock back). Nothing on the original invoice is
 *  changed. The credit is recorded on the phone only once the server has issued it.
 *
 *  Safe to retry: the intent is saved first (same `localId`), the server draft id is remembered, and finalizing twice is harmless.
 *  A draft left behind by a lost answer has no accounting or stock effect until it is finalized. */
export async function issueCreditNote(api: Api, db: Db, saleLocalId: string, picks: ReturnPick[], goodsReturned: boolean, reason: string, localId: string, now: number): Promise<Credited> {
  const why = reason.trim();
  if (!why) throw new CreditNoteError("Say why this credit is being given.");
  const sale = await getSale(db, saleLocalId);
  if (!sale) throw new CreditNoteError("Invoice not found.");
  const docId = sale.progress.docId;
  if (!docId || !sale.progress.finalized) throw new CreditNoteError("This invoice hasn't reached the server yet. Edit it on the phone instead; a credit note can only be issued once the server has it.");
  if (sale.cancelled) throw new CreditNoteError("This invoice is cancelled.");

  let note = await getCreditNote(db, localId);
  if (!note) {
    const earlier = creditedQty(await creditNotesForSale(db, saleLocalId));
    const lines: SaleLine[] = [];
    for (const p of picks) {
      const orig = sale.payload.lines[p.index];
      if (!orig) throw new CreditNoteError("That line is not on the invoice.");
      const qty = dec(p.qty);
      if (qty <= 0n) continue;
      const left = dec(orig.qty) - (earlier.get(p.index) ?? 0n);
      if (qty > left) throw new CreditNoteError(`Only ${decToString(left)} of ${orig.name} can still be credited.`);
      // The server copies a line's discount whole, so a part-return of a discounted line would be credited too much. Refuse, never guess.
      if (dec(orig.discount || "0") > 0n && qty < dec(orig.qty)) throw new CreditNoteError(`${orig.name} was sold with a discount. Credit the whole line, or cancel and re-issue the invoice.`);
      lines.push({ ...orig, qty: p.qty, lineIndex: p.index });
    }
    if (lines.length === 0) throw new CreditNoteError("Choose at least one item to credit.");
    const calc = calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), sale.payload.pricingMode ?? "EXCLUSIVE", payloadCalc(sale.payload).interState);
    const payload: SalePayload = { ...sale.payload, lines, paymentMethod: null, calc: snapshot(calc), dueDate: null, goodsReturned, note: undefined, corrects: undefined };
    await insertCreditNote(db, localId, saleLocalId, payload, why, now);
    note = await getCreditNote(db, localId);
  }
  if (!note) throw new CreditNoteError("Could not save the credit note.");
  if (note.state === "issued") return { number: note.number ?? "", totalMinor: parseMinor(note.total) };

  let draftId = note.server_doc_id;
  if (!draftId) {
    const src = await api.get<ServerDoc>(`/sales/documents/${docId}`);
    const sorted = [...src.lines].sort((a, b) => a.LineNumber - b.LineNumber);
    if (sorted.length !== sale.payload.lines.length) throw new CreditNoteError("The invoice on the server has a different number of lines than on this phone. Refresh and try again.");
    const quantities: Record<string, string> = {};
    for (const l of note.payload.lines) {
      const sl = sorted[l.lineIndex!]!;
      if (dec(sl.Quantity) !== dec(sale.payload.lines[l.lineIndex!]!.qty)) throw new CreditNoteError("A quantity on the server differs from this phone. Refresh and try again.");
      quantities[sl.ID] = l.qty;
    }
    const created = await api.post<{ ID: string }>(`/sales/documents/${docId}/convert`, { target_type: note.payload.goodsReturned ? "SALES_RETURN" : "CREDIT_NOTE", line_quantities: quantities });
    draftId = created.ID;
    await setCreditDraft(db, localId, draftId);
  }
  try {
    await api.post(`/sales/documents/${draftId}/finalize`);
  } catch (e) {
    // Already finalized by an earlier attempt whose answer was lost: that is the state we want, read it back below.
    if (!(e instanceof ApiError && e.status === 409)) throw e;
  }
  const doc = (await api.get<ServerDoc>(`/sales/documents/${draftId}`)).document;
  if (doc.Status !== "FINALIZED" || !doc.DocumentNumber) throw new CreditNoteError("The server has not issued the credit note yet. Try again; nothing was lost.");
  const total = doc.GrandTotalAmount ? minorToString(toMinor(dec(doc.GrandTotalAmount.amount))) : "0";
  await setCreditIssued(db, localId, doc.DocumentNumber, total);
  if (note.payload.goodsReturned) for (const l of note.payload.lines) await adjustStock(db, l.variantId, Number(l.qty));
  return { number: doc.DocumentNumber, totalMinor: parseMinor(total) };
}
