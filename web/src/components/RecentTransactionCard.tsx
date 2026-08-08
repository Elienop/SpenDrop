import { format } from 'date-fns';
import type { Transaction } from '../api/types';
import { AmountDisplay } from './AmountDisplay';
import { TransactionDescription } from './TransactionDescription';
import { getCategoryColorVar } from '@/lib/chart-colors';

export interface RecentTransactionCardProps {
  transaction: Transaction;
  baseCode: string;
}

/**
 * One row of the Dashboard's Recent Transactions list, at phone width.
 *
 * Deliberately NOT `TransactionCard`, and deliberately not a parallel
 * implementation of it either. The ledger's card is an editing surface — it
 * carries selection, long-press, and tap-to-open-the-edit-sheet — and this is a
 * read-only summary with none of those. Reaching that by adding four
 * off-switches to `TransactionCard` would give one component two personalities
 * and put summary behaviour behind flags on the most heavily pinned component
 * in the slice.
 *
 * What must NOT diverge between the two is the anatomy, so the anatomy is what
 * is shared: `TransactionDescription` (bound + titled + fallback) and
 * `AmountDisplay` are the same components the ledger card renders, which makes
 * the truncation discipline structurally impossible to get wrong here — the
 * failure this surface actually had.
 *
 * Field set matches the table it replaces: category avatar, description,
 * category name, date, amount. No creator line — this surface has never shown
 * attribution and it is a summary, not an editing surface where a member needs
 * to know whose row it is before touching it.
 */
export function RecentTransactionCard({
  transaction,
  baseCode,
}: RecentTransactionCardProps) {
  const color = getCategoryColorVar({ id: transaction.category_id });

  return (
    <li className="flex min-h-14 items-center gap-3 px-1 py-2">
      {/*
        Decorative: it is the category's initial in the category's colour, and
        the category NAME is spelled out on the line beside it. Announcing "G"
        before "Groceries" is noise, so it is hidden from assistive tech rather
        than given a label that duplicates the text.
      */}
      <div
        className="flex size-10 shrink-0 items-center justify-center rounded-lg"
        style={{
          backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
        }}
        aria-hidden="true"
      >
        <span className="text-xs font-medium" style={{ color }}>
          {transaction.category_name?.[0]?.toUpperCase() ?? '?'}
        </span>
      </div>

      <div className="flex min-w-0 flex-1 flex-col">
        <TransactionDescription transaction={transaction} />
        {/*
          The table prints the raw `date` string ("2026-04-01"). That is a
          storage format, not a reading format, and on a card it sits inches
          from a human-readable amount — so it is formatted the same way every
          other date on the phone surfaces is.
        */}
        <p className="mt-0.5 truncate text-xs text-muted-foreground">
          {transaction.category_name} ·{' '}
          {format(new Date(transaction.date), 'MMM d, yyyy')}
        </p>
      </div>

      {/*
        The ledger's AmountDisplay, deliberately NOT the table's bare
        base-currency figure — the same call Trash's card makes, for the same
        reason: this household books in LBP and USD daily, so a card showing
        only the converted number hides the figure the row was entered as.
        Matching the table here would be keeping the wrong parity.
      */}
      <AmountDisplay
        amount={transaction.amount}
        originalAmount={transaction.original_amount}
        originalCurrency={transaction.original_currency}
        type={transaction.category_type}
        baseCode={baseCode}
        className="shrink-0"
      />
    </li>
  );
}
