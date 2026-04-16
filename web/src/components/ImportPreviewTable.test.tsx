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
});
