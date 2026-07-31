// Calendar primitives: year bounds, month labels, and date format
// strings. Anything the UI needs to render or parse a date lives here.

// --- Planning year bounds ---
// Mirror `PlanningMinYear` / `PlanningMaxYear` in `internal/api/limits.go`.
// They bound the year PARAM of the planning endpoints — budgets, category
// budgets, and savings goals — and nothing else.
//
// These are NOT the bounds on ledger data. Go splits the two windows
// deliberately: `MinDataYear` / `MaxDataYear` ([1900, 2100]) bound what a
// transaction DATE may be and what every reports / dashboard / export year
// param accepts, while the planning window is narrower because those rows are
// configuration the household writes forward, not history it imports backward
// — nothing plans a 1990 budget, and a century of empty years in the Budgets
// Select buys nothing.
//
// So do not reach for these to bound anything that reads ledger data. The
// Reports and Dashboard year pickers are driven by the ledger itself, via
// `GET /api/reports/years` (`useReportYears`) — not by a constant.
//
// Renamed from `MIN_YEAR` / `MAX_YEAR` (values unchanged) when the Go
// constants they used to mirror, `MinYear` / `MaxYear`, were split and
// deleted. The old names read as a product-wide floor, which is exactly the
// misreading that put a planning bound on the Reports picker.

export const PLANNING_MIN_YEAR = 2000;
export const PLANNING_MAX_YEAR = 2100;

// --- Data year bounds ---
// Mirror `MinDataYear` / `MaxDataYear` in `internal/api/limits.go`. This is
// the window a transaction DATE may occupy, and the same window every
// reports / dashboard / export year param accepts — one bound, so every year
// the ledger can hold is a year the reports can display.
//
// The Reports year picker does NOT count down from `MIN_DATA_YEAR`: it offers
// only the years the ledger actually has (`useReportYears`). These constants
// are the OUTER bound on what that response can contain, which is what makes
// them useful — they name the range a row has to fall outside of to land in
// `out_of_range_years`, and they size the worst case the report window must
// still be able to reach.

export const MIN_DATA_YEAR = 1900;
export const MAX_DATA_YEAR = 2100;

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

/** One `<Select>` option: a year rendered as both value and label. */
export interface YearOption {
  value: string;
  label: string;
}

/**
 * Returns a CONTIGUOUS list of year options `{ value, label }`, descending
 * from the current year down to `startYear`.
 *
 * This is the PLANNING range, and Budgets legitimately wants it: a household
 * plans a budget for a year it has no transactions in yet, so offering only
 * years that already hold data would make next year unselectable.
 *
 * Reports and Dashboard must NOT use this — they read the ledger, which is
 * sparse, and a contiguous range there offers dozens of empty years. Use
 * `yearOptionsFrom` with `useReportYears` instead.
 *
 * This stays a PURE function: it must not fetch, so `@/lib/dates` keeps
 * no network dependency. The flip side is that it returns an EMPTY list
 * for a `startYear` above the current year, so callers must pass a floor
 * that has already been guarded.
 */
export function yearOptions(startYear: number): YearOption[] {
  const currentYear = new Date().getFullYear();
  const opts: YearOption[] = [];
  for (let y = currentYear; y >= startYear; y--) {
    opts.push({ value: String(y), label: String(y) });
  }
  return opts;
}

/**
 * Returns a SPARSE list of year options `{ value, label }` — exactly the
 * `years` given, deduplicated and sorted newest-first, with every `selected`
 * year unioned in.
 *
 * Pair with `useReportYears`, which sources `years` from the ledger via
 * `GET /api/reports/years`. A ledger is not contiguous: a household with rows
 * in 1984 and 2026 and nothing between gets two options here, where
 * `yearOptions(1984)` would have produced forty-three.
 *
 * WHY `selected` IS VARIADIC AND WHY IT IS NOT OPTIONAL POLISH. A Radix
 * `<Select>` holding a `value` with no matching `<SelectItem>` renders a
 * BLANK TRIGGER — silently, with no error and no console warning. The years
 * list is refetched (SSE invalidates it on every transaction mutation), so it
 * can NARROW under a live selection: delete the last 1984 row and 1984 leaves
 * the response while the Select still holds "1984". Unioning the caller's
 * current selection is what stops that, and it is the direct replacement for
 * the `Math.min(floorYear, year)` guard the contiguous version needed.
 *
 * Variadic because one list can drive several Selects — PatternsTab feeds both
 * its heatmap `year` and its tag `tagYear` from a single call, and a
 * single-selection union would blank whichever one it left out.
 */
export function yearOptionsFrom(
  years: number[],
  ...selected: number[]
): YearOption[] {
  const unique = Array.from(new Set([...years, ...selected]));
  unique.sort((a, b) => b - a);
  return unique.map((y) => ({ value: String(y), label: String(y) }));
}
