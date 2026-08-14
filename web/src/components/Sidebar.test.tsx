import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../hooks/useTrashCount', () => ({
  useTrashCount: vi.fn(),
}));

import { useAuth } from '../hooks/useAuth';
import { useTrashCount } from '../hooks/useTrashCount';
import { ThemeProvider } from './theme-provider';
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

function renderSidebar(currentPath = '/') {
  return render(
    <ThemeProvider>
      <MemoryRouter initialEntries={[currentPath]}>
        <Sidebar />
      </MemoryRouter>
    </ThemeProvider>,
  );
}

describe('Sidebar', () => {
  const mockLogout = vi.fn();

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

  test('renders all navigation links', () => {
    renderSidebar();
    expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /reports/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /transactions/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /categories/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /settings/i })).toBeInTheDocument();
  });

  test('marks the current route as active via aria-current', () => {
    renderSidebar('/transactions');
    const link = screen.getByRole('link', { name: /transactions/i });
    expect(link).toHaveAttribute('aria-current', 'page');
  });

  test('active route gets the bg-muted styling (not just aria-current)', () => {
    // aria-current is set by NavLink internally — it can be present
    // without our manual `isActive` className branch firing (e.g. if
    // a future minifier mangles the className function). Assert on
    // the actual class so we catch a regression in the styling
    // contract, not just the ARIA contract.
    renderSidebar('/transactions');
    const link = screen.getByRole('link', { name: /transactions/i });
    // Exact token, not a substring. Every row carries `hover:bg-muted` in
    // its base classes, so the original `className.toContain('bg-muted')`
    // passed with the `isActive &&` branch deleted — mutation-verified
    // 2026-08-08, the assertion this test exists for was never firing.
    expect(link.className.split(/\s+/)).toContain('bg-muted');
  });

  test('displays user display name when expanded', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(screen.getByText('Alice')).toBeInTheDocument();
  });

  test('displays user avatar initial', () => {
    renderSidebar();
    expect(screen.getByText('A')).toBeInTheDocument();
  });

  test('avatar initial takes a whole code point from an astral-plane name', () => {
    // display_name is user-editable, so a leading emoji is easy to create.
    // `name[0]` yields half a surrogate pair and renders as U+FFFD.
    mockedUseAuth.mockReturnValue({
      user: { ...mockUser, display_name: '😀mile' },
      loading: false,
      unverified: false,
      refreshUser: vi.fn(),
      login: vi.fn(),
      register: vi.fn(),
      logout: mockLogout,
    });
    renderSidebar();
    expect(screen.getByText('😀')).toBeInTheDocument();
    expect(screen.queryByText('\ud83d')).not.toBeInTheDocument();
  });

  test('calls logout when logout button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByRole('button', { name: /log\s*out/i }));
    expect(mockLogout).toHaveBeenCalledTimes(1);
  });

  test('has semantic nav element', () => {
    renderSidebar();
    expect(screen.getByRole('navigation')).toBeInTheDocument();
  });

  test('renders collapsed by default', () => {
    renderSidebar();
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
  });

  test('expands when toggle button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  test('persists expanded state to localStorage', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(localStorage.getItem('spendrop-sidebar')).toBe('true');
  });

  test('reads initial state from localStorage', () => {
    localStorage.setItem('spendrop-sidebar', 'true');
    renderSidebar();
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  test('dispatches sidebar-toggle event on toggle', async () => {
    const listener = vi.fn();
    window.addEventListener('sidebar-toggle', listener);
    try {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(listener).toHaveBeenCalled();
    } finally {
      window.removeEventListener('sidebar-toggle', listener);
    }
  });

  describe('Trash count badge', () => {
    test('hides the badge entirely when trash count is 0 (expanded)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 0,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const trashLink = screen.getByRole('link', { name: /trash/i });
      // No number rendered next to the label.
      expect(trashLink).not.toHaveTextContent(/\d+/);
    });

    test('renders the count next to the Trash label when admin and count > 0', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 7,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const trashLink = screen.getByRole('link', { name: /trash/i });
      // accessible name includes the count via the visible badge text.
      expect(trashLink).toHaveTextContent('7');
    });

    test('passes enabled=true to useTrashCount for admin users', () => {
      renderSidebar();
      expect(mockedUseTrashCount).toHaveBeenCalledWith(true);
    });

    test('passes enabled=true to useTrashCount for member users too (B5: no longer admin-gated)', () => {
      mockedUseAuth.mockReturnValue({
        user: { ...mockUser, role: 'member' },
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: mockLogout,
      });
      renderSidebar();
      expect(mockedUseTrashCount).toHaveBeenCalledWith(true);
    });

    test('renders the Trash link for non-admin users too (B5: no longer admin-gated)', () => {
      mockedUseAuth.mockReturnValue({
        user: { ...mockUser, role: 'member' },
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: mockLogout,
      });
      mockedUseTrashCount.mockReturnValue({
        count: 99,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      expect(
        screen.getByRole('link', { name: /trash/i }),
      ).toBeInTheDocument();
    });

    test('hides the badge text in the collapsed sidebar even when count > 0', () => {
      // Sidebar starts collapsed (default localStorage). In this state
      // the link is icon-only and there is no room for a number next
      // to the label, so the badge is suppressed.
      mockedUseTrashCount.mockReturnValue({
        count: 5,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      const trashLink = screen.getByRole('link', { name: /trash/i });
      expect(trashLink).not.toHaveTextContent(/5/);
    });

    test('uses an aria-label that names the count for screen readers (plural)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 7,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      expect(
        screen.getByRole('link', { name: 'Trash, 7 items' }),
      ).toBeInTheDocument();
    });

    test('uses a singular aria-label when the count is exactly 1', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 1,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      expect(
        screen.getByRole('link', { name: 'Trash, 1 item' }),
      ).toBeInTheDocument();
    });

    test('caps the visible badge text at "99+" while the aria-label keeps the real count', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 150,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      // aria-label uses the un-capped count for screen-reader fidelity.
      const link = screen.getByRole('link', { name: 'Trash, 150 items' });
      // Visible text is capped — the raw "150" is NOT in the rendered
      // text, but "99+" is.
      expect(link).toHaveTextContent('99+');
      expect(link.textContent).not.toContain('150');
    });

    test('shows the literal "99" at exactly 99 (cap kicks in at 100)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 99,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(
        screen.getByRole('link', { name: 'Trash, 99 items' }),
      ).toHaveTextContent('99');
    });

    test('shows "99+" at exactly 100', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 100,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(
        screen.getByRole('link', { name: 'Trash, 100 items' }),
      ).toHaveTextContent('99+');
    });

    test('collapsed sidebar shows a dot indicator on Trash when count > 0', () => {
      // Collapsed by default (localStorage cleared in beforeEach).
      mockedUseTrashCount.mockReturnValue({
        count: 3,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      const trashLink = screen.getByRole('link', { name: /trash/i });
      // The dot is a positioned span with data-testid for direct
      // assertion (the number is suppressed in collapsed mode).
      expect(
        trashLink.querySelector('[data-testid="trash-dot"]'),
      ).toBeInTheDocument();
    });

    test('collapsed sidebar does NOT show a dot when count is 0', () => {
      mockedUseTrashCount.mockReturnValue({
        count: 0,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      const trashLink = screen.getByRole('link', { name: /trash/i });
      expect(
        trashLink.querySelector('[data-testid="trash-dot"]'),
      ).not.toBeInTheDocument();
    });

    test('aria-label is set in collapsed mode too (so SR users still hear the count)', () => {
      mockedUseTrashCount.mockReturnValue({
        count: 4,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      // Even collapsed, the link's accessible name includes the count.
      expect(
        screen.getByRole('link', { name: 'Trash, 4 items' }),
      ).toBeInTheDocument();
    });
  });

  test('renders the Trash link with badge for a member', () => {
    mockedUseAuth.mockReturnValue({
      user: { ...mockUser, id: 2, role: 'member' as const },
      loading: false,
      unverified: false,
      refreshUser: vi.fn(),
      login: vi.fn(),
      register: vi.fn(),
      logout: mockLogout,
    });
    mockedUseTrashCount.mockReturnValue({
      count: 3,
      loading: false,
      refetch: vi.fn(),
    });
    renderSidebar();
    // Assert the count itself (not just that a Trash link exists) — the
    // aria-label is `${label}, ${badge} item${badge === 1 ? '' : 's'}`.
    expect(
      screen.getByRole('link', { name: 'Trash, 3 items' }),
    ).toBeInTheDocument();
  });

  describe('layout', () => {
    test('the fixed column is removed below md (MobileNav takes over there)', () => {
      // happy-dom does not evaluate breakpoints, so this is a structural
      // pin: unprefixed `flex` would leave a 48–240px fixed aside on top
      // of a 390px phone viewport, and `hidden` is also what keeps the
      // phone from seeing two copies of the same navigation in the
      // accessibility tree.
      renderSidebar();
      const tokens = screen
        .getByRole('complementary')
        .className.split(/\s+/)
        .filter(Boolean);
      expect(tokens).toContain('hidden');
      expect(tokens).toContain('md:flex');
      expect(tokens).not.toContain('flex');
    });

    test('renders a Separator between the nav sections', () => {
      // Separator divides the top section (nav + Trash) from the
      // bottom section (Settings + Logout). Shadcn's Separator
      // renders role="none" (decorative) by default — query by the
      // primitive's data-orientation attribute instead.
      renderSidebar();
      const separator = document.querySelector('[data-orientation="horizontal"]');
      expect(separator).not.toBeNull();
    });

    test('Reports sits between Transactions and Budgets in the nav order', async () => {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const nav = screen.getByRole('navigation');
      const links = Array.from(nav.querySelectorAll('a[href]')).map(
        (a) => (a as HTMLAnchorElement).getAttribute('href'),
      );
      const tx = links.indexOf('/transactions');
      const rep = links.indexOf('/reports');
      const bud = links.indexOf('/budgets');
      expect(tx).toBeGreaterThanOrEqual(0);
      expect(rep).toBeGreaterThan(tx);
      expect(bud).toBeGreaterThan(rep);
    });

    test('Settings sits in the bottom section AFTER the separator', async () => {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const nav = screen.getByRole('navigation');
      const separator = nav.querySelector('[data-orientation="horizontal"]');
      const settings = screen.getByRole('link', { name: /settings/i });
      // compareDocumentPosition: bit 4 = "follows" (separator precedes settings).
      const rel = separator!.compareDocumentPosition(settings);
      expect(rel & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    test('no Menu / Admin / General section titles are rendered', async () => {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      // The old grouping titles are gone — replaced by a single
      // separator. Members never saw them collapsed either.
      expect(screen.queryByText(/^Menu$/)).not.toBeInTheDocument();
      expect(screen.queryByText(/^Admin$/)).not.toBeInTheDocument();
      expect(screen.queryByText(/^General$/)).not.toBeInTheDocument();
    });
  });

  /**
   * WHAT THESE CAN AND CANNOT PROVE. Nothing below measures a pixel: happy-dom
   * runs no layout, and the Tailwind stylesheet is never loaded in tests
   * (`globals.css` is imported only by `main.tsx`, which no test renders). A
   * `coarse:`-prefixed token is in the DOM on a mouse and on a finger alike, so
   * these pin the wiring and the 44px belongs to the browser check — at 1130px
   * with a coarse pointer, which is the household tablet in landscape and the
   * one surface that renders THIS column with a thumb driving it.
   *
   * The wiring is worth more here than it is on a Button. Every `<Button>` in
   * the app inherits `coarse:min-h-11` from the primitive, which makes "this
   * control has a floor" a claim that passes even where nobody floored
   * anything. A sidebar row is a raw `NavLink` anchor — no primitive touches it
   * — so the token is present only if this call site put it there, and deleting
   * that call site is what the mutation run confirms these catch.
   */
  describe('touch floor on a coarse pointer', () => {
    /** The nav rows, in DOM order. */
    function navRows(): HTMLElement[] {
      return within(screen.getByRole('navigation')).getAllByRole('link');
    }

    /**
     * `classList.contains` rather than a substring test on `className`: the
     * substring form matches `md:coarse:min-h-11` (the width-gated variant this
     * whole idiom exists to avoid) and would report it as a pass.
     *
     * Returns the hrefs that are MISSING the token, so a failure names the rows
     * rather than just the first one.
     */
    function rowsMissing(token: string): (string | null)[] {
      return navRows()
        .filter((row) => !row.classList.contains(token))
        .map((row) => row.getAttribute('href'));
    }

    test('every nav row carries the height floor when collapsed', () => {
      renderSidebar();
      // Length assertion first: without it a query that silently matched two
      // rows would satisfy "none are missing the token" just as well.
      expect(navRows()).toHaveLength(9);
      expect(rowsMissing('coarse:min-h-11')).toEqual([]);
    });

    test('every nav row carries the height floor when expanded', async () => {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      expect(navRows()).toHaveLength(9);
      expect(rowsMissing('coarse:min-h-11')).toEqual([]);
    });

    test('the collapsed rows floor the width too, and the expanded rows do not need to', async () => {
      // Collapsed the row is a 32px square, and a height-only floor would leave
      // a 44x32 target — taller, still a miss for a thumb. Expanded it is a
      // full-width row, where a width floor is inert; asserting `w-full` there
      // is what makes "no width token" a deliberate answer rather than an
      // omission.
      const user = userEvent.setup();
      renderSidebar();
      expect(rowsMissing('coarse:min-w-11')).toEqual([]);

      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(rowsMissing('w-full')).toEqual([]);
    });

    test('a mouse keeps the existing density at every width', async () => {
      // The floor is additive. Nothing here may become unconditional, and
      // nothing may hang off a width gate: `md:`-prefixed is the exact bug —
      // the tablet takes the desktop side of it and stays at 32px.
      const user = userEvent.setup();
      renderSidebar();
      for (const row of navRows()) {
        expect(row).toHaveClass('!size-8');
        expect(row).not.toHaveClass('min-h-11');
        expect(row).not.toHaveClass('min-w-11');
        expect(
          Array.from(row.classList).filter((t) => t.startsWith('md:')),
        ).toEqual([]);
      }

      await user.click(screen.getByLabelText('Toggle sidebar'));
      for (const row of navRows()) {
        expect(row).toHaveClass('px-3', 'py-2');
        expect(row).not.toHaveClass('min-h-11');
        expect(
          Array.from(row.classList).filter((t) => t.startsWith('md:')),
        ).toEqual([]);
      }
    });

    test('positive control: the probe discriminates, and is not true of the whole tree', () => {
      renderSidebar();
      const row = screen.getByRole('link', { name: /dashboard/i });

      // Half one — the probe is exact-token. A near-miss and the width-gated
      // variant both report absent on the very element that passes above, so
      // the passes are not a substring matching anything floor-shaped.
      expect(row.classList.contains('coarse:min-h-11')).toBe(true);
      expect(row.classList.contains('coarse:min-h-1')).toBe(false);
      expect(row.classList.contains('md:coarse:min-h-11')).toBe(false);

      // Half two — the token is not simply everywhere. The avatar is the
      // shell's one non-interactive `size-8` box, so it is what "floored" would
      // look like if an ungated floor had been sprayed across the column: this
      // is the assertion that fails if the floor stops being a decision.
      const avatar = screen.getByText('A');
      expect(avatar.closest('span')?.classList.contains('coarse:min-h-11')).toBe(
        false,
      );
    });

    test('the collapse toggle is floored on both axes and keeps its 32px mouse box', () => {
      // This one comes from `Button` (base = height, `size="icon"` = width), so
      // the positive half is weak on its own — it is true of every Button in
      // the app. The load-bearing halves are the negatives: `size-8` survived
      // (so the floor really is `min-*` in a separate tailwind-merge group and
      // did not replace the density), and no width gate crept back in.
      renderSidebar();
      const toggle = screen.getByLabelText('Toggle sidebar');
      expect(toggle).toHaveClass('coarse:min-h-11', 'coarse:min-w-11', 'size-8');
      expect(toggle).not.toHaveClass('h-10', 'w-10');
      expect(
        Array.from(toggle.classList).filter((t) => t.startsWith('md:')),
      ).toEqual([]);
    });

    test('Log out is floored in both states', async () => {
      const user = userEvent.setup();
      renderSidebar();
      const collapsed = screen.getByRole('button', { name: /log\s*out/i });
      // Collapsed it is square by className, not by `size="icon"` — the
      // primitive cannot see that shape, so the width half is the call site's
      // own `TOUCH_TARGET_SQUARE`.
      expect(collapsed).toHaveClass('coarse:min-h-11', 'coarse:min-w-11');

      await user.click(screen.getByLabelText('Toggle sidebar'));
      const expanded = screen.getByRole('button', { name: /log\s*out/i });
      expect(expanded).toHaveClass('coarse:min-h-11');
    });

    test('the collapsed rail scrolls past its rows instead of clipping them', async () => {
      // The floor's own side effect. Nine 44px rows plus the footer can make
      // the collapsed column taller than a landscape tablet's viewport, and the
      // nav used to be `overflow-hidden` on both axes — which would put
      // Settings and Log out below the cut with nothing to scroll. X still
      // clips, because the aside animates its width and a row is briefly wider
      // than the rail. Naming both axes is load-bearing: `overflow-y-auto`
      // alone leaves X `visible`, which CSS then promotes to `auto`.
      const user = userEvent.setup();
      renderSidebar();
      const nav = screen.getByRole('navigation');
      expect(nav).toHaveClass('overflow-x-hidden', 'overflow-y-auto');
      expect(nav).not.toHaveClass('overflow-hidden');

      // Expanded is a 240px column that has always scrolled; unchanged here,
      // asserted so a future edit cannot collapse the two branches into one
      // without noticing they are different answers.
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(screen.getByRole('navigation')).toHaveClass('overflow-auto');
    });

    test('every interactive control in the column clears the height floor', async () => {
      // The invariant as one sweep, so a control added to the shell later
      // cannot land under the floor just because no test named it. Covers both
      // states because the expanded column mounts one control the collapsed one
      // does not (the colour-theme Select).
      function unflooredControls(): string[] {
        const aside = screen.getByRole('complementary');
        return Array.from(aside.querySelectorAll('a, button'))
          .filter((el) => !el.classList.contains('coarse:min-h-11'))
          .map(
            (el) =>
              el.getAttribute('aria-label') ??
              el.textContent?.trim() ??
              el.tagName,
          );
      }

      const user = userEvent.setup();
      renderSidebar();
      // 9 rows + toggle + Log out + the theme mode toggle.
      expect(
        screen.getByRole('complementary').querySelectorAll('a, button'),
      ).toHaveLength(12);
      expect(unflooredControls()).toEqual([]);

      await user.click(screen.getByLabelText('Toggle sidebar'));
      // ...plus the colour-theme Select trigger, which only renders expanded.
      expect(
        screen.getByRole('complementary').querySelectorAll('a, button'),
      ).toHaveLength(13);
      expect(unflooredControls()).toEqual([]);
    });
  });
});
