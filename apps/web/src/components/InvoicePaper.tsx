import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api } from "../lib/api-client";
import { formatMoney } from "../lib/money";
import type { LegalEntity } from "../lib/useOrgContext";
import layout from "../pages/DashboardPage.module.css";
import styles from "./InvoicePaper.module.css";
import ui from "./ui.module.css";

/** Mirrors internal/modules/sales/httpapi.preview: the figures the printed invoice
 * carries, already rounded and formatted by the server. */
interface Party {
  name: string;
  tax_label: string;
  tax_id: string;
  address: string[];
}
interface Preview {
  title: string;
  number: string;
  issue_date: string;
  due_date: string | null;
  currency: string;
  generic_tax: boolean;
  seller: Party;
  seller_phone: string;
  seller_email: string;
  seller_website: string;
  bill_to: Party;
  place_of_supply: string;
  reference: string;
  lines: { no: number; description: string; code: string; quantity: string; unit: string; rate: string; taxable: string; tax_rate: string; tax: string; total: string }[];
  subtotal: string;
  taxes: { label: string; rate?: string; amount: string }[];
  grand_total: string;
  base_currency?: string;
  exchange_rate?: string;
  grand_total_base?: string;
  bank: { name: string; account: string; code: string; upi: string };
  notes: string;
  terms: string;
  signatory: string;
  tax_notes: string[];
  signed_note?: string;
}

const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { day: "2-digit", month: "short", year: "numeric" });

/** The invoice as the customer receives it, on screen: the same seller, customer, itemised
 * tax and totals as the PDF, so a mistake shows up before it is printed or sent. */
export function InvoicePaper({ documentId, company, stamp }: { documentId: string; company?: LegalEntity; stamp?: string }) {
  const { t } = useTranslation();
  const preview = useQuery({
    queryKey: ["sales-document-preview", documentId],
    queryFn: () => api.get<Preview>(`/sales/documents/${documentId}/preview`),
  });

  if (preview.isPending) return <div className={layout.skeleton} style={{ height: 420 }} aria-hidden="true" />;
  if (preview.isError) {
    return (
      <div className={layout.errorState} role="alert">
        {t("invoice.previewFailed", { defaultValue: "Couldn't load the invoice preview." })}{" "}
        <button type="button" onClick={() => void preview.refetch()}>
          {t("common.retry", { defaultValue: "Retry" })}
        </button>
      </div>
    );
  }

  const p = preview.data;
  const money = (amount: string) => formatMoney({ amount, currency: p.currency });
  const hasBank = p.bank.name || p.bank.account || p.bank.upi;
  const showCode = p.lines.some((l) => l.code);
  const logo = company?.LogoPNG ? `data:image/png;base64,${company.LogoPNG}` : null;
  const taxHeader = p.generic_tax ? t("tax.heading", { defaultValue: "Tax" }) : "GST";

  return (
    <article className={styles.paper} aria-label={`${p.title} ${p.number}`}>
      <header className={styles.head}>
        <div className={styles.brand}>
          {logo ? <img src={logo} alt="" className={styles.logo} /> : <span className={styles.monogram} aria-hidden="true">{(p.seller.name || "?").trim().charAt(0).toUpperCase()}</span>}
          <div>
            <strong>{p.seller.name}</strong>
            {p.seller.tax_id ? (
              <span className={styles.muted}>
                {p.seller.tax_label} {p.seller.tax_id}
              </span>
            ) : null}
          </div>
        </div>
        <div className={styles.title}>
          <h2>{p.title}</h2>
          <p className={styles.number}>{p.number}</p>
          {stamp ? <span className={styles.stamp}>{stamp}</span> : null}
        </div>
      </header>

      <dl className={styles.meta}>
        <div>
          <dt>{t("invoice.issued", { defaultValue: "Issued" })}</dt>
          <dd>{day(p.issue_date)}</dd>
        </div>
        {p.due_date ? (
          <div>
            <dt>{t("invoice.due", { defaultValue: "Due" })}</dt>
            <dd>{day(p.due_date)}</dd>
          </div>
        ) : null}
        {!p.generic_tax && p.place_of_supply ? (
          <div>
            <dt>{t("invoice.placeOfSupply", { defaultValue: "Place of supply" })}</dt>
            <dd>{p.place_of_supply}</dd>
          </div>
        ) : null}
        {p.reference ? (
          <div>
            <dt>{t("invoice.reference", { defaultValue: "Your reference" })}</dt>
            <dd>{p.reference}</dd>
          </div>
        ) : null}
        <div className={styles.amount}>
          <dt>{t("invoice.total", { defaultValue: "Total" })}</dt>
          <dd>{money(p.grand_total)}</dd>
        </div>
      </dl>

      <section className={styles.parties}>
        <div>
          <h3>{t("invoice.from", { defaultValue: "From" })}</h3>
          <p>
            {p.seller.address.map((a) => (
              <span key={a}>{a}</span>
            ))}
            {[p.seller_phone, p.seller_email, p.seller_website].filter(Boolean).map((c) => (
              <span key={c} className={styles.muted}>
                {c}
              </span>
            ))}
          </p>
        </div>
        <div>
          <h3>{t("invoice.billedTo", { defaultValue: "Billed to" })}</h3>
          <p>
            <strong>{p.bill_to.name}</strong>
            {p.bill_to.address.map((a) => (
              <span key={a}>{a}</span>
            ))}
            {p.bill_to.tax_id ? (
              <span className={styles.muted}>
                {p.bill_to.tax_label} {p.bill_to.tax_id}
              </span>
            ) : null}
          </p>
        </div>
      </section>

      <div className={ui.tableScroll} tabIndex={0}>
        <table className={styles.lines}>
          <thead>
            <tr>
              <th scope="col">#</th>
              <th scope="col">{t("invoice.item", { defaultValue: "Item" })}</th>
              {showCode ? <th scope="col">{p.generic_tax ? t("tax.category") : "HSN/SAC"}</th> : null}
              <th scope="col" className={styles.num}>{t("invoice.qty", { defaultValue: "Qty" })}</th>
              <th scope="col" className={styles.num}>{t("invoice.rate", { defaultValue: "Rate" })}</th>
              <th scope="col" className={styles.num}>{taxHeader}</th>
              <th scope="col" className={styles.num}>{t("invoice.amount", { defaultValue: "Amount" })}</th>
            </tr>
          </thead>
          <tbody>
            {p.lines.map((l) => (
              <tr key={l.no}>
                <td className={styles.num}>{l.no}</td>
                <td>{l.description}</td>
                {showCode ? <td>{l.code || "—"}</td> : null}
                <td className={styles.num}>
                  {l.quantity} {l.unit}
                </td>
                <td className={styles.num}>{l.rate ? money(l.rate) : "—"}</td>
                <td className={styles.num}>{l.tax_rate ? `${l.tax_rate.replace(/%$/, "")}%` : "—"}</td>
                <td className={styles.num}>{money(l.total)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className={styles.foot}>
        <div className={styles.notes}>
          {p.tax_notes.length > 0 ? <p className={styles.muted}>{p.tax_notes.join(" · ")}</p> : null}
          {hasBank ? (
            <div>
              <h3>{t("invoice.payTo", { defaultValue: "Pay to" })}</h3>
              <p>
                {p.bank.name ? <span>{p.bank.name}</span> : null}
                {p.bank.account ? <span className={styles.mono}>{p.bank.account}{p.bank.code ? ` · ${p.bank.code}` : ""}</span> : null}
                {p.bank.upi ? <span className={styles.mono}>UPI {p.bank.upi}</span> : null}
              </p>
            </div>
          ) : null}
          {p.notes ? <p>{p.notes}</p> : null}
          {p.terms ? <p className={styles.muted}>{p.terms}</p> : null}
        </div>
        <dl className={styles.totals}>
          <div>
            <dt>{t("invoice.subtotal", { defaultValue: "Subtotal" })}</dt>
            <dd>{money(p.subtotal)}</dd>
          </div>
          {p.taxes.map((x) => (
            <div key={`${x.label}-${x.rate ?? ""}`}>
              <dt>
                {x.label}
                {x.rate ? ` ${x.rate}%` : ""}
              </dt>
              <dd>{money(x.amount)}</dd>
            </div>
          ))}
          <div className={styles.grand}>
            <dt>{t("invoice.total", { defaultValue: "Total" })}</dt>
            <dd>{money(p.grand_total)}</dd>
          </div>
          {p.grand_total_base && p.base_currency ? (
            <div>
              <dt>
                {p.base_currency} @ {p.exchange_rate}
              </dt>
              <dd>{formatMoney({ amount: p.grand_total_base, currency: p.base_currency })}</dd>
            </div>
          ) : null}
        </dl>
      </div>

      <footer className={styles.sign}>
        {p.signed_note ? <p className={styles.muted}>{p.signed_note}</p> : <span />}
        <div>
          <div className={styles.signLine} />
          <span className={styles.muted}>{p.signatory || t("invoice.signatory", { defaultValue: "Authorised signatory" })}</span>
        </div>
      </footer>
    </article>
  );
}
