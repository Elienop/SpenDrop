import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../hooks/useAuth', () => ({ useAuth: vi.fn() }));
// Mock only the `api` singleton — keep the real `ApiError` class so that
// `err instanceof ApiError` checks in the component work against the same
// constructor the test instantiates.
vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>();
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      patch: vi.fn(),
      del: vi.fn(),
      upload: vi.fn(),
    },
  };
});
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

import { useAuth } from '../hooks/useAuth';
import { api, ApiError } from '../api/client';
import { toast } from 'sonner';
import { Settings } from './Settings';
import type { User } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

const memberUser: User = {
  id: 2,
  username: 'bob',
  display_name: 'Bob',
  role: 'member',
  created_at: '2024-01-01',
};

const adminUser: User = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin',
  created_at: '2024-01-01',
};

const logout = vi.fn();

function seedGetMock(users: User[] = []) {
  mockedApi.get.mockImplementation((path: string) => {
    if (path === 'users') return Promise.resolve(users);
    if (path === 'api-tokens') return Promise.resolve({ tokens: [] });
    if (path === 'currencies') return Promise.resolve([]);
    if (path === 'savings-goals') return Promise.resolve([]);
    if (path.includes('budget')) return Promise.resolve([]);
    return Promise.resolve([]);
  });
}

/**
 * Fresh QueryClient per render, matching what `main.tsx` provides in
 * production. Settings renders `<AppVersion />`, whose `useServerVersion`
 * hook is a `useQuery` — without a provider the whole page throws on mount.
 */
function withQueryClient({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function renderSettings(initialTab = 'account') {
  return render(
    <MemoryRouter initialEntries={[`/settings?tab=${initialTab}`]}>
      <Settings />
    </MemoryRouter>,
    { wrapper: withQueryClient },
  );
}

function mockAuth(user: User) {
  mockedUseAuth.mockReturnValue({
    user,
    loading: false,
    unverified: false,
    login: vi.fn(),
    register: vi.fn(),
    logout,
  });
}

describe('Account tab — change password', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    seedGetMock();
    mockAuth(memberUser);
  });

  test('Account tab is visible to a non-admin member', () => {
    renderSettings();
    expect(screen.getByRole('tab', { name: /account/i })).toBeInTheDocument();
  });

  test('Account tab is also visible to an admin', () => {
    mockAuth(adminUser);
    renderSettings();
    expect(screen.getByRole('tab', { name: /account/i })).toBeInTheDocument();
  });

  test('each password field has its own independently-scoped reveal toggle', async () => {
    const user = userEvent.setup();
    renderSettings();

    // All three start masked.
    expect(screen.getByLabelText('Current password')).toHaveAttribute(
      'type',
      'password',
    );
    expect(screen.getByLabelText('New password')).toHaveAttribute(
      'type',
      'password',
    );

    // Field-scoped accessible names: three buttons all called "Show password"
    // would be ambiguous in a screen-reader rotor and unqueryable here.
    await user.click(
      screen.getByRole('button', { name: 'Show current password' }),
    );

    // Revealing one field must not reveal its siblings.
    expect(screen.getByLabelText('Current password')).toHaveAttribute(
      'type',
      'text',
    );
    expect(screen.getByLabelText('New password')).toHaveAttribute(
      'type',
      'password',
    );
    expect(screen.getByLabelText('Confirm new password')).toHaveAttribute(
      'type',
      'password',
    );
  });

  test('renders the change-password form with three fields and a sign-out warning', async () => {
    renderSettings();
    expect(
      screen.getByLabelText('Current password'),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('New password')).toBeInTheDocument();
    expect(
      screen.getByLabelText('Confirm new password'),
    ).toBeInTheDocument();
    // Inline warning about signing out everywhere + revoking tokens.
    expect(
      screen.getByText(/this signs you out everywhere/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/revokes all your api tokens/i),
    ).toBeInTheDocument();
  });

  test('shows a validation error when new and confirm do not match', async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.type(screen.getByLabelText('Current password'), 'oldpass123');
    await user.type(screen.getByLabelText('New password'), 'newpass123');
    await user.type(
      screen.getByLabelText('Confirm new password'),
      'different123',
    );
    await user.click(
      screen.getByRole('button', { name: /change password/i }),
    );
    await waitFor(() => {
      expect(screen.getByText(/passwords do not match/i)).toBeInTheDocument();
    });
    expect(mockedApi.post).not.toHaveBeenCalled();
  });

  test('shows a validation error when new password is too short', async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.type(screen.getByLabelText('Current password'), 'oldpass123');
    await user.type(screen.getByLabelText('New password'), 'short');
    await user.type(screen.getByLabelText('Confirm new password'), 'short');
    await user.click(
      screen.getByRole('button', { name: /change password/i }),
    );
    await waitFor(() => {
      expect(
        screen.getByText(/at least 8 characters/i),
      ).toBeInTheDocument();
    });
    expect(mockedApi.post).not.toHaveBeenCalled();
  });

  test('posts to auth/password with snake_case body on valid submit', async () => {
    const user = userEvent.setup();
    mockedApi.post.mockResolvedValueOnce({
      status: 'password_changed',
      tokens_revoked: 2,
    });
    renderSettings();
    await user.type(screen.getByLabelText('Current password'), 'oldpass123');
    await user.type(screen.getByLabelText('New password'), 'newpass123');
    await user.type(
      screen.getByLabelText('Confirm new password'),
      'newpass123',
    );
    await user.click(
      screen.getByRole('button', { name: /change password/i }),
    );
    await waitFor(() => {
      expect(mockedApi.post).toHaveBeenCalledWith('auth/password', {
        current_password: 'oldpass123',
        new_password: 'newpass123',
      });
    });
  });

  test('on success: fires success toast, logs out, and redirects to /login', async () => {
    const user = userEvent.setup();
    mockedApi.post.mockResolvedValueOnce({
      status: 'password_changed',
      tokens_revoked: 3,
    });
    renderSettings();
    await user.type(screen.getByLabelText('Current password'), 'oldpass123');
    await user.type(screen.getByLabelText('New password'), 'newpass123');
    await user.type(
      screen.getByLabelText('Confirm new password'),
      'newpass123',
    );
    await user.click(
      screen.getByRole('button', { name: /change password/i }),
    );
    await waitFor(() => {
      expect(vi.mocked(toast).success).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(logout).toHaveBeenCalled();
    });
  });

  test('a 401 (by status, not message text) surfaces a current-password error', async () => {
    const user = userEvent.setup();
    // The handler must branch on the HTTP status, not the message string.
    // Use an ApiError whose message is deliberately NOT 'Unauthorized' to
    // prove the mapping is status-driven and no longer a string tautology.
    mockedApi.post.mockRejectedValueOnce(
      new ApiError('invalid credentials', 401),
    );
    renderSettings();
    await user.type(screen.getByLabelText('Current password'), 'wrongpass1');
    await user.type(screen.getByLabelText('New password'), 'newpass123');
    await user.type(
      screen.getByLabelText('Confirm new password'),
      'newpass123',
    );
    await user.click(
      screen.getByRole('button', { name: /change password/i }),
    );
    await waitFor(() => {
      expect(
        screen.getByText(/current password is incorrect/i),
      ).toBeInTheDocument();
    });
    expect(logout).not.toHaveBeenCalled();
  });

  test('400 surfaces the server message on the new-password field', async () => {
    const user = userEvent.setup();
    mockedApi.post.mockRejectedValueOnce(
      new Error('password must be at least 12 characters'),
    );
    renderSettings();
    await user.type(screen.getByLabelText('Current password'), 'oldpass123');
    await user.type(screen.getByLabelText('New password'), 'newpass123');
    await user.type(
      screen.getByLabelText('Confirm new password'),
      'newpass123',
    );
    await user.click(
      screen.getByRole('button', { name: /change password/i }),
    );
    await waitFor(() => {
      expect(
        screen.getByText(/password must be at least 12 characters/i),
      ).toBeInTheDocument();
    });
    expect(logout).not.toHaveBeenCalled();
  });
});

describe('Users tab — admin reset password', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    seedGetMock([adminUser, memberUser]);
    mockAuth(adminUser);
  });

  test('renders a Reset password action for other users', async () => {
    renderSettings('users');
    await waitFor(() => {
      expect(screen.getByText('bob')).toBeInTheDocument();
    });
    expect(
      screen.getByRole('button', { name: /reset password for bob/i }),
    ).toBeInTheDocument();
  });

  test("does NOT render a Reset password action for the admin's own row", async () => {
    renderSettings('users');
    await waitFor(() => {
      expect(screen.getByText('alice')).toBeInTheDocument();
    });
    expect(
      screen.queryByRole('button', { name: /reset password for alice/i }),
    ).not.toBeInTheDocument();
  });

  test('reset dialog validates confirm match before posting', async () => {
    const user = userEvent.setup();
    renderSettings('users');
    await waitFor(() => {
      expect(screen.getByText('bob')).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /reset password for bob/i }),
    );
    const dialog = await screen.findByRole('alertdialog');
    await user.type(
      within(dialog).getByLabelText('New password'),
      'newpass123',
    );
    await user.type(
      within(dialog).getByLabelText('Confirm new password'),
      'mismatch123',
    );
    await user.click(
      within(dialog).getByRole('button', { name: /reset password/i }),
    );
    await waitFor(() => {
      expect(
        within(dialog).getByText(/passwords do not match/i),
      ).toBeInTheDocument();
    });
    expect(mockedApi.post).not.toHaveBeenCalled();
  });

  test('posts to users/{id}/reset-password with new_password and toasts on success', async () => {
    const user = userEvent.setup();
    mockedApi.post.mockResolvedValueOnce({
      status: 'password_reset',
      tokens_revoked: 1,
    });
    renderSettings('users');
    await waitFor(() => {
      expect(screen.getByText('bob')).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /reset password for bob/i }),
    );
    const dialog = await screen.findByRole('alertdialog');
    await user.type(
      within(dialog).getByLabelText('New password'),
      'newpass123',
    );
    await user.type(
      within(dialog).getByLabelText('Confirm new password'),
      'newpass123',
    );
    await user.click(
      within(dialog).getByRole('button', { name: /reset password/i }),
    );
    await waitFor(() => {
      expect(mockedApi.post).toHaveBeenCalledWith('users/2/reset-password', {
        new_password: 'newpass123',
      });
    });
    await waitFor(() => {
      expect(vi.mocked(toast).success).toHaveBeenCalled();
    });
  });
});
