import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { useOrgContext } from "../../lib/useOrgContext";
import type { Opp } from "./crm";
import styles from "./CrmPage.module.css";

const EMPTY = { title: "", company: "", contact: "", email: "", phone: "", value: "", currency: "" };

/** Adding a lead is a short, occasional task, so it lives in a dialog instead of taking the top of the page. */
export function NewLeadDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (opp: Opp) => void }) {
  const org = useOrgContext();
  const defaultCurrency = org.legalEntity?.BaseCurrencyCode || org.organisation?.DefaultCurrencyCode || "INR";
  const qc = useQueryClient();
  const ref = useRef<HTMLDialogElement>(null);
  const [form, setForm] = useState(EMPTY);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) {
      setForm(EMPTY);
      dialog.showModal();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  const create = useMutation({
    mutationFn: () =>
      api.post<Opp>("/crm/opportunities", {
        title: form.title,
        company_name: form.company,
        contact_name: form.contact,
        contact_email: form.email,
        contact_phone: form.phone,
        value: form.value || "0",
        currency: (form.currency || defaultCurrency).toUpperCase(),
      }),
    onSuccess: (opp) => {
      void qc.invalidateQueries({ queryKey: ["crm"] });
      onCreated(opp);
    },
  });

  const set = (key: keyof typeof EMPTY) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [key]: e.target.value });
  const valid = form.title.trim() && (form.company.trim() || form.contact.trim());

  return (
    <dialog ref={ref} className={styles.dialog} aria-labelledby="new-lead-heading" onClose={onClose}>
      <form
        className={styles.sheet}
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <div className={styles.sheetHead}>
          <h2 id="new-lead-heading">New lead</h2>
          <button type="button" className={ui.btnGhost} onClick={onClose}>
            Cancel
          </button>
        </div>
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="o-title">What is it about</label>
            <input id="o-title" className={ui.input} maxLength={160} value={form.title} onChange={set("title")} autoFocus />
          </div>
          <div className={ui.field}>
            <label htmlFor="o-company">Company</label>
            <input id="o-company" className={ui.input} value={form.company} onChange={set("company")} />
          </div>
          <div className={ui.field}>
            <label htmlFor="o-contact">Contact person</label>
            <input id="o-contact" className={ui.input} value={form.contact} onChange={set("contact")} />
          </div>
          <div className={ui.field}>
            <label htmlFor="o-phone">Phone</label>
            <input id="o-phone" type="tel" className={ui.input} value={form.phone} onChange={set("phone")} />
          </div>
          <div className={ui.field}>
            <label htmlFor="o-email">Email</label>
            <input id="o-email" type="email" className={ui.input} value={form.email} onChange={set("email")} />
          </div>
          <div className={ui.field}>
            <label htmlFor="o-value">Expected value</label>
            <input id="o-value" inputMode="decimal" className={ui.input} value={form.value} onChange={set("value")} />
          </div>
          <div className={ui.field}>
            <label htmlFor="o-cur">Currency</label>
            <input id="o-cur" className={ui.input} maxLength={3} value={form.currency || defaultCurrency} onChange={(e) => setForm({ ...form, currency: e.target.value.toUpperCase() })} />
          </div>
        </div>
        <p className={ui.muted} style={{ margin: 0 }}>
          A company or a contact person is needed so the lead can be found and called later.
        </p>
        {create.isError ? (
          <p role="alert" style={{ color: "var(--color-negative)", margin: 0 }}>
            {create.error instanceof ApiError ? create.error.message : "Could not add the lead."}
          </p>
        ) : null}
        <div className={ui.formActions} style={{ justifyContent: "flex-end" }}>
          <button type="submit" className={ui.btnPrimary} disabled={!valid || create.isPending}>
            {create.isPending ? "Adding…" : "Add lead"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
