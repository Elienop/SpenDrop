import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import React from 'react';

vi.mock('../hooks/useChartTheme', () => ({
  useChartTheme: () => ({
    axisStroke: '#58585F',
    gridStroke: '#1E1E23',
    tooltipBg: '#1E1E23',
    tooltipBorder: '#2A2A30',
    tooltipText: '#F5F5F6',
    hoverBg: 'rgba(129,140,248,0.08)',
    incomeColor: '#7EC89B',
    expenseColor: '#E88B9C',
    categoryColors: ['#818CF8', '#7EC89B', '#E88B9C', '#E8A87C', '#7CAFD4', '#58585F'],
  }),
}));

vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  CartesianGrid: () => <div />,
  Tooltip: () => <div />,
  ReferenceLine: () => <div />,
  PieChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Pie: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Cell: () => <div />,
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
      { id: 1, name: 'Food', color: '#818CF8', total: 1200, transaction_count: 15 },
      { id: 2, name: 'Transport', color: '#7EC89B', total: 800, transaction_count: 8 },
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
          amount: 42.50,
          description: 'Groceries',
          date: '2026-04-01',
          category_name: 'Food',
          category_type: 'expense',
          category_color: '#818CF8',
          currency_code: 'USD',
        },
      ],
      total: 1,
      page: 1,
      per_page: 6,
      total_pages: 1,
    }),
  },
}));

vi.mock('../components/ChartTooltip', () => ({
  ChartTooltip: () => <div />,
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

vi.mock('../hooks/useChartPatterns', () => ({
  useChartPatterns: () => ({
    cashFlow: {
      income: { fill: '#5347CE', legendStyle: {} },
      expense: { fill: 'url(#stripe)', stroke: '#5347CE', strokeWidth: 1.5, legendStyle: {} },
    },
    getCategoryPattern: () => ({ fill: '#5347CE', legendStyle: {} }),
    getCategoryDefs: () => [],
    buildStyleMap: () => ({}),
    ChartPatternDefs: () => null,
  }),
  ChartPatternDefs: () => null,
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
      expect(screen.getByText('of goal')).toBeInTheDocument();
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
    expect(screen.getByText('6M')).toBeInTheDocument();
    expect(screen.getByText('12M')).toBeInTheDocument();
  });

});
