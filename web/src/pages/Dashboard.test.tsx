import { describe, test, expect, vi, beforeEach } from 'vitest';
import {
  render as rtlRender,
  screen,
  waitFor,
  within,
  type RenderOptions,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import React from 'react';

// Each render gets a fresh QueryClient so the `useBaseCurrency` →
// `useCurrencies` useQuery (migrated to TanStack Query) has a provider and an
// isolated cache. `retry: false` keeps rejected currency fetches from re-firing.
function render(ui: React.ReactElement, options?: RenderOptions) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: React.ReactNode }) =>
    React.createElement(QueryClientProvider, { client }, children);
  return rtlRender(ui, { wrapper, ...options });
}
import { STORAGE_KEYS } from '@/lib/storage-keys';
import type { UseDashboardResult } from '../hooks/useDashboard';
import type { CategoryBreakdownItem } from '../api/types';

// Recharts sizing mock — NOT a wholesale stub.
//
// This file used to replace every recharts export with a passthrough `<div>`
// (`Bar: () => <div />`, `Tooltip: () => <div />`, …). That renders the Cash
// Flow chart structurally invisible to the suite: the recharts 2 -> 3 bump
// changed three library DEFAULTS with no compile error and the whole 1400-test
// suite stayed green while a legend silently reordered in the browser. A
// stubbed `Bar` cannot tell you the chart drew two series, in the right order,
// off the right dataKeys.
//
// The ONLY thing that genuinely does not work under happy-dom is
// `ResponsiveContainer`: it measures its parent, gets 0x0 (there is no layout
// engine), and recharts bails on a non-positive size. So mock exactly that and
// leave the real library everywhere else — the same shape already proven in
// `src/components/ui/chart.test.tsx`.
//
// The explicit `React.ReactElement<{ width?: number; height?: number }>`
// generic is REQUIRED: `cloneElement` has no overload for an element whose
// props are `unknown`, and `tsc -b` type-checks test files (unlike
// `--noEmit`), so without it the suite stays green while types go red.
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    ResponsiveContainer: ({
      children,
    }: {
      children: React.ReactElement<{ width?: number; height?: number }>;
    }) => React.cloneElement(children, { width: 400, height: 300 }),
  };
});

const defaultDashboardData: UseDashboardResult = {
  summary: {
    year: 2026,
    month: 4,
    budget: 5000,
    total_spent: 3200,
    total_income: 4500,
    remaining: 1300,
    savings_this_month: 500,
    savings_goal: 10000,
    savings_ytd: 7500,
    savings_goal_progress: 75,
  },
  // Backend returns trend newest-first (see dashboard_handlers.go) — keep
  // this fixture in the same order so chartData's `.reverse()` produces
  // chronologically-sorted bars.
  trend: [
    { year: 2026, month: 4, total_spent: 3200, total_income: 4500 },
    { year: 2026, month: 3, total_spent: 2800, total_income: 4200 },
  ],
  categories: [
    { id: 1, name: 'Food', total: 1200, limit: null, over: false },
    { id: 2, name: 'Transport', total: 800, limit: null, over: false },
  ],
  loading: false,
  fetching: false,
  error: '',
};

const mockUseDashboard = vi.fn<() => UseDashboardResult>(() => defaultDashboardData);

vi.mock('../hooks/useDashboard', () => ({
  useDashboard: (...args: unknown[]) => mockUseDashboard(...(args as [])),
}));

/**
 * Years the mocked `reports/years` endpoint reports, per test. Kept mutable so
 * a case can narrow the ledger and prove the persisted selection survives.
 *
 * Deliberately routed through the api-client mock rather than mocking
 * `useReportYears` itself: this way the Dashboard drives the REAL hook, the
 * REAL query key, and the REAL request path, so a typo in any of the three
 * fails here. `useReportYears`'s own suite stubs `fetch` a level below and
 * pins the wire field names.
 */
let reportYears: number[] = [2026];
/** When set, `reports/years` rejects — the offline / 401 / 500 degradation. */
let reportYearsFails = false;
// Mutable holder in the same style as `reportYears` above, because `vi.mock`'s
// factory is hoisted and cannot close over a per-test value. Only the
// empty-description test moves it.
let recentDescription = 'Groceries';
/** Same holder idiom, for the signed-amount cases. Positive expense by
 *  default, which is what every pre-existing test in this file assumes. */
let recentAmount = 42.5;
let recentType: 'expense' | 'income' = 'expense';

vi.mock('../api/client', () => ({
  api: {
    // Path-aware: `useBaseCurrency` (→ `useCurrencies`, now TanStack Query)
    // fetches `currencies` and expects a Currency[] — returning the recent-tx
    // paginated object for every path would make `list.find(...)` throw and
    // unmount the Dashboard subtree. The recent-transactions table reads the
    // paginated `transactions?...` response.
    get: vi.fn((path: string) => {
      if (path === 'reports/years') {
        if (reportYearsFails) {
          return Promise.reject(new Error('offline'));
        }
        return Promise.resolve({
          years: reportYears,
          current_year: reportYears[0] ?? new Date().getFullYear(),
          has_transactions: true,
          out_of_range_years: [],
        });
      }
      if (path === 'currencies') {
        return Promise.resolve([
          {
            code: 'USD',
            name: 'US Dollar',
            symbol: '$',
            rate_to_base: 1,
            is_base: true,
            updated_at: '2026-04-01T00:00:00Z',
          },
        ]);
      }
      return Promise.resolve({
        transactions: [
          {
            id: 1,
            user_id: 1,
            date: '2026-04-01',
            amount: recentAmount,
            original_amount: null,
            original_currency: null,
            description: recentDescription,
            category_id: 1,
            category_name: 'Food',
            category_type: recentType,
            tags: null,
            notes: null,
            created_at: '2026-04-01T00:00:00Z',
            updated_at: '2026-04-01T00:00:00Z',
          },
        ],
        total: 1,
        page: 1,
        per_page: 6,
        total_pages: 1,
      });
    }),
  },
}));

// An untyped factory, so nothing checks this against AuthContextType: it used
// to offer `isAuthenticated`, which the context has never had, and to omit
// `unverified`, which it does have. Both corrected here. The mocked functions
// are minted once in the factory closure rather than per call, so their
// identity is stable across renders.
vi.mock('../hooks/useAuth', () => {
  const login = vi.fn();
  const register = vi.fn();
  const logout = vi.fn();
  const refreshUser = vi.fn();
  return {
    useAuth: () => ({
      user: { id: 1, username: 'elie', display_name: 'Elie' },
      loading: false,
      unverified: false,
      login,
      register,
      logout,
      refreshUser,
    }),
  };
});

import { Dashboard } from './Dashboard';

describe('Dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseDashboard.mockReturnValue(defaultDashboardData);
    reportYears = [new Date().getFullYear()];
    reportYearsFails = false;
    recentDescription = 'Groceries';
    recentAmount = 42.5;
    recentType = 'expense';
  });

  test('renders welcome heading with user name', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/Welcome back, Elie/);
  });

  test('renders month/year selectors', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByLabelText(/month/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
  });

  // Today + a 140px month picker + a 100px year picker is ~318px of rigid
  // controls, against ~358px of content on a 390px phone. It fits, but with
  // nothing to spare and nothing to give: the row has no shrinkable item, so
  // one more control or a wider locale pushes the page into horizontal
  // scroll. Wrapping is the degrade. Verified as a class contract only —
  // happy-dom lays nothing out.
  test('the header controls can wrap instead of widening the page', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    const controls = screen.getByRole('button', { name: 'Today' }).parentElement;
    expect(controls).toHaveClass('flex', 'flex-wrap');
  });

  // A grid item defaults to min-width:auto, so anything wide inside a card —
  // the transactions table, a future chart — becomes the track's min-content
  // and widens the whole page. Both report tabs with this shape did exactly
  // that at a 390px viewport (599px and 3530px); this grid measured clean with
  // current dev data, which is a property of the data, not of the layout.
  // Asserted over every child the grid has, so a new card fails here too.
  test('every card in the chart/table grid may shrink below its content', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    const grid = screen.getByText('Recent Transactions').closest('.grid');
    if (!grid) throw new Error('Recent Transactions card is not inside a grid');

    const cards = Array.from(grid.children);
    expect(cards.length).toBeGreaterThan(0);
    for (const card of cards) {
      expect(card).toHaveClass('min-w-0');
    }
  });

  // The KPI delta badges only render when the trend contains the month BEFORE
  // the one the Dashboard has selected, and the Dashboard defaults to today.
  // These helpers build a fixture anchored to the current date so the badge
  // actually appears — with a fixed 2026-04 fixture no badge renders and the
  // assertions below would pass vacuously.
  function trendForCurrentMonth(thisSpent: number, prevSpent: number) {
    const now = new Date();
    const y = now.getFullYear();
    const m = now.getMonth() + 1;
    const prevM = m === 1 ? 12 : m - 1;
    const prevY = m === 1 ? y - 1 : y;
    return [
      { year: y, month: m, total_spent: thisSpent, total_income: 4500 },
      { year: prevY, month: prevM, total_spent: prevSpent, total_income: 4200 },
    ];
  }

  function expensesBadge() {
    // "Expenses" also appears in the cash-flow chart config, so pick the
    // occurrence whose CardHeader actually carries a delta badge.
    const found = screen
      .getAllByText('Expenses')
      .map((el) => el.parentElement)
      .find((p) => p?.textContent?.includes('%'));
    if (!found) throw new Error('no Expenses KPI badge rendered');
    return found;
  }

  test('the Expenses KPI reports a spending increase as an increase', async () => {
    // Spending ROSE from 2800 to 3200 — up 14.3%. The badge used to negate the
    // expenses delta and render that rise as "-14.3%" with a downward arrow.
    // KpiCard applies no good/bad colouring, so the flipped sign was not
    // conveying "spending more is bad"; it told the user their spending had
    // fallen when it had grown.
    localStorage.clear();
    mockUseDashboard.mockReturnValue({
      ...defaultDashboardData,
      summary: { ...defaultDashboardData.summary!, total_spent: 3200 },
      trend: trendForCurrentMonth(3200, 2800),
    });
    render(<MemoryRouter><Dashboard /></MemoryRouter>);

    const header = await waitFor(expensesBadge);
    expect(header).toHaveTextContent('+14.3%');
    expect(header).not.toHaveTextContent('-14.3%');
  });

  test('the Expenses KPI reports a spending decrease as a decrease', async () => {
    // Spending FELL from 4000 to 3200 — down 20%.
    localStorage.clear();
    mockUseDashboard.mockReturnValue({
      ...defaultDashboardData,
      summary: { ...defaultDashboardData.summary!, total_spent: 3200 },
      trend: trendForCurrentMonth(3200, 4000),
    });
    render(<MemoryRouter><Dashboard /></MemoryRouter>);

    const header = await waitFor(expensesBadge);
    expect(header).toHaveTextContent('-20.0%');
  });

  test('renders KPI cards with Total Balance', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Total Balance')).toBeInTheDocument();
      expect(screen.getAllByText('Income').length).toBeGreaterThanOrEqual(1);
      expect(screen.getAllByText('Expenses').length).toBeGreaterThanOrEqual(1);
      expect(screen.getByText('Savings Rate')).toBeInTheDocument();
      // Assert the KPI row actually rendered values — "Income" / "Expenses"
      // strings also appear in the cash-flow chart config, so label-only
      // assertions would pass even if the KPI row were missing. These
      // formatted dollar splits (4500-3200=1300) are unique to KPI cards.
      expect(screen.getByText('$1,300.00')).toBeInTheDocument();
      expect(screen.getByText('$4,500.00')).toBeInTheDocument();
      expect(screen.getByText('$3,200.00')).toBeInTheDocument();
    });
  });

  test('renders negative Total Balance with minus sign when expenses exceed income', async () => {
    mockUseDashboard.mockReturnValue({
      ...defaultDashboardData,
      summary: {
        ...defaultDashboardData.summary!,
        total_income: 2000,
        total_spent: 3598.9,
        remaining: -1598.9,
      },
    });
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      // Regression guard for the "Total Balance" KPI: when expenses exceed
      // income, the card must surface the sign. Previous code called
      // `Math.abs()` in `splitCurrency`, which silently stripped the sign
      // and rendered `$1,598.90` — identical to a positive balance.
      expect(screen.getByText('-$1,598.90')).toBeInTheDocument();
    });
  });

  test('renders Cash Flow section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Cash Flow')).toBeInTheDocument();
    });
  });

  test('renders Spending by Category section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Spending by Category')).toBeInTheDocument();
    });
  });

  test('renders Recent Transactions in a table layout', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Recent Transactions')).toBeInTheDocument();
      // Should use a table element instead of a list
      expect(screen.getByRole('table')).toBeInTheDocument();
      // Should have a "View All" link
      expect(screen.getByRole('link', { name: /view all/i })).toBeInTheDocument();
    });
  });

  // The desktop table composes its own sign and colour — it does NOT go
  // through `AmountDisplay`, which is what the phone card list beside it uses
  // (`Dashboard.mobile.test.tsx` covers that branch). Two implementations of
  // one rule is exactly how the two presentations of the same row drifted.
  describe('the recent-transactions amount', () => {
    const amountCell = (): HTMLElement => {
      const row = screen.getByText('Groceries').closest('tr');
      if (row === null) throw new Error('no recent-transactions row');
      return within(row).getAllByRole('cell').slice(-1)[0];
    };

    test('a normal expense renders one minus, in the ordinary colour', async () => {
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      await waitFor(() => expect(screen.getByText('Groceries')).toBeInTheDocument());
      // Exact: "-$42.50" is a substring of the "--$42.50" a double-signed
      // render produces.
      expect(amountCell().textContent).toBe('-$42.50');
      expect(
        within(amountCell()).queryByTestId('amount-sign-note'),
      ).toBeNull();
    });

    test('income renders one plus, in the inflow colour', async () => {
      recentType = 'income';
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      await waitFor(() => expect(screen.getByText('Groceries')).toBeInTheDocument());
      expect(amountCell().textContent).toBe('+$42.50');
      expect(amountCell().querySelector('span')).toHaveClass('text-emerald-500');
    });

    test('a REFUND renders as an inflow on an expense row, and says so', async () => {
      recentAmount = -42.5;
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      await waitFor(() => expect(screen.getByText('Groceries')).toBeInTheDocument());
      expect(amountCell().textContent).toBe('+$42.50Refund');
      expect(amountCell().querySelector('span')).toHaveClass('text-emerald-500');
      expect(
        within(amountCell()).getByTestId('amount-sign-note'),
      ).toHaveTextContent('Refund');
    });

    test('an income REVERSAL renders as an outflow, and says so', async () => {
      recentType = 'income';
      recentAmount = -42.5;
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      await waitFor(() => expect(screen.getByText('Groceries')).toBeInTheDocument());
      expect(amountCell().textContent).toBe('-$42.50Reversal');
      expect(amountCell().querySelector('span')).not.toHaveClass(
        'text-emerald-500',
      );
    });
  });

  test('renders transaction data in table rows', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      // The mock transaction "Groceries" should appear in a table row
      expect(screen.getByText('Groceries')).toBeInTheDocument();
      // "Food" appears in both the category bars and the transaction table
      expect(screen.getAllByText('Food').length).toBeGreaterThanOrEqual(2);
      // Category initial should appear in the icon cell
      expect(screen.getByText('F')).toBeInTheDocument();
    });
  });

  test('the desktop description cell is bounded in both directions', async () => {
    // The phone branch renders `TransactionDescription`, which is bound, titled
    // and fallback-applied. This desktop branch rendered a bare `<span>`, so one
    // imported row with an unbroken token stretched the table — measured, the
    // same shape that took the Reports tables to 3464px inside a 293px card.
    //
    // NOT the `min-w-0` + `truncate` idiom the ledger uses, and this is the part
    // a future edit is most likely to "correct" back. That mechanism works on a
    // FLEX child and does nothing in a table: a `white-space: nowrap` cell
    // contributes its full text width as the column's min-content, so a
    // `max-w-*` above it becomes the column's FLOOR. Measured in Chrome with
    // that idiom: `max-w-md` held the description column at 448px inside a card
    // 384.5px wide at a 1024px viewport — 302px of overflow, worse than the bug.
    //
    // `overflow-wrap:anywhere` removes the floor for MIN-content, and the
    // tempting conclusion — that a cap is therefore harmless above it — is
    // false. Re-measured, twice, because a review asked for exactly that change:
    // Chrome's auto table layout sizes this column from its max-content capped
    // by `max-width`, so a long token makes the cap the width. Adding
    // `max-w-md` back pinned the column at 448px and the table overflowed its
    // card by 302px at 1024, 174px at 1280 and 87px at 1440. Without it: 0px at
    // 768, 900, 1024, 1150, 1280, 1440 and 1920, column adapting 145px -> 465px.
    //
    // The two Reports cells DO carry caps, and that is not an inconsistency:
    // theirs never bind (12rem against a 107px column at phone width, 28rem
    // against 383px at desktop), while this card is only 384.5px wide at 1024.
    //
    // happy-dom lays nothing out, so none of that is observable here; what is
    // asserted is that the classes reach the rendered cell.
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    const description = await screen.findByText('Groceries');

    expect(description.className).toContain('[overflow-wrap:anywhere]');
    expect(description.className).toContain('line-clamp-2');
    expect(description).toHaveAttribute('title', 'Groceries');

    const cell = description.closest('td');
    // Asserted to EXIST first: `closest('td')?.className ?? ''` passes happily
    // against '' if the markup ever stops being a table, which is the one shape
    // that would make every assertion above meaningless.
    expect(cell).not.toBeNull();
    expect(cell!.className).not.toMatch(/max-w-/);

    // BOTH lines in this cell, not just the description. Bounding only the
    // first left the column pinned at 473.7px and the table overflowing its
    // card by 119px at 1440 — the category name below is user-supplied too and
    // the server caps it at 100 characters, so it sets the column's min-content
    // on its own. Found by measuring after the description was already bounded.
    const category = within(cell!).getByText('Food');
    expect(category.className).toContain('[overflow-wrap:anywhere]');
    expect(category.className).toContain('line-clamp-1');
  });

  test('an empty description falls back instead of rendering nothing', async () => {
    // Pins the `transactionLabel` half of the same change. The default fixture
    // is 'Groceries', so `transactionLabel(tx)` and `tx.description` agree and
    // reverting to the raw field stays green — this is the row that separates
    // them. Import bypasses the presence check the per-row edit route applies,
    // so a blank description is reachable from a spreadsheet, and without the
    // fallback the cell renders an empty clipped box with an empty `title`.
    recentDescription = '';

    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    const fallback = await screen.findByText('(no description)');
    expect(fallback).toHaveAttribute('title', '(no description)');
    expect(fallback.className).toContain('line-clamp-2');
  });

  test('does not render Savings Progress section', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Savings Progress')).not.toBeInTheDocument();
    expect(screen.queryByText('Saved YTD')).not.toBeInTheDocument();
    expect(screen.queryByText('Annual Goal')).not.toBeInTheDocument();
  });

  test('does not render removed Monthly Budget section', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Monthly Budget')).not.toBeInTheDocument();
  });

  test('renders 6M and 12M toggle buttons inside a ButtonGroup', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    const btn6 = screen.getByRole('button', { name: '6M' });
    const btn12 = screen.getByRole('button', { name: '12M' });
    expect(btn6).toBeInTheDocument();
    expect(btn12).toBeInTheDocument();
    // Both buttons should be inside a group container
    const groups = screen.getAllByRole('group');
    const cashFlowGroup = groups.find((g) => g.contains(btn6));
    expect(cashFlowGroup).toBeDefined();
    expect(cashFlowGroup).toContainElement(btn12);
  });

  // ── The Cash Flow chart, as the REAL recharts draws it ───────────────────
  //
  // Every other assertion in this file survives a stubbed recharts, which is
  // exactly the problem the wholesale mock created: `Bar: () => <div />` and
  // `XAxis: () => <div />` cannot tell you that the chart drew the wrong
  // series, silently dropped one, ordered the months newest-first, or that a
  // library default moved under the 2 -> 3 bump. These read the SVG recharts
  // actually produced.
  describe('the Cash Flow chart draws the trend', () => {
    /** One drawn bar: the gradient identifies its series, `x` its month. */
    type DrawnBar = { fill: string; x: number; height: number };

    const drawnBars = (container: HTMLElement): DrawnBar[] =>
      Array.from(
        container.querySelectorAll<SVGPathElement>('path.recharts-rectangle'),
      ).map((p) => ({
        fill: p.getAttribute('fill') ?? '',
        x: Number(p.getAttribute('x')),
        height: Number(p.getAttribute('height')),
      }));

    /** Heights of one series, ordered oldest month → newest (left → right). */
    const seriesHeights = (bars: DrawnBar[], fill: string): number[] =>
      bars
        .filter((b) => b.fill === fill)
        .sort((a, b) => a.x - b.x)
        .map((b) => b.height);

    /** X-axis tick labels, in the order recharts laid them out. */
    const tickLabels = (container: HTMLElement): string[] =>
      Array.from(
        container.querySelectorAll('text.recharts-cartesian-axis-tick-value'),
      ).map((t) => t.textContent ?? '');

    /**
     * Resolve a `fill="url(#id)"` reference to the paint server it NAMES.
     * Returns null when nothing carries that id — a DANGLING reference, which
     * paints nothing on screen while the `fill` attribute string still reads
     * exactly right.
     *
     * Scoped to the rendered container, not to the bar's own `<svg>`, and that
     * is deliberate: a same-document `url(#id)` resolves against the DOCUMENT,
     * so a paint server in a sibling `<svg>` is a legitimate reference and a
     * same-`<svg>` assertion would reject working markup. Matching on
     * `[id="…"]` rather than `#id` sidesteps CSS identifier escaping.
     */
    const resolvePaintServer = (
      container: HTMLElement,
      fill: string,
    ): Element | null => {
      const id = /^url\(#(.+)\)$/.exec(fill)?.[1];
      if (id === undefined) return null;
      return container.querySelector(`[id="${id}"]`);
    };

    test('draws one bar per month for BOTH series, each on its own gradient', async () => {
      const { container } = render(<MemoryRouter><Dashboard /></MemoryRouter>);

      // Two assertions, and they cover different failures:
      //
      // 1. The `fill` STRINGS partition the bars two-and-two. The gradient
      //    reference is the only thing in the DOM that names a series, so this
      //    is what catches a lost <Bar> or a series drawn on the wrong fill.
      // 2. Each of those references RESOLVES to a real <linearGradient> in the
      //    rendered tree. Assertion 1 alone only compares literals, and that
      //    gap is mutation-proven: renaming the def to `fillIncomeRENAMED`
      //    while leaving `fill="url(#fillIncome)"` untouched points every
      //    income bar at a paint server that does not exist — unpainted in a
      //    browser — and assertion 1 still passes. Only assertion 2 fails.
      await waitFor(() => {
        const byFill: Record<string, number> = {};
        for (const b of drawnBars(container)) {
          byFill[b.fill] = (byFill[b.fill] ?? 0) + 1;
        }
        expect(byFill).toEqual({
          'url(#fillIncome)': 2,
          'url(#fillExpense)': 2,
        });

        // Positive control on the resolver itself: an id that is not in the
        // SVG must come back null. Without it a resolver that returned the
        // first node it found would wave the loop below through on any string.
        // The sentinel is deliberately an id no def could ever plausibly be
        // renamed TO — a near-miss like `fillIncomeRENAMED` collides with the
        // mutant, and the mutation then dies HERE instead of on the assertion
        // that is supposed to catch it.
        expect(resolvePaintServer(container, 'url(#no-such-paint-server)')).toBeNull();

        const defs = Object.keys(byFill).map((fill) => {
          const def = resolvePaintServer(container, fill);
          expect(def, `${fill} resolves to nothing — dangling paint server`).not.toBeNull();
          expect(def?.tagName.toLowerCase()).toBe('lineargradient');
          return def;
        });
        // Two references, two DISTINCT gradients — not both bars sharing one.
        expect(new Set(defs).size).toBe(2);
      });
    });

    test('bar heights encode the trend values against one shared axis', async () => {
      const { container } = render(<MemoryRouter><Dashboard /></MemoryRouter>);

      // The fixture is newest-first, as the backend sends it; `chartData`
      // reverses it, so left → right is Mar then Apr.
      const expected = [4200, 4500, 2800, 3200]; // income Mar/Apr, expense Mar/Apr

      // Bars grow in from zero height (recharts' `isAnimationActive` default,
      // ~400ms for a Bar) and mid-flight the two series are NOT in step with
      // each other — measured one frame in, income and expense sat 2% apart on
      // a scale they must ultimately share. Asserting INSIDE waitFor is what
      // makes that a retry rather than a flake: the only state that satisfies
      // it is the settled one.
      await waitFor(() => {
        const bars = drawnBars(container);
        const heights = [
          ...seriesHeights(bars, 'url(#fillIncome)'),
          ...seriesHeights(bars, 'url(#fillExpense)'),
        ];

        // Positive control FIRST: every comparison below is a RATIO, so an
        // all-zero chart — bars that never drew, or a collapsed y-domain —
        // would divide 0 by 0 and needs to be excluded explicitly rather than
        // left to produce a NaN that happens to fail for the wrong reason.
        expect(heights).toHaveLength(4);
        for (const h of heights) expect(h).toBeGreaterThan(0);

        // Both series share one y-axis, so ONE pixels-per-dollar scale has to
        // explain all four bars. Compared as heights normalised against the
        // first bar, not as absolute pixels: the pixel values depend on the
        // mocked 400x300 box and recharts' margins, the proportions do not.
        // Normalising also keeps the tolerance meaningful — the raw scale here
        // is ~0.004 px/$, small enough that `toBeCloseTo(…, 3)` on it would
        // wave through a 2% error, which is exactly the size of the
        // mid-animation skew this has to reject.
        const norm = (xs: number[]) => xs.map((x) => x / xs[0]);
        norm(heights).forEach((ratio, i) => {
          expect(ratio).toBeCloseTo(norm(expected)[i], 3);
        });
      });
    });

    test('the X axis labels every month bucket, oldest first', async () => {
      const { container } = render(<MemoryRouter><Dashboard /></MemoryRouter>);

      // Pins `formatMonthTick` and the chronological order. What it does NOT
      // pin, despite appearances, is `interval={0}`: verified by mutation —
      // deleting it from the XAxis leaves this file 32/32 green, because
      // recharts thins ticks by measured text width and happy-dom measures
      // everything as zero, so its default keeps every tick anyway. Tick
      // thinning is a browser-only behaviour and this is a browser-only check.
      //
      // `formatMonthTick` itself IS load-bearing here: a bare "Mar" collapsed
      // same-month buckets from different years into one category on any 12M
      // view that crossed a year boundary.
      await waitFor(() => {
        expect(tickLabels(container)).toEqual(["Mar'26", "Apr'26"]);
      });
    });

    test('6M shows the LAST six months; 12M shows the whole trend', async () => {
      // Newest-first, like the backend: Aug back to Jan.
      mockUseDashboard.mockReturnValue({
        ...defaultDashboardData,
        trend: [8, 7, 6, 5, 4, 3, 2, 1].map((month) => ({
          year: 2026,
          month,
          total_spent: 1000 + month,
          total_income: 2000 + month,
        })),
      });
      const user = userEvent.setup();
      const { container } = render(<MemoryRouter><Dashboard /></MemoryRouter>);

      // Default view is 6m. `slice(-6)` keeps the SIX MOST RECENT months —
      // `slice(0, 6)` would keep Jan–Jun and look just as plausible in a
      // screenshot, which is precisely why it needs pinning by label.
      await waitFor(() => {
        expect(tickLabels(container)).toEqual([
          "Mar'26", "Apr'26", "May'26", "Jun'26", "Jul'26", "Aug'26",
        ]);
      });
      // Positive control on the bar side: six buckets must mean twelve bars,
      // otherwise the label list above could be describing an axis drawn over
      // a chart that plotted nothing.
      await waitFor(() => expect(drawnBars(container)).toHaveLength(12));

      await user.click(screen.getByRole('button', { name: '12M' }));
      await waitFor(() => {
        expect(tickLabels(container)).toEqual([
          "Jan'26", "Feb'26", "Mar'26", "Apr'26",
          "May'26", "Jun'26", "Jul'26", "Aug'26",
        ]);
      });
      await waitFor(() => expect(drawnBars(container)).toHaveLength(16));
    });
  });

  test('does not show "Other" category or "Show more" when <= 6 categories', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Other')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /show more/i })).not.toBeInTheDocument();
  });

  test('shows a budget bar and an over-budget header badge for an over category', async () => {
    mockUseDashboard.mockReturnValue({
      ...defaultDashboardData,
      categories: [
        { id: 1, name: 'Food', total: 612, limit: 500, over: true },
        { id: 2, name: 'Transport', total: 180, limit: 300, over: false },
      ],
    });
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      // Passive header badge counts over-budget categories (1 of 2 here).
      expect(screen.getByText('1 over budget')).toBeInTheDocument();
      // Per-category budget percentage: 612/500 = 122%.
      expect(screen.getByText(/122%/)).toBeInTheDocument();
      // Under-budget category still shows its percentage: 180/300 = 60%.
      expect(screen.getByText(/60%/)).toBeInTheDocument();
    });
  });

  // ── Refunded categories ──
  //
  // Every assertion here is about a NEGATIVE `total`, which the wire could not
  // produce before signed amounts and which the page's arithmetic was written
  // without. The bar widths are read out of the inline `style` because that is
  // where the defect lived: a negative percentage is not a small bar, it is an
  // INVALID CSS declaration that the browser drops, leaving a block div at
  // `width: auto` — the full track.
  describe('a category the household got money back on', () => {
    /** The painted fill inside a track, by the track's position on the card. */
    const fills = (container: HTMLElement): string[] =>
      Array.from(
        container.querySelectorAll<HTMLElement>('.rounded-full > div'),
      ).map((el) => el.style.width);

    test('the budget bar paints EMPTY, not full', async () => {
      // Food: 500 budgeted, netted -125 after refunds. `budgetPct` is -25,
      // and `width: "-25%"` renders as a fully consumed budget.
      mockUseDashboard.mockReturnValue({
        ...defaultDashboardData,
        categories: [
          { id: 1, name: 'Food', total: -125, limit: 500, over: false },
        ],
      });
      const { container } = render(
        <MemoryRouter>
          <Dashboard />
        </MemoryRouter>,
      );
      await waitFor(() =>
        expect(screen.getByText(/-25%/)).toBeInTheDocument(),
      );
      // Both bars on the row: the category gauge and the budget bar. Neither
      // may paint, and NEITHER may be left at `auto` — an empty string here
      // is the failure mode, not a pass.
      expect(fills(container)).toEqual(['0%', '0%']);
    });

    test('the percentage text keeps the real, negative figure', async () => {
      // The bar reports consumption, which cannot go below empty; the number
      // reports the arithmetic. Clamping BOTH would hide the refund entirely.
      mockUseDashboard.mockReturnValue({
        ...defaultDashboardData,
        categories: [
          { id: 1, name: 'Food', total: -125, limit: 500, over: false },
        ],
      });
      render(
        <MemoryRouter>
          <Dashboard />
        </MemoryRouter>,
      );
      await waitFor(() =>
        expect(screen.getByText(/-\$125\.00 \/ \$500\.00 · -25%/)).toBeInTheDocument(),
      );
    });

    test('it is named as refunded rather than painted as a tiny spender', async () => {
      // `Math.max(pct, 2)` floors every slice at a visible 2% bar, so a
      // refunded category used to render as the smallest spender on the card
      // — indistinguishable from a real one.
      mockUseDashboard.mockReturnValue({
        ...defaultDashboardData,
        categories: [
          { id: 1, name: 'Food', total: 300, limit: null, over: false },
          { id: 2, name: 'Transport', total: -40, limit: null, over: false },
        ],
      });
      const { container } = render(
        <MemoryRouter>
          <Dashboard />
        </MemoryRouter>,
      );
      await waitFor(() =>
        expect(screen.getByTestId('category-refunded-2')).toHaveTextContent(
          'Net refund',
        ),
      );
      // Food takes its full share of the GROSS spending (300 of 300), and
      // Transport paints nothing at all.
      expect(fills(container)).toEqual(['100%', '0%']);
      expect(screen.queryByTestId('category-refunded-1')).not.toBeInTheDocument();
    });

    test('a category refunded to exactly zero is named too', async () => {
      // Zero is the state that reads as "nothing happened" while the ledger
      // holds two rows. It is not a spender and not an empty category.
      mockUseDashboard.mockReturnValue({
        ...defaultDashboardData,
        categories: [
          { id: 1, name: 'Food', total: 300, limit: null, over: false },
          { id: 2, name: 'Transport', total: 0, limit: null, over: false },
        ],
      });
      render(
        <MemoryRouter>
          <Dashboard />
        </MemoryRouter>,
      );
      await waitFor(() =>
        expect(screen.getByTestId('category-refunded-2')).toHaveTextContent(
          'Refunded in full',
        ),
      );
    });

    test('one refund cannot inflate every other slice past 100%', async () => {
      // The share denominator used to be the NET of all categories. With a
      // refund in the set that net is smaller than the biggest spender —
      // here 300 + (-200) = 100 — so Food's share came out 300%: a bar three
      // times its own track, and every other slice wrong with it.
      mockUseDashboard.mockReturnValue({
        ...defaultDashboardData,
        categories: [
          { id: 1, name: 'Food', total: 300, limit: null, over: false },
          { id: 2, name: 'Transport', total: -200, limit: null, over: false },
        ],
      });
      const { container } = render(
        <MemoryRouter>
          <Dashboard />
        </MemoryRouter>,
      );
      await waitFor(() =>
        expect(screen.getByTestId('category-refunded-2')).toBeInTheDocument(),
      );
      expect(fills(container)).toEqual(['100%', '0%']);
      // The header total stays NET, deliberately: the month really did cost
      // $100 once the refund is counted.
      expect(screen.getByText('$100.00')).toBeInTheDocument();
    });
  });

  test('shows no over-budget badge when everything is within budget', async () => {
    mockUseDashboard.mockReturnValue({
      ...defaultDashboardData,
      categories: [
        { id: 1, name: 'Food', total: 180, limit: 300, over: false },
        { id: 2, name: 'Transport', total: 95, limit: null, over: false },
      ],
    });
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText(/60%/)).toBeInTheDocument(); // Food has a limit
    });
    expect(screen.queryByText(/over budget/)).not.toBeInTheDocument();
  });

  describe('Today button', () => {
    beforeEach(() => {
      // Each test seeds localStorage with a specific month/year, so clear
      // it up-front to avoid leaking state from earlier tests in this file.
      localStorage.clear();
    });

    test('is disabled when already viewing the current month', () => {
      // The "Today" control is an escape hatch, so it should go dark when
      // the user is already on today's month — otherwise it flashes an
      // action that does nothing.
      const now = new Date();
      localStorage.setItem(STORAGE_KEYS.dashboardYear, String(now.getFullYear()));
      localStorage.setItem(STORAGE_KEYS.dashboardMonth, String(now.getMonth() + 1));
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      expect(screen.getByRole('button', { name: /today/i })).toBeDisabled();
    });

    test('is enabled when viewing a past month', () => {
      localStorage.setItem(STORAGE_KEYS.dashboardYear, '2024');
      localStorage.setItem(STORAGE_KEYS.dashboardMonth, '3');
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      expect(screen.getByRole('button', { name: /today/i })).not.toBeDisabled();
    });

    test('resets to the current month when clicked', async () => {
      const user = userEvent.setup();
      localStorage.setItem(STORAGE_KEYS.dashboardYear, '2024');
      localStorage.setItem(STORAGE_KEYS.dashboardMonth, '3');
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      const todayBtn = screen.getByRole('button', { name: /today/i });
      expect(todayBtn).not.toBeDisabled();
      await user.click(todayBtn);
      // After the click the button flips to disabled — the state change
      // is what the test proves; no need to poke into localStorage.
      await waitFor(() => expect(todayBtn).toBeDisabled());
    });
  });

  describe('with more than 6 categories', () => {
    const manyCategories: CategoryBreakdownItem[] = [
      { id: 1, name: 'Food', total: 1200, limit: null, over: false },
      { id: 2, name: 'Transport', total: 800, limit: null, over: false },
      { id: 3, name: 'Housing', total: 700, limit: null, over: false },
      { id: 4, name: 'Entertainment', total: 500, limit: null, over: false },
      { id: 5, name: 'Healthcare', total: 400, limit: null, over: false },
      { id: 6, name: 'Utilities', total: 300, limit: null, over: false },
      { id: 7, name: 'Shopping', total: 200, limit: null, over: false },
      { id: 8, name: 'Education', total: 100, limit: null, over: false },
    ];

    beforeEach(() => {
      mockUseDashboard.mockReturnValue({
        ...defaultDashboardData,
        categories: manyCategories,
      });
    });

    test('shows first 6 categories and hides the rest when collapsed', () => {
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      // First 6 should be visible
      expect(screen.getByText('Food')).toBeInTheDocument();
      expect(screen.getByText('Transport')).toBeInTheDocument();
      expect(screen.getByText('Housing')).toBeInTheDocument();
      expect(screen.getByText('Entertainment')).toBeInTheDocument();
      expect(screen.getByText('Healthcare')).toBeInTheDocument();
      expect(screen.getByText('Utilities')).toBeInTheDocument();
      // 7th and 8th should NOT be visible
      expect(screen.queryByText('Shopping')).not.toBeInTheDocument();
      expect(screen.queryByText('Education')).not.toBeInTheDocument();
      // No "Other" bucket
      expect(screen.queryByText('Other')).not.toBeInTheDocument();
    });

    test('shows "Show more" button when more than 6 categories', () => {
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      expect(screen.getByRole('button', { name: /show more/i })).toBeInTheDocument();
    });

    test('expands to show all categories and "Show less" on click', async () => {
      const user = userEvent.setup();
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      const showMoreBtn = screen.getByRole('button', { name: /show more/i });
      await user.click(showMoreBtn);
      // All 8 categories should now be visible
      expect(screen.getByText('Shopping')).toBeInTheDocument();
      expect(screen.getByText('Education')).toBeInTheDocument();
      // "Show more" replaced by "Show less"
      expect(screen.queryByRole('button', { name: /show more/i })).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: /show less/i })).toBeInTheDocument();
    });

    test('collapses back to 6 categories when "Show less" is clicked', async () => {
      const user = userEvent.setup();
      render(<MemoryRouter><Dashboard /></MemoryRouter>);
      // Expand
      await user.click(screen.getByRole('button', { name: /show more/i }));
      // Collapse
      await user.click(screen.getByRole('button', { name: /show less/i }));
      // 7th and 8th hidden again
      expect(screen.queryByText('Shopping')).not.toBeInTheDocument();
      expect(screen.queryByText('Education')).not.toBeInTheDocument();
      // "Show more" is back
      expect(screen.getByRole('button', { name: /show more/i })).toBeInTheDocument();
    });
  });

  // The Dashboard year Select used to be `Array.from({length: 5}, ...)` — a
  // rolling five-year window from today. `/api/dashboard/summary` is where an
  // old row first shows up as a NUMBER, so a household that imported a 1984
  // statement could see the total but never select the year that produced it.
  //
  // Its trend request is hardcoded `months=12`, so widening the picker costs
  // nothing on the wire.
  describe('the year picker follows the ledger', () => {
    /** Open the Year Select and return the years it offers, newest first. */
    async function offeredYears(
      user: ReturnType<typeof userEvent.setup>,
    ): Promise<number[]> {
      await user.click(screen.getByRole('combobox', { name: /^year$/i }));
      const listbox = await screen.findByRole('listbox');
      return within(listbox)
        .getAllByRole('option')
        .map((el) => Number(el.textContent));
    }

    function setup() {
      // An open Radix Select sets `pointer-events: none` on <body>, and
      // happy-dom has no layout engine to tell the portalled content apart.
      return userEvent.setup({ pointerEventsCheck: 0 });
    }

    beforeEach(() => {
      localStorage.clear();
    });

    test('offers exactly the years the ledger holds, gaps included', async () => {
      reportYears = [2026, 2024, 1984];
      const user = setup();
      render(<MemoryRouter><Dashboard /></MemoryRouter>);

      await waitFor(async () =>
        expect(await offeredYears(user)).toEqual([2026, 2024, 1984]),
      );
    });

    test('keeps a persisted year the ledger no longer reports', async () => {
      // `dashboardYear` is persisted in localStorage, so the selection can
      // OUTLIVE the data that produced it — reinstall, restore, or just delete
      // the last 2024 row. The years response then arrives NARROWER than the
      // restored selection, and a Select holding a value with no matching item
      // renders a blank trigger, silently. This is the one picker where the
      // selection can be stale before the first render.
      localStorage.setItem(STORAGE_KEYS.dashboardYear, '2024');
      localStorage.setItem(STORAGE_KEYS.dashboardMonth, '3');
      reportYears = [2026];
      const user = setup();
      render(<MemoryRouter><Dashboard /></MemoryRouter>);

      const trigger = screen.getByRole('combobox', { name: /^year$/i });
      await waitFor(async () =>
        expect(await offeredYears(user)).toEqual([2026, 2024]),
      );
      expect(trigger).toHaveTextContent('2024');
    });

    test('still offers a usable year when the request fails', async () => {
      reportYearsFails = true;
      const user = setup();
      render(<MemoryRouter><Dashboard /></MemoryRouter>);

      expect(await offeredYears(user)).toEqual([new Date().getFullYear()]);
    });
  });
});
