import { Undo2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { amountSignNote, type AmountSignKind } from '@/lib/format';
import type { TransactionType } from '@/lib/transaction-types';

export interface AmountSignNoteProps {
  /** The row's STORED amount — signed, exactly as it came off the wire. */
  amount: number;
  type: TransactionType;
  className?: string;
}

/**
 * The word beside a signed amount whose sign disagrees with its type.
 *
 * Renders nothing at all for an ordinary row, which is why every ledger
 * surface can mount it unconditionally: a positive expense and a positive
 * income explain themselves, and a label on all four million of them would be
 * noise.
 *
 * WHY THE AMOUNT ALONE IS NOT ENOUGH. A refund is a negative expense, so it
 * displays as `+$20.00` in the income colour — on a row whose category is
 * "Groceries". Without this the household reads that as income booked to the
 * wrong category, or as somebody's typo, and the two are indistinguishable
 * from the number. The reversal case is the mirror: a negative income renders
 * `-$100.00` in the ordinary expense styling.
 *
 * THE METADATA REGISTER, not a Badge. It sits in the same visual class as the
 * creator line ("Entered by …") and the date line: `text-xs`,
 * `text-muted-foreground`, an `aria-hidden` icon and a word. A Badge would put
 * it in the same register as the category and the tags, which are things the
 * user chose — this is a fact about the number. `font-sans` and
 * `font-normal` are load-bearing: every consumer nests this inside a
 * `font-mono` amount block, so without them the word renders in the tabular
 * figures face.
 */
const COPY: Record<AmountSignKind, string> = {
  refund: 'Refund',
  reversal: 'Reversal',
};

export function AmountSignNote({ amount, type, className }: AmountSignNoteProps) {
  const kind = amountSignNote(amount, type);
  if (kind === null) return null;
  return (
    <span
      data-testid="amount-sign-note"
      className={cn(
        'flex items-center gap-1 font-sans text-xs font-normal text-muted-foreground',
        className,
      )}
    >
      <Undo2 className="size-3 shrink-0" aria-hidden="true" />
      {COPY[kind]}
    </span>
  );
}
