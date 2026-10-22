import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import { ContactActions } from "./ContactActions";
import { KIND_LABEL, useMoveOpportunity, whenLabel, who, type Activity, type Opp, type Owner, type Stage } from "./crm";
import styles from "./CrmPage.module.css";

/** A deal in a side sheet: where it stands, who owns it, what happened, what is next. */
export function OpportunityDrawer({ opp, stages, owners, onClose }: { opp: Opp | undefined; stages: Stage[]; owners: Owner[] | undefined; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (opp && !dialog.open) dialog.showModal();
    else if (!opp && dialog.open) dialog.close();
  }, [opp]);

  return (
    <dialog
      ref={ref}
      className={styles.drawer}
      aria-labelledby="opp-heading"
      onClose={onClose}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      {opp ? <DrawerBody key={opp.id} opp={opp} stages={stages} owners={owners} onClose={onClose} /> : null}
    </dialog>
  );
}

function DrawerBody({ opp, stages, owners, onClose }: { opp: Opp; stages: Stage[]; owners: Owner[] | undefined; onClose: () => void }) {
  const qc = useQueryClient();
  const refresh = () => void qc.invalidateQueries({ queryKey: ["crm"] });
  const acts = useQuery({ queryKey: ["crm", "activities", opp.id], queryFn: () => api.getListField<Activity>(`/crm/opportunities/${opp.id}/activities`, "activities") });
  const [kind, setKind] = useState("NOTE");
  const [subject, setSubject] = useState("");
  const [due, setDue] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);

  const add = useMutation({
    mutationFn: () => api.post(`/crm/opportunities/${opp.id}/activities`, { kind, subject, due_at: due }),
    onSuccess: () => {
      setSubject("");
      setDue("");
      refresh();
    },
  });
  const toggle = useMutation({ mutationFn: (a: Activity) => api.post(`/crm/activities/${a.id}/${a.done_at ? "reopen" : "done"}`), onSuccess: refresh });
  const move = useMoveOpportunity(stages);
  const assign = useMutation({ mutationFn: (owner: string) => api.post(`/crm/opportunities/${opp.id}/assign`, { owner_user_id: owner || null }), onSuccess: refresh });
  const makeCustomer = useMutation({ mutationFn: () => api.post(`/crm/opportunities/${opp.id}/make-customer`), onSuccess: refresh });
  const remove = useMutation({
    mutationFn: () => api.delete(`/crm/opportunities/${opp.id}`),
    onSuccess: () => {
      refresh();
      onClose();
    },
  });
  const failed = [add, toggle, move, assign, makeCustomer, remove].find((m) => m.isError)?.error;
  const currentIndex = stages.findIndex((s) => s.id === opp.stage_id);
  const close = whenLabel(opp.expected_close);
  const lateClose = close.late && opp.stage_kind === "OPEN";

  return (
    <div className={styles.sheet}>
      <div className={styles.sheetHead}>
        <div>
          <h2 id="opp-heading">{opp.title}</h2>
          <p className={styles.sub}>{who(opp)}</p>
        </div>
        <button type="button" className={ui.btnGhost} onClick={onClose}>
          Close
        </button>
      </div>

      <ol className={styles.stageStrip} aria-label="Pipeline stage">
        {stages.map((s, i) => (
          <li key={s.id}>
            <button
              type="button"
              aria-current={s.id === opp.stage_id ? "step" : undefined}
              data-kind={s.kind}
              data-past={i < currentIndex && s.kind === "OPEN" ? "true" : undefined}
              disabled={move.isPending || s.id === opp.stage_id}
              onClick={() => move.mutate({ id: opp.id, stageId: s.id })}
            >
              {s.name}
            </button>
          </li>
        ))}
      </ol>

      <dl className={styles.facts}>
        <dt>Value</dt>
        <dd>
          <strong>{formatMoney({ amount: opp.value, currency: opp.currency })}</strong>
        </dd>
        {opp.expected_close ? (
          <>
            <dt>Expected close</dt>
            <dd className={lateClose ? styles.late : undefined}>{close.label}</dd>
          </>
        ) : null}
        {opp.contact_name ? (
          <>
            <dt>Contact</dt>
            <dd>{opp.contact_name}</dd>
          </>
        ) : null}
        {opp.contact_phone ? (
          <>
            <dt>Phone</dt>
            <dd>{opp.contact_phone}</dd>
          </>
        ) : null}
        {opp.contact_email ? (
          <>
            <dt>Email</dt>
            <dd>{opp.contact_email}</dd>
          </>
        ) : null}
        {opp.source ? (
          <>
            <dt>Source</dt>
            <dd>{opp.source}</dd>
          </>
        ) : null}
      </dl>
      <ContactActions opp={opp} />
      {opp.notes ? <p style={{ margin: 0 }}>{opp.notes}</p> : null}

      <div className={ui.field}>
        <label htmlFor="opp-owner">Owner</label>
        <select id="opp-owner" className={ui.select} value={opp.owner_user_id ?? ""} disabled={!owners || assign.isPending} onChange={(e) => assign.mutate(e.target.value)}>
          <option value="">Unassigned</option>
          {owners?.map((o) => (
            <option key={o.id} value={o.id}>
              {o.name}
            </option>
          ))}
        </select>
      </div>

      <div className={ui.formActions} style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 8 }}>
        {opp.party_id ? (
          <>
            <Link to="/sales/new" search={{ customer: opp.party_id, kind: "QUOTATION" }} className={ui.btnPrimary}>
              Create a quotation
            </Link>
            <Link to="/contacts/$id" params={{ id: opp.party_id }} className={ui.btnSecondary}>
              Open customer
            </Link>
          </>
        ) : (
          <button type="button" className={ui.btnPrimary} disabled={makeCustomer.isPending} onClick={() => makeCustomer.mutate()}>
            {makeCustomer.isPending ? "Adding…" : "Make this a customer"}
          </button>
        )}
      </div>

      <section aria-labelledby="activity-heading" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <h3 id="activity-heading" style={{ fontSize: "var(--text-md)" }}>
          Activity
        </h3>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
          style={{ display: "flex", flexDirection: "column", gap: 8 }}
        >
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <select aria-label="Type" className={ui.select} style={{ width: "auto" }} value={kind} onChange={(e) => setKind(e.target.value)}>
              {Object.entries(KIND_LABEL).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
            <input aria-label="What happened, or what is to do" placeholder="What happened, or what is to do" className={ui.input} style={{ flex: "1 1 200px" }} maxLength={200} value={subject} onChange={(e) => setSubject(e.target.value)} />
            {kind === "TASK" ? <input aria-label="Due" type="date" className={ui.input} style={{ width: "auto" }} value={due} onChange={(e) => setDue(e.target.value)} /> : null}
            <button type="submit" className={ui.btnSecondary} disabled={!subject.trim() || add.isPending}>
              Add
            </button>
          </div>
        </form>
        {acts.isPending ? <p className={styles.sub}>Loading activity…</p> : null}
        {acts.isError ? (
          <p role="alert" className={styles.errorBanner}>
            Activity could not be loaded.{" "}
            <button type="button" className={ui.btnGhost} onClick={() => void acts.refetch()}>
              Retry
            </button>
          </p>
        ) : null}
        {acts.data && acts.data.length === 0 ? <p className={styles.sub}>Nothing logged yet. Note a call or schedule a follow-up above.</p> : null}
        <ul className={styles.timeline}>
          {(acts.data ?? []).map((a) => {
            const w = whenLabel(a.due_at);
            return (
              <li key={a.id} data-kind={a.kind}>
                <span style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  {a.kind === "TASK" ? <input type="checkbox" aria-label={`Mark "${a.subject}" done`} checked={!!a.done_at} onChange={() => toggle.mutate(a)} /> : <span className={ui.badge} data-tone="neutral">{KIND_LABEL[a.kind] ?? a.kind}</span>}
                  <span className={a.done_at ? styles.done : undefined}>{a.subject}</span>
                </span>
                <span className={styles.sub}>
                  {a.kind === "TASK" && a.due_at && !a.done_at ? (
                    <span className={w.late ? styles.late : undefined}>{w.late ? w.label : `Due ${w.label}`} · </span>
                  ) : null}
                  {new Date(a.created_at).toLocaleDateString()}
                </span>
              </li>
            );
          })}
        </ul>
      </section>

      {failed ? (
        <p role="alert" className={styles.errorBanner}>
          {failed instanceof ApiError ? failed.message : "Something went wrong."}
        </p>
      ) : null}

      <div className={styles.dangerRow}>
        {confirmDelete ? (
          <>
            <span className={styles.sub}>Delete this lead and its activity?</span>
            <button type="button" className={ui.btnDanger} disabled={remove.isPending} onClick={() => remove.mutate()}>
              {remove.isPending ? "Deleting…" : "Yes, delete"}
            </button>
            <button type="button" className={ui.btnGhost} onClick={() => setConfirmDelete(false)}>
              Keep
            </button>
          </>
        ) : (
          <button type="button" className={ui.btnGhost} onClick={() => setConfirmDelete(true)}>
            Delete lead
          </button>
        )}
      </div>
    </div>
  );
}
