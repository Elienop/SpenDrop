import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import { BulkEditDialog } from './Transactions.BulkEditDialog';
import type { Category } from '../api/types';

const categories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2026-01-01T00:00:00Z',
  },
  {
    id: 2,
    name: 'Cleaning',
    type: 'expense',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '2026-01-01T00:00:00Z',
  },
];

function renderDialog(overrides: Partial<{
  open: boolean;
  count: number;
  onClose: () => void;
  onSubmit: (result: unknown) => void;
}> = {}) {
  return render(
    <BulkEditDialog
      open={overrides.open ?? true}
      onClose={overrides.onClose ?? (() => {})}
      count={overrides.count ?? 12}
      categories={categories}
      onSubmit={overrides.onSubmit ?? (() => {})}
    />,
  );
}

describe('BulkEditDialog', () => {
  test('opens with all fields at noChange / empty', () => {
    renderDialog();
    expect(
      screen.getByRole('heading', { name: /edit 12/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('checkbox', { name: /set date/i }),
    ).not.toBeChecked();
    expect(screen.getByPlaceholderText(/keep same/i)).toBeInTheDocument();
    // Default category select reads "— No change —" (the trigger button shows
    // it; SelectContent items are present-but-hidden in the DOM, hence
    // getAllByText rather than getByText).
    expect(screen.getAllByText(/no change/i).length).toBeGreaterThan(0);
    expect(
      screen.getByRole('button', { name: /apply to 12/i }),
    ).toBeDisabled();
  });

  test('Apply enables when category is changed', async () => {
    const user = userEvent.setup();
    renderDialog();

    // The Select trigger is the first combobox (button) in the form.
    const triggers = screen
      .getAllByRole('combobox')
      .filter((el) => el.tagName === 'BUTTON');
    await user.click(triggers[0]);

    const option = screen.getByRole('option', { name: /groceries/i });
    await user.click(option);

    expect(
      screen.getByRole('button', { name: /apply to 12/i }),
    ).toBeEnabled();
  });

  test('Tags radio is disabled while tag input is empty', () => {
    renderDialog();
    expect(screen.getByRole('radio', { name: /add/i })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /remove/i })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /replace/i })).toBeDisabled();
  });

  test('Tags radio enables when user types in the tag input', async () => {
    const user = userEvent.setup();
    renderDialog();
    const tagsInput = screen.getByLabelText(/tags/i);
    await user.type(tagsInput, 'tax');
    expect(screen.getByRole('radio', { name: /add/i })).toBeEnabled();
  });

  test('Submit calls onSubmit with only dirty fields in the patch', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    renderDialog({ onSubmit });

    const triggers = screen
      .getAllByRole('combobox')
      .filter((el) => el.tagName === 'BUTTON');
    await user.click(triggers[0]);
    await user.click(screen.getByRole('option', { name: /groceries/i }));

    await user.click(
      screen.getByRole('button', { name: /apply to 12/i }),
    );

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ patch: { category_id: 1 } }),
    );
    expect(onSubmit.mock.calls[0][0].tagsMode).toBeUndefined();
  });
});
