import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// Mock the auth module
vi.mock('./hooks/useAuth', () => ({
  useAuth: vi.fn(),
  AuthProvider: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

// Mock useTheme (Sidebar uses it)
vi.mock('./hooks/useTheme', () => ({
  useTheme: vi.fn().mockReturnValue({
    theme: 'dark',
    resolvedTheme: 'dark',
    setTheme: vi.fn(),
  }),
}));

// Mock the API client (real pages call api.get, etc.)
vi.mock('./api/client', () => ({
  api: {
    get: vi.fn().mockResolvedValue({ transactions: [], total: 0, page: 1, per_page: 5 }),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

// Mock useDashboard so Dashboard renders content instead of loading skeleton
vi.mock('./hooks/useDashboard', () => ({
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
    trend: [],
    categories: [],
    loading: false,
    error: '',
  }),
}));

// Mock useChartPatterns (Dashboard uses it)
vi.mock('./hooks/useChartPatterns', () => ({
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

// Mock useChartTheme (Dashboard charts use it)
vi.mock('./hooks/useChartTheme', () => ({
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

// Mock ChartTooltip
vi.mock('./components/ChartTooltip', () => ({
  ChartTooltip: () => <div />,
}));

// Mock recharts (Dashboard and Reports use it)
vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: () => <div />,
  LineChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Line: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  CartesianGrid: () => <div />,
  PieChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Pie: () => <div />,
  Cell: () => <div />,
}));

import { useAuth } from './hooks/useAuth';
import App from './App';

const mockedUseAuth = vi.mocked(useAuth);

const authenticatedUser = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin' as const,
  created_at: '2024-01-01',
};

function renderApp(initialPath = '/') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <App />
    </MemoryRouter>,
  );
}

describe('App', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('when authenticated', () => {
    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: authenticatedUser,
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('renders sidebar and dashboard heading on /', () => {
      renderApp('/');
      // Sidebar has the app title
      expect(screen.getByText('SpenDrop')).toBeInTheDocument();
      // Main content has an h1 with welcome greeting
      expect(
        screen.getByRole('heading', { level: 1 }),
      ).toHaveTextContent(/Welcome back/);
    });

    test('renders reports heading on /reports', () => {
      renderApp('/reports');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Reports' }),
      ).toBeInTheDocument();
    });

    test('renders transactions heading on /transactions', () => {
      renderApp('/transactions');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Transactions' }),
      ).toBeInTheDocument();
    });

    test('renders categories heading on /categories', () => {
      renderApp('/categories');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Categories' }),
      ).toBeInTheDocument();
    });

    test('renders settings heading on /settings', () => {
      renderApp('/settings');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Settings' }),
      ).toBeInTheDocument();
    });
  });

  describe('when not authenticated', () => {
    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: null,
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('renders login page on /login without sidebar', () => {
      renderApp('/login');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Login' }),
      ).toBeInTheDocument();
      expect(screen.queryByText('SpenDrop')).not.toBeInTheDocument();
    });

    test('renders register page on /register without sidebar', () => {
      renderApp('/register');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Register' }),
      ).toBeInTheDocument();
      expect(screen.queryByText('SpenDrop')).not.toBeInTheDocument();
    });

    test('redirects to login for protected routes', async () => {
      renderApp('/');
      await waitFor(() => {
        expect(
          screen.getByRole('heading', { level: 1, name: 'Login' }),
        ).toBeInTheDocument();
      });
    });
  });
});
