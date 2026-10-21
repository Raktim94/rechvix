import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import type { Party } from "../../lib/partyTypes";
import layout from "../DashboardPage.module.css";

interface Asset { id: string; party_id: string; customer: string; name: string; model?: string; serial_number?: string; location_note?: string; latitude?: string; longitude?: string; warranty_until?: string; in_warranty: boolean; status: string }
interface Incident { id: string; number: string; party_id: string; customer: string; asset_id?: string; asset_name?: string; title: string; description?: string; priority: string; status: string; assigned_user_id?: string; scheduled_for?: string; latitude?: string; longitude?: string; source: string; created_at: string; minutes_spent: number }
interface Update { id: string; kind: string; body: string; minutes_spent?: number; created_at: string }
interface Plan { id: string; asset_id: string; asset_name: string; customer: string; title: string; every_days: number; next_due: string; priority: string; active: boolean }

const STATUS = ["OPEN", "IN_PROGRESS", "WAITING", "RESOLVED", "CLOSED"];
const STATUS_LABEL: Record<string, string> = { OPEN: "Open", IN_PROGRESS: "In progress", WAITING: "Waiting", RESOLVED: "Resolved", CLOSED: "Closed" };
const PRIORITY_TONE: Record<string, string> = { URGENT: "negative", HIGH: "warning", NORMAL: "neutral", LOW: "neutral" };

function mapLink(lat?: string, lng?: string) {
  return lat && lng ? `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lng}#map=17/${lat}/${lng}` : null;
}

function useMyLocation(set: (lat: string, lng: string) => void) {
  const [err, setErr] = useState("");
  return {
    err,
    locate: () => {
      setErr("");
      if (!navigator.geolocation) { setErr("This browser cannot share a location."); return; }
      navigator.geolocation.getCurrentPosition(
        (p) => set(p.coords.latitude.toFixed(6), p.coords.longitude.toFixed(6)),
        () => setErr("Location was not shared."),
        { enableHighAccuracy: true, timeout: 10000 },
      );
    },
  };
}

function IncidentDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["service", "incident", id], queryFn: () => api.get<{ incident: Incident; updates: Update[] }>(`/service/incidents/${id}`) });
  const refresh = () => { void qc.invalidateQueries({ queryKey: ["service"] }); };
  const [note, setNote] = useState("");
  const [minutes, setMinutes] = useState("");
  const [visit, setVisit] = useState("");
  const comment = useMutation({ mutationFn: () => api.post(`/service/incidents/${id}/comments`, { body: note, minutes_spent: minutes ? Number(minutes) : null }), onSuccess: () => { setNote(""); setMinutes(""); refresh(); } });
  const setStatus = useMutation({ mutationFn: (s: string) => api.post(`/service/incidents/${id}/status`, { status: s }), onSuccess: refresh });
  const assign = useMutation({ mutationFn: () => api.post(`/service/incidents/${id}/assign`, { assign_to_me: true, scheduled_for: visit }), onSuccess: refresh });
  if (!q.data) return null;
  const { incident: i, updates } = q.data;
  const err = [comment, setStatus, assign].find((m) => m.isError)?.error;
  const link = mapLink(i.latitude, i.longitude);
  return (
    <section className={layout.panel} aria-labelledby="inc-heading">
      <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
        <h2 id="inc-heading">{i.number} · {i.title}</h2>
        <button type="button" className={ui.btnGhost} onClick={onClose}>Close</button>
      </div>
      <p className={ui.muted}>{i.customer}{i.asset_name ? ` · ${i.asset_name}` : ""} · <span className={ui.badge} data-tone={PRIORITY_TONE[i.priority]}>{i.priority.toLowerCase()}</span> · {i.minutes_spent} min logged{i.scheduled_for ? ` · visit ${new Date(i.scheduled_for).toLocaleString()}` : ""}{link ? <> · <a href={link} target="_blank" rel="noreferrer">Show on map<span className="srOnly"> (opens in a new tab)</span></a></> : null}</p>
      {i.description ? <p>{i.description}</p> : null}
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="inc-status">Status</label>
          <select id="inc-status" className={ui.input} value={i.status} onChange={(e) => setStatus.mutate(e.target.value)}>
            {STATUS.map((s) => <option key={s} value={s}>{STATUS_LABEL[s]}</option>)}
          </select>
        </div>
        <div className={ui.field}>
          <label htmlFor="inc-visit">Plan a visit</label>
          <input id="inc-visit" type="datetime-local" className={ui.input} value={visit} onChange={(e) => setVisit(e.target.value)} />
        </div>
      </div>
      <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
        <button type="button" className={ui.btnSecondary} disabled={assign.isPending} onClick={() => assign.mutate()}>Assign to me{visit ? " and plan the visit" : ""}</button>
      </div>
      <h3>Work log</h3>
      <form onSubmit={(e) => { e.preventDefault(); comment.mutate(); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}><label htmlFor="inc-note">Note</label><input id="inc-note" className={ui.input} value={note} onChange={(e) => setNote(e.target.value)} /></div>
          <div className={ui.field}><label htmlFor="inc-min">Minutes spent (optional)</label><input id="inc-min" type="number" min={1} max={1440} className={ui.input} value={minutes} onChange={(e) => setMinutes(e.target.value)} /></div>
        </div>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}><button type="submit" className={ui.btnPrimary} disabled={!note.trim() || comment.isPending}>Add to the log</button></div>
      </form>
      {err ? <p role="alert">{err instanceof ApiError ? err.message : "Something went wrong."}</p> : null}
      <ul style={{ listStyle: "none", padding: 0, margin: "12px 0 0", display: "flex", flexDirection: "column", gap: 6 }}>
        {[...updates].reverse().map((u) => (
          <li key={u.id}><span className={ui.muted}>{new Date(u.created_at).toLocaleString()} · {u.kind.toLowerCase()}</span> {u.body}{u.minutes_spent ? ` (${u.minutes_spent} min)` : ""}</li>
        ))}
      </ul>
    </section>
  );
}

function Incidents({ customers, assets }: { customers: Party[]; assets: Asset[] }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState("");
  const list = useQuery({ queryKey: ["service", "incidents", status], queryFn: () => api.getListField<Incident>(`/service/incidents?status=${status}`, "incidents") });
  const [open, setOpen] = useState<string | null>(null);
  const [form, setForm] = useState({ party_id: "", asset_id: "", title: "", description: "", priority: "NORMAL", latitude: "", longitude: "" });
  const loc = useMyLocation((lat, lng) => setForm((f) => ({ ...f, latitude: lat, longitude: lng })));
  const create = useMutation({
    mutationFn: () => api.post<Incident>("/service/incidents", { ...form, party_id: form.party_id || "00000000-0000-0000-0000-000000000000", asset_id: form.asset_id || null }),
    onSuccess: (i) => { setForm({ ...form, title: "", description: "" }); void qc.invalidateQueries({ queryKey: ["service"] }); setOpen(i.id); },
  });
  const ownerAssets = assets.filter((a) => a.status === "ACTIVE" && (!form.party_id || a.party_id === form.party_id));
  return (
    <>
      <section className={layout.panel} aria-labelledby="new-inc">
        <h2 id="new-inc">New incident</h2>
        <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="i-cust">Customer</label>
              <select id="i-cust" className={ui.input} value={form.party_id} onChange={(e) => setForm({ ...form, party_id: e.target.value, asset_id: "" })}>
                <option value="">Choose…</option>
                {customers.map((c) => <option key={c.ID} value={c.ID}>{c.LegalName}</option>)}
              </select>
            </div>
            <div className={ui.field}>
              <label htmlFor="i-asset">Equipment (optional)</label>
              <select id="i-asset" className={ui.input} value={form.asset_id} onChange={(e) => setForm({ ...form, asset_id: e.target.value })}>
                <option value="">None</option>
                {ownerAssets.map((a) => <option key={a.id} value={a.id}>{a.name}{a.serial_number ? ` (${a.serial_number})` : ""}</option>)}
              </select>
            </div>
            <div className={ui.field}><label htmlFor="i-title">What is wrong</label><input id="i-title" className={ui.input} maxLength={200} value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} /></div>
            <div className={ui.field}>
              <label htmlFor="i-prio">Priority</label>
              <select id="i-prio" className={ui.input} value={form.priority} onChange={(e) => setForm({ ...form, priority: e.target.value })}>
                {["LOW", "NORMAL", "HIGH", "URGENT"].map((p) => <option key={p} value={p}>{p.toLowerCase()}</option>)}
              </select>
            </div>
          </div>
          <div className={ui.field}><label htmlFor="i-desc">Details</label><textarea id="i-desc" className={ui.input} rows={2} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} /></div>
          <div className={ui.formActions} style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 8 }}>
            <button type="submit" className={ui.btnPrimary} disabled={!form.title.trim() || (!form.party_id && !form.asset_id) || create.isPending}>Create incident</button>
            <button type="button" className={ui.btnSecondary} onClick={loc.locate}>Use my location for the site</button>
            {form.latitude ? <span className={ui.muted}>{form.latitude}, {form.longitude}</span> : null}
          </div>
          {loc.err ? <p role="alert">{loc.err}</p> : null}
          {create.isError ? <p role="alert">{create.error instanceof ApiError ? create.error.message : "Could not create the incident."}</p> : null}
        </form>
      </section>
      {open ? <IncidentDetail key={open} id={open} onClose={() => setOpen(null)} /> : null}
      <section className={layout.panel} aria-labelledby="inc-list">
        <h2 id="inc-list">Incidents</h2>
        <div className={ui.field} style={{ maxWidth: 240 }}>
          <label htmlFor="inc-filter">Show</label>
          <select id="inc-filter" className={ui.input} value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">Everything not closed</option>
            {STATUS.map((s) => <option key={s} value={s}>{STATUS_LABEL[s]}</option>)}
          </select>
        </div>
        {list.data && list.data.length === 0 ? <p className={layout.emptyState}>No incidents here.</p> : (
          <div className={ui.tableScroll} tabIndex={0}>
            <table className={ui.table}>
              <caption className="srOnly">Incidents</caption>
              <thead><tr><th scope="col">No.</th><th scope="col">Customer</th><th scope="col">Equipment</th><th scope="col">Problem</th><th scope="col">Priority</th><th scope="col">Status</th><th scope="col">Time</th></tr></thead>
              <tbody>
                {(list.data ?? []).map((i) => (
                  <tr key={i.id}>
                    <td><button type="button" className={ui.btnGhost} onClick={() => setOpen(i.id)}>{i.number}</button></td>
                    <td>{i.customer}</td><td>{i.asset_name ?? ""}</td><td>{i.title}{i.source === "MAINTENANCE" ? " (maintenance)" : ""}</td>
                    <td><span className={ui.badge} data-tone={PRIORITY_TONE[i.priority]}>{i.priority.toLowerCase()}</span></td>
                    <td>{STATUS_LABEL[i.status]}</td><td>{i.minutes_spent ? `${i.minutes_spent} min` : ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}

function Assets({ customers, assets }: { customers: Party[]; assets: Asset[] }) {
  const qc = useQueryClient();
  const [f, setF] = useState({ party_id: "", name: "", model: "", serial_number: "", location_note: "", warranty_until: "", latitude: "", longitude: "" });
  const loc = useMyLocation((lat, lng) => setF((x) => ({ ...x, latitude: lat, longitude: lng })));
  const create = useMutation({ mutationFn: () => api.post("/service/assets", f), onSuccess: () => { setF({ ...f, name: "", model: "", serial_number: "", location_note: "", warranty_until: "" }); void qc.invalidateQueries({ queryKey: ["service"] }); } });
  const retire = useMutation({ mutationFn: (a: Asset) => api.put(`/service/assets/${a.id}`, { party_id: a.party_id, name: a.name, model: a.model ?? "", serial_number: a.serial_number ?? "", location_note: a.location_note ?? "", latitude: a.latitude ?? "", longitude: a.longitude ?? "", warranty_until: a.warranty_until ?? "", status: a.status === "ACTIVE" ? "RETIRED" : "ACTIVE" }), onSuccess: () => void qc.invalidateQueries({ queryKey: ["service"] }) });
  return (
    <>
      <section className={layout.panel} aria-labelledby="new-asset">
        <h2 id="new-asset">Customer equipment</h2>
        <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="a-cust">Customer</label>
              <select id="a-cust" className={ui.input} value={f.party_id} onChange={(e) => setF({ ...f, party_id: e.target.value })}>
                <option value="">Choose…</option>
                {customers.map((c) => <option key={c.ID} value={c.ID}>{c.LegalName}</option>)}
              </select>
            </div>
            <div className={ui.field}><label htmlFor="a-name">Name</label><input id="a-name" className={ui.input} value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="a-model">Model</label><input id="a-model" className={ui.input} value={f.model} onChange={(e) => setF({ ...f, model: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="a-serial">Serial number</label><input id="a-serial" className={ui.input} value={f.serial_number} onChange={(e) => setF({ ...f, serial_number: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="a-loc">Where it is</label><input id="a-loc" className={ui.input} value={f.location_note} onChange={(e) => setF({ ...f, location_note: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="a-war">Warranty until</label><input id="a-war" type="date" className={ui.input} value={f.warranty_until} onChange={(e) => setF({ ...f, warranty_until: e.target.value })} /></div>
          </div>
          <div className={ui.formActions} style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 8 }}>
            <button type="submit" className={ui.btnPrimary} disabled={!f.party_id || !f.name.trim() || create.isPending}>Add equipment</button>
            <button type="button" className={ui.btnSecondary} onClick={loc.locate}>Use my location for the site</button>
            {f.latitude ? <span className={ui.muted}>{f.latitude}, {f.longitude}</span> : null}
          </div>
          {loc.err ? <p role="alert">{loc.err}</p> : null}
          {create.isError ? <p role="alert">{create.error instanceof ApiError ? create.error.message : "Could not add this equipment."}</p> : null}
        </form>
      </section>
      <section className={layout.panel} aria-labelledby="asset-list">
        <h2 id="asset-list">Equipment list</h2>
        {assets.length === 0 ? <p className={layout.emptyState}>No equipment yet.</p> : (
          <div className={ui.tableScroll} tabIndex={0}>
            <table className={ui.table}>
              <caption className="srOnly">Customer equipment</caption>
              <thead><tr><th scope="col">Customer</th><th scope="col">Equipment</th><th scope="col">Serial</th><th scope="col">Warranty</th><th scope="col">Site</th><th scope="col"><span className="srOnly">Actions</span></th></tr></thead>
              <tbody>
                {assets.map((a) => {
                  const link = mapLink(a.latitude, a.longitude);
                  return (
                    <tr key={a.id}>
                      <td>{a.customer}</td><td>{a.name}{a.model ? ` · ${a.model}` : ""}{a.status === "RETIRED" ? " (retired)" : ""}</td><td>{a.serial_number}</td>
                      <td>{a.warranty_until ? `${a.in_warranty ? "Until" : "Ended"} ${a.warranty_until}` : ""}</td>
                      <td>{a.location_note}{link ? <> <a href={link} target="_blank" rel="noreferrer">Map<span className="srOnly"> (opens in a new tab)</span></a></> : null}</td>
                      <td><button type="button" className={ui.btnGhost} onClick={() => retire.mutate(a)}>{a.status === "ACTIVE" ? "Retire" : "Reactivate"}</button></td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}

function Plans({ assets }: { assets: Asset[] }) {
  const qc = useQueryClient();
  const plans = useQuery({ queryKey: ["service", "plans"], queryFn: () => api.getListField<Plan>("/service/plans", "plans") });
  const [f, setF] = useState({ asset_id: "", title: "", every_days: 90, first_due: new Date().toISOString().slice(0, 10), priority: "NORMAL" });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["service", "plans"] });
  const create = useMutation({ mutationFn: () => api.post("/service/plans", f), onSuccess: () => { setF({ ...f, title: "" }); refresh(); } });
  const toggle = useMutation({ mutationFn: (p: Plan) => api.post(`/service/plans/${p.id}/${p.active ? "pause" : "resume"}`), onSuccess: refresh });
  const remove = useMutation({ mutationFn: (id: string) => api.delete(`/service/plans/${id}`), onSuccess: refresh });
  return (
    <>
      <section className={layout.panel} aria-labelledby="new-plan">
        <h2 id="new-plan">Maintenance plans</h2>
        <p className={ui.muted}>A plan raises an incident on its due date, then moves to the next one.</p>
        <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="p-asset">Equipment</label>
              <select id="p-asset" className={ui.input} value={f.asset_id} onChange={(e) => setF({ ...f, asset_id: e.target.value })}>
                <option value="">Choose…</option>
                {assets.filter((a) => a.status === "ACTIVE").map((a) => <option key={a.id} value={a.id}>{a.customer} · {a.name}</option>)}
              </select>
            </div>
            <div className={ui.field}><label htmlFor="p-title">Service</label><input id="p-title" className={ui.input} value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} /></div>
            <div className={ui.field}><label htmlFor="p-days">Every (days)</label><input id="p-days" type="number" min={1} max={3650} className={ui.input} value={f.every_days} onChange={(e) => setF({ ...f, every_days: Number(e.target.value) })} /></div>
            <div className={ui.field}><label htmlFor="p-first">First due</label><input id="p-first" type="date" className={ui.input} value={f.first_due} onChange={(e) => setF({ ...f, first_due: e.target.value })} /></div>
          </div>
          <div className={ui.formActions} style={{ justifyContent: "flex-start" }}><button type="submit" className={ui.btnPrimary} disabled={!f.asset_id || !f.title.trim() || create.isPending}>Add plan</button></div>
          {create.isError ? <p role="alert">{create.error instanceof ApiError ? create.error.message : "Could not add the plan."}</p> : null}
        </form>
        {plans.data && plans.data.length > 0 ? (
          <div className={ui.tableScroll} tabIndex={0}>
            <table className={ui.table}>
              <caption className="srOnly">Maintenance plans</caption>
              <thead><tr><th scope="col">Customer</th><th scope="col">Equipment</th><th scope="col">Service</th><th scope="col">Every</th><th scope="col">Next due</th><th scope="col"><span className="srOnly">Actions</span></th></tr></thead>
              <tbody>
                {plans.data.map((p) => (
                  <tr key={p.id}><td>{p.customer}</td><td>{p.asset_name}</td><td>{p.title}</td><td>{p.every_days} days</td><td>{p.active ? p.next_due : "Paused"}</td>
                    <td><button type="button" className={ui.btnGhost} onClick={() => toggle.mutate(p)}>{p.active ? "Pause" : "Resume"}</button> <button type="button" className={ui.btnGhost} onClick={() => remove.mutate(p.id)}>Delete</button></td></tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </section>
    </>
  );
}

export function ServicePage() {
  const [tab, setTab] = useState<"incidents" | "assets" | "plans">("incidents");
  const customers = useQuery({ queryKey: ["parties"], queryFn: () => api.getListField<Party>("/contacts/parties", "parties") });
  const assets = useQuery({ queryKey: ["service", "assets"], queryFn: () => api.getListField<Asset>("/service/assets", "assets") });
  const tabs: [typeof tab, string][] = [["incidents", "Incidents"], ["assets", "Equipment"], ["plans", "Maintenance"]];
  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Field service</h1>
          <p className={layout.subtitle}>Equipment your customers own, the incidents raised on it, and the maintenance that keeps it running.</p>
        </div>
      </div>
      <div role="tablist" aria-label="Field service" style={{ display: "flex", gap: 8, marginBottom: 12 }}>
        {tabs.map(([k, label]) => <button key={k} type="button" role="tab" aria-selected={tab === k} className={tab === k ? ui.btnPrimary : ui.btnSecondary} onClick={() => setTab(k)}>{label}</button>)}
      </div>
      {tab === "incidents" ? <Incidents customers={(customers.data ?? []).filter((c) => c.PartyType !== "SUPPLIER")} assets={assets.data ?? []} /> : null}
      {tab === "assets" ? <Assets customers={(customers.data ?? []).filter((c) => c.PartyType !== "SUPPLIER")} assets={assets.data ?? []} /> : null}
      {tab === "plans" ? <Plans assets={assets.data ?? []} /> : null}
    </div>
  );
}
