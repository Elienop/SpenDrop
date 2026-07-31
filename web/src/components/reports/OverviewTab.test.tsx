import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';

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

// Recharts is stubbed so the XAxis configuration is inspectable. shadcn's
// `ui/chart.tsx` does `import * as RechartsPrimitive from "recharts"` and
// reads names at module-eval time, so every surface it can touch must be
// declared here or ChartContainer crashes with "Element type is invalid".
vi.mock('recharts', () => ({
  BarChart: ({ children }: { children: ReactNode }) => (
    <div data-chart="bar">{children}</div>
  ),
  AreaChart: ({ children }: { children: ReactNode }) => (
    <div data-chart="area">{children}</div>
  ),
  Bar: () => <div />,
  Area: () => <div />,
  XAxis: ({
    interval,
    minTickGap,
  }: {
    interval?: number | string;
    minTickGap?: number;
  }) => (
    <div
      data-testid="xaxis"
      data-interval={String(interval)}
      data-min-tick-gap={String(minTickGap)}
    />
  ),
  YAxis: () => <div />,
  CartesianGrid: () => <div />,
  ReferenceLine: () => <div />,
  ResponsiveContainer: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  Cell: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  Surface: () => <div />,
  Layer: () => <div />,
  Sector: () => <div />,
  LabelList: () => <div />,
  Customized: () => <div />,
}));

import { OverviewTab } from './OverviewTab';
import { monthsToCoverYear, MAX_AXIS_TICKS, MAX_REPORT_MONTHS } from './utils';

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

  function cashFlowInterval(): number {
    const { container } = render(<OverviewTab />);
    const areaChart = container.querySelector('[data-chart="area"]');
    expect(areaChart).not.toBeNull();
    const axis = within(areaChart as HTMLElement).getByTestId('xaxis');
    return Number(axis.getAttribute('data-interval'));
  }

  test('the Net Cash Flow axis labels every month of a 12-month window', () => {
    seedMonths(12);
    // interval 0 = every bucket. This is the assertion the old test inverted,
    // and the one that would have caught the missing June.
    expect(cashFlowInterval()).toBe(0);
  });

  test('the Net Cash Flow axis thins its ticks for an All-time window', () => {
    seedMonths(516);
    const interval = cashFlowInterval();
    expect(interval).toBeGreaterThan(0);
    // Bounded, not merely thinned: at most MAX_AXIS_TICKS labels render.
    expect(Math.ceil(516 / (interval + 1))).toBeLessThanOrEqual(MAX_AXIS_TICKS);
  });
});
