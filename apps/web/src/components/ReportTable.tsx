import { apiUrl } from "../lib/api-client";
import { useReportTable, withFormat } from "../lib/useReportTable";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

/** Report cells arrive as plain strings. Pure numbers get locale grouping
 * (display only — nothing is recomputed) and right alignment; anything
 * else (names, SKUs, dates, codes) stays left-aligned in the normal font.
 * Identifier-like columns are never grouped, so SKU "5000" or HSN "7318"
 * are not turned into "5,000". */
const IDENTIFIER_HEADER = /sku|code|hsn|sac|number|no\.?$|id$|gstin|phone|pin|year/i;
const NUMERIC = /^-?\d+(\.\d+)?$/;
function formatCell(value: string, header: string): { text: string; numeric: boolean } {
  if (!NUMERIC.test(value) || IDENTIFIER_HEADER.test(header)) return { text: value, numeric: false };
  const decimals = value.includes(".") ? (value.split(".")[1] ?? "").length : 0;
  const n = Number(value);
  if (!Number.isFinite(n) || Math.abs(n) > Number.MAX_SAFE_INTEGER) return { text: value, numeric: true };
  return {
    text: n.toLocaleString(undefined, { minimumFractionDigits: decimals, maximumFractionDigits: decimals }),
    numeric: true,
  };
}

// One generic renderer for every report table in the app (Inventory,
// Purchases, Accounting, GST, Reports) — each screen just points this at
// a path instead of hand-building a table per report.
export function ReportTable({ path, emptyLabel }: { path: string; emptyLabel?: string }) {
  const query = useReportTable(path);

  if (query.isPending) {
    return <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />;
  }
  if (query.isError) {
    return (
      <p className={layout.errorState} role="alert">
        Couldn't load this report.
      </p>
    );
  }
  const headers = query.data.headers ?? [];
  const rows = query.data.rows ?? [];
  // A column is numeric when every non-empty cell is, so headers align with their figures.
  const numericCols = headers.map((h, j) => rows.length > 0 && rows.every((r) => !String(r[j] ?? "") || formatCell(String(r[j]), h).numeric));
  if (rows.length === 0) {
    return <p className={layout.emptyState}>{emptyLabel ?? "Nothing to show yet."}</p>;
  }
  return (
    <div>
      <div className={ui.exportRow}>
        <span className={ui.exportLabel}>Export:</span>
        <a className={ui.btnSecondary} href={apiUrl(withFormat(path, "csv"))}>
          CSV
        </a>
        <a className={ui.btnSecondary} href={apiUrl(withFormat(path, "xlsx"))}>
          Excel
        </a>
        <a className={ui.btnSecondary} href={apiUrl(withFormat(path, "pdf"))}>
          PDF
        </a>
      </div>
      <div className={ui.tableScroll} tabIndex={0}>
        <table className={ui.table}>
          <thead>
            <tr>
              {headers.map((h, i) => (
                <th key={i} scope="col" style={numericCols[i] ? { textAlign: "right" } : undefined}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => (
              <tr key={i}>
                {row.map((cell, j) => {
                  const f = formatCell(String(cell ?? ""), headers[j] ?? "");
                  return (
                    <td key={j} className={f.numeric ? "num" : undefined} style={f.numeric ? { textAlign: "right" } : undefined}>
                      {f.text}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
