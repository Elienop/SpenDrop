import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { Mock } from 'vitest';
import { createElement, type ReactNode } from 'react';
import { TransactionRow } from './TransactionRow';
import type { TransactionRowProps } from './TransactionRow';
import type { Transaction, Category } from '../api/types';

vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(async (path: string) => {
      if (path === 'currencies') {
        return [
          { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
        ];
      }
      return [];
    }),
  },
}));

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
];

function makeTx(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: 1,
    user_id: 1,
    date: '2026-04-01',
    amount: 25.5,
    original_amount: null,
    original_currency: null,
    description: 'Weekly groceries',
    category_id: 1,
    category_name: 'Groceries',
    category_type: 'expense',
    tags: null,
    notes: null,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
    ...overrides,
  };
}

function renderRow(
  transaction: Transaction,
  onUpdate?: TransactionRowProps['onUpdate'],
  onDelete?: TransactionRowProps['onDelete'],
  extraProps: Partial<TransactionRowProps> = {},
) {
  const update = onUpdate ?? vi.fn().mockResolvedValue(undefined);
  const del = onDelete ?? vi.fn().mockResolvedValue(undefined);
  // Fresh QueryClient per render so the `useCurrencies` cache is isolated per
  // test (replacing the old module-cache reset shim). `retry: false` keeps
  // rejected currency fetches from re-firing.
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return render(
    <table>
      <tbody>
        <TransactionRow
          transaction={transaction}
          categories={mockCategories}
          onUpdate={update}
          onDelete={del}
          onError={vi.fn()}
          {...extraProps}
        />
      </tbody>
    </table>,
    { wrapper },
  );
}

async function openActionsMenu(description: string) {
  const user = userEvent.setup();
  await user.click(
    screen.getByRole('button', { name: `Actions for ${description}` }),
  );
  return user;
}

describe('TransactionRow tags display', () => {
  it('renders tag pills when transaction has tags', () => {
    renderRow(makeTx({ tags: 'groceries,weekly' }));
    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
  });

  it('renders empty cell when tags are null', () => {
    renderRow(makeTx({ tags: null }));
    expect(screen.queryByText('groceries')).not.toBeInTheDocument();
  });

  it('renders empty cell when tags are empty string', () => {
    renderRow(makeTx({ tags: '' }));
    expect(screen.queryByText('groceries')).not.toBeInTheDocument();
  });
});

describe('TransactionRow actions menu', () => {
  it('exposes an actions menu trigger with an explicit aria-label', () => {
    renderRow(makeTx({ description: 'Weekly groceries' }));
    expect(
      screen.getByRole('button', { name: 'Actions for Weekly groceries' }),
    ).toBeInTheDocument();
  });

  it('opens a menu with Edit and Delete items when the trigger is clicked', async () => {
    renderRow(makeTx({ description: 'Weekly groceries' }));
    await openActionsMenu('Weekly groceries');
    expect(screen.getByRole('menuitem', { name: /edit/i })).toBeInTheDocument();
    expect(
      screen.getByRole('menuitem', { name: /delete/i }),
    ).toBeInTheDocument();
  });

  it('calls onDelete when Delete is chosen', async () => {
    const onDelete = vi
      .fn<TransactionRowProps['onDelete']>()
      .mockResolvedValue(undefined);
    renderRow(
      makeTx({ description: 'Weekly groceries' }),
      undefined,
      onDelete,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /delete/i }));
    expect(onDelete).toHaveBeenCalledWith(1);
  });
});

describe('TransactionRow tags editing', () => {
  let onUpdate: Mock<TransactionRowProps['onUpdate']>;

  beforeEach(() => {
    onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockResolvedValue(undefined);
  });

  it('shows TagInput with existing tags in edit mode', async () => {
    renderRow(makeTx({ tags: 'groceries,weekly' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
  });

  it('includes tags in save/update call', async () => {
    renderRow(makeTx({ tags: 'groceries' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith(
        expect.objectContaining({ tags: 'groceries' }),
      );
    });
  });

  it('resets tags on cancel', async () => {
    renderRow(makeTx({ tags: 'groceries' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    const tagInputEl = screen.getByLabelText('Add tag');
    await user.type(tagInputEl, 'extra{Enter}');
    expect(screen.getByText('extra')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /cancel/i }));

    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.queryByText('extra')).not.toBeInTheDocument();
  });
});

describe('TransactionRow amount display', () => {
  it('renders single-line amount when original_* are null (regression)', () => {
    renderRow(makeTx({ amount: 25.5, original_amount: null, original_currency: null }));
    expect(screen.getByText(/-\$25\.50/)).toBeInTheDocument();
    expect(screen.queryByTestId('amount-display-secondary')).not.toBeInTheDocument();
  });

  it('renders two-line amount when original_currency differs from base', () => {
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
    );
    expect(screen.getByText(/-\$1\.67/)).toBeInTheDocument();
    expect(screen.getByTestId('amount-display-secondary')).toHaveTextContent('LBP');
  });

  it('falls back to single-line when original_currency equals base (defensive)', () => {
    renderRow(
      makeTx({
        amount: 25.5,
        original_amount: 25.5,
        original_currency: 'USD',
      }),
    );
    expect(screen.queryByTestId('amount-display-secondary')).not.toBeInTheDocument();
  });
});

describe('TransactionRow edit — currency', () => {
  it('prefills picker with original_currency when set', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
      onUpdate,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    expect(
      await screen.findByRole('button', { name: /currency: lbp/i }),
    ).toBeInTheDocument();
    expect(screen.getByDisplayValue('150000')).toBeInTheDocument();
  });

  it('prefills picker with baseCode when original_* is null', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    expect(
      await screen.findByRole('button', { name: /currency: usd/i }),
    ).toBeInTheDocument();
  });

  it('_PrefillsBaseWhenOriginalNull_AfterLoad: race guard updates picker once currencies resolve to non-USD base', async () => {
    const client = await import('@/api/client');
    const original = (client.api.get as ReturnType<typeof vi.fn>).getMockImplementation();
    (client.api.get as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path === 'currencies') {
        return [
          { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1.1, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'EUR', name: 'Euro', symbol: '\u20ac', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
        ];
      }
      return [];
    });

    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5, original_amount: null, original_currency: null }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    expect(
      await screen.findByRole('button', { name: /currency: eur/i }),
    ).toBeInTheDocument();

    if (original) {
      (client.api.get as ReturnType<typeof vi.fn>).mockImplementation(original);
    }
  });

  it('saves expanded payload when user switches to non-base currency', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: usd/i });

    await user.click(screen.getByRole('button', { name: /currency: usd/i }));
    await user.click(await screen.findByRole('option', { name: /LBP/ }));
    const amountInput = screen.getByRole('spinbutton');
    await user.clear(amountInput);
    await user.type(amountInput, '150000');

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledTimes(1);
    });
    const payload = onUpdate.mock.calls[0][0];
    expect(payload).toMatchObject({
      id: 1,
      amount: 1.67,
      original_amount: 150000,
      original_currency: 'LBP',
    });
    expect(payload).not.toHaveProperty('currency');
  });

  it('saves collapsed payload when user switches back to base currency', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
      onUpdate,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: lbp/i });

    await user.click(screen.getByRole('button', { name: /currency: lbp/i }));
    await user.click(await screen.findByRole('option', { name: /USD/ }));
    const amountInput = screen.getByRole('spinbutton');
    await user.clear(amountInput);
    await user.type(amountInput, '2');

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledTimes(1);
    });
    const payload = onUpdate.mock.calls[0][0];
    expect(payload).toMatchObject({ id: 1, amount: 2 });
    expect(payload).not.toHaveProperty('original_amount');
    expect(payload).not.toHaveProperty('original_currency');
  });
});

// ---------------------------------------------------------------------------
// Keyboard shortcuts for inline edit (see spec 2026-04-18)
// ---------------------------------------------------------------------------
describe('TransactionRow — keyboard shortcuts', () => {
  // Scoped to this describe intentionally: the body uses `screen.getByRole`,
  // which requires an active `render` that the sibling tests at module scope
  // do not all share. Do not hoist to module level without also factoring in
  // which describe blocks actually render first.
  async function openEditMode(description = 'Weekly groceries') {
    const user = await openActionsMenu(description);
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    return user;
  }

  it('Esc on a clean edit row cancels, does not call onUpdate', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ description: 'Weekly groceries' }), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.keyboard('{Escape}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument();
  });

  it('Esc on a dirty edit row reverts silently to original values', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ description: 'Weekly groceries' }), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.clear(desc);
    await user.type(desc, 'EDITED BUT ABANDONED');
    expect(desc.value).toBe('EDITED BUT ABANDONED');

    await user.keyboard('{Escape}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
    expect(screen.queryByText('EDITED BUT ABANDONED')).not.toBeInTheDocument();
  });

  it('Enter from description input (no ghost) saves the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.keyboard('{Enter}');

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Enter from description with a ghost visible accepts ghost, does NOT save', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ description: 'x' }), onUpdate, undefined, {
      descriptionSuggestions: ['groceries-weekly'],
    });
    const user = await openEditMode('x');

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.clear(desc);
    await user.type(desc, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries-weekly');

    await user.keyboard('{Enter}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(desc.value).toBe('groceries-weekly');

    await user.keyboard('{Enter}');
    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Enter from tag input with typed buffer commits the chip, does NOT save', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ tags: null }), onUpdate);
    const user = await openEditMode();

    const tagInput = screen.getByLabelText('Add tag') as HTMLInputElement;
    await user.click(tagInput);
    await user.type(tagInput, 'urgent');
    await user.keyboard('{Enter}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.getByText('urgent')).toBeInTheDocument();
    expect(tagInput.value).toBe('');

    await user.keyboard('{Enter}');
    await waitFor(() =>
      expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ tags: 'urgent' })),
    );
  });

  it('Enter from amount input saves the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const amount = screen.getByRole('spinbutton') as HTMLInputElement;
    await user.click(amount);
    await user.keyboard('{Enter}');

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Ctrl+Enter force-saves even when a ghost is visible', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate, undefined, {
      descriptionSuggestions: ['groceries-weekly'],
    });
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.clear(desc);
    await user.type(desc, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries-weekly');

    fireEvent.keyDown(desc, { key: 'Enter', ctrlKey: true });

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate.mock.calls[0][0].description).toBe('gro');
  });

  it('Cmd+Enter behaves the same as Ctrl+Enter (macOS)', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    fireEvent.keyDown(desc, { key: 'Enter', metaKey: true });

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Esc inside open category Select closes the Select and does NOT cancel the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    // In Radix react-select ^2.2.6 the SelectTrigger has role=combobox but does
    // NOT set aria-haspopup=listbox. The TagInput and AutocompleteInput in this
    // row are also role=combobox (on <input>s), so we disambiguate by tag — the
    // Radix Select trigger is the only <button role=combobox>.
    //
    // Revisit this selector if Radix Select ever changes its trigger element
    // from <button> (e.g., to a div with role=button) or starts emitting
    // aria-haspopup="listbox" — the semantically stronger selector at that
    // point would be aria-haspopup, matching ARIA 1.2 combobox guidance.
    const trigger = screen
      .getAllByRole('combobox')
      .find((el) => el.tagName === 'BUTTON');
    if (!trigger) throw new Error('Category SelectTrigger not found');
    await user.click(trigger);
    await screen.findByRole('option', { name: 'Groceries' });

    await user.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('option', { name: 'Groceries' })).not.toBeInTheDocument(),
    );
    expect(screen.getByRole('button', { name: /save/i })).toBeInTheDocument();
    expect(onUpdate).not.toHaveBeenCalled();

    // Second Escape: cancels the row (receiver of last resort). In a real
    // browser Radix's FocusScope restores focus to the trigger and
    // user.keyboard would target it; happy-dom doesn't reliably replay that
    // restoration, so dispatch directly to the trigger where focus would be.
    fireEvent.keyDown(trigger, { key: 'Escape' });
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument(),
    );
    expect(onUpdate).not.toHaveBeenCalled();
  });
});
