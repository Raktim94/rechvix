import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { api } from "../lib/api-client";
import { formatMoney, type Money } from "../lib/money";
import { useReportTable, withLegalEntity } from "../lib/useReportTable";
import { whatsAppShareUrl } from "../lib/whatsapp";
import styles from "../pages/DashboardPage.module.css";
import { WhatsAppIcon } from "./icons";
import ui from "./ui.module.css";

interface ReceivableRow {
  PartyID: string;
  PartyName: string;
  Phone: string;
  Total: Money;
}

const SHOWN = 5;

/** The two lists an owner acts on every morning: who to chase for money
 * (one tap to a WhatsApp reminder) and what to reorder. Both reuse the
 * report endpoints the Contacts and Inventory pages already call, so the
 * numbers always agree with those pages. */
export function DashboardFollowUps({ legalEntityId }: { legalEntityId: string | undefined }) {
  return (
    <div className={styles.lowerRow}>
      <Collect />
      <Reorder legalEntityId={legalEntityId} />
    </div>
  );
}

function Collect() {
  const q = useQuery({
    queryKey: ["receivables-detailed"], // same key as ReceivablesPanel, so it is fetched once
    queryFn: () => api.getListField<ReceivableRow>("/reports/accounting/receivables/detailed", "parties"),
  });
  const rows = [...(q.data ?? [])].sort((a, b) => Number(b.Total.amount) - Number(a.Total.amount)).slice(0, SHOWN);

  return (
    <section className={styles.panel} aria-labelledby="collect-heading">
      <h2 id="collect-heading">Collect payments</h2>
      {q.isPending ? (
        <div className={styles.skeleton} style={{ height: 140 }} aria-hidden="true" />
      ) : q.isError ? (
        <p className={styles.errorState} role="alert">
          Couldn't load who owes you.
        </p>
      ) : rows.length === 0 ? (
        <p className={styles.emptyState}>Nobody owes you anything right now.</p>
      ) : (
        <>
          <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
            {rows.map((r) => {
              const wa = r.Phone
                ? whatsAppShareUrl(r.Phone, `Reminder: you have an outstanding balance of ${formatMoney(r.Total)} with us. Please arrange payment at your earliest convenience. Thank you!`)
                : null;
              return (
                <li key={r.PartyID} style={{ display: "flex", alignItems: "center", gap: 12, padding: "10px 0", borderBottom: "1px solid var(--color-border)" }}>
                  <span style={{ flex: 1, minWidth: 0, overflowWrap: "anywhere" }}>{r.PartyName || "—"}</span>
                  <strong className="num">{formatMoney(r.Total)}</strong>
                  {wa ? (
                    <a className={ui.btnSecondary} href={wa} target="_blank" rel="noopener noreferrer" aria-label={`Send ${r.PartyName} a WhatsApp reminder`} style={{ minHeight: 44, minWidth: 44, display: "inline-flex", alignItems: "center", justifyContent: "center" }}>
                      <WhatsAppIcon />
                    </a>
                  ) : (
                    <span className={ui.muted} style={{ fontSize: "var(--text-xs)" }}>
                      no phone
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
          <p style={{ margin: "12px 0 0" }}>
            <Link to="/contacts">All customers and balances →</Link>
          </p>
        </>
      )}
    </section>
  );
}

function Reorder({ legalEntityId }: { legalEntityId: string | undefined }) {
  const q = useReportTable(withLegalEntity("/reports/inventory/low-stock?format=json", legalEntityId));
  // headers: Warehouse, Product, SKU, Qty On Hand, Reorder Level
  const rows = (q.data?.rows ?? []).slice(0, SHOWN);

  return (
    <section className={styles.panel} aria-labelledby="reorder-heading">
      <h2 id="reorder-heading">Running low</h2>
      {q.isPending ? (
        <div className={styles.skeleton} style={{ height: 140 }} aria-hidden="true" />
      ) : q.isError ? (
        <p className={styles.errorState} role="alert">
          Couldn't load stock levels.
        </p>
      ) : rows.length === 0 ? (
        <p className={styles.emptyState}>Nothing is running low.</p>
      ) : (
        <>
          <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
            {rows.map((r, i) => (
              <li key={`${r[1]}-${r[0]}-${i}`} style={{ display: "flex", alignItems: "center", gap: 12, padding: "10px 0", borderBottom: "1px solid var(--color-border)" }}>
                <span style={{ flex: 1, minWidth: 0, overflowWrap: "anywhere" }}>
                  {r[1]}
                  <span className={ui.muted} style={{ display: "block", fontSize: "var(--text-xs)" }}>
                    {r[0]} · {r[2]}
                  </span>
                </span>
                <span className="num" style={{ color: "var(--color-negative)" }}>
                  {r[3]} <span className={ui.muted}>/ {r[4]}</span>
                </span>
              </li>
            ))}
          </ul>
          <p style={{ margin: "12px 0 0", display: "flex", gap: 16, flexWrap: "wrap" }}>
            <Link to="/purchases">Create a purchase →</Link>
            <Link to="/inventory">All stock →</Link>
          </p>
        </>
      )}
    </section>
  );
}
