import { describe, test, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';

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
    created_at: '',
  },
  {
    id: 2,
    name: 'Transport',
    type: 'expense',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '',
  },
];

/**
 * Mirrors how Transactions.tsx mounts the dialog: permanently rendered, with
 * only `open` toggling. That is the whole point — the component never
 * unmounts, so react-hook-form state survives a close.
 */
function Harness({
  onSubmit,
}: {
  onSubmit: (p: unknown) => void | Promise<void>;
}) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button onClick={() => setOpen(true)}>reopen</button>
      <BulkEditDialog
        open={open}
        onClose={() => setOpen(false)}
        count={3}
        categories={categories}
        onSubmit={onSubmit}
      />
    </>
  );
}

describe('BulkEditDialog', () => {
  test('an abandoned patch is not carried into the next opening', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<Harness onSubmit={vi.fn()} />);

    // Type a patch the user then abandons.
    await user.type(screen.getByLabelText('Description'), 'Coffee');
    await user.type(screen.getByLabelText('Tags'), 'holiday');
    expect(screen.getByLabelText('Description')).toHaveValue('Coffee');

    // Abandon it — Cancel/Escape/backdrop all route through onClose.
    await user.keyboard('{Escape}');
    await waitFor(() => {
      expect(screen.queryByLabelText('Description')).not.toBeInTheDocument();
    });

    // Reopen against what is, in the real page, a different selection.
    await user.click(screen.getByText('reopen'));

    const desc = await screen.findByLabelText('Description');
    expect(desc).toHaveValue('');
    expect(screen.getByLabelText('Tags')).toHaveValue('');
  });

  test('Apply cannot be re-entered while a submit is in flight', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    // Never resolves: models an in-flight bulk PATCH.
    const onSubmit = vi.fn(() => new Promise<void>(() => {}));
    render(<Harness onSubmit={onSubmit} />);

    await user.type(screen.getByLabelText('Description'), 'Coffee');

    const apply = screen.getByRole('button', { name: /apply/i });
    await user.click(apply);
    await user.click(apply);
    await user.click(apply);

    // The parent must RETURN the dispatch promise for RHF to hold
    // isSubmitting; a discarded `void dispatch(p)` lets every extra click
    // fire another bulk PATCH against the same selection.
    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
  });

  test('the Cmd/Ctrl+Enter chord also refuses re-entry while in flight', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onSubmit = vi.fn(() => new Promise<void>(() => {}));
    render(<Harness onSubmit={onSubmit} />);

    const desc = screen.getByLabelText('Description');
    await user.type(desc, 'Coffee');

    await user.type(desc, '{Control>}{Enter}{/Control}');
    await user.type(desc, '{Control>}{Enter}{/Control}');

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
  });
});
