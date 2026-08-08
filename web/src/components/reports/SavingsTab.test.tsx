import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { IncomeExpenseEntry, YoYResponse } from '@/api/types';

const useIncomeExpenses = vi.fn();
const useYearOverYear = vi.fn();
vi.mock('@/hooks/useReports', () => ({
  useIncomeExpenses: (months: number) => useIncomeExpenses(months),
  useYearOverYear: (year: number) => useYearOverYear(year),
}));

const apiGet = vi.fn();
vi.mock('@/api/client', () => ({
  api: { get: (...args: unknown[]) => apiGet(...args) },
}));

vi.mock('@/hooks/useBaseCurrency', () => ({
  useBaseCurrency: () => 'USD',
}));

// The year Select offers exactly the years the ledger holds. 2024 is included
// so the option this test picks exists; `yearPicker.test.tsx` is what proves
// the tab actually threads the ledger years through.
vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => ({
    years: [2026, 2024, 2019],
    currentYear: 2026,
    hasTransactions: true,
    outOfRangeYears: [],
    futureYears: [],
    loading: false,
  }),
}));

import { SavingsTab } from './SavingsTab';
import { monthsToCoverYear } from './utils';

function reportResult<T>(data: T) {
  return { data, loading: false, fetching: false, error: '' };
}

const emptyIncExp: IncomeExpenseEntry[] = [];
const emptyYoY: YoYResponse = {
  current_year: 2026,
  previous_year: 2025,
  current: [],
  previous: [],
};

describe('SavingsTab report window', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    // Pinned so both yearOptions() and the expected window are deterministic;
    // otherwise this test silently changes meaning every January.
    vi.setSystemTime(new Date(Date.UTC(2026, 6, 15)));
    useIncomeExpenses.mockReturnValue(reportResult(emptyIncExp));
    useYearOverYear.mockReturnValue(reportResult(emptyYoY));
    apiGet.mockResolvedValue([]);
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  // The bug lived at the call site, not in the helper: monthsToCoverYear was
  // extracted and unit-tested, but reverting SavingsTab's `months` back to a
  // hardcoded 24 left the entire frontend suite green. This pins the wiring.
  test('asks for a window that covers the selected year, not a fixed 24', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<SavingsTab />);

    // Current year first: the floor is still 24.
    expect(useIncomeExpenses).toHaveBeenCalledWith(24);

    await user.click(screen.getByRole('combobox', { name: /savings year/i }));
    await user.click(await screen.findByRole('option', { name: '2024' }));

    // 2024 is two years back from the pinned 2026, so the window must widen to
    // 36 months to reach January 2024. A hardcoded 24 would have started the
    // curve in August 2024 and measured goal progress against five months.
    expect(monthsToCoverYear(2024, 2026)).toBe(36);
    expect(useIncomeExpenses).toHaveBeenCalledWith(36);
  });
});

// SavingsTab needs no `min-w-0`: its cards are children of a flex COLUMN, and
// the automatic-minimum-size trap that let a Recharts SVG widen the page in
// OverviewTab and SpendingTab (599px and 3530px at a 390px viewport) applies
// to a grid track and to a flex MAIN axis — not to the cross axis, where a
// column item is stretched to the container instead of sized by its content.
// This pins that premise: turn the root into a grid and this fails, which is
// the moment min-w-0 becomes required here too.
describe('SavingsTab phone width', () => {
  beforeEach(() => {
    useIncomeExpenses.mockReturnValue(reportResult(emptyIncExp));
    useYearOverYear.mockReturnValue(reportResult(emptyYoY));
    apiGet.mockResolvedValue([]);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  test('lays its cards out in a flex column, not a grid', () => {
    const { container } = render(<SavingsTab />);
    const root = container.firstElementChild;
    if (!root) throw new Error('SavingsTab rendered nothing');
    expect(root).toHaveClass('flex', 'flex-col');
    expect(root).not.toHaveClass('grid');
  });
});
