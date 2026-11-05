import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { api } from "../lib/api-client";
import { useOrgContext } from "../lib/useOrgContext";
import { withLegalEntity } from "../lib/useReportTable";
import styles from "./SetupChecklist.module.css";

const STORAGE_KEY = "rechvix.setup.done";

function readDone(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}
function writeDone() {
  try {
    window.localStorage.setItem(STORAGE_KEY, "1");
  } catch {
    /* private mode: the checklist simply keeps checking */
  }
}

interface Step {
  key: string;
  title: string;
  hint: string;
  to: string;
  cta: string;
  done: boolean;
}

/** First-run guidance for a brand-new business: the four things that
 * must exist before the first invoice can go out. It checks real data (not
 * a manual tick list), hides itself once everything is done, and stops
 * querying after that so it costs nothing on later loads. */
export function SetupChecklist() {
  const org = useOrgContext();
  const [finished, setFinished] = useState(readDone);
  const enabled = !finished && !!org.legalEntity;

  const products = useQuery({
    queryKey: ["setup-products"],
    queryFn: () => api.getListField<{ ID: string }>("/catalogue/products", "products"),
    enabled,
  });
  const parties = useQuery({
    queryKey: ["setup-parties"],
    queryFn: () => api.getListField<{ ID: string; LegalName: string }>("/contacts/parties", "parties"),
    enabled,
  });
  const sales = useQuery({
    queryKey: ["setup-sales", org.legalEntity?.ID],
    queryFn: () => api.getListField<{ ID: string; Status: string }>(withLegalEntity("/sales/documents", org.legalEntity?.ID), "documents"),
    enabled,
  });

  if (finished || !org.legalEntity) return null;
  if (products.isPending || parties.isPending || sales.isPending) return null;
  if (products.isError || parties.isError || sales.isError) return null;

  const isIndia = org.legalEntity.CountryCode === "IN";
  const steps: Step[] = [];
  if (isIndia) {
    steps.push({
      key: "state",
      title: "Set your business state",
      hint: "Needed to charge CGST + SGST or IGST correctly. Add your GSTIN here too if you are registered.",
      to: "/settings",
      cta: "Open settings",
      done: !!org.legalEntity.GSTStateCode,
    });
  }
  steps.push(
    {
      key: "items",
      title: "Add your items",
      hint: "Type them in, or import a spreadsheet from Vyapar, Tally or Excel.",
      to: "/catalogue",
      cta: "Add items",
      done: (products.data?.length ?? 0) > 0,
    },
    {
      key: "parties",
      title: "Add a customer",
      hint: "Optional for cash sales: Walk-in / Cash sale works without one.",
      to: "/contacts",
      cta: "Add contact",
      done: (parties.data ?? []).some((p) => p.LegalName !== "Walk-in Customer"),
    },
    {
      key: "sale",
      title: "Create your first invoice",
      hint: "Pick a customer, scan or search items, and finalize. Press Ctrl+Enter to finish quickly.",
      to: "/sales/new",
      cta: "New sale",
      done: (sales.data ?? []).some((d) => d.Status === "FINALIZED"),
    },
  );

  const doneCount = steps.filter((s) => s.done).length;
  if (doneCount === steps.length) {
    writeDone();
    return null;
  }
  const next = steps.find((s) => !s.done)!;

  return (
    <section className={styles.card} aria-labelledby="setup-title">
      <div className={styles.head}>
        <div>
          <h2 id="setup-title">Get started</h2>
          <p className={styles.sub}>
            {doneCount} of {steps.length} steps done. Next: {next.title.toLowerCase()}.
          </p>
        </div>
        <button
          type="button"
          className={styles.dismiss}
          onClick={() => {
            writeDone();
            setFinished(true);
          }}
        >
          Hide
        </button>
      </div>
      <div
        className={styles.bar}
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={steps.length}
        aria-valuenow={doneCount}
        aria-label="Setup progress"
      >
        <span style={{ width: `${(doneCount / steps.length) * 100}%` }} />
      </div>
      <ol className={styles.steps}>
        {steps.map((s) => (
          <li key={s.key} data-done={s.done} data-next={s === next}>
            <span className={styles.mark} aria-hidden="true">
              {s.done ? "✓" : ""}
            </span>
            <div className={styles.text}>
              <strong>
                {s.title}
                {s.done ? <span className="srOnly"> (done)</span> : null}
              </strong>
              {!s.done ? <span>{s.hint}</span> : null}
            </div>
            {!s.done ? (
              <Link to={s.to} className={s === next ? styles.primary : styles.secondary}>
                {s.cta}
              </Link>
            ) : null}
          </li>
        ))}
      </ol>
    </section>
  );
}
