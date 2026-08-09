// Chart helpers for the Reports page — and, since the Dashboard's Cash Flow
// chart is the same rotated-month-axis shape as this page's, for that one too:
// `Dashboard.tsx` imports `ROTATED_MONTH_TICK_PADDING` from here. The header
// used to say "specific to the Reports page", which was already untrue of
// `MAX_REPORT_MONTHS` and is now untrue of a cross-page constant.
//
// The honest end state is a `src/lib/chart-axis.ts` alongside the existing
// `src/lib/chart-colors.ts`, holding the axis half and leaving the report-window
// half (`MAX_REPORT_MONTHS`, `monthsToCoverYear`) here. That is a rename across
// six files and is deliberately NOT bundled into this batch.
//
// EVERY PIXEL NUMBER IN THIS FILE IS ~15px PESSIMISTIC, and so is every one
// this project has recorded from a resized desktop browser. They were taken in
// headless Chrome at an emulated viewport, where a scrolling container's
// `overflow-x: auto` forces `overflow-y` to compute to `auto` too and Chrome
// reserves a classic-scrollbar gutter — the page's content box is ~15px
// narrower than the same viewport on a phone, which has overlay scrollbars and
// no gutter. So a chart measuring 263px at an emulated 360px viewport is ~278px
// on the owner's S24. Sizing against the pessimistic figure is deliberate;
// quoting it as the on-device width is not. The fuller version of this, with
// the two conflicting measurements that make it concrete, is in the tab-strip
// note in `pages/Reports.tsx`.
//
// Most calendar / currency primitives live in `@/lib/dates` and `@/lib/format`.

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

/**
 * THE MONTH-AXIS TICK RULE. One geometry, shared by every chart that labels
 * months, because they are all solving the same two inequalities.
 *
 * Rotated labels are parallel strips: two of them clear each other only when
 * `spacing × sin(angle) ≥ inkHeight`. Recharts also clamps nothing, so a
 * rotated end-anchored label hangs off its tick in both directions and paints
 * outside the SVG unless the axis reserves that gutter as `padding`.
 *
 * Measured in Chrome at 12px, the size these axes used to be — `May'26` is the
 * widest label at 39.9px, its INK is 11px tall (not the 16px `getBBox()`
 * reports, which is the em box), and a flat 3-letter month is 23.7px. Both
 * terms scale with the font, which is what makes the font the strongest lever:
 * a smaller one shrinks the spacing the labels NEED and the gutter they
 * overhang into, at the same time.
 *
 * WHY 9px AND -45°, and what it replaced. At -30°/12px the labels needed 22px
 * of spacing and overhung 36.3px to the left, so the axes carried
 * `{left: 40, right: 26}` — 66px of a 263px chart at a 360px viewport, a
 * quarter of the plot, squeezing the bars into the middle while Budget vs
 * Actual next to them used its full width. Available spacing at 360, zero
 * padding: 21.1px on the charts with no YAxis, 14.4–15.7px on the ones that
 * spend 80px on a currency axis. Against that:
 *
 *   -30°, 12px   needs 22.0px   overhangs L 36.3  R  8.0   fits nothing
 *   -30°, 10px   needs 18.3px   overhangs L 30.2  R  6.7   fails the YAxis charts
 *   -45°, 10px   needs 13.0px   overhangs L 24.9  R  9.4   Year-over-Year by 0.6px
 *   -45°,  9px   needs 11.7px   overhangs L 22.4  R  8.5   clears every chart
 *   flat,  9px   needs 17.8px   overhangs +/-8.9           fits the wide plots
 *
 * READ THE `R` COLUMN WITH ITS CAVEAT: it is the label's real INK overhang, and
 * it is NOT what sizes the right gutter. That is set by recharts' own footprint
 * check on the last tick — ~14.8px, roughly twice the ink — and the number in
 * this table is why an earlier revision used `right: 10` and collapsed the
 * whole end-anchored strategy to a single label. See `monthAxisInterval`.
 *
 * 10px was built and measured first and is NOT enough: Year-over-Year's twelve
 * bands sat 13.6px apart and ten-point labels need 13.0px, which is inside the
 * noise. The margins 9px actually ships with are measured and listed on
 * `monthAxisInterval` — do not restate them here, or the two drift. The gutter
 * drops from 66px to 36px, so the plot goes from 187px to 217px of the same
 * 253px, which is the point.
 * Steepening rather than shallowing: `sin` is in the denominator, so a steeper
 * angle needs LESS spacing, and it also lays more of the label's width down the
 * vertical rather than the horizontal, which shrinks the left overhang.
 */
export const MONTH_TICK_ANGLE = -45;

/**
 * Tick font for every month axis. A number rather than a Tailwind class because
 * recharts needs it as an SVG attribute to lay the tick out; a class would style
 * the text after the geometry had already been computed from the inherited 12px.
 */
export const MONTH_TICK = { fontSize: 9 } as const;

/**
 * XAxis `padding` for a rotated, end-anchored month axis with no `<YAxis>` to
 * lend it a left gutter. Sized from the overhangs above with a couple of px of
 * slack: the first band centre sits at `5 + plot/2n` = 15.5px at a 360px
 * viewport, and a 9px label reaches 22.4px to its left. Sized for the POINT
 * scale, which is the tighter of the two: a LineChart or AreaChart puts its
 * first tick at exactly `margin.left + padding.left`, while a BarChart's band
 * scale adds another half-band. Measured at `left: 12`, Category Trends — the
 * one point-scale chart with no YAxis — still clipped `Mar'26` at -7.6px while
 * its band-scale siblings were clean.
 */
export const ROTATED_MONTH_TICK_PADDING = { left: 20, right: 16 };

/**
 * The same rule for a chart that already renders a `<YAxis>`. Its width (80px on
 * every such chart here) IS the left gutter, so the left half must stay 0 —
 * adding it back would narrow the plot for nothing.
 *
 * The right half is NOT the mirror of that and must match the sibling constant:
 * a YAxis lends nothing to the right edge, and the number there is set by
 * recharts' footprint check on the last tick rather than by the ink overhang.
 * Shrinking it to the ~8.5px the label actually overhangs is what collapsed the
 * end-anchored strategy to one label. See `monthAxisInterval`.
 */
export const ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS = { left: 0, right: 16 };

/**
 * `interval` for a month axis of `bucketCount` buckets.
 *
 * `0` — render EVERY bucket — up to the count the geometry above is sized for,
 * because the owner's complaint was that thinning to 3–6 labels lost him the
 * months, not that they collided. Measured on the built container at 360px,
 * with every gutter above applied — twelve rotated 9px labels need 11.3px of
 * spacing and a flat one 17.4px:
 *
 *   Year-over-Year    13.1px   +1.8   <- the tightest on the page
 *   Net Cash Flow     14.3px   +3.0
 *   Budget vs Actual  21.1px   +3.7   (flat, so it is measured against 17.4)
 *   Income vs Expenses 18.1px  +6.8
 *
 * So twelve fit by construction and no collision check is wanted. Year-over-Year
 * is the chart to re-measure first if any of the geometry above changes.
 *
 * Beyond that the count is unbounded — the Time Period control reaches 24 and
 * All time, which is ~516 buckets for a 1984 ledger — and no fixed geometry
 * survives it, so recharts measures the real plot and picks a stride. Both
 * `equidistant*` strategies pick a STRIDE rather than dropping individuals,
 * which is what the `preserve*` family gets wrong.
 *
 * END-anchored, and getting there took two attempts. `equidistantPreserveStart`
 * anchors the OLDEST bucket, and on a window that trails the present that
 * leaves the CURRENT month unlabelled — measured, an All-time Net Cash Flow at
 * 360px labelled up to `Sep'21` while the chart ran to `Aug'26`, and a 24-month
 * window stopped at `May'26`. On a budgeting chart the most recent month is the
 * one being looked for.
 *
 * `equidistantPreserveEnd` was tried FIRST and degenerated to exactly one label
 * on a point scale, which is why the Start variant was used at all. The cause
 * was the right gutter, not the strategy: recharts checks the last tick against
 * the plot's right bound using its own footprint model, and on a point scale the
 * last tick sits ON that bound — so the check can only pass if `padding.right`
 * is at least half the modelled footprint. At 9px and -45° that is
 * `(29.9·cos45 + 12·sin45) / 2` ≈ 14.8px, which is why the right gutter is 16
 * and not 10.
 *
 * The threshold is the bucket count, not a width, and that is deliberate: a
 * width-keyed switch is what was tried and disproved (the measurements are in
 * the header of `chartAxis.seam.test.ts`).
 */
export const MAX_UNTHINNED_MONTH_TICKS = 12;

export function monthAxisInterval(
  bucketCount: number,
): 0 | 'equidistantPreserveEnd' {
  return bucketCount <= MAX_UNTHINNED_MONTH_TICKS ? 0 : 'equidistantPreserveEnd';
}

/**
 * Height for the Spending → Category Breakdown chart, in px.
 *
 * The same rule and the same numbers as `tagChart.ts`, and for the same reason:
 * one horizontal bar per row, from a list the backend does not cap. A fixed
 * `h-[300px]` squeezed the bars once a household had more than a handful of
 * categories, and the category NAMES on the YAxis are what collide first —
 * reported from the owner's ledger of ~9, invisible on a dev database of 2.
 *
 * Deliberately uncapped, like the tag chart: a ceiling reintroduces the squeeze
 * the sizing exists to remove. The floor keeps a two-category chart from looking
 * broken, and matches the height this chart used to have unconditionally, so
 * nothing shrinks for a household that was already fine.
 *
 * Not shared with `tagChartHeightPx` even though the arithmetic is identical:
 * one is per TAG and one per CATEGORY, they are read by different pages, and
 * folding them together would mean a future change to one silently resizing the
 * other. The duplication is four numbers; the coupling would be invisible.
 */
export const CATEGORY_ROW_PX = 36;
export const CATEGORY_CHART_PADDING_PX = 48;
export const CATEGORY_CHART_MIN_PX = 300;

export function categoryChartHeightPx(categoryCount: number): number {
  return Math.max(
    CATEGORY_CHART_MIN_PX,
    categoryCount * CATEGORY_ROW_PX + CATEGORY_CHART_PADDING_PX,
  );
}

/**
 * A category name shortened to one axis-label line.
 *
 * Recharts WRAPS a `<YAxis type="category">` label that does not fit its
 * `width`, and the wrapped lines grow downward into the next row: measured, a
 * 100-character name rendered across FIVE tspans, 64px tall, overlapping its
 * neighbour — which is the collision the data-driven height was meant to end.
 * Sizing the rows for the tallest possible label instead would mean 64px rows
 * for every household, so the label is bounded and the row stays one line.
 *
 * Nothing is lost that the user cannot get: the bar's tooltip carries the full
 * name, and the Category Breakdown card is a ranking, not a reference list.
 * Names are user-supplied and the server caps them at 100 characters
 * (`internal/api/limits.go`), so the ceiling is reachable from the UI alone.
 *
 * 18 is what fits a 100px axis at the tick font with the ellipsis: longer and
 * recharts wraps again, shorter and ordinary names like "Health/medical" start
 * losing characters for nothing.
 */
export const MAX_CATEGORY_LABEL_CHARS = 18;

export function shortenCategoryLabel(name: string): string {
  return name.length > MAX_CATEGORY_LABEL_CHARS
    ? `${name.slice(0, MAX_CATEGORY_LABEL_CHARS - 1).trimEnd()}\u2026`
    : name;
}

/**
 * Phone-width table density for the Reports tables.
 *
 * shadcn's `TableCell` ships `p-4`, which is 32px of horizontal padding per
 * column before any content. On a five-column table at a 360px viewport that
 * is 160px of the ~278px available, and the table pans. Halving it below `sm`
 * and restoring it above buys the columns back on the phone without changing
 * anything on a desktop.
 *
 * SHARED because it was copy-pasted between SpendingTab and PatternsTab, and
 * the second copy carried a comment reading "see the note there" — a pointer
 * to prose that a later edit to either table could silently invalidate. One
 * constant means the two densities cannot drift apart, which is the whole
 * reason they matched.
 *
 * NOT used by `Dashboard.tsx`, deliberately. Its recent-transactions table
 * keeps its own local copy with a comment re-measuring why `max-w-*` binds
 * there and not here; that reasoning belongs at its call site and would be
 * lost behind an import.
 */
export const PHONE_TABLE_DENSITY =
  '[&_td]:px-2 [&_th]:px-2 sm:[&_td]:px-4 sm:[&_th]:px-4';

/**
 * A free-text table cell bounded in BOTH axes.
 *
 * Two classes doing two different jobs, which is why they travel together:
 * `overflow-wrap:anywhere` (not `break-words`) is what bounds the WIDTH of a
 * string with no spaces in it — import bypasses the 500-character description
 * limit the per-row edit route enforces, so a spreadsheet cell can put an
 * arbitrarily long unbroken run into the ledger. `line-clamp-3` then bounds the
 * HEIGHT, because a wrapped cell grows downward without limit.
 *
 * THREE lines rather than one truncated line: neither surface has row
 * expansion, and `title` is dead on touch, so the clamped text is the only way
 * back to the content on the primary platform. Three lines is ~27 characters at
 * phone width and ~150 at desktop, against ~14 for a single truncated line.
 * Pair it with `TABLE_TEXT_CELL_WIDTH` on the cell and a `title` for pointers.
 */
export const CLAMPED_TABLE_TEXT = 'line-clamp-3 [overflow-wrap:anywhere]';

/**
 * The width bound that makes `CLAMPED_TABLE_TEXT` do anything.
 *
 * A table cell sizes to its content by default, so the clamp above has nothing
 * to clamp against until the cell itself is bounded. Narrow on a phone, roomy
 * from `md` up where there is width to spend.
 */
export const TABLE_TEXT_CELL_WIDTH = 'max-w-[12rem] md:max-w-md';
