import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';

const CURRENT_YEAR = 2026;

const yearsResult = {
  years: [CURRENT_YEAR],
  currentYear: CURRENT_YEAR,
  hasTransactions: true,
  outOfRangeYears: [] as number[],
  futureYears: [] as number[],
  loading: false,
};
const useReportYears = vi.fn(() => yearsResult);
vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => useReportYears(),
}));

const useIncomeExpenses = vi.fn();
vi.mock('@/hooks/useReports', () => ({
  useIncomeExpenses: (months: number) => useIncomeExpenses(months),
  useBudgetVsActual: () => ({
    data: [],
    loading: false,
    fetching: false,
    error: '',
  }),
}));

vi.mock('@/hooks/useBaseCurrency', () => ({ useBaseCurrency: () => 'USD' }));

// SIZING-ONLY recharts mock — the pattern proven in `components/ui/chart.test.tsx`.
//
// This file used to stub recharts WHOLESALE (`XAxis: () => <div
// data-interval={...} />`, `Tooltip: () => <div />`, …). That made it
// structurally blind: it could read back the props we passed, which only ever
// proved that we passed them. The recharts 2 -> 3 bump changed three library
// DEFAULTS with no compile error and this file — like the other five wholesale
// mocks — stayed green while a real chart misrendered in the browser.
//
// `ResponsiveContainer` measures its parent, which is 0x0 under happy-dom, and
// recharts bails on a non-positive size. A fixed size is therefore the ONLY
// thing standing between us and real charts in tests, so it is the only thing
// mocked here. Everything else below is the genuine library, which is the
// point: the axis assertions now read the <text> nodes recharts actually
// emitted, not the number we handed it.
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

import { OverviewTab } from './OverviewTab';
import { monthsToCoverYear, MAX_REPORT_MONTHS } from './utils';

function setup() {
  return userEvent.setup({
    advanceTimers: vi.advanceTimersByTime,
    pointerEventsCheck: 0,
  });
}

/** Open the Time Period Select and click the option named `name`. */
async function pickPeriod(
  user: ReturnType<typeof userEvent.setup>,
  name: RegExp,
): Promise<void> {
  await user.click(screen.getByRole('combobox', { name: /time period/i }));
  const listbox = await screen.findByRole('listbox');
  await user.click(within(listbox).getByRole('option', { name }));
}

/** Months the tab most recently asked `useIncomeExpenses` for. */
function requestedMonths(): number {
  const calls = useIncomeExpenses.mock.calls;
  return calls[calls.length - 1][0] as number;
}

describe('OverviewTab time period', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date(Date.UTC(CURRENT_YEAR, 6, 15)));
    useReportYears.mockReturnValue(yearsResult);
    useIncomeExpenses.mockReturnValue({
      data: [],
      loading: false,
      fetching: false,
      error: '',
    });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  test('defaults to a 12-month window', () => {
    render(<OverviewTab />);
    expect(requestedMonths()).toBe(12);
  });

  test('offers All time even for a household with only this year', async () => {
    // Shown unconditionally: the charts here are driven by a MONTHS control,
    // not by the year picker, so All time is the only control that can reach
    // an old year at all. Hiding it until the ledger looks old enough would
    // make the fix invisible in exactly the case it exists for.
    const user = setup();
    render(<OverviewTab />);

    await user.click(screen.getByRole('combobox', { name: /time period/i }));
    const listbox = await screen.findByRole('listbox');
    expect(
      within(listbox).getByRole('option', { name: /all time/i }),
    ).toBeInTheDocument();
  });

  test('All time asks for a window that reaches the oldest ledger year', async () => {
    useReportYears.mockReturnValue({
      ...yearsResult,
      years: [2026, 2024, 1984],
    });
    const user = setup();
    render(<OverviewTab />);

    await pickPeriod(user, /all time/i);

    // 1984 is the oldest year, so the window must span back to January 1984.
    expect(requestedMonths()).toBe(monthsToCoverYear(1984, CURRENT_YEAR));
    expect(requestedMonths()).toBe(516);
  });

  test('All time stays bounded by the ledger, never by the cap', async () => {
    // `years` is filtered to the data window and held at the current year, so
    // All time cannot ask for the 2412-month worst case from real data. If it
    // ever did, the response would be ~5x larger than the widest real ledger.
    useReportYears.mockReturnValue({
      ...yearsResult,
      years: [2026, 1984],
    });
    const user = setup();
    render(<OverviewTab />);

    await pickPeriod(user, /all time/i);

    expect(requestedMonths()).toBeLessThan(MAX_REPORT_MONTHS);
  });

  test('All time on an empty ledger still asks for a valid window', async () => {
    // The degradation path: `years` is `[current year]`, so the span is one
    // year. `monthsToCoverYear` floors at 24, which is what keeps this from
    // reaching the backend as `months=0` — and `?months=NaN` would fail
    // strconv.Atoi and 400 the request outright.
    useReportYears.mockReturnValue({
      ...yearsResult,
      years: [CURRENT_YEAR],
      hasTransactions: false,
    });
    const user = setup();
    render(<OverviewTab />);

    await pickPeriod(user, /all time/i);

    expect(requestedMonths()).toBe(24);
    expect(Number.isFinite(requestedMonths())).toBe(true);
  });

  test('a years response arriving after All time is picked widens the window', async () => {
    // THE reason the discriminant is stored instead of the resolved month
    // count. On first paint `years` is the current-year fallback, so All time
    // resolves to 24. When the real list lands the resolved count changes to
    // 516 — and a <Select> whose value was "24" would then hold a value with
    // no matching item and render a BLANK TRIGGER.
    const user = setup();
    const { rerender } = render(<OverviewTab />);

    await pickPeriod(user, /all time/i);
    expect(requestedMonths()).toBe(24);

    useReportYears.mockReturnValue({
      ...yearsResult,
      years: [2026, 1984],
    });
    rerender(<OverviewTab />);

    expect(requestedMonths()).toBe(516);
    expect(
      screen.getByRole('combobox', { name: /time period/i }),
    ).toHaveTextContent(/all time/i);
  });

  test('switching back to a fixed period after the years land keeps the trigger readable', async () => {
    useReportYears.mockReturnValue({
      ...yearsResult,
      years: [2026, 1984],
    });
    const user = setup();
    render(<OverviewTab />);

    await pickPeriod(user, /all time/i);
    expect(requestedMonths()).toBe(516);

    await pickPeriod(user, /6 months/i);

    expect(requestedMonths()).toBe(6);
    expect(
      screen.getByRole('combobox', { name: /time period/i }),
    ).toHaveTextContent(/6 months/i);
  });
});

describe('OverviewTab chart axes', () => {
  beforeEach(() => {
    useReportYears.mockReturnValue(yearsResult);
    useIncomeExpenses.mockReturnValue({
      data: [],
      loading: false,
      fetching: false,
      error: '',
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  // Tick thinning must be DERIVED from the bucket count, because both fixed
  // strategies are wrong at one end of the range:
  //
  //   interval={0}              one rotated <text> node per bucket — free at
  //                             12, the dominant render cost of the tab at the
  //                             516 buckets All time produces for a 1984 ledger.
  //   interval="preserveStartEnd"  pins first AND last, thins between. On the
  //                             AreaChart's point scale the last tick sits hard
  //                             against the right edge, so its neighbour loses
  //                             the minTickGap contest: a real 12-month window
  //                             at 598px rendered 11 ticks, May'26 -> Jul'26.
  //                             The June point was present and hoverable, only
  //                             its label gone — nothing looked broken.
  //
  // An earlier version of this test asserted data-interval === 'preserveStartEnd',
  // pinning the second bug as though it were the requirement. Assert the
  // requirement itself instead: every bucket labelled while they fit, bounded
  // after that.
  function seedMonths(count: number) {
    useIncomeExpenses.mockReturnValue({
      data: Array.from({ length: count }, (_, i) => ({
        year: 2026 - Math.floor((count - 1 - i) / 12),
        month: ((i % 12) + 1),
        income: 0,
        expenses: 0,
        net: 1,
      })),
      loading: false,
      fetching: false,
      error: '',
    });
  }

  /**
   * The x-axis tick labels the Net Cash Flow chart ACTUALLY rendered, in
   * document order.
   *
   * This used to read `data-interval` back off a stubbed `<XAxis>`, i.e. it
   * asserted the argument we passed to a component that did not exist. The
   * bug this suite exists for — a labelled bucket silently vanishing — lives
   * entirely on the other side of that stub, in recharts' own tick selection.
   * So query recharts' real output instead: `.recharts-cartesian-axis-tick-value`
   * is the `<text>` node it emits per rendered tick.
   *
   * Scoped by the card's `aria-labelledby`, because the tab renders three
   * charts and only this one is the AreaChart whose point scale dropped June.
   *
   * NOTE the selector: recharts 3 no longer nests the label `<text>` inside
   * `.recharts-xAxis`. It emits the tick LINES there and hoists the tick
   * LABELS into a separate `recharts-zIndex-layer_2000` group, so
   * `.recharts-xAxis .recharts-cartesian-axis-tick-value` matches NOTHING on
   * this version — it reads as "the axis rendered no labels", which is
   * indistinguishable from the regression this file is meant to catch. Query
   * the label group by its own class instead. (Found by dumping the real DOM
   * during this conversion; a wholesale mock cannot see a reparent like this
   * at all, which is the case for doing the conversion.)
   */
  function netCashFlowTicks(): string[] {
    const { container } = render(<OverviewTab />);
    const card = container.querySelector(
      '[aria-labelledby="net-cash-flow-heading"]',
    );
    if (!card) throw new Error('Net Cash Flow card did not render');

    // Positive control on the fixture itself: prove recharts drew SOMETHING
    // before reading labels off it, so "the chart did not render" reports as
    // itself rather than as an axis with nothing to thin.
    //
    // It is the bound-style assertions that need this. `toBeLessThan(516)` is
    // satisfied by an empty axis (0 < 516), so on its own it reads a blank
    // chart as a well-thinned one. The two `toEqual` literal-array assertions
    // do NOT need it — an empty list fails them outright — so this control is
    // the thing keeping the length bounds honest, not the label assertions.
    expect(card.querySelector('.recharts-surface')).not.toBeNull();

    const labels = card.querySelector('.recharts-xAxis-tick-labels');
    if (!labels) throw new Error('Net Cash Flow chart rendered no x-axis labels');

    return Array.from(
      labels.querySelectorAll('.recharts-cartesian-axis-tick-value'),
    ).map((el) => el.textContent?.trim() ?? '');
  }

  test('the Net Cash Flow axis labels every month of a 12-month window', () => {
    seedMonths(12);

    // The requirement, stated as the labels a reader sees: all twelve months,
    // in order, none skipped. `preserveStartEnd` rendered eleven of these and
    // jumped May'26 -> Jul'26; the data point was still there and still
    // hoverable, which is why nothing looked broken.
    //
    // The expectation is written out LITERALLY, and that is the whole point of
    // it. It used to be built by calling `formatMonthTick` — the same function
    // `<XAxis tickFormatter>` calls — so the oracle was the subject: mutating
    // `formatMonthTick` to `return 'XXX'` collapsed all twelve labels AND all
    // twelve expectations to the same string and this file stayed green at
    // 10/10. Literals derived by hand from the fixture (twelve buckets, months
    // 1-12, all of 2026 per `seedMonths`) are independent of it, so month
    // identity, ordering, distinctness and the `'YY` suffix are all actually
    // asserted rather than cancelling out.
    //
    // CAVEAT, measured during the conversion: this test alone does NOT kill an
    // `interval="preserveStartEnd"` mutant. That bug is width-driven — recharts
    // drops the neighbour that loses the `minTickGap` contest — and happy-dom
    // measures every <text> as zero-wide, so no collision ever fires here and
    // all twelve labels render either way. The All-time test below is what
    // kills that mutant (58 labels instead of 24). Do not read a green 12-month
    // case as proof the thinning strategy is right; only a browser can show
    // that. The browser measurements live in the header of
    // `chartAxis.seam.test.ts`.
    expect(netCashFlowTicks()).toEqual([
      "Jan'26",
      "Feb'26",
      "Mar'26",
      "Apr'26",
      "May'26",
      "Jun'26",
      "Jul'26",
      "Aug'26",
      "Sep'26",
      "Oct'26",
      "Nov'26",
      "Dec'26",
    ]);
  });

  test('the Net Cash Flow axis thins its ticks for an All-time window', () => {
    seedMonths(516);

    // WHAT THIS USED TO ASSERT, AND WHY IT CANNOT ANY MORE. Until this batch
    // the axis passed a DERIVED numeric interval, so 516 buckets under a
    // 24-label cap produced exactly 24 labels on a stride of 22 — a number this
    // harness could check, and it did, against 24 hand-derived month strings.
    //
    // The axis now passes `interval="equidistantPreserveStart"`, which asks
    // recharts to measure the plot and pick the largest stride that fits. That
    // is the fix for a defect this harness could never have seen: a fixed cap
    // is blind to the container, and `md:grid-cols-2` halves every chart on
    // this tab at 768px, so twelve labels measured 6.8px of clearance there
    // against the 11px their ink needs — worse than the phone. Measurements are
    // in the header of `chartAxis.seam.test.ts`.
    //
    // happy-dom performs no layout, so recharts measures every tick as 0x0, no
    // collision ever fires, and ALL 516 render here. That is an artefact of the
    // environment, not the behaviour: the same build renders 6 of 516 at 360px
    // and 12 at 700px in Chrome. So the count is asserted as the artefact it is
    // — proof the data still reaches the axis — and the thinning itself is
    // pinned as source text in `chartAxis.seam.test.ts` and measured in a
    // browser. Do NOT restore an assertion on the rendered count: under this
    // strategy it can only ever repeat what happy-dom failed to lay out.
    const ticks = netCashFlowTicks();

    // happy-dom performs no layout, so recharts measures every tick as 0x0 and
    // its collision arithmetic degenerates to `minTickGap` alone — it renders 52
    // of the 516 here, where the same build renders 6 at 360px and 12 at 700px
    // in Chrome. So the COUNT is an artefact of the environment and is not
    // asserted; what is asserted is the property that made this strategy the
    // right one, and it survives the degeneracy: the rendered set is a UNIFORM
    // stride anchored at the oldest bucket.
    //
    // That is the whole defect. `preserveEnd` — recharts' default, and what this
    // axis falls back to with the interval deleted — walks the ticks and drops
    // whichever ones its collision pass reaches, so the cadence changes mid-axis
    // and counting points off a label lands on the wrong month. Measured on the
    // sibling Budget vs Actual chart at 420px: nine of twelve, `Jan Feb Mar Apr
    // Jun Jul Sep Oct Dec`.
    expect(ticks.length).toBeGreaterThan(1);
    expect(ticks.length).toBeLessThan(516);

    // `seedMonths(516)` numbers bucket i as month (i % 12) + 1 of year
    // 2026 - floor((515 - i) / 12), so the buckets run Jan 1984 -> Dec 2026 and
    // no two-digit year is ambiguous: 84..99 is the 1900s, 00..26 the 2000s.
    // Decoding the LABELS back to bucket numbers is what makes this a check on
    // the axis rather than on the fixture.
    const SHORT = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
                   'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
    const bucketOf = (label: string): number => {
      const month = SHORT.indexOf(label.slice(0, 3));
      const yy = Number(label.slice(4));
      expect(month).toBeGreaterThanOrEqual(0);
      const year = yy >= 84 ? 1900 + yy : 2000 + yy;
      return (year - 1984) * 12 + month;
    };
    const buckets = ticks.map(bucketOf);

    // Anchored at the oldest bucket. `equidistantPreserveStart` pins index 0;
    // the End-anchored variant would not, which is one of the two mutants this
    // distinguishes.
    expect(buckets[0]).toBe(0);

    // And every gap identical — the assertion `preserveEnd` and
    // `preserveStartEnd` both fail, because they drop individuals rather than
    // striding.
    const strides = buckets.slice(1).map((b, i) => b - buckets[i]);
    expect(new Set(strides).size).toBe(1);
    expect(strides[0]).toBeGreaterThan(0);

    // The formatter is still pinned at the oldest end — a `formatMonthTick` that
    // stopped applying renders a raw ISO key here — and every label is distinct,
    // so a stride that doubled back or repeated a bucket shows up.
    expect(ticks[0]).toBe("Jan'84");
    expect(new Set(ticks).size).toBe(ticks.length);
  });
});

// A grid item defaults to min-width:auto, so the width of the Recharts SVG in
// a card becomes the track's min-content and widens the page — measured live at
// a 390px viewport, this tab rendered 599px (Spending, same shape, 3530px). The
// loop also latches: ResponsiveContainer then measures the widened track and
// grows to match, so only a fresh mount with the fix present loads correctly.
// `min-w-0` on the chart itself does NOT break it; it has to be on the item.
describe('OverviewTab phone width', () => {
  beforeEach(() => {
    useReportYears.mockReturnValue(yearsResult);
    useIncomeExpenses.mockReturnValue({
      data: [],
      loading: false,
      fetching: false,
      error: '',
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  // Asserted over every child the grid actually has, not a counted few, so a
  // new chart card added without the class fails here.
  test('every card in the root grid may shrink below its content', () => {
    const { container } = render(<OverviewTab />);
    const root = container.firstElementChild;
    if (!root) throw new Error('OverviewTab rendered nothing');

    // Premise. A flex column would be safe for a different reason (no
    // min-width:auto trap in the cross axis), so if the layout ever changes
    // this fails and the invariant below gets re-derived rather than skipped.
    expect(root).toHaveClass('grid');

    const cards = Array.from(root.children);
    expect(cards.length).toBeGreaterThan(0);
    for (const card of cards) {
      expect(card).toHaveClass('min-w-0');
    }
  });
});
