import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError, apiUrl } from "../../lib/api-client";
import type { Party } from "../../lib/partyTypes";
import { useOrgContext } from "../../lib/useOrgContext";
import { withLegalEntity } from "../../lib/useReportTable";
import layout from "../DashboardPage.module.css";
import type { SalesDocument } from "../sales/types";

interface Settings { creditor_name: string; creditor_id: string; iban: string; bic?: string; configured: boolean }
interface Mandate { id: string; party_id: string; customer: string; mandate_ref: string; signed_on: string; iban_last4: string; account_holder: string; status: string }
interface Batch { id: string; msg_id: string; collection_date: string; total: string; count: number; status: string }
interface Coll { id: string; invoice: string; customer: string; amount: string; end_to_end_id: string; sequence_type: string; status: string; reason?: string }

function nextBusinessDay(days: number) {
  const d = new Date();
  let n = 0;
  while (n < days) { d.setDate(d.getDate() + 1); if (d.getDay() !== 0 && d.getDay() !== 6) n++; }
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function err(e: unknown, fb: string) { return e instanceof ApiError ? e.message : fb; }

function SettingsPanel({ leId }: { leId: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["sepa", "settings", leId], queryFn: () => api.get<Settings>(`/sepa/settings?legal_entity_id=${leId}`) });
  const [f, setF] = useState<Settings | null>(null);
  const v = f ?? q.data;
  const save = useMutation({ mutationFn: () => api.put(`/sepa/settings?legal_entity_id=${leId}`, v), onSuccess: () => { setF(null); void qc.invalidateQueries({ queryKey: ["sepa"] }); } });
  if (!v) return null;
  return (
    <section className={layout.panel} aria-labelledby="sepa-cred">
      <h2 id="sepa-cred">Your creditor details</h2>
      <p className={ui.muted}>Your bank gives you a SEPA creditor identifier when you are set up to collect direct debits.</p>
      <form onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}><label htmlFor="sp-name">Name on the collection</label><input id="sp-name" className={ui.input} value={v.creditor_name} onChange={(e) => setF({ ...v, creditor_name: e.target.value })} /></div>
          <div className={ui.field}><label htmlFor="sp-cid">Creditor identifier</label><input id="sp-cid" className={ui.input} placeholder="ES12ZZZ12345678" value={v.creditor_id} onChange={(e) => setF({ ...v, creditor_id: e.target.value })} /></div>
          <div className={ui.field}><label htmlFor="sp-iban">Your IBAN</label><input id="sp-iban" className={ui.input} value={v.iban} onChange={(e) => setF({ ...v, iban: e.target.value })} /></div>
          <div className={ui.field}><label htmlFor="sp-bic">Your BIC (optional)</label><input id="sp-bic" className={ui.input} value={v.bic ?? ""} onChange={(e) => setF({ ...v, bic: e.target.value })} /></div>
        </div>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}><button type="submit" className={ui.btnPrimary} disabled={save.isPending}>Save</button>{save.isSuccess ? <span role="status">Saved.</span> : null}</div>
        {save.isError ? <p role="alert">{err(save.error, "Could not save.")}</p> : null}
      </form>
    </section>
  );
}

function Mandates({ customers }: { customers: Party[] }) {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["sepa", "mandates"], queryFn: () => api.getListField<Mandate>("/sepa/mandates", "mandates") });
  const [f, setF] = useState({ party_id: "", mandate_ref: "", signed_on: new Date().toISOString().slice(0, 10), iban: "", bic: "", account_holder: "" });
  const create = useMutation({ mutationFn: () => api.post("/sepa/mandates", f), onSuccess: () => { setF({ ...f, mandate_ref: "", iban: "", bic: "", account_holder: "" }); void qc.invalidateQueries({ queryKey: ["sepa", "mandates"] }); } });
  const revoke = useMutation({ mutationFn: (id: string) => api.post(`/sepa/mandates/${id}/revoke`), onSuccess: () => void qc.invalidateQueries({ queryKey: ["sepa", "mandates"] }) });
  return (
    <section className={layout.panel} aria-labelledby="sepa-mand">
      <h2 id="sepa-mand">Customer mandates</h2>
      <p className={ui.muted}>Only record a mandate the customer has signed. Keep the signed paper or electronic copy: your bank can ask for it.</p>
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="m-cust">Customer</label>
            <select id="m-cust" className={ui.input} value={f.party_id} onChange={(e) => setF({ ...f, party_id: e.target.value })}>
              <option value="">Choose…</option>{customers.map((c) => <option key={c.ID} value={c.ID}>{c.LegalName}</option>)}
            </select>
          </div>
          <div className={ui.field}><label htmlFor="m-holder">Account holder</label><input id="m-holder" className={ui.input} value={f.account_holder} onChange={(e) => setF({ ...f, account_holder: e.target.value })} /></div>
          <div className={ui.field}><label htmlFor="m-iban">Customer IBAN</label><input id="m-iban" className={ui.input} autoComplete="off" value={f.iban} onChange={(e) => setF({ ...f, iban: e.target.value })} /></div>
          <div className={ui.field}><label htmlFor="m-bic">BIC (optional)</label><input id="m-bic" className={ui.input} value={f.bic} onChange={(e) => setF({ ...f, bic: e.target.value })} /></div>
          <div className={ui.field}><label htmlFor="m-signed">Signed on</label><input id="m-signed" type="date" className={ui.input} value={f.signed_on} onChange={(e) => setF({ ...f, signed_on: e.target.value })} /></div>
          <div className={ui.field}><label htmlFor="m-ref">Mandate reference (optional)</label><input id="m-ref" className={ui.input} maxLength={35} value={f.mandate_ref} onChange={(e) => setF({ ...f, mandate_ref: e.target.value })} /></div>
        </div>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}><button type="submit" className={ui.btnPrimary} disabled={!f.party_id || !f.iban || !f.account_holder || create.isPending}>Add mandate</button></div>
        {create.isError ? <p role="alert">{err(create.error, "Could not add the mandate.")}</p> : null}
      </form>
      {list.data && list.data.length > 0 ? (
        <div className={ui.tableScroll} tabIndex={0}>
          <table className={ui.table}>
            <caption className="srOnly">Direct-debit mandates</caption>
            <thead><tr><th scope="col">Customer</th><th scope="col">Reference</th><th scope="col">IBAN</th><th scope="col">Signed</th><th scope="col">Status</th><th scope="col"><span className="srOnly">Actions</span></th></tr></thead>
            <tbody>{list.data.map((m) => <tr key={m.id}><td>{m.customer}</td><td>{m.mandate_ref}</td><td>…{m.iban_last4}</td><td>{m.signed_on}</td><td>{m.status.toLowerCase()}</td><td>{m.status === "ACTIVE" ? <button type="button" className={ui.btnGhost} onClick={() => revoke.mutate(m.id)}>Revoke</button> : null}</td></tr>)}</tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}

function BatchDetail({ batch }: { batch: Batch }) {
  const qc = useQueryClient();
  const cols = useQuery({ queryKey: ["sepa", "collections", batch.id], queryFn: () => api.getListField<Coll>(`/sepa/batches/${batch.id}/collections`, "collections") });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["sepa"] });
  const sent = useMutation({ mutationFn: () => api.post(`/sepa/batches/${batch.id}/sent`), onSuccess: refresh });
  const discard = useMutation({ mutationFn: () => api.delete(`/sepa/batches/${batch.id}`), onSuccess: refresh });
  const result = useMutation({ mutationFn: (v: { id: string; collected: boolean }) => api.post(`/sepa/collections/${v.id}/result`, { collected: v.collected, reason: v.collected ? "" : "Rejected by the bank" }), onSuccess: refresh });
  const e = [sent, discard, result].find((m) => m.isError)?.error;
  return (
    <div style={{ marginTop: 12 }}>
      <div className={ui.formActions} style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 8 }}>
        <a className={ui.btnPrimary} href={apiUrl(`/sepa/batches/${batch.id}/file.xml`)}>Download the file for your bank</a>
        {batch.status === "DRAFT" ? <button type="button" className={ui.btnSecondary} disabled={sent.isPending} onClick={() => sent.mutate()}>I sent it to the bank</button> : null}
        {batch.status === "DRAFT" ? <button type="button" className={ui.btnGhost} onClick={() => discard.mutate()}>Discard</button> : null}
      </div>
      {e ? <p role="alert">{err(e, "Something went wrong.")}</p> : null}
      <div className={ui.tableScroll} tabIndex={0}>
        <table className={ui.table}>
          <caption className="srOnly">Collections in this batch</caption>
          <thead><tr><th scope="col">Invoice</th><th scope="col">Customer</th><th scope="col">Amount</th><th scope="col">Type</th><th scope="col">Status</th><th scope="col"><span className="srOnly">Result</span></th></tr></thead>
          <tbody>{(cols.data ?? []).map((c) => (
            <tr key={c.id}><td>{c.invoice}</td><td>{c.customer}</td><td>{c.amount}</td><td>{c.sequence_type}</td><td>{c.status.toLowerCase()}{c.reason ? ` · ${c.reason}` : ""}</td>
              <td>{batch.status === "SENT" && c.status === "PENDING" ? <><button type="button" className={ui.btnGhost} onClick={() => result.mutate({ id: c.id, collected: true })}>Collected</button> <button type="button" className={ui.btnGhost} onClick={() => result.mutate({ id: c.id, collected: false })}>Rejected</button></> : null}</td></tr>
          ))}</tbody>
        </table>
      </div>
    </div>
  );
}

function Batches({ leId }: { leId: string }) {
  const qc = useQueryClient();
  const org = useOrgContext();
  const docs = useQuery({ queryKey: ["sales-documents", leId], queryFn: () => api.getListField<SalesDocument>(withLegalEntity("/sales/documents", leId), "documents") });
  const batches = useQuery({ queryKey: ["sepa", "batches"], queryFn: () => api.getListField<Batch>("/sepa/batches", "batches") });
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [date, setDate] = useState(nextBusinessDay(3));
  const [open, setOpen] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => api.post<Batch>("/sepa/batches", { legal_entity_id: leId, collection_date: date, document_ids: [...picked] }),
    onSuccess: (b) => { setPicked(new Set()); setOpen(b.id); void qc.invalidateQueries({ queryKey: ["sepa"] }); },
  });
  const invoices = (docs.data ?? []).filter((d) => d.Status === "FINALIZED" && (d.DocumentType === "TAX_INVOICE" || d.DocumentType === "POS_INVOICE") && d.CurrencyCode === "EUR");
  void org;
  return (
    <section className={layout.panel} aria-labelledby="sepa-batches">
      <h2 id="sepa-batches">Collect by direct debit</h2>
      <p className={ui.muted}>Choose euro invoices whose customers have a mandate. The file is for your bank; invoices are marked paid when you record the bank's result.</p>
      <div className={ui.tableScroll} tabIndex={0} style={{ maxHeight: 260, overflowY: "auto" }}>
        <table className={ui.table}>
          <caption className="srOnly">Invoices to collect</caption>
          <thead><tr><th scope="col"><span className="srOnly">Select</span></th><th scope="col">Invoice</th><th scope="col">Date</th><th scope="col">Total</th></tr></thead>
          <tbody>{invoices.map((d) => (
            <tr key={d.ID}><td><input type="checkbox" aria-label={`Collect ${d.DocumentNumber}`} checked={picked.has(d.ID)} onChange={(e) => { const n = new Set(picked); if (e.target.checked) n.add(d.ID); else n.delete(d.ID); setPicked(n); }} /></td><td>{d.DocumentNumber}</td><td>{new Date(d.IssueDate).toLocaleDateString()}</td><td>{d.GrandTotalAmount?.amount ?? ""}</td></tr>
          ))}</tbody>
        </table>
      </div>
      <div className={ui.formGrid}>
        <div className={ui.field}><label htmlFor="sp-date">Collection date</label><input id="sp-date" type="date" className={ui.input} min={nextBusinessDay(2)} value={date} onChange={(e) => setDate(e.target.value)} /></div>
      </div>
      <div className={ui.formActions} style={{ justifyContent: "flex-start" }}><button type="button" className={ui.btnPrimary} disabled={picked.size === 0 || create.isPending} onClick={() => create.mutate()}>Create the collection file ({picked.size})</button></div>
      {create.isError ? <p role="alert">{err(create.error, "Could not create the file.")}</p> : null}
      {(batches.data ?? []).map((b) => (
        <div key={b.id} style={{ marginTop: 16, borderTop: "1px solid var(--color-border, #ddd)", paddingTop: 12 }}>
          <button type="button" className={ui.btnGhost} aria-expanded={open === b.id} onClick={() => setOpen(open === b.id ? null : b.id)}><strong>{b.msg_id}</strong></button>
          <span className={ui.muted}> · collect on {b.collection_date} · {b.count} invoice{b.count === 1 ? "" : "s"} · {b.total} EUR · {b.status === "DRAFT" ? "not sent yet" : "sent to the bank"}</span>
          {open === b.id ? <BatchDetail batch={b} /> : null}
        </div>
      ))}
    </section>
  );
}

export function SepaPage() {
  const org = useOrgContext();
  const customers = useQuery({ queryKey: ["parties"], queryFn: () => api.getListField<Party>("/contacts/parties", "parties") });
  const le = org.legalEntity;
  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Direct debit (SEPA)</h1>
          <p className={layout.subtitle}>Collect euro invoices from customers who signed a SEPA Core mandate.</p>
        </div>
      </div>
      {le ? (
        <>
          <SettingsPanel leId={le.ID} />
          <Mandates customers={(customers.data ?? []).filter((c) => c.PartyType !== "SUPPLIER")} />
          <Batches leId={le.ID} />
        </>
      ) : null}
    </div>
  );
}
