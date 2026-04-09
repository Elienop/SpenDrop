import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';

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
  category_color: '#e94560',
  tags: 'food,weekly',
  notes: null,
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
};

const mockUseTransactions = vi.fn();

// Mock the hooks and api
vi.mock('../api/client', () => ({
  api: {
    get: vi.fn().mockResolvedValue([]),
  },
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
    loading: false,
    saveFilter: mockSaveFilter,
    deleteFilter: mockDeleteFilter,
    refetch: vi.fn(),
  }),
}));

import { Transactions } from './Transactions';

function defaultHookReturn(overrides = {}) {
  return {
    transactions: [defaultTransaction],
    total: 1,
    page: 1,
    perPage: 20,
    filters: { ...defaultFilters },
    setFilter: mockSetFilter,
    clearFilters: mockClearFilters,
    clearPanelFilters: mockClearPanelFilters,
    setPage: vi.fn(),
    loading: false,
    error: '',
    createTransaction: vi.fn(),
    updateTransaction: vi.fn(),
    deleteTransaction: vi.fn(),
    ...overrides,
  };
}

describe('Transactions page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseTransactions.mockReturnValue(defaultHookReturn());
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

    it('clicking Filters button toggles filter panel visibility', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      // Panel not visible initially
      expect(screen.queryByRole('button', { name: 'This Month' })).not.toBeInTheDocument();

      // Open panel
      await user.click(screen.getByRole('button', { name: 'Filters' }));
      expect(screen.getByRole('button', { name: 'This Month' })).toBeInTheDocument();

      // Close panel
      await user.click(screen.getByRole('button', { name: 'Filters' }));
      expect(screen.queryByRole('button', { name: 'This Month' })).not.toBeInTheDocument();
    });

    it('clicking + Add button toggles entry form and changes label to Cancel', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      // Click + Add
      await user.click(screen.getByRole('button', { name: '+ Add' }));
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: '+ Add' })).not.toBeInTheDocument();

      // Click Cancel
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
      expect(screen.getByRole('button', { name: /Filters \(2\)/ })).toBeInTheDocument();
    });

    it('Filters button has aria-expanded attribute', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      const filtersBtn = screen.getByRole('button', { name: 'Filters' });
      expect(filtersBtn).toHaveAttribute('aria-expanded', 'false');

      await user.click(filtersBtn);
      expect(filtersBtn).toHaveAttribute('aria-expanded', 'true');
    });

    it('+ Add button has aria-expanded attribute', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      const addBtn = screen.getByRole('button', { name: '+ Add' });
      expect(addBtn).toHaveAttribute('aria-expanded', 'false');

      await user.click(addBtn);
      expect(screen.getByRole('button', { name: 'Cancel' })).toHaveAttribute(
        'aria-expanded',
        'true',
      );
    });
  });

  describe('filter panel tabs', () => {
    it('switches between Date, Category, Amount, and Saved tabs', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      // Open filter panel
      await user.click(screen.getByRole('button', { name: 'Filters' }));

      // Date tab is default — shows preset buttons
      expect(screen.getByRole('button', { name: 'This Month' })).toBeInTheDocument();

      // Switch to Category tab
      await user.click(screen.getByRole('button', { name: 'Category' }));
      expect(screen.getByLabelText('Filter by tags')).toBeInTheDocument();

      // Switch to Amount tab
      await user.click(screen.getByRole('button', { name: 'Amount' }));
      expect(screen.getByLabelText('Minimum amount')).toBeInTheDocument();

      // Switch to Saved tab
      await user.click(screen.getByRole('button', { name: 'Saved' }));
      expect(screen.getByRole('button', { name: 'Big expenses' })).toBeInTheDocument();
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
      expect(screen.getByText('$50 - $200')).toBeInTheDocument();
    });

    it('hides chips when filter panel is open', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, amountMin: '50' },
        }),
      );
      render(<Transactions />);
      expect(screen.getByText('Min $50')).toBeInTheDocument();

      // Open panel — chips should hide
      await user.click(screen.getByRole('button', { name: /Filters/ }));
      expect(screen.queryByText('Min $50')).not.toBeInTheDocument();
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
    it('renders saved filter chips in the Saved tab', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      // Open panel, go to Saved tab
      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));

      expect(screen.getByRole('button', { name: 'Big expenses' })).toBeInTheDocument();
    });

    it('renders a save filter button in the Saved tab', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));

      expect(screen.getByRole('button', { name: /save filter/i })).toBeInTheDocument();
    });

    it('clicking a saved filter chip loads its filters via setFilter', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));
      await user.click(screen.getByRole('button', { name: 'Big expenses' }));

      expect(mockSetFilter).toHaveBeenCalledWith('amountMin', '100');
      expect(mockSetFilter).toHaveBeenCalledWith('amountMax', '500');
    });

    it('calls saveFilter hook with name and current filter JSON on save', async () => {
      const user = userEvent.setup();
      const originalPrompt = window.prompt;
      window.prompt = vi.fn().mockReturnValue('My filter');
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));
      await user.click(screen.getByRole('button', { name: /save filter/i }));

      expect(mockSaveFilter).toHaveBeenCalledWith('My filter', expect.any(String));
      window.prompt = originalPrompt;
    });

    it('calls deleteFilter hook when delete button is clicked', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));

      const deleteBtn = screen.getByRole('button', { name: /delete saved filter/i });
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
      const url = new URL(openSpy.mock.calls[0][0] as string, 'http://localhost');
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

      const url = new URL(openSpy.mock.calls[0][0] as string, 'http://localhost');
      expect(url.searchParams.get('category_id')).toBe('5');
      expect(url.searchParams.has('category_ids')).toBe(false);
      openSpy.mockRestore();
    });
  });

  describe('tags column', () => {
    it('renders a Tags column header in the table', () => {
      render(<Transactions />);
      const headers = screen.getAllByRole('columnheader');
      const tagsHeader = headers.find((h) => h.textContent === 'Tags');
      expect(tagsHeader).toBeDefined();
    });
  });
});
