import {
  render as rtlRender,
  screen,
  waitFor,
  fireEvent,
  act,
  type RenderOptions,
} from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  describe,
  it,
  expect,
  vi,
  beforeEach,
  afterEach,
  type Mock,
} from 'vitest';
import { createElement, type ReactElement, type ReactNode } from 'react';
import { TransactionEntryRow } from './TransactionEntryRow';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import { MAX_DESCRIPTION_LENGTH } from '@/lib/constants';
import type { Category, Transaction } from '../api/types';

// Each render gets a fresh QueryClient so the `useCurrencies` cache is isolated
// per test (replacing the old module-cache reset shim). `retry: false` keeps
// rejected currency fetches from re-firing.
function render(ui: ReactElement, options?: RenderOptions) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return rtlRender(ui, { wrapper, ...options });
}

// Mock sonner so toast.success / toast.error become inspectable and no portal renders
vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    loading: vi.fn(),
  },
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

/** The options the last toast.error call carried. */
function lastErrorOpts(): ToastOpts {
  const calls = (toast.error as Mock).mock.calls;
  return calls[calls.length - 1][1] as ToastOpts;
}

// Keep the real ApiError class: `saveFailureMessage` / `isRetryableSaveFailure`
// discriminate on `instanceof ApiError`, so a mock that omits it turns every
// failure path into a TypeError rather than exercising the branch.
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: {
    get: vi.fn(async (path: string) => {
      if (path === 'currencies') {
        return [
          { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'EUR', name: 'Euro', symbol: '€', rate_to_base: 0.9, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
        ];
      }
      return [];
    }),
    post: vi.fn(),
    put: vi.fn(),
    del: vi.fn(),
  },
}));

// Import after the mock so we get the mocked version
import { toast } from 'sonner';
import { ApiError } from '@/api/client';

/** Fill the row's required fields (amount, description, category). */
async function fillRow(
  user: UserEvent,
  { amount, description }: { amount: string; description: string },
) {
  await user.clear(screen.getByLabelText(/amount/i));
  await user.type(screen.getByLabelText(/amount/i), amount);
  await user.type(screen.getByLabelText(/description/i), description);
  await user.click(screen.getByRole('button', { name: /select category/i }));
  await user.click(await screen.findByRole('option', { name: /groceries/i }));
}

const mockCategories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '2026-01-01',
  },
  {
    id: 2,
    name: 'Salary',
    type: 'income',
    icon: null,
    sort_order: 2,
    is_active: true,
    created_at: '2026-01-01',
  },
  {
    id: 3,
    name: 'Transport',
    type: 'expense',
    icon: null,
    sort_order: 3,
    is_active: true,
    created_at: '2026-01-01',
  },
];

const savedTransaction: Transaction = {
  id: 42,
  user_id: 1,
  created_by: 'Elie',
  date: '2026-04-08',
  amount: 127.43,
  description: 'Whole Foods',
  category_id: 1,
  category_name: 'Groceries',
  category_type: 'expense',
  tags: 'food',
  notes: null,
  original_amount: null,
  original_currency: null,
  created_at: '2026-04-08T12:00:00Z',
  updated_at: '2026-04-08T12:00:00Z',
};

describe('TransactionEntryRow', () => {
  let onSubmit: Mock;
  let onDelete: Mock;

  beforeEach(() => {
    onSubmit = vi.fn().mockResolvedValue(savedTransaction);
    onDelete = vi.fn().mockResolvedValue(undefined);
    (toast.success as Mock).mockClear();
    (toast.error as Mock).mockClear();
    (toast.loading as Mock).mockClear();
    localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // -----------------------------------------------------------------
  // Phase A: Basic render + API payload shape
  // -----------------------------------------------------------------

  it('renders every field with its label', () => {
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    expect(screen.getByLabelText(/date/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/amount/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /select category/i }),
    ).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/add tags/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /add/i })).toBeInTheDocument();
  });

  it('submits the canonical payload shape on a full fill + save', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/date/i));
    await user.type(screen.getByLabelText(/date/i), '2026-04-08');
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '127.43');
    await user.type(screen.getByLabelText(/description/i), 'Whole Foods');

    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    await user.type(screen.getByPlaceholderText(/add tags/i), 'food{Enter}');

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    expect(onSubmit).toHaveBeenCalledWith({
      date: '2026-04-08',
      amount: 127.43,
      description: 'Whole Foods',
      category_id: 1,
      tags: 'food',
      // Idempotency key for this submit — asserted exactly (not via
      // toMatchObject) so a future field can't join the wire unnoticed.
      client_key: expect.any(String),
    });
  });

  // A save that reached the server and committed, but whose response was lost,
  // is indistinguishable from one that never arrived. Retry re-sends the same
  // submission so the server can recognize it; rebuilding the payload here
  // would mint a second identity and create a second row.
  it('Retry re-sends the identical payload, key included', async () => {
    const user = userEvent.setup();
    onSubmit.mockRejectedValueOnce(new Error('network down'));
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '12');
    await user.type(screen.getByLabelText(/description/i), 'Eggs');
    await user.click(screen.getByRole('button', { name: /select category/i }));
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));
    const opts = lastErrorOpts();
    expect(opts.action?.label).toMatch(/retry/i);

    await tapAction(opts);

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
    const first = onSubmit.mock.calls[0][0] as CreateTransactionInput;
    const second = onSubmit.mock.calls[1][0] as CreateTransactionInput;
    expect(first.client_key).toEqual(expect.any(String));
    expect(second.client_key).toBe(first.client_key);
    // Byte-identical on the wire, not merely equal in the fields we happened
    // to think of.
    expect(JSON.stringify(second)).toBe(JSON.stringify(first));
    // The retry succeeded, so the row behaves like any other save.
    await waitFor(() => expect(toast.success).toHaveBeenCalled());
  });

  it('the failure toast never expires and can be dismissed by hand', async () => {
    const user = userEvent.setup();
    onSubmit.mockRejectedValueOnce(new Error('network down'));
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await fillRow(user, { amount: '12', description: 'Eggs' });
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));
    const opts = lastErrorOpts();
    // The Add button beside it mints a fresh key and never expires; a toast
    // that timed out would quietly leave only the duplicating path.
    expect(opts.duration).toBe(Infinity);
    expect(opts.closeButton).toBe(true);
  });

  it('offers no Retry on a 4xx, but does on a 5xx', async () => {
    const user = userEvent.setup();
    onSubmit.mockRejectedValueOnce(new ApiError('bad request', 400));
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await fillRow(user, { amount: '12', description: 'Eggs' });
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));
    // Re-sending the identical body would be refused identically, so a Retry
    // button here would only teach the user that it does nothing. The server's
    // own message is shown instead of a generic one.
    expect((toast.error as Mock).mock.calls[0][0]).toMatch(/bad request/i);
    expect(lastErrorOpts().action).toBeUndefined();

    // A 5xx is the server breaking rather than judging — worth another try.
    onSubmit.mockRejectedValueOnce(new ApiError('server error', 500));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(2));
    expect(lastErrorOpts().action?.label).toMatch(/retry/i);
  });

  it('Retry swaps the toast in place instead of stacking a new one', async () => {
    const user = userEvent.setup();
    onSubmit.mockRejectedValueOnce(new Error('network down'));
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await fillRow(user, { amount: '12', description: 'Eggs' });
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));
    const errorOpts = lastErrorOpts();
    const slot = errorOpts.id;
    expect(slot).toEqual(expect.any(String));

    const event = await tapAction(errorOpts);
    // sonner would otherwise dismiss the toast the moment the action runs.
    expect(event.preventDefault).toHaveBeenCalled();
    expect(toast.loading).toHaveBeenCalledWith(
      expect.stringMatching(/retrying/i),
      expect.objectContaining({ id: slot }),
    );

    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    const [, successOpts] = (toast.success as Mock).mock.calls[0] as [
      string,
      ToastOpts,
    ];
    expect(successOpts.id).toBe(slot);
  });

  it('a retry that succeeds does not wipe a draft typed since the failure', async () => {
    const user = userEvent.setup();
    onSubmit.mockRejectedValueOnce(new Error('network down'));
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await fillRow(user, { amount: '12', description: 'Eggs' });
    await user.click(screen.getByRole('button', { name: /add/i }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));

    // The user gives up waiting and starts the next entry in the row.
    await user.clear(screen.getByLabelText(/description/i));
    await user.type(screen.getByLabelText(/description/i), 'Milk');

    // ...then the earlier failure's Retry finally goes through.
    await tapAction(lastErrorOpts());

    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    // The rescued entry saved under its own description, and the draft in the
    // row survived — resetting it would have destroyed work as a side effect.
    expect(
      (onSubmit.mock.calls[1][0] as CreateTransactionInput).description,
    ).toBe('Eggs');
    expect(screen.getByLabelText(/description/i)).toHaveValue('Milk');
  });

  it('a retry on an untouched row still clears it for the next entry', async () => {
    const user = userEvent.setup();
    onSubmit.mockRejectedValueOnce(new Error('network down'));
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await fillRow(user, { amount: '12', description: 'Eggs' });
    await user.click(screen.getByRole('button', { name: /add/i }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(1));

    await tapAction(lastErrorOpts());

    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByLabelText(/description/i)).toHaveValue(''),
    );
  });

  // The weak flank of the whole feature: a second click while the first save is
  // still in flight builds a NEW payload with a NEW key, so the server has no
  // way to recognize it as the same entry. That is a real duplicate, not a
  // replay.
  it('disables Add while a save is in flight', async () => {
    const user = userEvent.setup();
    let release!: (tx: Transaction) => void;
    onSubmit.mockImplementationOnce(
      () =>
        new Promise<Transaction>((resolve) => {
          release = resolve;
        }),
    );
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await fillRow(user, { amount: '12', description: 'Eggs' });
    await user.click(screen.getByRole('button', { name: /add/i }));

    const button = await screen.findByRole('button', { name: /saving/i });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('aria-busy', 'true');

    await act(async () => {
      release(savedTransaction);
    });

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^add$/i })).toBeEnabled(),
    );
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  // The mouse path is protected by the disabled attribute, because React
  // flushes a click synchronously and the second click lands on a button that
  // is already disabled. The KEYBOARD path is not: `form.handleSubmit(submit)()`
  // validates asynchronously (zodResolver), so two presses in the same tick both
  // get through before `setIsSending(true)` has committed — and each builds its
  // own payload with its own client_key, which is two real rows rather than one
  // replay. Only the synchronous `sendingRef` guard stops that, and only a test
  // that never awaits between the presses can see it.
  it('two Ctrl+Enter presses in one tick submit ONE entry, not two', async () => {
    const user = userEvent.setup();
    // Hold the first save open so both presses land inside the in-flight window.
    let release!: (tx: Transaction) => void;
    onSubmit.mockImplementation(
      () =>
        new Promise<Transaction>((resolve) => {
          release = resolve;
        }),
    );
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await fillRow(user, { amount: '12', description: 'Eggs' });

    const amount = screen.getByLabelText(/amount/i);
    act(() => amount.focus());
    // Deliberately no await between these two: userEvent's keyboard() flushes
    // between presses, which lets the guard's state commit and hides the race.
    fireEvent.keyDown(amount, { key: 'Enter', ctrlKey: true });
    fireEvent.keyDown(amount, { key: 'Enter', ctrlKey: true });

    // Let both validation passes settle, so a second submission would have
    // arrived by the time we count.
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const keys = new Set(
      onSubmit.mock.calls.map(
        (call) => (call[0] as CreateTransactionInput).client_key,
      ),
    );
    // Two keys here would mean the server sees two unrelated submissions and
    // creates two rows — the failure the whole feature exists to prevent.
    expect(keys.size).toBe(1);

    await act(async () => {
      release(savedTransaction);
    });
    await waitFor(() => expect(toast.success).toHaveBeenCalled());
  });

  it('a save after editing the failed row mints a NEW key', async () => {
    const user = userEvent.setup();
    onSubmit.mockRejectedValueOnce(new Error('network down'));
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '12');
    await user.type(screen.getByLabelText(/description/i), 'Eggs');
    await user.click(screen.getByRole('button', { name: /select category/i }));
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));

    // The form survives the failure, so correcting the amount and pressing
    // Save is a DIFFERENT intent — it must not inherit the failed key, or the
    // server would answer with the old row and drop the correction.
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '20');
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
    const first = onSubmit.mock.calls[0][0] as CreateTransactionInput;
    const second = onSubmit.mock.calls[1][0] as CreateTransactionInput;
    expect(second.amount).toBe(20);
    expect(second.client_key).not.toBe(first.client_key);
  });

  // A create that reaches the server but whose response is lost would otherwise
  // be indistinguishable from one that never arrived. The key is what lets the
  // server recognize the second send as the same submission.
  it('carries a client_key per submit, and a different one for the next entry', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '10');
    await user.type(screen.getByLabelText(/description/i), 'Coffee');
    await user.click(screen.getByRole('button', { name: /select category/i }));
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));

    // Category and date survive the reset, so the next entry only needs an
    // amount and a description.
    await user.type(screen.getByLabelText(/amount/i), '4');
    await user.type(screen.getByLabelText(/description/i), 'Bus');
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));

    const first = onSubmit.mock.calls[0][0] as { client_key?: string };
    const second = onSubmit.mock.calls[1][0] as { client_key?: string };
    expect(first.client_key).toBeTruthy();
    expect(second.client_key).toBeTruthy();
    // Two separate intents must never share a key, or the server would answer
    // the second entry with the first one's row and silently drop it.
    expect(second.client_key).not.toBe(first.client_key);
  });

  it('sends an empty tags string when no tags were added', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '10');
    await user.type(screen.getByLabelText(/description/i), 'Coffee');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    expect(onSubmit.mock.calls[0][0].tags).toBe('');
  });

  // -----------------------------------------------------------------
  // Phase B: Category picker (Popover + Command)
  // -----------------------------------------------------------------

  it('filters the category list as the user types in the picker', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    const searchbox = screen.getByPlaceholderText(/search category/i);
    await user.type(searchbox, 'tra');

    expect(
      await screen.findByRole('option', { name: /transport/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('option', { name: /groceries/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('option', { name: /salary/i }),
    ).not.toBeInTheDocument();
  });

  it('selecting a category from the picker updates the trigger label', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /transport/i }));

    expect(
      screen.getByRole('button', { name: /transport/i }),
    ).toBeInTheDocument();
  });

  // -----------------------------------------------------------------
  // Phase C: Keyboard — Enter navigation + ⌘Enter submit
  // -----------------------------------------------------------------

  it('Enter on Amount advances focus without submitting', async () => {
    // With Phase J wiring the amount advances to currency (covered in the
    // Phase J Tab order test). Here we just assert the non-submitting
    // navigation contract: Enter does NOT submit from amount.
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });

    const amount = screen.getByLabelText(/amount/i);
    act(() => amount.focus());
    await user.type(amount, '50{Enter}');

    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('Cmd/Ctrl+Enter from any field submits the form immediately', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '10');
    await user.type(screen.getByLabelText(/description/i), 'Bread');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    act(() => screen.getByLabelText(/amount/i).focus());
    await user.keyboard('{Control>}{Enter}{/Control}');

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
  });

  // -----------------------------------------------------------------
  // Phase D: Escape resets
  // -----------------------------------------------------------------

  it('Cmd/Ctrl+Enter from the amount field clears the amount for the next entry', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    const amount = screen.getByLabelText(/amount/i) as HTMLInputElement;
    await user.clear(amount);
    await user.type(amount, '25');
    await user.type(screen.getByLabelText(/description/i), 'Coffee');
    await user.click(screen.getByRole('button', { name: /select category/i }));
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    // Submit from the amount field itself, so it never loses focus.
    // AmountCurrencyInput deliberately ignores incoming `value` while
    // focused, so the post-save reset to 0 was swallowed and "25" stayed in
    // the DOM — ready to concatenate into the next transaction's amount.
    act(() => amount.focus());
    await user.keyboard('{Control>}{Enter}{/Control}');

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    await waitFor(() => {
      expect(amount.value).not.toBe('25');
    });
  });

  it('Escape inside the category picker dismisses the picker without wiping the row', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    const amount = screen.getByLabelText(/amount/i) as HTMLInputElement;
    const description = screen.getByLabelText(
      /description/i,
    ) as HTMLInputElement;

    await user.clear(amount);
    await user.type(amount, '42');
    await user.type(description, 'Half-typed entry');

    // Open the category picker, then press Escape to dismiss it. Radix
    // portals the popover content, but React still replays the synthetic
    // keydown up the React tree to the form's handler — which used to treat
    // it as "cancel the row" and destroy everything typed so far.
    await user.click(screen.getByRole('button', { name: /select category/i }));
    await user.keyboard('{Escape}');

    expect(amount.value).toBe('42');
    expect(description.value).toBe('Half-typed entry');
  });

  it('Escape resets every field to its default value', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    const amount = screen.getByLabelText(/amount/i) as HTMLInputElement;
    const description = screen.getByLabelText(
      /description/i,
    ) as HTMLInputElement;

    await user.clear(amount);
    await user.type(amount, '999');
    await user.type(description, 'Wrong entry');

    act(() => description.focus());
    await user.keyboard('{Escape}');

    expect(amount.value).toBe('');
    expect(description.value).toBe('');
  });

  // -----------------------------------------------------------------
  // Phase E: onSubmit side effects — post-save reset + Sonner toast + focus
  // -----------------------------------------------------------------

  it('after save clears amount/description/tags but preserves date/category', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/date/i));
    await user.type(screen.getByLabelText(/date/i), '2026-04-08');
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '25');
    await user.type(screen.getByLabelText(/description/i), 'Lunch');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.type(screen.getByPlaceholderText(/add tags/i), 'food{Enter}');

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalled();
    });

    expect(
      (screen.getByLabelText(/amount/i) as HTMLInputElement).value,
    ).toBe('');
    expect(
      (screen.getByLabelText(/description/i) as HTMLInputElement).value,
    ).toBe('');
    expect((screen.getByLabelText(/date/i) as HTMLInputElement).value).toBe(
      '2026-04-08',
    );
    expect(
      screen.getByRole('button', { name: /groceries/i }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/amount/i)).toHaveFocus();
  });

  it('fires a Sonner success toast with an Undo action on save', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '5');
    await user.type(screen.getByLabelText(/description/i), 'Gum');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledTimes(1);
    });
    const [msg, opts] = (toast.success as Mock).mock.calls[0];
    expect(msg).toMatch(/saved/i);
    expect(opts.duration).toBe(4000);
    expect(opts.action.label).toMatch(/undo/i);
    expect(typeof opts.action.onClick).toBe('function');
    expect(typeof opts.onAutoClose).toBe('function');
  });

  // -----------------------------------------------------------------
  // Phase F: Undo — click action + ⌘Z while toast visible + auto-close guard
  // -----------------------------------------------------------------

  it('clicking the Undo action calls onDelete with the saved id', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '5');
    await user.type(screen.getByLabelText(/description/i), 'Gum');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled();
    });
    const opts = (toast.success as Mock).mock.calls[0][1];

    // act-wrapped: undo runs onDelete then form.reset, whose field updates
    // would otherwise land outside act.
    await act(async () => {
      await opts.action.onClick();
    });

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledWith(42);
    await waitFor(() => {
      expect(
        (screen.getByLabelText(/description/i) as HTMLInputElement).value,
      ).toBe('Gum');
    });
  });

  it('\u2318Z after toast auto-close is a no-op (onDelete NOT called)', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '5');
    await user.type(screen.getByLabelText(/description/i), 'Gum');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled();
    });
    const opts = (toast.success as Mock).mock.calls[0][1];

    // Invoke the auto-close callback directly to exercise the buffer-clear
    // contract without spinning up fake timers for the real 4000ms duration.
    opts.onAutoClose();

    await user.keyboard('{Control>}z{/Control}');

    expect(onDelete).not.toHaveBeenCalled();
  });

  // -----------------------------------------------------------------
  // Phase I: Error paths — submit rejection + undo rejection
  // -----------------------------------------------------------------

  it('shows an error toast and keeps the form when onSubmit rejects', async () => {
    const user = userEvent.setup();
    onSubmit.mockRejectedValueOnce(new Error('network down'));
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '12');
    await user.type(screen.getByLabelText(/description/i), 'Eggs');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledTimes(1);
    });
    // An unanswered request has an UNKNOWN outcome — the copy says so, and
    // says that Retry cannot duplicate, because otherwise the honest response
    // is to go and check the ledger first.
    expect((toast.error as Mock).mock.calls[0][0]).toMatch(/confirm the save/i);
    expect((toast.error as Mock).mock.calls[0][0]).toMatch(/duplicate/i);
    // Success toast must NOT fire on rejection
    expect(toast.success).not.toHaveBeenCalled();
    // Form is not reset — user can retry
    expect(
      (screen.getByLabelText(/amount/i) as HTMLInputElement).value,
    ).toBe('12');
    expect(
      (screen.getByLabelText(/description/i) as HTMLInputElement).value,
    ).toBe('Eggs');
    // onSubmit was called, but localStorage was NOT updated on failure
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem('spendrop-last-category')).toBeNull();
  });

  it('restores the undo buffer and toasts when onDelete rejects during undo', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '5');
    await user.type(screen.getByLabelText(/description/i), 'Gum');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled();
    });
    const opts = (toast.success as Mock).mock.calls[0][1];

    // First undo attempt: onDelete rejects. Buffer must be restored.
    onDelete.mockRejectedValueOnce(new Error('server error'));
    // act-wrapped: undo runs onDelete then form.reset, whose field updates
    // would otherwise land outside act.
    await act(async () => {
      await opts.action.onClick();
    });

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect((toast.error as Mock).mock.calls[0][0]).toMatch(/could not undo/i);
    // Form NOT reset to the saved values on failure — leave post-save state
    expect(
      (screen.getByLabelText(/description/i) as HTMLInputElement).value,
    ).toBe('');

    // Second undo attempt succeeds because the buffer was restored.
    // act-wrapped: undo runs onDelete then form.reset, whose field updates
    // would otherwise land outside act.
    await act(async () => {
      await opts.action.onClick();
    });
    expect(onDelete).toHaveBeenCalledTimes(2);
    await waitFor(() => {
      expect(
        (screen.getByLabelText(/description/i) as HTMLInputElement).value,
      ).toBe('Gum');
    });
  });

  // -----------------------------------------------------------------
  // Phase G: Inline validation via FormMessage
  // -----------------------------------------------------------------

  it('shows inline errors for missing amount, description, and category', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.click(screen.getByRole('button', { name: /add/i }));

    expect(await screen.findByText('> 0')).toBeInTheDocument();
    const requiredErrors = await screen.findAllByText('required');
    expect(requiredErrors.length).toBeGreaterThanOrEqual(2);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  // -----------------------------------------------------------------
  // Phase H: localStorage persistence — date + category
  // -----------------------------------------------------------------

  it('writes spendrop-last-date and spendrop-last-category on save', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/date/i));
    await user.type(screen.getByLabelText(/date/i), '2026-04-08');
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '15');
    await user.type(screen.getByLabelText(/description/i), 'Bus');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /transport/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalled();
    });

    expect(localStorage.getItem('spendrop-last-date')).toBe('2026-04-08');
    expect(localStorage.getItem('spendrop-last-category')).toBe('3');
  });

  it('reads spendrop-last-date and spendrop-last-category on mount', () => {
    localStorage.setItem('spendrop-last-date', '2026-03-15');
    localStorage.setItem('spendrop-last-category', '2');

    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    expect((screen.getByLabelText(/date/i) as HTMLInputElement).value).toBe(
      '2026-03-15',
    );
    expect(
      screen.getByRole('button', { name: /salary/i }),
    ).toBeInTheDocument();
  });

  // -----------------------------------------------------------------
  // Phase J: Currency selector
  // -----------------------------------------------------------------

  it('defaults currency to baseCode when localStorage is empty', async () => {
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    expect(
      await screen.findByRole('button', { name: /currency: usd/i }),
    ).toBeInTheDocument();
  });

  it('defaults currency to spendrop-last-currency when present', async () => {
    localStorage.setItem('spendrop-last-currency', 'LBP');
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    expect(
      await screen.findByRole('button', { name: /currency: lbp/i }),
    ).toBeInTheDocument();
  });

  it('submits the collapsed payload when currency === baseCode', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '25');
    await user.type(screen.getByLabelText(/description/i), 'Lunch');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    const payload = onSubmit.mock.calls[0][0];
    expect(payload).not.toHaveProperty('currency');
    expect(payload).not.toHaveProperty('original_amount');
    expect(payload).not.toHaveProperty('original_currency');
    expect(payload).toMatchObject({ amount: 25 });
  });

  it('submits the expanded payload when currency !== baseCode', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '150000');
    await user.type(screen.getByLabelText(/description/i), 'Groceries');
    await user.click(screen.getByRole('button', { name: /currency: usd/i }));
    await user.click(await screen.findByRole('option', { name: /LBP/ }));
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    const payload = onSubmit.mock.calls[0][0];
    expect(payload).toMatchObject({
      amount: 1.67, // 150000 / 90000 = 1.666... → 1.67
      original_amount: 150000,
      original_currency: 'LBP',
    });
    expect(payload).not.toHaveProperty('currency');
  });

  it('persists currency on successful save', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '50');
    await user.type(screen.getByLabelText(/description/i), 'x');
    await user.click(screen.getByRole('button', { name: /currency: usd/i }));
    await user.click(await screen.findByRole('option', { name: /EUR/ }));
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() =>
      expect(localStorage.getItem('spendrop-last-currency')).toBe('EUR'),
    );
  });

  it('Tab order: date → amount → currency → description → category', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );
    await screen.findByRole('button', { name: /currency: usd/i });
    const amount = screen.getByLabelText(/amount/i);
    act(() => amount.focus());
    await user.type(amount, '5{Enter}');
    expect(screen.getByRole('button', { name: /currency: usd/i })).toHaveFocus();
  });

  // -----------------------------------------------------------------
  // Description length
  //
  // This row used to cap descriptions at 200 while the server, the bulk-edit
  // dialog and the importer all took 500 — so a description that saved fine
  // one dialog over failed here, with Zod's bare "Too big" as the only
  // explanation.
  //
  // The emoji case is the one a naive fix gets wrong. Zod's `.max()` and
  // `String.length` count UTF-16 code units, so 500 astral-plane characters
  // measure 1,000 there and 500 in Go — a value the server accepts, refused in
  // the browser. `charCount` counts code points, which is what Go counts.
  //
  // `fireEvent.change` rather than `user.type`: typing 500 characters one
  // keystroke at a time takes tens of seconds and tests nothing extra.
  // -----------------------------------------------------------------

  /** Fill amount + category, set description directly, save. */
  async function submitWithDescription(
    user: UserEvent,
    description: string,
  ): Promise<void> {
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '12');
    fireEvent.change(screen.getByLabelText(/description/i), {
      target: { value: description },
    });
    await user.click(screen.getByRole('button', { name: /select category/i }));
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));
  }

  it.each([
    ['plain ASCII', 'd'],
    ['Arabic', 'ب'],
    ['an astral-plane emoji', '🧾'],
  ])(
    'saves a description of exactly the full limit written in %s',
    async (_label, unit) => {
      const user = userEvent.setup();
      render(
        <TransactionEntryRow
          categories={mockCategories}
          onSubmit={onSubmit}
          onDelete={onDelete}
        />,
      );

      const atLimit = unit.repeat(MAX_DESCRIPTION_LENGTH);
      await submitWithDescription(user, atLimit);

      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
      expect(
        (onSubmit.mock.calls[0][0] as CreateTransactionInput).description,
      ).toBe(atLimit);
    },
  );

  it('refuses a description one character over the limit', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await submitWithDescription(user, 'ب'.repeat(MAX_DESCRIPTION_LENGTH + 1));

    expect(
      await screen.findByText(`max ${MAX_DESCRIPTION_LENGTH}`),
    ).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
