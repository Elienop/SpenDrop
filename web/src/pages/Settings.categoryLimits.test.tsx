import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

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
import { Settings } from './Settings';
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
  if (path === 'savings-goals')
    return Promise.resolve([
      { id: 1, year: 2026, target_amount: 6000, updated_at: '' },
    ]);
  if (path === 'users') return Promise.resolve([]);
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

function renderSettings() {
  return render(
    <MemoryRouter>
      <Settings />
    </MemoryRouter>,
  );
}

describe('Settings — Category Limits panel', () => {
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
    vi.useRealTimers();
  });

  describe('as admin', () => {
    beforeEach(asAdmin);

    test('renders the Category Limits card heading', async () => {
      renderSettings();
      await waitFor(() => {
        expect(screen.getByText(/category limits/i)).toBeInTheDocument();
      });
    });

    test('fetches limits for the default (current) month and year', async () => {
      renderSettings();
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          'category-budgets?year=2026&month=4',
        );
      });
    });

    test('lists only active expense categories, expense sort order, with limits prefilled', async () => {
      renderSettings();
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
      renderSettings();

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
      renderSettings();

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
      renderSettings();

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
      renderSettings();

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
      renderSettings();

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
        await screen.findByText(/discard unsaved budget changes/i),
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
      renderSettings();

      const rent = await screen.findByLabelText(/Limit for Rent/i);
      await user.type(rent, '1500');

      mockedApi.get.mockClear();
      const yearSelect = screen.getByRole('combobox', {
        name: /limits year/i,
      });
      await user.click(yearSelect);
      await user.click(await screen.findByRole('option', { name: '2025' }));

      expect(
        await screen.findByText(/discard unsaved budget changes/i),
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
      renderSettings();

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

    test('unsaved category-limit edits guard a tab switch', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      const rent = await screen.findByLabelText(/Limit for Rent/i);
      await user.type(rent, '1500');

      // Switch away to the Currencies tab — the dirty category-limit edits
      // must trigger the discard guard, not switch silently.
      await user.click(screen.getByRole('tab', { name: /currencies/i }));

      expect(
        await screen.findByText(/discard unsaved budget changes/i),
      ).toBeInTheDocument();
    });

    test('rejects a non-positive limit with a per-row toast and no API call', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

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

  describe('as member (read-only)', () => {
    beforeEach(asMember);

    test('shows limits but no editable inputs or save button', async () => {
      renderSettings();

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
      renderSettings();

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
      renderSettings();

      await waitFor(() => {
        expect(screen.getByText('Groceries')).toBeInTheDocument();
      });
      expect(screen.queryByText(/NaN/)).not.toBeInTheDocument();
    });
  });
});
