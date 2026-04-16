import { useMemo } from 'react';
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

export function ImportPreviewTable({ preview }: ImportPreviewTableProps) {
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
                  <TableCell>{row.date}</TableCell>
                  <TableCell>{row.description}</TableCell>
                  <TableCell className="text-muted-foreground">{row.category}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">
                    {typeof row.amount === 'number' ? row.amount.toFixed(2) : row.amount}
                  </TableCell>
                  <TableCell />
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
