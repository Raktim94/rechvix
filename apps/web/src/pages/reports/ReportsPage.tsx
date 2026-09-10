import ReactECharts from "echarts-for-react";
import { ReportTable } from "../../components/ReportTable";
import { formatMoney, moneyToApproxNumber } from "../../lib/money";
import { useOrgContext } from "../../lib/useOrgContext";
import { chartPalette } from "../../lib/chartColors";
import { useReportTable, withLegalEntity } from "../../lib/useReportTable";
import { useTheme } from "../../theme/ThemeProvider";
import layout from "../DashboardPage.module.css";

export function ReportsPage() {
  const { theme } = useTheme();
  const dark = theme === "dark";
  const org = useOrgContext();
  const legalEntityId = org.legalEntity?.ID;

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Reports</h1>
          <p className={layout.subtitle}>Sales, purchases, and stock movement — the last 30 days by default.</p>
        </div>
      </div>

      <div className={layout.panel}>
        <h2>Top products by profit</h2>
        <GrossProfitChart dark={dark} legalEntityId={legalEntityId} />
      </div>

      <div className={layout.panel}>
        <h2>Sales invoices</h2>
        <ReportTable path={withLegalEntity("/reports/sales/invoices?format=json", legalEntityId)} />
      </div>
      <div className={layout.panel}>
        <h2>Gross profit</h2>
        <ReportTable path={withLegalEntity("/reports/sales/gross-profit?format=json", legalEntityId)} />
      </div>

      <div className={layout.panel}>
        <h2>Purchases by day</h2>
        <PurchaseSummaryChart dark={dark} legalEntityId={legalEntityId} />
      </div>
      <div className={layout.panel}>
        <h2>Purchase summary</h2>
        <ReportTable path={withLegalEntity("/reports/purchases/summary?format=json", legalEntityId)} />
      </div>
      <div className={layout.panel}>
        <h2>Purchase documents</h2>
        <ReportTable path={withLegalEntity("/reports/purchases/documents?format=json", legalEntityId)} emptyLabel="No purchases recorded yet." />
      </div>
      <div className={layout.panel}>
        <h2>Stock movements</h2>
        <ReportTable path={withLegalEntity("/reports/inventory/movements?format=json", legalEntityId)} emptyLabel="No stock movements recorded yet." />
      </div>

      {/* Trial balance/receivables/payables also live on Accounting
          (alongside the chart of accounts they're derived from) — shown
          here too since "Reports" is where anyone actually goes looking
          for them; same ReportTable, same live data, just a second door
          into it rather than a duplicate implementation. NOT company-
          filtered (no withLegalEntity here) — journals/accounts have no
          cheap company join available yet, see
          reporting/pg.Repo.TrialBalance's own doc comment; these three
          stay organisation-wide on purpose. */}
      <div className={layout.panel}>
        <h2>Trial balance</h2>
        <ReportTable path="/reports/accounting/trial-balance?format=json" />
      </div>
      <div className={layout.panel}>
        <h2>Receivables (who owes you)</h2>
        <ReportTable path="/reports/accounting/receivables?format=json" emptyLabel="Nobody owes you anything right now." />
      </div>
      <div className={layout.panel}>
        <h2>Payables (what you owe)</h2>
        <ReportTable path="/reports/accounting/payables?format=json" emptyLabel="You don't owe any suppliers right now." />
      </div>
    </div>
  );
}

// Gross-profit rows: [Product, SKU, Qty Sold, Revenue, Approx COGS, Approx
// Profit] (internal/modules/reporting/httpapi/reports.go's grossProfit).
// Top 8 by profit, revenue vs. profit as two categorical bars per product
// — magnitude comparison across products, not a trend, so grouped bars
// beat a line here.
function GrossProfitChart({ dark, legalEntityId }: { dark: boolean; legalEntityId: string | undefined }) {
  const query = useReportTable(withLegalEntity("/reports/sales/gross-profit?format=json", legalEntityId));

  if (query.isPending) {
    return <div className={layout.skeleton} style={{ height: 280 }} aria-hidden="true" />;
  }
  if (query.isError) {
    return (
      <p className={layout.errorState} role="alert">
        Couldn't load gross profit.
      </p>
    );
  }
  const rows = query.data.rows ?? [];
  if (rows.length === 0) {
    return <p className={layout.emptyState}>No sales to compute gross profit from yet.</p>;
  }

  const top = [...rows]
    .sort((a, b) => Number(b[5] ?? 0) - Number(a[5] ?? 0))
    .slice(0, 8)
    .reverse(); // echarts category axis renders bottom-to-top; reverse so #1 lands on top

  const products = top.map((r) => r[0] ?? "");
  const revenue = top.map((r) => moneyToApproxNumber({ amount: r[3] ?? "0", currency: "INR" }));
  const profit = top.map((r) => moneyToApproxNumber({ amount: r[5] ?? "0", currency: "INR" }));

  const { accent: profitColor, text: revenueColor, grid: gridColor } = chartPalette(dark);
  const textColor = revenueColor;

  const option = {
    grid: { left: 140, right: 24, top: 8, bottom: 32 },
    legend: {
      data: ["Revenue", "Profit"],
      bottom: 0,
      textStyle: { color: textColor, fontFamily: "IBM Plex Sans" },
    },
    xAxis: {
      type: "value" as const,
      splitLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Mono" },
    },
    yAxis: {
      type: "category" as const,
      data: products,
      axisLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Sans" },
    },
    tooltip: { trigger: "axis" as const, axisPointer: { type: "shadow" as const } },
    series: [
      { name: "Revenue", type: "bar" as const, data: revenue, color: revenueColor, barGap: "10%" },
      { name: "Profit", type: "bar" as const, data: profit, color: profitColor },
    ],
  };

  return (
    <>
      <table className="srOnly">
        <caption>Top products by approximate profit</caption>
        <thead>
          <tr>
            <th scope="col">Product</th>
            <th scope="col">Revenue</th>
            <th scope="col">Approx profit</th>
          </tr>
        </thead>
        <tbody>
          {top.map((r, i) => (
            <tr key={r[1] ?? i}>
              <td>{r[0]}</td>
              <td>{formatMoney({ amount: r[3] ?? "0", currency: "INR" })}</td>
              <td>{formatMoney({ amount: r[5] ?? "0", currency: "INR" })}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div aria-hidden="true">
        <ReactECharts option={option} style={{ height: Math.max(280, top.length * 36 + 60) }} notMerge />
      </div>
    </>
  );
}

// Purchase-summary rows: [Key, Documents, Total], default group_by=day
// (same domain.GroupByDay default as the Dashboard's sales trend) —
// plotted as bars rather than the Dashboard's line so the two daily-trend
// charts in this app don't read as the same chart with different data.
function PurchaseSummaryChart({ dark, legalEntityId }: { dark: boolean; legalEntityId: string | undefined }) {
  const query = useReportTable(withLegalEntity("/reports/purchases/summary?format=json", legalEntityId));

  if (query.isPending) {
    return <div className={layout.skeleton} style={{ height: 260 }} aria-hidden="true" />;
  }
  if (query.isError) {
    return (
      <p className={layout.errorState} role="alert">
        Couldn't load the purchase summary.
      </p>
    );
  }
  const rows = query.data.rows ?? [];
  if (rows.length === 0) {
    return <p className={layout.emptyState}>No purchases recorded yet.</p>;
  }

  const keys = rows.map((r) => r[0] ?? "");
  const totals = rows.map((r) => moneyToApproxNumber({ amount: r[2] ?? "0", currency: "INR" }));

  const { warning: accent, text: textColor, grid: gridColor } = chartPalette(dark);

  const option = {
    grid: { left: 48, right: 16, top: 24, bottom: 32 },
    xAxis: {
      type: "category" as const,
      data: keys,
      axisLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Sans" },
    },
    yAxis: {
      type: "value" as const,
      splitLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Mono" },
    },
    tooltip: { trigger: "axis" as const, axisPointer: { type: "shadow" as const } },
    series: [
      {
        type: "bar" as const,
        data: totals,
        color: accent,
        itemStyle: { borderRadius: [4, 4, 0, 0] },
        barMaxWidth: 32,
      },
    ],
  };

  return (
    <>
      <table className="srOnly">
        <caption>Purchase totals, most recent {rows.length} period(s)</caption>
        <thead>
          <tr>
            <th scope="col">Period</th>
            <th scope="col">Documents</th>
            <th scope="col">Total</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r[0] ?? i}>
              <td>{r[0]}</td>
              <td>{r[1]}</td>
              <td>{formatMoney({ amount: r[2] ?? "0", currency: "INR" })}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div aria-hidden="true">
        <ReactECharts option={option} style={{ height: 260 }} notMerge />
      </div>
    </>
  );
}
