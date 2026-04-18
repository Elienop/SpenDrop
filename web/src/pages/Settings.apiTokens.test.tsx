import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

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

function renderSettingsOnApiTokensTab() {
  return render(
    <MemoryRouter initialEntries={['/settings?tab=api-tokens']}>
      <Settings />
    </MemoryRouter>,
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
      login: vi.fn(),
      logout: vi.fn(),
      refresh: vi.fn(),
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

  test('clicking + New token opens the create dialog', async () => {
    const user = userEvent.setup();
    seedGetMock([]);
    renderSettingsOnApiTokensTab();
    // Wait for the empty state so we know the mount fetch resolved.
    await waitFor(() => {
      expect(screen.getByText(/no api tokens yet/i)).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole('button', { name: /\+ new token/i }),
    );
    // The dialog body has a header with "Create API token" and the three
    // form fields. Assert on the header + one field to prove the dialog
    // is the create form (not some other modal).
    expect(
      screen.getByRole('heading', { name: /create api token/i }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^name$/i)).toBeInTheDocument();
    expect(
      screen.getByLabelText(/confirm password/i),
    ).toBeInTheDocument();
  });

  test('submitting create dialog with valid password shows show-once reveal', async () => {
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
    await user.click(screen.getByRole('button', { name: /\+ new token/i }));
    await user.type(screen.getByLabelText(/^name$/i), 'Homepage');
    await user.type(
      screen.getByLabelText(/confirm password/i),
      'hunter2',
    );
    await user.click(
      screen.getByRole('button', { name: /^create$/i }),
    );
    // The dialog content is swapped in-place — look for the banner copy
    // from §7.5 and the full plaintext in a read-only input.
    await waitFor(() => {
      expect(
        screen.getByText(/only time you'll see this token/i),
      ).toBeInTheDocument();
    });
    const revealInput = screen.getByLabelText(
      /your new api token/i,
    ) as HTMLInputElement;
    expect(revealInput.value).toBe(
      'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123',
    );
    expect(revealInput.readOnly).toBe(true);
    // The POST body must carry exactly what the user typed — no silent
    // defaulting of `expires_at` away from `null`.
    expect(mockedApi.post).toHaveBeenCalledWith('api-tokens', {
      name: 'Homepage',
      expires_at: null,
      password: 'hunter2',
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
    await user.click(screen.getByRole('button', { name: /\+ new token/i }));
    await user.type(screen.getByLabelText(/^name$/i), 'Homepage');
    await user.type(
      screen.getByLabelText(/confirm password/i),
      'hunter2',
    );
    await user.click(screen.getByRole('button', { name: /^create$/i }));
    await waitFor(() => {
      expect(
        screen.getByText(/only time you'll see this token/i),
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
      expect(vi.mocked(toast).success).toHaveBeenCalledWith('Copied');
    });
  });

  test('closing the reveal clears plaintext from state (cannot be re-opened)', async () => {
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
    await user.click(screen.getByRole('button', { name: /\+ new token/i }));
    await user.type(screen.getByLabelText(/^name$/i), 'Homepage');
    await user.type(screen.getByLabelText(/confirm password/i), 'hunter2');
    await user.click(screen.getByRole('button', { name: /^create$/i }));
    await waitFor(() => {
      expect(
        screen.getByText(/only time you'll see this token/i),
      ).toBeInTheDocument();
    });
    // Click Done — the reveal dialog closes and plaintext should be
    // unreachable from the rendered tree.
    await user.click(screen.getByRole('button', { name: /^done$/i }));
    await waitFor(() => {
      expect(
        screen.queryByText(/only time you'll see this token/i),
      ).not.toBeInTheDocument();
    });
    // Re-open the create dialog. If the component cached plaintext, it
    // would leak here (either as the form's default value or as a
    // stale reveal). Neither is allowed.
    await user.click(screen.getByRole('button', { name: /\+ new token/i }));
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
});
