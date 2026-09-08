import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import ReactECharts from "echarts-for-react";
import styles from "./DashboardPage.module.css";
import { AlertTriangleIcon, ArrowDownCircleIcon, ArrowUpCircleIcon, BoxIcon, ClockIcon, SalesIcon, WalletIcon } from "../components/icons";
import { QuickAccess } from "../components/QuickAccess";
import { StatCard } from "../components/StatCard";
import { api } from "../lib/api-client";
import { toLocalIsoDate } from "../lib/calendarGrid";
import { formatMoney, isZeroMoney, moneyToApproxNumber, type Money } from "../lib/money";
import { useTheme } from "../theme/ThemeProvider";

interface StaffMember {
  ID: string;
  Name: string;
  IsActive: boolean;
}
interface AttendanceRecord {
  StaffMemberID: string;
  Status: "PRESENT" | "ABSENT" | "LEAVE";
}
interface Task {
  ID: string;
  Title: string;
  Status: "PENDING" | "DONE";
}

/** "What to do" today, plus who's in — the brief's own ask for a daily
 * summary that covers both tasks and staff. Kept as its own panel
 * rather than folded into the financial stat-card grid above, since
 * "today's numbers" and "today's people/to-dos" are different questions
 * a shop owner asks at different moments of the day. */
function TodaySummaryPanel() {
  const todayIso = toLocalIsoDate(new Date());
  const staff = useQuery({
    queryKey: ["staff-members"],
    queryFn: () => api.getListField<StaffMember>("/staff/members", "staff_members"),
  });
  const attendance = useQuery({
    queryKey: ["staff-attendance", todayIso, todayIso],
    queryFn: () => api.getListField<AttendanceRecord>(`/staff/attendance?from=${todayIso}&to=${todayIso}`, "attendance"),
  });
  const tasks = useQuery({
    queryKey: ["staff-tasks", todayIso, todayIso],
    queryFn: () => api.getListField<Task>(`/staff/tasks?from=${todayIso}&to=${todayIso}`, "tasks"),
  });

  const activeStaff = (staff.data ?? []).filter((m) => m.IsActive);
  const statusByStaffId = new Map((attendance.data ?? []).map((a) => [a.StaffMemberID, a.Status]));
  const absentToday = activeStaff.filter((m) => statusByStaffId.get(m.ID) === "ABSENT");
  const pendingTasks = (tasks.data ?? []).filter((t) => t.Status === "PENDING");

  if (staff.isPending || attendance.isPending || tasks.isPending) {
    return (
      <div className={styles.panel}>
        <div className={styles.skeleton} style={{ height: 120 }} aria-hidden="true" />
      </div>
    );
  }
  if (activeStaff.length === 0 && pendingTasks.length === 0) {
    return null;
  }

  return (
    <div className={styles.panel}>
      <h2>Today</h2>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 24 }}>
        <div>
          <p className={styles.subtitle} style={{ marginBottom: 8 }}>
            {activeStaff.length > 0 ? `${activeStaff.length - absentToday.length} of ${activeStaff.length} staff in today` : "No staff added yet"}
          </p>
          {absentToday.length > 0 ? (
            <p style={{ color: "var(--color-negative)", fontSize: "var(--text-sm)" }}>Absent: {absentToday.map((m) => m.Name).join(", ")}</p>
          ) : null}
          <Link to="/calendar" style={{ fontSize: "var(--text-sm)" }}>
            Mark attendance →
          </Link>
        </div>
        <div>
          <p className={styles.subtitle} style={{ marginBottom: 8 }}>
            {pendingTasks.length > 0 ? `${pendingTasks.length} task${pendingTasks.length > 1 ? "s" : ""} due today` : "Nothing due today"}
          </p>
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: "var(--text-sm)" }}>
            {pendingTasks.slice(0, 4).map((t) => (
              <li key={t.ID}>{t.Title}</li>
            ))}
          </ul>
          {pendingTasks.length > 4 ? <p className={styles.subtitle}>+{pendingTasks.length - 4} more</p> : null}
        </div>
      </div>
    </div>
  );
}

/** Mirrors internal/modules/reporting/domain.DashboardSummary exactly —
 * that struct has no json tags, so field names serialize verbatim as Go's
 * exported names (checked against internal/modules/reporting/domain/domain.go). */
interface DashboardSummary {
  TodaySales: Money;
  TodayCollections: Money;
  TodayPurchases: Money;
  OutstandingReceivable: Money;
  OutstandingPayable: Money;
  CurrentStockValue: Money;
  LowStockCount: number;
  OverdueReceivable: Money;
}

/** internal/modules/reporting/httpapi's export.Table shape for
 * GET /reports/sales/summary?format=json&group_by=day — rows are
 * pre-stringified [Key, DocumentCount, Taxable, Tax, GrandTotal]. */
interface ReportTable {
  title: string;
  headers: string[];
  rows: string[][];
}

function useDashboard() {
  return useQuery({
    queryKey: ["dashboard"],
    queryFn: () => api.get<DashboardSummary>("/reports/dashboard"),
  });
}

function useSalesTrend() {
  return useQuery({
    queryKey: ["reports", "sales-summary", "day"],
    queryFn: () => api.get<ReportTable>("/reports/sales/summary?group_by=day&format=json"),
  });
}

export function DashboardPage() {
  const dashboard = useDashboard();
  const trend = useSalesTrend();
  const { theme } = useTheme();

  return (
    <div className={styles.page}>
      <div className={styles.heading}>
        <div>
          <h1>Dashboard</h1>
          <p className={styles.subtitle}>Today's business, at a glance.</p>
        </div>
      </div>

      <QuickAccess />

      {dashboard.isError ? (
        <div className={styles.errorState} role="alert">
          Couldn't load today's summary. Check your connection and try again.
        </div>
      ) : dashboard.isPending ? (
        <div className={styles.cardGrid}>
          {Array.from({ length: 8 }, (_, i) => (
            <div key={i} className={styles.skeleton} aria-hidden="true" />
          ))}
        </div>
      ) : (
        <div className={styles.cardGrid}>
          <StatCard label="Today's sales" value={formatMoney(dashboard.data.TodaySales)} icon={<SalesIcon />} />
          <StatCard
            label="Today's collections"
            value={formatMoney(dashboard.data.TodayCollections)}
            polarity={isZeroMoney(dashboard.data.TodayCollections) ? "neutral" : "positive"}
            icon={<WalletIcon />}
          />
          <StatCard label="Today's purchases" value={formatMoney(dashboard.data.TodayPurchases)} icon={<ArrowUpCircleIcon />} />
          <StatCard
            label="Outstanding receivable"
            value={formatMoney(dashboard.data.OutstandingReceivable)}
            polarity={isZeroMoney(dashboard.data.OutstandingReceivable) ? "neutral" : "warning"}
            icon={<ArrowDownCircleIcon />}
          />
          <StatCard
            label="Outstanding payable"
            value={formatMoney(dashboard.data.OutstandingPayable)}
            polarity={isZeroMoney(dashboard.data.OutstandingPayable) ? "neutral" : "warning"}
            icon={<ArrowUpCircleIcon />}
          />
          <StatCard label="Current stock value" value={formatMoney(dashboard.data.CurrentStockValue)} icon={<BoxIcon />} />
          <StatCard
            label="Low stock items"
            value={String(dashboard.data.LowStockCount)}
            polarity={dashboard.data.LowStockCount > 0 ? "warning" : "neutral"}
            icon={<AlertTriangleIcon />}
          />
          <StatCard
            label="Overdue receivable"
            value={formatMoney(dashboard.data.OverdueReceivable)}
            polarity={isZeroMoney(dashboard.data.OverdueReceivable) ? "neutral" : "negative"}
            icon={<ClockIcon />}
          />
        </div>
      )}

      <div className={styles.panelRow}>
        <div className={styles.panel}>
          <h2>Sales trend</h2>
          {trend.isError ? (
            <div className={styles.errorState} role="alert">
              Couldn't load the sales trend.
            </div>
          ) : trend.isPending ? (
            <div className={styles.skeleton} style={{ height: 260 }} aria-hidden="true" />
          ) : (trend.data.rows ?? []).length === 0 ? (
            <p className={styles.emptyState}>
              No sales recorded yet. Once you create and finalize an invoice, its trend will show up here.
            </p>
          ) : (
            <SalesTrendChart rows={trend.data.rows ?? []} dark={theme === "dark"} />
          )}
        </div>

        <div className={styles.panel}>
          <h2>Receivable vs payable</h2>
          {dashboard.isError ? (
            <div className={styles.errorState} role="alert">
              Couldn't load outstanding balances.
            </div>
          ) : dashboard.isPending ? (
            <div className={styles.skeleton} style={{ height: 260 }} aria-hidden="true" />
          ) : isZeroMoney(dashboard.data.OutstandingReceivable) && isZeroMoney(dashboard.data.OutstandingPayable) ? (
            <p className={styles.emptyState}>Nothing outstanding on either side yet.</p>
          ) : (
            <ReceivablePayableChart
              receivable={dashboard.data.OutstandingReceivable}
              payable={dashboard.data.OutstandingPayable}
              dark={theme === "dark"}
            />
          )}
        </div>
      </div>

      <TodaySummaryPanel />
    </div>
  );
}

function SalesTrendChart({ rows, dark }: { rows: string[][]; dark: boolean }) {
  const accent = dark ? "#29c191" : "#0f6e5c";
  const textColor = dark ? "#9db0a4" : "#5b6b62";
  const gridColor = dark ? "#2b3632" : "#dbdfd8";

  const days = rows.map((r) => r[0] ?? "");
  const totals = rows.map((r) => moneyToApproxNumber({ amount: r[4] ?? "0", currency: "INR" }));

  // echarts-for-react renders to a bare <canvas> with no text alternative
  // — a screen-reader user gets nothing from it (WCAG 1.1.1). This table
  // carries the same day/total data as real, readable markup; the chart
  // itself is hidden from assistive tech below so the two aren't both
  // announced.
  const accessibleTable = (
    <table className="srOnly">
      <caption>Daily sales total, most recent {rows.length} day(s)</caption>
      <thead>
        <tr>
          <th scope="col">Date</th>
          <th scope="col">Total</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={r[0] ?? i}>
            <td>{r[0]}</td>
            <td>{formatMoney({ amount: r[4] ?? "0", currency: "INR" })}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );

  const option = {
    grid: { left: 48, right: 16, top: 24, bottom: 32 },
    xAxis: {
      type: "category" as const,
      data: days,
      axisLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Sans" },
    },
    yAxis: {
      type: "value" as const,
      splitLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Mono" },
    },
    tooltip: { trigger: "axis" as const },
    series: [
      {
        type: "line" as const,
        data: totals,
        color: accent,
        smooth: false,
        symbolSize: 6,
        lineStyle: { width: 2 },
        areaStyle: { opacity: 0.08, color: accent },
      },
    ],
  };

  return (
    <>
      {accessibleTable}
      <div aria-hidden="true">
        <ReactECharts option={option} style={{ height: 260 }} notMerge />
      </div>
    </>
  );
}

// Two magnitudes, compared — a horizontal bar, not a donut (two-slice pies
// force the reader to compare angles; bars compare on one shared, labeled
// axis instead). Colors are categorical (which side of the ledger this is),
// not polarity: outstanding-anything is a "watch this" state either way,
// same read as the two StatCards above using the same "warning" tone.
function ReceivablePayableChart({ receivable, payable, dark }: { receivable: Money; payable: Money; dark: boolean }) {
  const receivableColor = dark ? "#29c191" : "#0f6e5c";
  const payableColor = dark ? "#e0b355" : "#906409";
  const textColor = dark ? "#9db0a4" : "#5b6b62";
  const gridColor = dark ? "#2b3632" : "#dbdfd8";

  const receivableValue = moneyToApproxNumber(receivable);
  const payableValue = moneyToApproxNumber(payable);

  const accessibleTable = (
    <table className="srOnly">
      <caption>Outstanding receivable vs. payable</caption>
      <thead>
        <tr>
          <th scope="col">Type</th>
          <th scope="col">Amount</th>
        </tr>
      </thead>
      <tbody>
        <tr>
          <td>Receivable</td>
          <td>{formatMoney(receivable)}</td>
        </tr>
        <tr>
          <td>Payable</td>
          <td>{formatMoney(payable)}</td>
        </tr>
      </tbody>
    </table>
  );

  const option = {
    grid: { left: 84, right: 48, top: 16, bottom: 16 },
    xAxis: {
      type: "value" as const,
      splitLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Mono" },
    },
    yAxis: {
      type: "category" as const,
      data: ["Payable", "Receivable"],
      axisLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Sans" },
    },
    tooltip: { trigger: "item" as const },
    series: [
      {
        type: "bar" as const,
        data: [
          { value: payableValue, itemStyle: { color: payableColor, borderRadius: [0, 4, 4, 0] } },
          { value: receivableValue, itemStyle: { color: receivableColor, borderRadius: [0, 4, 4, 0] } },
        ],
        barWidth: 22,
        label: {
          show: true,
          position: "right" as const,
          color: textColor,
          fontFamily: "IBM Plex Mono",
          formatter: (p: { value: number }) => formatMoney({ amount: String(p.value), currency: "INR" }),
        },
      },
    ],
  };

  return (
    <>
      {accessibleTable}
      <div aria-hidden="true">
        <ReactECharts option={option} style={{ height: 260 }} notMerge />
      </div>
    </>
  );
}
