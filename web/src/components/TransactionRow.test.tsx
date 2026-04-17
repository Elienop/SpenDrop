import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { Mock } from 'vitest';
import { TransactionRow } from './TransactionRow';
import type { TransactionRowProps } from './TransactionRow';
import type { Transaction, Category } from '../api/types';
import { __resetCurrenciesCacheForTests } from '../hooks/useCurrencies';

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

beforeEach(() => {
  __resetCurrenciesCacheForTests();
});

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
) {
  const update = onUpdate ?? vi.fn().mockResolvedValue(undefined);
  const del = onDelete ?? vi.fn().mockResolvedValue(undefined);
  return render(
    <table>
      <tbody>
        <TransactionRow
          transaction={transaction}
          categories={mockCategories}
          onUpdate={update}
          onDelete={del}
          onError={vi.fn()}
        />
      </tbody>
    </table>,
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
    __resetCurrenciesCacheForTests();

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
