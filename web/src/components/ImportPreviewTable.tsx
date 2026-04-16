import { useMemo, useState, useCallback, type KeyboardEvent } from 'react';
import type { ImportPreview, PatchRowRequest } from '@/api/types';
import type { CellError } from '@/hooks/useImportSession';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

type EditableField = 'date' | 'description' | 'amount';
type EditingCell = { rowID: number; field: EditableField } | null;

export interface ImportPreviewTableProps {
  preview: ImportPreview;
  cellErrors: Record<string, CellError>;
  unresolvedCount: number;
  canImport: boolean;
  pendingPatchCount: number;
  onPatchRow: (
    rowID: number,
    field: PatchRowRequest['field'],
    value: string | boolean,
  ) => Promise<void>;
  onConfirm: () => void;
}

export function ImportPreviewTable(props: ImportPreviewTableProps) {
  const {
    preview,
    cellErrors,
    unresolvedCount,
    canImport,
    pendingPatchCount,
    onPatchRow,
    onConfirm,
  } = props;

  // Local edit state — which cell is currently in edit mode and its
  // in-progress draft value. This is the ONLY local state the component
  // owns; every visual state otherwise derives from props on each render
  // (see collisionRowIds above). The draft is flushed to the server via
  // onPatchRow on commit, then the server's merged value replaces the
  // displayed text on the next render.
  const [editing, setEditing] = useState<EditingCell>(null);
  const [draft, setDraft] = useState<string>('');

  // Derive collision membership from props on EVERY render. No useState,
  // no useEffect — structural guarantee against importcsv #16 (the
  // stale-style bug where a row that flipped collision → clean kept its
  // amber highlight until the next unrelated state change).
  const collisionRowIds = useMemo(() => {
    const s = new Set<number>();
    for (const group of preview.collision_groups) {
      for (const rowID of group.member_row_ids) s.add(rowID);
    }
    return s;
  }, [preview.collision_groups]);

  const keepCount = preview.rows.filter((r) => !r.skip).length;

  const beginEdit = useCallback(
    (rowID: number, field: EditableField, current: string) => {
      setEditing({ rowID, field });
      setDraft(current);
    },
    [],
  );

  const commitEdit = useCallback(async () => {
    // Snapshot-and-clear: capture the target BEFORE clearing `editing`.
    // The subsequent onBlur fires against a now-null `editing`, so the
    // early return short-circuits the double-commit path naturally.
    if (!editing) return;
    const target = editing;
    setEditing(null);
    await onPatchRow(target.rowID, target.field, draft);
  }, [editing, draft, onPatchRow]);

  const cancelEdit = useCallback(() => setEditing(null), []);

  const onKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter') {
        // Prevent ancestor form submit on Enter.
        e.preventDefault();
        void commitEdit();
      } else if (e.key === 'Tab') {
        // Do NOT preventDefault — the browser must advance focus after we
        // unmount the Input. `setEditing(null)` inside commitEdit drops
        // the Input on the next render, and the browser settles focus on
        // the next tab-able element in DOM order (spec §193-194).
        void commitEdit();
      } else if (e.key === 'Escape') {
        cancelEdit();
      }
    },
    [commitEdit, cancelEdit],
  );

  const renderEditableCell = (
    row: ImportPreview['rows'][number],
    field: EditableField,
    displayValue: string,
    extraClass = '',
  ) => {
    const isEditing = editing?.rowID === row.row_id && editing.field === field;
    const errKey = `${row.row_id}:${field}`;
    const err = cellErrors[errKey];
    return (
      <TableCell
        className={extraClass}
        data-cell-error={err ? 'true' : undefined}
        onDoubleClick={() => beginEdit(row.row_id, field, displayValue)}
      >
        {isEditing ? (
          <Input
            autoFocus
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => void commitEdit()}
            onKeyDown={onKeyDown}
            className={`h-7 px-2 py-0 text-sm ${err ? 'ring-1 ring-destructive' : ''}`}
          />
        ) : (
          <span>{displayValue}</span>
        )}
        {err && <p className="text-xs text-destructive mt-0.5">{err.message}</p>}
      </TableCell>
    );
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="max-h-[480px] overflow-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Date</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>Category</TableHead>
              <TableHead className="text-right">Amount</TableHead>
              <TableHead className="w-12">Skip</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {preview.rows.map((row) => {
              const isCollision = collisionRowIds.has(row.row_id);
              return (
                <TableRow
                  key={row.row_id}
                  data-row-id={row.row_id}
                  data-collision={isCollision ? 'true' : undefined}
                  className={isCollision ? 'bg-amber-500/[0.09] border-l-2 border-l-amber-500' : ''}
                >
                  <TableCell />
                  {renderEditableCell(row, 'date', row.date)}
                  {renderEditableCell(row, 'description', row.description)}
                  <TableCell className="text-muted-foreground">{row.category}</TableCell>
                  {renderEditableCell(
                    row,
                    'amount',
                    typeof row.amount === 'number' ? row.amount.toFixed(2) : String(row.amount),
                    'text-right font-mono tabular-nums',
                  )}
                  <TableCell />
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
      <div className="flex items-center justify-between gap-3 pt-2 border-t border-border">
        <div className="text-sm">
          {unresolvedCount > 0 ? (
            <span className="text-amber-500" aria-live="polite">
              {`Fix or skip ${unresolvedCount} ${unresolvedCount === 1 ? 'collision' : 'collisions'} to enable import`}
            </span>
          ) : (
            <span className="text-emerald-500" aria-live="polite">
              {`Ready to import ${keepCount} rows`}
            </span>
          )}
        </div>
        <Button
          type="button"
          disabled={!canImport || pendingPatchCount > 0}
          onClick={onConfirm}
        >
          {`Import ${keepCount}`}
        </Button>
      </div>
    </div>
  );
}
