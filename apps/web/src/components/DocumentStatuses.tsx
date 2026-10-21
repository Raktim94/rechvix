import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "../lib/api-client";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

export interface DocStatus { id: string; name: string; color: string; sort_order: number; active: boolean; in_use: number }
export interface DocAssignment { document_id: string; status_id: string; name: string; color: string }

const tone: Record<string, string> = { neutral: "neutral", blue: "info", green: "positive", amber: "warning", red: "negative", purple: "neutral" };

export function useDocStatuses() {
  return useQuery({ queryKey: ["doc-statuses"], queryFn: () => api.getListField<DocStatus>("/document-statuses", "statuses") });
}

/** Statuses assigned to a set of documents, for showing a badge in a list. */
export function useDocAssignments(ids: string[]) {
  const key = ids.join(",");
  return useQuery({
    queryKey: ["doc-assignments", key],
    enabled: ids.length > 0,
    queryFn: () => api.getListField<DocAssignment>(`/document-statuses/assignments?ids=${key}`, "assignments"),
  });
}

export function StatusBadge({ name, color }: { name: string; color: string }) {
  return <span className={ui.badge} data-tone={tone[color] ?? "neutral"}>{name}</span>;
}

/** A workflow label on a document ("Waiting for client"). It never changes the document's accounting state. */
export function DocumentStatusPicker({ documentId }: { documentId: string }) {
  const qc = useQueryClient();
  const statuses = useDocStatuses();
  const current = useDocAssignments([documentId]);
  const set = useMutation({
    mutationFn: (statusId: string | null) => api.put(`/sales/documents/${documentId}/custom-status`, { status_id: statusId }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["doc-assignments"] }),
  });
  const active = (statuses.data ?? []).filter((s) => s.active);
  const value = current.data?.[0]?.status_id ?? "";
  if (active.length === 0 && !value) return null;
  return (
    <div className={ui.field} style={{ marginTop: 12, maxWidth: 320 }}>
      <label htmlFor="doc-status">Workflow status</label>
      <select id="doc-status" className={ui.input} value={value} disabled={set.isPending} onChange={(e) => set.mutate(e.target.value || null)}>
        <option value="">None</option>
        {(statuses.data ?? []).filter((s) => s.active || s.id === value).map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
      </select>
      {set.isError ? <p role="alert">{set.error instanceof ApiError ? set.error.message : "Could not change the status."}</p> : null}
    </div>
  );
}

/** Settings: define the labels. */
export function DocumentStatusSettings() {
  const qc = useQueryClient();
  const statuses = useDocStatuses();
  const [name, setName] = useState("");
  const [color, setColor] = useState("blue");
  const refresh = () => void qc.invalidateQueries({ queryKey: ["doc-statuses"] });
  const create = useMutation({ mutationFn: () => api.post("/document-statuses", { name, color, sort_order: (statuses.data?.length ?? 0) + 1 }), onSuccess: () => { setName(""); refresh(); } });
  const toggle = useMutation({ mutationFn: (s: DocStatus) => api.put(`/document-statuses/${s.id}`, { name: s.name, color: s.color, sort_order: s.sort_order, active: !s.active }), onSuccess: refresh });
  const remove = useMutation({ mutationFn: (id: string) => api.delete(`/document-statuses/${id}`), onSuccess: refresh });
  const err = [create, toggle, remove].find((m) => m.isError)?.error;
  return (
    <section className={layout.panel} aria-labelledby="docstatus-heading">
      <h2 id="docstatus-heading">Workflow statuses</h2>
      <p className={ui.muted}>Your own labels for following a sale through your process, like "Waiting for client" or "Ready to ship". They do not change the invoice itself.</p>
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}><label htmlFor="ds-name">Name</label><input id="ds-name" className={ui.input} maxLength={40} value={name} onChange={(e) => setName(e.target.value)} /></div>
          <div className={ui.field}>
            <label htmlFor="ds-color">Colour</label>
            <select id="ds-color" className={ui.input} value={color} onChange={(e) => setColor(e.target.value)}>
              {["neutral", "blue", "green", "amber", "red", "purple"].map((c) => <option key={c} value={c}>{c}</option>)}
            </select>
          </div>
        </div>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}><button type="submit" className={ui.btnPrimary} disabled={!name.trim() || create.isPending}>Add status</button></div>
      </form>
      {err ? <p role="alert">{err instanceof ApiError ? err.message : "Something went wrong."}</p> : null}
      {statuses.data && statuses.data.length > 0 ? (
        <ul style={{ listStyle: "none", padding: 0, margin: "12px 0 0", display: "flex", flexDirection: "column", gap: 6 }}>
          {statuses.data.map((s) => (
            <li key={s.id} style={{ display: "flex", gap: 10, alignItems: "center" }}>
              <StatusBadge name={s.name} color={s.color} />
              <span className={ui.muted}>{s.in_use} document{s.in_use === 1 ? "" : "s"}{s.active ? "" : " · switched off"}</span>
              <button type="button" className={ui.btnGhost} onClick={() => toggle.mutate(s)}>{s.active ? "Switch off" : "Switch on"}</button>
              {s.in_use === 0 ? <button type="button" className={ui.btnGhost} onClick={() => remove.mutate(s.id)}>Delete</button> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
