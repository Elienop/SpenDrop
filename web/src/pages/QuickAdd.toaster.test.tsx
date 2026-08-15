import { describe, test, expect, vi } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useEffect, type ReactNode } from 'react';
import { toast } from 'sonner';
import { ThemeProviderContext } from '@/hooks/useTheme';

// B50, QuickAdd's half. `/quick` mounts its OWN <Toaster /> (it is a
// self-contained route, not part of AppShell), so it needs the same ordering
// guarantee for the same reason — the full argument lives in the comment block
// at AppShell.tsx's <Toaster />.
//
// The shape that is at risk here is specifically a CHILD that toasts on mount:
// QuickAdd's own effects run after its children's, so its own mount toasts were
// never in danger, but anything it renders above the Toaster was.
//
// Why a separate file from QuickAdd.test.tsx: that suite mocks BOTH `sonner`
// and `@/components/ui/sonner` away (it asserts on captured toast calls), and
// neither a mocked bus nor a stubbed Toaster can observe a subscription-order
// bug. Here both are real and only QuickAdd's data layer is stubbed.

vi.mock('@/hooks/useIsCoarsePointer', () => ({
  useIsCoarsePointer: () => false,
}));

// RecentlyAdded stands in for any child that toasts from a mount effect. It
// renders inside <main>, i.e. after the Toaster in the fixed order and before
// it in the broken one.
vi.mock('@/components/RecentlyAdded', () => ({
  RecentlyAdded: () => {
    useEffect(() => {
      toast.info('cold-load probe');
    }, []);
    return <div data-testid="recently-added" />;
  },
}));

vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => ({ user: { id: 7 }, refreshUser: vi.fn() }),
}));

vi.mock('@/hooks/useDescriptionHistory', () => ({
  useDescriptionHistory: () => ({
    descriptions: [],
    failed: false,
    waitingForNetwork: false,
    retry: vi.fn(),
  }),
}));

vi.mock('@/lib/offline-queue', () => ({
  enqueue: vi.fn(),
  removeQueued: vi.fn(),
  getAllQueued: vi.fn(async () => []),
  needsSignIn: () => false,
  subscribe: () => () => {},
}));

// Typed to the real `ApiClient.get` (`get<T>(path: string): Promise<T>`) rather
// than to a variadic `unknown[]` — a mock that lies about its shape stops
// failing when the thing it stands in for changes.
//
// Referenced through an arrow, never passed directly: `vi.mock` is hoisted
// above this `const`, so a factory that reads `apiGet` eagerly hits the TDZ.
const apiGet = vi.fn<(path: string) => Promise<unknown[]>>(async () => []);
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: {
    get: (path: string) => apiGet(path),
    post: vi.fn(),
    del: vi.fn(),
    put: vi.fn(),
  },
}));
// The same module under the relative specifier some hooks import it by.
vi.mock('../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: {
    get: (path: string) => apiGet(path),
    post: vi.fn(),
    del: vi.fn(),
    put: vi.fn(),
  },
}));

// NOTE: `sonner` and `@/components/ui/sonner` are deliberately NOT mocked here.

import { QuickAdd } from './QuickAdd';

function renderQuickAdd() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <ThemeProviderContext.Provider
      value={{
        theme: 'dark',
        setTheme: () => {},
        colorTheme: null,
        setColorTheme: () => {},
      }}
    >
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </ThemeProviderContext.Provider>
  );
  return render(
    <MemoryRouter initialEntries={['/quick']}>
      <QuickAdd />
    </MemoryRouter>,
    { wrapper },
  );
}

describe('QuickAdd toaster mount order (B50)', () => {
  // Positive control: the probe child really mounted, so a missing toast below
  // means the toast was dropped and not that the child never rendered.
  test('renders the probe child', async () => {
    renderQuickAdd();
    expect(screen.getByTestId('recently-added')).toBeInTheDocument();
    // Flush QuickAdd's mocked category/currency queries before the test ends;
    // without this their resolution lands outside act and warns.
    await act(async () => {});
  });

  test('a toast fired from a child mount effect renders on a cold load', async () => {
    renderQuickAdd();
    expect(await screen.findByText('cold-load probe')).toBeInTheDocument();
  });
});
