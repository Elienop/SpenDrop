import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within, act } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement, type ReactNode } from 'react';
import { ApiError, NetworkError } from '@/api/client';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import type { UseDescriptionHistoryResult } from '@/hooks/useDescriptionHistory';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
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
const toastLoading = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign(
    () => undefined,
    {
      success: (...args: unknown[]) => toastSuccess(...args),
      error: (...args: unknown[]) => toastError(...args),
      loading: (...args: unknown[]) => toastLoading(...args),
    },
  ),
  Toaster: () => null,
}));

/** The options object the component hands sonner, as far as tests read it. */
type ToastOpts = {
  id?: string | number;
  duration?: number;
  closeButton?: boolean;
  action?: {
    label: string;
    onClick: (event: { preventDefault: () => void }) => void;
  };
};

/**
 * Invoke a toast action the way sonner does — with an event whose
 * `preventDefault` decides whether sonner keeps the toast open.
 *
 * act-wrapped and awaited because the handler kicks off an async send whose
 * state updates would otherwise land outside act and warn.
 */
async function tapAction(
  opts: ToastOpts,
): Promise<{ preventDefault: ReturnType<typeof vi.fn> }> {
  const event = { preventDefault: vi.fn() };
  await act(async () => {
    opts.action?.onClick(event);
  });
  return event;
}

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
    created_by: 'Elie',
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

vi.mock('@/api/client', async (importOriginal) => ({
  // Keep the real ApiError class so `err instanceof ApiError` in QuickAdd's
  // submit handler discriminates server rejections from network failures.
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    post: (...args: unknown[]) => apiPost(...args),
    del: (...args: unknown[]) => apiDel(...args),
    put: vi.fn(),
  },
}));

// Re-export under the relative specifier some hooks use.
vi.mock('../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    post: (...args: unknown[]) => apiPost(...args),
    del: (...args: unknown[]) => apiDel(...args),
    put: vi.fn(),
  },
}));

// Stub the offline queue so component tests never touch IndexedDB. Individual
// tests override these (e.g. asserting an enqueue, or a non-zero pending count).
const enqueue = vi.fn();
const removeQueued = vi.fn();
const getAllQueued = vi.fn();
const needsSignIn = vi.fn<(userId: number) => boolean>();
vi.mock('@/lib/offline-queue', () => ({
  enqueue: (...args: unknown[]) => enqueue(...args),
  removeQueued: (...args: unknown[]) => removeQueued(...args),
  getAllQueued: (...args: unknown[]) => getAllQueued(...args),
  needsSignIn: (userId: number) => needsSignIn(userId),
  subscribe: () => () => {},
}));

// The "Recently added" panel has its own test; stub it here so QuickAdd's
// tests don't depend on its recent-transactions fetch.
vi.mock('@/components/RecentlyAdded', () => ({
  RecentlyAdded: () => null,
}));

// QuickAdd (and useQuickAdd) read the authenticated user to namespace the
// offline queue. Mock useAuth to a fixed member so the tests don't need to
// mount AuthProvider (whose mount fires an `auth/me` fetch).
const TEST_USER_ID = 7;
vi.mock('@/hooks/useAuth', () => {
  const refreshUser = vi.fn();
  return { useAuth: () => ({ user: { id: TEST_USER_ID }, refreshUser }) };
});

// useDescriptionHistory has its own test; stub it here so QuickAdd tests
// can deterministically inject a list (or empty) and skip the hook's
// `transactions?...` fetch path.
const retryHistory = vi.fn();
const historyMock = vi.fn<() => UseDescriptionHistoryResult>();
vi.mock('@/hooks/useDescriptionHistory', () => ({
  useDescriptionHistory: () => historyMock(),
}));

/** The hook's result, defaulting to a healthy load of `descriptions`. */
function history(
  descriptions: string[],
  over: Partial<UseDescriptionHistoryResult> = {},
): UseDescriptionHistoryResult {
  return {
    descriptions,
    failed: false,
    waitingForNetwork: false,
    retry: retryHistory,
    ...over,
  };
}

import { QuickAdd } from './QuickAdd';

function renderQuickAdd() {
  // Fresh QueryClient per render so the migrated useQuery hooks QuickAdd uses
  // (`useCategories`, `useCurrencies`) have a provider and an isolated cache —
  // letting the per-test `apiGet` mock drive the category/currency states
  // (loading / empty / error / Retry). `retry: false` keeps a rejected
  // categories fetch from re-firing so the error state surfaces deterministically.
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return render(
    <MemoryRouter initialEntries={['/quick']}>
      <QuickAdd />
    </MemoryRouter>,
    { wrapper },
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  // Default to online; the offline describe flips this per-test.
  Object.defineProperty(navigator, 'onLine', {
    configurable: true,
    value: true,
  });
  apiGet.mockImplementation((path: string) => {
    if (path === 'categories') return Promise.resolve(categories);
    if (path === 'currencies') return Promise.resolve(currencies);
    return Promise.resolve([]);
  });
  apiPost.mockResolvedValue(savedTransaction());
  apiDel.mockResolvedValue({});
  enqueue.mockResolvedValue(1);
  removeQueued.mockResolvedValue(undefined);
  getAllQueued.mockResolvedValue([]);
  needsSignIn.mockReturnValue(false);
  // Default: no description history. Individual tests override via
  // `historyMock.mockReturnValue([...])`.
  historyMock.mockReturnValue(history([]));
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

describe('QuickAdd — Income kind', () => {
  test('renders an Expense | Income toggle defaulting to Expense', async () => {
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    const expenseTab = screen.getByRole('tab', { name: /^expense$/i });
    const incomeTab = screen.getByRole('tab', { name: /^income$/i });
    expect(expenseTab).toBeInTheDocument();
    expect(incomeTab).toBeInTheDocument();
    // Defaults to Expense.
    expect(expenseTab).toHaveAttribute('aria-selected', 'true');
    expect(incomeTab).toHaveAttribute('aria-selected', 'false');
  });

  test('labels the Type and Entry-mode toggles for assistive tech', async () => {
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });
    // The two otherwise-identical tablists carry distinct accessible names so
    // a screen-reader user can tell the kind toggle from the mode toggle.
    expect(screen.getByRole('tablist', { name: /^type$/i })).toBeInTheDocument();
    expect(
      screen.getByRole('tablist', { name: /^entry mode$/i }),
    ).toBeInTheDocument();
  });

  test('income preview shows a + sign and income color', async () => {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await user.click(screen.getByRole('tab', { name: /^income$/i }));
    await screen.findByRole('button', { name: /^salary$/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'salary 1000');

    const preview = await screen.findByTestId('quick-preview-amount');
    await waitFor(() => expect(preview.textContent ?? '').toMatch(/^\+/));
    expect(preview.className).toContain('text-emerald-500');
  });

  test('switching to Income shows income chips and hides expense chips', async () => {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    // Expense mode: Salary (income) chip is absent, Groceries (expense) present.
    expect(
      screen.queryByRole('button', { name: /^salary$/i }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: /^income$/i }));

    // Income mode: Salary chip appears, expense chips disappear.
    expect(
      await screen.findByRole('button', { name: /^salary$/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /^groceries$/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /^transport$/i }),
    ).not.toBeInTheDocument();
  });

  test('in Income mode freeform parsing matches an income category', async () => {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await user.click(screen.getByRole('tab', { name: /^income$/i }));
    await screen.findByRole('button', { name: /^salary$/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'salary 1000');

    await waitFor(() => {
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '1,000',
      );
    });

    // The Salary income chip is auto-selected by the parser.
    const salaryChip = screen.getByRole('button', { name: /^salary$/i });
    expect(salaryChip).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: /^add$/i })).toBeEnabled();
  });

  test('submitting in Income mode posts with the income category_id', async () => {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await user.click(screen.getByRole('tab', { name: /^income$/i }));
    await screen.findByRole('button', { name: /^salary$/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'salary 1000');
    await waitFor(() =>
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '1,000',
      ),
    );

    await user.click(screen.getByRole('button', { name: /^add$/i }));

    await waitFor(() => expect(apiPost).toHaveBeenCalled());
    const [path, body] = apiPost.mock.calls[0];
    expect(path).toBe('transactions');
    expect(body).toMatchObject({
      amount: 1000,
      category_id: 2, // Salary (income)
      description: 'salary',
    });
  });

  test('switching kind resets the picked category', async () => {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    // Pick an expense category by tap.
    await user.click(screen.getByRole('button', { name: /^groceries$/i }));
    expect(
      screen.getByRole('button', { name: /^groceries$/i }),
    ).toHaveAttribute('aria-pressed', 'true');

    // Switch to Income — the expense pick must not leak in.
    await user.click(screen.getByRole('tab', { name: /^income$/i }));
    const salaryChip = await screen.findByRole('button', { name: /^salary$/i });
    expect(salaryChip).toHaveAttribute('aria-pressed', 'false');

    // Switch back to Expense — Groceries is no longer selected either.
    await user.click(screen.getByRole('tab', { name: /^expense$/i }));
    expect(
      await screen.findByRole('button', { name: /^groceries$/i }),
    ).toHaveAttribute('aria-pressed', 'false');
  });

  test('shows "No income categories yet" empty state in Income mode when there are none', async () => {
    apiGet.mockImplementation((path: string) => {
      // Only an expense category exists; income pool is empty.
      if (path === 'categories')
        return Promise.resolve([
          {
            id: 1,
            name: 'Groceries',
            type: 'expense',
            icon: null,
            sort_order: 0,
            is_active: true,
            created_at: '',
          },
        ]);
      if (path === 'currencies') return Promise.resolve(currencies);
      return Promise.resolve([]);
    });

    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await user.click(screen.getByRole('tab', { name: /^income$/i }));

    expect(
      await screen.findByText(/no income categories yet/i),
    ).toBeInTheDocument();
    const link = screen.getByRole('link', { name: /create one/i });
    expect(link).toHaveAttribute('href', '/categories');
  });

  test('persists the kind toggle to localStorage', async () => {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await user.click(screen.getByRole('tab', { name: /^income$/i }));

    expect(localStorage.getItem(STORAGE_KEYS.quickAddKind)).toBe('income');

    await user.click(screen.getByRole('tab', { name: /^expense$/i }));
    expect(localStorage.getItem(STORAGE_KEYS.quickAddKind)).toBe('expense');
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

describe('QuickAdd — build stamp', () => {
  test('shows which bundle this device is running', async () => {
    // The phone is where staleness bites: the service worker serves the
    // previously-cached shell for one launch after a deploy, and without this
    // line there is no way to tell which build you are looking at.
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    expect(await screen.findByTestId('app-version')).toHaveTextContent(
      /SpenDrop dev/,
    );
  });

  test('keeps the stamp out of the sticky footer', async () => {
    // Placement is the whole point of the choice, not a detail: the footer
    // holds the Add button against the on-screen keyboard and sonner stacks
    // its toasts over that same corner. The stamp belongs in the scrolling
    // content.
    renderQuickAdd();

    const stamp = await screen.findByTestId('app-version');
    expect(stamp.closest('footer')).toBeNull();
    expect(stamp.closest('main')).not.toBeNull();
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

describe('QuickAdd — offline capture', () => {
  test('queues the entry (no POST) and shows a "saved offline" toast', async () => {
    Object.defineProperty(navigator, 'onLine', {
      configurable: true,
      value: false,
    });
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });
    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'groceries 43');
    await waitFor(() =>
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '43',
      ),
    );

    await user.click(screen.getByRole('button', { name: /^add$/i }));

    // Enqueued locally; the network POST is never attempted while offline.
    await waitFor(() => expect(enqueue).toHaveBeenCalled());
    expect(apiPost).not.toHaveBeenCalled();
    // The queue is namespaced per user: enqueue(userId, payload).
    const [queuedUserId, queuedPayload] = enqueue.mock.calls[0];
    expect(queuedUserId).toBe(TEST_USER_ID);
    expect(queuedPayload).toMatchObject({
      amount: 43,
      category_id: 1,
      description: 'groceries',
    });

    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
    expect(toastSuccess.mock.calls[0][0]).toMatch(/offline/i);
  });

  test('renders the pending-sync banner when entries are queued', async () => {
    getAllQueued.mockResolvedValue([{ id: 1 }, { id: 2 }]);
    renderQuickAdd();
    expect(
      await screen.findByText(/2 entries saved on this device/i),
    ).toBeInTheDocument();
  });

  // Promising a sync that cannot happen is worse than saying nothing: the rows
  // sit there while the owner believes they are on their way.
  test('asks for a sign-in instead of promising a sync when the queue is held', async () => {
    getAllQueued.mockResolvedValue([{ id: 1 }]);
    needsSignIn.mockReturnValue(true);
    renderQuickAdd();

    expect(
      await screen.findByText(/sign in to sync/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/will sync when online/i)).toBeNull();
    expect(screen.getByRole('link', { name: /sign in/i })).toHaveAttribute(
      'href',
      '/login',
    );
  });
});

describe('QuickAdd — an identical entry is saved without comment', () => {
  test('ignores a duplicate verdict if a server ever sends one', async () => {
    // The content hash cannot distinguish a household member's identical
    // same-day entry from a retry double, so no verdict from it may reach the
    // toast. If a field like this reappears on the wire, the client must not
    // read it — the sentence it produced accused the wrong person and put a
    // one-tap delete next to the accusation.
    apiPost.mockResolvedValueOnce({
      ...savedTransaction({ id: 123 }),
      duplicate_of: 77,
    });
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });
    await user.type(screen.getByPlaceholderText(/lunch/i), 'groceries 43');
    await waitFor(() =>
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '43',
      ),
    );

    await user.click(screen.getByRole('button', { name: /^add$/i }));

    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
    expect(toastSuccess.mock.calls[0][0]).toBe('Transaction saved');
    expect(toastSuccess.mock.calls[0][0]).not.toMatch(
      /already|duplicate|copy/i,
    );
    // The Undo affordance is unchanged and still targets the new row.
    const [, opts] = toastSuccess.mock.calls[0] as [
      string,
      { action?: { label: string; onClick: () => void } },
    ];
    expect(opts.action?.label).toMatch(/undo/i);
    opts.action?.onClick();
    await waitFor(() =>
      expect(apiDel).toHaveBeenCalledWith('transactions/123'),
    );
  });

  test('an ordinary save is still reported as saved', async () => {
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });
    await user.type(screen.getByPlaceholderText(/lunch/i), 'groceries 43');
    await waitFor(() =>
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '43',
      ),
    );

    await user.click(screen.getByRole('button', { name: /^add$/i }));

    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
    expect(toastSuccess.mock.calls[0][0]).toMatch(/saved/i);
    expect(toastSuccess.mock.calls[0][0]).not.toMatch(/already/i);
  });
});

describe('QuickAdd — submit failure (online, never queues)', () => {
  test('a server rejection (ApiError) shows the message and does NOT queue', async () => {
    apiPost.mockRejectedValueOnce(new ApiError('bad request', 400));
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });
    await user.type(screen.getByPlaceholderText(/lunch/i), 'groceries 43');
    await waitFor(() =>
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '43',
      ),
    );

    await user.click(screen.getByRole('button', { name: /^add$/i }));

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(toastError.mock.calls[0][0]).toMatch(/bad request/i);
    // Critical dup-safety guarantee: an online failure must NOT be queued
    // (the write may have landed — re-posting it would duplicate the row).
    expect(enqueue).not.toHaveBeenCalled();
  });

  test('a network failure while "online" prompts retry and does NOT queue', async () => {
    apiPost.mockRejectedValueOnce(
      new NetworkError('Could not reach the server', 'unreachable'),
    );
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });
    await user.type(screen.getByPlaceholderText(/lunch/i), 'groceries 43');
    await waitFor(() =>
      expect(screen.getByTestId('quick-preview-amount')).toHaveTextContent(
        '43',
      ),
    );

    await user.click(screen.getByRole('button', { name: /^add$/i }));

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    // The load-bearing part is that the outcome is UNKNOWN and that trying
    // again is safe — not that the network is down.
    expect(toastError.mock.calls[0][0]).toMatch(/confirm the save/i);
    expect(toastError.mock.calls[0][0]).toMatch(/duplicate/i);
    expect(enqueue).not.toHaveBeenCalled();
  });
});

// The field bug: a POST that reached the server and committed, but whose
// response was lost, surfaced a Retry that created a second identical row. The
// client mints one key per submission, so a re-send of that submission is
// recognizable as the same intent — which only works if Retry re-sends the
// payload that was already built rather than rebuilding it from the form.
describe('QuickAdd — one idempotency key per intent', () => {
  async function typeAndAdd(user: UserEvent, entry: string) {
    await user.type(screen.getByPlaceholderText(/lunch/i), entry);
    await waitFor(() =>
      expect(screen.getByTestId('quick-preview-amount')).toBeInTheDocument(),
    );
    await user.click(screen.getByRole('button', { name: /^add$/i }));
  }

  function postedBody(call: number): CreateTransactionInput {
    return apiPost.mock.calls[call][1] as CreateTransactionInput;
  }

  test('a create carries a client_key', async () => {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');

    await waitFor(() => expect(apiPost).toHaveBeenCalled());
    expect(postedBody(0).client_key).toEqual(expect.any(String));
    // The server caps the column at 64 characters; a longer key would be
    // rejected or truncated into a different key.
    expect(postedBody(0).client_key?.length).toBeLessThanOrEqual(64);
  });

  test('Retry re-sends the identical body, key included', async () => {
    // "Online but unreachable" is the ambiguous case: the write may well have
    // landed, which is precisely when a re-keyed retry duplicates.
    apiPost.mockRejectedValueOnce(
      new NetworkError('Could not reach the server', 'unreachable'),
    );
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    const [, opts] = toastError.mock.calls[0] as [string, ToastOpts];
    expect(opts.action?.label).toMatch(/retry/i);

    await tapAction(opts);

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2));
    const first = postedBody(0);
    const second = postedBody(1);
    expect(first.client_key).toEqual(expect.any(String));
    expect(second.client_key).toBe(first.client_key);
    // Byte-identical on the wire, not merely equal in the fields we happened
    // to think of.
    expect(JSON.stringify(second)).toBe(JSON.stringify(first));
  });

  test('Retry after a 5xx also re-sends the same key', async () => {
    // A server that broke rather than judged: the same request may well work a
    // moment later, and it must go out under the original key in case the
    // failure happened after the row was written.
    apiPost.mockRejectedValueOnce(new ApiError('server error', 500));
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    const [, opts] = toastError.mock.calls[0] as [string, ToastOpts];
    expect(opts.action?.label).toMatch(/retry/i);
    await tapAction(opts);

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2));
    expect(postedBody(1).client_key).toBe(postedBody(0).client_key);
  });

  test('a 4xx rejection offers no Retry — sending it again changes nothing', async () => {
    apiPost.mockRejectedValueOnce(new ApiError('bad request', 400));
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    const [message, opts] = toastError.mock.calls[0] as [string, ToastOpts];
    // The server's own words, and no button that would repeat the refusal.
    expect(message).toMatch(/bad request/i);
    expect(opts.action).toBeUndefined();
    // The entry is still on screen to correct.
    expect(screen.getByPlaceholderText(/lunch/i)).toHaveValue('groceries 43');
  });

  test('the failure toast never expires and can be dismissed by hand', async () => {
    apiPost.mockRejectedValueOnce(
      new NetworkError('Could not reach the server', 'unreachable'),
    );
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    const [, opts] = toastError.mock.calls[0] as [string, ToastOpts];
    // The Add button next to it mints a fresh key and never expires; a toast
    // that timed out would quietly leave only the duplicating path.
    expect(opts.duration).toBe(Infinity);
    expect(opts.closeButton).toBe(true);
  });

  test('Retry swaps the toast in place instead of stacking a new one', async () => {
    apiPost.mockRejectedValueOnce(
      new NetworkError('Could not reach the server', 'unreachable'),
    );
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    const [, errorOpts] = toastError.mock.calls[0] as [string, ToastOpts];
    const slot = errorOpts.id;
    expect(slot).toEqual(expect.any(String));

    const event = await tapAction(errorOpts);
    // sonner would otherwise dismiss the toast the moment the action runs.
    expect(event.preventDefault).toHaveBeenCalled();
    expect(toastLoading).toHaveBeenCalledWith(
      expect.stringMatching(/retrying/i),
      expect.objectContaining({ id: slot }),
    );

    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
    const [, successOpts] = toastSuccess.mock.calls[0] as [string, ToastOpts];
    expect(successOpts.id).toBe(slot);
  });

  test('a retry that succeeds does not wipe a draft typed since the failure', async () => {
    apiPost.mockRejectedValueOnce(
      new NetworkError('Could not reach the server', 'unreachable'),
    );
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');
    await waitFor(() => expect(toastError).toHaveBeenCalled());

    // The user gives up waiting and starts the next entry.
    const input = screen.getByPlaceholderText(/lunch/i);
    await user.clear(input);
    await user.type(input, 'transport 9');

    // ...then the earlier failure's Retry finally goes through.
    const [, opts] = toastError.mock.calls[0] as [string, ToastOpts];
    await tapAction(opts);

    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
    // The rescued entry saved, but clearing the form would have destroyed the
    // draft as a side effect of tidying up.
    expect(postedBody(1)).toMatchObject({ description: 'groceries' });
    expect(input).toHaveValue('transport 9');
  });

  test('a retry on an untouched form still clears it for the next entry', async () => {
    apiPost.mockRejectedValueOnce(
      new NetworkError('Could not reach the server', 'unreachable'),
    );
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');
    await waitFor(() => expect(toastError).toHaveBeenCalled());

    const [, opts] = toastError.mock.calls[0] as [string, ToastOpts];
    await tapAction(opts);

    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByPlaceholderText(/lunch/i)).toHaveValue(''),
    );
  });

  test('the next entry after a successful save gets a NEW key', async () => {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());

    await typeAndAdd(user, 'transport 9');
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2));

    // Reusing a key here would make the server answer the second entry with
    // the first one's row — the transaction would vanish.
    expect(postedBody(1).client_key).not.toBe(postedBody(0).client_key);
    expect(postedBody(1)).toMatchObject({ amount: 9, category_id: 3 });
  });

  test('an offline capture stores the key inside the queued payload', async () => {
    Object.defineProperty(navigator, 'onLine', {
      configurable: true,
      value: false,
    });
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });

    await typeAndAdd(user, 'groceries 43');

    await waitFor(() => expect(enqueue).toHaveBeenCalled());
    const queuedPayload = enqueue.mock.calls[0][1] as CreateTransactionInput;
    // Minted at capture, then replayed verbatim by the queue — so a drain
    // that runs twice cannot create two rows.
    expect(queuedPayload.client_key).toEqual(expect.any(String));
  });
});

describe('QuickAdd — anti-regression on within import', () => {
  test('within is available for scoped queries', () => {
    expect(typeof within).toBe('function');
  });
});

// Typing "Cof" and seeing nothing is ambiguous: it reads as "you have never
// bought coffee". The category panel on this same screen already distinguishes
// "couldn't load" (message + Retry) from "nothing here yet"; the suggestions
// strip has to be just as honest.
describe('QuickAdd — suggestions that could not be loaded', () => {
  async function typeAPrefix() {
    const user = userEvent.setup();
    renderQuickAdd();
    await screen.findByRole('button', { name: /groceries/i });
    await user.type(screen.getByPlaceholderText(/lunch/i), 'cof');
    return user;
  }

  test('says the history could not be loaded and offers Retry', async () => {
    historyMock.mockReturnValue(history([], { failed: true }));

    const user = await typeAPrefix();

    expect(
      await screen.findByText(/couldn’t load your past entries/i),
    ).toBeInTheDocument();
    await user.click(
      screen.getByRole('button', { name: /retry past entries/i }),
    );
    expect(retryHistory).toHaveBeenCalled();
  });

  test('says it is waiting for a connection when the fetch is paused offline', async () => {
    historyMock.mockReturnValue(history([], { waitingForNetwork: true }));

    await typeAPrefix();

    expect(
      await screen.findByText(/past entries will appear when you reconnect/i),
    ).toBeInTheDocument();
    // Nothing to retry — the request has not been attempted.
    expect(
      screen.queryByRole('button', { name: /retry past entries/i }),
    ).toBeNull();
  });

  test('stays silent when the history simply has nothing to suggest', async () => {
    historyMock.mockReturnValue(history([]));

    await typeAPrefix();

    expect(
      screen.queryByText(/couldn’t load your past entries/i),
    ).toBeNull();
    expect(
      screen.queryByText(/past entries will appear when you reconnect/i),
    ).toBeNull();
  });

  test('does not nag before the user has typed a description', async () => {
    historyMock.mockReturnValue(history([], { failed: true }));
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    expect(
      screen.queryByText(/couldn’t load your past entries/i),
    ).toBeNull();
  });
});

describe('QuickAdd — description suggestions strip (freeform)', () => {
  test('shows matching past descriptions as chips when typing a prefix', async () => {
    historyMock.mockReturnValue(history(['lunch', 'lunchbox', 'coffee']));
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'lun');

    expect(
      await screen.findByRole('group', { name: /description suggestions/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'lunch' })).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'lunchbox' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'coffee' }),
    ).not.toBeInTheDocument();
  });

  test('filters out past descriptions that the freeform parser would re-split', async () => {
    // A description like "test 2" looks fine in the history list but, if
    // suggested as a chip, would set the input to "test 2 " — which the
    // parser then re-splits into description="test" and amount=$2, the
    // opposite of what the user just picked. The round-trip filter must
    // drop these so only suggestions the parser leaves intact are shown.
    historyMock.mockReturnValue(history(['tests', 'test 2', 'test #weekly']));
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'test');

    expect(
      await screen.findByRole('group', { name: /description suggestions/i }),
    ).toBeInTheDocument();
    // Clean suggestion survives the filter and renders.
    expect(screen.getByRole('button', { name: 'tests' })).toBeInTheDocument();
    // Parser-breaking suggestions (number-bearing, tag-bearing) are dropped.
    expect(
      screen.queryByRole('button', { name: 'test 2' }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'test #weekly' }),
    ).not.toBeInTheDocument();
  });

  test('tapping a chip rewrites the input to "<suggestion> " (trailing space)', async () => {
    historyMock.mockReturnValue(history(['lunch', 'lunchbox']));
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i) as HTMLInputElement;
    await user.type(input, 'lun');

    await user.click(screen.getByRole('button', { name: 'lunchbox' }));

    expect(input.value).toBe('lunchbox ');
    // Refocused for follow-on typing.
    await waitFor(() => expect(document.activeElement).toBe(input));
  });

  test('the strip disappears once an amount is typed', async () => {
    historyMock.mockReturnValue(history(['lunch', 'lunchbox']));
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'lun');
    expect(
      await screen.findByRole('group', { name: /description suggestions/i }),
    ).toBeInTheDocument();

    // Once an amount is parsed out, the suggestion strip vanishes — we don't
    // want to rewrite the input after the user has committed numbers.
    await user.type(input, 'ch 12');
    await waitFor(() => {
      expect(
        screen.queryByRole('group', { name: /description suggestions/i }),
      ).not.toBeInTheDocument();
    });
  });

  test('Tab in the freeform input accepts the first chip when the strip is visible', async () => {
    historyMock.mockReturnValue(history(['lunch', 'lunchbox']));
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i) as HTMLInputElement;
    await user.type(input, 'lun');
    await screen.findByRole('group', { name: /description suggestions/i });

    // First chip = 'lunch' (prefix match of 'lun', exact-match-no-extension
    // skip does not apply since the typed text is 'lun', not 'lunch').
    await user.keyboard('{Tab}');
    expect(input.value).toBe('lunch ');
  });

  test('switching to tap mode hides the strip (it is freeform-only)', async () => {
    historyMock.mockReturnValue(history(['lunch', 'lunchbox']));
    const user = userEvent.setup();
    renderQuickAdd();

    await screen.findByRole('button', { name: /groceries/i });

    const input = screen.getByPlaceholderText(/lunch/i);
    await user.type(input, 'lun');
    expect(
      await screen.findByRole('group', { name: /description suggestions/i }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: /tap/i }));

    expect(
      screen.queryByRole('group', { name: /description suggestions/i }),
    ).not.toBeInTheDocument();
  });
});
