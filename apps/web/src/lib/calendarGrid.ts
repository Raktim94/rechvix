/** Local-date formatting throughout (never toISOString(), which
 * converts to UTC and can shift the calendar date by one for any
 * timezone west of UTC) — a day cell's own iso string must always match
 * what a human looking at their own clock would call "today". */
export function toLocalIsoDate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

export interface CalendarCell {
  date: Date;
  iso: string;
  inMonth: boolean;
}

/** A 6-week-max grid (padded with the adjacent months' trailing/leading
 * days, like every familiar calendar app) for the given month. */
export function monthGrid(year: number, month: number): CalendarCell[] {
  const first = new Date(year, month, 1);
  const startOffset = first.getDay();
  const daysInMonth = new Date(year, month + 1, 0).getDate();

  const cells: CalendarCell[] = [];
  for (let i = startOffset - 1; i >= 0; i--) {
    const d = new Date(year, month, -i);
    cells.push({ date: d, iso: toLocalIsoDate(d), inMonth: false });
  }
  for (let day = 1; day <= daysInMonth; day++) {
    const d = new Date(year, month, day);
    cells.push({ date: d, iso: toLocalIsoDate(d), inMonth: true });
  }
  while (cells.length % 7 !== 0) {
    const last = cells[cells.length - 1]?.date ?? first;
    const d = new Date(last.getFullYear(), last.getMonth(), last.getDate() + 1);
    cells.push({ date: d, iso: toLocalIsoDate(d), inMonth: false });
  }
  return cells;
}
