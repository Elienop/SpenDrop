import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

// Spread the real module and stub only `api`, matching Settings.password.test.tsx.
// The real ApiError class has to survive: the page discriminates on
// `err instanceof ApiError && err.status === 400` to decide whether a failure
// belongs on the field or in a toast, and a factory that omits the class leaves
// that check testing `instanceof undefined`, which throws.
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
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api, ApiError } from '../api/client';
import { toast } from 'sonner';
import { Settings } from './Settings';
import { MAX_CURRENCY_SYMBOL_LENGTH } from '../lib/constants';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import type { Category, ImportPreview, ImportResult } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);
// The `sonner` module mock above is still required — Settings.tsx itself
// imports `toast` and calls it in budget/currency/goals/users flows. The
// old import-failure tests used to assert on `mockedToast.error`; 3.4b
// routed import errors through `importSession.error` → Alert instead, so
// the test-side binding is dead but the module mock is still live.

/**
 * Fresh QueryClient per render, matching what `main.tsx` provides in
 * production. Settings renders `<AppVersion />`, whose `useServerVersion`
 * hook is a `useQuery` — without a provider the whole page throws on mount.
 * `retry: false` keeps a rejected fetch from re-firing between assertions.
 */
function withQueryClient({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function renderSettings() {
  return render(
    <MemoryRouter>
      <Settings />
    </MemoryRouter>,
    { wrapper: withQueryClient },
  );
}

describe('Settings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // useImportSession persists the import_id in localStorage across a
    // successful upload + 60-min resume window. Tests that upload must
    // not leak that key into the next test's mount effect (which would
    // fire a GET /api/import/{id} and race the test's own wiring). Clear
    // explicitly rather than rely on happy-dom teardown.
    localStorage.clear();
    mockedApi.get.mockImplementation((path: string) => {
      if (path.includes('budget'))
        return Promise.resolve([
          { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
        ]);
      if (path === 'currencies')
        return Promise.resolve([
          {
            code: 'USD',
            name: 'US Dollar',
            symbol: '$',
            rate_to_base: 1,
            is_base: true,
            updated_at: '',
          },
          {
            code: 'EUR',
            name: 'Euro',
            symbol: '\u20AC',
            rate_to_base: 0.92,
            is_base: false,
            updated_at: '',
          },
        ]);
      if (path === 'savings-goals')
        return Promise.resolve([
          { id: 1, year: 2026, target_amount: 6000, updated_at: '' },
        ]);
      if (path === 'users')
        return Promise.resolve([
          {
            id: 1,
            username: 'alice',
            display_name: 'Alice',
            role: 'admin',
            created_at: '2024-01-01',
          },
        ]);
      return Promise.resolve([]);
    });
  });

  describe('as admin', () => {
    // Named rather than inline, because the display-name tests assert on it:
    // it is the auth context's own re-read of GET /auth/me, and renaming
    // yourself is the only thing on this page that may fire it.
    const mockRefreshUser = vi.fn();

    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: {
          id: 1,
          username: 'alice',
          display_name: 'Alice',
          role: 'admin',
          created_at: '2024-01-01',
        },
        loading: false,
        unverified: false,
        refreshUser: mockRefreshUser,
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('renders Settings heading', () => {
      renderSettings();
      expect(
        screen.getByRole('heading', { level: 1, name: /settings/i }),
      ).toBeInTheDocument();
    });

    test('stamps the running bundle version below the tabs', async () => {
      renderSettings();

      // Outside the Tabs, so it is present whichever panel is open — the
      // build is a property of the app, not of a settings section.
      const stamp = await screen.findByTestId('app-version');
      expect(stamp).toHaveTextContent(/SpenDrop dev/);
      expect(stamp.closest('[role="tabpanel"]')).toBeNull();
    });

    test('renders tab navigation', () => {
      renderSettings();
      expect(screen.getByRole('tab', { name: /account/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /currencies/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /users/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /api tokens/i })).toBeInTheDocument();
      expect(
        screen.getByRole('tab', { name: /import \/ export/i }),
      ).toBeInTheDocument();
    });

    // Six triggers for an admin come to roughly 550px of whitespace-nowrap
    // content, against ~358px of content width on a 390px phone. The strip has
    // to scroll rather than widen the page — and `justify-start` is the half
    // that is easy to lose: TabsList's own `justify-center` centres the
    // overflow, which parks Account past the left edge where scrollLeft cannot
    // reach it. Whether it scrolls is a browser check; that it is not centred
    // is pinnable here, and tailwind-merge really does drop justify-center.
    test('lets the tab strip scroll sideways instead of centring its overflow', () => {
      renderSettings();
      const strip = screen.getByRole('tablist');
      expect(strip).toHaveClass('overflow-x-auto', 'max-w-full', 'justify-start');
      expect(strip).not.toHaveClass('justify-center');
    });

    test('does not render relocated Budgets or Savings tabs', () => {
      renderSettings();
      expect(
        screen.queryByRole('tab', { name: /general/i }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole('tab', { name: /^savings$/i }),
      ).not.toBeInTheDocument();
      expect(screen.queryByText(/monthly budgets/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/savings goals/i)).not.toBeInTheDocument();
    });

    test('switches to currencies tab', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /currencies/i }));

      await waitFor(() => {
        expect(screen.getByText('USD')).toBeInTheDocument();
        expect(screen.getByText('EUR')).toBeInTheDocument();
      });
    });

    test('shows users tab for admin', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /users/i }));

      await waitFor(() => {
        expect(screen.getByText('alice')).toBeInTheDocument();
      });
    });

    test('renders Import / Export tab', () => {
      renderSettings();
      expect(
        screen.getByRole('tab', { name: /import \/ export/i }),
      ).toBeInTheDocument();
    });

    test('switches to data tab and shows export section', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^export$/i })).toBeInTheDocument();
      });
    });

    test('data tab has year input, mode buttons, and export button', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^monthly$/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^yearly$/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^export$/i })).toBeInTheDocument();
      });
    });

    test('export monthly opens correct URL', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
      });

      // Set year
      const yearInput = screen.getByLabelText(/year/i);
      await user.clear(yearInput);
      await user.type(yearInput, '2026');

      // Monthly is the default mode, click Export
      await user.click(screen.getByRole('button', { name: /^export$/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = openSpy.mock.calls[0][0] as string;
      expect(url).toMatch(/\/api\/export\/monthly\/2026\/\d+/);
      expect(openSpy.mock.calls[0][1]).toBe('_blank');
      openSpy.mockRestore();
    });

    test('export yearly opens correct URL', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
      });

      const yearInput = screen.getByLabelText(/year/i);
      await user.clear(yearInput);
      await user.type(yearInput, '2025');

      // Switch to yearly mode then click Export
      await user.click(screen.getByRole('button', { name: /^yearly$/i }));
      await user.click(screen.getByRole('button', { name: /^export$/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = openSpy.mock.calls[0][0] as string;
      expect(url).toBe('/api/export/yearly/2025');
      expect(openSpy.mock.calls[0][1]).toBe('_blank');
      openSpy.mockRestore();
    });

    test('saves currency rates via PUT with full currency object', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /currencies/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/rate for eur/i)).toBeInTheDocument();
      });

      const rateInput = screen.getByLabelText(/rate for eur/i);
      await user.clear(rateInput);
      await user.type(rateInput, '0.95');

      await user.click(screen.getByRole('button', { name: /save rates/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'currencies/EUR',
          expect.objectContaining({
            name: 'Euro',
            symbol: '\u20AC',
            rate_to_base: 0.95,
            is_base: false,
          }),
        );
      });
    });

    // The symbol field capped at 3 characters — both in the schema and as a
    // `maxLength` on the input — while the server allows 10
    // (MaxCurrencySymbolLength). Real symbols exceed 3: the Lebanese pound is
    // written "ل.ل.", four characters, and this household keeps its ledger in
    // LBP and USD. The `maxLength` made it worse than a validation error, since
    // the browser silently refused the fourth keystroke with nothing to read.
    // Two cases, doing two different jobs. "ل.ل." is the real one — four
    // characters, which the old cap of 3 refused outright. The row of
    // astral-plane characters is the one that discriminates the UNIT: it is
    // exactly at the limit in characters but twice that in UTF-16 code units,
    // so `String.length` or Zod's `.max()` would refuse it. Arabic cannot make
    // that distinction, because it is one UTF-16 unit per character.
    test('accepts the Lebanese pound symbol, typed keystroke by keystroke', async () => {
      mockedApi.post.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /currencies/i }));
      await waitFor(() => {
        expect(screen.getByLabelText(/^code$/i)).toBeInTheDocument();
      });

      await user.type(screen.getByLabelText(/^code$/i), 'LBP');
      await user.type(screen.getByLabelText(/^name$/i), 'Lebanese Pound');
      // user.type, deliberately: it is the only thing that exercises the
      // input's `maxLength`, and `maxLength={3}` swallowed the fourth
      // keystroke silently — no message, no way for the user to tell the
      // field from a broken keyboard. fireEvent would set the value directly
      // and never notice.
      await user.type(screen.getByLabelText(/^symbol$/i), 'ل.ل.');
      await user.clear(screen.getByLabelText(/rate to base/i));
      await user.type(screen.getByLabelText(/rate to base/i), '90000');

      await user.click(screen.getByRole('button', { name: /add currency/i }));

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith(
          'currencies',
          expect.objectContaining({ code: 'LBP', symbol: 'ل.ل.' }),
        );
      });
    });

    test('accepts a symbol of exactly the limit in astral-plane characters', async () => {
      mockedApi.post.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /currencies/i }));
      await waitFor(() => {
        expect(screen.getByLabelText(/^code$/i)).toBeInTheDocument();
      });

      const symbol = '𝟙'.repeat(MAX_CURRENCY_SYMBOL_LENGTH);
      await user.type(screen.getByLabelText(/^code$/i), 'XAA');
      await user.type(screen.getByLabelText(/^name$/i), 'Test Currency');
      fireEvent.change(screen.getByLabelText(/^symbol$/i), {
        target: { value: symbol },
      });
      await user.clear(screen.getByLabelText(/rate to base/i));
      await user.type(screen.getByLabelText(/rate to base/i), '2');

      await user.click(screen.getByRole('button', { name: /add currency/i }));

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith(
          'currencies',
          expect.objectContaining({ code: 'XAA', symbol }),
        );
      });
    });

    test('refuses a currency symbol one character over the limit', async () => {
      mockedApi.post.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /currencies/i }));
      await waitFor(() => {
        expect(screen.getByLabelText(/^symbol$/i)).toBeInTheDocument();
      });

      await user.type(screen.getByLabelText(/^code$/i), 'XXX');
      await user.type(screen.getByLabelText(/^name$/i), 'Too Wide');
      // fireEvent, because the input no longer carries a maxLength to stop it
      // and typing 11 characters one at a time proves nothing extra.
      fireEvent.change(screen.getByLabelText(/^symbol$/i), {
        target: { value: 'ب'.repeat(MAX_CURRENCY_SYMBOL_LENGTH + 1) },
      });
      await user.clear(screen.getByLabelText(/rate to base/i));
      await user.type(screen.getByLabelText(/rate to base/i), '2');

      await user.click(screen.getByRole('button', { name: /add currency/i }));

      expect(
        await screen.findByText(
          `Symbol must be ${MAX_CURRENCY_SYMBOL_LENGTH} characters or fewer`,
        ),
      ).toBeInTheDocument();
      expect(mockedApi.post).not.toHaveBeenCalled();
    });

    test('changes user role via PUT (not PATCH)', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /users/i }));

      await waitFor(() => {
        expect(
          screen.getByRole('combobox', { name: /role for alice/i }),
        ).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('combobox', { name: /role for alice/i }),
      );
      await user.click(screen.getByRole('option', { name: /^member$/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith('users/1', {
          role: 'member',
        });
      });
    });

    // B18. `alice` in the shared fixture IS the signed-in admin, so every test
    // here doubles as the motivating case: an admin shortening their own name
    // now that it labels every transaction they enter.
    describe('display name editor', () => {
      // The server's own cap, `MaxDisplayNameLength` in internal/api/limits.go,
      // written out rather than imported: the number is what has to agree
      // across the wire, and a test that reads the same constant the page reads
      // would still pass if both moved away from the server's.
      const LIMIT = 64;

      // alice is the signed-in admin; bob is somebody else. The shared fixture
      // lists alice alone, so the second row is seeded only where it is needed.
      const BOB = {
        id: 2,
        username: 'bob',
        display_name: 'Bob',
        role: 'member',
        created_at: '2024-01-01',
      };

      function seedUsers(list: unknown[]) {
        mockedApi.get.mockImplementation((path: string) =>
          path === 'users' ? Promise.resolve(list) : Promise.resolve([]),
        );
      }

      function renderUsersTab() {
        return render(
          <MemoryRouter initialEntries={['/settings?tab=users']}>
            <Settings />
          </MemoryRouter>,
          { wrapper: withQueryClient },
        );
      }

      async function openEditor(
        user: ReturnType<typeof userEvent.setup>,
        username = 'alice',
      ) {
        renderUsersTab();
        await waitFor(() => {
          expect(screen.getByText(username)).toBeInTheDocument();
        });
        await user.click(
          screen.getByRole('button', {
            name: new RegExp(`edit display name for ${username}`, 'i'),
          }),
        );
        return await screen.findByRole('dialog');
      }

      // Reset password is deliberately hidden on the admin's own row. This one
      // must NOT copy that rule, or the feature misses the case it exists for.
      test("offers the editor on the admin's own row", async () => {
        renderUsersTab();
        await waitFor(() => {
          expect(screen.getByText('alice')).toBeInTheDocument();
        });
        expect(
          screen.getByRole('button', { name: /edit display name for alice/i }),
        ).toBeInTheDocument();
        expect(
          screen.queryByRole('button', { name: /reset password for alice/i }),
        ).not.toBeInTheDocument();
      });

      test('PUTs display_name alone, never the role', async () => {
        mockedApi.put.mockResolvedValue({ status: 'updated' });
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        const field = within(dialog).getByLabelText('Display name');
        // Prefilled: the case this exists for is editing a name that is
        // already there, not typing one from scratch.
        expect(field).toHaveValue('Alice');
        await user.clear(field);
        await user.type(field, 'Ali');
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        await waitFor(() => {
          // Exact object, not objectContaining: the handler merges, so a
          // payload that also carried `role` would still succeed against the
          // server while turning every rename into a role write — and a role
          // write that differs from the stored one drops that user's sessions.
          expect(mockedApi.put).toHaveBeenCalledWith('users/1', {
            display_name: 'Ali',
          });
        });
        expect(mockedApi.put).toHaveBeenCalledTimes(1);
      });

      test('refetches the user list and invalidates cached transactions', async () => {
        mockedApi.put.mockResolvedValue({ status: 'updated' });
        const invalidate = vi.spyOn(QueryClient.prototype, 'invalidateQueries');
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        const usersFetchesBefore = mockedApi.get.mock.calls.filter(
          (call) => call[0] === 'users',
        ).length;

        await user.clear(within(dialog).getByLabelText('Display name'));
        await user.type(within(dialog).getByLabelText('Display name'), 'Ali');
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        await waitFor(() => {
          expect(mockedApi.put).toHaveBeenCalled();
        });

        // The response is {"status":"updated"} and carries no user object, so
        // the table is only correct again if the page asks the server again.
        await waitFor(() => {
          const after = mockedApi.get.mock.calls.filter(
            (call) => call[0] === 'users',
          ).length;
          expect(after).toBeGreaterThan(usersFetchesBefore);
        });

        // Display names are baked server-side into every transaction's
        // `created_by`, and a user PUT emits no SSE event — without this the
        // Transactions table and the /quick recently-added panel keep serving
        // the old name from cache. This is the assertion someone will
        // "simplify" away.
        await waitFor(() => {
          expect(invalidate).toHaveBeenCalledWith({
            queryKey: ['transactions'],
          });
        });
        invalidate.mockRestore();
      });

      test('refuses an empty name without calling the API', async () => {
        mockedApi.put.mockResolvedValue({ status: 'updated' });
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        await user.clear(within(dialog).getByLabelText('Display name'));
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        expect(
          await within(dialog).findByText(/display name is required/i),
        ).toBeInTheDocument();
        expect(mockedApi.put).not.toHaveBeenCalled();
      });

      // Whitespace-only is the case the server would answer with a 400 that
      // reads "display_name or role is required" — its own trim empties the
      // field, and the merge then leaves the old name in place. Caught here so
      // it never looks like a save that quietly did nothing.
      test('refuses a whitespace-only name without calling the API', async () => {
        mockedApi.put.mockResolvedValue({ status: 'updated' });
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        const field = within(dialog).getByLabelText('Display name');
        await user.clear(field);
        await user.type(field, '   ');
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        expect(
          await within(dialog).findByText(/display name is required/i),
        ).toBeInTheDocument();
        expect(mockedApi.put).not.toHaveBeenCalled();
      });

      // Astral-plane characters, so the pair discriminates the UNIT as well as
      // the number: 64 of these are 128 UTF-16 code units, which `.length` or
      // Zod's `.max()` would refuse while the server's rune count accepts them.
      test('refuses a display name one character over the limit', async () => {
        mockedApi.put.mockResolvedValue({ status: 'updated' });
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        const field = within(dialog).getByLabelText('Display name');
        // fireEvent: typing 65 characters one at a time proves nothing extra,
        // and the input carries no maxLength to stop it.
        fireEvent.change(field, { target: { value: '𝟙'.repeat(LIMIT + 1) } });
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        expect(
          await within(dialog).findByText(
            `Display name must be ${LIMIT} characters or fewer`,
          ),
        ).toBeInTheDocument();
        expect(mockedApi.put).not.toHaveBeenCalled();
      });

      test('accepts a display name of exactly the limit in astral-plane characters', async () => {
        mockedApi.put.mockResolvedValue({ status: 'updated' });
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);
        const atLimit = '𝟙'.repeat(LIMIT);

        fireEvent.change(within(dialog).getByLabelText('Display name'), {
          target: { value: atLimit },
        });
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        await waitFor(() => {
          expect(mockedApi.put).toHaveBeenCalledWith('users/1', {
            display_name: atLimit,
          });
        });
      });

      // The pair below is the whole point of gating the refresh. Renaming
      // YOURSELF has to move the name the shell renders out of the auth
      // context; renaming anyone else must not spend a /auth/me round-trip
      // that could only answer with your own unchanged profile.
      // Delete sits third in a row of three on a surface whose primary input is
      // a thumb, and it is the only one that destroys an account. Both halves
      // matter: the first click must NOT delete, and the confirm must.
      test('deletes only after the confirmation is accepted', async () => {
        mockedApi.del.mockResolvedValue({});
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderUsersTab();
        await waitFor(() => {
          expect(screen.getByText('alice')).toBeInTheDocument();
        });

        await user.click(screen.getByRole('button', { name: /delete alice/i }));
        const confirm = await screen.findByRole('alertdialog');
        // Opening the confirmation is not the deletion.
        expect(mockedApi.del).not.toHaveBeenCalled();

        await user.click(
          within(confirm).getByRole('button', { name: /^delete user$/i }),
        );

        await waitFor(() => {
          expect(mockedApi.del).toHaveBeenCalledWith('users/1');
        });
      });

      test('deletes nothing when the confirmation is cancelled', async () => {
        mockedApi.del.mockResolvedValue({});
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderUsersTab();
        await waitFor(() => {
          expect(screen.getByText('alice')).toBeInTheDocument();
        });

        await user.click(screen.getByRole('button', { name: /delete alice/i }));
        const confirm = await screen.findByRole('alertdialog');
        await user.click(
          within(confirm).getByRole('button', { name: /^cancel$/i }),
        );

        await waitFor(() => {
          expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
        });
        expect(mockedApi.del).not.toHaveBeenCalled();
      });

      // The cascade the dialog must NOT promise. transactions.user_id is ON
      // DELETE CASCADE, but the handler refuses with 409 before it can fire, so
      // copy claiming the ledger goes too would be false.
      test('the confirmation says the ledger is not at stake', async () => {
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderUsersTab();
        await waitFor(() => {
          expect(screen.getByText('alice')).toBeInTheDocument();
        });

        await user.click(screen.getByRole('button', { name: /delete alice/i }));
        const confirm = await screen.findByRole('alertdialog');

        expect(
          within(confirm).getByText(/no ledger history is at stake/i),
        ).toBeInTheDocument();
        expect(
          within(confirm).queryByText(/transactions will be deleted/i),
        ).not.toBeInTheDocument();
      });

      test('re-reads your own profile after you rename yourself', async () => {
        mockedApi.put.mockResolvedValue({ status: 'updated' });
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        await user.clear(within(dialog).getByLabelText('Display name'));
        await user.type(within(dialog).getByLabelText('Display name'), 'Ali');
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        await waitFor(() => {
          expect(mockRefreshUser).toHaveBeenCalledTimes(1);
        });
      });

      test('does NOT re-read your profile when you rename somebody else', async () => {
        seedUsers([
          {
            id: 1,
            username: 'alice',
            display_name: 'Alice',
            role: 'admin',
            created_at: '2024-01-01',
          },
          BOB,
        ]);
        mockedApi.put.mockResolvedValue({ status: 'updated' });
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user, 'bob');

        await user.clear(within(dialog).getByLabelText('Display name'));
        await user.type(within(dialog).getByLabelText('Display name'), 'Bobby');
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        // The rename itself still went through — otherwise this test would
        // pass on a page that simply never saves.
        await waitFor(() => {
          expect(mockedApi.put).toHaveBeenCalledWith('users/2', {
            display_name: 'Bobby',
          });
        });
        expect(mockRefreshUser).not.toHaveBeenCalled();
      });

      // A 400 is the server judging this VALUE, so it belongs on the field.
      test('puts a 400 from the server on the field, not in a toast', async () => {
        mockedApi.put.mockRejectedValue(
          new ApiError('display name must be 64 characters or less', 400),
        );
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        await user.clear(within(dialog).getByLabelText('Display name'));
        await user.type(within(dialog).getByLabelText('Display name'), 'Ali');
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        expect(
          await within(dialog).findByText(/64 characters or less/i),
        ).toBeInTheDocument();
        expect(mockedToast.error).not.toHaveBeenCalled();
        expect(mockedToast.success).not.toHaveBeenCalled();
      });

      // A transport failure is not a verdict on the name. Rendering "Failed to
      // fetch" as field validation tells the admin their name is wrong when it
      // is fine, and leaves them retyping a value the server never saw.
      test('sends a transport failure to a toast, not onto the field', async () => {
        mockedApi.put.mockRejectedValue(new Error('Failed to fetch'));
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        await user.clear(within(dialog).getByLabelText('Display name'));
        await user.type(within(dialog).getByLabelText('Display name'), 'Ali');
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        await waitFor(() => {
          expect(mockedToast.error).toHaveBeenCalledWith('Failed to fetch');
        });
        expect(
          within(dialog).queryByText(/failed to fetch/i),
        ).not.toBeInTheDocument();
      });

      // A 500 is the server answering, but not about this value either.
      test('sends a 500 to a toast, not onto the field', async () => {
        mockedApi.put.mockRejectedValue(new ApiError('internal error', 500));
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        await user.clear(within(dialog).getByLabelText('Display name'));
        await user.type(within(dialog).getByLabelText('Display name'), 'Ali');
        await user.click(within(dialog).getByRole('button', { name: /^save$/i }));

        await waitFor(() => {
          expect(mockedToast.error).toHaveBeenCalledWith('internal error');
        });
        expect(
          within(dialog).queryByText(/internal error/i),
        ).not.toBeInTheDocument();
      });

      // Renaming yourself is the case this editor exists for, so the copy has
      // to address the person doing it rather than describe them.
      test('addresses you in the second person on your own row', async () => {
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user);

        expect(
          within(dialog).getByText(/this is the name spendrop shows for you/i),
        ).toBeInTheDocument();
        expect(
          within(dialog).queryByText(/every transaction they enter/i),
        ).not.toBeInTheDocument();
      });

      test("keeps the third person on somebody else's row", async () => {
        seedUsers([
          {
            id: 1,
            username: 'alice',
            display_name: 'Alice',
            role: 'admin',
            created_at: '2024-01-01',
          },
          BOB,
        ]);
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const dialog = await openEditor(user, 'bob');

        expect(
          within(dialog).getByText(/every transaction they enter/i),
        ).toBeInTheDocument();
        expect(
          within(dialog).queryByText(/shows for you/i),
        ).not.toBeInTheDocument();
      });
    });
  });

  describe('forwarding toast for moved tabs', () => {
    function renderAt(path: string) {
      return render(
        <MemoryRouter initialEntries={[path]}>
          <Settings />
        </MemoryRouter>,
        { wrapper: withQueryClient },
      );
    }

    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: {
          id: 1,
          username: 'alice',
          display_name: 'Alice',
          role: 'admin',
          created_at: '2024-01-01',
        },
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('shows a forwarding toast for ?tab=savings naming the Savings page', () => {
      renderAt('/settings?tab=savings');
      expect(mockedToast.info).toHaveBeenCalledWith(
        'Savings has its own page now',
        expect.objectContaining({
          action: expect.objectContaining({ label: 'Open' }),
        }),
      );
    });

    test('shows a forwarding toast for ?tab=budgets', () => {
      renderAt('/settings?tab=budgets');
      expect(mockedToast.info).toHaveBeenCalledWith(
        'Budgets has its own page now',
        expect.any(Object),
      );
    });

    test('?tab=general forwards to Budgets (closest equivalent)', () => {
      renderAt('/settings?tab=general');
      expect(mockedToast.info).toHaveBeenCalledWith(
        'Budgets has its own page now',
        expect.any(Object),
      );
    });

    test('does NOT toast for a valid Settings tab', () => {
      renderAt('/settings?tab=account');
      expect(mockedToast.info).not.toHaveBeenCalled();
    });

    test('does NOT toast when no ?tab param is present', () => {
      renderAt('/settings');
      expect(mockedToast.info).not.toHaveBeenCalled();
    });

    test('toast Open action navigates to the new page', () => {
      renderAt('/settings?tab=savings');
      // Read the action callback handed to toast.info and invoke it.
      // We don't want to spy on react-router internally — just check
      // the action is wired to a function and clicking it produces
      // something callable.
      const calls = mockedToast.info.mock.calls;
      expect(calls.length).toBeGreaterThan(0);
      const arg = calls[0][1] as { action?: { onClick: () => void } };
      expect(typeof arg.action?.onClick).toBe('function');
      // Calling it shouldn't throw — react-router's navigate inside
      // MemoryRouter is a no-op for our purposes here; the contract
      // is "Open action calls a function tied to the moved route".
      arg.action!.onClick();
    });
  });

  describe('as member', () => {
    const originalFetch = globalThis.fetch;

    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: {
          id: 2,
          username: 'bob',
          display_name: 'Bob',
          role: 'member',
          created_at: '2024-01-01',
        },
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    afterEach(() => {
      globalThis.fetch = originalFetch;
    });

    // A member sees one trigger fewer, which is still wider than a phone. The
    // strip is on the container, not per-role, so this is the check that it
    // was not attached to the admin branch by accident.
    test('scrolls the tab strip for a member too', () => {
      renderSettings();
      const strip = screen.getByRole('tablist');
      expect(strip).toHaveClass('overflow-x-auto', 'max-w-full', 'justify-start');
      expect(strip).not.toHaveClass('justify-center');
    });

    test('hides users tab for non-admin', () => {
      renderSettings();
      expect(
        screen.queryByRole('tab', { name: /users/i }),
      ).not.toBeInTheDocument();
    });

    test('still shows account, currencies, api-tokens, and export tabs', () => {
      renderSettings();
      expect(screen.getByRole('tab', { name: /account/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /currencies/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /api tokens/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /^export$/i })).toBeInTheDocument();
    });

    // /api/import/* is behind auth.RequireAdmin, so the tab a member sees
    // holds only the Export card. Naming it "Import / Export" would have
    // the container promise a capability the panel cannot deliver, which
    // is the half a card-only gate leaves behind.
    test('labels the data tab "Export", never "Import / Export"', () => {
      renderSettings();
      expect(
        screen.queryByRole('tab', { name: /import/i }),
      ).not.toBeInTheDocument();
    });

    test('data tab keeps export but drops the import card', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /^export$/i }));

      await waitFor(() => {
        // Export stays fully available — it is not admin-gated server-side.
        expect(
          screen.getByRole('button', { name: /^export$/i }),
        ).toBeInTheDocument();
      });
      // CardTitle renders as a <div>, so the card is matched by text.
      expect(screen.queryByText(/^import$/i)).not.toBeInTheDocument();
      expect(screen.queryByLabelText(/excel file/i)).not.toBeInTheDocument();
    });

    // The import hooks must not mount for a member at all, not merely
    // render nothing: useImportSession's mount effect resumes a stored
    // import id with GET /api/import/{id} (raw fetch, not the mocked api
    // client), and a member who had a wizard open before the route was
    // gated still has that id in localStorage. Gating only the card's JSX
    // would leave that request firing and 403ing. Once, not repeatedly —
    // the effect's catch drops the stored id before inspecting the error
    // — but that one 403 is not the NotFoundError the catch stays silent
    // for, so it surfaces as a raw `forbidden` banner to a member who
    // cannot act on it.
    test('does not resume a stored import session', async () => {
      localStorage.setItem(STORAGE_KEYS.importId, 'stale-import-id');
      // Stubbed as the 403 the gated route actually returns to a member,
      // so that if the gate regresses this test fails on the assertion
      // below rather than on some incidental crash inside the hook.
      const fetchMock = vi.fn().mockResolvedValue({
        ok: false,
        status: 403,
        json: () => Promise.resolve({ error: 'admin access required' }),
      } as Response);
      globalThis.fetch = fetchMock;

      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();
      await user.click(screen.getByRole('tab', { name: /^export$/i }));

      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^export$/i }),
        ).toBeInTheDocument();
      });

      const importCalls = fetchMock.mock.calls.filter((call) =>
        String(call[0]).includes('/import/'),
      );
      expect(importCalls).toHaveLength(0);
    });
  });

  describe('Import Wizard', () => {
    const mockCategories: Category[] = [
      { id: 1, name: 'Food', type: 'expense', icon: null, sort_order: 1, is_active: true, created_at: '2026-01-01' },
      { id: 2, name: 'Transport', type: 'expense', icon: null, sort_order: 2, is_active: true, created_at: '2026-01-01' },
      { id: 3, name: 'Salary', type: 'income', icon: null, sort_order: 3, is_active: true, created_at: '2026-01-01' },
    ];

    const mockPreview: ImportPreview = {
      import_id: 'abc-123',
      row_count: 5,
      rows: [
        { row_id: 0, skip: false, content_hash: '', date: '2026-01-15', description: 'Grocery Store', amount: 45.50, category: 'Food' },
        { row_id: 1, skip: false, content_hash: '', date: '2026-01-16', description: 'Bus Ticket', amount: 2.50, category: 'Transport' },
        { row_id: 2, skip: false, content_hash: '', date: '2026-01-17', description: 'Coffee Shop', amount: 5.00, category: 'Unknown' },
      ],
      columns: ['date', 'description', 'amount', 'category'],
      unique_categories: ['Food', 'Transport', 'Unknown'],
      collision_groups: [],
      expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
    };

    const mockImportResult: ImportResult = {
      imported: 4,
      skipped: 1,
      total: 5,
    };

    function makeXlsxFile(name = 'transactions.xlsx') {
      return new File(['test'], name, {
        type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      });
    }

    // useImportSession hits raw `fetch` for POST /api/import/confirm (to
    // preserve the 409 UNRESOLVED_COLLISIONS body) rather than going
    // through the mocked `api.post`. These tests stub `globalThis.fetch`
    // per-case with a small helper and restore the original afterwards.
    const originalFetch = globalThis.fetch;

    function mockConfirmFetch(spec: {
      ok?: boolean;
      status?: number;
      body?: unknown;
    }): ReturnType<typeof vi.fn> {
      const fetchMock = vi.fn().mockResolvedValue({
        ok: spec.ok ?? true,
        status: spec.status ?? 200,
        json: () => Promise.resolve(spec.body ?? {}),
      } as Response);
      globalThis.fetch = fetchMock;
      return fetchMock;
    }

    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: {
          id: 1,
          username: 'alice',
          display_name: 'Alice',
          role: 'admin',
          created_at: '2024-01-01',
        },
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    afterEach(() => {
      globalThis.fetch = originalFetch;
    });

    async function goToDataTab() {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();
      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));
      return user;
    }

    test('shows import section with file input when data tab is active', async () => {
      await goToDataTab();

      // CardTitle renders as a <div>, not a heading element
      expect(screen.getByText(/^import$/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/excel file/i)).toBeInTheDocument();
    });

    test('shows info text about required columns', async () => {
      await goToDataTab();

      expect(screen.getByText(/date.*description.*amount/i)).toBeInTheDocument();
    });

    // End-to-end through Settings → ImportCard → ImportPreviewStep →
    // ImportPreviewTable, deliberately not through the hook or the table
    // in isolation. Both of those are gated correctly on their own; what
    // this pins is that the page actually WIRES the gate — the failure
    // shape where a control is right in isolation and the caller hands it
    // the weaker variant has already shipped twice in this batch.
    test('an over-long field in the upload response blocks Import on the real page', async () => {
      const serverNoteMessage =
        "This row's note is longer than the 2,000 characters SpenDrop stores. Skip this row, or shorten the note in your spreadsheet and upload again.";
      mockedApi.upload.mockResolvedValue({
        ...mockPreview,
        field_errors: [
          { row_id: 1, field: 'notes', message: serverNoteMessage },
        ],
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      // Blocked on arrival — no confirm round-trip was needed to learn
      // that the server would refuse this row.
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^Import \d+$/ }),
        ).toBeDisabled();
      });
      // The server's sentence reaches the screen unaltered through every
      // layer between the API client and the table.
      expect(screen.getByText(serverNoteMessage)).toBeInTheDocument();
      expect(
        screen.getByRole('button', { name: 'Skip this row' }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(/Fix or skip 1 too-long row to enable import/),
      ).toBeInTheDocument();
    });

    test('uploads file and shows preview on file selection', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();

      const fileInput = screen.getByLabelText(/excel file/i);
      await user.upload(fileInput, makeXlsxFile());

      await waitFor(() => {
        expect(mockedApi.upload).toHaveBeenCalledWith('import/upload', expect.any(File));
      });

      await waitFor(() => {
        expect(screen.getByText(/found 5 rows/i)).toBeInTheDocument();
      });
    });

    test('shows preview table with imported data', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByText('Grocery Store')).toBeInTheDocument();
        expect(screen.getByText('Bus Ticket')).toBeInTheDocument();
        expect(screen.getByText('Coffee Shop')).toBeInTheDocument();
      });
    });

    // The default category control appears when it has a job to do, and
    // not otherwise: rows with an empty Category cell need it, a file whose
    // categories all match does not. Asking for a decision that changes
    // nothing is how a control ends up ignored.
    test('shows default category dropdown when rows have an empty category', async () => {
      mockedApi.upload.mockResolvedValue({
        ...mockPreview,
        unresolved_categories: [
          { name: '', reason: 'missing' as const, row_ids: [2] },
        ],
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByLabelText(/default category/i)).toBeInTheDocument();
      });
    });

    test('hides the default category dropdown when nothing needs one', async () => {
      mockedApi.upload.mockResolvedValue({
        ...mockPreview,
        unresolved_categories: [],
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^import \d+$/i }),
        ).toBeEnabled();
      });
      expect(screen.queryByLabelText(/default category/i)).toBeNull();
    });

    test('shows import and cancel buttons in preview step', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        // Button label dropped the " Rows" suffix in 3.4b — the label now
        // comes from ImportPreviewTable's footer (`Import ${keepCount}`).
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument();
      });
    });

    test('confirms import and shows result', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockConfirmFetch({ body: mockImportResult });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });

      // 3.4b collapsed the confirm modal — clicking the Import button now
      // fires the confirm POST directly. The old two-step (click Import →
      // dialog → click "Confirm and Import") is gone.
      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      // A run that did not land every row leads with what did not land.
      // The old flat "4 imported, 1 skipped" sentence read identically
      // whether one row or four hundred had been dropped.
      await waitFor(() => {
        expect(
          screen.getByText(/1 of 5 rows were not imported/i),
        ).toBeInTheDocument();
      });
      expect(
        screen.getByText(/4 rows were added to your ledger/i),
      ).toBeInTheDocument();
    });

    test('shows "Import Another" button after successful import', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockConfirmFetch({ body: mockImportResult });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /import another/i })).toBeInTheDocument();
      });
    });

    test('resets to upload step when cancel is clicked', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.del.mockResolvedValue(undefined);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /cancel/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/excel file/i)).toBeInTheDocument();
      });
    });

    test('shows error message on upload failure', async () => {
      mockedApi.upload.mockRejectedValue(new Error('Invalid file format'));
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile('bad.xlsx'));

      await waitFor(() => {
        expect(screen.getByText(/invalid file format/i)).toBeInTheDocument();
      });
    });

    test('shows error message on confirm failure', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      // Non-409 confirm failure — the hook surfaces `err.error` from the
      // backend body via `importSession.error`, which the Alert renders
      // inline. 3.4b dropped the old toast path because a destructive
      // alert is more durable than a dismissable toast for a blocking
      // error on a multi-step wizard.
      mockConfirmFetch({
        ok: false,
        status: 500,
        body: { error: 'Import failed: duplicate rows' },
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      await waitFor(() => {
        expect(
          screen.getByText('Import failed: duplicate rows'),
        ).toBeInTheDocument();
      });
    });

    test('resets to upload step when "Import Another" is clicked', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockConfirmFetch({ body: mockImportResult });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /import another/i })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: /import another/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/excel file/i)).toBeInTheDocument();
      });
    });

    test('shows category mapping section in preview', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByText(/category mapping/i)).toBeInTheDocument();
      });
    });

    test('sends import_id when confirming import', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      const fetchMock = mockConfirmFetch({ body: mockImportResult });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      // `confirmImport` serializes its payload with JSON.stringify before
      // handing it to fetch, so we inspect the body string here rather
      // than relying on a structural matcher against the api.post mock
      // (which the hook bypasses — see web/src/api/import.ts).
      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalled();
      });
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toContain('/api/import/confirm');
      expect(init.method).toBe('POST');
      const parsedBody = JSON.parse(init.body as string) as {
        import_id: string;
      };
      expect(parsedBody.import_id).toBe('abc-123');
    });

    // End-to-end through Settings -> ImportCard -> ImportPreviewStep ->
    // ImportPreviewTable, deliberately not through the hook in isolation.
    // The hook's gate and the panel's controls are each correct on their
    // own; what these pin is that the page WIRES them together — the
    // failure shape where a control is right in isolation and the caller
    // hands it the weaker variant has shipped in this repo before.
    describe('unresolved categories', () => {
      const previewWithUnmapped: ImportPreview = {
        ...mockPreview,
        unresolved_categories: [
          { name: 'Grocries', reason: 'unmapped', row_ids: [0, 2] },
        ],
      };

      function stubCategoriesFetch() {
        mockedApi.get.mockImplementation((path: string) => {
          if (path === 'categories') return Promise.resolve(mockCategories);
          return Promise.resolve([]);
        });
      }

      test('a category matching nothing blocks Import on the real page', async () => {
        mockedApi.upload.mockResolvedValue(previewWithUnmapped);
        stubCategoriesFetch();

        const user = await goToDataTab();
        await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

        await waitFor(() => {
          expect(
            screen.getByRole('button', { name: /^Import \d+$/ }),
          ).toBeDisabled();
        });
        expect(
          screen.getByText(/1 category choice still needed below/),
        ).toBeInTheDocument();
        // How many rows the decision covers is the difference between
        // "one typo" and "half my ledger".
        expect(screen.getByText('2 rows')).toBeInTheDocument();
        expect(
          screen.getByRole('combobox', { name: /Map category Grocries/i }),
        ).toBeInTheDocument();
      });

      test('mapping the name through the panel enables Import', async () => {
        mockedApi.upload.mockResolvedValue(previewWithUnmapped);
        stubCategoriesFetch();

        const user = await goToDataTab();
        await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

        await waitFor(() => {
          expect(
            screen.getByRole('combobox', { name: /Map category Grocries/i }),
          ).toBeInTheDocument();
        });

        await user.click(
          screen.getByRole('combobox', { name: /Map category Grocries/i }),
        );
        await user.click(screen.getByRole('option', { name: 'Transport' }));

        await waitFor(() => {
          expect(
            screen.getByRole('button', { name: /^Import \d+$/ }),
          ).toBeEnabled();
        });
      });

      // Accepting the default for a name that matched nothing is allowed —
      // it just has to be something the user DID, not something that
      // happened to them because they picked a default for empty cells.
      test('choosing a default alone does not unblock an unmatched name', async () => {
        mockedApi.upload.mockResolvedValue({
          ...previewWithUnmapped,
          unresolved_categories: [
            { name: 'Grocries', reason: 'unmapped' as const, row_ids: [0, 2] },
            { name: '', reason: 'missing' as const, row_ids: [1] },
          ],
        });
        stubCategoriesFetch();

        const user = await goToDataTab();
        await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

        await waitFor(() => {
          expect(screen.getByLabelText(/default category/i)).toBeInTheDocument();
        });

        // Two decisions outstanding: the typo'd name, and the empty cells.
        expect(
          screen.getByText(/2 category choices still needed below/),
        ).toBeInTheDocument();

        await user.click(screen.getByLabelText(/default category/i));
        await user.click(screen.getByRole('option', { name: 'Food' }));

        // The default settled the empty cells and ONLY the empty cells —
        // two down to one, not two down to zero. Asserting the button is
        // still disabled would pass vacuously, since it was disabled
        // before the click too.
        await waitFor(() => {
          expect(
            screen.getByText(/1 category choice still needed below/),
          ).toBeInTheDocument();
        });
        expect(
          screen.getByRole('button', { name: /^Import \d+$/ }),
        ).toBeDisabled();
      });

      test('the bulk control files every remaining name under the default in one click', async () => {
        mockedApi.upload.mockResolvedValue(previewWithUnmapped);
        stubCategoriesFetch();
        const fetchMock = mockConfirmFetch({ body: mockImportResult });

        const user = await goToDataTab();
        await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

        await waitFor(() => {
          expect(screen.getByLabelText(/default category/i)).toBeInTheDocument();
        });

        // Nothing to apply until there is a default to apply.
        expect(
          screen.getByRole('button', {
            name: /Pick a default below to fill these at once/i,
          }),
        ).toBeDisabled();

        await user.click(screen.getByLabelText(/default category/i));
        await user.click(screen.getByRole('option', { name: 'Food' }));
        await user.click(
          screen.getByRole('button', { name: /Use Food for the remaining 1/i }),
        );

        await waitFor(() => {
          expect(
            screen.getByRole('button', { name: /^Import \d+$/ }),
          ).toBeEnabled();
        });

        await user.click(screen.getByRole('button', { name: /^Import \d+$/ }));

        // The decision travels as an explicit mapping, not as a fallback
        // the server has to infer. That is what makes it auditable in the
        // request and visible in every control afterwards.
        await waitFor(() => {
          expect(fetchMock).toHaveBeenCalled();
        });
        const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
        const parsedBody = JSON.parse(init.body as string) as {
          category_map: Record<string, number>;
        };
        expect(parsedBody.category_map.Grocries).toBe(1);
      });
    });

    describe('import result', () => {
      test('leads with what did not land, itemised', async () => {
        mockedApi.upload.mockResolvedValue(mockPreview);
        mockConfirmFetch({
          body: {
            imported: 12,
            skipped: 488,
            total: 500,
            skipped_reasons: { duplicate: 400, user_skipped: 88 },
          },
        });
        mockedApi.get.mockImplementation((path: string) => {
          if (path === 'categories') return Promise.resolve(mockCategories);
          return Promise.resolve([]);
        });

        const user = await goToDataTab();
        await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());
        await waitFor(() => {
          expect(
            screen.getByRole('button', { name: /^import \d+$/i }),
          ).toBeInTheDocument();
        });
        await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

        await waitFor(() => {
          expect(
            screen.getByText(/488 of 500 rows were not imported/i),
          ).toBeInTheDocument();
        });
        // A bare count is something the user can neither trust nor act on.
        expect(
          screen.getByText(/400 already in your ledger/i),
        ).toBeInTheDocument();
        expect(screen.getByText(/88 you skipped/i)).toBeInTheDocument();
      });

      test('a clean run says so without an alarm', async () => {
        mockedApi.upload.mockResolvedValue(mockPreview);
        mockConfirmFetch({
          body: { imported: 5, skipped: 0, total: 5, skipped_reasons: {} },
        });
        mockedApi.get.mockImplementation((path: string) => {
          if (path === 'categories') return Promise.resolve(mockCategories);
          return Promise.resolve([]);
        });

        const user = await goToDataTab();
        await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());
        await waitFor(() => {
          expect(
            screen.getByRole('button', { name: /^import \d+$/i }),
          ).toBeInTheDocument();
        });
        await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

        await waitFor(() => {
          expect(screen.getByText(/Imported all 5 rows/i)).toBeInTheDocument();
        });
        expect(screen.queryByText(/were not imported/i)).toBeNull();
      });
    });
  });
});
