import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { ImportPreviewTable } from './ImportPreviewTable';
import type { ImportPreview } from '@/api/types';

function makePreview(overrides: Partial<ImportPreview> = {}): ImportPreview {
  return {
    import_id: 'preview-abc',
    row_count: 3,
    columns: ['Date', 'Description', 'Amount', 'Category'],
    unique_categories: ['Food'],
    collision_groups: [],
    expires_at: '2099-01-01T00:00:00Z',
    rows: [
      {
        row_id: 0,
        skip: false,
        content_hash: 'h0',
        date: '2025-01-07',
        description: 'Starbucks',
        amount: 5,
        category: 'Food',
      },
      {
        row_id: 1,
        skip: false,
        content_hash: 'h1',
        date: '2025-01-08',
        description: "Trader Joe's",
        amount: 42.1,
        category: 'Food',
      },
      {
        row_id: 2,
        skip: false,
        content_hash: 'h2',
        date: '2025-01-09',
        description: 'Amazon',
        amount: 29.99,
        category: 'Food',
      },
    ],
    ...overrides,
  };
}

const noopProps = {
  cellErrors: {},
  unresolvedCount: 0,
  canImport: true,
  pendingPatchCount: 0,
  onPatchRow: vi.fn(),
  onConfirm: vi.fn(),
};

describe('ImportPreviewTable', () => {
  it('renders one row per preview.rows entry', () => {
    render(<ImportPreviewTable preview={makePreview()} {...noopProps} />);
    // Every row rendered — descriptions are a stable proxy.
    expect(screen.getByText('Starbucks')).toBeInTheDocument();
    expect(screen.getByText("Trader Joe's")).toBeInTheDocument();
    expect(screen.getByText('Amazon')).toBeInTheDocument();
  });

  it('applies amber collision class to rows in collision_groups and nothing to clean rows', () => {
    const preview = makePreview({
      collision_groups: [
        { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
      ],
    });
    render(<ImportPreviewTable preview={preview} {...noopProps} />);

    const row0 = screen.getByText('Starbucks').closest('tr')!;
    const row1 = screen.getByText("Trader Joe's").closest('tr')!;
    const row2 = screen.getByText('Amazon').closest('tr')!;

    // Collision rows carry the amber marker (asserted via data-collision so
    // we're not coupled to a specific Tailwind utility class).
    expect(row0.getAttribute('data-collision')).toBe('true');
    expect(row1.getAttribute('data-collision')).toBe('true');
    // Clean row is explicitly NOT collision — checking "!== 'true'" rather
    // than null because the attribute could be absent OR set to 'false'.
    expect(row2.getAttribute('data-collision')).not.toBe('true');
  });

  it('stale-style regression: a row that flips collision → clean loses data-collision on re-render', () => {
    // Start: row 0 is in a collision group.
    const before = makePreview({
      collision_groups: [
        { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
      ],
    });
    const { rerender } = render(<ImportPreviewTable preview={before} {...noopProps} />);

    expect(screen.getByText('Starbucks').closest('tr')!.getAttribute('data-collision')).toBe('true');

    // Simulate the PATCH response: row 0 is now clean. The hook's merge
    // hands us a fresh preview with collision_groups that no longer include
    // row 0. This is the EXACT case importcsv #16 blew — if the component
    // carries collision state in useState/useEffect-derived state instead of
    // deriving from props, row 0 keeps its amber class until the next state
    // update and the user sees a stale "unresolved" marker.
    const after = makePreview({
      collision_groups: [
        { group_id: 'g1', reason: 'intra_file', member_row_ids: [1] }, // row 0 removed
      ],
    });
    rerender(<ImportPreviewTable preview={after} {...noopProps} />);

    expect(screen.getByText('Starbucks').closest('tr')!.getAttribute('data-collision')).not.toBe('true');
    // And row 1 still carries it.
    expect(screen.getByText("Trader Joe's").closest('tr')!.getAttribute('data-collision')).toBe('true');
  });

  it('Import button reflects canImport and pendingPatchCount', () => {
    const onConfirm = vi.fn();
    const preview = makePreview();

    // Case 1: canImport=false → disabled, label shows unresolved count.
    const { rerender } = render(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={3}
        canImport={false}
        pendingPatchCount={0}
        onPatchRow={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    const btn = screen.getByRole('button', { name: /import/i });
    expect(btn).toBeDisabled();
    // Status message reflects unresolved count.
    expect(screen.getByText(/3.*collision/i)).toBeInTheDocument();

    // Case 2: canImport=true, pendingPatchCount=0 → enabled.
    rerender(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    expect(btn).toBeEnabled();

    // Case 3: pendingPatchCount > 0 → disabled even though canImport is
    // nominally true. This is the PATCH/confirm race guard.
    rerender(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={0}
        canImport={true}
        pendingPatchCount={2}
        onPatchRow={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    expect(btn).toBeDisabled();

    // Case 4: click back in case-2 state fires onConfirm once.
    rerender(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    btn.click();
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('double-click cell + type + Enter fires onPatchRow with correct payload, and inline 400 renders when cellErrors is non-empty', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn().mockResolvedValue(undefined);

    const { rerender } = render(
      <ImportPreviewTable
        preview={makePreview()}
        cellErrors={{}}
        unresolvedCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onConfirm={vi.fn()}
      />,
    );

    // Double-click row 0's description cell (Starbucks).
    const cell = screen.getByText('Starbucks');
    await user.dblClick(cell);

    // Edit input must exist, focused, and have the current value.
    const input = await screen.findByDisplayValue('Starbucks');
    expect(input).toHaveFocus();

    // Clear + type new value + Enter.
    await user.clear(input);
    await user.type(input, 'Starbucks NYC{Enter}');

    expect(onPatchRow).toHaveBeenCalledTimes(1);
    expect(onPatchRow).toHaveBeenCalledWith(0, 'description', 'Starbucks NYC');

    // Rerender with the 400 error injected for (row_id=0, field='description').
    rerender(
      <ImportPreviewTable
        preview={makePreview()}
        cellErrors={{ '0:description': { field: 'description', message: 'INVALID_DESCRIPTION' } }}
        unresolvedCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onConfirm={vi.fn()}
      />,
    );

    // Inline error message rendered.
    expect(screen.getByText('INVALID_DESCRIPTION')).toBeInTheDocument();
    // The cell has an error marker (data attribute to avoid coupling to classes).
    const row0 = screen.getByText('Starbucks').closest('tr')!;
    const errorCell = within(row0).getAllByText(/INVALID_DESCRIPTION|Starbucks/i)[0].closest('td')!;
    expect(errorCell.getAttribute('data-cell-error')).toBe('true');
  });

  it('Escape during edit cancels without firing onPatchRow', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn();

    render(
      <ImportPreviewTable
        preview={makePreview()}
        cellErrors={{}}
        unresolvedCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onConfirm={vi.fn()}
      />,
    );

    await user.dblClick(screen.getByText('Starbucks'));
    const input = await screen.findByDisplayValue('Starbucks');
    await user.clear(input);
    await user.type(input, 'Nope{Escape}');

    expect(onPatchRow).not.toHaveBeenCalled();
    // The cell shows the original value again.
    expect(screen.getByText('Starbucks')).toBeInTheDocument();
  });

  it('Skip checkbox toggles in both directions: unskipped row → skip=true, pre-skipped row → skip=false', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn().mockResolvedValue(undefined);

    // Fixture intentionally mixes skip states so a single render exercises
    // BOTH transitions. The un-skip case is the one most likely to regress
    // silently — after a "Skip all in group" bulk action the user needs a
    // way to un-skip individual rows, and that's the only UI surface for
    // it. If this test only checked the skip direction, a bug where the
    // checkbox was hard-coded to always emit `true` would pass.
    const preview = makePreview({
      rows: [
        {
          row_id: 0,
          skip: false,
          content_hash: 'h0',
          date: '2025-01-07',
          description: 'Starbucks',
          amount: 5,
          category: 'Food',
        },
        {
          row_id: 1,
          skip: true,
          content_hash: 'h1',
          date: '2025-01-08',
          description: "Trader Joe's",
          amount: 42.1,
          category: 'Food',
        },
      ],
    });

    render(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onConfirm={vi.fn()}
      />,
    );

    // Unskipped row → checkbox unchecked, click emits skip=true.
    const row0 = screen.getByText('Starbucks').closest('tr')!;
    const checkbox0 = within(row0).getByRole('checkbox');
    expect(checkbox0).not.toBeChecked();
    await user.click(checkbox0);
    expect(onPatchRow).toHaveBeenNthCalledWith(1, 0, 'skip', true);

    // Pre-skipped row → checkbox checked, click emits skip=false (un-skip path).
    const row1 = screen.getByText("Trader Joe's").closest('tr')!;
    const checkbox1 = within(row1).getByRole('checkbox');
    expect(checkbox1).toBeChecked();
    await user.click(checkbox1);
    expect(onPatchRow).toHaveBeenNthCalledWith(2, 1, 'skip', false);

    expect(onPatchRow).toHaveBeenCalledTimes(2);
  });

  it('renders a collision group header with "Skip all" that fires N onPatchRow calls', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn().mockResolvedValue(undefined);

    render(
      <ImportPreviewTable
        preview={makePreview({
          collision_groups: [
            { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1, 2] },
          ],
        })}
        cellErrors={{}}
        unresolvedCount={3}
        canImport={false}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onConfirm={vi.fn()}
      />,
    );

    // Header row present and labelled.
    expect(screen.getByText(/3 rows collide/i)).toBeInTheDocument();

    const skipAll = screen.getByRole('button', { name: /skip all/i });
    await user.click(skipAll);

    // One PATCH per member row, in member-row-id order, each with skip=true.
    expect(onPatchRow).toHaveBeenCalledTimes(3);
    expect(onPatchRow).toHaveBeenNthCalledWith(1, 0, 'skip', true);
    expect(onPatchRow).toHaveBeenNthCalledWith(2, 1, 'skip', true);
    expect(onPatchRow).toHaveBeenNthCalledWith(3, 2, 'skip', true);
  });
});
