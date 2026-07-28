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
 * capped at 120 to match the backend's MaxTrendMonths (values above it are
 * clamped server-side anyway).
 */
export function monthsToCoverYear(year: number, currentYear: number): number {
  return Math.min(120, Math.max(24, (currentYear - year + 1) * 12));
}
