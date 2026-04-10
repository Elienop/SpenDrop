import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import React from 'react';

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

vi.mock('../hooks/useDashboard', () => ({
  useDashboard: () => ({
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
  }),
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
});
