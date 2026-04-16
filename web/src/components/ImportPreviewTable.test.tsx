import { render, screen } from '@testing-library/react';
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
});
