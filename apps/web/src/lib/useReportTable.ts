import { useQuery } from "@tanstack/react-query";
import { api } from "./api-client";

/** internal/platform/export.Table — every /reports/* endpoint's shape
 * (format=json, the default) — title/headers/rows, rows pre-stringified. */
export interface ReportTableData {
  title: string;
  headers: string[];
  rows: string[][];
}

// Shared by components/ReportTable (the generic table renderer) and any
// chart that visualizes the same report data (e.g. ReportsPage's
// GrossProfitChart/PurchaseSummaryChart) — same queryKey everywhere, so
// TanStack Query dedupes the network request instead of a table and its
// chart each fetching the same path separately. Kept out of
// components/ReportTable.tsx so that file exports only the component
// (react-refresh/only-export-components).
export function useReportTable(path: string) {
  return useQuery({
    queryKey: ["report-table", path],
    queryFn: () => api.get<ReportTableData>(path),
  });
}

/** Every /reports/* endpoint accepts the same `format=csv|xlsx|pdf|json`
 * query param (see internal/modules/reporting/httpapi/handlers.go's
 * writeTable) — this swaps just that param on an existing report path
 * (which may already carry from/to/group_by/etc.) without disturbing the
 * rest of the query string, for building an export download link. */
export function withFormat(path: string, format: "csv" | "xlsx" | "pdf"): string {
  const [base, query = ""] = path.split("?");
  const params = new URLSearchParams(query);
  params.set("format", format);
  return `${base}?${params.toString()}`;
}
