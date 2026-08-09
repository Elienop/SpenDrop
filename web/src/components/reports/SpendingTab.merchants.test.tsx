import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import React from 'react';
import type {
  CategoryBreakdownItem,
  CategoryTrendEntry,
  ExpenseVelocityData,
  TopMerchantEntry,
} from '@/api/types';

// A SECOND file for this tab rather than a fixture change in `SpendingTab.test.tsx`.
// That file's `MERCHANTS` is empty on purpose — its own comment records that a
// populated Top Merchants table adds currency strings which collide with the
// bar-label assertions there. `vi.mock` is module-level, so the two fixtures
// cannot coexist in one file.
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    ResponsiveContainer: ({
      children,
    }: {
      children: React.ReactElement<{ width?: number; height?: number }>;
    }) => React.cloneElement(children, { width: 400, height: 300 }),
  };
});

/**
 * A 500-character unbroken token — the ceiling the per-row edit route enforces
 * (`internal/api/limits.go`), and the shape that was measured driving this
 * table to 3464px inside a 293px card. The import path does not enforce that
 * ceiling, so the real worst case is larger still; 500 is enough to distinguish
 * bounded from unbounded.
 *
 * A hostile token, not a long sentence: a sentence wraps at its spaces without
 * any help, so it would pass against unbounded markup and prove nothing.
 */
const HOSTILE = `MERCHANT-${'X'.repeat(491)}`;

const MERCHANTS: TopMerchantEntry[] = [
  { description: HOSTILE, tx_count: 4, total: 26.5 },
  { description: 'Corner Shop', tx_count: 2, total: 11 },
];

const BREAKDOWN: CategoryBreakdownItem[] = [
  { id: 2, name: 'Groceries', total: 300, limit: null, over: false },
];

const TRENDS: CategoryTrendEntry[] = [];

const VELOCITY: ExpenseVelocityData = {
  days_in_month: 2,
  budget: 0,
  current: [{ day: 1, daily_total: 10 }],
  previous: [{ day: 1, daily_total: 5 }],
};

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

describe('Top Merchants bounds a hostile description', () => {
  /**
   * WHAT THIS CANNOT DO. happy-dom lays nothing out, so it cannot see that the
   * table was 3464px wide, that the widest cell was 3269px, or that wrapping
   * alone would have replaced that with a 932px-tall row. Those are browser
   * measurements and they live in the comment at the call site. What is real
   * here is that the row renders the whole description, that the bounding
   * classes are on the elements that need them, and that the full text stays
   * reachable through `title` — three things a future edit can break silently.
   */
  function descriptionCell(): HTMLElement {
    render(<SpendingTab />);
    const cell = screen
      .getByRole('cell', { name: HOSTILE })
      .closest('td');
    if (!cell) throw new Error('no description cell rendered');
    return cell;
  }

  test('positive control: the full description reaches the DOM, untruncated', () => {
    // Everything below asserts about BOUNDING, which an empty cell would also
    // satisfy. This pins that the cell still carries all 500 characters — the
    // fix is presentational and must not shorten the string itself.
    const cell = descriptionCell();
    expect(cell.textContent).toBe(HOSTILE);
    expect(cell.textContent).toHaveLength(500);
  });

  test('the cell bounds its width where a table cell actually measures it', () => {
    const cell = descriptionCell();
    const inner = cell.querySelector('div');
    if (!inner) throw new Error('description is not wrapped in a bounding element');

    // On the INNER element, not the cell: `overflow-wrap` is what drops the
    // min-content contribution of an unbroken token, and Tailwind's
    // `break-words` would not — it breaks the token for painting and leaves the
    // measurement alone, which is exactly the trap this replaced.
    expect(inner.className).toContain('[overflow-wrap:anywhere]');
    // A cap for the ordinary case, so a merely long description cannot take the
    // whole card on a wide screen either.
    expect(cell.className).toMatch(/max-w-/);
  });

  test('the cell bounds its height, because wrapping trades one overflow for another', () => {
    const inner = descriptionCell().querySelector('div');
    expect(inner?.className).toContain('line-clamp-3');
  });

  test('the full text stays reachable through title where a pointer exists', () => {
    const inner = descriptionCell().querySelector('div');
    expect(inner?.getAttribute('title')).toBe(HOSTILE);
  });

  test('an ordinary description is untouched', () => {
    // The bound must not be visible on normal data — a short merchant name gets
    // no ellipsis, no break, no change.
    render(<SpendingTab />);
    expect(screen.getByRole('cell', { name: 'Corner Shop' }).textContent).toBe(
      'Corner Shop',
    );
  });
});
