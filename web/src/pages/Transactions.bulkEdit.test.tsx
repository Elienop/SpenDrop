import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import { BulkEditDialog } from './Transactions.BulkEditDialog';
import { BulkEditConfirmDialog } from './Transactions.BulkEditConfirmDialog';
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
    // Apply button: visible text is "Apply to 12", aria-label is the
    // accessible name "Apply changes to 12 transactions" (spec §4.4).
    expect(
      screen.getByRole('button', { name: /apply.*to 12/i }),
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
      screen.getByRole('button', { name: /apply.*to 12/i }),
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
      screen.getByRole('button', { name: /apply.*to 12/i }),
    );

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ patch: { category_id: 1 } }),
    );
    expect(onSubmit.mock.calls[0][0].tagsMode).toBeUndefined();
  });

  test('Cmd+Enter is a no-op while a previous submit is in-flight', async () => {
    const user = userEvent.setup();
    // Non-resolving promise pins RHF's isSubmitting=true after the first
    // submit lands. The Cmd+Enter chord must not fire a second submit().
    const onSubmit = vi.fn().mockImplementation(() => new Promise(() => {}));
    renderDialog({ onSubmit });

    // Make the patch non-empty so canSubmit=true.
    const triggers = screen
      .getAllByRole('combobox')
      .filter((el) => el.tagName === 'BUTTON');
    await user.click(triggers[0]);
    await user.click(screen.getByRole('option', { name: /groceries/i }));

    // Click Apply -> enters in-flight state.
    await user.click(
      screen.getByRole('button', { name: /apply.*to 12/i }),
    );
    expect(onSubmit).toHaveBeenCalledTimes(1);

    // Apply button is now disabled (canSubmit && !isSubmitting -> false).
    expect(
      screen.getByRole('button', { name: /apply.*to 12/i }),
    ).toBeDisabled();

    // Cmd+Enter from a focused field should be ignored while in-flight.
    const description = screen.getByLabelText(/description/i);
    description.focus();
    await user.keyboard('{Meta>}{Enter}{/Meta}');
    await user.keyboard('{Control>}{Enter}{/Control}');

    expect(onSubmit).toHaveBeenCalledTimes(1);
  });
});

describe('BulkEditConfirmDialog', () => {
  test('summarizes the patch + count', () => {
    render(
      <BulkEditConfirmDialog
        open={true} onCancel={() => {}} onConfirm={() => {}}
        count={1247}
        patch={{ patch: { category_id: 5 }, tagsMode: undefined }}
        categoryName={(id) => id === 5 ? 'Groceries' : 'unknown'}
      />
    );
    expect(screen.getByRole('alertdialog')).toBeInTheDocument();
    expect(screen.getByText(/category/i)).toBeInTheDocument();
    expect(screen.getByText(/groceries/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /apply.*to 1,?247/i })).toBeInTheDocument();
  });

  test('truncates long descriptions at 80 chars with ellipsis', () => {
    const long = 'a'.repeat(120);
    render(
      <BulkEditConfirmDialog
        open={true} onCancel={() => {}} onConfirm={() => {}}
        count={5}
        patch={{ patch: { description: long } }}
        categoryName={() => ''}
      />
    );
    const node = screen.getByText((content) => content.includes('…'));
    expect(node).toBeInTheDocument();
  });

  test('confirm fires onConfirm', async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(
      <BulkEditConfirmDialog
        open={true} onCancel={() => {}} onConfirm={onConfirm}
        count={3} patch={{ patch: { category_id: 5 } }} categoryName={() => 'Groceries'}
      />
    );
    await user.click(screen.getByRole('button', { name: /apply.*to 3/i }));
    expect(onConfirm).toHaveBeenCalled();
  });
});
