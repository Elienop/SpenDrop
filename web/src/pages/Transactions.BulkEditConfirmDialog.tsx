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

function transactionCount(n: number): string {
  return `${n} transaction${n === 1 ? '' : 's'}`;
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
            Apply changes to {transactionCount(count)}?
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
          {/*
            No aria-label. The old one read "Apply changes to N transactions"
            over a visible "Apply to N", so the accessible name did not
            contain the visible label — WCAG 2.5.3 (Label in Name), which
            breaks speech control: saying "click Apply to 5" matches nothing.
            Spelling the phrase out in the visible text makes the two
            identical and leaves nothing to drift apart.

            "Apply changes to N", not "Apply to N": the dialog that opens this
            one has its own "Apply to N" submit button, and while both are
            mounted two buttons would answer to the same spoken name.
          */}
          <AlertDialogAction onClick={onConfirm}>
            Apply changes to {transactionCount(count)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
