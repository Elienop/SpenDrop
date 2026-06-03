// Sizing for the Patterns → Tag Analysis horizontal bar chart.
//
// The chart draws one bar per tag, and the backend returns every distinct tag
// (no cap). A fixed height squeezes the bars to a few unreadable pixels each
// once a household has more than a handful of tags, so the chart is sized to the
// data: a comfortable per-tag row, plus room for the x-axis, with a floor so a
// one- or two-tag chart still looks intentional. Intentionally *uncapped* —
// readability (every bar full-height) is the goal the user asked for; a ceiling
// would re-introduce the squeeze. Household tag counts are small in practice.
//
// Lives in its own module (not PatternsTab.tsx) so the component file only
// exports a component — satisfying react-refresh/only-export-components — while
// the pure sizing function stays unit-testable. Values sit on the 4px grid.

export const TAG_ROW_PX = 36;
export const TAG_CHART_PADDING_PX = 48;
export const TAG_CHART_MIN_PX = 224;

export function tagChartHeightPx(tagCount: number): number {
  return Math.max(TAG_CHART_MIN_PX, tagCount * TAG_ROW_PX + TAG_CHART_PADDING_PX);
}
