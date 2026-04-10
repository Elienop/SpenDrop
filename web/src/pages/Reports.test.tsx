import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { api } from '../api/client';
import type {
  YoYResponse,
  CategoryTrendEntry,
  IncomeExpenseEntry,
  TopMerchantEntry,
} from '../api/types';

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

// Mock the shadcn chart primitive so the component tree renders without
// measuring the DOM in happy-dom. Each wrapper just renders its children.
vi.mock('@/components/ui/chart', () => ({
  ChartContainer: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="chart-container">{children}</div>
  ),
  ChartTooltip: () => <div />,
  ChartTooltipContent: () => <div />,
  ChartLegend: () => <div />,
  ChartLegendContent: () => <div />,
}));

// Mock recharts primitives used directly by the Reports page. The shadcn
// chart primitive wraps these but the page still imports Bar/BarChart/Line
// etc. directly from recharts.
vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="responsive-container">{children}</div>
  ),
  BarChart: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="bar-chart">{children}</div>
  ),
  Bar: () => <div />,
  LineChart: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="line-chart">{children}</div>
  ),
  Line: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  CartesianGrid: () => <div />,
}));

import { Reports } from './Reports';

const mockYoY = {
  current_year: 2026,
  previous_year: 2025,
  current: Array.from({ length: 12 }, (_, i) => ({
    month: i + 1, expenses: 1000 + i * 100, income: 2000,
  })),
  previous: Array.from({ length: 12 }, (_, i) => ({
    month: i + 1, expenses: 900 + i * 80, income: 1800,
  })),
} satisfies YoYResponse;

const mockCatTrends = {
  categories: [
    {
      id: 1,
      name: 'Food',
      type: 'expense',
      data: [{ year: 2026, month: 1, total: 500 }],
    },
  ],
} satisfies { categories: CategoryTrendEntry[] };

const mockIncExp = {
  data: Array.from({ length: 12 }, (_, i) => ({
    year: 2026, month: i + 1, income: 2000, expenses: 1500, net: 500,
  })),
} satisfies { data: IncomeExpenseEntry[] };

const mockMerchants = {
  merchants: [
    { description: 'Grocery Store', tx_count: 8, total: 450.50 },
    { description: 'Gas Station', tx_count: 4, total: 200.00 },
  ],
} satisfies { merchants: TopMerchantEntry[] };

beforeEach(() => {
  vi.mocked(api.get).mockImplementation((path: string) => {
    if (path.startsWith('reports/year-over-year')) return Promise.resolve(mockYoY);
    if (path.startsWith('reports/category-trends')) return Promise.resolve(mockCatTrends);
    if (path.startsWith('reports/income-expenses')) return Promise.resolve(mockIncExp);
    if (path.startsWith('reports/top-merchants')) return Promise.resolve(mockMerchants);
    return Promise.reject(new Error('unknown path'));
  });
});

function renderReports() {
  return render(
    <MemoryRouter>
      <Reports />
    </MemoryRouter>,
  );
}

describe('Reports', () => {
  it('renders the page heading', () => {
    renderReports();
    expect(
      screen.getByRole('heading', { level: 1, name: 'Reports' }),
    ).toBeInTheDocument();
  });

  it('renders all four section headings', async () => {
    renderReports();
    await waitFor(() => {
      expect(screen.getByText('Year-over-Year Comparison')).toBeInTheDocument();
      expect(screen.getByText('Income vs Expenses')).toBeInTheDocument();
      expect(screen.getByText('Category Trends')).toBeInTheDocument();
      expect(screen.getByText('Top Merchants')).toBeInTheDocument();
    });
  });

  it('renders top merchants list', async () => {
    renderReports();
    await waitFor(() => {
      expect(screen.getByText('Grocery Store')).toBeInTheDocument();
      expect(screen.getByText('Gas Station')).toBeInTheDocument();
    });
  });

  it('renders year selector for year-over-year (combobox role)', () => {
    renderReports();
    expect(
      screen.getByRole('combobox', { name: /year-over-year year/i }),
    ).toBeInTheDocument();
  });

  it('renders time period selector for income/expenses (combobox role)', () => {
    renderReports();
    expect(
      screen.getByRole('combobox', { name: /time period/i }),
    ).toBeInTheDocument();
  });

  it('renders month and year selectors for top merchants (combobox role)', () => {
    renderReports();
    expect(
      screen.getByRole('combobox', { name: /merchant month/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('combobox', { name: /merchant year/i }),
    ).toBeInTheDocument();
  });

  it('shows empty state when no merchants', async () => {
    vi.mocked(api.get).mockImplementation((path: string) => {
      if (path.startsWith('reports/top-merchants'))
        return Promise.resolve({ merchants: [] } satisfies {
          merchants: TopMerchantEntry[];
        });
      if (path.startsWith('reports/year-over-year')) return Promise.resolve(mockYoY);
      if (path.startsWith('reports/category-trends')) return Promise.resolve(mockCatTrends);
      if (path.startsWith('reports/income-expenses')) return Promise.resolve(mockIncExp);
      return Promise.reject(new Error('unknown'));
    });

    renderReports();
    await waitFor(() => {
      expect(
        screen.getByText('No transactions for this period'),
      ).toBeInTheDocument();
    });
  });

  it('opens the period selector and switches to 24 months', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderReports();

    await user.click(
      screen.getByRole('combobox', { name: /time period/i }),
    );
    await user.click(screen.getByRole('option', { name: /24 months/i }));

    // After switching, the trigger should show the new value.
    await waitFor(() => {
      expect(
        screen.getByRole('combobox', { name: /time period/i }),
      ).toHaveTextContent(/24 months/i);
    });

    // And the hook must have re-fetched with months=24. The real
    // useIncomeExpenses / useCategoryTrends hooks build their URL as
    // `reports/<name>?months=<n>`, so assert api.get was called with a
    // path containing `months=24`. Guards against the Select being
    // wired to nothing.
    expect(vi.mocked(api.get)).toHaveBeenCalledWith(
      expect.stringContaining('months=24'),
    );
  });
});
