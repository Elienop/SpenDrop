import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { TransactionRow } from './TransactionRow';
import type { Transaction, Category } from '../api/types';

const mockCategories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    // NOTE: color field still exists on the Category type until commit 12 drops
    // the DB column. We keep a dummy value to satisfy TypeScript but no test
    // below asserts on it — the new CategoryBadge derives color from id alone.
    color: '#e94560',
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
    // Same note as mockCategories above — kept for type-correctness only.
    category_color: '#e94560',
    tags: null,
    notes: null,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
    ...overrides,
  };
}

function renderRow(
  transaction: Transaction,
  onUpdate?: ReturnType<typeof vi.fn>,
  onDelete?: ReturnType<typeof vi.fn>,
) {
  return render(
    <table>
      <tbody>
        <TransactionRow
          transaction={transaction}
          categories={mockCategories}
          onUpdate={(onUpdate ?? vi.fn().mockResolvedValue(undefined)) as never}
          onDelete={(onDelete ?? vi.fn().mockResolvedValue(undefined)) as never}
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
    const onDelete = vi.fn().mockResolvedValue(undefined);
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
  let onUpdate: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onUpdate = vi.fn().mockResolvedValue(undefined);
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

    // TagInput is the kept-as-is legacy component; its input is still the only
    // text input inside the edit row. Grab it by placeholder-less text query
    // fallback: find all text inputs and pick the last one (the tag field).
    const textInputs = screen
      .getAllByRole('textbox')
      .filter((el) => (el as HTMLInputElement).type === 'text');
    const tagInputEl = textInputs[textInputs.length - 1];
    await user.type(tagInputEl, 'extra{Enter}');
    expect(screen.getByText('extra')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /cancel/i }));

    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.queryByText('extra')).not.toBeInTheDocument();
  });
});
