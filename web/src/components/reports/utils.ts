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
