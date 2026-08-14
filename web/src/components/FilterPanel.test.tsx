import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { FilterPanel } from './FilterPanel';
import type { TransactionFilters } from '../hooks/useTransactions';
import type { Category } from '../api/types';

const categories: Category[] = [
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

const emptyFilters: TransactionFilters = {
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

function renderPanel(filters: Partial<TransactionFilters> = {}) {
  const setFilter = vi.fn<
    (key: keyof TransactionFilters, value: string) => void
  >();
  render(
    <FilterPanel
      filters={{ ...emptyFilters, ...filters }}
      setFilter={setFilter}
      clearPanelFilters={vi.fn()}
      categories={categories}
      savedFilters={[]}
      onSaveFilter={vi.fn()}
      onLoadFilter={vi.fn()}
      onDeleteFilter={vi.fn()}
    />,
  );
  return { setFilter };
}

/** Opens the Amount tab, whose inputs are the subject here. */
async function openAmountTab(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('tab', { name: 'Amount' }));
}

// Amounts are signed now — a refund is a negative expense — and the backend
// compares amount_min/amount_max against the signed value. So this is the ONE
// place in the app where a typed minus is the user saying what they mean: a
// household hunting for what came back this month has no other way to ask.
// The amount ENTRY inputs keep their `min="0"`, because there the sign comes
// from the Refund toggle and a minus can only be a typo.
describe('FilterPanel — signed amount bounds', () => {
  // This is the assertion that would fail if `min="0"` came back. The test
  // below it would NOT: `min` marks a value `:invalid` and clamps the spinner,
  // neither of which any DOM implementation here models, and neither of which
  // ever blocked typing.
  it('_NoLowerBoundOnEitherEnd: negative bounds are enterable', async () => {
    const user = userEvent.setup();
    renderPanel();
    await openAmountTab(user);

    // Both ends, because pinning one would leave "Max $" quietly clamped and
    // a range like -100..-1 half-unreachable.
    expect(screen.getByLabelText('Minimum amount')).not.toHaveAttribute('min');
    expect(screen.getByLabelText('Maximum amount')).not.toHaveAttribute('min');
  });

  it('_AcceptsANegativeBound: typing one reaches the filter state', async () => {
    const user = userEvent.setup();
    const { setFilter } = renderPanel();
    await openAmountTab(user);

    await user.type(screen.getByLabelText('Maximum amount'), '-5');

    // Guards the wiring rather than the attribute: the box hands its raw text
    // straight to `setFilter`, so a future onChange that parsed or clamped the
    // value — the obvious "tidy-up" for a numeric filter — would drop the sign
    // here and quietly make refunds unfilterable again.
    expect(setFilter).toHaveBeenCalledWith('amountMax', '-5');
    expect(setFilter.mock.calls.every(([key]) => key === 'amountMax')).toBe(
      true,
    );
  });

  it('_StillANumericInput: the change is the bound, not the input type', async () => {
    const user = userEvent.setup();
    renderPanel();
    await openAmountTab(user);

    for (const name of ['Minimum amount', 'Maximum amount']) {
      const input = screen.getByLabelText(name);
      expect(input).toHaveAttribute('type', 'number');
      expect(input).toHaveAttribute('step', '0.01');
    }
  });
});
