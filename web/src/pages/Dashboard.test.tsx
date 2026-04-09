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
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  CartesianGrid: () => <div />,
  PieChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Pie: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Cell: () => <div />,
  // Referenced by shadcn's chart.tsx namespace import (ChartLegend, ChartStyle, tooltip plumbing)
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
    trend: [
      { year: 2026, month: 3, total_spent: 2800, total_income: 4200 },
      { year: 2026, month: 4, total_spent: 3200, total_income: 4500 },
    ],
    categories: [
      { id: 1, name: 'Food', color: '#818CF8', total: 1200 },
      { id: 2, name: 'Transport', color: '#7EC89B', total: 800 },
    ],
    loading: false,
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
          category_color: '#818CF8',
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
    });
  });

  test('renders Cash Flow section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Cash Flow')).toBeInTheDocument();
    });
  });

  test('renders Spending and Recent Transactions sections', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Spending by Category')).toBeInTheDocument();
      expect(screen.getByText('Recent Transactions')).toBeInTheDocument();
    });
  });

  test('renders Savings Progress section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Savings Progress')).toBeInTheDocument();
      expect(screen.getByText('Saved YTD')).toBeInTheDocument();
      expect(screen.getByText('Annual Goal')).toBeInTheDocument();
    });
  });

  test('does not render removed Monthly Budget section', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Monthly Budget')).not.toBeInTheDocument();
  });

  test('renders 6M and 12M toggle buttons', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByRole('tab', { name: '6M' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '12M' })).toBeInTheDocument();
  });
});
