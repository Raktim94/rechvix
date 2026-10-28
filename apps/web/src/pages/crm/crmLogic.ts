import type { Opp, Stage } from "./crm";

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

/** "Today", "Tomorrow", "Overdue · 3 Oct": the one phrase a follow-up needs, and whether it is late. */
export function whenLabel(iso: string | undefined | null): { label: string; late: boolean } {
  if (!iso) return { label: "", late: false };
  const date = new Date(iso.length === 10 ? `${iso}T00:00:00` : iso);
  if (Number.isNaN(date.getTime())) return { label: "", late: false };
  const today = startOfDay(new Date());
  const diffDays = Math.round((startOfDay(date) - today) / 86_400_000);
  if (diffDays === 0) return { label: "Today", late: false };
  if (diffDays === 1) return { label: "Tomorrow", late: false };
  const text = date.toLocaleDateString(undefined, { day: "numeric", month: "short" });
  return diffDays < 0 ? { label: `Overdue · ${text}`, late: true } : { label: text, late: false };
}


export type Band = "HOT" | "WARM" | "COLD";
export interface ScoreFactor { label: string; points: number; max: number }
export interface LeadScore { score: number; band: Band; factors: ScoreFactor[] }

/** Open stages in pipeline order; later stages are closer to a sale. */
function openIndex(o: Opp, stages: Stage[]): { i: number; n: number } {
  const open = stages.filter((s) => s.kind === "OPEN").sort((a, b) => a.sort_order - b.sort_order);
  return { i: Math.max(0, open.findIndex((s) => s.id === o.stage_id)), n: open.length };
}

/**
 * A transparent 0-100 score from six facts anyone can read off the lead, so a
 * rep can see why a lead is HOT and what is missing: reachable (phone, email),
 * a value, a realistic close date, a follow-up already booked, and how far
 * along the pipeline it has moved. Closed deals are not scored.
 */
export function leadScore(o: Opp, stages: Stage[]): LeadScore | null {
  if (o.stage_kind !== "OPEN") return null;
  const { i, n } = openIndex(o, stages);
  const closeLate = whenLabel(o.expected_close).late;
  const factors: ScoreFactor[] = [
    { label: "Has a phone number", points: o.contact_phone ? 20 : 0, max: 20 },
    { label: "Has an email", points: o.contact_email ? 10 : 0, max: 10 },
    { label: "Has a deal value", points: Number(o.value) > 0 ? 20 : 0, max: 20 },
    { label: "Close date is still ahead", points: o.expected_close && !closeLate ? 15 : 0, max: 15 },
    { label: "A follow-up is booked", points: o.open_tasks > 0 ? 15 : 0, max: 15 },
    { label: "Moved along the pipeline", points: n > 1 ? Math.round((i / (n - 1)) * 20) : 0, max: 20 },
  ];
  const score = factors.reduce((t, f) => t + f.points, 0);
  return { score, band: score >= 70 ? "HOT" : score >= 40 ? "WARM" : "COLD", factors };
}

/** Chance a deal in this stage is won, for the weighted forecast: 10% in the first open stage rising to 80% in the last. */
export function stageProbability(o: Opp, stages: Stage[]): number {
  if (o.stage_kind === "WON") return 1;
  if (o.stage_kind === "LOST") return 0;
  const { i, n } = openIndex(o, stages);
  return n > 1 ? 0.1 + 0.7 * (i / (n - 1)) : 0.5;
}

export const LOSS_REASONS = ["Price too high", "No response", "Chose a competitor", "Not interested", "Wrong number or contact", "Bought elsewhere", "Timing not right", "Other"];

/** Spreadsheet apps run a cell starting with = + - @ as a formula; a leading apostrophe makes it plain text. */
export function csvCell(v: string | number | undefined | null): string {
  let t = v === undefined || v === null ? "" : String(v);
  if (/^[=+\-@\t\r]/.test(t)) t = "'" + t;
  return /[",\n\r]/.test(t) ? `"${t.replace(/"/g, '""')}"` : t;
}

export function leadsToCsv(opps: Opp[], stages: Stage[], owners: Map<string, string>): string {
  const stageName = new Map(stages.map((s) => [s.id, s.name]));
  const head = ["Title", "Company", "Contact", "Phone", "Email", "Value", "Currency", "Stage", "Owner", "Source", "Expected close", "Open follow-ups", "Score", "Notes"];
  const lines = opps.map((o) => {
    const sc = leadScore(o, stages);
    return [o.title, o.customer_name || o.company_name, o.contact_name, o.contact_phone, o.contact_email, o.value, o.currency, stageName.get(o.stage_id), (o.owner_user_id && owners.get(o.owner_user_id)) || "", o.source, o.expected_close, o.open_tasks, sc ? `${sc.score} ${sc.band}` : "", o.notes].map(csvCell).join(",");
  });
  return [head.join(","), ...lines].join("\r\n");
}

export function downloadCsv(name: string, csv: string) {
  const url = URL.createObjectURL(new Blob(["\ufeff" + csv], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}
