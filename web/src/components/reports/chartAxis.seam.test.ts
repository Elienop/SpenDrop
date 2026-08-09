import { describe, test, expect } from 'vitest';

import overviewSource from './OverviewTab.tsx?raw';
import spendingSource from './SpendingTab.tsx?raw';
import savingsSource from './SavingsTab.tsx?raw';
import patternsSource from './PatternsTab.tsx?raw';
import dashboardSource from '@/pages/Dashboard.tsx?raw';
import {
  categoryChartHeightPx,
  shortenCategoryLabel,
  MAX_CATEGORY_LABEL_CHARS,
  CATEGORY_CHART_MIN_PX,
  CATEGORY_ROW_PX,
  monthAxisInterval,
  MAX_UNTHINNED_MONTH_TICKS,
  MONTH_TICK,
  MONTH_TICK_ANGLE,
  ROTATED_MONTH_TICK_PADDING,
  ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS,
} from './utils';

/**
 * SOURCE PINS. Everything in this file is a wiring seam, deliberately, and the
 * reason is worth stating once rather than apologising for five times:
 *
 * happy-dom performs no layout. A clipped tick label, a collision between two
 * labels, and a table that is wider than its card are all GEOMETRY, and none of
 * them is observable in vitest. Worse, recharts degrades in a way that looks
 * like success: `SpendingTab.test.tsx:296` records that with no layout its
 * overlap arithmetic collapses to `minTickGap` alone, so a 12-, 24- or 48-point
 * axis renders EVERY tick with or without an `interval` prop. A test asserting
 * "twelve tick labels rendered" would pass on every mutant here.
 *
 * So the props are pinned as source text, the measurements live in the comments
 * at each call site, and the behaviour was verified in Chrome against the built
 * container at 390px and 1440px. Do not upgrade any assertion in this file into
 * a claim about what the chart looks like.
 */

// PatternsTab is in the list even though it has no angled axis today. It is a
// Reports tab with charts on it, so it is exactly where a fourth rotated month
// axis would be added — and leaving it out is how the count control below stops
// covering the file it most needs to.
const SOURCES: ReadonlyArray<readonly [string, string]> = [
  ['OverviewTab.tsx', overviewSource],
  ['SpendingTab.tsx', spendingSource],
  ['SavingsTab.tsx', savingsSource],
  ['PatternsTab.tsx', patternsSource],
  ['Dashboard.tsx', dashboardSource],
];

/**
 * Every `<XAxis … />` in a source file. `XAxis` is always self-closing here and
 * never contains a nested element, so a non-greedy match to the first `/>` is
 * unambiguous — unlike `<ChartLegend>`, which wraps a self-closing child and
 * needed neutralising first (see `chart.test.tsx`).
 */
const axes = (source: string): string[] =>
  source.match(/<XAxis[\s\S]*?\/>/g) ?? [];

describe('the month-axis tick rule', () => {
  // THE MEASUREMENTS, kept here because they outlived the code that carried
  // them. `axisTickInterval` and `MAX_AXIS_TICKS` were deleted in this batch
  // (owner-approved) and their docblock held the only record of why a fixed
  // tick cap cannot work. In full:
  //
  //   * Rotated labels are parallel strips. Two clear each other only when
  //     `spacing × sin(angle) >= inkHeight`. Ink height is 11px at a 12px tick
  //     font, measured through canvas metrics — NOT the 16px `getBBox()`
  //     reports, which is the em box and over-reports by 45%.
  //   * A viewport-keyed cap was built, measured and thrown away. Chart width is
  //     not monotonic in viewport width: the Reports grid is `md:grid-cols-2`,
  //     so every chart HALVES at 768px, exactly where such a cap switches off.
  //     Twelve labels on Net Cash Flow, measured on the built container —
  //     360px: 8.5px of clearance (OVERLAPS); 700px: 22.8px (ok); 768px: 6.8px
  //     (OVERLAPS, worse than the phone); 900px: 9.8px (OVERLAPS); 1024px:
  //     12.7px (ok). This is the finding to read before reaching for a
  //     breakpoint-keyed axis constant again.
  //   * `MAX_AXIS_TICKS = 24` was sized for render cost, never for fit: 24
  //     rotated labels measured a 10.4-11.2px gap against the 11px they need,
  //     at 1440px, on both a 24-month and an All-time window.
  //   * `equidistantPreserveEnd` DEGENERATES on a point scale — every stepsize
  //     fails its visibility walk and it renders exactly ONE label.
  //
  // What replaced all of it is a smaller, steeper tick: the font is in both
  // terms of the collision condition AND in the overhang, so shrinking it buys
  // fit and plot width at once.
  const angled = (source: string) =>
    axes(source).filter((a) => a.includes('angle={MONTH_TICK_ANGLE}'));
  const withPlainPadding = (source: string) =>
    axes(source).filter((a) => a.includes('padding={ROTATED_MONTH_TICK_PADDING}'));
  const withInsideYAxisPadding = (source: string) =>
    axes(source).filter((a) =>
      a.includes('padding={ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS}'),
    );

  test.each(SOURCES)('%s: every angled axis reserves a gutter and shrinks its font', (_name, source) => {
    for (const axis of angled(source)) {
      // COUNTED PER VARIANT below, and matched on the CLOSING brace here: the
      // plain constant's name is a PREFIX of the `_INSIDE_YAXIS` one, so a
      // `toMatch(/padding=\{ROTATED_MONTH_TICK_PADDING/)` accepted both and let
      // a chart silently take `left: 0` — the clip this rule exists to prevent.
      const plain = axis.includes('padding={ROTATED_MONTH_TICK_PADDING}');
      const insideYAxis = axis.includes(
        'padding={ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS}',
      );
      expect(plain || insideYAxis).toBe(true);
      expect(axis).toContain('tick={MONTH_TICK}');
    }
  });

  test('three angled axes take the full gutter, three sit inside a YAxis', () => {
    // Positive control for the loop above, which passes vacuously over an empty
    // list, AND the pin that keeps the two constants non-interchangeable.
    // Full gutter: Income vs Expenses, Category Trends, the Dashboard's Cash
    // Flow. Inside a YAxis: Net Cash Flow, Year-over-Year, cumulative savings —
    // each already spends 80px on a currency axis, which IS the left gutter.
    const plain = SOURCES.reduce((n, [, src]) => n + withPlainPadding(src).length, 0);
    const insideYAxis = SOURCES.reduce(
      (n, [, src]) => n + withInsideYAxisPadding(src).length,
      0,
    );
    const total = SOURCES.reduce((n, [, src]) => n + angled(src).length, 0);
    expect(plain).toBe(3);
    expect(insideYAxis).toBe(3);
    expect(plain + insideYAxis).toBe(total);
  });

  test('the geometry constants stay inside what the narrowest chart can carry', () => {
    // At a 360px viewport the tightest plot has 14.4px of spacing between
    // twelve month buckets (Year-over-Year: 263px of SVG minus an 80px YAxis).
    // A -45 degree, 9px tick needs 11.7px of that and overhangs 22.4px left /
    // 8.5px right. Every number below is one of those bounds, so a "tidy-up"
    // that shallows the angle or grows the font fails here rather than in the
    // browser. 10px was built and measured and left Year-over-Year 0.6px of
    // margin, which is why the font bound is an upper one.
    expect(Math.abs(MONTH_TICK_ANGLE)).toBeGreaterThanOrEqual(45);
    expect(MONTH_TICK.fontSize).toBeLessThanOrEqual(9);

    // The gutters are what the owner objected to: `{left: 40, right: 26}` was
    // 66px of a 263px chart, a quarter of the plot, and it squeezed the bars
    // into the middle while Budget vs Actual used its full width. They must
    // stay small AND still clear the overhang, which is why both bounds are here.
    expect(ROTATED_MONTH_TICK_PADDING.left).toBeGreaterThanOrEqual(18);
    expect(ROTATED_MONTH_TICK_PADDING.left).toBeLessThanOrEqual(24);
    // The right gutter is sized by the END-anchored strategy, not by the ink:
    // recharts checks the LAST tick against the plot's right bound with its own
    // footprint model, and on a point scale that tick sits ON the bound — so the
    // check passes only if the padding covers half the modelled footprint,
    // `(29.9·cos45 + 12·sin45) / 2` ~ 14.8px. At 10 it failed and the whole
    // strategy collapsed to ONE label.
    expect(ROTATED_MONTH_TICK_PADDING.right).toBeGreaterThanOrEqual(15);
    expect(ROTATED_MONTH_TICK_PADDING.right).toBeLessThanOrEqual(20);

    // And the whole point of the second constant: its left gutter is the YAxis,
    // so this must stay 0. Setting it to 12 is otherwise invisible.
    expect(ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS.left).toBe(0);
    expect(ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS.right).toBeGreaterThanOrEqual(15);
  });

  test('twelve buckets are rendered whole; only a longer window is thinned', () => {
    // The owner's complaint was that thinning lost him the months, not that
    // they collided — so up to the count the geometry is sized for, every
    // bucket is labelled. Beyond it the window is unbounded (24 months, and
    // ~516 for an All-time view over a 1984 ledger) and recharts measures the
    // real plot instead.
    expect(monthAxisInterval(12)).toBe(0);
    expect(monthAxisInterval(MAX_UNTHINNED_MONTH_TICKS)).toBe(0);
    expect(monthAxisInterval(MAX_UNTHINNED_MONTH_TICKS + 1)).toBe(
      'equidistantPreserveEnd',
    );
    expect(monthAxisInterval(516)).toBe('equidistantPreserveEnd');

    // END-anchored, not Start, and this is the assertion that pins WHY. On a
    // window that trails the present, the Start variant anchors the OLDEST
    // bucket and leaves the CURRENT month unlabelled — measured, an All-time
    // Net Cash Flow at 360px labelled up to `Sep'21` while the chart ran to
    // `Aug'26`, and a 24-month window stopped at `May'26`. On a budgeting chart
    // the most recent month is the one being looked for. After: every window at
    // both widths ends on `Aug'26`.
    expect(monthAxisInterval(516)).not.toBe('equidistantPreserveStart');
  });
});

describe('every month axis goes through the shared rule', () => {
  const AXIS_OWNERS: ReadonlyArray<readonly [string, string, number]> = [
    ['OverviewTab.tsx', overviewSource, 3],
    ['SavingsTab.tsx', savingsSource, 2],
    ['SpendingTab.tsx', spendingSource, 1],
    ['Dashboard.tsx', dashboardSource, 1],
  ];

  const monthAxes = (source: string) =>
    axes(source).filter(
      (a) => a.includes('dataKey="date"') || a.includes('dataKey="name"'),
    );

  test.each(AXIS_OWNERS)('%s: derives its interval and its font from the rule', (_name, source, count) => {
    const found = monthAxes(source);
    // Positive control: the filter finding nothing would pass the loop.
    expect(found).toHaveLength(count);
    for (const axis of found) {
      expect(axis).toContain('interval={monthAxisInterval(');
      expect(axis).toContain('tick={MONTH_TICK}');
    }
  });

  test('no chart has gone back to a fixed or hand-picked interval', () => {
    // The four shapes this replaced, any of which a future edit could restore
    // without looking wrong.
    for (const [, source] of AXIS_OWNERS) {
      for (const axis of monthAxes(source)) {
        expect(axis).not.toContain('interval={0}');
        expect(axis).not.toContain('interval="preserve');
        expect(axis).not.toContain('axisTickInterval');
      }
    }
  });

  test('the deleted helper is gone and nothing reaches for it', () => {
    // `axisTickInterval` and `MAX_AXIS_TICKS` were removed with the owner's
    // approval; the measurements that justified them are in this file's header
    // instead. This keeps a reintroduction loud.
    for (const [, source] of AXIS_OWNERS) {
      expect(source).not.toContain('axisTickInterval(');
      expect(source).not.toContain('MAX_AXIS_TICKS');
    }
  });
});

describe('free-text table cells are bounded in both directions', () => {
  // Also a source pin, and for the same reason: a table that is 3464px wide
  // inside a 293px card is a layout fact, and happy-dom reports every element
  // as 0x0. The DOM half — that the classes reach the rendered cell — is
  // asserted for real in `SpendingTab.merchants.test.tsx` and
  // `pages/Dashboard.test.tsx`. This pin exists for PatternsTab, whose Recurring
  // Expenses table shares the treatment but sits in a file whose other card is
  // being rebuilt concurrently, so rendering it here would couple the two.
  //
  // `overflow-wrap:anywhere` is the class that bounds the WIDTH: Tailwind's
  // `break-words` (`overflow-wrap:break-word`) breaks a token for painting but
  // leaves the element's min-content contribution at the token's full width, so
  // a table cell keeps sizing itself to it. `line-clamp-*` bounds the HEIGHT,
  // which wrapping alone trades the width problem for — measured, a 500-char
  // token wrapped into a 932px-tall row at 390px, and import does not enforce
  // the 500-char per-row ceiling.
  const CELLS: ReadonlyArray<readonly [string, string]> = [
    ['SpendingTab.tsx (Top Merchants)', spendingSource],
    ['PatternsTab.tsx (Recurring Expenses)', patternsSource],
    ['Dashboard.tsx (desktop Recent Transactions)', dashboardSource],
  ];

  test.each(CELLS)('%s bounds width and height together', (_name, source) => {
    const bounded = source.match(/className="line-clamp-\d[^"]*\[overflow-wrap:anywhere\][^"]*"/g) ?? [];
    expect(bounded.length).toBeGreaterThan(0);
  });

  test.each(CELLS)('%s: no half-fix — the two classes always travel together', (_name, source) => {
    // The failure mode this catches is a half-fix: `overflow-wrap:anywhere`
    // without a clamp turns a horizontal overflow into an unbounded vertical
    // one, and a clamp without it leaves the column sized to the longest token.
    //
    // Counted over `className="…"` attributes rather than the raw file, because
    // the comments at these call sites NAME both classes and a file-wide count
    // reads those as usages.
    //
    // And asserted ONE-DIRECTIONALLY, from the unbounded-width class outwards.
    // The symmetric `expect(withClamp).toEqual(withAnywhere)` looks stronger and
    // is worse: `line-clamp-*` is an ordinary layout utility, so the first
    // unrelated one added anywhere in these files — a truncated card title, a
    // two-line subtitle — would fail this test with nothing wrong. Both halves
    // of the half-fix are still caught: dropping the wrap empties the list,
    // dropping the clamp fails the match.
    const classNames = source.match(/className="[^"]*"/g) ?? [];
    const boundedCells = classNames.filter((c) =>
      c.includes('[overflow-wrap:anywhere]'),
    );

    // At least one — the positive control, since an empty list passes any
    // `for` — and EVERY one of them clamped. Not an exact count: the
    // Dashboard's cell carries two bounded fields (the description and the
    // category name under it), because bounding only the first left the column
    // pinned at 473.7px by the second.
    expect(boundedCells.length).toBeGreaterThan(0);
    for (const cell of boundedCells) {
      expect(cell).toMatch(/line-clamp-\d/);
    }
  });
});

describe('Category Breakdown is sized to its data, not to a constant', () => {
  // The owner reported the category names overlapping on a ledger of ~9; a dev
  // database of 2 could not show it. Two halves, and both are needed: the chart
  // grows a row per category, and each label is bounded to ONE line so the rows
  // it grew are the rows it uses.

  test('the height grows one row per category and never shrinks below the old fixed one', () => {
    // The floor is the height this chart used to have unconditionally, so a
    // household that was already fine sees no change.
    expect(categoryChartHeightPx(0)).toBe(CATEGORY_CHART_MIN_PX);
    expect(categoryChartHeightPx(2)).toBe(CATEGORY_CHART_MIN_PX);
    // And beyond the floor it is linear in the count — measured in the browser
    // at 10 categories: 408px, against the 300px that was squeezing them.
    expect(categoryChartHeightPx(10)).toBe(408);
    expect(categoryChartHeightPx(11) - categoryChartHeightPx(10)).toBe(CATEGORY_ROW_PX);
    // Uncapped on purpose: a ceiling reintroduces the squeeze.
    expect(categoryChartHeightPx(40)).toBeGreaterThan(1000);
  });

  test('a long category name is shortened to one line, an ordinary one is untouched', () => {
    // Recharts WRAPS an over-wide category label and the extra lines grow into
    // the next row — measured, a 100-character name rendered five tspans tall
    // and overlapped its neighbour even with the data-driven height.
    const long = 'Household Groceries Pharmacy And Sundries'.padEnd(100, 'Z');
    const short = shortenCategoryLabel(long);
    expect(short.length).toBe(MAX_CATEGORY_LABEL_CHARS);
    expect(short.endsWith('\u2026')).toBe(true);

    // The names a household actually uses must come through whole — a formatter
    // that ellipsised "Health/medical" would be a regression, not a fix.
    for (const name of ['Food', 'Health/medical', 'Transportation', 'Gifts']) {
      expect(shortenCategoryLabel(name)).toBe(name);
    }
    // Exactly at the limit is not shortened; one over is.
    expect(shortenCategoryLabel('X'.repeat(MAX_CATEGORY_LABEL_CHARS)))
      .toHaveLength(MAX_CATEGORY_LABEL_CHARS);
    expect(shortenCategoryLabel('X'.repeat(MAX_CATEGORY_LABEL_CHARS + 1)))
      .toContain('\u2026');
  });

  test('the chart wires both halves in', () => {
    // WIRING SEAM: happy-dom lays nothing out, so neither the height nor the
    // wrap is observable. What is checkable is that the pure functions above
    // reach the axis, and that the height is not back to a Tailwind constant.
    const breakdown = spendingSource.slice(
      spendingSource.indexOf('category-breakdown-heading'),
      spendingSource.indexOf('category-trends-heading'),
    );
    expect(breakdown).toContain('categoryChartHeightPx(breakdownSorted.length)');
    expect(breakdown).toContain('tickFormatter={shortenCategoryLabel}');
    expect(breakdown).toContain('interval={0}');

    // And the fixed height is GONE from the chart container. Scoped to the
    // container's own className rather than the whole card: the loading
    // Skeleton keeps an `h-[300px]`, correctly — it renders before the count is
    // known — so a card-wide regex fails against correct code, which is how the
    // first version of this assertion failed.
    const container = breakdown.slice(
      breakdown.indexOf('<ChartContainer'),
      breakdown.indexOf('<BarChart'),
    );
    expect(container).toContain('style={{ height:');
    expect(container).not.toMatch(/h-\[\d+px\]/);
  });
});

describe('the two Reports tables are dense at phone width', () => {
  // The shared `TableCell` is `p-4` and `TableHead` `px-4`, so four columns
  // spend 128px on padding before any content — measured, Top Merchants wanted
  // 318.5px inside a 293px card and the amount column was clipped. Halving the
  // horizontal half below `sm` gave 64px back: measured after, 263px inside
  // 263px, zero overflow.
  const DENSE = ['[&_td]:px-2', '[&_th]:px-2', 'sm:[&_td]:px-4', 'sm:[&_th]:px-4'];

  test.each([
    ['SpendingTab.tsx (Top Merchants)', spendingSource],
    ['PatternsTab.tsx (Recurring Expenses)', patternsSource],
  ])('%s carries both halves of the responsive density', (_name, source) => {
    for (const cls of DENSE) {
      expect(source).toContain(cls);
    }
    // BOTH halves: the phone-only half alone would shrink every viewport, and
    // the `sm:` half alone would shrink none.
    expect(source).toContain('sm:[&_td]:px-4');
    // And it is scoped to a table, not applied to the shared component — the
    // ledger, Trash, the import preview and Settings did not ask for it.
    expect(source).not.toContain('TableCell className="px-2"');
  });
});
