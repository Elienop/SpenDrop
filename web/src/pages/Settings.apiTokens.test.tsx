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
});
