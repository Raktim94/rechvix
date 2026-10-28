import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import ReactECharts from "echarts-for-react";
import styles from "./DashboardPage.module.css";
import { DashboardFollowUps, PaymentModesPanel } from "../components/DashboardFollowUps";
import { QuickAccess } from "../components/QuickAccess";
import { api } from "../lib/api-client";
import { toLocalIsoDate } from "../lib/calendarGrid";
import { chartPalette } from "../lib/chartColors";
import { buildSeries, formatPercent, isoDay, percentChange, sum, type DayPoint } from "../lib/dashboardMath";
import { currencySymbol, formatMoney, formatMoneyWhole, isZeroMoney, moneyToApproxNumber, type Money } from "../lib/money";
import { useOrgContext } from "../lib/useOrgContext";
import { withLegalEntity } from "../lib/useReportTable";
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
/** Who is in today and what is pending today. Shared by the "Needs attention" list and the Today panel; the query keys are the same, so it is fetched once. */
function useToday() {
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
  return {
    isPending: staff.isPending || attendance.isPending || tasks.isPending,
    activeStaff,
    absentToday: activeStaff.filter((m) => statusByStaffId.get(m.ID) === "ABSENT"),
    pendingTasks: (tasks.data ?? []).filter((t) => t.Status === "PENDING"),
  };
}

function TodaySummaryPanel() {
  const { t } = useTranslation();
  const { isPending, activeStaff, absentToday, pendingTasks } = useToday();

  if (isPending) {
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
      <h2>{t("dashboard.today")}</h2>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 24 }}>
        <div>
          <p className={styles.subtitle} style={{ marginBottom: 8 }}>
            {activeStaff.length > 0 ? t("dashboard.staffIn", { present: activeStaff.length - absentToday.length, total: activeStaff.length }) : t("dashboard.noStaff")}
          </p>
          {absentToday.length > 0 ? (
            <p style={{ color: "var(--color-negative)", fontSize: "var(--text-sm)" }}>{t("dashboard.absent", { names: absentToday.map((m) => m.Name).join(", ") })}</p>
          ) : null}
          <Link to="/calendar" style={{ fontSize: "var(--text-sm)" }}>
            {t("dashboard.markAttendance")}
          </Link>
        </div>
        <div>
          <p className={styles.subtitle} style={{ marginBottom: 8 }}>
            {pendingTasks.length > 0 ? t("dashboard.tasksDue", { count: pendingTasks.length }) : t("dashboard.nothingDue")}
          </p>
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: "var(--text-sm)" }}>
            {pendingTasks.slice(0, 4).map((t) => (
              <li key={t.ID}>{t.Title}</li>
            ))}
          </ul>
          {pendingTasks.length > 4 ? <p className={styles.subtitle}>{t("dashboard.moreTasks", { count: pendingTasks.length - 4 })}</p> : null}
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

// Not company-filtered — reporting.app.Service.Dashboard takes no Filter at all
// (unlike SalesSummary below), so a multi-company install's balances total every
// company together. The sales trend is company-filtered.
function useDashboard() {
  return useQuery({
    queryKey: ["dashboard"],
    queryFn: () => api.get<DashboardSummary>("/reports/dashboard"),
  });
}

/** Daily sales for the chosen period and the same-length period before it, in one request. */
function useSalesTrend(legalEntityId: string | undefined, days: number) {
  const end = new Date();
  const from = isoDay(new Date(end.getFullYear(), end.getMonth(), end.getDate() - (2 * days - 1)));
  const to = isoDay(end);
  return useQuery({
    queryKey: ["reports", "sales-summary", "day", legalEntityId, from, to],
    queryFn: () => api.get<ReportTable>(withLegalEntity(`/reports/sales/summary?group_by=day&format=json&from=${from}&to=${to}`, legalEntityId)),
  });
}

/** Open follow-ups from the CRM. Quiet on failure: not every role can see the CRM, and the overview must not break for them. */
function useCrmFollowUps() {
  return useQuery({
    queryKey: ["crm", "tasks"],
    queryFn: () => api.getListField<{ id: string; due_at?: string }>("/crm/tasks", "tasks"),
    select: (tasks) => ({ total: tasks.length, overdue: tasks.filter((t) => t.due_at && new Date(t.due_at).getTime() < Date.now()).length }),
    retry: false,
  });
}

const PERIODS = [7, 30, 90] as const;
type Period = (typeof PERIODS)[number];
const PERIOD_KEY = "rechvix:dashboard-period";

function readPeriod(): Period {
  try {
    const stored = Number(localStorage.getItem(PERIOD_KEY));
    return (PERIODS as readonly number[]).includes(stored) ? (stored as Period) : 30;
  } catch {
    return 30;
  }
}

function Delta({ change, vs }: { change: number | null; vs: string }) {
  if (change === null) return null;
  const tone = Math.abs(change) < 0.05 ? "flat" : change > 0 ? "up" : "down";
  return (
    <span className={styles.delta} data-tone={tone}>
      <span aria-hidden="true">{tone === "up" ? "▲" : tone === "down" ? "▼" : "•"}</span>
      {tone === "flat" ? "No change" : `${tone === "up" ? "Up" : "Down"} ${formatPercent(change)}`} {vs}
    </span>
  );
}

function Hero({ trend, days, currency, dark }: { trend: ReturnType<typeof useSalesTrend>; days: Period; currency: string; dark: boolean }) {
  const { t } = useTranslation();
  if (trend.isError) {
    return (
      <div className={styles.hero}>
        <div className={styles.errorState} role="alert">
          {t("dashboard.loadTrendFailed")}{" "}
          <button type="button" onClick={() => void trend.refetch()}>
            {t("common.retry", { defaultValue: "Retry" })}
          </button>
        </div>
      </div>
    );
  }
  if (trend.isPending) {
    return (
      <div className={styles.hero}>
        <div className={styles.skeleton} style={{ height: 340 }} aria-hidden="true" />
      </div>
    );
  }
  const totals = new Map<string, number>();
  for (const r of trend.data.rows ?? []) totals.set(r[0] ?? "", moneyToApproxNumber({ amount: r[4] ?? "0", currency }));
  const now = new Date();
  const all = buildSeries(totals, now, 2 * days);
  const previous = all.slice(0, days);
  const current = all.slice(days);
  const currentTotal = sum(current);
  const previousTotal = sum(previous);
  const change = percentChange(currentTotal, previousTotal);
  const money = (n: number) => formatMoneyWhole({ amount: String(n), currency });
  const insight =
    currentTotal === 0 && previousTotal === 0
      ? t("dashboard.noSalesYet")
      : change === null
        ? t("dashboard.insightFirst", { defaultValue: "Sales have started: nothing to compare with the previous {{days}} days yet.", days })
        : Math.abs(change) < 0.05
          ? t("dashboard.insightFlat", { defaultValue: "Sales are level with the previous {{days}} days.", days })
          : change > 0
            ? t("dashboard.insightUp", { defaultValue: "Sales are up {{pct}} on the previous {{days}} days.", pct: formatPercent(change), days })
            : t("dashboard.insightDown", { defaultValue: "Sales are down {{pct}} on the previous {{days}} days.", pct: formatPercent(change), days });

  return (
    <section className={styles.hero} aria-labelledby="hero-heading">
      <div>
        <span className={styles.eyebrow}>{t("dashboard.salesPeriod", { defaultValue: "Sales, last {{days}} days", days })}</span>
        <h2 id="hero-heading" className={styles.insight}>
          {insight}
        </h2>
      </div>
      <div className={styles.heroFigure}>
        <span className={styles.heroValue}>{money(currentTotal)}</span>
        <Delta change={change} vs={t("dashboard.vsPrevious", { defaultValue: "vs previous {{days}} days", days })} />
      </div>
      {currentTotal === 0 && previousTotal === 0 ? null : (
        <>
          <SalesTrendChart current={current} previous={previous} dark={dark} currency={currency} />
          <div className={styles.legend} aria-hidden="true">
            <span>{t("dashboard.thisPeriod", { defaultValue: "This period" })}</span>
            <span>{t("dashboard.previousPeriod", { defaultValue: "Previous period" })}</span>
          </div>
        </>
      )}
    </section>
  );
}

interface AttentionItem {
  key: string;
  to: string;
  tone: "warning" | "negative";
  title: string;
  detail?: string;
  value: string;
}

function Attention({ summary, overdueFollowUps }: { summary: DashboardSummary | undefined; overdueFollowUps: number }) {
  const { t } = useTranslation();
  const today = useToday();
  const items: AttentionItem[] = [];

  if (summary && !isZeroMoney(summary.OverdueReceivable)) {
    items.push({
      key: "overdue",
      to: "/reports",
      tone: "negative",
      title: t("dashboard.attOverdue", { defaultValue: "Customers are overdue" }),
      detail: t("dashboard.attOverdueHint", { defaultValue: "Chase these first" }),
      value: formatMoneyWhole(summary.OverdueReceivable),
    });
  }
  if (overdueFollowUps > 0) {
    items.push({
      key: "followups",
      to: "/crm",
      tone: "negative",
      title: t("dashboard.attFollowUps", { defaultValue: "CRM follow-ups are overdue" }),
      detail: t("dashboard.attFollowUpsHint", { defaultValue: "Leads waiting on a reply" }),
      value: String(overdueFollowUps),
    });
  }
  if (summary && summary.LowStockCount > 0) {
    items.push({
      key: "stock",
      to: "/inventory",
      tone: "warning",
      title: t("dashboard.attLowStock", { defaultValue: "Items are low on stock" }),
      detail: t("dashboard.attLowStockHint", { defaultValue: "Reorder before you run out" }),
      value: String(summary.LowStockCount),
    });
  }
  if (!today.isPending && today.absentToday.length > 0) {
    items.push({
      key: "absent",
      to: "/calendar",
      tone: "warning",
      title: t("dashboard.attAbsent", { defaultValue: "Staff absent today" }),
      detail: today.absentToday.map((m) => m.Name).join(", "),
      value: String(today.absentToday.length),
    });
  }
  if (!today.isPending && today.pendingTasks.length > 0) {
    items.push({
      key: "tasks",
      to: "/calendar",
      tone: "warning",
      title: t("dashboard.attTasks", { defaultValue: "Tasks due today" }),
      detail: today.pendingTasks.slice(0, 2).map((x) => x.Title).join(" · "),
      value: String(today.pendingTasks.length),
    });
  }

  return (
    <section className={styles.attention} aria-labelledby="attention-heading">
      <h2 id="attention-heading">{t("dashboard.attention", { defaultValue: "Needs your attention" })}</h2>
      {!summary ? (
        <div className={styles.skeleton} style={{ height: 200 }} aria-hidden="true" />
      ) : items.length === 0 ? (
        <p className={styles.allClear}>{t("dashboard.allClear", { defaultValue: "Nothing is overdue, low or waiting. Books and stock are quiet." })}</p>
      ) : (
        <ul className={styles.attentionList}>
          {items.map((i) => (
            <li key={i.key}>
              <Link to={i.to} className={styles.attentionItem}>
                <span className={styles.mark} data-tone={i.tone} aria-hidden="true" />
                <span className={styles.attentionText}>
                  {i.title}
                  {i.detail ? <small>{i.detail}</small> : null}
                </span>
                <span className={styles.attentionValue}>{i.value}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Strip({ summary, today }: { summary: DashboardSummary; today: { yesterday: number; sales: number } }) {
  const { t } = useTranslation();
  const currency = summary.TodaySales.currency;
  const receivable = moneyToApproxNumber(summary.OutstandingReceivable);
  const overdue = moneyToApproxNumber(summary.OverdueReceivable);
  const overduePct = receivable > 0 ? Math.min(100, Math.round((overdue / receivable) * 100)) : 0;
  const collected = moneyToApproxNumber(summary.TodayCollections);
  const sales = moneyToApproxNumber(summary.TodaySales);
  const vsYesterday = percentChange(sales, today.yesterday);

  return (
    <section className={styles.strip} aria-label={t("dashboard.balances", { defaultValue: "Today and balances" })}>
      <div className={styles.cell}>
        <span className={styles.eyebrow}>{t("dashboard.todaysSales")}</span>
        <span className={styles.cellValue}>{formatMoneyWhole(summary.TodaySales)}</span>
        <span className={styles.cellHint}>
          {vsYesterday === null
            ? t("dashboard.noYesterday", { defaultValue: "No sales yesterday to compare" })
            : `${vsYesterday >= 0 ? "▲" : "▼"} ${formatPercent(vsYesterday)} ${t("dashboard.vsYesterday", { defaultValue: "vs yesterday" })}`}
        </span>
      </div>
      <div className={styles.cell}>
        <span className={styles.eyebrow}>{t("dashboard.todaysCollections")}</span>
        <span className={styles.cellValue}>{formatMoneyWhole(summary.TodayCollections)}</span>
        <span className={styles.cellHint}>
          {sales > 0 ? t("dashboard.ofSales", { defaultValue: "{{pct}}% of today's sales", pct: Math.round((collected / sales) * 100) }) : t("dashboard.noSalesToday", { defaultValue: "No sales yet today" })}
        </span>
      </div>
      <div className={styles.cell}>
        <span className={styles.eyebrow}>{t("dashboard.toCollect", { defaultValue: "To collect" })}</span>
        <span className={styles.cellValue}>{formatMoneyWhole(summary.OutstandingReceivable)}</span>
        {overdue > 0 ? (
          <>
            <span className={styles.meter} aria-hidden="true">
              <span style={{ width: `${overduePct}%` }} />
            </span>
            <span className={styles.cellHint} data-tone="negative">
              {formatMoneyWhole(summary.OverdueReceivable)} {t("dashboard.overdue", { defaultValue: "overdue" })} ({overduePct}%)
            </span>
          </>
        ) : (
          <span className={styles.cellHint}>{receivable > 0 ? t("dashboard.nothingOverdue", { defaultValue: "Nothing overdue" }) : t("dashboard.nothingOwed", { defaultValue: "Nobody owes you" })}</span>
        )}
      </div>
      <div className={styles.cell}>
        <span className={styles.eyebrow}>{t("dashboard.toPay", { defaultValue: "To pay" })}</span>
        <span className={styles.cellValue}>{formatMoneyWhole(summary.OutstandingPayable)}</span>
        <span className={styles.cellHint}>
          {t("dashboard.purchasesToday", { defaultValue: "Bought today: {{amount}}", amount: formatMoneyWhole({ amount: summary.TodayPurchases.amount, currency }) })}
        </span>
      </div>
      <div className={styles.cell}>
        <span className={styles.eyebrow}>{t("dashboard.currentStockValue")}</span>
        <span className={styles.cellValue}>{formatMoneyWhole(summary.CurrentStockValue)}</span>
        <span className={styles.cellHint} data-tone={summary.LowStockCount > 0 ? "negative" : undefined}>
          {summary.LowStockCount > 0 ? t("dashboard.lowStockCount", { defaultValue: "{{count}} low on stock", count: summary.LowStockCount }) : t("dashboard.stockHealthy", { defaultValue: "Nothing low on stock" })}
        </span>
      </div>
    </section>
  );
}

export function DashboardPage() {
  const { t } = useTranslation();
  const org = useOrgContext();
  const { theme } = useTheme();
  const [days, setDaysState] = useState<Period>(readPeriod);
  const dashboard = useDashboard();
  const trend = useSalesTrend(org.legalEntity?.ID, days);
  const followUps = useCrmFollowUps();
  const currency = org.legalEntity?.BaseCurrencyCode || org.organisation?.DefaultCurrencyCode || "INR";

  const setDays = (d: Period) => {
    setDaysState(d);
    try {
      localStorage.setItem(PERIOD_KEY, String(d));
    } catch {
      // Remembering the period is a convenience; the page works without it.
    }
  };

  const yesterday = (() => {
    const key = isoDay(new Date(new Date().getFullYear(), new Date().getMonth(), new Date().getDate() - 1));
    const row = trend.data?.rows?.find((r) => r[0] === key);
    return row ? moneyToApproxNumber({ amount: row[4] ?? "0", currency }) : 0;
  })();
  const refreshing = dashboard.isFetching || trend.isFetching;
  const updated = dashboard.dataUpdatedAt ? new Date(dashboard.dataUpdatedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : null;

  return (
    <div className={styles.page}>
      <div className={styles.heading}>
        <div>
          <h1>{t("dashboard.title")}</h1>
          <p className={styles.subtitle}>
            {org.legalEntity?.LegalName ? `${org.legalEntity.LegalName} · ` : ""}
            {t("dashboard.subtitle")}
          </p>
        </div>
        <div className={styles.headTools}>
          <div className={styles.segmented} role="group" aria-label={t("dashboard.period", { defaultValue: "Period" })}>
            {PERIODS.map((p) => (
              <button key={p} type="button" aria-pressed={days === p} onClick={() => setDays(p)}>
                {t("dashboard.daysShort", { defaultValue: "{{count}} days", count: p })}
              </button>
            ))}
          </div>
          <button
            type="button"
            className={styles.refresh}
            disabled={refreshing}
            onClick={() => {
              void dashboard.refetch();
              void trend.refetch();
            }}
          >
            {refreshing ? t("dashboard.refreshing", { defaultValue: "Refreshing…" }) : t("dashboard.refresh", { defaultValue: "Refresh" })}
          </button>
        </div>
      </div>

      <QuickAccess />

      {dashboard.isError ? (
        <div className={styles.errorState} role="alert">
          {t("dashboard.loadSummaryFailed")}{" "}
          <button type="button" onClick={() => void dashboard.refetch()}>
            {t("common.retry", { defaultValue: "Retry" })}
          </button>
        </div>
      ) : null}

      <div className={styles.heroRow}>
        <Hero trend={trend} days={days} currency={currency} dark={theme === "dark"} />
        <Attention summary={dashboard.data} overdueFollowUps={followUps.data?.overdue ?? 0} />
      </div>

      {dashboard.data ? (
        <Strip summary={dashboard.data} today={{ yesterday, sales: moneyToApproxNumber(dashboard.data.TodaySales) }} />
      ) : dashboard.isPending ? (
        <div className={styles.skeleton} style={{ height: 112 }} aria-hidden="true" />
      ) : null}

      <div className={styles.lowerRow}>
        <div className={styles.panel}>
          <h2>{t("dashboard.receivableVsPayable")}</h2>
          {dashboard.isError ? (
            <div className={styles.errorState} role="alert">
              {t("dashboard.loadBalancesFailed")}
            </div>
          ) : dashboard.isPending ? (
            <div className={styles.skeleton} style={{ height: 200 }} aria-hidden="true" />
          ) : isZeroMoney(dashboard.data.OutstandingReceivable) && isZeroMoney(dashboard.data.OutstandingPayable) ? (
            <p className={styles.emptyState}>{t("dashboard.nothingOutstanding")}</p>
          ) : (
            <ReceivablePayableChart receivable={dashboard.data.OutstandingReceivable} payable={dashboard.data.OutstandingPayable} dark={theme === "dark"} />
          )}
        </div>
        <TodaySummaryPanel />
      </div>

      <PaymentModesPanel days={days} legalEntityId={org.legalEntity?.ID} currency={currency} />

      <DashboardFollowUps legalEntityId={org.legalEntity?.ID} />

      {updated ? (
        <p className={styles.freshness}>
          {t("dashboard.asOf", { defaultValue: "Balances as of {{time}}. Sales are by invoice date, in {{currency}}.", time: updated, currency })}
        </p>
      ) : null}
    </div>
  );
}

function SalesTrendChart({ current, previous, dark, currency }: { current: DayPoint[]; previous: DayPoint[]; dark: boolean; currency: string }) {
  const { accent, muted, text: textColor, grid: gridColor } = chartPalette(dark);

  // echarts-for-react renders to a bare <canvas> with no text alternative
  // (WCAG 1.1.1). This table carries the same day/total data as real markup;
  // the chart is hidden from assistive tech so the two are not both announced.
  const accessibleTable = (
    <table className="srOnly">
      <caption>Daily sales total, most recent {current.length} day(s)</caption>
      <thead>
        <tr>
          <th scope="col">Date</th>
          <th scope="col">Total</th>
        </tr>
      </thead>
      <tbody>
        {current.map((p) => (
          <tr key={p.date}>
            <td>{p.date}</td>
            <td>{formatMoney({ amount: String(p.total), currency })}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );

  const option = {
    grid: { left: 8, right: 8, top: 12, bottom: 24, containLabel: true },
    xAxis: {
      type: "category" as const,
      data: current.map((p) => p.date.slice(5)),
      boundaryGap: false,
      axisLine: { lineStyle: { color: gridColor } },
      axisTick: { show: false },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Sans", hideOverlap: true },
    },
    yAxis: {
      type: "value" as const,
      splitNumber: 3,
      splitLine: { lineStyle: { color: gridColor, opacity: 0.6 } },
      axisLabel: {
        color: textColor,
        fontFamily: "IBM Plex Mono",
        formatter: (v: number) => new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(v),
      },
    },
    tooltip: {
      trigger: "axis" as const,
      valueFormatter: (v: number) => formatMoney({ amount: String(v), currency }),
    },
    series: [
      {
        name: "Previous period",
        type: "line" as const,
        data: previous.map((p) => p.total),
        color: muted,
        symbol: "none",
        lineStyle: { width: 1.5, type: "dashed" as const },
      },
      {
        name: "This period",
        type: "line" as const,
        data: current.map((p) => p.total),
        color: accent,
        symbol: "none",
        lineStyle: { width: 2.5 },
        areaStyle: { opacity: 0.1, color: accent },
      },
    ],
  };

  return (
    <>
      {accessibleTable}
      <div aria-hidden="true">
        <ReactECharts option={option} style={{ height: 240 }} notMerge />
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
  const { accent: receivableColor, warning: payableColor, text: textColor, grid: gridColor } = chartPalette(dark);

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

  // Compact figures ("2.5L", "98K") keep both the bar labels and the axis
  // readable on a 375px screen; the exact amounts are in the table above
  // for assistive tech and in the tooltip.
  const compact = (v: number) => new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(v);
  const option = {
    grid: { left: 8, right: 8, top: 16, bottom: 8, containLabel: true },
    xAxis: {
      type: "value" as const,
      splitNumber: 3,
      boundaryGap: [0, "30%"] as [number, string], // room for the value label beside the longest bar
      splitLine: { lineStyle: { color: gridColor } },
      axisLabel: { color: textColor, fontFamily: "IBM Plex Mono", hideOverlap: true, formatter: compact },
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
          formatter: (p: { value: number }) => `${currencySymbol(receivable.currency)}${compact(p.value)}`,
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
