import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../hooks/useDashboard', () => ({
  useDashboard: vi.fn(),
}));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

// Mock recharts to avoid rendering canvas in tests
vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="responsive-container">{children}</div>
  ),
  BarChart: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="bar-chart">{children}</div>
  ),
  Bar: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  CartesianGrid: () => <div />,
  PieChart: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="pie-chart">{children}</div>
  ),
  Pie: () => <div />,
  Cell: () => <div />,
}));

import { useAuth } from '../hooks/useAuth';
import { useDashboard } from '../hooks/useDashboard';
import { api } from '../api/client';
import { Dashboard } from './Dashboard';

const mockedUseAuth = vi.mocked(useAuth);
const mockedUseDashboard = vi.mocked(useDashboard);
const mockedApi = vi.mocked(api);

function renderDashboard() {
  return render(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );
}

describe('Dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: {
        id: 1,
        username: 'alice',
        display_name: 'Alice',
        role: 'admin',
        created_at: '2024-01-01',
      },
      loading: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: vi.fn(),
    });
    mockedApi.get.mockResolvedValue({ transactions: [], total: 0, page: 1, per_page: 5 });
  });

  test('renders Dashboard heading', () => {
    mockedUseDashboard.mockReturnValue({
      summary: null,
      trend: [],
      categories: [],
      loading: true,
      error: '',
    });

    renderDashboard();
    expect(
      screen.getByRole('heading', { level: 1, name: /dashboard/i }),
    ).toBeInTheDocument();
  });

  test('shows loading state', () => {
    mockedUseDashboard.mockReturnValue({
      summary: null,
      trend: [],
      categories: [],
      loading: true,
      error: '',
    });

    renderDashboard();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
  });

  test('shows error state', () => {
    mockedUseDashboard.mockReturnValue({
      summary: null,
      trend: [],
      categories: [],
      loading: false,
      error: 'Failed to load',
    });

    renderDashboard();
    expect(screen.getByRole('alert')).toHaveTextContent('Failed to load');
  });

  test('renders 4 KPI cards with summary data', async () => {
    mockedUseDashboard.mockReturnValue({
      summary: {
        year: 2026,
        month: 4,
        budget: 3000,
        total_spent: 1200,
        total_income: 4000,
        remaining: 1800,
        savings_this_month: 500,
        savings_goal: 6000,
        savings_ytd: 2000,
        savings_goal_progress: 33.3,
      },
      trend: [],
      categories: [],
      loading: false,
      error: '',
    });

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Budget')).toBeInTheDocument();
      expect(screen.getByText('Spent')).toBeInTheDocument();
      expect(screen.getByText('Remaining')).toBeInTheDocument();
      expect(screen.getByText('Savings Progress')).toBeInTheDocument();
    });
  });

  test('renders spending trend chart area', () => {
    mockedUseDashboard.mockReturnValue({
      summary: {
        year: 2026,
        month: 4,
        budget: 3000,
        total_spent: 1200,
        total_income: 4000,
        remaining: 1800,
        savings_this_month: 500,
        savings_goal: 6000,
        savings_ytd: 2000,
        savings_goal_progress: 33.3,
      },
      trend: [
        { year: 2026, month: 3, total_spent: 1100, total_income: 3800 },
      ],
      categories: [],
      loading: false,
      error: '',
    });

    renderDashboard();
    expect(screen.getByText(/spending trend/i)).toBeInTheDocument();
  });

  test('renders category breakdown chart area', () => {
    mockedUseDashboard.mockReturnValue({
      summary: {
        year: 2026,
        month: 4,
        budget: 3000,
        total_spent: 1200,
        total_income: 4000,
        remaining: 1800,
        savings_this_month: 500,
        savings_goal: 6000,
        savings_ytd: 2000,
        savings_goal_progress: 33.3,
      },
      trend: [],
      categories: [
        { id: 1, name: 'Food', color: '#ff0000', total: 500 },
      ],
      loading: false,
      error: '',
    });

    renderDashboard();
    expect(screen.getByText(/category breakdown/i)).toBeInTheDocument();
  });

  test('renders month/year selector', () => {
    mockedUseDashboard.mockReturnValue({
      summary: null,
      trend: [],
      categories: [],
      loading: true,
      error: '',
    });

    renderDashboard();
    expect(screen.getByLabelText(/month/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
  });
});
