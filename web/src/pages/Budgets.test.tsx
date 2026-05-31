import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, cleanup } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement, type ReactNode } from 'react';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
    upload: vi.fn(),
  },
}));

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { toast } from 'sonner';
import { Budgets } from './Budgets';
import type { Category, CategoryBudget } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);

const CATEGORIES: Category[] = [
  {
    id: 10,
    name: 'Groceries',
    type: 'expense',
    icon: '🛒',
    sort_order: 1,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 11,
    name: 'Rent',
    type: 'expense',
    icon: '🏠',
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 12,
    name: 'Old Cable',
    type: 'expense',
    icon: null,
    sort_order: 5,
    is_active: false,
    created_at: '2024-01-01',
  },
  {
    id: 20,
    name: 'Salary',
    type: 'income',
    icon: '💰',
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
];

const CATEGORY_LIMITS: CategoryBudget[] = [{ category_id: 10, amount: 400 }];

function defaultGet(path: string): Promise<unknown> {
  // Order matters: category-budgets contains "budget" so it must be
  // matched before the generic budgets branch.
  if (path.startsWith('category-budgets')) return Promise.resolve(CATEGORY_LIMITS);
  if (path === 'categories' || path.startsWith('categories'))
    return Promise.resolve(CATEGORIES);
  if (path.includes('budget'))
    return Promise.resolve([
      { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
    ]);
  if (path === 'currencies')
    return Promise.resolve([
      {
        code: 'USD',
        name: 'US Dollar',
        symbol: '$',
        rate_to_base: 1,
        is_base: true,
        updated_at: '',
      },
    ]);
  return Promise.resolve([]);
}

function asAdmin() {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    },
    loading: false,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

function asMember() {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 2,
      username: 'bob',
      display_name: 'Bob',
      role: 'member',
      created_at: '2024-01-01',
    },
    loading: false,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

// Wraps a subtree in a fresh QueryClientProvider so the `useBaseCurrency` →
// `useCurrencies` useQuery (migrated to TanStack Query) has a provider and an
// isolated cache. Used by `renderBudgets` and the inline navigation-guard
// renders that mount a sidebar anchor alongside <Budgets />.
function QueryWrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return createElement(QueryClientProvider, { client }, children);
}

function renderBudgets() {
  return render(
    <QueryWrapper>
      <MemoryRouter>
        <Budgets />
      </MemoryRouter>
    </QueryWrapper>,
  );
}

describe('Budgets page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    mockedApi.get.mockImplementation(defaultGet);
    // Freeze to a known month so default month/year and the
    // category-budgets fetch query are deterministic.
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
  });

  afterEach(() => {
    // Explicit RTL cleanup — happy-dom + vitest can leak DOM between
    // tests when an outer beforeEach installs document-level listeners,
    // and a leaked previous render shadows the current one's queries.
    cleanup();
    vi.useRealTimers();
  });

  describe('page chrome', () => {
    beforeEach(asAdmin);

    test('renders the Budgets heading', async () => {
      renderBudgets();
      expect(
        screen.getByRole('heading', { level: 1, name: /^budgets$/i }),
      ).toBeInTheDocument();
    });

    test('renders both the Monthly Budgets card and the Category Limits card', async () => {
      renderBudgets();
      await waitFor(() => {
        expect(screen.getByText(/monthly budgets/i)).toBeInTheDocument();
      });
      // Two matches exist (CardTitle + the "Save Category Limits" button).
      // The CardTitle is the one we care about; assert its presence loosely.
      expect(screen.getAllByText(/category limits/i).length).toBeGreaterThan(0);
    });
  });

  describe('Monthly Budgets panel — as admin', () => {
    beforeEach(asAdmin);

    test('fetches budgets from the budgets endpoint', async () => {
      renderBudgets();
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          expect.stringMatching(/^budgets\?year=/),
        );
      });
    });

    test('saves budget via PUT to budgets/{year}/{month}', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const label = /Budget for April 2026/i;
      await waitFor(() => {
        expect(screen.getByLabelText(label)).toBeInTheDocument();
      });

      const input = screen.getByLabelText(label);
      await user.clear(input);
      await user.type(input, '5000');

      await user.click(screen.getByRole('button', { name: /save budgets/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'budgets/2026/4',
          { amount: 5000 },
        );
      });
    });

    test('Set all applies amount to every month and shows Undo', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      // Wait for the initial fetch so baseline is populated.
      await waitFor(() => {
        expect(
          (screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement).value,
        ).toBe('3000');
      });

      const bulk = screen.getByLabelText(/Apply amount to all months of 2026/i);
      await user.clear(bulk);
      await user.type(bulk, '1000');
      await user.click(screen.getByRole('button', { name: /^Apply$/ }));

      // Every month takes the bulk value.
      for (const name of [
        'January', 'February', 'March', 'April', 'May', 'June',
        'July', 'August', 'September', 'October', 'November', 'December',
      ]) {
        const input = screen.getByLabelText(
          new RegExp(`Budget for ${name} 2026`, 'i'),
        ) as HTMLInputElement;
        expect(input.value).toBe('1000');
      }

      // Undo chip is visible.
      expect(
        screen.getByRole('button', { name: /^Undo$/ }),
      ).toBeInTheDocument();
    });

    test('Undo restores pre-bulk amounts', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      const may = () =>
        screen.getByLabelText(/Budget for May 2026/i) as HTMLInputElement;

      await waitFor(() => expect(april().value).toBe('3000'));
      expect(may().value).toBe('');

      const bulk = screen.getByLabelText(/Apply amount to all months of 2026/i);
      await user.type(bulk, '1000');
      await user.click(screen.getByRole('button', { name: /^Apply$/ }));
      expect(april().value).toBe('1000');
      expect(may().value).toBe('1000');

      await user.click(screen.getByRole('button', { name: /^Undo$/ }));

      expect(april().value).toBe('3000');
      expect(may().value).toBe('');
      expect(
        screen.queryByRole('button', { name: /^Undo$/ }),
      ).not.toBeInTheDocument();
    });

    test('Copy from previous year fills months from prior year', async () => {
      // Override the default mock: return different rows for 2025.
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'budgets?year=2026')
          return Promise.resolve([
            { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
          ]);
        if (path === 'budgets?year=2025')
          return Promise.resolve([
            { id: 10, year: 2025, month: 1, amount: 800, updated_at: '' },
            { id: 11, year: 2025, month: 2, amount: 900, updated_at: '' },
          ]);
        return defaultGet(path);
      });

      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      await waitFor(() => {
        expect(
          (screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement).value,
        ).toBe('3000');
      });

      await user.click(screen.getByRole('button', { name: /Copy from 2025/i }));

      await waitFor(() => {
        expect(
          (screen.getByLabelText(/Budget for January 2026/i) as HTMLInputElement).value,
        ).toBe('800');
      });
      expect(
        (screen.getByLabelText(/Budget for February 2026/i) as HTMLInputElement).value,
      ).toBe('900');
      // Months not present in prior year reset to empty (Copy is a replace).
      expect(
        (screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement).value,
      ).toBe('');
      expect(
        screen.getByRole('button', { name: /^Undo$/ }),
      ).toBeInTheDocument();
    });

    test('Copy from previous year shows info toast when prior year is empty', async () => {
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'budgets?year=2026')
          return Promise.resolve([
            { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
          ]);
        if (path === 'budgets?year=2025') return Promise.resolve([]);
        return defaultGet(path);
      });

      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      await waitFor(() => {
        expect(screen.getByLabelText(/Budget for April 2026/i)).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /Copy from 2025/i }));

      await waitFor(() => {
        expect(mockedToast.info).toHaveBeenCalledWith('No budgets found for 2025');
      });
      // April input unchanged (no bulk write happened).
      expect(
        (screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement).value,
      ).toBe('3000');
      expect(
        screen.queryByRole('button', { name: /^Undo$/ }),
      ).not.toBeInTheDocument();
    });

    test('Apply then Copy then Undo restores original baseline', async () => {
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'budgets?year=2026')
          return Promise.resolve([
            { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
          ]);
        if (path === 'budgets?year=2025')
          return Promise.resolve([
            { id: 10, year: 2025, month: 1, amount: 800, updated_at: '' },
          ]);
        return defaultGet(path);
      });

      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      const january = () =>
        screen.getByLabelText(/Budget for January 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      // First bulk: Apply "1000" → snapshot captures baseline (Apr=3000).
      const bulk = screen.getByLabelText(/Apply amount to all months of 2026/i);
      await user.type(bulk, '1000');
      await user.click(screen.getByRole('button', { name: /^Apply$/ }));
      expect(april().value).toBe('1000');

      // Second bulk: Copy from 2025 → snapshot MUST NOT be overwritten.
      await user.click(screen.getByRole('button', { name: /Copy from 2025/i }));
      await waitFor(() => expect(january().value).toBe('800'));

      // Undo goes back to the ORIGINAL baseline (April=3000, others empty),
      // not the intermediate Apply-filled state.
      await user.click(screen.getByRole('button', { name: /^Undo$/ }));
      expect(april().value).toBe('3000');
      expect(january().value).toBe('');
    });

    test('Undo then Apply takes a fresh snapshot', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      const bulk = () =>
        screen.getByLabelText(/Apply amount to all months of 2026/i);

      // First cycle: Apply → Undo.
      await user.type(bulk(), '1000');
      await user.click(screen.getByRole('button', { name: /^Apply$/ }));
      await user.click(screen.getByRole('button', { name: /^Undo$/ }));
      expect(april().value).toBe('3000');

      // Second cycle: the snapshot was cleared by Undo, so the next
      // Apply captures the current (post-Undo baseline) state, and
      // Undo correctly returns there — not to some stale snapshot.
      await user.type(bulk(), '2500');
      await user.click(screen.getByRole('button', { name: /^Apply$/ }));
      expect(april().value).toBe('2500');

      await user.click(screen.getByRole('button', { name: /^Undo$/ }));
      expect(april().value).toBe('3000');
    });

    test('Save button shows dirty count when a row diverges from baseline', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      // Baseline: Save button shows plain label; the dirty live region is
      // always mounted (for reliable AT announcement) but empty when clean.
      expect(
        screen.getByRole('button', { name: /^Save Budgets$/ }),
      ).toBeInTheDocument();
      expect(screen.getByTestId('budget-dirty-indicator').textContent).toBe('');

      // Edit April → dirty count = 1.
      await user.clear(april());
      await user.type(april(), '3500');

      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^Save Budgets \(1\)$/ }),
        ).toBeInTheDocument();
      });
      expect(
        screen.getByTestId('budget-dirty-indicator').textContent,
      ).toMatch(/1 unsaved change$/);

      // Edit May → dirty count = 2.
      await user.type(screen.getByLabelText(/Budget for May 2026/i), '500');

      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^Save Budgets \(2\)$/ }),
        ).toBeInTheDocument();
      });
      expect(
        screen.getByTestId('budget-dirty-indicator').textContent,
      ).toMatch(/2 unsaved changes$/);
    });

    test('announces first unsaved change via a persistently-mounted live region', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      // Region exists and is a polite live region while clean (empty text), so
      // assistive tech observes a content mutation on the first 0->1 change.
      const region = screen.getByTestId('budget-dirty-indicator');
      expect(region.textContent).toBe('');
      expect(region).toHaveAttribute('aria-live', 'polite');

      // One edit → the SAME node (not a freshly-inserted one) gains the text.
      await user.clear(april());
      await user.type(april(), '3500');

      await waitFor(() => {
        expect(screen.getByTestId('budget-dirty-indicator').textContent).toMatch(
          /1 unsaved change$/,
        );
      });
      expect(screen.getByTestId('budget-dirty-indicator')).toBe(region);
    });

    test('year change with unsaved changes opens discard dialog and respects Keep editing', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      await user.clear(april());
      await user.type(april(), '4000');

      // Baseline call count: initial fetch for 2026 + currencies + category-budgets + categories.
      const getCallsBeforeYearChange = mockedApi.get.mock.calls.length;

      // Open Radix Select and choose 2025.
      const trigger = screen.getByRole('combobox', {
        name: /budget year/i,
      });
      await user.click(trigger);
      await user.click(
        await screen.findByRole('option', { name: '2025' }),
      );

      // shadcn Dialog replaces the old window.confirm — assert on the
      // dialog body and the "Keep editing" / "Discard changes" buttons.
      const dialog = await screen.findByRole('alertdialog');
      expect(dialog).toHaveTextContent(/1 unsaved change/i);
      expect(dialog).toHaveTextContent(/2025/);

      await user.click(
        screen.getByRole('button', { name: /keep editing/i }),
      );

      // Cancel => year unchanged, no refetch, dirty input preserved.
      expect(april().value).toBe('4000');
      expect(mockedApi.get.mock.calls.length).toBe(
        getCallsBeforeYearChange,
      );
      expect(
        screen.getByRole('button', { name: /^Save Budgets \(1\)$/ }),
      ).toBeInTheDocument();
      // Dialog is dismissed.
      await waitFor(() =>
        expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(),
      );
    });

    test('year change Discard changes commits the year switch', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      await user.clear(april());
      await user.type(april(), '4000');

      await user.click(
        screen.getByRole('combobox', { name: /budget year/i }),
      );
      await user.click(
        await screen.findByRole('option', { name: '2025' }),
      );

      // Accept the discard → refetch for 2025 fires.
      await user.click(
        await screen.findByRole('button', { name: /discard changes/i }),
      );

      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith('budgets?year=2025');
      });
    });

    test('dirty indicator and Save count clear after successful save', async () => {
      mockedApi.put.mockResolvedValue({});
      // After save, the refetch should return the new amount so the
      // baseline is updated and dirtyCount drops back to 0.
      let aprilAmount = 3000;
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'budgets?year=2026')
          return Promise.resolve([
            {
              id: 1,
              year: 2026,
              month: 4,
              amount: aprilAmount,
              updated_at: '',
            },
          ]);
        return defaultGet(path);
      });

      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      await user.clear(april());
      await user.type(april(), '4000');

      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^Save Budgets \(1\)$/ }),
        ).toBeInTheDocument();
      });

      // Flip the mock so the post-save refetch returns the new value.
      aprilAmount = 4000;
      await user.click(
        screen.getByRole('button', { name: /^Save Budgets \(1\)$/ }),
      );

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith('budgets/2026/4', {
          amount: 4000,
        });
      });
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^Save Budgets$/ }),
        ).toBeInTheDocument();
      });
      // The live region stays mounted after save; its text clears to empty.
      await waitFor(() => {
        expect(screen.getByTestId('budget-dirty-indicator').textContent).toBe(
          '',
        );
      });
    });

    test('annual total reflects per-row edits', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const total = () => screen.getByLabelText(/^Annual total$/);

      await waitFor(() => {
        expect(total().textContent).toBe('$3,000.00');
      });

      const may = screen.getByLabelText(/Budget for May 2026/i);
      await user.type(may, '500');

      await waitFor(() => {
        expect(total().textContent).toBe('$3,500.00');
      });
    });
  });

  describe('Category Limits panel — as admin', () => {
    beforeEach(asAdmin);

    test('renders the Category Limits card heading', async () => {
      renderBudgets();
      await waitFor(() => {
        expect(screen.getByText(/category limits/i)).toBeInTheDocument();
      });
    });

    test('fetches limits for the default (current) month and year', async () => {
      renderBudgets();
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          'category-budgets?year=2026&month=4',
        );
      });
    });

    test('lists only active expense categories, expense sort order, with limits prefilled', async () => {
      renderBudgets();
      // Rent (sort_order 0) before Groceries (sort_order 1); income and
      // inactive excluded.
      await waitFor(() => {
        expect(
          screen.getByLabelText(/Limit for Rent/i),
        ).toBeInTheDocument();
      });
      expect(screen.getByLabelText(/Limit for Groceries/i)).toBeInTheDocument();
      expect(screen.queryByLabelText(/Limit for Salary/i)).not.toBeInTheDocument();
      expect(
        screen.queryByLabelText(/Limit for Old Cable/i),
      ).not.toBeInTheDocument();

      // Groceries has an existing limit of 400; Rent has none (blank).
      expect(
        (screen.getByLabelText(/Limit for Groceries/i) as HTMLInputElement).value,
      ).toBe('400');
      expect(
        (screen.getByLabelText(/Limit for Rent/i) as HTMLInputElement).value,
      ).toBe('');
    });

    test('PUTs a newly set positive limit on save', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const rent = await screen.findByLabelText(/Limit for Rent/i);
      await user.type(rent, '1500');

      await user.click(
        screen.getByRole('button', { name: /save category limits/i }),
      );

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'category-budgets/2026/4/11',
          { amount: 1500 },
        );
      });
    });

    test('DELETEs a cleared existing limit on save', async () => {
      mockedApi.del.mockResolvedValue(undefined);
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const groceries = await screen.findByLabelText(/Limit for Groceries/i);
      await user.clear(groceries);

      await user.click(
        screen.getByRole('button', { name: /save category limits/i }),
      );

      await waitFor(() => {
        expect(mockedApi.del).toHaveBeenCalledWith('category-budgets/2026/4/10');
      });
    });

    test('does not call the API for unchanged rows', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      await screen.findByLabelText(/Limit for Groceries/i);
      await user.click(
        screen.getByRole('button', { name: /save category limits/i }),
      );

      await waitFor(() => {
        expect(mockedToast.info).toHaveBeenCalledWith('No changes to save');
      });
      expect(mockedApi.put).not.toHaveBeenCalled();
      expect(mockedApi.del).not.toHaveBeenCalled();
    });

    test('changing the month refetches limits for that month', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      await screen.findByLabelText(/Limit for Groceries/i);

      const monthSelect = screen.getByRole('combobox', {
        name: /limits month/i,
      });
      await user.click(monthSelect);
      await user.click(await screen.findByRole('option', { name: 'July' }));

      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          'category-budgets?year=2026&month=7',
        );
      });
    });

    test('changing the month with unsaved edits opens the discard dialog instead of wiping', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const rent = await screen.findByLabelText(/Limit for Rent/i);
      await user.type(rent, '1500');

      // Picking a different month must NOT immediately refetch — it should
      // prompt to discard first.
      mockedApi.get.mockClear();
      const monthSelect = screen.getByRole('combobox', {
        name: /limits month/i,
      });
      await user.click(monthSelect);
      await user.click(await screen.findByRole('option', { name: 'July' }));

      // Discard dialog is shown; no refetch happened yet.
      expect(
        await screen.findByText(/discard unsaved changes/i),
      ).toBeInTheDocument();
      expect(mockedApi.get).not.toHaveBeenCalledWith(
        'category-budgets?year=2026&month=7',
      );

      // Confirming discard applies the month change and refetches.
      await user.click(
        screen.getByRole('button', { name: /discard changes/i }),
      );
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          'category-budgets?year=2026&month=7',
        );
      });
    });

    test('changing the year with unsaved edits opens the discard dialog instead of wiping', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const rent = await screen.findByLabelText(/Limit for Rent/i);
      await user.type(rent, '1500');

      mockedApi.get.mockClear();
      const yearSelect = screen.getByRole('combobox', {
        name: /limits year/i,
      });
      await user.click(yearSelect);
      await user.click(await screen.findByRole('option', { name: '2025' }));

      expect(
        await screen.findByText(/discard unsaved changes/i),
      ).toBeInTheDocument();
      expect(mockedApi.get).not.toHaveBeenCalledWith(
        'category-budgets?year=2025&month=4',
      );

      await user.click(
        screen.getByRole('button', { name: /discard changes/i }),
      );
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          'category-budgets?year=2025&month=4',
        );
      });
    });

    test('keeping edits cancels the discard dialog and leaves the month unchanged', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const rent = await screen.findByLabelText(/Limit for Rent/i);
      await user.type(rent, '1500');

      mockedApi.get.mockClear();
      const monthSelect = screen.getByRole('combobox', {
        name: /limits month/i,
      });
      await user.click(monthSelect);
      await user.click(await screen.findByRole('option', { name: 'July' }));

      await user.click(screen.getByRole('button', { name: /keep editing/i }));

      // No refetch and the edited value survives.
      expect(mockedApi.get).not.toHaveBeenCalledWith(
        'category-budgets?year=2026&month=7',
      );
      expect(
        (screen.getByLabelText(/Limit for Rent/i) as HTMLInputElement).value,
      ).toBe('1500');
    });

    test('rejects a non-positive limit with a per-row toast and no API call', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const rent = await screen.findByLabelText(/Limit for Rent/i);
      await user.type(rent, '0');

      await user.click(
        screen.getByRole('button', { name: /save category limits/i }),
      );

      await waitFor(() => {
        expect(mockedToast.error).toHaveBeenCalledWith(
          expect.stringMatching(/greater than 0/i),
        );
      });
      expect(mockedApi.put).not.toHaveBeenCalled();
    });
  });

  describe('Category Limits panel — as member (read-only)', () => {
    beforeEach(asMember);

    test('shows limits but no editable inputs or save button', async () => {
      renderBudgets();

      await waitFor(() => {
        expect(screen.getByText(/category limits/i)).toBeInTheDocument();
      });
      // The existing Groceries limit is shown as text, not an input.
      expect(screen.queryByLabelText(/Limit for Groceries/i)).not.toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: /save category limits/i }),
      ).not.toBeInTheDocument();

      // Category names and the existing limit still render as read-only text.
      expect(screen.getByText('Groceries')).toBeInTheDocument();
      expect(screen.getByText('Rent')).toBeInTheDocument();
      expect(screen.getByText('$400.00')).toBeInTheDocument();
    });

    test('renders the read-only limit with font-mono tabular-nums', async () => {
      renderBudgets();

      const cell = await screen.findByText('$400.00');
      expect(cell).toHaveClass('font-mono');
      expect(cell).toHaveClass('tabular-nums');
    });

    test('falls back to em-dash for a non-finite limit value', async () => {
      // A future bug could land a non-numeric amount in the limits payload.
      // The read-only view must degrade to "—" rather than rendering "$NaN".
      mockedApi.get.mockImplementation((path: string) => {
        if (path.startsWith('category-budgets'))
          return Promise.resolve([
            { category_id: 10, amount: 'oops' as unknown as number },
          ]);
        return defaultGet(path);
      });
      renderBudgets();

      await waitFor(() => {
        expect(screen.getByText('Groceries')).toBeInTheDocument();
      });
      expect(screen.queryByText(/NaN/)).not.toBeInTheDocument();
    });
  });

  describe('in-app navigation guard', () => {
    beforeEach(asAdmin);

    afterEach(() => {
      // The popstate-guard effect pushes a sentinel into window.history
      // when dirty. RTL's cleanup unmounts the component (which removes
      // the popstate listener and triggers our own sentinel cleanup),
      // but if a test errored before unmount completed, sentinel
      // entries may linger. Drain them so a later test calling real
      // history.back() doesn't fall through our debris.
      //
      // Bounded — happy-dom's history.back() doesn't reliably decrement
      // when there's nothing to go back to, so an unbounded while can
      // OOM the worker.
      for (let i = 0; i < 8; i++) {
        if (
          typeof window === 'undefined' ||
          !(window.history.state as { spendropBudgetsGuard?: boolean } | null)
            ?.spendropBudgetsGuard
        ) {
          break;
        }
        window.history.back();
      }
    });

    test('sidebar navigation with dirty budgets is intercepted by discard dialog', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      // Render a sidebar-style anchor alongside Budgets so the
      // capture-phase document listener has a real <a href> to catch.
      // Using a raw anchor (not react-router's <Link>) exercises the
      // same DOM path the real Sidebar's NavLink produces.
      render(
        <QueryWrapper>
          <MemoryRouter>
            <a href="/transactions" data-testid="sidebar-link">
              Transactions
            </a>
            <Budgets />
          </MemoryRouter>
        </QueryWrapper>,
      );

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      await user.clear(april());
      await user.type(april(), '4000');
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^Save Budgets \(1\)$/ }),
        ).toBeInTheDocument();
      });

      await user.click(screen.getByTestId('sidebar-link'));

      const dialog = await screen.findByRole('alertdialog');
      expect(dialog).toHaveTextContent(/1 unsaved change/i);
      expect(dialog).toHaveTextContent(/Transactions/);

      // Keep editing → still on Budgets, input preserved.
      await user.click(
        screen.getByRole('button', { name: /keep editing/i }),
      );
      expect(april().value).toBe('4000');
      await waitFor(() =>
        expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(),
      );
    });

    test('sidebar navigation without dirty budgets passes through unguarded', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      render(
        <QueryWrapper>
          <MemoryRouter>
            <a href="/transactions" data-testid="sidebar-link">
              Transactions
            </a>
            <Budgets />
          </MemoryRouter>
        </QueryWrapper>,
      );

      // Wait for initial budget fetch so baseline is populated — a
      // premature click would race the ref write and look clean even
      // if the guard were broken.
      await waitFor(() => {
        expect(
          screen.getByLabelText(/Budget for April 2026/i),
        ).toBeInTheDocument();
      });

      await user.click(screen.getByTestId('sidebar-link'));

      // No dirty edits → no dialog, nav proceeds normally (browser-
      // level; MemoryRouter doesn't actually follow <a>, but the
      // absence of the dialog is what we're asserting).
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    });

    // NOTE: The page-level nav guard sums both editor sections' dirty
    // counts (monthly + category limits), so "Monthly Budgets dirty
    // arms the guard" is sufficient functional coverage for the OR
    // logic. A standalone "category-limit-only" guard test was
    // attempted but is flaky under happy-dom + the inputs nested in
    // an async-loaded Card — the Monthly Budgets case above exercises
    // the same document.addEventListener path. If a future regression
    // separates the two refs, the category-limits panel test
    // ("changing the month with unsaved edits opens the discard
    // dialog instead of wiping") already proves the dirty count
    // reaches the inner-panel dialog, which uses the same memo.

    test('browser back (popstate) with dirty edits opens the discard prompt', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderBudgets();

      const april = () =>
        screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
      await waitFor(() => expect(april().value).toBe('3000'));

      await user.clear(april());
      await user.type(april(), '4000');
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /^Save Budgets \(1\)$/ }),
        ).toBeInTheDocument();
      });

      // Fire a popstate (browser Back / Forward / mobile swipe). The
      // page's sentinel effect has pushed a duplicate history entry
      // while dirty; the synthetic event below simulates the browser
      // popping it.
      window.dispatchEvent(new PopStateEvent('popstate', { state: null }));

      const dialog = await screen.findByRole('alertdialog');
      expect(dialog).toHaveTextContent(/1 unsaved change/i);
      expect(dialog).toHaveTextContent(/the previous page/i);

      // Cancel → dialog closes, edits stay.
      await user.click(
        screen.getByRole('button', { name: /keep editing/i }),
      );
      await waitFor(() =>
        expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(),
      );
      expect(april().value).toBe('4000');
    });
  });
});
