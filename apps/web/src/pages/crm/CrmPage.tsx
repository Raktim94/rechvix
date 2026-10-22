import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import layout from "../DashboardPage.module.css";

interface Stage { id: string; name: string; sort_order: number; kind: string }
interface Opp {
  id: string; title: string; party_id?: string; customer_name?: string; company_name?: string; contact_name?: string; contact_email?: string; contact_phone?: string;
  owner_user_id?: string; value: string; currency: string; stage_id: string; stage_kind: string; expected_close?: string; source?: string; notes?: string; open_tasks: number;
}
interface Activity { id: string; kind: string; subject: string; notes?: string; due_at?: string; done_at?: string; created_at: string; opportunity_title?: string; opportunity_id: string }

const KIND_LABEL: Record<string, string> = { CALL: "Call", EMAIL: "Email", MEETING: "Meeting", NOTE: "Note", TASK: "Task" };

function who(o: Opp) {
  return o.customer_name || o.company_name || o.contact_name || "";
}

function OppDetail({ opp, stages, onClose }: { opp: Opp; stages: Stage[]; onClose: () => void }) {
  const qc = useQueryClient();
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["crm"] });
  };
  const acts = useQuery({ queryKey: ["crm", "activities", opp.id], queryFn: () => api.getListField<Activity>(`/crm/opportunities/${opp.id}/activities`, "activities") });
  const [kind, setKind] = useState("NOTE");
  const [subject, setSubject] = useState("");
  const [due, setDue] = useState("");
  const add = useMutation({
    mutationFn: () => api.post(`/crm/opportunities/${opp.id}/activities`, { kind, subject, due_at: due }),
    onSuccess: () => { setSubject(""); setDue(""); refresh(); },
  });
  const toggle = useMutation({ mutationFn: (a: Activity) => api.post(`/crm/activities/${a.id}/${a.done_at ? "reopen" : "done"}`), onSuccess: refresh });
  const move = useMutation({ mutationFn: (stageId: string) => api.post(`/crm/opportunities/${opp.id}/move`, { stage_id: stageId }), onSuccess: refresh });
  const owners = useQuery({ queryKey: ["crm-owners"], queryFn: () => api.getListField<{ id: string; name: string }>("/crm/owners", "owners") });
  const assign = useMutation({ mutationFn: (owner: string) => api.post(`/crm/opportunities/${opp.id}/assign`, { owner_user_id: owner || null }), onSuccess: refresh });
  const makeCustomer = useMutation({ mutationFn: () => api.post(`/crm/opportunities/${opp.id}/make-customer`), onSuccess: refresh });
  const remove = useMutation({ mutationFn: () => api.delete(`/crm/opportunities/${opp.id}`), onSuccess: () => { refresh(); onClose(); } });
  const err = [add, toggle, move, assign, makeCustomer, remove].find((m) => m.isError)?.error;
  return (
    <section className={layout.panel} aria-labelledby="opp-heading">
      <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
        <h2 id="opp-heading">{opp.title}</h2>
        <button type="button" className={ui.btnGhost} onClick={onClose}>Close</button>
      </div>
      <p className={ui.muted}>{who(opp)}{opp.contact_name && opp.customer_name ? ` · ${opp.contact_name}` : ""}{opp.contact_email ? ` · ${opp.contact_email}` : ""}{opp.contact_phone ? ` · ${opp.contact_phone}` : ""}</p>
      <p><strong>{formatMoney({ amount: opp.value, currency: opp.currency })}</strong>{opp.expected_close ? ` · expected to close ${opp.expected_close}` : ""}</p>
      {opp.notes ? <p>{opp.notes}</p> : null}
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="opp-stage">Stage</label>
          <select id="opp-stage" className={ui.input} value={opp.stage_id} onChange={(e) => move.mutate(e.target.value)}>
            {stages.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </div>
        <div className={ui.field}>
          <label htmlFor="opp-owner">Opportunity owner</label>
          <select id="opp-owner" className={ui.input} value={opp.owner_user_id ?? ""} disabled={owners.isPending || owners.isError || assign.isPending} onChange={(e) => assign.mutate(e.target.value)}>
            <option value="">Unassigned</option>
            {owners.data?.map((owner) => <option key={owner.id} value={owner.id}>{owner.name}</option>)}
          </select>
          {owners.isError ? <p role="alert">Owners could not be loaded. <button type="button" onClick={() => void owners.refetch()}>Retry</button></p> : null}
        </div>
      </div>
      <div className={ui.formActions} style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 8 }}>
        {opp.party_id ? <Link to="/sales/new" search={{ customer: opp.party_id, kind: "QUOTATION" }} className={ui.btnPrimary}>Create a quotation</Link> : <button type="button" className={ui.btnPrimary} disabled={makeCustomer.isPending} onClick={() => makeCustomer.mutate()}>Make this a customer</button>}
        {opp.party_id ? <Link to="/contacts/$id" params={{ id: opp.party_id }} className={ui.btnSecondary}>Open customer</Link> : null}
        <button type="button" className={ui.btnGhost} onClick={() => { if (window.confirm("Delete this opportunity and its activities?")) remove.mutate(); }}>Delete</button>
      </div>
      {err ? <p role="alert">{err instanceof ApiError ? err.message : "Something went wrong."}</p> : null}

      <h3>Activity</h3>
      <form onSubmit={(e) => { e.preventDefault(); add.mutate(); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="act-kind">Type</label>
            <select id="act-kind" className={ui.input} value={kind} onChange={(e) => setKind(e.target.value)}>
              {Object.entries(KIND_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </div>
          <div className={ui.field}>
            <label htmlFor="act-subject">What happened, or what is to do</label>
            <input id="act-subject" className={ui.input} maxLength={200} value={subject} onChange={(e) => setSubject(e.target.value)} />
          </div>
          {kind === "TASK" ? (
            <div className={ui.field}>
              <label htmlFor="act-due">Due</label>
              <input id="act-due" type="date" className={ui.input} value={due} onChange={(e) => setDue(e.target.value)} />
            </div>
          ) : null}
        </div>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
          <button type="submit" className={ui.btnSecondary} disabled={!subject.trim() || add.isPending}>Add</button>
        </div>
      </form>
      <ul style={{ listStyle: "none", padding: 0, margin: "12px 0 0", display: "flex", flexDirection: "column", gap: 8 }}>
        {(acts.data ?? []).map((a) => (
          <li key={a.id} style={{ display: "flex", gap: 10, alignItems: "baseline" }}>
            {a.kind === "TASK" ? <input type="checkbox" aria-label={`Mark "${a.subject}" done`} checked={!!a.done_at} onChange={() => toggle.mutate(a)} /> : <span className={ui.badge} data-tone="neutral">{KIND_LABEL[a.kind]}</span>}
            <span style={a.done_at && a.kind === "TASK" ? { textDecoration: "line-through" } : undefined}>{a.subject}</span>
            <span className={ui.muted}>{a.due_at ? `due ${new Date(a.due_at).toLocaleDateString()} · ` : ""}{new Date(a.created_at).toLocaleDateString()}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function CrmPage() {
  const qc = useQueryClient();
  const stages = useQuery({ queryKey: ["crm", "stages"], queryFn: () => api.getListField<Stage>("/crm/stages", "stages") });
  const opps = useQuery({ queryKey: ["crm", "opps"], queryFn: () => api.getListField<Opp>("/crm/opportunities", "opportunities") });
  const tasks = useQuery({ queryKey: ["crm", "tasks"], queryFn: () => api.getListField<Activity>("/crm/tasks", "tasks") });
  const [selected, setSelected] = useState<string | null>(null);
  const [form, setForm] = useState({ title: "", company: "", contact: "", email: "", phone: "", value: "", currency: "EUR" });
  const create = useMutation({
    mutationFn: () => api.post<Opp>("/crm/opportunities", { title: form.title, company_name: form.company, contact_name: form.contact, contact_email: form.email, contact_phone: form.phone, value: form.value || "0", currency: form.currency }),
    onSuccess: (o) => { setForm({ ...form, title: "", company: "", contact: "", email: "", phone: "", value: "" }); void qc.invalidateQueries({ queryKey: ["crm"] }); setSelected(o.id); },
  });
  const current = (opps.data ?? []).find((o) => o.id === selected);
  const totalOpen = (opps.data ?? []).filter((o) => o.stage_kind === "OPEN").reduce((n, o) => n + Number(o.value), 0);

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>CRM</h1>
          <p className={layout.subtitle}>Leads and deals, from first contact to a customer. {totalOpen > 0 ? `Open pipeline: ${formatMoney({ amount: String(totalOpen), currency: opps.data?.[0]?.currency ?? "EUR" })}.` : ""}</p>
        </div>
      </div>

      <section className={layout.panel} aria-labelledby="new-opp-heading">
        <h2 id="new-opp-heading">New lead</h2>
        <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
          <div className={ui.formGrid}>
            <div className={ui.field}><label htmlFor="o-title">What is it about</label><input id="o-title" className={ui.input} maxLength={160} value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="o-company">Company</label><input id="o-company" className={ui.input} value={form.company} onChange={(e) => setForm({ ...form, company: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="o-contact">Contact</label><input id="o-contact" className={ui.input} value={form.contact} onChange={(e) => setForm({ ...form, contact: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="o-email">Email</label><input id="o-email" type="email" className={ui.input} value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="o-phone">Phone</label><input id="o-phone" className={ui.input} value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="o-value">Expected value</label><input id="o-value" inputMode="decimal" className={ui.input} value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="o-cur">Currency</label><input id="o-cur" className={ui.input} maxLength={3} value={form.currency} onChange={(e) => setForm({ ...form, currency: e.target.value.toUpperCase() })} /></div>
          </div>
          <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
            <button type="submit" className={ui.btnPrimary} disabled={!form.title.trim() || (!form.company.trim() && !form.contact.trim()) || create.isPending}>Add lead</button>
          </div>
          {create.isError ? <p role="alert">{create.error instanceof ApiError ? create.error.message : "Could not add the lead."}</p> : null}
        </form>
      </section>

      {current && stages.data ? <OppDetail key={current.id} opp={current} stages={stages.data} onClose={() => setSelected(null)} /> : null}

      <section className={layout.panel} aria-labelledby="pipeline-heading">
        <h2 id="pipeline-heading">Pipeline</h2>
        {!stages.data || !opps.data ? <div className={layout.skeleton} style={{ height: 200 }} aria-hidden="true" /> : (
          <div style={{ display: "grid", gridAutoFlow: "column", gridAutoColumns: "minmax(220px, 1fr)", gap: 12, overflowX: "auto", paddingBottom: 8 }} tabIndex={0}>
            {stages.data.map((st) => {
              const items = opps.data.filter((o) => o.stage_id === st.id);
              return (
                <div key={st.id} role="group" aria-label={`${st.name}, ${items.length} opportunities`}>
                  <h3 style={{ fontSize: "var(--text-sm)", marginBottom: 8 }}>{st.name} <span className={ui.muted}>({items.length})</span></h3>
                  <ul style={{ listStyle: "none", padding: 0, margin: 0, display: "flex", flexDirection: "column", gap: 8 }}>
                    {items.map((o) => (
                      <li key={o.id}>
                        <button type="button" onClick={() => setSelected(o.id)} className={ui.btnSecondary} style={{ width: "100%", textAlign: "left", display: "block", whiteSpace: "normal" }} aria-pressed={selected === o.id}>
                          <strong>{o.title}</strong><br />
                          <span className={ui.muted}>{who(o)}</span><br />
                          {formatMoney({ amount: o.value, currency: o.currency })}{o.open_tasks > 0 ? ` · ${o.open_tasks} task${o.open_tasks === 1 ? "" : "s"}` : ""}
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
              );
            })}
          </div>
        )}
      </section>

      {tasks.data && tasks.data.length > 0 ? (
        <section className={layout.panel} aria-labelledby="crm-tasks-heading">
          <h2 id="crm-tasks-heading">Follow-ups to do</h2>
          <ul style={{ listStyle: "none", padding: 0, margin: 0, display: "flex", flexDirection: "column", gap: 6 }}>
            {tasks.data.map((t) => (
              <li key={t.id}>
                <button type="button" className={ui.btnGhost} onClick={() => setSelected(t.opportunity_id)}>{t.subject}</button>
                <span className={ui.muted}> · {t.opportunity_title}{t.due_at ? ` · due ${new Date(t.due_at).toLocaleDateString()}` : ""}</span>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  );
}
