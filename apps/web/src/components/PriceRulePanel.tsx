import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, ApiError } from "../lib/api-client";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

interface Rule { enabled: boolean; markup_percent: string; mode: "RAISE_ONLY" | "ALWAYS" }

/** Keeps selling prices in step with purchase costs: price = cost plus a markup, on the default price list. */
export function PriceRulePanel() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["purchase-rule"], queryFn: () => api.get<Rule>("/pricing/purchase-rule") });
  const [form, setForm] = useState<Rule | null>(null);
  useEffect(() => { if (q.data) setForm(q.data); }, [q.data]);
  const save = useMutation({
    mutationFn: (f: Rule) => api.put<Rule>("/pricing/purchase-rule", f),
    onSuccess: (d) => qc.setQueryData(["purchase-rule"], d),
  });
  if (!form) return null;
  return (
    <section className={layout.panel} aria-labelledby="price-rule-heading">
      <h2 id="price-rule-heading">Prices from purchases</h2>
      <p className={ui.muted}>When you finalize a purchase, update the selling price on your default price list to the purchase cost plus a markup. Purchases in a different currency from the price list are skipped.</p>
      <form onSubmit={(e) => { e.preventDefault(); save.mutate(form); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="pr-markup">Markup over cost (%)</label>
            <input id="pr-markup" type="number" min={0} max={1000} step="0.01" className={ui.input} value={form.markup_percent} onChange={(e) => setForm({ ...form, markup_percent: e.target.value })} />
          </div>
          <div className={ui.field}>
            <label htmlFor="pr-mode">When the new cost is lower</label>
            <select id="pr-mode" className={ui.input} value={form.mode} onChange={(e) => setForm({ ...form, mode: e.target.value as Rule["mode"] })}>
              <option value="RAISE_ONLY">Keep the current price (only raise prices)</option>
              <option value="ALWAYS">Lower the price too</option>
            </select>
          </div>
        </div>
        <label style={{ display: "flex", gap: 8, alignItems: "center", margin: "8px 0" }}>
          <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} /> Update selling prices automatically
        </label>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
          <button type="submit" className={ui.btnPrimary} disabled={save.isPending}>{save.isPending ? "Saving…" : "Save"}</button>
          {save.isSuccess ? <span role="status">Saved.</span> : null}
        </div>
        {save.isError ? <p role="alert">{save.error instanceof ApiError ? save.error.message : "Could not save."}</p> : null}
      </form>
    </section>
  );
}
