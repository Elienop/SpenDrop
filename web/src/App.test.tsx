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
      // Main content has an h1 with Dashboard
      expect(
        screen.getByRole('heading', { level: 1, name: 'Dashboard' }),
      ).toBeInTheDocument();
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
