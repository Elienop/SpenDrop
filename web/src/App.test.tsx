import { describe, test, expect, vi, beforeEach } from 'vitest';
import React from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

// Mock the auth module
vi.mock('./hooks/useAuth', () => ({
  useAuth: vi.fn(),
  AuthProvider: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

// Mock the API client (real pages call api.get, etc.)
//
// Routed by path rather than one blanket `mockResolvedValue`. The blanket
// version answered EVERY endpoint with the paginated-transactions shape, which
// held only because these tests never let a query settle: the moment one does
// (the 12M click below flushes them), `useCurrencies` runs `list.find` over
// that object and throws `list.find is not a function`. Two endpoints therefore
// get their real shape; everything else keeps the old default.
vi.mock('./api/client', () => ({
  api: {
    get: vi.fn(async (path: string) => {
      if (path.startsWith('currencies')) return [];
      if (path.startsWith('reports/years')) {
        return {
          years: [2026],
          current_year: 2026,
          has_transactions: true,
          out_of_range_years: [],
          future_years: [],
        };
      }
      return { transactions: [], total: 0, page: 1, per_page: 5 };
    }),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

// Twelve months of cash-flow trend, newest-first — the order
// `/api/dashboard/trend` returns and the order `Dashboard` reverses before
// charting. Twelve is not decoration: it is the widest bucket count the axis
// has to label, and it straddles a year boundary, which is the case the `'YY`
// suffix in `formatMonthTick` exists for.
//
// It used to be `trend: []`. An empty chart renders no ticks and no bars, so
// with the wholesale recharts mock gone it would still have asserted nothing —
// the fixture is what gives the chart something to get wrong.
const DASHBOARD_TREND = [
  { year: 2026, month: 7, total_spent: 3200, total_income: 4500 },
  { year: 2026, month: 6, total_spent: 3100, total_income: 4400 },
  { year: 2026, month: 5, total_spent: 3000, total_income: 4300 },
  { year: 2026, month: 4, total_spent: 2900, total_income: 4200 },
  { year: 2026, month: 3, total_spent: 2800, total_income: 4100 },
  { year: 2026, month: 2, total_spent: 2700, total_income: 4000 },
  { year: 2026, month: 1, total_spent: 2600, total_income: 3900 },
  { year: 2025, month: 12, total_spent: 2500, total_income: 3800 },
  { year: 2025, month: 11, total_spent: 2400, total_income: 3700 },
  { year: 2025, month: 10, total_spent: 2300, total_income: 3600 },
  { year: 2025, month: 9, total_spent: 2200, total_income: 3500 },
  { year: 2025, month: 8, total_spent: 2100, total_income: 3400 },
];

// Mock useDashboard so Dashboard renders content instead of loading skeleton
vi.mock('./hooks/useDashboard', () => ({
  useDashboard: () => ({
    summary: {
      budget: 5000,
      total_spent: 3200,
      total_income: 4500,
      remaining: 1300,
      savings_this_month: 500,
      savings_goal: 10000,
      savings_ytd: 7500,
      savings_goal_progress: 75,
    },
    trend: DASHBOARD_TREND,
    categories: [],
    loading: false,
    error: '',
  }),
}));

// Mock ONLY recharts' sizing — the rest of the library is real.
//
// This file used to stub every recharts export as a passthrough `<div>`, which
// made it structurally blind: the 2 -> 3 bump changed three library DEFAULTS
// with no compile error and every test here stayed green, because a `<div />`
// standing in for `<XAxis>` cannot drop a tick or reorder a legend.
//
// `ResponsiveContainer` measures its parent, which is 0x0 under happy-dom, and
// recharts bails on a non-positive size — so a fixed size is the single thing
// standing between us and real charts in tests. Same recipe as
// `components/ui/chart.test.tsx`, which is where it was proven.
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    // Typed explicitly: `cloneElement` has no overload for an element whose
    // props are unknown, so the size props must be declared for `tsc -b` —
    // which type-checks test files, unlike `--noEmit`.
    ResponsiveContainer: ({
      children,
    }: {
      children: React.ReactElement<{ width?: number; height?: number }>;
    }) => React.cloneElement(children, { width: 400, height: 300 }),
  };
});

import { useAuth } from './hooks/useAuth';
import App from './App';
import { ThemeProvider } from './components/theme-provider';

const mockedUseAuth = vi.mocked(useAuth);

const authenticatedUser = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin' as const,
  created_at: '2024-01-01',
};

function renderApp(initialPath = '/') {
  // Real pages mount useTransactions(), which calls useQueryClient() and now
  // hard-requires a QueryClientProvider ancestor (the production provider lives
  // in main.tsx, not App.tsx). Fresh client per render with retries off and gc
  // disabled so cached data never leaks across tests.
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <MemoryRouter initialEntries={[initialPath]}>
          <App />
        </MemoryRouter>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}

describe('App', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('when authenticated', () => {
    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: authenticatedUser,
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('renders sidebar and dashboard heading on /', () => {
      renderApp('/');
      // Sidebar has the app title
      expect(screen.getByText('SpenDrop')).toBeInTheDocument();
      // Main content has an h1 with welcome greeting
      expect(
        screen.getByRole('heading', { level: 1 }),
      ).toHaveTextContent(/Welcome back/);
    });

    // The chart assertions below are the reason the wholesale recharts mock had
    // to go. This file mounts the WHOLE app — App -> ProtectedRoute -> AppShell
    // -> Dashboard -> ChartContainer -> BarChart — so it is the only place that
    // sees the cash-flow chart render through the real shell. Against the old
    // `Bar: () => <div />` stubs none of this DOM existed, and every query
    // below would have returned an empty list while the tests stayed green.
    //
    // Every selector here is recharts' OWN (`.recharts-*`), not ours: that is
    // deliberate, because the classes only exist if the real library rendered.

    test('cash flow chart renders real recharts bars, one per series per month', () => {
      const { container } = renderApp('/');

      // Positive control for the sizing mock. `ResponsiveContainer` measures
      // its parent, which is 0x0 under happy-dom, and recharts bails on a
      // non-positive size — so if the fixed 400x300 clone ever stops being
      // applied, the library lays nothing out.
      //
      // It queries `.recharts-surface`, NOT `.recharts-wrapper`. Measured, not
      // assumed: with the shim neutralised to `cloneElement(children, {})`, the
      // wrapper still renders as an empty shell, so a wrapper check passes in
      // exactly the state it is supposed to catch — an inert control. The
      // surface is the first node that only exists once the chart is really
      // drawn.
      //
      // Scope note, so this control is not credited with more than it does:
      // the value assertions below would NOT go silently vacuous without it —
      // they fail loudly (`expected [] to deeply equal [ 'Feb'26', … ]`). This
      // control's job is to make that failure legible as "the chart never
      // rendered" rather than "the data is wrong".
      const surface = container.querySelector('.recharts-surface');
      expect(surface).not.toBeNull();

      // The default view is 6M, and `chartData` reverses the newest-first
      // fixture before slicing the last six — so these are the SIX most recent
      // months of DASHBOARD_TREND, oldest to newest, left to right.
      //
      // This pins four separate things at once, each of which would have been
      // invisible before: the XAxis `dataKey="date"` (wrong key -> bare 0..5
      // indices), the `tickFormatter={formatMonthTick}` (dropped -> raw
      // "2026-07-01" ISO strings), the `[...trend].reverse()` (dropped ->
      // Jul'26 first), and the 6m slice width.
      const ticks = Array.from(
        container.querySelectorAll('.recharts-cartesian-axis-tick-value'),
      ).map((el) => el.textContent);
      expect(ticks).toEqual([
        "Feb'26",
        "Mar'26",
        "Apr'26",
        "May'26",
        "Jun'26",
        "Jul'26",
      ]);

      // Two <Bar> series (income, expense) x six months.
      //
      // These count NODES, so be exact about what they do and do not catch.
      // Both halves below were run as mutations on pages/Dashboard.tsx and
      // watched to fail:
      //   - deleting the expense <Bar> takes `.recharts-bar` 2 -> 1;
      //   - pointing it at a key no row has (`dataKey="doesNotExist"`) takes
      //     `.recharts-bar-rectangle` 12 -> 6, NOT to 0 — recharts drops the
      //     rectangle for an undefined value but keeps the series <g>, so
      //     `.recharts-bar` stays at 2. That asymmetry is why both counts are
      //     asserted rather than either one alone.
      // They are BLIND to a series bound to the wrong-but-PRESENT key: mutating
      // `<Bar dataKey="expense">` to `dataKey="income"` leaves both numbers here
      // untouched and this test green. The `plots its own dataKey` test below is
      // what covers that, by reading VALUES.
      expect(container.querySelectorAll('.recharts-bar')).toHaveLength(2);
      expect(container.querySelectorAll('.recharts-bar-rectangle')).toHaveLength(12);
    });

    // The bar geometry recharts renders, per series. `fill` is the series'
    // visual identity (`url(#fillIncome)` / `url(#fillExpense)`) and is a
    // separate prop from `dataKey`, which is exactly what makes it usable as a
    // label here: a `dataKey` mutation cannot repaint the bars to match itself.
    function readSeriesHeights(container: HTMLElement, fill: string): number[] {
      return Array.from(
        container.querySelectorAll<SVGPathElement>('.recharts-bar-rectangle path'),
      )
        .filter((path) => path.getAttribute('fill') === fill)
        .map((path) => Number(path.getAttribute('height')));
    }

    test('each cash flow series plots its own dataKey, not the other one', async () => {
      const { container } = renderApp('/');

      // recharts animates bars up from zero (Bar's default `animationDuration`
      // is 400ms in recharts 3), so the height attributes are a moving target
      // while that runs, and the two series are a frame or so apart: on the
      // first painted frame the measured px-per-unit was 0.000318 for income
      // against 0.000302 for expense, a 5% skew. Heights are therefore
      // proportional to values only WITHIN one series mid-flight. `waitFor`
      // polls until the animation settles, at which point both series sit on
      // one exact y-scale and the reconstruction below is consistent across
      // them — the retry loop is for settling, not for slow rendering.
      await waitFor(
        () => {
          const income = readSeriesHeights(container, 'url(#fillIncome)');
          const expense = readSeriesHeights(container, 'url(#fillExpense)');
          expect(income).toHaveLength(6);
          expect(expense).toHaveLength(6);

          // Recover the plotted numbers from pixels. The scale is read off the
          // chart's own tallest income bar rather than hard-coded, so anything
          // that rescales every bar by the same factor — a different axis
          // domain, a different mock canvas size — cancels out instead of
          // breaking the test. Rounded to the nearest 10 to absorb the height
          // attribute's 4dp; adjacent fixture values are 100 apart, ten times
          // that bucket.
          const pixelsPerUnit = income[5] / 4500;
          const toUnits = (height: number) =>
            Math.round(height / pixelsPerUnit / 10) * 10;

          // Exactly the fixture's `total_income` / `total_spent` for Feb..Jul
          // 2026, oldest to newest. Income and expense are disjoint ranges
          // (3900+ vs 3200-) so neither series can satisfy the other's
          // assertion.
          expect(income.map(toUnits)).toEqual([
            4000, 4100, 4200, 4300, 4400, 4500,
          ]);
          expect(expense.map(toUnits)).toEqual([
            2700, 2800, 2900, 3000, 3100, 3200,
          ]);
        },
        // 3s, deliberately UNDER vitest's 5s testTimeout. This was first
        // written as 8s, and the mutant then blew the TEST timeout before
        // `waitFor` gave up — the run reported "Test timed out in 5000ms",
        // which names neither the series nor the values. Keeping the inner
        // timeout lower is what makes the failure legible. Headroom is ample:
        // the whole test measures 169ms wall clock and the animation it waits
        // out is 400ms.
        { timeout: 3000, interval: 100 },
      );

      // MUTATION-PROVEN: with `<Bar dataKey="expense">` changed to
      // `dataKey="income"` in pages/Dashboard.tsx this fails with
      // `expected [ 4000, 4100, 4200, 4300, 4400, 4500 ] to deeply equal
      // [ 2700, 2800, 2900, 3000, 3100, 3200 ]` — a value assertion, not a
      // crash and not a timeout. That is the mutation the node counts in the
      // test above cannot see at all.
      //
      // `fill` is used only as the series LABEL, but that still binds paint to
      // data: swapping the two gradients while leaving both dataKeys alone was
      // run as well, and also fails (`expected [ 3800, 3940, 4080, 4220, 4360,
      // 4500 ] to deeply equal [ 4000, ... ]` — the values land under the other
      // label, rescaled by the anchor). So this covers the binding from either
      // side.
      //
      // WHAT THIS DOES *NOT* PIN: the absolute y-scale. `pixelsPerUnit` is
      // derived from a bar of this same chart, so a change that rescaled the
      // whole axis uniformly (a different domain, a different container size)
      // is invisible here by construction. It pins the RELATIVE magnitudes,
      // which is what identifies the binding — not the axis.
    });

    test('12M view widens to twelve months across the year boundary', () => {
      const { container } = renderApp('/');

      fireEvent.click(screen.getByRole('button', { name: '12M' }));

      // Aug'25 -> Jul'26: the `'YY` suffix is load-bearing. A bare "Aug"
      // tick collapses same-month buckets from different years into one
      // category on any 12M view that crosses a year boundary, which is
      // exactly the shape `formatMonthTick` exists to prevent — and this
      // fixture deliberately straddles one.
      //
      // WHAT THIS DOES *NOT* PIN: the chart's `interval={0}`. Verified by
      // mutation — deleting that prop from the XAxis left all ten tests here
      // green. recharts only drops overlapping ticks after MEASURING the tick
      // text, and happy-dom has no text metrics (every string measures 0 wide),
      // so nothing ever overlaps in this environment and the default
      // `interval="preserveEnd"` behaves like `0`. Twelve labels here therefore
      // prove twelve BUCKETS, not twelve drawn labels; only a browser pass can
      // prove the latter. Do not read a passing run as cover for that prop.
      const ticks = Array.from(
        container.querySelectorAll('.recharts-cartesian-axis-tick-value'),
      ).map((el) => el.textContent);
      expect(ticks).toEqual([
        "Aug'25",
        "Sep'25",
        "Oct'25",
        "Nov'25",
        "Dec'25",
        "Jan'26",
        "Feb'26",
        "Mar'26",
        "Apr'26",
        "May'26",
        "Jun'26",
        "Jul'26",
      ]);
      expect(container.querySelectorAll('.recharts-bar-rectangle')).toHaveLength(24);
    });

    test('renders reports heading on /reports', () => {
      renderApp('/reports');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Reports' }),
      ).toBeInTheDocument();
    });

    test('renders transactions heading on /transactions', () => {
      renderApp('/transactions');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Transactions' }),
      ).toBeInTheDocument();
    });

    test('renders categories heading on /categories', () => {
      renderApp('/categories');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Categories' }),
      ).toBeInTheDocument();
    });

    test('renders settings heading on /settings', () => {
      renderApp('/settings');
      expect(
        screen.getByRole('heading', { level: 1, name: 'Settings' }),
      ).toBeInTheDocument();
    });
  });

  describe('when not authenticated', () => {
    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: null,
        loading: false,
        unverified: false,
        refreshUser: vi.fn(),
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('renders login page on /login without sidebar', () => {
      renderApp('/login');
      expect(
        screen.getByRole('heading', { level: 1, name: /spendrop/i }),
      ).toBeInTheDocument();
      expect(screen.queryByRole('complementary')).not.toBeInTheDocument();
    });

    test('renders register page on /register without sidebar', () => {
      renderApp('/register');
      expect(
        screen.getByRole('heading', { level: 1, name: /spendrop/i }),
      ).toBeInTheDocument();
      expect(screen.queryByRole('complementary')).not.toBeInTheDocument();
    });

    test('redirects to login for protected routes', async () => {
      renderApp('/');
      await waitFor(() => {
        expect(
          screen.getByRole('heading', { level: 1, name: /spendrop/i }),
        ).toBeInTheDocument();
      });
    });
  });
});
