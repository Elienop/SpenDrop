import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  describe,
  it,
  expect,
  vi,
  beforeEach,
  afterEach,
  type Mock,
} from 'vitest';
import { TransactionEntryRow } from './TransactionEntryRow';
import type { Category, Transaction } from '../api/types';

// Mock sonner so toast.success / toast.error become inspectable and no portal renders
vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
  Toaster: () => null,
}));

// Import after the mock so we get the mocked version
import { toast } from 'sonner';

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
    });
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

  it('Enter on Amount moves focus to Description', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    const amount = screen.getByLabelText(/amount/i);
    amount.focus();
    await user.type(amount, '50{Enter}');

    expect(screen.getByLabelText(/description/i)).toHaveFocus();
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

    screen.getByLabelText(/amount/i).focus();
    await user.keyboard('{Control>}{Enter}{/Control}');

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
  });

  // -----------------------------------------------------------------
  // Phase D: Escape resets
  // -----------------------------------------------------------------

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

    description.focus();
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

    await opts.action.onClick();

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
    expect((toast.error as Mock).mock.calls[0][0]).toMatch(/failed to save/i);
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
    await opts.action.onClick();

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect((toast.error as Mock).mock.calls[0][0]).toMatch(/could not undo/i);
    // Form NOT reset to the saved values on failure — leave post-save state
    expect(
      (screen.getByLabelText(/description/i) as HTMLInputElement).value,
    ).toBe('');

    // Second undo attempt succeeds because the buffer was restored.
    await opts.action.onClick();
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

    expect(await screen.findByText(/amount must be > 0/i)).toBeInTheDocument();
    expect(
      await screen.findByText(/description required/i),
    ).toBeInTheDocument();
    expect(
      await screen.findByText(/category required/i),
    ).toBeInTheDocument();
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
});
