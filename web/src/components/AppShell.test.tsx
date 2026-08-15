import { describe, test, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// The shell's routes pull in every page (and their data layers). This suite
// is about the layout contract of <main>, so the pages, the two navigation
// surfaces and the toaster are stubbed out.
vi.mock('../pages/Dashboard', () => ({
  // The testid is the routing suite's positive control: "not-found panel
  // absent on /" only proves anything if / demonstrably rendered its page.
  Dashboard: () => <div data-testid="page-dashboard" />,
}));
vi.mock('../pages/Transactions', () => ({ Transactions: () => <div /> }));
vi.mock('../pages/Budgets', () => ({ Budgets: () => <div /> }));
vi.mock('../pages/Savings', () => ({ Savings: () => <div /> }));
vi.mock('../pages/Categories', () => ({ Categories: () => <div /> }));
vi.mock('../pages/Reports', () => ({ Reports: () => <div /> }));
vi.mock('../pages/Settings', () => ({ Settings: () => <div /> }));
vi.mock('../pages/Trash', () => ({ Trash: () => <div /> }));
vi.mock('@/components/ui/sonner', () => ({ Toaster: () => <div /> }));
vi.mock('./Sidebar', () => ({
  Sidebar: () => <div data-testid="desktop-sidebar" />,
}));
vi.mock('./MobileNav', () => ({
  MobileNav: () => <div data-testid="mobile-nav" />,
}));

// The routing suite renders through ProtectedRoute, which consumes useAuth.
// Mocked so the tests control the answer; an anonymous render would bounce to
// /login before AppShell's Routes ever saw the path, and a "not-found panel
// absent" assertion would then pass with the fallback route deleted.
vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
  AuthProvider: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

import React from 'react';
import { AppShell } from './AppShell';
import { ProtectedRoute } from './ProtectedRoute';
import { useAuth } from '../hooks/useAuth';

const mockedUseAuth = vi.mocked(useAuth);

function renderShell() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <AppShell />
    </MemoryRouter>,
  );
}

/** Tailwind class tokens on an element, for breakpoint-prefix assertions. */
function classes(el: Element): string[] {
  return el.className.split(/\s+/).filter(Boolean);
}

describe('AppShell layout', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  // Wiring seam: without this mount a phone has no navigation at all, and
  // nothing else in the suite would notice.
  test('mounts both navigation surfaces', () => {
    renderShell();
    expect(screen.getByTestId('desktop-sidebar')).toBeInTheDocument();
    expect(screen.getByTestId('mobile-nav')).toBeInTheDocument();
  });

  // Invariants 1 + 2. happy-dom does not evaluate breakpoints, so the pin is
  // structural: every class that reserves room for the fixed sidebar must
  // carry the `md:` prefix, whatever the persisted state says.
  test('reserves no sidebar width below md when the sidebar is persisted collapsed', () => {
    renderShell();
    const main = classes(screen.getByRole('main'));
    expect(main).toContain('md:pl-12');
    expect(main.filter((c) => /^pl-/.test(c))).toEqual([]);
  });

  test('reserves no sidebar width below md when the sidebar is persisted EXPANDED', () => {
    localStorage.setItem('spendrop-sidebar', 'true');
    renderShell();
    const main = classes(screen.getByRole('main'));
    expect(main).toContain('md:pl-60');
    // The 240px column is the brick: unprefixed, it leaves 70px of content
    // on a 390px phone.
    expect(main.filter((c) => /^pl-/.test(c))).toEqual([]);
  });

  // Invariant 8: the drawer animates as a transform; the content column must
  // not animate its padding on a phone.
  test('animates the sidebar padding only from md up', () => {
    renderShell();
    const main = classes(screen.getByRole('main'));
    expect(main).toContain('md:transition-[padding]');
    expect(main).not.toContain('transition-[padding]');
  });

  // Invariant 1: 40px of gutter per side is 20% of a 390px viewport.
  test('uses a 16px content gutter below md, widening from md up', () => {
    const { container } = renderShell();
    const wrapper = container.querySelector('.max-w-\\[1400px\\]');
    expect(wrapper).not.toBeNull();
    const gutters = classes(wrapper!);
    expect(gutters).toContain('px-4');
    expect(gutters).toContain('md:px-10');
    expect(gutters).not.toContain('px-10');
  });
});

describe('AppShell routing', () => {
  // Through ProtectedRoute, as production mounts the shell (App.tsx sends
  // "/*" into it) — auth answers before the inner Routes do, so an
  // unauthenticated probe would never reach them.
  function renderShellAt(path: string) {
    return render(
      <MemoryRouter initialEntries={[path]}>
        <ProtectedRoute>
          <AppShell />
        </ProtectedRoute>
      </MemoryRouter>,
    );
  }

  beforeEach(() => {
    localStorage.clear();
    mockedUseAuth.mockReturnValue({
      user: {
        id: 1,
        username: 'alice',
        display_name: 'Alice',
        role: 'admin' as const,
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

  test('unknown path renders the not-found panel with the attempted path', () => {
    renderShellAt('/nonsense');
    expect(
      screen.getByRole('heading', { level: 1, name: 'Page not found' }),
    ).toBeInTheDocument();
    // The echoed path is the point of a panel over a redirect: it is what
    // shows a typo'd bookmark its typo.
    expect(screen.getByText('/nonsense')).toBeInTheDocument();
    // ...and it reads as a SENTENCE. The path sits in its own <span> with the
    // full stop outside it, so the surrounding whitespace is decided by JSX
    // line breaks rather than by anything visible in the source. Pinned as the
    // whole string: one space before the path, none before the stop.
    expect(
      screen.getByText('/nonsense').closest('p'),
    ).toHaveTextContent(/^Nothing lives at \/nonsense\.$/);
    expect(
      screen.getByRole('link', { name: 'Back to Dashboard' }),
    ).toHaveAttribute('href', '/');
  });

  test('known path renders its page, not the not-found panel', () => {
    renderShellAt('/');
    // Positive control first: without it, "panel absent" also passes when
    // nothing rendered at all (e.g. an unauthenticated bounce to /login).
    expect(screen.getByTestId('page-dashboard')).toBeInTheDocument();
    expect(
      screen.queryByRole('heading', { name: 'Page not found' }),
    ).not.toBeInTheDocument();
  });

  test('the not-found escape hatch clears the touch floor on a coarse pointer', () => {
    // The shell's one interactive control outside the nav column, and the only
    // way off this panel other than the browser's back button. It is a `<Link>`
    // in Button clothing, so `asChild` is what carries the floor here — the
    // `h-10` assertion is what proves Slot merged the variant onto the ANCHOR
    // rather than onto a wrapper, which is the failure mode that would leave a
    // bare 20px text link reading as styled.
    renderShellAt('/nonsense');
    const back = screen.getByRole('link', { name: 'Back to Dashboard' });
    expect(back).toHaveClass('h-10', 'coarse:min-h-11');
    // The two specific wrong answers: ungated inflates a mouse desktop at every
    // width, and width-gated leaves the ~1130px tablet on the 40px desktop size
    // while a thumb is doing the tapping.
    expect(back).not.toHaveClass('min-h-11');
    expect(back).not.toHaveClass('md:min-h-11');
    // Control: the token is a decision on this element, not something true of
    // everything the panel renders.
    expect(
      screen.getByText('/nonsense').classList.contains('coarse:min-h-11'),
    ).toBe(false);
  });

  test('the echoed path is allowed to break mid-token', () => {
    // A URL is one long token as far as line-breaking is concerned, and this
    // paragraph is a content-sized flex item — so an unbounded span pans the
    // whole page sideways on a 360px phone. Token assertion because happy-dom
    // lays nothing out; the exact form matters, `break-words` does NOT lower
    // min-content and would not fix it.
    const longPath =
      '/reports/monthly-category-breakdown-for-the-whole-household-2026';
    renderShellAt(longPath);
    const echoed = screen.getByText(longPath);
    expect(echoed.className.split(/\s+/)).toContain('[overflow-wrap:anywhere]');
  });
});
