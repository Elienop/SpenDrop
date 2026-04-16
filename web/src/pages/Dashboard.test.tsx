import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import React from 'react';
import { STORAGE_KEYS } from '@/lib/storage-keys';

// Recharts mock — Recharts ships as ES modules that don't render in jsdom
// without a ResizeObserver, so every component is stubbed to a passthrough.
//
// IMPORTANT: shadcn's `src/components/ui/chart.tsx` does:
//
//     import * as RechartsPrimitive from "recharts"
//     const ChartLegend = RechartsPrimitive.Legend
//     // and ChartStyle reads RechartsPrimitive.* directly
//
// Because it's a namespace import, any name referenced at module-eval time
// that's missing from this mock becomes `undefined` and React crashes with
// "Element type is invalid: expected a string or a class/function but got:
// undefined" as soon as ChartContainer mounts. The mock below therefore
// declares *every* Recharts surface shadcn's chart helper can touch, not
// just the ones Dashboard.tsx uses directly. Add new stubs here whenever a
// future chart primitive starts pulling in more Recharts exports.
vi.mock('recharts', () => ({
  // Used by Dashboard.tsx
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  CartesianGrid: () => <div />,
  // Referenced by shadcn's chart.tsx namespace import (ChartLegend, ChartStyle, tooltip plumbing)
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  YAxis: () => <div />,
  Cell: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  Surface: () => <div />,
  Layer: () => <div />,
  Sector: () => <div />,
  LabelList: () => <div />,
  Customized: () => <div />,
  ReferenceLine: () => <div />,
}));

const defaultDashboardData = {
  summary: {
    budget: 5000,
    total_spent: 3200,
    total_income: 4500,
    remaining: 1300,
    savings_this_month: 500,
    savings_goal: 10000,
    savings_ytd: 7500,
    savings_goal_progress: 75,
  },
  // Backend returns trend newest-first (see dashboard_handlers.go) — keep
  // this fixture in the same order so chartData's `.reverse()` produces
  // chronologically-sorted bars.
  trend: [
    { year: 2026, month: 4, total_spent: 3200, total_income: 4500 },
    { year: 2026, month: 3, total_spent: 2800, total_income: 4200 },
  ],
  categories: [
    { id: 1, name: 'Food', total: 1200 },
    { id: 2, name: 'Transport', total: 800 },
  ],
  loading: false,
  fetching: false,
  error: '',
};

const mockUseDashboard = vi.fn(() => defaultDashboardData);

vi.mock('../hooks/useDashboard', () => ({
  useDashboard: (...args: unknown[]) => mockUseDashboard(...(args as [])),
}));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn().mockResolvedValue({
      transactions: [
        {
          id: 1,
          user_id: 1,
          date: '2026-04-01',
          amount: 42.50,
          original_amount: null,
          original_currency: null,
          description: 'Groceries',
          category_id: 1,
          category_name: 'Food',
          category_type: 'expense',
          tags: null,
          notes: null,
          created_at: '2026-04-01T00:00:00Z',
          updated_at: '2026-04-01T00:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      per_page: 6,
      total_pages: 1,
    }),
  },
}));

vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({
    user: { id: 1, username: 'elie', display_name: 'Elie' },
    isAuthenticated: true,
    loading: false,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  }),
}));

import { Dashboard } from './Dashboard';

describe('Dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('renders welcome heading with user name', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/Welcome back, Elie/);
  });

  test('renders month/year selectors', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByLabelText(/month/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
  });

  test('renders KPI cards with Total Balance', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Total Balance')).toBeInTheDocument();
      expect(screen.getAllByText('Income').length).toBeGreaterThanOrEqual(1);
      expect(screen.getAllByText('Expenses').length).toBeGreaterThanOrEqual(1);
      expect(screen.getByText('Savings Rate')).toBeInTheDocument();
      // Assert the KPI row actually rendered values — "Income" / "Expenses"
      // strings also appear in the cash-flow chart config, so label-only
      // assertions would pass even if the KPI row were missing. These
      // formatted dollar splits (4500-3200=1300) are unique to KPI cards.
      expect(screen.getByText('$1,300.00')).toBeInTheDocument();
      expect(screen.getByText('$4,500.00')).toBeInTheDocument();
      expect(screen.getByText('$3,200.00')).toBeInTheDocument();
    });
  });

  test('renders negative Total Balance with minus sign when expenses exceed income', async () => {
    mockUseDashboard.mockReturnValue({
      ...defaultDashboardData,
      summary: {
        ...defaultDashboardData.summary,
        total_income: 2000,
        total_spent: 3598.9,
        remaining: -1598.9,
      },
    });
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      // Regression guard for the "Total Balance" KPI: when expenses exceed
      // income, the card must surface the sign. Previous code called
      // `Math.abs()` in `splitCurrency`, which silently stripped the sign
      // and rendered `$1,598.90` — identical to a positive balance.
      expect(screen.getByText('-$1,598.90')).toBeInTheDocument();
    });
  });

  test('renders Cash Flow section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Cash Flow')).toBeInTheDocument();
    });
  });

  test('renders Spending by Category section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Spending by Category')).toBeInTheDocument();
    });
  });

  test('renders Recent Transactions in a table layout', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Recent Transactions')).toBeInTheDocument();
      // Should use a table element instead of a list
      expect(screen.getByRole('table')).toBeInTheDocument();
      // Should have a "View All" link
      expect(screen.getByRole('link', { name: /view all/i })).toBeInTheDocument();
    });
  });

  test('renders transaction data in table rows', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      // The mock transaction "Groceries" should appear in a table row
      expect(screen.getByText('Groceries')).toBeInTheDocument();
      // "Food" appears in both the category bars and the transaction table
      expect(screen.getAllByText('Food').length).toBeGreaterThanOrEqual(2);
      // Category initial should appear in the icon cell
      expect(screen.getByText('F')).toBeInTheDocument();
    });
  });

  test('does not render Savings Progress section', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Savings Progress')).not.toBeInTheDocument();
    expect(screen.queryByText('Saved YTD')).not.toBeInTheDocument();
    expect(screen.queryByText('Annual Goal')).not.toBeInTheDocument();
  });

  test('does not render removed Monthly Budget section', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Monthly Budget')).not.toBeInTheDocument();
  });

  test('renders 6M and 12M toggle buttons inside a ButtonGroup', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    const btn6 = screen.getByRole('button', { name: '6M' });
    const btn12 = screen.getByRole('button', { name: '12M' });
    expect(btn6).toBeInTheDocument();
    expect(btn12).toBeInTheDocument();
    // Both buttons should be inside a group container
    const groups = screen.getAllByRole('group');
    const cashFlowGroup = groups.find((g) => g.contains(btn6));
    expect(cashFlowGroup).toBeDefined();
    expect(cashFlowGroup).toContainElement(btn12);
  });

  test('does not show "Other" category or "Show more" when <= 6 categories', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Other')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /show more/i })).not.toBeInTheDocument();
  });

  describe('Today button', () => {
    beforeEach(() => {
      // Each test seeds localStorage with a specific month/year, so clear
      // it up-front to avoid leaking state from earlier tests in this file.
      localStorage.clear();
    });

    test('is disabled when already viewing the current month', () => {
      // The "Today" control is an escape hatch, so it should go dark when
      // the user is already on today's month — otherwise it flashes an
      // action that does nothing.
      const now = new Date();
      localStorage.setItem(STORAGE_KEYS.dashboardYear, String(now.getFullYear()));
      localStorage.setItem(STORAGE_KEYS.dashboardMonth, String(now.getMonth() + 1));
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      expect(screen.getByRole('button', { name: /today/i })).toBeDisabled();
    });

    test('is enabled when viewing a past month', () => {
      localStorage.setItem(STORAGE_KEYS.dashboardYear, '2024');
      localStorage.setItem(STORAGE_KEYS.dashboardMonth, '3');
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      expect(screen.getByRole('button', { name: /today/i })).not.toBeDisabled();
    });

    test('resets to the current month when clicked', async () => {
      const user = userEvent.setup();
      localStorage.setItem(STORAGE_KEYS.dashboardYear, '2024');
      localStorage.setItem(STORAGE_KEYS.dashboardMonth, '3');
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      const todayBtn = screen.getByRole('button', { name: /today/i });
      expect(todayBtn).not.toBeDisabled();
      await user.click(todayBtn);
      // After the click the button flips to disabled — the state change
      // is what the test proves; no need to poke into localStorage.
      await waitFor(() => expect(todayBtn).toBeDisabled());
    });
  });

  describe('with more than 6 categories', () => {
    const manyCategories = [
      { id: 1, name: 'Food', total: 1200 },
      { id: 2, name: 'Transport', total: 800 },
      { id: 3, name: 'Housing', total: 700 },
      { id: 4, name: 'Entertainment', total: 500 },
      { id: 5, name: 'Healthcare', total: 400 },
      { id: 6, name: 'Utilities', total: 300 },
      { id: 7, name: 'Shopping', total: 200 },
      { id: 8, name: 'Education', total: 100 },
    ];

    beforeEach(() => {
      mockUseDashboard.mockReturnValue({
        ...defaultDashboardData,
        categories: manyCategories,
      });
    });

    test('shows first 6 categories and hides the rest when collapsed', () => {
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      // First 6 should be visible
      expect(screen.getByText('Food')).toBeInTheDocument();
      expect(screen.getByText('Transport')).toBeInTheDocument();
      expect(screen.getByText('Housing')).toBeInTheDocument();
      expect(screen.getByText('Entertainment')).toBeInTheDocument();
      expect(screen.getByText('Healthcare')).toBeInTheDocument();
      expect(screen.getByText('Utilities')).toBeInTheDocument();
      // 7th and 8th should NOT be visible
      expect(screen.queryByText('Shopping')).not.toBeInTheDocument();
      expect(screen.queryByText('Education')).not.toBeInTheDocument();
      // No "Other" bucket
      expect(screen.queryByText('Other')).not.toBeInTheDocument();
    });

    test('shows "Show more" button when more than 6 categories', () => {
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      expect(screen.getByRole('button', { name: /show more/i })).toBeInTheDocument();
    });

    test('expands to show all categories and "Show less" on click', async () => {
      const user = userEvent.setup();
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      const showMoreBtn = screen.getByRole('button', { name: /show more/i });
      await user.click(showMoreBtn);
      // All 8 categories should now be visible
      expect(screen.getByText('Shopping')).toBeInTheDocument();
      expect(screen.getByText('Education')).toBeInTheDocument();
      // "Show more" replaced by "Show less"
      expect(screen.queryByRole('button', { name: /show more/i })).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: /show less/i })).toBeInTheDocument();
    });

    test('collapses back to 6 categories when "Show less" is clicked', async () => {
      const user = userEvent.setup();
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      // Expand
      await user.click(screen.getByRole('button', { name: /show more/i }));
      // Collapse
      await user.click(screen.getByRole('button', { name: /show less/i }));
      // 7th and 8th hidden again
      expect(screen.queryByText('Shopping')).not.toBeInTheDocument();
      expect(screen.queryByText('Education')).not.toBeInTheDocument();
      // "Show more" is back
      expect(screen.getByRole('button', { name: /show more/i })).toBeInTheDocument();
    });
  });
});
