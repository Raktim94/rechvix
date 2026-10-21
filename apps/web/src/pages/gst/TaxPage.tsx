import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ReportTable } from "../../components/ReportTable";
import ui from "../../components/ui.module.css";
import { countryName } from "../../lib/names";
import { useTaxReference } from "../../lib/reference";
import { withLegalEntity } from "../../lib/useReportTable";
import type { LegalEntity } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";

/** The tax screen for a company outside India: the verified rates that are built in
 * and the tax summary (collected on sales, paid on purchases, by tax and rate).
 * Indian companies keep the GST / e-Way Bill page. */
export function TaxPage({ legalEntity }: { legalEntity: LegalEntity }) {
  const { t } = useTranslation();
  const ref = useTaxReference(legalEntity.CountryCode);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  const params = new URLSearchParams({ format: "json" });
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  const summaryPath = withLegalEntity(`/reports/tax/summary?${params.toString()}`, legalEntity.ID);

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>{t("tax.heading")}</h1>
          <p className={layout.subtitle}>{t("tax.pageSubtitle", { company: legalEntity.LegalName })}</p>
        </div>
        <Link to="/settings" className={ui.btnSecondary}>
          {t("tax.settingsLink")}
        </Link>
      </div>

      <div className={layout.panel}>
        <h2>{t("tax.ratesTitle")}</h2>
        {ref.data && !ref.data.supported ? (
          <p role="alert">{t("tax.notSupported", { country: countryName(legalEntity.CountryCode, legalEntity.CountryCode) })}</p>
        ) : (
          <>
            <p className={ui.muted}>{ref.data ? t("tax.ratesHelp", { version: ref.data.rate_pack_version }) : null}</p>
            {legalEntity.CountryCode === "US" ? <p className={ui.muted}>{t("tax.usNote")}</p> : null}
            <div className={ui.tableScroll} tabIndex={0}>
              <table className={ui.table}>
                <thead>
                  <tr>
                    <th scope="col">{t("tax.colCategory")}</th>
                    <th scope="col">{t("tax.colRate")}</th>
                    <th scope="col">{t("tax.colKind")}</th>
                  </tr>
                </thead>
                <tbody>
                  {(ref.data?.categories ?? []).map((c) => (
                    <tr key={c.code}>
                      <td>{c.code}</td>
                      <td className="num">{c.rate ? `${c.rate}%` : "—"}</td>
                      <td>{c.kind}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>

      <div className={layout.panel}>
        <h2>{t("tax.summaryTitle")}</h2>
        <p className={ui.muted}>{t("tax.summaryHelp")}</p>
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="tax-from">{t("tax.from")}</label>
            <input id="tax-from" type="date" className={ui.input} value={from} max={to || undefined} onChange={(e) => setFrom(e.target.value)} />
          </div>
          <div className={ui.field}>
            <label htmlFor="tax-to">{t("tax.to")}</label>
            <input id="tax-to" type="date" className={ui.input} value={to} min={from || undefined} onChange={(e) => setTo(e.target.value)} />
          </div>
        </div>
        <ReportTable path={summaryPath} emptyLabel={t("tax.emptySummary")} />
      </div>
    </div>
  );
}
