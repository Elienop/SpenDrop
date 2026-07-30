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
 * It is deliberately far larger than any window `yearOptions` can ask for.
 * Both previous values were literals sized against a hard-coded year floor and
 * both were already scheduled to start truncating: 120 is exactly
 * `(2033 - 2024 + 1) * 12`, so it would have bound from 2034; 600 binds from
 * 2050 now that the floor is derived from the ledger and can reach `MIN_YEAR`.
 *
 * The backend derives its `MaxTrendMonths` as `(MaxYear - MinYear + 1) * 12`
 * — the widest window the picker can ever legitimately ask for, given
 * `/api/settings/report-year-floor` clamps the floor to `MinYear`. This is
 * that same number, `(MAX_YEAR - MIN_YEAR + 1) * 12`, and it is pinned to the
 * Go constant by TestMaxTrendMonths_MatchesFrontendMaxReportMonths, which
 * regex-reads the literal below — so keep it a literal, not an expression.
 */
export const MAX_REPORT_MONTHS = 1212;

/**
 * How many months of income/expense history must be fetched so that the
 * whole of `year` is covered, given the report window always ends at the
 * current month.
 *
 * SavingsTab used a hardcoded 24, but `yearOptions` offers every year from
 * HISTORICAL_YEAR_START. In mid-2026 a 24-month window began in August 2024,
 * so selecting 2024 filtered down to five months and rendered them as the
 * whole year: the cumulative savings curve started mid-year and goal progress
 * was measured against a partial total, with nothing on screen indicating the
 * data had been truncated.
 *
 * Floored at 24 to preserve the previous behaviour for the current year, and
 * capped at MAX_REPORT_MONTHS purely as a sanity bound — the cap must never be
 * reachable from the year Select, or it becomes the truncation bug again.
 */
export function monthsToCoverYear(year: number, currentYear: number): number {
  return Math.min(MAX_REPORT_MONTHS, Math.max(24, (currentYear - year + 1) * 12));
}
