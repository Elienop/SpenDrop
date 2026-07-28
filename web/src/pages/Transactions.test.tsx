import {
  render as rtlRender,
  screen,
  waitFor,
  within,
  type RenderOptions,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement, type ReactElement, type ReactNode } from 'react';

// Each render gets a fresh QueryClient so the migrated useQuery hooks this page
// renders through (`useBaseCurrency` → `useCurrencies`, and `useSuggestions`)
// have a provider and an isolated cache. `retry: false` keeps rejected fetches
// from re-firing.
function render(ui: ReactElement, options?: RenderOptions) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return rtlRender(ui, { wrapper, ...options });
}

const mockSaveFilter = vi.fn();
const mockDeleteFilter = vi.fn();
const mockSetFilter = vi.fn();
const mockClearFilters = vi.fn();
const mockClearPanelFilters = vi.fn();

const defaultFilters = {
  dateFrom: '',
  dateTo: '',
  categoryId: '',
  categoryIds: '',
  amountMin: '',
  amountMax: '',
  tags: '',
  type: '',
  search: '',
};

const defaultTransaction = {
  id: 1,
  user_id: 1,
  date: '2026-04-01',
  amount: 25.5,
  original_amount: null,
  original_currency: null,
  description: 'Groceries',
  category_id: 1,
  category_name: 'Groceries',
  category_type: 'expense',
  tags: 'food,weekly',
  notes: null,
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
};

const mockUseTransactions = vi.fn();
const mockGetReadyRegistration = vi.fn();

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn().mockResolvedValue([]),
    post: vi.fn().mockResolvedValue({}),
    put: vi.fn().mockResolvedValue({}),
    del: vi.fn().mockResolvedValue({}),
  },
}));

vi.mock('@/lib/push-sw', () => ({
  getReadyRegistration: () => mockGetReadyRegistration(),
}));

vi.mock('../hooks/useTransactions', () => ({
  useTransactions: (...args: unknown[]) => mockUseTransactions(...args),
}));

vi.mock('../hooks/useSavedFilters', () => ({
  useSavedFilters: () => ({
    savedFilters: [
      {
        id: 10,
        user_id: 1,
        name: 'Big expenses',
        filter_json: '{"amountMin":"100","amountMax":"500"}',
        created_at: '',
        updated_at: '',
      },
    ],
    initialLoad: false,
    saveFilter: mockSaveFilter,
    deleteFilter: mockDeleteFilter,
    refetch: vi.fn(),
  }),
}));

import { Transactions } from './Transactions';

const mockSetPage = vi.fn();
const mockSetPerPage = vi.fn();
const mockSetSort = vi.fn();

function defaultHookReturn(overrides = {}) {
  return {
    transactions: [defaultTransaction],
    total: 1,
    page: 1,
    perPage: 20,
    sortBy: 'date' as const,
    sortDir: 'desc' as const,
    filters: { ...defaultFilters },
    setFilter: mockSetFilter,
    clearFilters: mockClearFilters,
    clearPanelFilters: mockClearPanelFilters,
    setPage: mockSetPage,
    setPerPage: mockSetPerPage,
    setSort: mockSetSort,
    initialLoad: false,
    error: '',
    createTransaction: vi.fn(),
    updateTransaction: vi.fn(),
    deleteTransaction: vi.fn(),
    deleteByFilter: vi.fn().mockResolvedValue(0),
    bulkUpdate: vi
      .fn()
      .mockResolvedValue({ updated: 0, skipped: 0, visibleIds: [] }),
    bulkUpdateByFilter: vi.fn().mockResolvedValue({ updated: 0 }),
    buildFilterQuery: vi.fn().mockReturnValue(''),
    refetch: vi.fn(),
    ...overrides,
  };
}

describe('Transactions page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseTransactions.mockReturnValue(defaultHookReturn());
    // Default: no service worker registration, so the mount effect's
    // notification-cleanup short-circuits to a no-op in every other test.
    mockGetReadyRegistration.mockResolvedValue(null);
  });

  describe('toolbar', () => {
    it('renders search input, type toggle, Filters button, and + Add button', () => {
      render(<Transactions />);
      expect(screen.getByLabelText('Search transactions')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'All' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Expenses' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Income' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Filters' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: '+ Add' })).toBeInTheDocument();
    });

    it('clicking Filters button opens the filter Sheet and shows Date tab content', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      expect(
        screen.queryByRole('button', { name: 'This Month' }),
      ).not.toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      expect(
        screen.getByRole('button', { name: 'This Month' }),
      ).toBeInTheDocument();
    });

    it('clicking + Add button toggles entry form and changes label to Cancel', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: '+ Add' }));
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: '+ Add' }),
      ).not.toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'Cancel' }));
      expect(screen.getByRole('button', { name: '+ Add' })).toBeInTheDocument();
    });

    it('Filters button shows count when filters are active', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, dateFrom: '2026-01-01', amountMin: '50' },
        }),
      );
      render(<Transactions />);
      expect(
        screen.getByRole('button', { name: /Filters.*2/ }),
      ).toBeInTheDocument();
    });

    it('Filters button has aria-expanded attribute that reflects Sheet state', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      const filtersBtn = screen.getByRole('button', { name: 'Filters' });
      expect(filtersBtn).toHaveAttribute('aria-expanded', 'false');

      await user.click(filtersBtn);
      expect(filtersBtn).toHaveAttribute('aria-expanded', 'true');
    });
  });

  describe('filter panel tabs', () => {
    it('switches between Date, Category, Amount, and Saved tabs', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));

      // Date tab is default — preset button visible
      expect(
        screen.getByRole('button', { name: 'This Month' }),
      ).toBeInTheDocument();

      // shadcn Tabs uses role="tab" for triggers
      await user.click(screen.getByRole('tab', { name: 'Category' }));
      expect(screen.getByLabelText('Filter by tags')).toBeInTheDocument();

      await user.click(screen.getByRole('tab', { name: 'Amount' }));
      expect(screen.getByLabelText('Minimum amount')).toBeInTheDocument();

      await user.click(screen.getByRole('tab', { name: 'Saved' }));
      expect(
        screen.getByRole('button', { name: 'Big expenses' }),
      ).toBeInTheDocument();
    });
  });

  describe('active filter chips', () => {
    it('shows chips when filters are set and panel is closed', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, amountMin: '50', amountMax: '200' },
        }),
      );
      render(<Transactions />);
      expect(screen.getByText('$50.00 - $200.00')).toBeInTheDocument();
    });

    it('hides chips when filter Sheet is open', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, amountMin: '50' },
        }),
      );
      render(<Transactions />);
      expect(screen.getByText('Min $50.00')).toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: /Filters/ }));
      expect(screen.queryByText('Min $50.00')).not.toBeInTheDocument();
    });

    it('clicking chip x clears that specific filter group', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, tags: 'groceries' },
        }),
      );
      render(<Transactions />);

      await user.click(screen.getByLabelText('Clear groceries filter'));
      expect(mockSetFilter).toHaveBeenCalledWith('tags', '');
    });
  });

  describe('saved filters integration', () => {
    async function openSavedTab() {
      const user = userEvent.setup();
      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('tab', { name: 'Saved' }));
      return user;
    }

    it('renders saved filter chips in the Saved tab', async () => {
      render(<Transactions />);
      await openSavedTab();
      expect(
        screen.getByRole('button', { name: 'Big expenses' }),
      ).toBeInTheDocument();
    });

    it('renders a save filter button in the Saved tab', async () => {
      render(<Transactions />);
      await openSavedTab();
      expect(
        screen.getByRole('button', { name: /save filter/i }),
      ).toBeInTheDocument();
    });

    it('clicking a saved filter chip loads its filters via setFilter', async () => {
      render(<Transactions />);
      const user = await openSavedTab();
      await user.click(screen.getByRole('button', { name: 'Big expenses' }));

      expect(mockSetFilter).toHaveBeenCalledWith('amountMin', '100');
      expect(mockSetFilter).toHaveBeenCalledWith('amountMax', '500');
    });

    it('calls saveFilter hook with name and current filter JSON on save', async () => {
      const originalPrompt = window.prompt;
      window.prompt = vi.fn().mockReturnValue('My filter');
      render(<Transactions />);
      const user = await openSavedTab();
      await user.click(screen.getByRole('button', { name: /save filter/i }));

      expect(mockSaveFilter).toHaveBeenCalledWith('My filter', expect.any(String));
      window.prompt = originalPrompt;
    });

    it('calls deleteFilter hook when delete button is clicked', async () => {
      render(<Transactions />);
      const user = await openSavedTab();

      const deleteBtn = screen.getByRole('button', {
        name: /delete saved filter/i,
      });
      await user.click(deleteBtn);
      expect(mockDeleteFilter).toHaveBeenCalledWith(10);
    });
  });

  describe('export button', () => {
    it('renders an Export Excel button', () => {
      render(<Transactions />);
      expect(
        screen.getByRole('button', { name: /export excel/i }),
      ).toBeInTheDocument();
    });

    it('opens export URL in new tab when clicked with no filters', async () => {
      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = openSpy.mock.calls[0][0] as string;
      expect(url).toBe('/api/export/transactions');
      expect(openSpy.mock.calls[0][1]).toBe('_blank');
      openSpy.mockRestore();
    });

    it('includes filter params in the export URL when filters are active', async () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: {
            dateFrom: '2026-01-01',
            dateTo: '2026-03-31',
            categoryId: '',
            categoryIds: '1,2',
            amountMin: '50',
            amountMax: '500',
            tags: 'food',
            type: 'expense',
            search: 'groceries',
          },
        }),
      );

      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = new URL(
        openSpy.mock.calls[0][0] as string,
        'http://localhost',
      );
      expect(url.pathname).toBe('/api/export/transactions');
      expect(url.searchParams.get('date_from')).toBe('2026-01-01');
      expect(url.searchParams.get('date_to')).toBe('2026-03-31');
      expect(url.searchParams.get('category_ids')).toBe('1,2');
      expect(url.searchParams.get('amount_min')).toBe('50');
      expect(url.searchParams.get('amount_max')).toBe('500');
      expect(url.searchParams.get('tags')).toBe('food');
      expect(url.searchParams.get('type')).toBe('expense');
      expect(url.searchParams.get('search')).toBe('groceries');
      openSpy.mockRestore();
    });

    it('uses categoryId when categoryIds is empty', async () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, categoryId: '5', categoryIds: '' },
        }),
      );

      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      const url = new URL(
        openSpy.mock.calls[0][0] as string,
        'http://localhost',
      );
      expect(url.searchParams.get('category_id')).toBe('5');
      expect(url.searchParams.has('category_ids')).toBe(false);
      openSpy.mockRestore();
    });
  });

  describe('tags column', () => {
    it('renders a Tags column header in the table', () => {
      render(<Transactions />);
      const headers = screen.getAllByRole('columnheader');
      const tagsHeader = headers.find((h) => h.textContent?.includes('Tags'));
      expect(tagsHeader).toBeDefined();
    });
  });

  describe('pagination bar', () => {
    it('renders rows per page select with options 10, 20, 50, 100', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ total: 100 }),
      );
      render(<Transactions />);
      // Should have "Rows per page" label
      const labels = screen.getAllByText('Rows per page');
      expect(labels.length).toBeGreaterThanOrEqual(1);
    });

    it('renders pagination bar both above and below the table', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ total: 100 }),
      );
      render(<Transactions />);
      // Both top and bottom should have "Rows per page" text
      const labels = screen.getAllByText('Rows per page');
      expect(labels).toHaveLength(2);
    });

    it('calls setPerPage when rows per page changes', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ total: 100 }),
      );
      render(<Transactions />);

      // Find the rows-per-page Select trigger by its accessible value.
      // Filter out the AutocompleteInput combobox (which is a real <input>);
      // the Select's trigger is rendered as a <button>.
      const triggers = screen
        .getAllByRole('combobox')
        .filter((el) => el.tagName === 'BUTTON');
      expect(triggers.length).toBeGreaterThanOrEqual(1);
      await user.click(triggers[0]);

      // Click the "50" option
      const option50 = screen.getByRole('option', { name: '50' });
      await user.click(option50);

      expect(mockSetPerPage).toHaveBeenCalledWith(50);
    });
  });

  describe('sortable headers', () => {
    it('renders sort dropdown trigger buttons for Date, Description, Category, Amount, Tags', () => {
      render(<Transactions />);
      const thead = screen.getAllByRole('rowgroup')[0];
      const headerScope = within(thead);
      expect(headerScope.getByRole('button', { name: /date/i })).toBeInTheDocument();
      expect(headerScope.getByRole('button', { name: /description/i })).toBeInTheDocument();
      expect(headerScope.getByRole('button', { name: /category/i })).toBeInTheDocument();
      expect(headerScope.getByRole('button', { name: /amount/i })).toBeInTheDocument();
      expect(headerScope.getByRole('button', { name: /tags/i })).toBeInTheDocument();
    });

    it('calls setSort with column name when header is clicked', async () => {
      const user = userEvent.setup();
      render(<Transactions />);
      const thead = screen.getAllByRole('rowgroup')[0];
      await user.click(within(thead).getByRole('button', { name: /amount/i }));
      expect(mockSetSort).toHaveBeenCalledWith('amount');
    });

  });

  describe('loading state', () => {
    it('renders Skeleton elements during initial load', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ initialLoad: true, transactions: [] }),
      );
      const { container } = render(<Transactions />);
      // Skeleton components have animate-pulse class
      const skeletons = container.querySelectorAll('.animate-pulse');
      expect(skeletons.length).toBeGreaterThan(0);
    });
  });

  describe('empty state', () => {
    it('renders empty state message when no transactions', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ transactions: [], total: 0 }),
      );
      render(<Transactions />);
      expect(screen.getByText(/no transactions found/i)).toBeInTheDocument();
    });
  });

  describe('selection state machine', () => {
    // Builds a small set of transactions on "page 1" with more rows
    // available beyond it, so the select-all-matching banner is reachable.
    function makeRows(n: number) {
      return Array.from({ length: n }, (_, i) => ({
        ...defaultTransaction,
        id: i + 1,
        description: `Row ${i + 1}`,
      }));
    }

    it('selecting one row shows "1 selected" action bar', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ transactions: makeRows(3), total: 3 }),
      );
      render(<Transactions />);

      const checkbox = screen.getByRole('checkbox', { name: /select row 1/i });
      await user.click(checkbox);

      expect(screen.getByText('1 selected')).toBeInTheDocument();
      // No "Select all X matching" banner when total equals page count.
      expect(
        screen.queryByRole('button', { name: /select all .* matching/i }),
      ).not.toBeInTheDocument();
    });

    it('selecting every visible row surfaces the "Select all X matching" banner when more rows exist', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ transactions: makeRows(2), total: 137 }),
      );
      render(<Transactions />);

      // Click both row checkboxes.
      await user.click(
        screen.getByRole('checkbox', { name: /select row 1/i }),
      );
      await user.click(
        screen.getByRole('checkbox', { name: /select row 2/i }),
      );

      expect(screen.getByText('2 selected')).toBeInTheDocument();
      expect(
        screen.getByRole('button', { name: /select all 137 matching/i }),
      ).toBeInTheDocument();
    });

    it('clicking "Select all X matching" switches to all-matching scope and locks row checkboxes', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ transactions: makeRows(2), total: 137 }),
      );
      render(<Transactions />);

      // Escalate to all-matching via header checkbox + banner.
      await user.click(
        screen.getByRole('checkbox', { name: /select row 1/i }),
      );
      await user.click(
        screen.getByRole('checkbox', { name: /select row 2/i }),
      );
      await user.click(
        screen.getByRole('button', { name: /select all 137 matching/i }),
      );

      expect(
        screen.getByText('All 137 matching transactions selected'),
      ).toBeInTheDocument();

      // Individual row checkboxes must be disabled — the only way out
      // of all-matching mode is the header checkbox or Clear selection.
      const row1 = screen.getByRole('checkbox', { name: /select row 1/i });
      expect(row1).toBeDisabled();
    });

    it('Delete in all-matching scope opens confirmation dialog instead of deleting immediately', async () => {
      const user = userEvent.setup();
      const deleteByFilter = vi.fn().mockResolvedValue(137);
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          transactions: makeRows(2),
          total: 137,
          deleteByFilter,
        }),
      );
      render(<Transactions />);

      await user.click(
        screen.getByRole('checkbox', { name: /select row 1/i }),
      );
      await user.click(
        screen.getByRole('checkbox', { name: /select row 2/i }),
      );
      await user.click(
        screen.getByRole('button', { name: /select all 137 matching/i }),
      );

      // Click Delete in the action bar.
      const actionBar = screen
        .getByText('All 137 matching transactions selected')
        .closest('div')!.parentElement!;
      await user.click(
        within(actionBar).getByRole('button', { name: /^Delete$/ }),
      );

      // Confirmation modal appears; delete has NOT been called yet.
      expect(
        screen.getByRole('dialog', {
          name: /delete all matching transactions/i,
        }),
      ).toBeInTheDocument();
      expect(deleteByFilter).not.toHaveBeenCalled();
    });

    it('confirming in the dialog triggers the filter-based delete', async () => {
      const user = userEvent.setup();
      const deleteByFilter = vi.fn().mockResolvedValue(137);
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          transactions: makeRows(2),
          total: 137,
          deleteByFilter,
        }),
      );
      render(<Transactions />);

      await user.click(
        screen.getByRole('checkbox', { name: /select row 1/i }),
      );
      await user.click(
        screen.getByRole('checkbox', { name: /select row 2/i }),
      );
      await user.click(
        screen.getByRole('button', { name: /select all 137 matching/i }),
      );
      // Open the action bar's Delete — opens the dialog.
      const actionBar = screen
        .getByText('All 137 matching transactions selected')
        .closest('div')!.parentElement!;
      await user.click(
        within(actionBar).getByRole('button', { name: /^Delete$/ }),
      );

      // Click the confirm button inside the dialog.
      const dialog = screen.getByRole('dialog', {
        name: /delete all matching transactions/i,
      });
      await user.click(
        within(dialog).getByRole('button', { name: /delete 137/i }),
      );

      expect(deleteByFilter).toHaveBeenCalledTimes(1);
    });

    it('Cancel in the dialog does not call deleteByFilter and leaves selection intact', async () => {
      const user = userEvent.setup();
      const deleteByFilter = vi.fn();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          transactions: makeRows(2),
          total: 137,
          deleteByFilter,
        }),
      );
      render(<Transactions />);

      await user.click(
        screen.getByRole('checkbox', { name: /select row 1/i }),
      );
      await user.click(
        screen.getByRole('checkbox', { name: /select row 2/i }),
      );
      await user.click(
        screen.getByRole('button', { name: /select all 137 matching/i }),
      );
      const actionBar = screen
        .getByText('All 137 matching transactions selected')
        .closest('div')!.parentElement!;
      await user.click(
        within(actionBar).getByRole('button', { name: /^Delete$/ }),
      );

      const dialog = screen.getByRole('dialog', {
        name: /delete all matching transactions/i,
      });
      await user.click(within(dialog).getByRole('button', { name: /cancel/i }));

      expect(deleteByFilter).not.toHaveBeenCalled();
      expect(
        screen.getByText('All 137 matching transactions selected'),
      ).toBeInTheDocument();
    });
  });

  describe('bulk edit integration', () => {
    function makeRows(n: number) {
      return Array.from({ length: n }, (_, i) => ({
        ...defaultTransaction,
        id: i + 1,
        description: `Row ${i + 1}`,
      }));
    }

    // Pick the first Select trigger inside the BulkEditDialog (the only
    // dialog mounted in these tests). Filtered to <button> because
    // AutocompleteInputs render real <input role="combobox"> elements that
    // can't be opened by clicking.
    function categoryTriggerInDialog() {
      const dialog = screen.getByRole('dialog', { name: /edit \d+ transactions/i });
      return within(dialog)
        .getAllByRole('combobox')
        .filter((el) => el.tagName === 'BUTTON')[0];
    }

    it('Edit button is absent when no row is selected', () => {
      render(<Transactions />);
      expect(
        screen.queryByRole('button', { name: /^edit \(/i }),
      ).not.toBeInTheDocument();
    });

    it('Edit button reads "Edit (N)" once rows are selected', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ transactions: makeRows(3), total: 3 }),
      );
      render(<Transactions />);

      await user.click(screen.getByRole('checkbox', { name: /select row 1/i }));
      await user.click(screen.getByRole('checkbox', { name: /select row 2/i }));

      expect(
        screen.getByRole('button', { name: /^edit \(2\)$/i }),
      ).toBeInTheDocument();
    });

    it('Clicking Edit opens BulkEditDialog with N=selectedCount', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({ transactions: makeRows(3), total: 3 }),
      );
      render(<Transactions />);

      await user.click(screen.getByRole('checkbox', { name: /select row 1/i }));
      await user.click(screen.getByRole('checkbox', { name: /select row 2/i }));
      await user.click(screen.getByRole('checkbox', { name: /select row 3/i }));

      await user.click(screen.getByRole('button', { name: /^edit \(3\)$/i }));

      expect(
        screen.getByRole('heading', { name: /edit 3 transactions/i }),
      ).toBeInTheDocument();
    });

    it('extra Apply clicks do not fire additional bulk updates', async () => {
      const user = userEvent.setup();
      // Never resolves — models a bulk PATCH still in flight.
      const bulkUpdate = vi.fn(() => new Promise(() => {}));
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          transactions: makeRows(3),
          total: 3,
          bulkUpdate,
          bulkUpdateByFilter: vi.fn(),
        }),
      );
      const { api } = await import('../api/client');
      (api.get as ReturnType<typeof vi.fn>).mockImplementation(
        (path: string) => {
          if (path === 'categories') {
            return Promise.resolve([
              {
                id: 5,
                name: 'Cleaning',
                type: 'expense',
                icon: null,
                sort_order: 0,
                is_active: true,
                created_at: '2026-01-01T00:00:00Z',
              },
            ]);
          }
          return Promise.resolve([]);
        },
      );
      render(<Transactions />);

      await user.click(screen.getByRole('checkbox', { name: /select row 1/i }));
      await user.click(screen.getByRole('checkbox', { name: /select row 2/i }));
      await user.click(screen.getByRole('button', { name: /^edit \(2\)$/i }));
      await waitFor(() => expect(api.get).toHaveBeenCalledWith('categories'));

      const trigger = categoryTriggerInDialog();
      await user.click(trigger);
      await user.click(await screen.findByRole('option', { name: /cleaning/i }));

      const dialog = screen.getByRole('dialog', { name: /edit 2 transactions/i });
      const apply = within(dialog).getByRole('button', { name: /apply.*to 2/i });

      await user.click(apply);
      await user.click(apply);
      await user.click(apply);

      // The dialog awaits onSubmit so RHF holds formState.isSubmitting for the
      // duration — but only if the page RETURNS the dispatch promise. With a
      // discarded `void dispatchBulkEdit(p)` the flag flips back before the
      // request leaves and every extra click fires another bulk PATCH.
      await waitFor(() => expect(bulkUpdate).toHaveBeenCalledTimes(1));
    });

    it('page-mode submit dispatches bulkUpdate, NOT bulkUpdateByFilter', async () => {
      const user = userEvent.setup();
      const bulkUpdate = vi
        .fn()
        .mockResolvedValue({ updated: 2, skipped: 0, visibleIds: [1, 2] });
      const bulkUpdateByFilter = vi.fn();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          transactions: makeRows(3),
          total: 3,
          bulkUpdate,
          bulkUpdateByFilter,
        }),
      );
      // Categories — needed for the dialog's Select to have options.
      const { api } = await import('../api/client');
      (api.get as ReturnType<typeof vi.fn>).mockImplementation(
        (path: string) => {
          if (path === 'categories') {
            return Promise.resolve([
              {
                id: 5,
                name: 'Cleaning',
                type: 'expense',
                icon: null,
                sort_order: 0,
                is_active: true,
                created_at: '2026-01-01T00:00:00Z',
              },
            ]);
          }
          return Promise.resolve([]);
        },
      );
      render(<Transactions />);

      await user.click(screen.getByRole('checkbox', { name: /select row 1/i }));
      await user.click(screen.getByRole('checkbox', { name: /select row 2/i }));
      await user.click(screen.getByRole('button', { name: /^edit \(2\)$/i }));

      // Wait for categories to load before opening Select.
      await waitFor(() =>
        expect(api.get).toHaveBeenCalledWith('categories'),
      );

      // Pick the dialog's Category trigger and choose Cleaning.
      const trigger = categoryTriggerInDialog();
      await user.click(trigger);
      const option = await screen.findByRole('option', { name: /cleaning/i });
      await user.click(option);

      // Click the Apply button in the dialog.
      const dialog = screen.getByRole('dialog', { name: /edit 2 transactions/i });
      await user.click(
        within(dialog).getByRole('button', { name: /apply.*to 2/i }),
      );

      await waitFor(() => expect(bulkUpdate).toHaveBeenCalledTimes(1));
      expect(bulkUpdate).toHaveBeenCalledWith(
        expect.objectContaining({
          ids: [1, 2],
          patch: expect.objectContaining({ category_id: 5 }),
        }),
      );
      expect(bulkUpdateByFilter).not.toHaveBeenCalled();
    });

    it('all-matching mode submit routes through confirm dialog before firing PATCH', async () => {
      const user = userEvent.setup();
      const bulkUpdate = vi.fn();
      const bulkUpdateByFilter = vi
        .fn()
        .mockResolvedValue({ updated: 137 });
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          transactions: makeRows(2),
          total: 137,
          bulkUpdate,
          bulkUpdateByFilter,
          buildFilterQuery: vi.fn().mockReturnValue('search=foo'),
        }),
      );
      const { api } = await import('../api/client');
      (api.get as ReturnType<typeof vi.fn>).mockImplementation(
        (path: string) => {
          if (path === 'categories') {
            return Promise.resolve([
              {
                id: 9,
                name: 'Cleaning',
                type: 'expense',
                icon: null,
                sort_order: 0,
                is_active: true,
                created_at: '2026-01-01T00:00:00Z',
              },
            ]);
          }
          return Promise.resolve([]);
        },
      );
      render(<Transactions />);

      // Escalate to all-matching scope.
      await user.click(screen.getByRole('checkbox', { name: /select row 1/i }));
      await user.click(screen.getByRole('checkbox', { name: /select row 2/i }));
      await user.click(
        screen.getByRole('button', { name: /select all 137 matching/i }),
      );

      // Open BulkEditDialog (count is 137 in all-matching scope).
      await user.click(screen.getByRole('button', { name: /^edit \(137\)$/i }));

      await waitFor(() => expect(api.get).toHaveBeenCalledWith('categories'));

      const trigger = categoryTriggerInDialog();
      await user.click(trigger);
      await user.click(await screen.findByRole('option', { name: /cleaning/i }));

      // Apply in the form-mode dialog should open the confirm AlertDialog,
      // NOT fire the PATCH.
      const editDialog = screen.getByRole('dialog', {
        name: /edit 137 transactions/i,
      });
      await user.click(
        within(editDialog).getByRole('button', { name: /apply.*to 137/i }),
      );

      const confirm = await screen.findByRole('alertdialog');
      expect(confirm).toBeInTheDocument();
      expect(bulkUpdateByFilter).not.toHaveBeenCalled();

      // Click Apply inside the confirm dialog.
      await user.click(
        within(confirm).getByRole('button', { name: /apply.*to 137/i }),
      );

      await waitFor(() =>
        expect(bulkUpdateByFilter).toHaveBeenCalledTimes(1),
      );
      expect(bulkUpdateByFilter).toHaveBeenCalledWith(
        expect.objectContaining({
          filterQuery: 'search=foo',
          patch: expect.objectContaining({ category_id: 9 }),
        }),
      );
    });

    it('successful page-mode submit prunes selectedIds to the refetched ID set', async () => {
      const user = userEvent.setup();
      // bulkUpdate returns visibleIds: [1] — rows 2 and 3 fell out of the
      // current filter after the patch and must be pruned from the
      // selection. After submit, the Edit button re-reads "Edit (1)".
      const bulkUpdate = vi
        .fn()
        .mockResolvedValue({ updated: 3, skipped: 0, visibleIds: [1] });
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          transactions: makeRows(3),
          total: 3,
          bulkUpdate,
        }),
      );
      const { api } = await import('../api/client');
      (api.get as ReturnType<typeof vi.fn>).mockImplementation(
        (path: string) => {
          if (path === 'categories') {
            return Promise.resolve([
              {
                id: 7,
                name: 'Cleaning',
                type: 'expense',
                icon: null,
                sort_order: 0,
                is_active: true,
                created_at: '2026-01-01T00:00:00Z',
              },
            ]);
          }
          return Promise.resolve([]);
        },
      );
      render(<Transactions />);

      await user.click(screen.getByRole('checkbox', { name: /select row 1/i }));
      await user.click(screen.getByRole('checkbox', { name: /select row 2/i }));
      await user.click(screen.getByRole('checkbox', { name: /select row 3/i }));
      await user.click(screen.getByRole('button', { name: /^edit \(3\)$/i }));

      await waitFor(() => expect(api.get).toHaveBeenCalledWith('categories'));

      const trigger = categoryTriggerInDialog();
      await user.click(trigger);
      await user.click(await screen.findByRole('option', { name: /cleaning/i }));

      const dialog = screen.getByRole('dialog', { name: /edit 3 transactions/i });
      await user.click(
        within(dialog).getByRole('button', { name: /apply.*to 3/i }),
      );

      await waitFor(() => expect(bulkUpdate).toHaveBeenCalledTimes(1));

      // After pruning, the Edit button should report 1 selected (only id=1
      // remains in visibleIds), or vanish if the action bar is hidden.
      await waitFor(() => {
        expect(
          screen.queryByRole('button', { name: /^edit \(3\)$/i }),
        ).not.toBeInTheDocument();
      });
      expect(
        screen.getByRole('button', { name: /^edit \(1\)$/i }),
      ).toBeInTheDocument();
    });
  });

  describe('app badge', () => {
    afterEach(() => {
      Reflect.deleteProperty(navigator, 'clearAppBadge');
    });

    it('clears the PWA app-icon badge on mount (Badging API available)', () => {
      const clearAppBadge = vi.fn(() => Promise.resolve());
      Object.defineProperty(navigator, 'clearAppBadge', {
        configurable: true,
        value: clearAppBadge,
      });

      render(<Transactions />);

      expect(clearAppBadge).toHaveBeenCalledTimes(1);
    });

    it('does not throw when the Badging API is unavailable', () => {
      // navigator.clearAppBadge intentionally absent (afterEach deletes it).
      expect(() => render(<Transactions />)).not.toThrow();
    });
  });

  describe('activity notification reset', () => {
    it('closes the showing activity notification on mount so the rollup baseline resets', async () => {
      const close = vi.fn();
      const getNotifications = vi.fn().mockResolvedValue([{ close }, { close }]);
      mockGetReadyRegistration.mockResolvedValue({ getNotifications });

      render(<Transactions />);

      await waitFor(() =>
        expect(getNotifications).toHaveBeenCalledWith({ tag: 'activity' }),
      );
      await waitFor(() => expect(close).toHaveBeenCalledTimes(2));
    });

    it('does not throw when no service worker registration is available', () => {
      mockGetReadyRegistration.mockResolvedValue(null);
      expect(() => render(<Transactions />)).not.toThrow();
    });

    it('skips cleanup when getNotifications is unsupported', () => {
      // Registration present but without the Notifications API surface.
      mockGetReadyRegistration.mockResolvedValue({});
      expect(() => render(<Transactions />)).not.toThrow();
    });
  });
});
