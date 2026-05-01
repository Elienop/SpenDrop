import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import type { ComputedPatchResult } from './Transactions.computePatch';

const TRUNCATE_AT = 80;

function trunc(s: string): string {
  return s.length > TRUNCATE_AT ? s.slice(0, TRUNCATE_AT) + '…' : s;
}

interface Props {
  open: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  count: number;
  patch: ComputedPatchResult;
  categoryName: (id: number) => string;
}

export function BulkEditConfirmDialog({
  open,
  onCancel,
  onConfirm,
  count,
  patch,
  categoryName,
}: Props) {
  const lines: string[] = [];
  if (patch.patch.date) lines.push(`Date → ${patch.patch.date}`);
  if (patch.patch.description)
    lines.push(`Description → "${trunc(patch.patch.description)}"`);
  if (patch.patch.category_id)
    lines.push(`Category → ${categoryName(patch.patch.category_id)}`);
  if (patch.patch.tags)
    lines.push(`Tags ${patch.tagsMode}: ${trunc(patch.patch.tags)}`);

  return (
    <AlertDialog open={open} onOpenChange={(o) => { if (!o) onCancel(); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            Apply changes to {count} transactions?
          </AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div>
              <ul className="text-sm space-y-1 mt-2">
                {lines.map((l, i) => (
                  <li key={i}>{l}</li>
                ))}
              </ul>
            </div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={onCancel}>Cancel</AlertDialogCancel>
          {/* Default primary palette — bulk-edit is recoverable, not destructive (spec §3.4) */}
          <AlertDialogAction
            onClick={onConfirm}
            aria-label={`Apply changes to ${count} transactions`}
          >
            Apply to {count}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
