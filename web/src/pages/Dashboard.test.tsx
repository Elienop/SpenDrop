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

// Recharts mock — Recharts ships as ES modules that don't render in jsdom
// without a ResizeObserver, so every component is stubbed to a passthrough.
//
// IMPORTANT: shadcn's `src/components/ui/chart.tsx` does:
//
//     import * as RechartsPrimitive from "recharts"
//     const ChartLegend = RechartsPrimitive.Legend
//     // and ChartStyle reads RechartsPrimitive.* directly
//
// Because it's a namespace import, any name referenced at module-eval time
// that's missing from this mock becomes `undefined` and React crashes with
// "Element type is invalid: expected a string or a class/function but got:
// undefined" as soon as ChartContainer mounts. The mock below therefore
// declares *every* Recharts surface shadcn's chart helper can touch, not
// just the ones Dashboard.tsx uses directly. Add new stubs here whenever a
// future chart primitive starts pulling in more Recharts exports.
vi.mock('recharts', () => ({
  // Used by Dashboard.tsx
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  CartesianGrid: () => <div />,
  // Referenced by shadcn's chart.tsx namespace import (ChartLegend, ChartStyle, tooltip plumbing)
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  YAxis: () => <div />,
  Cell: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  Surface: () => <div />,
  Layer: () => <div />,
  Sector: () => <div />,
  LabelList: () => <div />,
  Customized: () => <div />,
  ReferenceLine: () => <div />,
}));

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
            amount: 42.50,
            original_amount: null,
            original_currency: null,
            description: 'Groceries',
            category_id: 1,
            category_name: 'Food',
            category_type: 'expense',
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

vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({
    user: { id: 1, username: 'elie', display_name: 'Elie' },
    isAuthenticated: true,
    loading: false,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  }),
}));

import { Dashboard } from './Dashboard';

describe('Dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseDashboard.mockReturnValue(defaultDashboardData);
    reportYears = [new Date().getFullYear()];
    reportYearsFails = false;
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
