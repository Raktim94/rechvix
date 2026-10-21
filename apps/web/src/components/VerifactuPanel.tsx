import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api, apiUrl, ApiError } from "../lib/api-client";
import type { LegalEntity } from "../lib/useOrgContext";
import ui from "./ui.module.css";
import layout from "../pages/DashboardPage.module.css";

interface Settings {
  legal_entity_id: string;
  applicable: boolean;
  enabled: boolean;
  environment: "test" | "production";
  installation_number: string;
  has_certificate: boolean;
  cert_subject?: string;
  cert_not_after?: string;
  producer_configured: boolean;
  problems: string[];
  records: number;
  pending: number;
}
interface RecordRow {
  seq: number;
  type: string;
  invoice_number: string;
  issue_date: string;
  invoice_type?: string;
  invoice_total?: string;
  hash: string;
  status: string;
  attempts: number;
  aeat_csv?: string;
  error_code?: string;
  error_message?: string;
}
interface ChainReport {
  records: number;
  valid: boolean;
  broken_at?: number;
  message?: string;
}

function errText(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

/** Spanish VERI*FACTU: switch the invoice record chain on, upload the company's electronic certificate, watch what
 * has been sent to AEAT and check the chain. Records are written whenever an invoice is finalized or cancelled. */
export function VerifactuPanel({ company }: { company: LegalEntity }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const base = `/legal-entities/${company.ID}/verifactu`;
  const settingsKey = ["verifactu", company.ID];
  const settings = useQuery({ queryKey: settingsKey, queryFn: () => api.get<Settings>(base) });
  const records = useQuery({
    queryKey: [...settingsKey, "records"],
    queryFn: () => api.getListField<RecordRow>(`${base}/records?limit=50`, "records"),
    enabled: !!settings.data?.enabled || (settings.data?.records ?? 0) > 0,
  });
  const [env, setEnv] = useState<"test" | "production" | null>(null);
  const [pw, setPw] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [report, setReport] = useState<ChainReport | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: settingsKey });

  const save = useMutation({
    mutationFn: (v: { enabled: boolean; environment: string }) => api.put<Settings>(base, v),
    onSuccess: (d) => qc.setQueryData(settingsKey, d),
  });
  const upload = useMutation({
    mutationFn: async () => {
      const res = await fetch(apiUrl(`${base}/certificate`), {
        method: "PUT",
        credentials: "include",
        headers: { "Content-Type": "application/octet-stream", "X-Certificate-Password": pw },
        body: file,
      });
      const body = await res.json().catch(() => undefined);
      if (!res.ok) throw new ApiError(res.status, body?.error?.code ?? "UNKNOWN_ERROR", body?.error?.message ?? "Upload failed");
      return body as Settings;
    },
    onSuccess: (d) => {
      qc.setQueryData(settingsKey, d);
      setPw("");
      setFile(null);
    },
  });
  const send = useMutation({ mutationFn: () => api.post(`${base}/send`), onSuccess: refresh });
  const verify = useMutation({ mutationFn: () => api.get<ChainReport>(`${base}/verify`), onSuccess: setReport });

  const s = settings.data;
  if (settings.isPending) return null;
  if (!s) return null;
  const environment = env ?? s.environment;

  return (
    <section className={layout.panel} aria-labelledby="verifactu-heading">
      <h2 id="verifactu-heading">{t("verifactu.heading")}</h2>
      <p className={ui.muted}>{t("verifactu.intro")}</p>

      {s.problems.length > 0 ? (
        <div role="status">
          <strong>{t("verifactu.toDo")}</strong>
          <ul>{s.problems.map((p) => <li key={p}>{p}</li>)}</ul>
        </div>
      ) : null}

      {s.applicable ? (
        <form onSubmit={(e) => { e.preventDefault(); save.mutate({ enabled: !s.enabled, environment }); }}>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="vf-env">{t("verifactu.environment")}</label>
              <select id="vf-env" className={ui.input} value={environment} onChange={(e) => setEnv(e.target.value as "test" | "production")} disabled={s.enabled}>
                <option value="test">{t("verifactu.envTest")}</option>
                <option value="production">{t("verifactu.envProd")}</option>
              </select>
            </div>
            {s.installation_number ? <div className={ui.field}><span>{t("verifactu.installation")}</span><strong>{s.installation_number}</strong></div> : null}
          </div>
          <div className={ui.formActions} style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 8 }}>
            <button type="submit" className={s.enabled ? ui.btnSecondary : ui.btnPrimary} disabled={save.isPending}>
              {s.enabled ? t("verifactu.disable") : t("verifactu.enable")}
            </button>
            {s.enabled ? <span role="status">{t("verifactu.on")}</span> : null}
          </div>
          {save.isError ? <p role="alert">{errText(save.error, t("verifactu.saveFailed"))}</p> : null}
        </form>
      ) : null}

      <h3>{t("verifactu.certificate")}</h3>
      {s.has_certificate ? (
        <p>{t("verifactu.certOk", { subject: s.cert_subject ?? "", date: s.cert_not_after ? s.cert_not_after.slice(0, 10) : "" })}</p>
      ) : (
        <p className={ui.muted}>{t("verifactu.certNone")}</p>
      )}
      <form onSubmit={(e) => { e.preventDefault(); upload.mutate(); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="vf-cert">{t("verifactu.certFile")}</label>
            <input id="vf-cert" type="file" accept=".p12,.pfx,application/x-pkcs12" className={ui.input} onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
          </div>
          <div className={ui.field}>
            <label htmlFor="vf-pw">{t("verifactu.certPassword")}</label>
            <input id="vf-pw" type="password" className={ui.input} value={pw} autoComplete="new-password" onChange={(e) => setPw(e.target.value)} />
          </div>
        </div>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
          <button type="submit" className={ui.btnSecondary} disabled={!file || !pw || upload.isPending}>{upload.isPending ? t("verifactu.uploading") : t("verifactu.upload")}</button>
        </div>
        {upload.isError ? <p role="alert">{errText(upload.error, t("verifactu.uploadFailed"))}</p> : null}
      </form>

      <h3>{t("verifactu.register")}</h3>
      <p className={ui.muted}>{t("verifactu.registerCount", { records: s.records, pending: s.pending })}</p>
      <div className={ui.formActions} style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 8 }}>
        <button type="button" className={ui.btnPrimary} disabled={!s.enabled || send.isPending} onClick={() => send.mutate()}>{send.isPending ? t("verifactu.sending") : t("verifactu.sendNow")}</button>
        <button type="button" className={ui.btnSecondary} disabled={verify.isPending} onClick={() => verify.mutate()}>{t("verifactu.verify")}</button>
        <a className={ui.btnSecondary} href={apiUrl(`${base}/export.xml`)}>{t("verifactu.exportXml")}</a>
      </div>
      {send.isError ? <p role="alert">{errText(send.error, t("verifactu.sendFailed"))}</p> : null}
      {report ? (
        <p role={report.valid ? "status" : "alert"}>
          {report.valid ? t("verifactu.chainOk", { records: report.records }) : t("verifactu.chainBroken", { seq: report.broken_at ?? 0, message: report.message ?? "" })}
        </p>
      ) : null}

      {records.data && records.data.length > 0 ? (
        <div className={ui.tableScroll} tabIndex={0}>
          <table className={ui.table}>
            <caption className="sr-only">{t("verifactu.register")}</caption>
            <thead>
              <tr>
                <th scope="col">#</th><th scope="col">{t("verifactu.colType")}</th><th scope="col">{t("verifactu.colInvoice")}</th>
                <th scope="col">{t("verifactu.colDate")}</th><th scope="col">{t("verifactu.colTotal")}</th><th scope="col">{t("verifactu.colStatus")}</th>
              </tr>
            </thead>
            <tbody>
              {records.data.map((r) => (
                <tr key={r.seq}>
                  <td>{r.seq}</td>
                  <td>{r.type === "ANULACION" ? t("verifactu.cancel") : r.invoice_type}</td>
                  <td>{r.invoice_number}</td>
                  <td>{r.issue_date}</td>
                  <td>{r.invoice_total ?? ""}</td>
                  <td>
                    {t(`verifactu.status.${r.status}`, { defaultValue: r.status })}
                    {r.aeat_csv ? ` · CSV ${r.aeat_csv}` : ""}
                    {r.error_message ? ` · ${r.error_code ?? ""} ${r.error_message}` : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}
