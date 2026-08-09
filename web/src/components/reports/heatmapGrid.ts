// Pure geometry and scale for the spending heatmap.
//
// Extracted from the component so the two things a rebuild silently gets
// wrong — the COLUMN COUNT and the INTENSITY BUCKETING — can be driven
// directly by a table-driven test instead of only through a rendered grid
// that happy-dom cannot lay out.

import { format as formatDate, parseISO } from 'date-fns';
import { FORMAT_DATE_FULL, MONTH_NAMES_SHORT } from '@/lib/dates';

/** Rows in the desktop year grid, columns in the phone month grid. */
export const WEEKDAY_COUNT = 7;

/**
 * Percentile → opacity stops for the primary color. Shared by
 * `buildIntensityScale` (cell rendering) and `HeatmapLegend` (scale
 * swatches) so the rendered scale can never drift.
 */
export const OPACITY_STOPS = [0.3, 0.5, 0.75, 1] as const;

/**
 * Chronological cells for a whole year, Monday-first, padded at both ends so
 * the array length is always a multiple of 7.
 *
 * Verified correct across all 201 years in [1900, 2100] and six timezones —
 * every operation is UTC-constructed and UTC-read, so `toISOString` cannot
 * shift a day. Do NOT "fix" the Monday conversion, the UTC handling or the
 * pad loops.
 *
 * The output length is legitimately VARIABLE: 371 cells (53 columns) for 194
 * of those years, and 378 cells (54 columns) for the seven leap years that
 * begin on a Sunday — 1928, 1956, 1984, 2012, 2040, 2068 and 2096. Consumers
 * must derive their column count from `cells.length / WEEKDAY_COUNT`; the
 * previous hardcoded `repeat(53, 1fr)` left Dec 31 in an implicit 54th track
 * that escaped the grid box.
 */
export function generateYearDates(year: number): (string | null)[] {
  const cells: (string | null)[] = [];
  const jan1 = new Date(Date.UTC(year, 0, 1));
  const startDow = (jan1.getUTCDay() + 6) % 7; // Monday = 0

  // Pad empty cells for days before Jan 1
  for (let i = 0; i < startDow; i++) cells.push(null);

  // Fill all days of the year (UTC to avoid timezone offset issues)
  const d = new Date(Date.UTC(year, 0, 1));
  while (d.getUTCFullYear() === year) {
    const iso = d.toISOString().slice(0, 10);
    cells.push(iso);
    d.setUTCDate(d.getUTCDate() + 1);
  }

  // Pad end to fill final column (total cells = multiple of 7)
  while (cells.length % WEEKDAY_COUNT !== 0) cells.push(null);
  return cells;
}

/**
 * Chronological cells for ONE month, same Monday-first padding contract as
 * `generateYearDates`. `month` is 0-indexed, matching `Date`.
 *
 * Built the same UTC way for the same reason: the ISO strings are keys into
 * the heatmap payload, and a local-time construction would shift every one of
 * them by a day for any user west of GMT.
 */
export function generateMonthDates(
  year: number,
  month: number,
): (string | null)[] {
  const cells: (string | null)[] = [];
  const first = new Date(Date.UTC(year, month, 1));
  const startDow = (first.getUTCDay() + 6) % 7; // Monday = 0

  for (let i = 0; i < startDow; i++) cells.push(null);

  const d = new Date(Date.UTC(year, month, 1));
  while (d.getUTCFullYear() === year && d.getUTCMonth() === month) {
    cells.push(d.toISOString().slice(0, 10));
    d.setUTCDate(d.getUTCDate() + 1);
  }

  while (cells.length % WEEKDAY_COUNT !== 0) cells.push(null);
  return cells;
}

/**
 * Desktop layout: 7 rows (Mon…Sun) × N columns (weeks), GitHub-style.
 *
 * This replaces the old `gridAutoFlow: 'column'` CSS trick, which mapped a
 * flat chronological DOM list onto `row = i % 7`. That worked visually but
 * left `role="grid"` unreachable — a grid needs real row elements — and it
 * could be deleted without a single test failing while every weekday row
 * silently became wrong. Here the transposition is in the data, so the rows
 * ARE the weekdays and nothing about the CSS can move them.
 */
export function toWeekdayRows(cells: (string | null)[]): (string | null)[][] {
  const columns = cells.length / WEEKDAY_COUNT;
  const rows: (string | null)[][] = [];
  for (let r = 0; r < WEEKDAY_COUNT; r++) {
    const row: (string | null)[] = [];
    for (let c = 0; c < columns; c++) row.push(cells[c * WEEKDAY_COUNT + r]);
    rows.push(row);
  }
  return rows;
}

/** Phone layout: N rows (weeks) × 7 columns (Mon…Sun) — an ordinary calendar. */
export function toWeekRows(cells: (string | null)[]): (string | null)[][] {
  const rows: (string | null)[][] = [];
  for (let i = 0; i < cells.length; i += WEEKDAY_COUNT) {
    rows.push(cells.slice(i, i + WEEKDAY_COUNT));
  }
  return rows;
}

/**
 * One entry per column of the desktop year grid: the short month name where
 * that month's 1st falls, `null` everywhere else.
 *
 * Every month gets a DISTINCT starting column in every year of [1900, 2100],
 * and consecutive month starts are never closer than 4 columns, so a flat
 * 12-label row is always placeable — no "skip the crowded month" logic
 * needed. The label is approximate by construction: it sits over a column
 * whose first day can be up to six days earlier than the 1st, which is why
 * the label row is `aria-hidden` and every cell carries its own full date.
 */
export function monthLabelColumns(
  year: number,
  cells: (string | null)[],
): (string | null)[] {
  const columns = cells.length / WEEKDAY_COUNT;
  const labels: (string | null)[] = new Array<string | null>(columns).fill(null);
  const positions = new Map<string, number>();
  cells.forEach((cell, i) => {
    if (cell !== null) positions.set(cell, i);
  });
  MONTH_NAMES_SHORT.forEach((name, m) => {
    const iso = `${year}-${String(m + 1).padStart(2, '0')}-01`;
    const i = positions.get(iso);
    if (i === undefined) return;
    labels[Math.floor(i / WEEKDAY_COUNT)] = name;
  });
  return labels;
}

/**
 * Maps a day's total onto one of `OPACITY_STOPS` by its RANK among the
 * spending days, not by comparing it against percentile VALUES.
 *
 * The value-comparison version this replaces had a real defect. It computed
 * `pct(p) = sorted[floor(n * p)]`, so for n ≤ 4 spending days `p75` WAS the
 * maximum, and the darkest stop was only returned for `total > p75` —
 * strictly. A year with a single spending day therefore painted that day, the
 * largest spend in the set, at the PALEST stop. Ranking makes the top stop
 * reachable by construction: the largest total always has rank n, q = 1, and
 * lands in the last bucket for every n ≥ 1.
 *
 * ZERO-SPEND DAYS ARE ABSENT FROM `totals`, AND THAT IS LOAD-BEARING. The
 * backend's `SumExpensesByDay` groups by date over rows that exist, so a day
 * with no expense produces no row and never reaches this function (the cell
 * renderer takes its `bg-muted` branch first). If anyone ever "fixes" the
 * endpoint to emit all 365 days, three quarters of the ranks collapse onto
 * zero-totalled days and every real spending day jumps to the darkest stop.
 *
 * Ties share a rank — `rank` is "how many days total this much or less" — so
 * a year where every day spent the same amount paints uniformly rather than
 * inventing a gradient that is not in the data.
 */
export function buildIntensityScale(
  totals: number[],
): (total: number) => number {
  const sorted = [...totals].sort((a, b) => a - b);
  const n = sorted.length;
  const rank = new Map<number, number>();
  // Last write wins, so `rank.get(v)` is the count of days totalling <= v.
  sorted.forEach((value, i) => rank.set(value, i + 1));

  const top = OPACITY_STOPS[OPACITY_STOPS.length - 1];
  return (total: number): number => {
    if (n === 0) return top;
    const q = (rank.get(total) ?? n) / n;
    const bucket = Math.ceil(q * OPACITY_STOPS.length) - 1;
    return OPACITY_STOPS[Math.min(OPACITY_STOPS.length - 1, Math.max(0, bucket))];
  };
}

/**
 * The largest spending day among the cells CURRENTLY RENDERED, or null when
 * none of them has any spending.
 *
 * WHY THIS EXISTS AT ALL: the intensity scale buckets by RANK, so the darkest
 * stop is by construction the top quartile of the spending days — a quarter of
 * them, not one of them. No amount of looking at the grid can answer "which
 * day was heaviest", and on a busy month the darkest stop can hold a dozen
 * cells. This is the only thing that answers it, which is why the caller
 * renders it as ordinary text rather than as a screen-reader-only aside.
 *
 * TIES: the EARLIEST day wins, because `dates` arrives chronologically from
 * `generateYearDates` / `generateMonthDates` and the first strict maximum is
 * kept. That is a decision, not an accident of sorting — so the count of other
 * days sharing the maximum is returned alongside it and the caller says so
 * rather than presenting one arbitrary winner as unique.
 *
 * Days with no rows are skipped rather than compared: `lookup` has no entry
 * for them, and a grid with no spending at all must produce null so the caller
 * can render nothing instead of "Heaviest day: … $0.00".
 */
export function heaviestDay(
  dates: (string | null)[],
  lookup: Map<string, number>,
): { date: string; total: number; tiedWith: number } | null {
  let date: string | null = null;
  let total = 0;
  let tiedWith = 0;
  for (const candidate of dates) {
    if (candidate === null) continue;
    const value = lookup.get(candidate) ?? 0;
    // INTENT AND FAST PATH, not a load-bearing branch — say so rather than let
    // the next reader assume it is guarding something. `date` is only assigned
    // where `value > total` and `total` starts at 0, so a zero can never win
    // and a `tiedWith` it bumped is discarded with the null return. Verified by
    // exhausting all 120 orderings of [0, 0, 3, 3, 7]: relaxing this to
    // `value < 0` changes no result, which is why a mutation of it survives.
    if (value <= 0) continue;
    if (value > total) {
      date = candidate;
      total = value;
      tiedWith = 0;
    } else if (value === total) {
      tiedWith += 1;
    }
  }
  return date === null ? null : { date, total, tiedWith };
}

/**
 * Whether a cell should be shown as NOT YET HAPPENED rather than as a zero.
 *
 * `total` is part of the question, not just the date. A future date normally
 * has no expense rows for the same reason a quiet day has none, so it arrives
 * as `total === 0` — but a household CAN enter a future-dated expense (the
 * ledger accepts any date up to `MaxDataYear`), and a future day that someone
 * has deliberately recorded spending on must paint and announce that spending,
 * not be blanked as "upcoming". So the two are ordered: recorded data wins.
 *
 * ISO `YYYY-MM-DD` strings compare correctly with `>`, and both sides come
 * from the same generator, so no parsing is involved.
 *
 * Exported so the LABEL and the CELL PAINT cannot disagree: a future day that
 * announced "upcoming" while painting the same grey as a real zero, or the
 * reverse, would be worse than either alone.
 */
export function isUpcoming(
  dateStr: string,
  todayIso: string,
  total: number,
): boolean {
  return total === 0 && dateStr > todayIso;
}

/**
 * The cell's accessible name — date AND amount, on the cell itself.
 *
 * NOT in the tooltip. Radix's tooltip cannot open on touch (its only
 * pointer-open path returns immediately for `pointerType === 'touch'`, and a
 * tap's `pointerdown` suppresses the focus-open behind it), so a value that
 * lives only in a tooltip is unreadable on this household's primary platform.
 * Putting it here makes the announcement identical for mouse, keyboard and
 * screen reader, and independent of any overlay existing at all.
 *
 * "no spending" rather than "$0.00 spent": the payload omits days with no
 * expense rows entirely, so a zero cell means NO DATA, not a day that
 * genuinely totalled zero.
 *
 * `total > 0` IS a safe stand-in for "this day has rows" only because amounts
 * cannot be zero or negative: `CHECK(amount_cents > 0)` on the transactions
 * table (migration 010) plus the handler's own "amount must be positive". If
 * signed amounts ever land — the refunds-offsetting-a-category question — a
 * day of refunds could sum to <= 0 and would then be announced as "no
 * spending" and skipped by the sheet's fetch gate, which reads the same
 * predicate. Both would need to move to a has-rows flag on the wire.
 *
 * AND "upcoming" rather than either, for a day that has not happened. A
 * future date has no expense rows for exactly the same reason a quiet day
 * has none, so it arrives here as `total === 0` and would otherwise assert
 * "no spending" — a claim about the future, not a report about the past. On
 * the phone this is not an edge case: the grid opens on the CURRENT month, so
 * for most of any month most of the first thing the household sees would be
 * that claim.
 */
export function heatmapCellLabel(
  dateStr: string,
  total: number,
  format: (amount: number) => string,
  todayIso: string,
): string {
  const day = formatDate(parseISO(dateStr), FORMAT_DATE_FULL);
  if (isUpcoming(dateStr, todayIso, total)) return `${day}: upcoming`;
  const prefix = dateStr === todayIso ? 'Today, ' : '';
  return total > 0
    ? `${prefix}${day}: ${format(total)} spent`
    : `${prefix}${day}: no spending`;
}
