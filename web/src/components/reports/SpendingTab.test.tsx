import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/react';
import type { ReactNode } from 'react';

const listResult = { data: [], loading: false, fetching: false, error: '' };
// buildVelocityData takes `ExpenseVelocityData | null` and short-circuits on
// null, so this is the cheapest shape that renders the card without a fixture.
const nullResult = { data: null, loading: false, fetching: false, error: '' };

vi.mock('@/hooks/useReports', () => ({
  useCategoryBreakdown: () => listResult,
  useCategoryTrends: () => listResult,
  useTopMerchants: () => listResult,
  useExpenseVelocity: () => nullResult,
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

// Every name this file and `ui/chart.tsx` import from recharts has to be
// declared: a factory mock missing an export throws at import time, not at
// render. No chart body renders here (the hooks return empty data), so the
// stubs only need to exist.
vi.mock('recharts', () => ({
  BarChart: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  LineChart: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  ResponsiveContainer: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  Bar: () => <div />,
  Line: () => <div />,
  Cell: () => <div />,
  LabelList: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  CartesianGrid: () => <div />,
  ReferenceLine: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  Surface: () => <div />,
  Layer: () => <div />,
  Sector: () => <div />,
  Customized: () => <div />,
}));

import { SpendingTab } from './SpendingTab';

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
  afterEach(() => {
    cleanup();
  });

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
