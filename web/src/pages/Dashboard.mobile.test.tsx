import { render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement, type ReactNode } from 'react';
import type { UseDashboardResult } from '../hooks/useDashboard';

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

const transactions = [
  {
    id: 1,
    user_id: 1,
    created_by: 'Elie',
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

// Recharts renders nothing useful without a ResizeObserver; every primitive
// the shadcn chart helper touches via its namespace import has to exist or the
// chart crashes on mount. Same stub set as Dashboard.test.tsx.
vi.mock('recharts', () => ({
  BarChart: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  CartesianGrid: () => <div />,
  ResponsiveContainer: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  YAxis: () => <div />,
  Cell: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  LabelList: () => <div />,
  Customized: () => <div />,
  ReferenceLine: () => <div />,
}));

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
    trend: [
      { year: 2026, month: 4, total_spent: 3200, total_income: 4500 },
      { year: 2026, month: 3, total_spent: 2800, total_income: 4200 },
    ],
    categories: [{ id: 1, name: 'Food', total: 1200, limit: null, over: false }],
    loading: false,
    fetching: false,
    error: '',
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
      '-$16.85',
    );
    expect(
      within(first).getByTestId('amount-display-secondary'),
    ).toHaveTextContent('1,500,000.00 LBP');
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
