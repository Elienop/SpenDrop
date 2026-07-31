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

/** Upper bound on rendered x-axis tick labels, at any window width. */
export const MAX_AXIS_TICKS = 24;

/**
 * Recharts `interval` for an axis over `bucketCount` points: show every
 * (n+1)th tick so at most MAX_AXIS_TICKS labels render.
 *
 * This exists because BOTH fixed strategies are wrong at one end of the range:
 *
 *   `interval={0}`             every bucket gets a rotated <text> node. Free at
 *                              12; the dominant render cost of this tab at the
 *                              516 buckets an All-time window over a 1984
 *                              ledger produces.
 *   `interval="preserveStartEnd"`  Recharts pins the first AND last tick, then
 *                              thins between them. On a point scale (AreaChart)
 *                              the last tick sits hard against the right edge,
 *                              so its neighbour loses the minTickGap contest and
 *                              vanishes — measured: a 12-month Net Cash Flow
 *                              chart at 598px rendered 11 ticks, jumping
 *                              May'26 -> Jul'26. The June data was present and
 *                              hoverable; only its label was gone, which is the
 *                              worst version of the bug because nothing looks
 *                              broken. Its sibling BarChart kept all 12 at the
 *                              same width, because a band scale pads its edges.
 *
 * A derived numeric interval is deterministic: it does not depend on Recharts'
 * collision heuristic, on the scale type, or on the container width. Every
 * bucket shows through MAX_AXIS_TICKS, and beyond that the count stays bounded.
 * `interval={0}` is still what a small chart gets — as a computed result rather
 * than a hardcoded assumption that stops holding when the window widens.
 */
export function axisTickInterval(bucketCount: number, maxTicks = MAX_AXIS_TICKS): number {
  if (bucketCount <= maxTicks) return 0;
  return Math.ceil(bucketCount / maxTicks) - 1;
}
