import { apiUrl } from "../lib/api-client";
import { useReportTable, withFormat } from "../lib/useReportTable";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

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
      <div className={ui.tableScroll}>
        <table className={ui.table}>
          <thead>
            <tr>
              {headers.map((h, i) => (
                <th key={i} scope="col">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => (
              <tr key={i}>
                {row.map((cell, j) => (
                  <td key={j} className={j > 0 ? "num" : undefined}>
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
