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
