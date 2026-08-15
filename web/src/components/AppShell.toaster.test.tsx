import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { useEffect } from 'react';
import { toast } from 'sonner';
import { ThemeProviderContext } from '@/hooks/useTheme';

// B50: a toast fired from a routed page's MOUNT effect must render on a cold
// load of that route.
//
// Why this file exists separately from AppShell.test.tsx: that suite stubs
// `@/components/ui/sonner` out (it is about <main>'s layout contract), and a
// stubbed Toaster cannot observe the subscription-order bug at all. Here the
// REAL Toaster is mounted and only the pages are stubbed.
//
// The mechanism, from sonner 2.0.7 (`node_modules/sonner/dist/index.mjs`):
//
//   this.addToast = (data) => { this.publish(data); this.toasts = [...] };
//   this.publish  = (data) => { this.subscribers.forEach(s => s(data)); };
//
// and the Toaster's own subscription is a passive effect over state that
// starts EMPTY and is never seeded from `ToastState.toasts`:
//
//   const [toasts, setToasts] = React.useState([]);
//   React.useEffect(() => ToastState.subscribe(...), [toasts]);
//
// So a toast published before the Toaster subscribes is delivered to nobody
// and never replayed. React flushes passive effects in tree order, so whether
// the routed page's effect or the Toaster's subscription runs first is decided
// by their DOM order inside AppShell — which is exactly what this test pins.

// Dashboard stands in for any page that toasts on mount (in production:
// Settings' one-shot `?tab=` forwarding toast).
vi.mock('../pages/Dashboard', () => ({
  Dashboard: () => {
    useEffect(() => {
      toast.info('cold-load probe');
    }, []);
    return <div data-testid="page-dashboard" />;
  },
}));
vi.mock('../pages/Transactions', () => ({ Transactions: () => <div /> }));
vi.mock('../pages/Budgets', () => ({ Budgets: () => <div /> }));
vi.mock('../pages/Savings', () => ({ Savings: () => <div /> }));
vi.mock('../pages/Categories', () => ({ Categories: () => <div /> }));
vi.mock('../pages/Reports', () => ({ Reports: () => <div /> }));
vi.mock('../pages/Settings', () => ({ Settings: () => <div /> }));
vi.mock('../pages/Trash', () => ({ Trash: () => <div /> }));
vi.mock('./Sidebar', () => ({ Sidebar: () => <div /> }));
vi.mock('./MobileNav', () => ({ MobileNav: () => <div /> }));
// NOTE: `@/components/ui/sonner` is deliberately NOT mocked here.

import { AppShell } from './AppShell';

function renderShell() {
  return render(
    <ThemeProviderContext.Provider
      value={{
        theme: 'dark',
        setTheme: () => {},
        colorTheme: null,
        setColorTheme: () => {},
      }}
    >
      <MemoryRouter initialEntries={['/']}>
        <AppShell />
      </MemoryRouter>
    </ThemeProviderContext.Provider>,
  );
}

describe('AppShell toaster mount order (B50)', () => {
  // Positive control: the probe page really did render, so a missing toast
  // below means the toast was dropped and not that the route never mounted.
  test('renders the probe page', () => {
    renderShell();
    expect(screen.getByTestId('page-dashboard')).toBeInTheDocument();
  });

  test('a toast fired from a page mount effect renders on a cold load', async () => {
    renderShell();
    expect(
      await screen.findByText('cold-load probe'),
    ).toBeInTheDocument();
  });

  // The functional invariant is only "before the routed content" (a Toaster
  // after Sidebar/MobileNav would still catch every page's mount toast), but
  // the a11y note at the site — Notifications is the FIRST landmark and a
  // visible toast the first Tab stop — is true of the LITERAL first child, so
  // pin the literal position; a move to third child is a comment lie even
  // though the test above stays green.
  test('the Toaster is the literal first child of the shell root', () => {
    const { container } = renderShell();
    const root = container.firstElementChild;
    expect(root).not.toBeNull();
    const first = root!.firstElementChild;
    expect(first?.tagName).toBe('SECTION');
    expect(first?.getAttribute('aria-label')).toBe('Notifications alt+T');
  });
});
