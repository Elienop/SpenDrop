import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { api } from '../api/client';

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

vi.mock('../hooks/useTheme', () => ({
  useTheme: () => ({ theme: 'dark', resolvedTheme: 'dark', setTheme: vi.fn() }),
}));

// Mock recharts to avoid canvas rendering in tests
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
};

const mockCatTrends = {
  categories: [
    { id: 1, name: 'Food', color: '#ff0000', type: 'expense', data: [{ year: 2026, month: 1, total: 500 }] },
  ],
};

const mockIncExp = {
  data: Array.from({ length: 12 }, (_, i) => ({
    year: 2026, month: i + 1, income: 2000, expenses: 1500, net: 500,
  })),
};

const mockMerchants = {
  year: 2026,
  month: 4,
  merchants: [
    { description: 'Grocery Store', tx_count: 8, total: 450.50 },
    { description: 'Gas Station', tx_count: 4, total: 200.00 },
  ],
};

beforeEach(() => {
  vi.mocked(api.get).mockImplementation((path: string) => {
    if (path.includes('year-over-year')) return Promise.resolve(mockYoY);
    if (path.includes('category-trends')) return Promise.resolve(mockCatTrends);
    if (path.includes('income-expenses')) return Promise.resolve(mockIncExp);
    if (path.includes('top-merchants')) return Promise.resolve(mockMerchants);
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
  it('renders the page heading', async () => {
    renderReports();
    expect(screen.getByRole('heading', { level: 1, name: 'Reports' })).toBeInTheDocument();
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

  it('renders year selector for year-over-year', () => {
    renderReports();
    const select = screen.getByLabelText('Year');
    expect(select).toBeInTheDocument();
  });

  it('renders time period selector for income/expenses', () => {
    renderReports();
    const select = screen.getByLabelText('Time period');
    expect(select).toBeInTheDocument();
  });

  it('shows empty state when no merchants', async () => {
    vi.mocked(api.get).mockImplementation((path: string) => {
      if (path.includes('top-merchants'))
        return Promise.resolve({ year: 2026, month: 4, merchants: [] });
      if (path.includes('year-over-year')) return Promise.resolve(mockYoY);
      if (path.includes('category-trends')) return Promise.resolve(mockCatTrends);
      if (path.includes('income-expenses')) return Promise.resolve(mockIncExp);
      return Promise.reject(new Error('unknown'));
    });

    renderReports();
    await waitFor(() => {
      expect(screen.getByText('No transactions for this period')).toBeInTheDocument();
    });
  });
});
