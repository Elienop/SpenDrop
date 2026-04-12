// Calendar primitives: year bounds, month labels, and date format
// strings. Anything the UI needs to render or parse a date lives here.

// --- Year bounds ---
// Mirror `MinYear` / `MaxYear` in `internal/api/limits.go`. Keep wide
// enough for historic spreadsheet imports; keep narrow enough that the
// year-picker does not need pagination.

export const MIN_YEAR = 2000;
export const MAX_YEAR = 2100;

/**
 * Historical cutoff used by year-picker helpers that don't need the
 * full span — e.g. the reports page only wants "recent years the user
 * has data for". Keep this a rolling value rather than hard-coded.
 */
export const HISTORICAL_YEAR_START = 2024;

// --- Month labels ---

/** Short month names, indexed 0-11. */
export const MONTH_NAMES_SHORT = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
] as const;

/** Full month names, indexed 0-11. */
export const MONTH_NAMES_FULL = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
] as const;

// --- Date-fns format strings ---

/** ISO date format, used whenever the backend expects `YYYY-MM-DD`. */
export const FORMAT_ISO_DATE = 'yyyy-MM-dd';
/** Human-readable range label like "Jan 3". */
export const FORMAT_DATE_SHORT = 'MMM d';
/** Full human-readable date like "January 3, 2026". */
export const FORMAT_DATE_LONG = 'PPP';

// --- Chart month-axis formatters ---
//
// Shared between every Recharts time-series chart so all of them render
// an unambiguous `Jun'25`-style X-axis without local copies of the Intl
// plumbing. Consumers are expected to store an ISO-8601 "YYYY-MM-01"
// date in their chart data — matches shadcn's `chart-bar-interactive`
// convention so `new Date(value)` parses reliably.

/** Month-name part of the tick formatter — Intl handles locale/ICU
 *  detail so we don't hardcode a `["Jan", "Feb", ...]` array. We avoid
 *  the `{ year: '2-digit' }` option because in `en-US` it renders as
 *  `"Jan 26"` (plain space), which is ambiguous with a day-of-month
 *  reading (`Jan 26` = January 26th). We compose the `'YY` suffix
 *  ourselves. `timeZone: 'UTC'` pins parsing so a user in a negative
 *  UTC offset doesn't see `"Dec '25"` for a January bucket. */
const MONTH_SHORT_FMT = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  timeZone: 'UTC',
});

/** Full month + year for tooltip headers. `"2026-01-01"` → `"January 2026"`. */
const MONTH_LONG_FMT = new Intl.DateTimeFormat('en-US', {
  month: 'long',
  year: 'numeric',
  timeZone: 'UTC',
});

/** `"2026-01-01"` → `"Jan'26"`. Compact, unambiguous, one token wide. */
export function formatMonthTick(value: string): string {
  const d = new Date(value);
  const year2 = String(d.getUTCFullYear()).slice(-2);
  return `${MONTH_SHORT_FMT.format(d)}'${year2}`;
}

/** `"2026-01-01"` → `"January 2026"` for tooltip headers. Typed as
 *  `unknown` because Recharts' `labelFormatter` passes in whatever the
 *  dataKey resolves to, and TS has no runtime guarantee that it's a
 *  string. */
export function formatMonthLabel(value: unknown): string {
  if (typeof value !== 'string') return '';
  return MONTH_LONG_FMT.format(new Date(value));
}

// --- Helpers ---

/**
 * Returns a `YYYY-MM-DD` string for the given date in the local
 * timezone. Matches the format the backend expects on
 * `date` columns (transactions, budgets, …).
 *
 * We deliberately avoid `date.toISOString()` because that converts
 * to UTC and would shift to the previous day for any user west of
 * GMT before noon local time.
 */
export function formatYYYYMMDD(date: Date): string {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}

/**
 * Returns a list of year options `{ value, label }` suitable for a
 * `<Select>`, descending from the current year down to `startYear`.
 */
export function yearOptions(
  startYear: number = HISTORICAL_YEAR_START,
): { value: string; label: string }[] {
  const currentYear = new Date().getFullYear();
  const opts: { value: string; label: string }[] = [];
  for (let y = currentYear; y >= startYear; y--) {
    opts.push({ value: String(y), label: String(y) });
  }
  return opts;
}
