import { useState } from "react";
import { api, ApiError } from "../lib/api-client";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

/** Mirrors internal/platform/importer.RowResult/Report — every bulk
 * import endpoint (products, parties, and any future one) returns this
 * exact shape, so one component/one set of interfaces serves all of
 * them. */
interface RowResult {
  RowNumber: number;
  Outcome: "COMMITTED" | "VALID" | "ERROR" | "DUPLICATE";
  Message: string;
}
interface ImportReport {
  DryRun: boolean;
  Total: number;
  Committed: number;
  Valid: number;
  Errors: number;
  Duplicates: number;
  Results: RowResult[] | null;
}

function formatFor(file: File): "csv" | "xlsx" | null {
  const ext = file.name.toLowerCase().split(".").pop();
  if (ext === "csv") return "csv";
  if (ext === "xlsx") return "xlsx";
  return null;
}

// Minimal RFC 4180 quoting — only wraps a field in quotes (doubling any
// embedded quote) when it actually contains a comma, quote, or newline,
// so the common case stays plain and readable if someone opens this in a
// text editor instead of a spreadsheet app.
function csvField(value: string): string {
  return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}

function buildSampleCsv(headers: string[], rows: Record<string, string>[]): string {
  const lines = [headers.map(csvField).join(",")];
  for (const row of rows) {
    lines.push(headers.map((h) => csvField(row[h] ?? "")).join(","));
  }
  return lines.join("\r\n") + "\r\n";
}

function downloadSampleCsv(filename: string, headers: string[], rows: Record<string, string>[]) {
  const blob = new Blob([buildSampleCsv(headers, rows)], { type: "text/csv;charset=utf-8;" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

/** A collapsible "Import from CSV/Excel" panel — one component reused by
 * every bulk-import endpoint (internal/platform/importer's shared
 * dry-run + per-row-report design already made every module's import
 * logic identical server-side; nothing files-specific had a frontend
 * caller before this, matching the same "built and tested, zero UI"
 * pattern as invoice branding/API keys/webhooks before those got one).
 * Flow: pick a file -> Preview (dry_run=true, shows the report, writes
 * nothing) -> Import (dry_run=false, actually commits). A row is never
 * silently skipped — every row's outcome is listed. */
export function ImportPanel({
  title,
  path,
  columns,
  sampleRows,
  onImported,
}: {
  title: string;
  path: string;
  columns: string[];
  /** Raw column-key -> example-value rows for a downloadable sample CSV
   * (header row derived from the first row's keys). Optional — a panel
   * with no realistic example to offer (or no stable header set) can
   * omit this and keep just the inline `columns` description. */
  sampleRows?: Record<string, string>[];
  onImported: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [busy, setBusy] = useState<"preview" | "import" | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function run(dryRun: boolean) {
    if (!file) return;
    const format = formatFor(file);
    if (!format) {
      setError("Please choose a .csv or .xlsx file.");
      return;
    }
    setError(null);
    setBusy(dryRun ? "preview" : "import");
    try {
      const res = await api.uploadFile<ImportReport>(path, file, format, dryRun);
      setReport(res);
      if (!dryRun) {
        onImported();
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not process this file.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0, fontSize: "1rem" }}>{title}</h2>
        <div className={ui.toolbarSpacer} />
        <button type="button" className={ui.btnSecondary} onClick={() => setOpen((v) => !v)}>
          {open ? "Hide" : "Import from CSV/Excel"}
        </button>
      </div>

      {open ? (
        <div style={{ marginTop: 12 }}>
          <p className={ui.muted} style={{ marginBottom: 8 }}>
            Expected columns (first row is the header): <code>{columns.join(", ")}</code>
            {sampleRows && sampleRows.length > 0
              ? (() => {
                  const rows = sampleRows;
                  return (
                    <>
                      {" — "}
                      <button
                        type="button"
                        style={{ background: "none", border: "none", padding: 0, font: "inherit", color: "var(--color-accent)", textDecoration: "underline", cursor: "pointer" }}
                        onClick={() => downloadSampleCsv(`${path.split("/").pop()}-sample.csv`, Object.keys(rows[0] ?? {}), rows)}
                      >
                        download a sample CSV
                      </button>
                    </>
                  );
                })()
              : null}
          </p>
          <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            <input
              type="file"
              accept=".csv,.xlsx"
              aria-label={`${title} file`}
              onChange={(e) => {
                setFile(e.target.files?.[0] ?? null);
                setReport(null);
                setError(null);
              }}
            />
            <button type="button" className={ui.btnSecondary} disabled={!file || busy !== null} onClick={() => void run(true)}>
              {busy === "preview" ? "Checking…" : "Preview"}
            </button>
            {report && report.DryRun && report.Errors < report.Total ? (
              <button type="button" className={ui.btnPrimary} disabled={busy !== null} onClick={() => void run(false)}>
                {busy === "import" ? "Importing…" : `Import ${report.Total - report.Errors} row(s)`}
              </button>
            ) : null}
          </div>

          {error ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {error}
            </p>
          ) : null}

          {report ? (
            <div style={{ marginTop: 12 }}>
              <p>
                {report.DryRun ? "Preview: " : "Imported: "}
                <strong>{report.Committed}</strong> committed, <strong>{report.Valid}</strong> ready, <strong>{report.Duplicates}</strong> duplicate
                (skipped), <strong>{report.Errors}</strong> error{report.Errors === 1 ? "" : "s"} — {report.Total} row(s) total.
              </p>
              {(report.Results ?? []).length > 0 ? (
                <div className={ui.tableScroll}>
                  <table className={ui.table}>
                    <thead>
                      <tr>
                        <th scope="col">Row</th>
                        <th scope="col">Outcome</th>
                        <th scope="col">Message</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(report.Results ?? []).map((r, i) => (
                        <tr key={i}>
                          <td className="num">{r.RowNumber}</td>
                          <td>
                            <span
                              className={ui.badge}
                              data-tone={r.Outcome === "ERROR" ? "negative" : r.Outcome === "DUPLICATE" ? "warning" : "positive"}
                            >
                              {r.Outcome}
                            </span>
                          </td>
                          <td>{r.Message || "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
