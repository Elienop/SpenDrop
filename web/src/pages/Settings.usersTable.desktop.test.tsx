import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../hooks/useAuth', () => ({ useAuth: vi.fn() }));

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
import { api } from '../api/client';
import { Settings } from './Settings';
import type { User } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

/**
 * The household users table at TABLET-LANDSCAPE desktop widths.
 *
 * This is the desktop tree, not the phone one: 1130px is well above `md`, so
 * the table renders — but the shell spends 240px of it on the expanded sidebar
 * and 80px on the page gutter, leaving the card about 745px. A row of three
 * `whitespace-nowrap` buttons contributes the SUM of their widths to the
 * cell's min-content, and Actions is the last column, so the whole thing
 * landed past the right edge of the table's scroll box: the buttons that edit,
 * reset and delete an account were reachable only by panning.
 *
 * WHAT THIS FILE CAN AND CANNOT PROVE. happy-dom has no layout engine — it
 * reports zero for every width — so the 745px arithmetic above is browser
 * work, and it is not asserted here. What is asserted is the MECHANISM: the
 * actions row is allowed to wrap, which is what drops its min-content
 * contribution from the sum of three buttons to the widest one, and all three
 * buttons are still in the row (a "fix" that dropped one would also fit).
 */

interface HappyDomWindow {
  happyDOM?: { setViewport: (v: { width?: number; height?: number }) => void };
}

/** Galaxy Tab S10 FE landscape, the width the pan was measured at. */
const TABLET_LANDSCAPE = 1130;
const PHONE_WIDTH = 360;

function setViewportWidth(width: number): void {
  const controllable = window as unknown as HappyDomWindow;
  if (!controllable.happyDOM) {
    throw new Error(
      'happy-dom viewport control is unavailable — viewport tests cannot run',
    );
  }
  controllable.happyDOM.setViewport({ width });
}

const alice: User = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin',
  created_at: '2024-01-01',
};

const bob: User = {
  id: 2,
  username: 'bob',
  display_name: 'Bob',
  role: 'member',
  created_at: '2024-01-01',
};

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

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockedUseAuth.mockReturnValue({
    user: alice,
    loading: false,
    unverified: false,
    refreshUser: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
  mockedApi.get.mockImplementation((path: string) => {
    if (path === 'users') return Promise.resolve([alice, bob]);
    if (path === 'api-tokens') return Promise.resolve({ tokens: [] });
    return Promise.resolve([]);
  });
});

afterEach(() => {
  setViewportWidth(1024);
});

/** The row-actions container for one user, found via a button inside it. */
function actionsRowFor(username: string): HTMLElement {
  const button = screen.getByRole('button', {
    name: `Delete ${username}`,
  });
  const row = button.parentElement;
  if (row === null) throw new Error('actions row not found');
  return row;
}

describe('the users table at 1130px with the sidebar expanded', () => {
  test('renders the DESKTOP tree, not the phone card list', async () => {
    setViewportWidth(TABLET_LANDSCAPE);
    renderSettings();
    // The positive control for everything below: 1130px is above `md`, so the
    // table is what mounts. Without this the class assertions would be about
    // an element that never rendered.
    const table = await screen.findByRole('table');
    expect(
      screen.queryByRole('list', { name: 'Household users' }),
    ).not.toBeInTheDocument();
    expect(within(table).getAllByRole('row')).toHaveLength(3);
  });

  test('the row actions are allowed to wrap instead of running off the edge', async () => {
    setViewportWidth(TABLET_LANDSCAPE);
    renderSettings();
    await screen.findByRole('table');

    const actions = actionsRowFor('bob');
    // Exact token membership, not a substring: `toContain('flex-wrap')` on the
    // class string would also pass on `md:flex-wrap`, which is a different
    // rule that leaves the tablet unchanged.
    const tokens = Array.from(actions.classList);
    expect(tokens).toContain('flex-wrap');
    expect(tokens).toContain('justify-end');
    expect(tokens).not.toContain('flex-nowrap');

    // Wrapping is only a fix if nothing was removed to make room. All three
    // per-row actions are still here, in this row, on a member's row.
    const buttons = within(actions)
      .getAllByRole('button')
      .map((b) => b.getAttribute('aria-label'));
    expect(buttons).toEqual([
      'Edit display name for bob',
      'Reset password for bob',
      'Delete bob',
    ]);
  });

  test("the admin's own row keeps its two actions wrappable too", async () => {
    setViewportWidth(TABLET_LANDSCAPE);
    renderSettings();
    await screen.findByRole('table');

    // Reset is deliberately absent on your own row — you rotate your own
    // password from the account card, which checks the current one.
    const actions = actionsRowFor('alice');
    expect(Array.from(actions.classList)).toContain('flex-wrap');
    const buttons = within(actions)
      .getAllByRole('button')
      .map((b) => b.getAttribute('aria-label'));
    expect(buttons).toEqual(['Edit display name for alice', 'Delete alice']);
  });

  test('below md this table is not the surface at all', async () => {
    // The absence half of the first control, from the other side: the fix
    // above is a desktop-tree fix, and if the phone ever started rendering
    // this table again that is a different bug wearing this one's clothes.
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByRole('list', { name: 'Household users' });
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });
});
