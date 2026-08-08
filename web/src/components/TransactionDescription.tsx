import type { Transaction } from '../api/types';
import { cn } from '@/lib/utils';
import { transactionLabel } from '@/lib/transaction-label';

export interface TransactionDescriptionProps {
  transaction: Pick<Transaction, 'description'>;
  className?: string;
}

/**
 * A transaction's description as a bounded, titled, fallback-applied line.
 *
 * The three properties travel together on purpose, because each one alone is
 * insufficient and the surface that had none of them is what prompted this
 * component:
 *
 *   - `truncate` + `min-w-0` bound the width. Import bypasses the 500-character
 *     description limit the per-row edit route enforces, so a spreadsheet cell
 *     can put an arbitrarily long string into the ledger. Unbounded, one such
 *     row stretched the Dashboard's recent-transactions table to 3,269px inside
 *     a 293px box and pushed the amount roughly eleven screens off-screen.
 *     `min-w-0` is not decoration: a flex child's automatic minimum size is its
 *     content, so `truncate` alone does nothing until the floor is lifted.
 *   - `title` keeps the full text reachable once it is clipped. Dead on touch,
 *     which is why the fallback below matters more than it looks.
 *   - `transactionLabel` supplies "(no description)" for the empty case, so a
 *     clipped line never collapses to nothing and any accessible name built
 *     from it still names an object.
 *
 * Shared rather than repeated so the three cannot drift apart per surface —
 * which is exactly how the Dashboard ended up with none of them while the
 * ledger had all three.
 */
export function TransactionDescription({
  transaction,
  className,
}: TransactionDescriptionProps) {
  const label = transactionLabel(transaction);
  return (
    <div className={cn('min-w-0 truncate font-medium', className)} title={label}>
      {label}
    </div>
  );
}
