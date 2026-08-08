import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, cleanup, waitFor } from '@testing-library/react';
import React from 'react';
import { TYPE_EXPENSE } from '@/lib/transaction-types';
import type {
  CategoryBreakdownItem,
  CategoryTrendEntry,
  ExpenseVelocityData,
  TopMerchantEntry,
} from '@/api/types';

// This file used to mock recharts WHOLESALE (`Bar: () => <div />`, …) and feed
// every hook an empty list, so not one chart body ever rendered. The recharts
// 2 -> 3 bump changed three library DEFAULTS with no compile error and the
// whole suite stayed green while a legend silently reordered in the browser —
// this file was part of that blindness.
//
// It now mocks ONLY the sizing, exactly as `components/ui/chart.test.tsx` does.
// `ResponsiveContainer` measures its parent, which is 0x0 under happy-dom, and
// recharts bails on a non-positive size — so a fixed size is the single thing
// standing between us and real charts in tests. Everything below the container
// is the genuine library: the legend ordering pinned here lives in recharts'
// own selector, not in our wrapper, and a stubbed Legend would assert nothing.
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    // Typed explicitly: `cloneElement` has no overload for an element whose
    // props are unknown, so the size props must be declared for `tsc -b` —
    // which type-checks test files, unlike `--noEmit`.
    ResponsiveContainer: ({
      children,
    }: {
      children: React.ReactElement<{ width?: number; height?: number }>;
    }) => React.cloneElement(children, { width: 400, height: 300 }),
  };
});

// --- Fixtures -------------------------------------------------------------
//
// The ids are chosen so the two orderings the charts care about DISAGREE.
// SpendingTab declares its series in descending-spend order but keys them
// `cat-<id>`, so recharts 3's default legend sorter orders the chips by that
// key STRING: 'cat-11' < 'cat-2' < 'cat-4'. With ids 2 / 11 / 4 and spend
// 300 / 200 / 100 the sorted order (Transport, Groceries, Utilities) is not
// the declared one (Groceries, Transport, Utilities), so the mutant is
// visible — verified by deleting `itemSorter={null}` and watching this file
// fail. Ids that happened to sort into spend order would have made the whole
// suite pass over a reordered legend, which is the shape of the original bug;
// do not "tidy" these to 1 / 2 / 3.
const BREAKDOWN: CategoryBreakdownItem[] = [
  // Deliberately NOT pre-sorted: the component sorts by total descending.
  { id: 11, name: 'Transport', total: 200, limit: null, over: false },
  { id: 4, name: 'Utilities', total: 100, limit: null, over: false },
  { id: 2, name: 'Groceries', total: 300, limit: null, over: false },
];

/**
 * Twelve months, Mar'25 -> Feb'26 — the window the component actually asks for
 * (`useCategoryTrends(12)`), spanning a year boundary so `formatMonthTick`'s
 * 2-digit year is exercised on both sides of it.
 *
 * EMITTED NEWEST-FIRST, DELIBERATELY. Do not "tidy" this into ascending order.
 * `catTrendData` builds `monthSet` in iteration order and then calls `.sort()`
 * on it; with an ascending fixture that sort is a NO-OP, so deleting it left
 * all 8 tests green while the X-axis test's comment claimed the chronological
 * sort was covered. Reversing the fixture makes `monthSet`'s insertion order
 * descending, so the axis is ascending ONLY because `.sort()` runs — which is
 * what turns that claim into an actual assertion.
 *
 * Reversing is safe for everything else here: `byCat` is keyed by date string,
 * and each category's `totalSum` is an order-independent sum, so the legend's
 * spend ranking is unchanged.
 *
 * This replaced a two-month fixture. It widens what the X-axis test below can
 * see (twelve distinct months, formatted, in chronological order) but it does
 * NOT make that test sensitive to `interval={0}` — see the note on that test
 * for the measurements and why no fixture width fixes that.
 */
function twelveMonths(base: number): CategoryTrendEntry['data'] {
  return Array.from({ length: 12 }, (_, i) => ({
    year: i < 10 ? 2025 : 2026,
    month: i < 10 ? i + 3 : i - 9,
    total: base + i,
  })).reverse();
}

// Sums: Groceries 3666 > Transport 1866 > Utilities 546 — the same ranking the
// old two-month fixture had, so the legend-order test is unchanged in substance.
const TRENDS: CategoryTrendEntry[] = [
  { id: 4, name: 'Utilities', type: TYPE_EXPENSE, data: twelveMonths(40) },
  { id: 2, name: 'Groceries', type: TYPE_EXPENSE, data: twelveMonths(300) },
  { id: 11, name: 'Transport', type: TYPE_EXPENSE, data: twelveMonths(150) },
];

const VELOCITY: ExpenseVelocityData = {
  days_in_month: 30,
  budget: 600,
  current: [
    { day: 1, daily_total: 10 },
    { day: 2, daily_total: 20 },
  ],
  previous: [
    { day: 1, daily_total: 5 },
    { day: 2, daily_total: 5 },
  ],
};

// Left empty on purpose: Top Merchants is a plain <Table>, not a chart, and a
// populated one only adds currency strings that collide with the bar-label
// assertions below. This matches the pre-conversion fixture for that card.
const MERCHANTS: TopMerchantEntry[] = [];

const ok = <T,>(data: T) => ({ data, loading: false, fetching: false, error: '' });

vi.mock('@/hooks/useReports', () => ({
  useCategoryBreakdown: () => ok(BREAKDOWN),
  useCategoryTrends: () => ok(TRENDS),
  useTopMerchants: () => ok(MERCHANTS),
  useExpenseVelocity: () => ok(VELOCITY),
}));

vi.mock('@/hooks/useBaseCurrency', () => ({ useBaseCurrency: () => 'USD' }));

vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => ({
    years: [2026],
    currentYear: 2026,
    hasTransactions: true,
    outOfRangeYears: [],
    futureYears: [],
    loading: false,
  }),
}));

import { SpendingTab } from './SpendingTab';

afterEach(cleanup);

/** The <Card> for a section, located by the heading it is labelled by. */
function card(container: HTMLElement, headingId: string): HTMLElement {
  const el = container.querySelector<HTMLElement>(
    `[aria-labelledby="${headingId}"]`,
  );
  if (!el) throw new Error(`no card labelled by #${headingId}`);
  return el;
}

/**
 * Legend chip labels, in DOM order, from the real `.recharts-legend-wrapper`.
 * The wrapper's only child is `ChartLegendContent`'s root, whose children are
 * the chips; the colour swatch inside each chip contributes no text.
 */
function legendLabels(scope: HTMLElement): string[] {
  const wrapper = scope.querySelector('.recharts-legend-wrapper');
  if (!wrapper) throw new Error('no .recharts-legend-wrapper rendered');
  return Array.from(wrapper.querySelectorAll(':scope > div > div')).map(
    (el) => el.textContent?.trim() ?? '',
  );
}

const svgTexts = (scope: HTMLElement, selector: string): string[] =>
  Array.from(scope.querySelectorAll(selector)).map(
    (el) => el.textContent?.trim() ?? '',
  );

/**
 * `<Bar>` mounts its `.recharts-bar-rectangle` groups synchronously but leaves
 * them EMPTY: the `<path>` arrives on the first animation frame and the
 * `<Bar>`'s `<LabelList>` text later still, at the end of the enter animation.
 * Asserting on either straight after `render` reads an empty list.
 *
 * So wait for the expected COUNT, then assert content and order separately —
 * never assert only "eventually non-empty", which would pass on a chart that
 * drew the wrong bars. If the nodes never arrive this times out and fails,
 * which is the intended outcome: it means the chart stopped drawing.
 *
 * Axis ticks and the legend need no wait; only geometry-derived children do.
 */
async function waitForCount(
  scope: HTMLElement,
  selector: string,
  count: number,
): Promise<void> {
  await waitFor(
    () => {
      expect(scope.querySelectorAll(selector)).toHaveLength(count);
    },
    { timeout: 5000 },
  );
}

describe('SpendingTab renders real recharts output', () => {
  test('positive control: all three charts mount with a real recharts surface', () => {
    // Without this, every assertion below could pass vacuously against a tab
    // that rendered no chart at all (a thrown-and-swallowed chart, a
    // ResponsiveContainer that measured 0x0, an over-eager future mock). The
    // count is exact so a chart that silently stops rendering is a failure,
    // not a shrug.
    const { container } = render(<SpendingTab />);
    expect(container.querySelectorAll('.recharts-wrapper')).toHaveLength(3);
    expect(container.querySelectorAll('.recharts-surface')).toHaveLength(3);
  });

  test('Expense Velocity legend keeps declaration order, not recharts 3 sorting', () => {
    // THE regression the recharts 3 bump shipped: `Legend` gained `itemSorter`,
    // defaulting to 'value' and applied inside recharts' OWN selector, so it
    // reorders CUSTOM legend content too. recharts 2 never sorted. Observed
    // live on this exact chart, which rendered "Current Month, Budget Pace,
    // Previous Month" against a declared current / previous / pace.
    const { container } = render(<SpendingTab />);

    expect(legendLabels(card(container, 'expense-velocity-heading'))).toEqual([
      'Current Month',
      'Previous Month',
      'Budget Pace',
    ]);
  });

  test('Category Trends legend keeps descending-spend order', () => {
    // Same defect, second call site. Declared order is by spend
    // (Groceries 3666 > Transport 1866 > Utilities 546); the dataKeys are
    // cat-2 / cat-11 / cat-4, which the default sorter would reorder to
    // cat-11, cat-2, cat-4 — destroying the ranking the chart exists to show.
    const { container } = render(<SpendingTab />);

    expect(legendLabels(card(container, 'category-trends-heading'))).toEqual([
      'Groceries',
      'Transport',
      'Utilities',
    ]);
  });

  test('Category Breakdown Y axis ranks categories by spend', () => {
    // The category axis is rendered by recharts from `breakdownSorted`, so its
    // tick order IS the spend ranking (fixture arrives 200 / 100 / 300).
    // recharts 3 moved tick TEXT out of `.recharts-yAxis` into a separate
    // z-index layer, `.recharts-yAxis-tick-labels` — a wholesale mock could
    // never have noticed that, and neither could `tsc`.
    const { container } = render(<SpendingTab />);
    const breakdown = card(container, 'category-breakdown-heading');

    expect(
      svgTexts(
        breakdown,
        '.recharts-yAxis-tick-labels .recharts-cartesian-axis-tick-value',
      ),
    ).toEqual(['Groceries', 'Transport', 'Utilities']);
  });

  test('Category Breakdown paints one <Cell> per category, keyed to its colour var', async () => {
    const { container } = render(<SpendingTab />);
    const breakdown = card(container, 'category-breakdown-heading');
    await waitForCount(breakdown, '.recharts-bar .recharts-rectangle', 3);

    // Real <Rectangle> paths from recharts' Bar, not a stub div. The `fill`
    // comes from SpendingTab's `<Cell fill={`var(--color-cat-${id})`} />`, so
    // this pins the category -> colour-slot mapping AND the descending order
    // in one assertion.
    expect(
      Array.from(
        breakdown.querySelectorAll('.recharts-bar .recharts-rectangle'),
      ).map((el) => el.getAttribute('fill')),
    ).toEqual([
      'var(--color-cat-2)',
      'var(--color-cat-11)',
      'var(--color-cat-4)',
    ]);
  });

  test('Category Breakdown bar labels are currency-formatted by the LabelList formatter', async () => {
    // recharts 3 retyped `LabelList`'s formatter argument as `RenderableText`
    // (string | number | boolean | null | undefined), so SpendingTab's
    // formatter now guards `typeof value === 'number'`. A formatter that stops
    // being called, or one whose guard rejects the real argument, renders empty
    // labels — invisible to a wholesale mock and to `tsc`, because the guard's
    // else-branch is a legal empty string.
    const { container } = render(<SpendingTab />);
    const breakdown = card(container, 'category-breakdown-heading');
    await waitForCount(breakdown, '.recharts-label-list text', 3);

    expect(svgTexts(breakdown, '.recharts-label-list text')).toEqual([
      '$300.00',
      '$200.00',
      '$100.00',
    ]);
  });

  test('Category Trends X axis formats and orders every month it is given', () => {
    // COVERED: `tickFormatter={formatMonthTick}` (a formatter that stops being
    // applied renders raw `"2025-03-01"` ISO keys here, and the Dec'25 -> Jan'26
    // step pins the 2-digit year across a boundary), the chronological sort in
    // `catTrendData`'s `sortedMonths`, and that the data pipeline drops no month.
    // The exact array makes a reorder or an off-by-one fail, not just a count.
    //
    // NOT COVERED: `interval={0}` on this axis (SpendingTab.tsx). Deleting it
    // leaves this test — and the whole file — green, and no fixture rescues that:
    //   * happy-dom has no layout, so recharts' `getStringSize` measures every
    //     tick via `getBoundingClientRect()` as 0x0 and its overlap arithmetic
    //     degenerates to `minTickGap` (5px) alone. Measured by varying the
    //     fixture length against the mutant: 12/24/48 points all render every
    //     tick, and thinning only starts at 72 (renders 36). So a 72-month
    //     fixture WOULD kill the mutant — but it contradicts
    //     `useCategoryTrends(12)` and pins a happy-dom artefact, not behaviour.
    //   * Feeding recharts real text metrics does not rescue it either. Tried:
    //     a stub for the `recharts_measurement_span` rect at 7px/char, giving
    //     "Mar'25" a 42px box. Still 12/12 ticks — because `angle={-30}` sends
    //     that box through `getAngledRectangleWidth`, which returns
    //     `height / sin(150°)` = 24px. Angling labels is exactly what lets them
    //     pack tighter, so at this chart's width they fit and the mutant lives.
    //     That stub was an experiment and is deliberately NOT kept: it bought
    //     no coverage and this file mocks sizing only.
    // So `interval={0}` here is defensive width-insurance whose effect is below
    // this harness's resolution. Pin it in a browser check, not here, and do not
    // add a comment claiming the tick COUNT proves anything about it.
    const { container } = render(<SpendingTab />);
    const trends = card(container, 'category-trends-heading');

    expect(
      svgTexts(
        trends,
        '.recharts-xAxis-tick-labels .recharts-cartesian-axis-tick-value',
      ),
    ).toEqual([
      "Mar'25",
      "Apr'25",
      "May'25",
      "Jun'25",
      "Jul'25",
      "Aug'25",
      "Sep'25",
      "Oct'25",
      "Nov'25",
      "Dec'25",
      "Jan'26",
      "Feb'26",
    ]);
  });
});

/**
 * A grid item defaults to `min-width: auto`, so the width of the Recharts SVG
 * inside a card becomes the track's min-content and widens the page — measured
 * live at a 390px viewport, this tab rendered 3530px wide. `min-w-0` on the
 * grid ITEM is what breaks it; the same property on the chart does not.
 *
 * happy-dom lays nothing out, so the class is the only pinnable part. It is
 * asserted over every child the grid actually has rather than a counted few,
 * so a new chart card added without the class fails here.
 */
describe('SpendingTab phone width', () => {
  test('every card in the root grid may shrink below its content', () => {
    const { container } = render(<SpendingTab />);
    const root = container.firstElementChild;
    if (!root) throw new Error('SpendingTab rendered nothing');

    // Premise. If this tab ever moves to a flex column — safe for a different
    // reason, since column-flex items have no min-width:auto trap — this fails
    // and the invariant below has to be re-derived rather than quietly skipped.
    expect(root).toHaveClass('grid');

    const cards = Array.from(root.children);
    expect(cards.length).toBeGreaterThan(0);
    for (const card of cards) {
      expect(card).toHaveClass('min-w-0');
    }
  });
});
