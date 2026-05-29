import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

// Mock the API client module
vi.mock('../api/client', () => {
  return {
    api: {
      get: vi.fn(),
      post: vi.fn(),
    },
  };
});

// Logout purges the leaving user's per-user offline queue. Mock the lib so the
// test asserts the call without touching IndexedDB.
const purgeQueue = vi.fn();
vi.mock('@/lib/offline-queue', () => ({
  purgeQueue: (...args: unknown[]) => purgeQueue(...args),
}));

// We'll import after mock setup
import { api } from '../api/client';
import { AuthProvider, useAuth } from './useAuth';

const mockedApi = vi.mocked(api);

// Test component that exposes auth context values
function AuthDisplay() {
  const { user, loading, login, logout, register } = useAuth();
  return (
    <div>
      <span data-testid="loading">{String(loading)}</span>
      <span data-testid="user">{user ? user.display_name : 'null'}</span>
      <button onClick={() => login('alice', 'pass123')}>Login</button>
      <button onClick={() => register('bob', 'pass456', 'Bob')}>
        Register
      </button>
      <button onClick={() => logout()}>Logout</button>
    </div>
  );
}

function renderWithProviders(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <AuthProvider>{ui}</AuthProvider>
    </MemoryRouter>,
  );
}

// The Cache Storage API is absent in happy-dom; stub a deletable spy so the
// logout purge path has something to call.
const cachesDelete = vi.fn().mockResolvedValue(true);

describe('useAuth', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    purgeQueue.mockResolvedValue(undefined);
    cachesDelete.mockResolvedValue(true);
    vi.stubGlobal('caches', { delete: cachesDelete });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  test('starts in loading state and checks session on mount', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });

    renderWithProviders(<AuthDisplay />);

    // Initially loading
    expect(screen.getByTestId('loading')).toHaveTextContent('true');

    // After session check resolves
    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    expect(mockedApi.get).toHaveBeenCalledWith('auth/me');
  });

  test('sets user to null when session check fails', async () => {
    mockedApi.get.mockRejectedValueOnce(new Error('Unauthorized'));

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('user')).toHaveTextContent('null');
  });

  test('login calls api and sets user state', async () => {
    // Initial session check fails (not logged in)
    mockedApi.get.mockRejectedValueOnce(new Error('Unauthorized'));
    // Login succeeds
    mockedApi.post.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });

    await user.click(screen.getByText('Login'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });
    expect(mockedApi.post).toHaveBeenCalledWith('auth/login', {
      username: 'alice',
      password: 'pass123',
    });
  });

  test('register calls api and sets user state', async () => {
    mockedApi.get.mockRejectedValueOnce(new Error('Unauthorized'));
    mockedApi.post.mockResolvedValueOnce({
      id: 2,
      username: 'bob',
      display_name: 'Bob',
      role: 'member',
      created_at: '2024-01-01',
    });

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });

    await user.click(screen.getByText('Register'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Bob');
    });
    expect(mockedApi.post).toHaveBeenCalledWith('auth/register', {
      username: 'bob',
      password: 'pass456',
      display_name: 'Bob',
    });
  });

  test('logout calls api and clears user state', async () => {
    // Session check returns user
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    // Logout succeeds
    mockedApi.post.mockResolvedValueOnce(undefined);

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    expect(mockedApi.post).toHaveBeenCalledWith('auth/logout');
  });

  test('logout clears user state even when the logout POST rejects with 401', async () => {
    // Session check returns a user (we are logged in).
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    // The server already killed the session (e.g. right after a password
    // change), so the logout POST rejects with 401.
    mockedApi.post.mockRejectedValueOnce(new Error('Unauthorized'));

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    // Local auth state must still be cleared despite the rejected POST.
    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    expect(mockedApi.post).toHaveBeenCalledWith('auth/logout');
  });

  test('logout purges the offline queue and api-lists cache for the leaving user', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 42,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.post.mockResolvedValueOnce(undefined);

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    // The leaving user's offline DB and the device-global api-lists Cache are
    // purged so a different account on this device can't replay/read them.
    expect(purgeQueue).toHaveBeenCalledWith(42);
    expect(cachesDelete).toHaveBeenCalledWith('spendrop-api-lists');
  });

  test('logout still purges the queue when the logout POST rejects', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 42,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.post.mockRejectedValueOnce(new Error('Unauthorized'));

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    expect(purgeQueue).toHaveBeenCalledWith(42);
  });
});
