import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

// Mock useAuth hook
vi.mock('../hooks/useAuth', () => {
  return {
    useAuth: vi.fn(),
    AuthProvider: ({ children }: { children: React.ReactNode }) => (
      <>{children}</>
    ),
  };
});

import { useAuth } from '../hooks/useAuth';
import { ProtectedRoute } from './ProtectedRoute';

const mockedUseAuth = vi.mocked(useAuth);

const alice = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin' as const,
  created_at: '2024-01-01',
};

function renderWithRouter(initialRoute: string) {
  return render(
    <MemoryRouter initialEntries={[initialRoute]}>
      <Routes>
        <Route path="/login" element={<div>Login Page</div>} />
        <Route
          path="/dashboard"
          element={
            <ProtectedRoute>
              <div>Dashboard Content</div>
            </ProtectedRoute>
          }
        />
        <Route
          path="/quick"
          element={
            <ProtectedRoute allowUnverified>
              <div>Capture Screen</div>
            </ProtectedRoute>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ProtectedRoute', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test('shows loading indicator while auth is loading', () => {
    mockedUseAuth.mockReturnValue({
      user: null,
      loading: true,
      unverified: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: vi.fn(),
    });

    renderWithRouter('/dashboard');

    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByText('Dashboard Content')).not.toBeInTheDocument();
  });

  test('redirects to /login when not authenticated', async () => {
    mockedUseAuth.mockReturnValue({
      user: null,
      loading: false,
      unverified: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: vi.fn(),
    });

    renderWithRouter('/dashboard');

    await waitFor(() => {
      expect(screen.getByText('Login Page')).toBeInTheDocument();
    });
    expect(screen.queryByText('Dashboard Content')).not.toBeInTheDocument();
  });

  test('renders children when authenticated', () => {
    mockedUseAuth.mockReturnValue({
      user: alice,
      loading: false,
      unverified: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: vi.fn(),
    });

    renderWithRouter('/dashboard');

    expect(screen.getByText('Dashboard Content')).toBeInTheDocument();
  });

  // An identity the server has not confirmed unlocks capture and NOTHING else.
  // Household data reads stay exactly as gated as they were.
  test('redirects an unverified identity away from a normal protected route', async () => {
    mockedUseAuth.mockReturnValue({
      user: alice,
      loading: false,
      unverified: true,
      login: vi.fn(),
      register: vi.fn(),
      logout: vi.fn(),
    });

    renderWithRouter('/dashboard');

    await waitFor(() => {
      expect(screen.getByText('Login Page')).toBeInTheDocument();
    });
    expect(screen.queryByText('Dashboard Content')).not.toBeInTheDocument();
  });

  test('lets an unverified identity reach a route marked allowUnverified', () => {
    mockedUseAuth.mockReturnValue({
      user: alice,
      loading: false,
      unverified: true,
      login: vi.fn(),
      register: vi.fn(),
      logout: vi.fn(),
    });

    renderWithRouter('/quick');

    expect(screen.getByText('Capture Screen')).toBeInTheDocument();
  });

  test('allowUnverified still requires an identity', async () => {
    mockedUseAuth.mockReturnValue({
      user: null,
      loading: false,
      unverified: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: vi.fn(),
    });

    renderWithRouter('/quick');

    await waitFor(() => {
      expect(screen.getByText('Login Page')).toBeInTheDocument();
    });
    expect(screen.queryByText('Capture Screen')).not.toBeInTheDocument();
  });
});
