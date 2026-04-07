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

describe('TransactionRow tags display', () => {
  it('renders tag pills when transaction has tags', () => {
    renderRow(makeTx({ tags: 'groceries,weekly' }));
    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
  });

  it('renders empty cell when tags are null', () => {
    renderRow(makeTx({ tags: null }));
    // No tag pills
    expect(screen.queryByText('groceries')).not.toBeInTheDocument();
  });

  it('renders empty cell when tags are empty string', () => {
    renderRow(makeTx({ tags: '' }));
    expect(screen.queryByText('groceries')).not.toBeInTheDocument();
  });
});

describe('TransactionRow tags editing', () => {
  let onUpdate: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onUpdate = vi.fn().mockResolvedValue(undefined);
  });

  it('shows TagInput with existing tags in edit mode', async () => {
    const user = userEvent.setup();
    renderRow(makeTx({ tags: 'groceries,weekly' }), onUpdate);

    // Enter edit mode
    await user.click(screen.getByRole('button', { name: /edit/i }));

    // Tag pills should still be visible in TagInput
    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
  });

  it('includes tags in save/update call', async () => {
    const user = userEvent.setup();
    renderRow(makeTx({ tags: 'groceries' }), onUpdate);

    // Enter edit mode
    await user.click(screen.getByRole('button', { name: /edit/i }));

    // Save via fireEvent.submit (happy-dom doesn't fire submit from button click)
    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith(
        expect.objectContaining({
          tags: 'groceries',
        }),
      );
    });
  });

  it('resets tags on cancel', async () => {
    const user = userEvent.setup();
    renderRow(makeTx({ tags: 'groceries' }), onUpdate);

    // Enter edit mode
    await user.click(screen.getByRole('button', { name: /edit/i }));

    // Find the tag text input within the TagInput wrapper
    // Placeholder is empty when tags exist, so query by the tagInput class
    const tagInputEl = document.querySelector('.tagInput') as HTMLInputElement;
    expect(tagInputEl).not.toBeNull();
    await user.type(tagInputEl, 'extra{Enter}');
    expect(screen.getByText('extra')).toBeInTheDocument();

    // Cancel
    await user.click(screen.getByRole('button', { name: /cancel/i }));

    // Should show original tags only (in display mode pills)
    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.queryByText('extra')).not.toBeInTheDocument();
  });
});
