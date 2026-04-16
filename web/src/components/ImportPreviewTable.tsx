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
            {preview.rows.map((row) => (
              <TableRow key={row.row_id} data-row-id={row.row_id}>
                <TableCell />
                <TableCell>{row.date}</TableCell>
                <TableCell>{row.description}</TableCell>
                <TableCell className="text-muted-foreground">{row.category}</TableCell>
                <TableCell className="text-right font-mono tabular-nums">
                  {typeof row.amount === 'number' ? row.amount.toFixed(2) : row.amount}
                </TableCell>
                <TableCell />
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
