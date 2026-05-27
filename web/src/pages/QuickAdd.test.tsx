import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import type { Category, Currency, Transaction } from '../api/types';

// --- Mocks ----------------------------------------------------------------

// Force the fine-pointer (desktop) branch so AutocompleteInput / TagInput
// render their plain inputs without a Radix Popover — keeps queries simple
// and avoids the touch popover focus dance in jsdom.
vi.mock('@/hooks/useIsCoarsePointer', () => ({
  useIsCoarsePointer: () => false,
}));

// Capture sonner toast calls so we can assert the success toast + Undo
// action without rendering a real Toaster (which needs matchMedia in jsdom).
const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign(
    () => undefined,
    {
      success: (...args: unknown[]) => toastSuccess(...args),
      error: (...args: unknown[]) => toastError(...args),
    },
  ),
  Toaster: () => null,
}));

// QuickAdd renders the app's Toaster wrapper (`@/components/ui/sonner`),
// which consumes useTheme() and would otherwise require a ThemeProvider in
// the test tree. Stub it to a no-op — toast assertions target the mocked
// `sonner` `toast` above. (In production main.tsx provides the provider.)
vi.mock('@/components/ui/sonner', () => ({
  Toaster: () => null,
}));

const categories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '',
  },
  {
    id: 2,
    name: 'Salary',
    type: 'income',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '',
  },
  {
    id: 3,
    name: 'Transport',
    type: 'expense',
    icon: null,
    sort_order: 2,
    is_active: true,
    created_at: '',
  },
];

const currencies: Currency[] = [
  {
    code: 'USD',
    name: 'US Dollar',
    symbol: '$',
    rate_to_base: 1,
    is_base: true,
    is_active: true,
    updated_at: '',
  },
];

function savedTransaction(over: Partial<Transaction> = {}): Transaction {
  return {
    id: 99,
    user_id: 1,
    date: '2026-05-27',
    amount: 43,
    original_amount: null,
    original_currency: null,
    description: 'groceries',
    category_id: 1,
    category_name: 'Groceries',
    category_type: 'expense',
    tags: null,
    notes: null,
    created_at: '',
    updated_at: '',
    ...over,
  };
}

const apiGet = vi.fn();
const apiPost = vi.fn();
const apiDel = vi.fn();

vi.mock('@/api/client', () => ({
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    post: (...args: unknown[]) => apiPost(...args),
    del: (...args: unknown[]) => apiDel(...args),
    put: vi.fn(),
  },
}));

// Re-export under the relative specifier some hooks use.
vi.mock('../api/client', () => ({
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    post: (...args: unknown[]) => apiPost(...args),
    del: (...args: unknown[]) => apiDel(...args),
    put: vi.fn(),
  },
}));

import { QuickAdd } from './QuickAdd';

function renderQuickAdd() {
  return render(
    <MemoryRouter initialEntries={['/quick']}>
      <QuickAdd />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  apiGet.mockImplementation((path: string) => {
    if (path === 'categories') return Promise.resolve(categories);
    if (path === 'currencies') return Promise.resolve(currencies);
    return Promise.resolve([]);
  });
  apiPost.mockResolvedValue(savedTransaction());
  apiDel.mockResolvedValue({});
});

describe('QuickAdd — Freeform mode', () => {
  test('parses "groceries 43", previews amount + preselected category, and POSTs', async () => {
    const user = userEvent.setup();
    renderQuickAdd();

    // Wait for categories to load (chips appear).
    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'groceries 43');

    // Preview shows the amount.
    await waitFor(() => {
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '43',
      );
    });

    // Groceries chip is preselected (aria-pressed).
    const groceriesChip = screen.getByRole('button', { name: /groceries/i });
    expect(groceriesChip).toHaveAttribute('aria-pressed', 'true');

    const addBtn = screen.getByRole('button', { name: /^add$/i });
    expect(addBtn).toBeEnabled();
    await user.click(addBtn);

    await waitFor(() => expect(apiPost).toHaveBeenCalled());
    const [path, body] = apiPost.mock.calls[0];
    expect(path).toBe('transactions');
    expect(body).toMatchObject({
      amount: 43,
      category_id: 1,
      description: 'groceries',
    });
  });
});

describe('QuickAdd — gating', () => {
  test('Add is disabled until a category is picked when parse did not match', async () => {
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    // "coffee 5" has an amount + description but no category name match.
    await user.type(input, 'coffee 5');

    await waitFor(() => {
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '5',
      );
    });

    const addBtn = screen.getByRole('button', { name: /^add$/i });
    expect(addBtn).toBeDisabled();

    // Pick a category chip → now enabled.
    await user.click(screen.getByRole('button', { name: /transport/i }));
    expect(addBtn).toBeEnabled();
  });
});

describe('QuickAdd — income scoping', () => {
  test('an income category name in freeform does NOT auto-enable Add', async () => {
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    // "Salary" is an INCOME category — it has no visible chip and must not
    // be auto-matched by the parser (which now only sees expense categories).
    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'salary 1000');

    await waitFor(() => {
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '1,000',
      );
    });

    // No income chip is rendered.
    expect(
      screen.queryByRole('button', { name: /^salary$/i }),
    ).not.toBeInTheDocument();

    // Add stays disabled because no (expense) category matched.
    expect(screen.getByRole('button', { name: /^add$/i })).toBeDisabled();
  });
});

describe('QuickAdd — Freeform preview placeholder', () => {
  test('shows "Add an amount" instead of $0.00 before an amount is typed', async () => {
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    // A description but no amount yet.
    await user.type(input, 'groceries');

    await waitFor(() => {
      const preview = screen.getByTestId('quick-preview-amount');
      expect(preview).toHaveTextContent(/add an amount/i);
      expect(preview).not.toHaveTextContent('0.00');
    });
  });
});

describe('QuickAdd — category states', () => {
  test('shows an error with a Retry button when categories fail to load', async () => {
    apiGet.mockImplementation((path: string) => {
      if (path === 'categories')
        return Promise.reject(new Error('boom'));
      if (path === 'currencies') return Promise.resolve(currencies);
      return Promise.resolve([]);
    });

    const user = userEvent.setup();
    renderQuickAdd();

    const retry = await screen.findByRole('button', { name: /retry/i });
    expect(screen.getByText(/boom/i)).toBeInTheDocument();

    // Retry refetches — now succeed.
    apiGet.mockImplementation((path: string) => {
      if (path === 'categories') return Promise.resolve(categories);
      if (path === 'currencies') return Promise.resolve(currencies);
      return Promise.resolve([]);
    });
    await user.click(retry);

    await screen.findByRole('button', { name: /groceries/i });
  });

  test('shows an empty state linking to /categories when there are no expense categories', async () => {
    apiGet.mockImplementation((path: string) => {
      if (path === 'categories') return Promise.resolve([]);
      if (path === 'currencies') return Promise.resolve(currencies);
      return Promise.resolve([]);
    });

    renderQuickAdd();

    const link = await screen.findByRole('link', { name: /create one/i });
    expect(link).toHaveAttribute('href', '/categories');
    expect(screen.getByText(/no expense categories yet/i)).toBeInTheDocument();

    // Add stays disabled (no category possible).
    expect(screen.getByRole('button', { name: /^add$/i })).toBeDisabled();
  });
});

describe('QuickAdd — Tap mode', () => {
  test('amount + chip tap posts the right payload', async () => {
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    // Switch to Tap mode.
    await user.click(screen.getByRole('tab', { name: /tap/i }));

    // Enter an amount in the AmountCurrencyInput (type=number spinbutton).
    const amountInput = screen.getByRole('spinbutton');
    await user.type(amountInput, '12');

    // Description.
    const desc = screen.getByLabelText(/description/i);
    await user.type(desc, 'bus ticket');

    // Tap the Transport chip.
    await user.click(screen.getByRole('button', { name: /transport/i }));

    const addBtn = screen.getByRole('button', { name: /^add$/i });
    await waitFor(() => expect(addBtn).toBeEnabled());
    await user.click(addBtn);

    await waitFor(() => expect(apiPost).toHaveBeenCalled());
    const [path, body] = apiPost.mock.calls[0];
    expect(path).toBe('transactions');
    expect(body).toMatchObject({
      amount: 12,
      category_id: 3,
      description: 'bus ticket',
    });
  });
});

describe('QuickAdd — success toast', () => {
  test('shows a success toast with an Undo action after save', async () => {
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'groceries 43');

    await waitFor(() =>
      expect(screen.getByTestId('quick-preview-amount')).toBeInTheDocument(),
    );

    await user.click(screen.getByRole('button', { name: /^add$/i }));

    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
    const [, opts] = toastSuccess.mock.calls[0] as [
      string,
      { action?: { label: string; onClick: () => void } },
    ];
    expect(opts.action?.label).toMatch(/undo/i);

    // Invoking the Undo action deletes the saved row.
    opts.action?.onClick();
    await waitFor(() => expect(apiDel).toHaveBeenCalledWith('transactions/99'));
  });
});

describe('QuickAdd — anti-regression on within import', () => {
  test('within is available for scoped queries', () => {
    expect(typeof within).toBe('function');
  });
});
