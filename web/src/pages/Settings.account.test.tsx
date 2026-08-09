import { describe, test, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../hooks/useAuth', () => ({ useAuth: vi.fn() }));

// Spread the real module and stub only `api`. The real `ApiError` class has to
// survive: the card discriminates on `err instanceof ApiError && err.status ===
// 400` to decide whether a failure belongs on the field or in a toast, and a
// factory that omits the class leaves that check testing `instanceof
// undefined`, which throws.
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
const mockedToast = vi.mocked(toast);

/**
 * The merged Account panel: `users` was retired INTO `account`, so one section
 * now holds a self-service card every role gets and a household table only an
 * admin gets.
 *
 * THE ADMIN BOUNDARY IS THE POINT OF THIS FILE, and it has two failure
 * directions that need two different mechanisms to catch:
 *
 *   1. A member LOSES their own account (somebody marks the section
 *      `adminOnly`). Caught in `settings-sections.test.ts` as a pure-function
 *      assertion — no viewport, no DOM, no mock timing.
 *   2. A member is OFFERED household controls (somebody deletes the `admin &&`
 *      in `<AccountSection>`). Caught here, on the member's panel content, and
 *      specifically by the ABSENCE OF `api.get('users')`: the household card
 *      fires that on mount and the backend answers 403, so a gate that only
 *      hid its markup would still make the request. Not mounting is the gate.
 */

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

const refreshUser = vi.fn();

function mockAuth(user: User) {
  mockedUseAuth.mockReturnValue({
    user,
    loading: false,
    unverified: false,
    refreshUser,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

function withQueryClient({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function renderSettings() {
  return render(
    <MemoryRouter initialEntries={['/settings?tab=account']}>
      <Settings />
    </MemoryRouter>,
    { wrapper: withQueryClient },
  );
}

/** The self card's form, scoped so the admin's rename dialog cannot be hit. */
function selfNameInput() {
  return screen.getByLabelText('Display name');
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockedApi.get.mockImplementation((path: string) => {
    if (path === 'users') return Promise.resolve([adminUser, memberUser]);
    if (path === 'api-tokens') return Promise.resolve({ tokens: [] });
    return Promise.resolve([]);
  });
  mockedApi.patch.mockResolvedValue(memberUser);
  mockAuth(memberUser);
});

describe('the admin boundary inside the merged panel', () => {
  test('a member gets their own card and NOT the household one', async () => {
    renderSettings();
    // Positive control first: the panel really did mount, so the absences
    // below are absences rather than an empty render.
    expect(await screen.findByText('Your account')).toBeInTheDocument();

    expect(screen.queryByText('Household users')).not.toBeInTheDocument();
    // The mount-time fetch is the assertion that a JSX-only gate would fail.
    expect(
      mockedApi.get.mock.calls.filter((call) => call[0] === 'users'),
    ).toHaveLength(0);
  });

  test('an admin gets both, with their own card first', async () => {
    mockAuth(adminUser);
    renderSettings();

    const self = await screen.findByText('Your account');
    const household = await screen.findByText('Household users');
    // Self card FIRST. A member's entire panel is the self card, so an
    // admin-first order would open the two roles on different content.
    expect(
      self.compareDocumentPosition(household) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    await waitFor(() => {
      expect(
        mockedApi.get.mock.calls.filter((call) => call[0] === 'users').length,
      ).toBeGreaterThan(0);
    });
  });
});

describe('the self card', () => {
  test('shows the username and role of whoever is signed in', async () => {
    renderSettings();
    const card = await screen.findByRole('group', { name: 'Your account' });
    expect(within(card).getByText('bob')).toBeInTheDocument();
    expect(within(card).getByText('Member')).toBeInTheDocument();
  });

  test('an admin sees their own role, not the member fixture', async () => {
    mockAuth(adminUser);
    renderSettings();
    // Scoped to the card because the household table below renders a role
    // Select per row whose value also reads "Admin".
    const card = await screen.findByRole('group', { name: 'Your account' });
    expect(within(card).getByText('alice')).toBeInTheDocument();
    expect(within(card).getByText('Admin')).toBeInTheDocument();
    expect(within(card).queryByText('Member')).not.toBeInTheDocument();
  });

  test('prefills the display-name box with the stored name', () => {
    renderSettings();
    // The motivating case is shortening a name that is already there.
    expect(selfNameInput()).toHaveValue('Bob');
  });
});

describe('a member renaming themselves', () => {
  test('PATCHes auth/me with display_name alone', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();

    await user.clear(selfNameInput());
    await user.type(selfNameInput(), 'Bobby');
    await user.click(
      screen.getByRole('button', { name: 'Save display name' }),
    );

    await waitFor(() => {
      // PATCH, not PUT: `PUT /api/users/{id}` sits behind RequireAdmin, so a
      // member calling it gets a 403 and stays stuck with the name an admin
      // typed for them. Exact object — the endpoint's whole request struct is
      // this one field, and anything else sent would go nowhere while
      // suggesting at the call site that it went somewhere.
      expect(mockedApi.patch).toHaveBeenCalledWith('auth/me', {
        display_name: 'Bobby',
      });
    });
    expect(mockedApi.patch).toHaveBeenCalledTimes(1);
    expect(mockedApi.put).not.toHaveBeenCalled();
  });

  test('re-reads the profile and invalidates cached transactions', async () => {
    const invalidate = vi.spyOn(QueryClient.prototype, 'invalidateQueries');
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();

    await user.clear(selfNameInput());
    await user.type(selfNameInput(), 'Bobby');
    await user.click(
      screen.getByRole('button', { name: 'Save display name' }),
    );

    // The shell renders this name out of the auth context — Sidebar and
    // MobileNav both read `user.display_name` — and nothing else refreshes it.
    await waitFor(() => {
      expect(refreshUser).toHaveBeenCalledTimes(1);
    });
    // Display names are resolved server-side into every transaction's
    // `created_by`, and this PATCH emits no SSE event. This is the assertion
    // someone will "simplify" away as redundant beside refreshUser.
    await waitFor(() => {
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['transactions'] });
    });
    invalidate.mockRestore();
  });

  test('refuses an empty name without calling the API', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();

    await user.clear(selfNameInput());
    await user.click(
      screen.getByRole('button', { name: 'Save display name' }),
    );

    expect(
      await screen.findByText(/display name is required/i),
    ).toBeInTheDocument();
    expect(mockedApi.patch).not.toHaveBeenCalled();
  });

  test('refuses a whitespace-only name without calling the API', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();

    await user.clear(selfNameInput());
    await user.type(selfNameInput(), '   ');
    await user.click(
      screen.getByRole('button', { name: 'Save display name' }),
    );

    expect(
      await screen.findByText(/display name is required/i),
    ).toBeInTheDocument();
    expect(mockedApi.patch).not.toHaveBeenCalled();
  });

  // Astral-plane characters, so the pair discriminates the UNIT as well as the
  // number: 64 of these are 128 UTF-16 code units, which `.length` or Zod's
  // `.max()` would refuse while the server's rune count accepts them. The
  // server's cap is `MaxDisplayNameLength` in internal/api/limits.go, written
  // out rather than imported — the number is what has to agree across the wire.
  const LIMIT = 64;

  test('refuses a display name one character over the limit', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();

    fireEvent.change(selfNameInput(), {
      target: { value: '𝟙'.repeat(LIMIT + 1) },
    });
    await user.click(
      screen.getByRole('button', { name: 'Save display name' }),
    );

    expect(
      await screen.findByText(
        `Display name must be ${LIMIT} characters or fewer`,
      ),
    ).toBeInTheDocument();
    expect(mockedApi.patch).not.toHaveBeenCalled();
  });

  test('accepts a display name of exactly the limit in astral-plane characters', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();
    const atLimit = '𝟙'.repeat(LIMIT);

    fireEvent.change(selfNameInput(), { target: { value: atLimit } });
    await user.click(
      screen.getByRole('button', { name: 'Save display name' }),
    );

    await waitFor(() => {
      expect(mockedApi.patch).toHaveBeenCalledWith('auth/me', {
        display_name: atLimit,
      });
    });
  });

  // The server refuses codepoints that can forge structure in a push
  // notification body (control characters, U+2028/9, bidi overrides) with a
  // 400 carrying its own explanation. That sentence has to reach the box the
  // person must fix — swallowing it into a toast leaves them retyping a name
  // with no idea which character is the problem.
  test('puts a 400 from the server on the field, not in a toast', async () => {
    mockedApi.patch.mockRejectedValue(
      new ApiError(
        'display name may not contain line or paragraph separators',
        400,
      ),
    );
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();

    await user.clear(selfNameInput());
    await user.type(selfNameInput(), 'Bobby');
    await user.click(
      screen.getByRole('button', { name: 'Save display name' }),
    );

    expect(
      await screen.findByText(/line or paragraph separators/i),
    ).toBeInTheDocument();
    expect(mockedToast.error).not.toHaveBeenCalled();
    expect(mockedToast.success).not.toHaveBeenCalled();
  });

  // A transport failure is not a verdict on the name. Rendering "Failed to
  // fetch" as field validation says the name is wrong when it is fine.
  test('sends a transport failure to a toast, not onto the field', async () => {
    mockedApi.patch.mockRejectedValue(new Error('Failed to fetch'));
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();

    await user.clear(selfNameInput());
    await user.type(selfNameInput(), 'Bobby');
    await user.click(
      screen.getByRole('button', { name: 'Save display name' }),
    );

    await waitFor(() => {
      expect(mockedToast.error).toHaveBeenCalledWith('Failed to fetch');
    });
    expect(screen.queryByText(/failed to fetch/i)).not.toBeInTheDocument();
  });
});

describe('an admin renaming themselves in their own card', () => {
  test('still goes through auth/me, not the admin user route', async () => {
    // The self card takes NO PROPS and has no role flag in scope — this is the
    // test that proves it behaves identically for both roles rather than
    // quietly branching to the admin endpoint for an admin. `PUT /users/1`
    // would also work for them, which is exactly why nothing else would catch
    // the divergence.
    mockAuth(adminUser);
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSettings();

    // Scoped to the self card: the household table below has its own rename
    // dialog whose field carries the same label.
    const card = await screen.findByRole('group', { name: 'Your account' });
    const field = within(card).getByLabelText('Display name');
    await user.clear(field);
    await user.type(field, 'Ali');
    await user.click(
      within(card).getByRole('button', { name: 'Save display name' }),
    );

    await waitFor(() => {
      expect(mockedApi.patch).toHaveBeenCalledWith('auth/me', {
        display_name: 'Ali',
      });
    });
    expect(mockedApi.put).not.toHaveBeenCalled();
  });
});
