import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../hooks/useAuth', () => ({ useAuth: vi.fn() }));
vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
    upload: vi.fn(),
  },
}));
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { toast } from 'sonner';
import { Settings } from './Settings';
import type { ApiToken, ListTokensResponse } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

// Helper: seed `mockedApi.get` with the responses Settings.tsx fires on
// mount. The api-tokens list is opt-in per test so we can exercise both
// the empty-state branch and the populated-list branch.
function seedGetMock(apiTokens: ApiToken[] = []) {
  mockedApi.get.mockImplementation((path: string) => {
    if (path === 'api-tokens') {
      const body: ListTokensResponse = { tokens: apiTokens };
      return Promise.resolve(body);
    }
    // Every other Settings.tsx section fetches on mount. Returning `[]`
    // (or a minimal shape) is enough because those sections are not
    // under test here; we only care that their fetches don't throw.
    if (path === 'users') return Promise.resolve([]);
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

function renderSettingsOnApiTokensTab() {
  return render(
    <MemoryRouter initialEntries={['/settings?tab=api-tokens']}>
      <Settings />
    </MemoryRouter>,
    { wrapper: withQueryClient },
  );
}

describe('ApiTokensSection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    mockedUseAuth.mockReturnValue({
      user: {
        id: 1,
        username: 'alice',
        display_name: 'Alice',
        role: 'member',
        created_at: '2024-01-01',
      },
      loading: false,
      unverified: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: vi.fn(),
    });
    // happy-dom has no Clipboard API by default; install a writable
    // spy so Task 7.5's Copy button has something to call into. Installed
    // in every test so no task needs to come back and add it later.
    Object.defineProperty(window.navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      writable: true,
      configurable: true,
    });
  });

  test('renders empty state when no tokens exist', async () => {
    seedGetMock([]);
    renderSettingsOnApiTokensTab();
    // The empty-state copy is rendered inside the card body. Wait for
    // the list fetch to resolve before asserting — mount fires `api.get`
    // asynchronously.
    await waitFor(() => {
      expect(
        screen.getByText(/no api tokens yet/i),
      ).toBeInTheDocument();
    });
    // Generic wording — no Homepage name-drop in the empty state.
    expect(
      screen.getByText(
        /create one to connect a script, dashboard, or other tool/i,
      ),
    ).toBeInTheDocument();
  });

  test('list shows token_prefix but never full token', async () => {
    const token: ApiToken = {
      id: 7,
      name: 'Homepage dashboard',
      token_prefix: 'spdr_aB3xQ9z7kL',
      created_at: '2026-04-18T14:23:00Z',
      last_used_at: null,
      last_used_ip: null,
      expires_at: null,
    };
    seedGetMock([token]);
    renderSettingsOnApiTokensTab();
    await waitFor(() => {
      // Prefix must be rendered (exact string match, monospaced cell).
      expect(screen.getByText('spdr_aB3xQ9z7kL')).toBeInTheDocument();
    });
    // The server never sent a full plaintext on the list response, so the
    // component has no way to render one. Guard this anyway: assert no
    // element in the tree contains a 38-char token-shaped string.
    const fullTokenRegex = /spdr_[a-zA-Z0-9]{26}_[a-zA-Z0-9]{6}/;
    expect(screen.queryByText(fullTokenRegex)).not.toBeInTheDocument();
  });

  test('clicking Create token opens the create dialog', async () => {
    const user = userEvent.setup();
    seedGetMock([]);
    renderSettingsOnApiTokensTab();
    // Wait for the empty state so we know the mount fetch resolved.
    await waitFor(() => {
      expect(screen.getByText(/no api tokens yet/i)).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /^create token$/i }),
    );
    // The dialog body has a header with "Create API token" and the
    // form fields. Assert on the header + Name field to prove the dialog
    // is the create form (not some other modal).
    expect(
      screen.getByRole('heading', { name: /create api token/i }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^name$/i)).toBeInTheDocument();
    // Password field was removed — if a future refactor adds it back,
    // this assertion catches it.
    expect(
      screen.queryByLabelText(/password/i),
    ).not.toBeInTheDocument();
  });

  test('submitting create dialog shows show-once reveal (no password field)', async () => {
    const user = userEvent.setup();
    seedGetMock([]);
    // Wire the POST response. The server returns the full plaintext
    // exactly once in this body; the component must echo it into a
    // reveal block and then forget it.
    mockedApi.post.mockResolvedValueOnce({
      id: 7,
      name: 'Homepage',
      token_prefix: 'spdr_aB3xQ9z7kL',
      created_at: '2026-04-18T14:23:00Z',
      last_used_at: null,
      last_used_ip: null,
      expires_at: null,
      token: 'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123',
    });
    renderSettingsOnApiTokensTab();
    await waitFor(() => {
      expect(screen.getByText(/no api tokens yet/i)).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /^create token$/i }),
    );
    await user.type(screen.getByLabelText(/^name$/i), 'Homepage');
    // Submit button inside the create form (distinguished from the
    // trigger outside by the form's button role).
    // Both the dialog trigger and the submit button say "Create token".
    // Scope to the dialog to hit the submit button specifically.
    {
      const dialog = screen.getByRole('dialog');
      await user.click(
        within(dialog).getByRole('button', { name: /^create token$/i }),
      );
    }
    // The dialog content is swapped in-place — look for the new copy
    // from the redesign and the full plaintext in a read-only input.
    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: /save your new token/i }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText(/copy it now/i)).toBeInTheDocument();
    const revealInput = screen.getByLabelText(
      /your new api token/i,
    ) as HTMLInputElement;
    expect(revealInput.value).toBe(
      'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123',
    );
    expect(revealInput.readOnly).toBe(true);
    // The POST body must carry exactly what the user typed and must NOT
    // include `password` — the backend no longer requires it.
    expect(mockedApi.post).toHaveBeenCalledWith('api-tokens', {
      name: 'Homepage',
      expires_at: null,
    });
  });

  test('Copy button writes to navigator.clipboard and fires success toast', async () => {
    const user = userEvent.setup();
    // userEvent.setup() attaches its own clipboard stub to navigator,
    // which replaces the `vi.fn` spy installed in beforeEach. Re-install
    // our writable spy AFTER setup() so the Copy button's call can be
    // asserted. Both stubs resolve to `undefined`, so no behavior drift.
    const writeTextSpy = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window.navigator, 'clipboard', {
      value: { writeText: writeTextSpy },
      writable: true,
      configurable: true,
    });
    seedGetMock([]);
    mockedApi.post.mockResolvedValueOnce({
      id: 7,
      name: 'Homepage',
      token_prefix: 'spdr_aB3xQ9z7kL',
      created_at: '2026-04-18T14:23:00Z',
      last_used_at: null,
      last_used_ip: null,
      expires_at: null,
      token: 'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123',
    });
    renderSettingsOnApiTokensTab();
    await waitFor(() => {
      expect(screen.getByText(/no api tokens yet/i)).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /^create token$/i }),
    );
    await user.type(screen.getByLabelText(/^name$/i), 'Homepage');
    // Both the dialog trigger and the submit button say "Create token".
    // Scope to the dialog to hit the submit button specifically.
    {
      const dialog = screen.getByRole('dialog');
      await user.click(
        within(dialog).getByRole('button', { name: /^create token$/i }),
      );
    }
    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: /save your new token/i }),
      ).toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: /^copy$/i }));
    // The writeText spy captures the exact string we handed the
    // clipboard. That string is the only place the plaintext should
    // land; a successful copy therefore also proves the reveal input's
    // value is the plaintext (not the prefix).
    expect(writeTextSpy).toHaveBeenCalledWith(
      'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123',
    );
    await waitFor(() => {
      // sonner's toast.success is mocked at the module level — assert
      // the success variant fired, not the error variant.
      expect(vi.mocked(toast).success).toHaveBeenCalledWith(
        'Copied to clipboard',
      );
    });
  });

  test('Copy button falls back to focus+select when clipboard rejects', async () => {
    const user = userEvent.setup();
    // Simulate an insecure-context clipboard: writeText rejects. The
    // component must focus+select the reveal input and fire the info
    // toast so the user can still copy with Ctrl/Cmd+C.
    const writeTextSpy = vi
      .fn()
      .mockRejectedValue(new Error('clipboard blocked'));
    Object.defineProperty(window.navigator, 'clipboard', {
      value: { writeText: writeTextSpy },
      writable: true,
      configurable: true,
    });
    seedGetMock([]);
    mockedApi.post.mockResolvedValueOnce({
      id: 7,
      name: 'Homepage',
      token_prefix: 'spdr_aB3xQ9z7kL',
      created_at: '2026-04-18T14:23:00Z',
      last_used_at: null,
      last_used_ip: null,
      expires_at: null,
      token: 'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123',
    });
    renderSettingsOnApiTokensTab();
    await waitFor(() => {
      expect(screen.getByText(/no api tokens yet/i)).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /^create token$/i }),
    );
    await user.type(screen.getByLabelText(/^name$/i), 'Homepage');
    // Both the dialog trigger and the submit button say "Create token".
    // Scope to the dialog to hit the submit button specifically.
    {
      const dialog = screen.getByRole('dialog');
      await user.click(
        within(dialog).getByRole('button', { name: /^create token$/i }),
      );
    }
    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: /save your new token/i }),
      ).toBeInTheDocument();
    });
    const revealInput = screen.getByLabelText(
      /your new api token/i,
    ) as HTMLInputElement;
    await user.click(screen.getByRole('button', { name: /^copy$/i }));
    // writeText was attempted (and rejected).
    expect(writeTextSpy).toHaveBeenCalled();
    await waitFor(() => {
      expect(document.activeElement).toBe(revealInput);
    });
    // A fallback info toast tells the user to press Ctrl/Cmd+C.
    await waitFor(() => {
      expect(vi.mocked(toast).info).toHaveBeenCalledWith(
        'Press Ctrl/Cmd+C to copy \u2014 clipboard blocked in this context.',
      );
    });
  });

  test("closing the reveal clears plaintext from state (cannot be re-opened)", async () => {
    const user = userEvent.setup();
    seedGetMock([]);
    mockedApi.post.mockResolvedValueOnce({
      id: 7,
      name: 'Homepage',
      token_prefix: 'spdr_aB3xQ9z7kL',
      created_at: '2026-04-18T14:23:00Z',
      last_used_at: null,
      last_used_ip: null,
      expires_at: null,
      token: 'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123',
    });
    renderSettingsOnApiTokensTab();
    await waitFor(() => {
      expect(screen.getByText(/no api tokens yet/i)).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /^create token$/i }),
    );
    await user.type(screen.getByLabelText(/^name$/i), 'Homepage');
    // Both the dialog trigger and the submit button say "Create token".
    // Scope to the dialog to hit the submit button specifically.
    {
      const dialog = screen.getByRole('dialog');
      await user.click(
        within(dialog).getByRole('button', { name: /^create token$/i }),
      );
    }
    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: /save your new token/i }),
      ).toBeInTheDocument();
    });
    // Click the confirmation footer button — the reveal dialog closes
    // and plaintext should be unreachable from the rendered tree.
    await user.click(
      screen.getByRole('button', { name: /i've saved my token/i }),
    );
    await waitFor(() => {
      expect(
        screen.queryByRole('heading', { name: /save your new token/i }),
      ).not.toBeInTheDocument();
    });
    // Re-open the create dialog. If the component cached plaintext, it
    // would leak here (either as the form's default value or as a
    // stale reveal). Neither is allowed.
    await user.click(
      screen.getByRole('button', { name: /^create token$/i }),
    );
    expect(
      screen.getByRole('heading', { name: /create api token/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByDisplayValue(
        'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123',
      ),
    ).not.toBeInTheDocument();
    const fullTokenRegex = /spdr_[a-zA-Z0-9]{26}_[a-zA-Z0-9]{6}/;
    expect(screen.queryByText(fullTokenRegex)).not.toBeInTheDocument();
  });

  test('Revoke opens AlertDialog with mono token name; confirming DELETEs /api/api-tokens/{id}', async () => {
    const user = userEvent.setup();
    const token: ApiToken = {
      id: 7,
      name: 'Homepage dashboard',
      token_prefix: 'spdr_aB3xQ9z7kL',
      created_at: '2026-04-18T14:23:00Z',
      last_used_at: null,
      last_used_ip: null,
      expires_at: null,
    };
    seedGetMock([token]);
    // DELETE /api/api-tokens/{id} returns 200 OK with JSON body
    // `{"ok":true}` per Chunk 4 (plan §Task 4.3). Mocking `undefined`
    // here would diverge from the wire contract and silently pass —
    // the `api.del<RevokeOneResponse>` call in the component would
    // then return `undefined` and any future logic that reads
    // `result.ok` would explode at runtime without the test catching
    // it. Always mock the exact shape the backend sends.
    mockedApi.del.mockResolvedValueOnce({ ok: true });
    renderSettingsOnApiTokensTab();
    await waitFor(() => {
      expect(screen.getByText('spdr_aB3xQ9z7kL')).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /revoke homepage dashboard/i }),
    );
    // The revoke AlertDialog is up. Radix AlertDialog announces with
    // role="alertdialog" — query by it to scope assertions.
    const alertDialog = await screen.findByRole('alertdialog');
    // Title quotes the token name inside a <span class="font-mono">.
    const monoName = within(alertDialog).getByText(/"Homepage dashboard"/);
    expect(monoName).toHaveClass('font-mono');
    // Body copy matches the new short wording.
    expect(
      within(alertDialog).getByText(
        /anything using this token will stop working immediately/i,
      ),
    ).toBeInTheDocument();
    await user.click(
      within(alertDialog).getByRole('button', { name: /^revoke token$/i }),
    );
    await waitFor(() => {
      expect(mockedApi.del).toHaveBeenCalledWith('api-tokens/7');
    });
  });

  test('Revoke all is only rendered when >=1 live token exists', async () => {
    seedGetMock([]);
    const { unmount } = renderSettingsOnApiTokensTab();
    await waitFor(() => {
      expect(screen.getByText(/no api tokens yet/i)).toBeInTheDocument();
    });
    // Empty list -> no Revoke all.
    expect(
      screen.queryByRole('button', { name: /revoke all/i }),
    ).not.toBeInTheDocument();
    unmount();
    // Non-empty list -> Revoke all visible.
    const token: ApiToken = {
      id: 7,
      name: 'Homepage dashboard',
      token_prefix: 'spdr_aB3xQ9z7kL',
      created_at: '2026-04-18T14:23:00Z',
      last_used_at: null,
      last_used_ip: null,
      expires_at: null,
    };
    seedGetMock([token]);
    renderSettingsOnApiTokensTab();
    await waitFor(() => {
      expect(screen.getByText('spdr_aB3xQ9z7kL')).toBeInTheDocument();
    });
    expect(
      screen.getByRole('button', { name: /revoke all/i }),
    ).toBeInTheDocument();
  });
});
