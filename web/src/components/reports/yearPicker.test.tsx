import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import type { YoYResponse } from '@/api/types';

// Every year <Select> on the Reports page offers exactly the years the ledger
// holds. `monthsToCoverYear` proved that this class of bug lives at the CALL
// SITE, not in the helper: it was extracted and unit-tested while SavingsTab
// still passed a hardcoded 24 and the whole suite stayed green. So this file
// drives the real tabs and enumerates the real <SelectItem>s rather than
// unit-testing `yearOptionsFrom` in isolation.

const CURRENT_YEAR = 2026;

const yearsResult = {
  years: [CURRENT_YEAR],
  currentYear: CURRENT_YEAR,
  hasTransactions: true,
  outOfRangeYears: [] as number[],
  loading: false,
};
const useReportYears = vi.fn(() => yearsResult);
vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => useReportYears(),
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

/**
 * Every year <Select> on the Reports page, by the tab that owns it and the
 * accessible name of its trigger. Each case is driven for real, so a tab that
 * forgets to thread the ledger years through fails here.
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

/** Open the named year Select and click the option for `year`. */
async function selectYear(
  user: ReturnType<typeof userEvent.setup>,
  picker: (typeof YEAR_PICKERS)[number],
  year: number,
): Promise<void> {
  await user.click(screen.getByRole('combobox', { name: picker.label }));
  const listbox = await screen.findByRole('listbox');
  await user.click(
    within(listbox)
      .getAllByRole('option')
      .filter((el) => picker.optionYear(el.textContent ?? '') === year)[0],
  );
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

describe('the Reports year pickers offer exactly the ledger years', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    // Pinned so "the current year" is deterministic; otherwise every
    // expectation below silently changes meaning each January.
    vi.setSystemTime(new Date(Date.UTC(CURRENT_YEAR, 6, 15)));
    useReportYears.mockReturnValue(yearsResult);
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  for (const picker of YEAR_PICKERS) {
    describe(picker.tab, () => {
      test('offers exactly the years the ledger holds, gaps included', async () => {
        // The headline defect and its fix in one case. A 1984 statement
        // imports and 1984 must be selectable — but 2025 holds no rows, so it
        // must NOT be offered. The old contiguous floor gave 43 options here.
        useReportYears.mockReturnValue({
          ...yearsResult,
          years: [2026, 2024, 1984],
        });
        const user = setup();
        render(picker.render());

        expect(await offeredYears(user, picker)).toEqual([2026, 2024, 1984]);
      });

      test('offers exactly the current year when the ledger is empty', async () => {
        useReportYears.mockReturnValue({
          ...yearsResult,
          years: [CURRENT_YEAR],
          hasTransactions: false,
        });
        const user = setup();
        render(picker.render());

        expect(await offeredYears(user, picker)).toEqual([CURRENT_YEAR]);
      });

      test('is never empty while the years are still loading', async () => {
        useReportYears.mockReturnValue({
          ...yearsResult,
          years: [CURRENT_YEAR],
          hasTransactions: false,
          loading: true,
        });
        const user = setup();
        render(picker.render());

        expect(await offeredYears(user, picker)).toEqual([CURRENT_YEAR]);
      });

      test('keeps the selected year selectable after a refetch drops it', async () => {
        // Pick 1984, then let a refetch drop it (the last 1984 row was just
        // deleted, and SSE re-derived the list). Dropping 1984 from the
        // options would leave the Select holding a value with no matching
        // item — a BLANK TRIGGER, silently, with no error.
        useReportYears.mockReturnValue({
          ...yearsResult,
          years: [2026, 2024, 1984],
        });
        const user = setup();
        const { rerender } = render(picker.render());

        await selectYear(user, picker, 1984);

        useReportYears.mockReturnValue({
          ...yearsResult,
          years: [2026, 2024],
        });
        rerender(picker.render());

        expect(await offeredYears(user, picker)).toContain(1984);
      });

      test('still shows the selected year on the trigger after a refetch drops it', async () => {
        // The user-visible half of the case above: `toContain` alone would
        // still pass if the option existed but the trigger had gone blank.
        useReportYears.mockReturnValue({
          ...yearsResult,
          years: [2026, 2024, 1984],
        });
        const user = setup();
        const { rerender } = render(picker.render());

        await selectYear(user, picker, 1984);

        useReportYears.mockReturnValue({
          ...yearsResult,
          years: [2026, 2024],
        });
        rerender(picker.render());

        expect(
          screen.getByRole('combobox', { name: picker.label }),
        ).toHaveTextContent('1984');
      });
    });
  }

  test('years arriving after mount widen the picker without resetting the selection', async () => {
    // All four tabs select the current year on mount, so the first paint (with
    // the current-year fallback) already has a valid selection. The real list
    // landing afterwards must only ADD older options.
    useReportYears.mockReturnValue({
      ...yearsResult,
      years: [CURRENT_YEAR],
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

    useReportYears.mockReturnValue({
      ...yearsResult,
      years: [2026, 2024, 1984],
    });
    rerender(<SpendingTab />);

    const trigger = screen.getByRole('combobox', { name: /breakdown year/i });
    expect(trigger).toHaveTextContent(String(CURRENT_YEAR));
    await user.click(trigger);
    expect(
      within(await screen.findByRole('listbox')).getAllByRole('option'),
    ).toHaveLength(3);
  });

  test('PatternsTab keeps BOTH of its selections alive in the shared list', async () => {
    // One list feeds the heatmap `year` and the tag `tagYear`. A union that
    // folded in only one selection would leave the other Select blank the
    // moment the ledger narrowed — and only one of the two would show it, so
    // a single-Select assertion would go green through the bug.
    useReportYears.mockReturnValue({
      ...yearsResult,
      years: [2026, 2024, 1984],
    });
    const user = setup();
    const heatmap = YEAR_PICKERS.find((p) => p.tab === 'Patterns (heatmap)')!;
    const tagsPicker = YEAR_PICKERS.find((p) => p.tab === 'Patterns (tags)')!;
    const { rerender } = render(<PatternsTab />);

    await selectYear(user, heatmap, 1984);
    await selectYear(user, tagsPicker, 2024);

    // Both selected years leave the ledger at once.
    useReportYears.mockReturnValue({ ...yearsResult, years: [2026] });
    rerender(<PatternsTab />);

    expect(
      screen.getByRole('combobox', { name: heatmap.label }),
    ).toHaveTextContent('1984');
    expect(
      screen.getByRole('combobox', { name: tagsPicker.label }),
    ).toHaveTextContent('2024');
    expect(await offeredYears(user, heatmap)).toEqual([2026, 2024, 1984]);
  });
});
