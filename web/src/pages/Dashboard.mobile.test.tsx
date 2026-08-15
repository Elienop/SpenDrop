import { render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  cloneElement,
  createElement,
  type ReactElement,
  type ReactNode,
} from 'react';
import type { UseDashboardResult } from '../hooks/useDashboard';
import type { Transaction } from '../api/types';

/**
 * Phone-width behaviour of the Dashboard's Recent Transactions panel.
 *
 * Own file, because the viewport is per-FILE state in happy-dom — a
 * `setViewport` leaking into Dashboard.test.tsx would quietly move its
 * table assertions onto the card branch.
 */
function setViewportWidth(width: number) {
  (
    window as unknown as {
      happyDOM: { setViewport: (v: { width: number }) => void };
    }
  ).happyDOM.setViewport({ width });
}

const PHONE_WIDTH = 390;
const DESKTOP_WIDTH = 1024;

const LONG_DESCRIPTION =
  'Weekly shop at the big supermarket on the coast road including household goods, cleaning supplies and a very long tail of items that the import path never trimmed because validateImportField only runs on the per-row edit route';

const transactions: Transaction[] = [
  {
    id: 1,
    user_id: 1,
    created_by: 'Elie',
    created_by_username: 'elienop',
    date: '2026-04-01',
    amount: 16.85,
    original_amount: 1500000,
    original_currency: 'LBP',
    description: LONG_DESCRIPTION,
    category_id: 1,
    category_name: 'Groceries',
    category_type: 'expense',
    tags: null,
    notes: null,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
  },
  {
    id: 2,
    user_id: 1,
    created_by: 'Elie',
    created_by_username: 'elienop',
    date: '2026-04-02',
    amount: 500,
    original_amount: null,
    original_currency: null,
    description: '',
    category_id: 2,
    category_name: 'Salary',
    category_type: 'income',
    tags: null,
    notes: null,
    created_at: '2026-04-02T00:00:00Z',
    updated_at: '2026-04-02T00:00:00Z',
  },
];

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(async (path: string) => {
      if (path === 'reports/years') {
        return {
          years: [2026],
          current_year: 2026,
          has_transactions: true,
          out_of_range_years: [],
        };
      }
      // The Dashboard reads `data.transactions` off PaginatedResponse — NOT
      // `data.data`. Getting that wrong renders neither presentation and looks
      // exactly like a broken viewport gate.
      if (path.startsWith('transactions')) {
        return { transactions, total: transactions.length };
      }
      if (path === 'currencies') {
        return [
          {
            code: 'USD',
            name: 'US Dollar',
            symbol: '$',
            rate_to_base: 1,
            is_base: true,
            updated_at: '2026-04-01T00:00:00Z',
          },
          {
            code: 'LBP',
            name: 'Lebanese Pound',
            symbol: 'LL',
            rate_to_base: 90000,
            is_base: false,
            updated_at: '2026-04-01T00:00:00Z',
          },
        ];
      }
      return [];
    }),
  },
}));

vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ user: { id: 1, display_name: 'Elie' }, loading: false }),
}));

// Recharts SIZING mock — deliberately not a wholesale stub.
//
// This file used to replace every recharts export with a passthrough `<div>`
// (`Bar: () => <div />`, `XAxis: () => <div />`, …). That renders the Cash
// Flow chart structurally invisible: the 2 -> 3 bump changed three library
// DEFAULTS with no compile error and the suite stayed green throughout. A
// stubbed `XAxis` cannot tell you the phone-width chart still labels every
// month — which is the one chart property this file exists to defend, since
// the whole point of the mobile pass was that nothing on this page may
// silently drop content or pan off-screen at 390px.
//
// The ONLY thing that genuinely cannot work under happy-dom is
// `ResponsiveContainer`: it measures its parent, gets 0x0 (there is no layout
// engine), and recharts bails on a non-positive size. So mock exactly that,
// and leave the real library everywhere else — the shape already proven in
// `src/components/ui/chart.test.tsx`.
//
// The explicit `ReactElement<{ width?: number; height?: number }>` generic is
// REQUIRED: `cloneElement` has no overload for an element whose props are
// `unknown`, and `tsc -b` type-checks test files (unlike `--noEmit`), so
// without it the suite stays green while types go red.
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  return {
    ...actual,
    ResponsiveContainer: ({
      children,
    }: {
      children: ReactElement<{ width?: number; height?: number }>;
    }) => cloneElement(children, { width: 400, height: 300 }),
  };
});

/**
 * Newest-first, exactly as `dashboard_handlers.go` returns it — the Dashboard
 * reverses it into chronological order for the chart, and a fixture in the
 * wrong order would hide that.
 *
 * SIX months, deliberately, and deliberately crossing a year boundary: the
 * default Cash Flow view is 6M, so this fills the slice exactly, and Jan'26
 * sitting next to Dec'25 is what makes the year-aware ISO `date` key
 * observable at all. Against the two-month fixture this file used to carry
 * (from when recharts was stubbed out entirely), a `slice` that kept the wrong
 * count and a tick key that ignored the year both label correctly.
 */
const TREND = [
  { year: 2026, month: 4, total_spent: 3200, total_income: 4500 },
  { year: 2026, month: 3, total_spent: 2800, total_income: 4200 },
  { year: 2026, month: 2, total_spent: 2600, total_income: 4100 },
  { year: 2026, month: 1, total_spent: 2400, total_income: 4000 },
  { year: 2025, month: 12, total_spent: 3900, total_income: 4300 },
  { year: 2025, month: 11, total_spent: 2200, total_income: 3900 },
];

/** What the axis must read, left to right, at phone width. Written out rather
 *  than derived from `TREND` through `formatMonthTick`: re-running the
 *  formatter here would assert only that it equals itself. */
const EXPECTED_MONTH_TICKS = [
  "Nov'25",
  "Dec'25",
  "Jan'26",
  "Feb'26",
  "Mar'26",
  "Apr'26",
];

vi.mock('../hooks/useDashboard', () => ({
  useDashboard: (): UseDashboardResult => ({
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
    trend: TREND,
    categories: [{ id: 1, name: 'Food', total: 1200, limit: null, over: false }],
    loading: false,
    fetching: false,
    error: '',
    refetch: () => {},
  }),
}));

import { Dashboard } from './Dashboard';

function renderDashboard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return render(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
    { wrapper },
  );
}

/**
 * Render AND prove the presentation swapped.
 *
 * Most assertions below would be equally true of the table — the description
 * text, the amount, "View All" are all present either way. If
 * `setViewportWidth` ever stops taking, the desktop tree renders and those
 * assertions go green against the wrong presentation. Cards present AND
 * `role="table"` absent, in the same render, is the control.
 */
async function renderMobile() {
  const result = renderDashboard();
  const list = await screen.findByRole('list', { name: 'Recent transactions' });
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  return { ...result, list };
}

beforeEach(() => {
  vi.clearAllMocks();
  setViewportWidth(PHONE_WIDTH);
});

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
});

describe('Dashboard recent transactions at phone width', () => {
  it('replaces the table with a card list', async () => {
    const { list } = await renderMobile();
    expect(within(list).getAllByRole('listitem')).toHaveLength(2);
  });

  // The control. The existing Dashboard suite asserts the table at the default
  // 1024px; this states the same thing from inside the mobile file, so a
  // regression in the gate fails here too rather than only next door.
  it('keeps the table, and only the table, at md and up', async () => {
    setViewportWidth(DESKTOP_WIDTH);
    renderDashboard();
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument());
    expect(
      screen.queryByRole('list', { name: 'Recent transactions' }),
    ).not.toBeInTheDocument();
  });

  // THE defect this conversion exists for: the table's description cell had no
  // bound at all, so one imported row stretched it to 3,269px inside a 293px
  // box and pushed the amount about eleven screens right.
  it('bounds the description instead of letting it pan the card', async () => {
    const { list } = await renderMobile();
    const description = within(list).getByText(LONG_DESCRIPTION);

    expect(description).toHaveClass('min-w-0', 'truncate', 'font-medium');
    // `truncate` does nothing while a flex child's automatic minimum size is
    // its content, so the floor-lift is asserted with it, not instead of it.
    expect(description).toHaveAttribute('title', LONG_DESCRIPTION);
  });

  it('names a row whose description was imported empty', async () => {
    const { list } = await renderMobile();
    expect(within(list).getByText('(no description)')).toBeInTheDocument();
  });

  it('carries the category, a readable date, and the amount', async () => {
    const { list } = await renderMobile();
    const [first] = within(list).getAllByRole('listitem');

    expect(first).toHaveTextContent('Groceries');
    // The table prints the raw stored string; a card gets a reading format.
    expect(first).toHaveTextContent('Apr 1, 2026');
    expect(first).not.toHaveTextContent('2026-04-01');
  });

  // Same call Trash's card makes, and for the same reason: this household
  // books in LBP and USD daily, so a card showing only the converted figure
  // hides the number the row was entered as. Matching the table here would be
  // keeping the wrong parity.
  it('shows the original currency the table never showed', async () => {
    const { list } = await renderMobile();
    const [first] = within(list).getAllByRole('listitem');

    expect(within(first).getByTestId('amount-display')).toHaveTextContent(
      /-\$16\.85/,
    );
    // Both lines carry the row's direction — see AmountDisplay. Anchored, so
    // a second sign character on either fails.
    expect(
      within(first).getByTestId('amount-display-secondary'),
    ).toHaveTextContent(/^-1,500,000\.00 LBP$/);
  });

  it('keeps the amount from being squeezed by a long description', async () => {
    const { list } = await renderMobile();
    const [first] = within(list).getAllByRole('listitem');
    expect(within(first).getByTestId('amount-display')).toHaveClass('shrink-0');
  });

  // This is a summary, not an editing surface: no selection, no tap-to-edit,
  // and no attribution line (which the table has never shown).
  it('stays read-only, with no selection or attribution', async () => {
    const { list } = await renderMobile();

    expect(within(list).queryByRole('checkbox')).not.toBeInTheDocument();
    expect(within(list).queryByRole('button')).not.toBeInTheDocument();
    expect(within(list).queryByText(/entered by/i)).not.toBeInTheDocument();
  });

  // ── Cash Flow chart ──
  //
  // The card list is why this file exists, but the same page draws a real
  // chart at 390px and, until the recharts 3 bump, the whole suite mocked it
  // away — so a dropped series or a dropped month bucket showed up only in the
  // browser. These run inside the mobile file on purpose: the chart shares the
  // page's narrow grid track, and phone width is where a bucket goes missing
  // first.
  //
  // `ResponsiveContainer` is the one thing still mocked (happy-dom has no
  // layout engine, so it measures 0x0 and recharts refuses to draw); every
  // element queried below is drawn by the real library off the real config.

  /**
   * The chart's drawn SVG — the positive control the count assertions below
   * rest on.
   *
   * `[data-chart]` here is a SCOPE, not the control: it is the shadcn
   * `ChartContainer` div, which renders whether or not recharts draws
   * anything. Neither is `.recharts-wrapper` — measured, not assumed, by
   * neutralising this file's sizing shim to `cloneElement(children, {})` (the
   * exact state the old wholesale recharts mock left this file in): the
   * container AND the wrapper both still render, the wrapper surviving as an
   * empty 81-byte shell (`<div class="recharts-wrapper" style="position:
   * relative; cursor: default;"></div>`) with no `recharts-*` descendants at
   * all. Only `.recharts-surface` and everything under it disappear.
   *
   * (An earlier version of this note said 336 bytes. That number was real but
   * belonged to a different node — it is `[data-chart]`.innerHTML.length, which
   * includes the shadcn `<ChartStyle>` <style> element from ui/chart.tsx. The
   * wrapper itself is 81. Corrected rather than dropped, because a measurement
   * attached to the wrong node is how a comment starts sounding authoritative
   * while being wrong.)
   *
   * So the surface is the first node whose
   * presence means the library actually laid the chart out, and asserting it
   * is what makes "2 series, 6 bars, 6 ticks" a claim about a live chart
   * rather than about a div.
   */
  function chartSurface(container: HTMLElement): Element {
    const chart = container.querySelector('[data-chart]');
    expect(chart).not.toBeNull();
    const surface = chart!.querySelector('.recharts-surface');
    expect(surface).not.toBeNull();
    return surface!;
  }

  // The control, stated as its own test so a sizing shim that stops working
  // fails by name rather than as an off-by-two count somewhere below. Verified
  // by mutation: `cloneElement(children, {})` fails here on
  // `expect(surface).not.toBeNull()`.
  it('actually draws the cash-flow chart at phone width', async () => {
    const { container } = await renderMobile();
    const surface = chartSurface(container);
    // A surface with nothing under it would still be a dead chart.
    expect(surface.querySelectorAll('.recharts-layer').length).toBeGreaterThan(
      0,
    );
  });

  it('draws both cash-flow series, one bar per month', async () => {
    const { container } = await renderMobile();
    const series = chartSurface(container).querySelectorAll('.recharts-bar');

    // Income and expense. One `<Bar>` silently dropped — or one rendering off
    // a dataKey the data does not carry — is invisible to a stubbed `Bar`.
    expect(series).toHaveLength(2);
    for (const bar of series) {
      expect(bar.querySelectorAll('.recharts-bar-rectangle')).toHaveLength(
        TREND.length,
      );
    }
  });

  it('labels every month on the axis, chronologically and year-aware', async () => {
    const { container } = await renderMobile();
    const ticks = Array.from(
      chartSurface(container).querySelectorAll(
        '.recharts-cartesian-axis-tick-value',
      ),
    ).map((tick) => tick.textContent);

    // Pins four things a passthrough `<XAxis />` said nothing about: the
    // `date` dataKey, `formatMonthTick`, the `.reverse()` that puts the newest
    // month on the right, and that the 6M slice keeps every bucket. Verified
    // by mutation: dropping the `.reverse()` renders `Apr'26 … Nov'25` and
    // fails here.
    //
    // What it does NOT cover, measured rather than assumed: deleting
    // `interval={0}` from the axis leaves this green. Recharts culls
    // overlapping ticks by measuring rendered text, happy-dom has no layout
    // engine, so every tick always survives here regardless of the setting.
    // A bucket lost to the DATA path (a wrong slice, a wrong key, a lost
    // reverse) fails; a bucket lost to tick culling is a browser check.
    expect(ticks).toEqual(EXPECTED_MONTH_TICKS);
  });

  it('keeps the month/Latest toggle and View All reachable', async () => {
    await renderMobile();

    const latest = screen.getByRole('button', { name: 'Latest' });
    expect(latest).toBeInTheDocument();
    // Its month sibling, queried structurally: the label is whichever month
    // the page has selected, which is the Dashboard's own state and not
    // something this test should assert a value for.
    const group = latest.parentElement!;
    expect(within(group).getAllByRole('button')).toHaveLength(2);
    expect(
      screen.getByRole('link', { name: /view all/i }),
    ).toBeInTheDocument();
  });
});
