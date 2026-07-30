import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import type { YoYResponse } from '@/api/types';

// Every year <Select> on the Reports page counts down from the ledger-derived
// floor. `monthsToCoverYear` proved that the bug lives at the CALL SITE, not
// in the helper: it was extracted and unit-tested while SavingsTab still passed
// a hardcoded 24 and the whole suite stayed green. So this file drives the real
// tabs and enumerates the real <SelectItem>s rather than unit-testing
// `yearOptions` in isolation.

const floorResult = {
  floorYear: 2026,
  hasTransactions: true,
  clamped: false,
  loading: false,
};
const useReportYearFloor = vi.fn(() => floorResult);
vi.mock('@/hooks/useReportYearFloor', () => ({
  useReportYearFloor: () => useReportYearFloor(),
}));

const reportResult = <T,>(data: T) => ({
  data,
  loading: false,
  fetching: false,
  error: '',
});

const emptyYoY: YoYResponse = {
  current_year: 2026,
  previous_year: 2025,
  current: [],
  previous: [],
};

vi.mock('@/hooks/useReports', () => ({
  useYearOverYear: () => reportResult<YoYResponse | null>(emptyYoY),
  useIncomeExpenses: () => reportResult([]),
  useCategoryTrends: () => reportResult([]),
  useTopMerchants: () => reportResult([]),
  useBudgetVsActual: () => reportResult([]),
  useExpenseVelocity: () => reportResult(null),
  useSpendingHeatmap: () => reportResult([]),
  useTagBreakdown: () => reportResult([]),
  useCategoryBreakdown: () => reportResult([]),
  useRecurring: () => ({ ...reportResult([]), refetch: vi.fn() }),
  dismissRecurring: vi.fn(),
}));

vi.mock('@/hooks/useBaseCurrency', () => ({ useBaseCurrency: () => 'USD' }));

vi.mock('@/api/client', () => ({
  api: { get: vi.fn(async () => []), post: vi.fn(async () => ({})) },
}));

import { OverviewTab } from './OverviewTab';
import { SpendingTab } from './SpendingTab';
import { SavingsTab } from './SavingsTab';
import { PatternsTab } from './PatternsTab';

const CURRENT_YEAR = 2026;

/**
 * Every year <Select> on the Reports page, by the tab that owns it and the
 * accessible name of its trigger. Each case is driven for real, so a tab that
 * forgets to thread the floor through fails here.
 */
const YEAR_PICKERS: {
  tab: string;
  render: () => ReactElement;
  label: RegExp;
  /** Year-over-Year renders "2026 vs 2025", so the option text is not the
   *  bare year — match on the leading year instead. */
  optionYear: (name: string) => number;
}[] = [
  {
    tab: 'Overview',
    render: () => <OverviewTab />,
    label: /budget year/i,
    optionYear: Number,
  },
  {
    tab: 'Spending',
    render: () => <SpendingTab />,
    label: /breakdown year/i,
    optionYear: Number,
  },
  {
    tab: 'Savings',
    render: () => <SavingsTab />,
    label: /savings year/i,
    optionYear: Number,
  },
  {
    tab: 'Savings (year-over-year)',
    render: () => <SavingsTab />,
    label: /year-over-year year/i,
    optionYear: (name) => Number(name.split(' ')[0]),
  },
  {
    tab: 'Patterns (heatmap)',
    render: () => <PatternsTab />,
    label: /heatmap year/i,
    optionYear: Number,
  },
  {
    tab: 'Patterns (tags)',
    render: () => <PatternsTab />,
    label: /tag year/i,
    optionYear: Number,
  },
];

/** Open the named year Select and return the years it offers, newest first. */
async function offeredYears(
  user: ReturnType<typeof userEvent.setup>,
  picker: (typeof YEAR_PICKERS)[number],
): Promise<number[]> {
  await user.click(screen.getByRole('combobox', { name: picker.label }));
  const listbox = await screen.findByRole('listbox');
  return within(listbox)
    .getAllByRole('option')
    .map((el) => picker.optionYear(el.textContent ?? ''));
}

function setup() {
  // pointerEventsCheck is off for the same reason Reports.test.tsx turns it
  // off: an open Radix Select sets `pointer-events: none` on <body>, and
  // happy-dom has no layout engine to tell the portalled content apart.
  return userEvent.setup({
    advanceTimers: vi.advanceTimersByTime,
    pointerEventsCheck: 0,
  });
}

describe('the Reports year pickers follow the ledger-derived floor', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    // Pinned so "the current year" is deterministic; otherwise every
    // expectation below silently changes meaning each January.
    vi.setSystemTime(new Date(Date.UTC(CURRENT_YEAR, 6, 15)));
    useReportYearFloor.mockReturnValue(floorResult);
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  for (const picker of YEAR_PICKERS) {
    describe(picker.tab, () => {
      test('offers every year from the ledger floor to the current year', async () => {
        // The headline defect: a 2019 bank statement imports, lands in every
        // aggregate, and 2019 was never selectable because the floor was the
        // hardcoded HISTORICAL_YEAR_START = 2024.
        useReportYearFloor.mockReturnValue({
          ...floorResult,
          floorYear: 2019,
        });
        const user = setup();
        render(picker.render());

        const years = await offeredYears(user, picker);

        expect(years).toEqual([
          2026, 2025, 2024, 2023, 2022, 2021, 2020, 2019,
        ]);
      });

      test('offers exactly the current year when the ledger is empty', async () => {
        useReportYearFloor.mockReturnValue({
          ...floorResult,
          floorYear: CURRENT_YEAR,
          hasTransactions: false,
        });
        const user = setup();
        render(picker.render());

        expect(await offeredYears(user, picker)).toEqual([CURRENT_YEAR]);
      });

      test('is never empty while the floor is still loading', async () => {
        useReportYearFloor.mockReturnValue({
          ...floorResult,
          floorYear: CURRENT_YEAR,
          hasTransactions: false,
          loading: true,
        });
        const user = setup();
        render(picker.render());

        expect(await offeredYears(user, picker)).toEqual([CURRENT_YEAR]);
      });

      test('keeps the selected year selectable if the floor later narrows past it', async () => {
        // Pick 2019, then let a refetch narrow the floor to 2024 (e.g. the
        // 2019 rows were deleted). Dropping 2019 from the list would leave the
        // Select holding a value with no matching item — a blank trigger and
        // an unreadable picker.
        useReportYearFloor.mockReturnValue({
          ...floorResult,
          floorYear: 2019,
        });
        const user = setup();
        const { rerender } = render(picker.render());

        await user.click(
          screen.getByRole('combobox', { name: picker.label }),
        );
        const listbox = await screen.findByRole('listbox');
        await user.click(
          within(listbox)
            .getAllByRole('option')
            .filter((el) => picker.optionYear(el.textContent ?? '') === 2019)[0],
        );

        useReportYearFloor.mockReturnValue({
          ...floorResult,
          floorYear: 2024,
        });
        rerender(picker.render());

        expect(await offeredYears(user, picker)).toContain(2019);
      });
    });
  }

  test('a floor arriving after mount widens the picker without resetting the selection', async () => {
    // All four tabs select the current year on mount, so the first paint (with
    // the current-year fallback) already has a valid selection. The floor
    // landing afterwards must only ADD older options.
    useReportYearFloor.mockReturnValue({
      ...floorResult,
      floorYear: CURRENT_YEAR,
      hasTransactions: false,
      loading: true,
    });
    const user = setup();
    const { rerender } = render(<SpendingTab />);

    await user.click(screen.getByRole('combobox', { name: /breakdown year/i }));
    expect(
      within(await screen.findByRole('listbox')).getAllByRole('option'),
    ).toHaveLength(1);
    await user.keyboard('{Escape}');

    // Now pick 2026 explicitly, then let the real floor arrive.
    useReportYearFloor.mockReturnValue({ ...floorResult, floorYear: 2019 });
    rerender(<SpendingTab />);

    const trigger = screen.getByRole('combobox', { name: /breakdown year/i });
    expect(trigger).toHaveTextContent(String(CURRENT_YEAR));
    await user.click(trigger);
    expect(
      within(await screen.findByRole('listbox')).getAllByRole('option'),
    ).toHaveLength(8);
  });
});
