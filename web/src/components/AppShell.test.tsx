import { describe, test, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// The shell's routes pull in every page (and their data layers). This suite
// is about the layout contract of <main>, so the pages, the two navigation
// surfaces and the toaster are stubbed out.
vi.mock('../pages/Dashboard', () => ({ Dashboard: () => <div /> }));
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

import { AppShell } from './AppShell';

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
