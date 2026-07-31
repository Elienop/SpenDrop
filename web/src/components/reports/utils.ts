// Reports-section helpers. Most calendar / currency primitives now live
// in `@/lib/dates` and `@/lib/format`; this file only keeps the chart
// configs that are specific to the Reports page.

import type { ChartConfig } from '@/components/ui/chart';

/**
 * Shared chart config for any stacked "income vs expenses" bar/area
 * chart on the Reports page. Keeping it here — instead of duplicating
 * the colour tokens in each tab — lets the tabs stay visually aligned.
 */
export const INCEXP_CONFIG = {
  income: { label: 'Income', color: 'hsl(var(--primary))' },
  expenses: { label: 'Expenses', color: 'hsl(var(--primary) / 0.35)' },
} satisfies ChartConfig;

/**
 * Upper bound on the report window, mirroring the backend's `MaxTrendMonths`
 * (internal/api/limits.go). The two MUST stay in step: the server clamps
 * `?months=` to its own limit, so a larger value here would be silently
 * truncated server-side and reintroduce the truncation this module exists to
 * prevent.
 *
 * There is now ZERO slack. The backend derives its `MaxTrendMonths` as
 * `(MaxDataYear - MinDataYear + 1) * 12` — the span of the window a
 * transaction DATE may occupy, which is exactly the widest window the picker
 * can ever legitimately ask for. At `[1900, 2100]` this value is that window,
 * not a comfortable margin above it, so widening the backend's data bounds
 * MUST update this number in the same change or the oldest years start
 * truncating silently again.
 *
 * Both earlier values were literals sized against a hard-coded year floor and
 * both were already scheduled to start truncating: 120 is exactly
 * `(2033 - 2024 + 1) * 12`, so it would have bound from 2034; 600 from 2050;
 * 1212 was `(2100 - 2000 + 1) * 12`, sized against the narrower planning
 * window rather than the data window.
 *
 * Pinned to the Go constant by TestMaxTrendMonths_MatchesFrontendMaxReportMonths,
 * which regex-reads the literal below — so keep it a literal, not an expression.
 */
export const MAX_REPORT_MONTHS = 2412;

/**
 * How many months of income/expense history must be fetched so that the
 * whole of `year` is covered, given the report window always ends at the
 * current month.
 *
 * SavingsTab used a hardcoded 24, but the year Select offers every year the
 * ledger holds (`useReportYears`, bounded below by MIN_DATA_YEAR — it was a
 * hard-coded 2024 when this was written). In mid-2026 a 24-month window began
 * in August 2024, so selecting 2024 filtered down to five months and rendered
 * them as the whole year: the cumulative savings curve started mid-year and
 * goal progress was measured against a partial total, with nothing on screen
 * indicating the data had been truncated.
 *
 * Floored at 24 to preserve the previous behaviour for the current year, and
 * capped at MAX_REPORT_MONTHS purely as a sanity bound — the cap must never be
 * reachable from the year Select, or it becomes the truncation bug again.
 */
export function monthsToCoverYear(year: number, currentYear: number): number {
  return Math.min(MAX_REPORT_MONTHS, Math.max(24, (currentYear - year + 1) * 12));
}
