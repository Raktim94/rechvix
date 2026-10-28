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

/** Mirrors internal/platform/importer.Field / Inspection. */
interface ImportField {
  name: string;
  label: string;
  required: boolean;
}
interface Inspection {
  headers: string[];
  fields: ImportField[];
  suggested: Record<string, string>;
  sample: Record<string, string>[];
  total: number;
}

/** A mapping value is either a column header or "=constant". */
const CONSTANT = "=";
const NONE = "";

function formatFor(file: File): "csv" | "xlsx" | "tally" | null {
  const ext = file.name.toLowerCase().split(".").pop();
  if (ext === "csv") return "csv";
  if (ext === "xlsx") return "xlsx";
  if (ext === "xml") return "tally"; // Tally Prime / ERP 9 master export
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
  extraQuery,
  mapColumns = false,
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
  /** Extra query params sent on every Preview/Import request alongside
   * format/dry_run — e.g. products' warehouse_id, needed only when the
   * file has an opening_qty column (catalogue.Service.ImportProducts'
   * own doc comment). Re-read on every run() call (not captured once),
   * so a value the caller derives from context (like the current
   * warehouse) stays current across repeated Preview/Import clicks. */
  extraQuery?: Record<string, string>;
  /** Show the "match your columns" step. Needs a `${path}/inspect` endpoint
   * on the server (importer.Inspect); omit for imports that have none. */
  mapColumns?: boolean;
  onImported: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [busy, setBusy] = useState<"preview" | "import" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [inspection, setInspection] = useState<Inspection | null>(null);
  const [mapping, setMapping] = useState<Record<string, string>>({});

  async function chooseFile(f: File | null) {
    setFile(f);
    setReport(null);
    setError(null);
    setInspection(null);
    setMapping({});
    if (!f || !mapColumns) return;
    const format = formatFor(f);
    if (!format) {
      setError("Please choose a .csv, .xlsx or Tally .xml file.");
      return;
    }
    try {
      const ins = await api.uploadFile<Inspection>(`${path}/inspect`, f, format, true);
      setInspection(ins);
      setMapping(ins.suggested ?? {});
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not read this file.");
    }
  }

  const blocked = inspection ? inspection.fields.some((f) => f.required && (!mapping[f.name] || mapping[f.name] === CONSTANT)) : false;

  async function run(dryRun: boolean) {
    if (!file) return;
    const format = formatFor(file);
    if (!format) {
      setError("Please choose a .csv, .xlsx or Tally .xml file.");
      return;
    }
    setError(null);
    setBusy(dryRun ? "preview" : "import");
    try {
      const res = await api.uploadFile<ImportReport>(path, file, format, dryRun, {
        ...extraQuery,
        ...(inspection ? { mapping: JSON.stringify(mapping) } : {}),
      });
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
              accept=".csv,.xlsx,.xml"
              aria-label={`${title} file`}
              onChange={(e) => void chooseFile(e.target.files?.[0] ?? null)}
            />
            <button type="button" className={ui.btnSecondary} disabled={!file || busy !== null || blocked} onClick={() => void run(true)}>
              {busy === "preview" ? "Checking…" : "Preview"}
            </button>
            {report && report.DryRun && report.Errors < report.Total ? (
              <button type="button" className={ui.btnPrimary} disabled={busy !== null} onClick={() => void run(false)}>
                {busy === "import" ? "Importing…" : `Import ${report.Total - report.Errors} row(s)`}
              </button>
            ) : null}
          </div>

          {inspection ? (
            <fieldset style={{ marginTop: 12, border: "1px solid var(--color-border)", borderRadius: 8, padding: 12 }}>
              <legend>Match your columns ({inspection.total} row{inspection.total === 1 ? "" : "s"} found)</legend>
              <div className={ui.tableScroll} tabIndex={0}>
                <table className={ui.table}>
                  <thead>
                    <tr>
                      <th scope="col">Rechvix field</th>
                      <th scope="col">Your column</th>
                      <th scope="col">First row</th>
                    </tr>
                  </thead>
                  <tbody>
                    {inspection.fields.map((f) => {
                      const value = mapping[f.name] ?? NONE;
                      const isConstant = value.startsWith(CONSTANT);
                      const selectValue = isConstant ? CONSTANT : value;
                      return (
                        <tr key={f.name}>
                          <th scope="row">
                            <label htmlFor={`map-${f.name}`}>
                              {f.label}
                              {f.required ? <span aria-label="required"> *</span> : null}
                            </label>
                          </th>
                          <td>
                            <select
                              id={`map-${f.name}`}
                              className={ui.input}
                              value={selectValue}
                              onChange={(e) => {
                                const v = e.target.value;
                                setMapping((m) => ({ ...m, [f.name]: v }));
                                setReport(null);
                              }}
                            >
                              <option value={NONE}>{f.required ? "Choose a column…" : "— skip —"}</option>
                              {inspection.headers.map((h) => (
                                <option key={h} value={h}>
                                  {h}
                                </option>
                              ))}
                              <option value={CONSTANT}>Same value for every row…</option>
                            </select>
                            {isConstant ? (
                              <input
                                className={ui.input}
                                aria-label={`${f.label} value for every row`}
                                style={{ marginTop: 4 }}
                                value={value.slice(CONSTANT.length)}
                                onChange={(e) => {
                                  const v = CONSTANT + e.target.value;
                                  setMapping((m) => ({ ...m, [f.name]: v }));
                                  setReport(null);
                                }}
                              />
                            ) : null}
                          </td>
                          <td className={ui.muted}>{isConstant ? value.slice(CONSTANT.length) || "—" : (inspection.sample[0]?.[value] ?? "—")}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              {blocked ? (
                <p className={ui.muted} style={{ marginTop: 8 }}>
                  Choose a column (or a fixed value) for every field marked *.
                </p>
              ) : null}
            </fieldset>
          ) : null}

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
                <div className={ui.tableScroll} tabIndex={0}>
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
