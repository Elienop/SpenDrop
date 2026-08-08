import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { useEffect } from 'react';
import { render, screen, within, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useNavigate } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../hooks/useTrashCount', () => ({
  useTrashCount: vi.fn(),
}));

import { useAuth } from '../hooks/useAuth';
import { useTrashCount } from '../hooks/useTrashCount';
import { ThemeProvider } from './theme-provider';
import { MobileNav } from './MobileNav';
import { Sidebar } from './Sidebar';

const mockedUseAuth = vi.mocked(useAuth);
const mockedUseTrashCount = vi.mocked(useTrashCount);

const mockUser = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin' as const,
  created_at: '2024-01-01',
};

/**
 * Lets a test change the route WITHOUT touching the drawer — the shape of
 * Android's back button popping history while the Sheet is still mounted.
 */
const routeControl: { go: (to: string) => void } = {
  go: () => {
    throw new Error('NavProbe not mounted');
  },
};

function NavProbe() {
  const navigate = useNavigate();
  useEffect(() => {
    routeControl.go = (to) => navigate(to);
  }, [navigate]);
  return null;
}

function renderMobileNav(currentPath = '/') {
  return render(
    <ThemeProvider>
      <MemoryRouter initialEntries={[currentPath]}>
        <MobileNav />
        <NavProbe />
      </MemoryRouter>
    </ThemeProvider>,
  );
}

/**
 * Installs a controllable `(min-width: 768px)` media query and returns a
 * `cross(matches)` that fires its change listeners. happy-dom ships no
 * matchMedia, so this is also what makes the component's feature-detect
 * take its live branch.
 */
function stubDesktopQuery() {
  const listeners: ((e: MediaQueryListEvent) => void)[] = [];
  const mql = {
    matches: false,
    media: '(min-width: 768px)',
    addEventListener: (_: string, l: (e: MediaQueryListEvent) => void) => {
      listeners.push(l);
    },
    removeEventListener: (_: string, l: (e: MediaQueryListEvent) => void) => {
      const i = listeners.indexOf(l);
      if (i >= 0) listeners.splice(i, 1);
    },
  };
  window.matchMedia = vi.fn(() => mql as unknown as MediaQueryList);
  return {
    listenerCount: () => listeners.length,
    cross: async (matches: boolean) => {
      mql.matches = matches;
      await act(async () => {
        for (const l of [...listeners]) {
          l({ matches } as MediaQueryListEvent);
        }
      });
    },
  };
}

/** Tailwind spacing token to px — 0.25rem steps against a 16px root. */
function spacingPx(token: string): number {
  const steps = Number(token.replace(/^(?:min-h|size|h|w)-/, ''));
  if (!Number.isFinite(steps)) {
    throw new Error(`unparseable spacing token: ${token}`);
  }
  return steps * 4;
}

/**
 * The vertical touch floor of a control, in px, derived from its own class
 * tokens. happy-dom runs no layout, so the pixels have to come from the
 * classes — but deriving them beats asserting a literal token: shrink the row
 * and this fails with the size it computed, rather than passing because some
 * other token still matched.
 *
 * `min-h-*` is authoritative when present. CSS clamps a fixed `height` up to
 * `min-height`, which is why shadcn Button's own `h-10` can sit alongside
 * `min-h-11` and the row still renders 44px.
 */
function touchFloorPx(el: HTMLElement): number {
  const tokens = Array.from(el.classList);
  const minH = tokens.find((c) => /^min-h-[\d.]+$/.test(c));
  if (minH) return spacingPx(minH);
  const fixed = tokens.find((c) => /^(?:size|h)-[\d.]+$/.test(c));
  if (!fixed) {
    throw new Error(`no height token on: ${el.getAttribute('class')}`);
  }
  return spacingPx(fixed);
}

/** Class tokens of an element, for breakpoint-prefix assertions. */
function classes(el: Element): string[] {
  return el.className.split(/\s+/).filter(Boolean);
}

/** Opens the drawer and returns its dialog element. */
async function openDrawer(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'Open navigation menu' }));
  return screen.getByRole('dialog');
}

describe('MobileNav', () => {
  const mockLogout = vi.fn();
  const originalMatchMedia = window.matchMedia;

  afterEach(() => {
    window.matchMedia = originalMatchMedia;
  });

  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: mockUser,
      loading: false,
      unverified: false,
      refreshUser: vi.fn(),
      login: vi.fn(),
      register: vi.fn(),
      logout: mockLogout,
    });
    mockedUseTrashCount.mockReturnValue({
      count: 0,
      loading: false,
      refetch: vi.fn(),
    });
  });

  test('the top bar exposes one labelled control and no drawer until it is used', () => {
    renderMobileNav();
    expect(
      screen.getByRole('button', { name: 'Open navigation menu' }),
    ).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    // The bar is a launcher, not a second nav: the wordmark home link is the
    // ONLY link on it, and no destination is reachable without opening.
    expect(screen.getAllByRole('link')).toHaveLength(1);
    expect(
      screen.queryByRole('link', { name: /transactions/i }),
    ).not.toBeInTheDocument();
  });

  // Near-universal expectation on a phone: tapping the wordmark goes home.
  test('the wordmark is a named link to the dashboard', () => {
    renderMobileNav('/reports');
    const home = screen.getByRole('link', { name: 'SpenDrop dashboard' });
    expect(home).toHaveAttribute('href', '/');
    // Keyboard-reachable: a home link excluded from the tab order fails
    // WCAG 2.1.1, so the accessible name is the fix, not tabIndex={-1}.
    expect(home).not.toHaveAttribute('tabindex');
  });

  // Invariant 3: every destination the desktop sidebar offers, each with a
  // VISIBLE text label. The sidebar's collapsed mode leans on Tooltips and
  // sr-only spans; neither conveys anything on a touch device.
  test.each([
    'Quick add',
    'Dashboard',
    'Transactions',
    'Reports',
    'Budgets',
    'Savings',
    'Categories',
    'Trash',
    'Settings',
    'Log out',
  ])('drawer shows "%s" as a visible label', async (label) => {
    const user = userEvent.setup();
    renderMobileNav();
    const dialog = await openDrawer(user);
    const text = within(dialog).getByText(label);
    expect(text).toBeInTheDocument();
    // The label must not be hidden from sight — an sr-only span would
    // satisfy getByText while showing the user an icon-only row.
    expect(text.className).not.toContain('sr-only');
    expect(text.closest('.sr-only')).toBeNull();
  });

  test('shows the signed-in identity in the drawer', async () => {
    const user = userEvent.setup();
    renderMobileNav();
    const dialog = await openDrawer(user);
    expect(within(dialog).getByText('Alice')).toBeInTheDocument();
    expect(within(dialog).getByText('@alice')).toBeInTheDocument();
  });

  // Invariant 5: the drawer consumes the same nav-items arrays as the
  // sidebar. A hand-maintained second list would drift here first.
  test('offers exactly the desktop sidebar destinations, in the same order', async () => {
    const sidebar = render(
      <ThemeProvider>
        <MemoryRouter initialEntries={['/']}>
          <Sidebar />
        </MemoryRouter>
      </ThemeProvider>,
    );
    const sidebarHrefs = Array.from(
      sidebar.container.querySelectorAll('a[href]'),
    ).map((a) => a.getAttribute('href'));
    sidebar.unmount();

    const user = userEvent.setup();
    renderMobileNav();
    const dialog = await openDrawer(user);
    const drawerHrefs = within(dialog)
      .getAllByRole('link')
      .map((a) => a.getAttribute('href'));

    expect(sidebarHrefs.length).toBeGreaterThan(0);
    expect(drawerHrefs).toEqual(sidebarHrefs);
  });

  // Invariant 4.
  test('closes when a destination is tapped', async () => {
    const user = userEvent.setup();
    renderMobileNav();
    const dialog = await openDrawer(user);
    await user.click(within(dialog).getByRole('link', { name: 'Budgets' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  // The one close the route-derived open state cannot cover: the pathname
  // does not change, so only the row's own onNavigate fires.
  test('closes when the ALREADY-ACTIVE destination is tapped', async () => {
    const user = userEvent.setup();
    renderMobileNav('/budgets');
    const dialog = await openDrawer(user);
    await user.click(within(dialog).getByRole('link', { name: 'Budgets' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  // Android's back button pops history with the Sheet still mounted. Nothing
  // inside the drawer is touched here.
  test('closes when the route changes underneath it', async () => {
    const user = userEvent.setup();
    renderMobileNav('/budgets');
    await openDrawer(user);
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    await act(async () => {
      routeControl.go('/reports');
    });

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  describe('viewport crossing md', () => {
    test('closes when the viewport grows into desktop width', async () => {
      const media = stubDesktopQuery();
      const user = userEvent.setup();
      renderMobileNav();
      expect(media.listenerCount()).toBe(1);
      await openDrawer(user);

      await media.cross(true);

      // Rotating to landscape reveals the desktop sidebar; the drawer must
      // not be left stranded on top of it.
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    test('a change back to phone width does NOT close it', async () => {
      const media = stubDesktopQuery();
      const user = userEvent.setup();
      renderMobileNav();
      await openDrawer(user);

      await media.cross(false);

      // The query fires in both directions; only the desktop crossing may
      // close, or it would fight the tap that just opened the drawer.
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    test('drops its media listener on unmount', () => {
      const media = stubDesktopQuery();
      const { unmount } = renderMobileNav();
      expect(media.listenerCount()).toBe(1);
      unmount();
      expect(media.listenerCount()).toBe(0);
    });
  });

  // 44px floor on EVERY control of a surface that is only ever touched. The
  // appearance controls default to desktop sizes (32px select trigger, 40px
  // icon button); the nav rows, Log out and the home link carry their own
  // min-height because their content alone would leave them at 36px.
  describe('touch targets', () => {
    test('the appearance controls are raised to 44px in the drawer', async () => {
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);

      const themeSelect = within(dialog).getByRole('combobox');
      expect(touchFloorPx(themeSelect)).toBeGreaterThanOrEqual(44);
      // Precedence, not just presence: the 32px desktop default must be gone,
      // or tailwind-merge kept the wrong one of the two.
      expect(classes(themeSelect)).not.toContain('h-8');

      const modeToggle = within(dialog).getByRole('button', {
        name: 'Toggle theme',
      });
      expect(touchFloorPx(modeToggle)).toBeGreaterThanOrEqual(44);
    });

    // The nine destinations are the most-tapped controls on the phone.
    test.each([
      'Quick add',
      'Dashboard',
      'Transactions',
      'Reports',
      'Budgets',
      'Savings',
      'Categories',
      'Trash',
      'Settings',
    ])('the "%s" row is a 44px target', async (label) => {
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      const row = within(dialog).getByRole('link', { name: label });
      expect(touchFloorPx(row)).toBeGreaterThanOrEqual(44);
    });

    test('the Log out row is a 44px target', async () => {
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      const logout = within(dialog).getByRole('button', { name: /log\s*out/i });
      expect(touchFloorPx(logout)).toBeGreaterThanOrEqual(44);
    });

    test('the wordmark home link is a 44px target', () => {
      renderMobileNav();
      const home = screen.getByRole('link', { name: 'SpenDrop dashboard' });
      expect(touchFloorPx(home)).toBeGreaterThanOrEqual(44);
    });

    test('the desktop sidebar keeps its compact appearance controls', async () => {
      // Same two components, unchanged there — the className is opt-in.
      const user = userEvent.setup();
      render(
        <ThemeProvider>
          <MemoryRouter initialEntries={['/']}>
            <Sidebar />
          </MemoryRouter>
        </ThemeProvider>,
      );
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const tokens = screen.getByRole('combobox').className.split(/\s+/);
      expect(tokens).toContain('h-8');
      expect(tokens).not.toContain('h-11');
    });

    test('the drawer trigger is a 44px target', () => {
      renderMobileNav();
      const trigger = screen.getByRole('button', {
        name: 'Open navigation menu',
      });
      expect(touchFloorPx(trigger)).toBeGreaterThanOrEqual(44);
    });
  });

  // happy-dom evaluates no breakpoints, so this is structural: the top bar and
  // the desktop aside must be display:none on opposite sides of md. Without
  // `md:hidden` the 56px sticky z-40 phone bar renders on every desktop page
  // beside the sidebar.
  describe('breakpoint swap', () => {
    test('the top bar is removed at md and up', () => {
      renderMobileNav();
      const tokens = classes(screen.getByRole('banner'));
      expect(tokens).toContain('md:hidden');
      // Unprefixed `hidden` would hide the bar at EVERY width, leaving the
      // phone with no way into navigation at all.
      expect(tokens).not.toContain('hidden');
    });

    test('exactly one navigation surface is visible at any width', () => {
      const sidebar = render(
        <ThemeProvider>
          <MemoryRouter initialEntries={['/']}>
            <Sidebar />
          </MemoryRouter>
        </ThemeProvider>,
      );
      const aside = classes(screen.getByRole('complementary'));
      sidebar.unmount();

      renderMobileNav();
      const bar = classes(screen.getByRole('banner'));

      // Below md: the aside is display:none and the bar is not.
      expect(aside).toContain('hidden');
      expect(bar).not.toContain('hidden');
      // From md up: exactly the reverse.
      expect(aside).toContain('md:flex');
      expect(bar).toContain('md:hidden');
    });
  });

  // display_name is user-editable, so a leading astral character is easy to
  // create. `name[0]` returns half a surrogate pair and renders as U+FFFD.
  describe('avatar initial', () => {
    test('takes a whole code point from an astral-plane name', async () => {
      mockedUseAuth.mockReturnValue({
        user: { ...mockUser, display_name: '😀mile' },
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: mockLogout,
      });
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      expect(within(dialog).getByText('😀')).toBeInTheDocument();
      // The lone high surrogate that `'😀mile'[0]` would yield.
      expect(within(dialog).queryByText('\ud83d')).not.toBeInTheDocument();
    });

    test('falls back to ? when there is no name', async () => {
      mockedUseAuth.mockReturnValue({
        user: { ...mockUser, display_name: '' },
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: mockLogout,
      });
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      expect(within(dialog).getByText('?')).toBeInTheDocument();
    });
  });

  // Invariant 7: plain-string className, not NavLink's function form.
  test('marks the current route active with bg-muted, not just aria-current', async () => {
    const user = userEvent.setup();
    renderMobileNav('/transactions');
    const dialog = await openDrawer(user);
    const link = within(dialog).getByRole('link', { name: 'Transactions' });
    expect(link).toHaveAttribute('aria-current', 'page');
    // Exact token, not a substring: every row carries `hover:bg-muted` in its
    // base classes, so `className.toContain('bg-muted')` passes with the
    // active branch deleted (verified — that mutation survived it).
    expect(link.className.split(/\s+/)).toContain('bg-muted');
  });

  test('logs out from the drawer', async () => {
    const user = userEvent.setup();
    renderMobileNav();
    const dialog = await openDrawer(user);
    await user.click(within(dialog).getByRole('button', { name: /log\s*out/i }));
    expect(mockLogout).toHaveBeenCalledTimes(1);
  });

  describe('Trash count badge', () => {
    test('shows no number when the count is 0', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 0,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      expect(within(dialog).getByRole('link', { name: 'Trash' })).not.toHaveTextContent(
        /\d/,
      );
    });

    test('names the real count for screen readers and shows it (plural)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 7,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      expect(
        within(dialog).getByRole('link', { name: 'Trash, 7 items' }),
      ).toHaveTextContent('7');
    });

    test('uses a singular aria-label at exactly 1', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 1,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      expect(
        within(dialog).getByRole('link', { name: 'Trash, 1 item' }),
      ).toBeInTheDocument();
    });

    // nav-items.ts owns the pill class so the two surfaces cannot drift.
    test('draws the pill with the same classes as the desktop sidebar', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 7,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();

      const sidebar = render(
        <ThemeProvider>
          <MemoryRouter initialEntries={['/']}>
            <Sidebar />
          </MemoryRouter>
        </ThemeProvider>,
      );
      await user.click(screen.getByLabelText('Toggle sidebar'));
      const sidebarPill = within(
        screen.getByRole('link', { name: 'Trash, 7 items' }),
      ).getByText('7');
      const sidebarClass = sidebarPill.className;
      sidebar.unmount();

      renderMobileNav();
      const dialog = await openDrawer(user);
      const drawerPill = within(
        within(dialog).getByRole('link', { name: 'Trash, 7 items' }),
      ).getByText('7');

      expect(sidebarClass).not.toBe('');
      expect(drawerPill.className).toBe(sidebarClass);
    });

    test('caps the visible pill at "99+" while the aria-label keeps the true count', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 150,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      const link = within(dialog).getByRole('link', { name: 'Trash, 150 items' });
      expect(link).toHaveTextContent('99+');
      expect(link.textContent).not.toContain('150');
    });
  });

  // Invariant 2: the persisted desktop-sidebar state must not reach the
  // phone. MobileNav neither reads nor writes it.
  describe('persisted sidebar state', () => {
    test('renders identically when spendrop-sidebar is "true"', async () => {
      localStorage.setItem('spendrop-sidebar', 'true');
      const user = userEvent.setup();
      renderMobileNav();
      const dialog = await openDrawer(user);
      // Full labels, and no trace of the desktop collapse control.
      expect(within(dialog).getByText('Transactions').className).not.toContain(
        'sr-only',
      );
      expect(
        screen.queryByRole('button', { name: 'Toggle sidebar' }),
      ).not.toBeInTheDocument();
    });

    test('opening and closing the drawer writes nothing to spendrop-sidebar', async () => {
      const listener = vi.fn();
      window.addEventListener('sidebar-toggle', listener);
      try {
        const user = userEvent.setup();
        renderMobileNav();
        const dialog = await openDrawer(user);
        await user.click(within(dialog).getByRole('link', { name: 'Budgets' }));
        expect(localStorage.getItem('spendrop-sidebar')).toBeNull();
        expect(listener).not.toHaveBeenCalled();
      } finally {
        window.removeEventListener('sidebar-toggle', listener);
      }
    });
  });
});
