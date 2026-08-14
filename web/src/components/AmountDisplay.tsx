import { cn } from '@/lib/utils';
import {
  displayAmount,
  formatSignedAmount,
  formatSignedCurrency,
} from '@/lib/format';
import type { TransactionType } from '@/lib/transaction-types';
import { AmountSignNote } from './AmountSignNote';

export interface AmountDisplayProps {
  /** The row's STORED amount, signed: negative on an expense is a refund. */
  amount: number;
  originalAmount: number | null;
  originalCurrency: string | null;
  type: TransactionType;
  baseCode: string;
  className?: string;
}

/**
 * The ledger's money block: one signed figure, its original-currency line, and
 * the word that explains a sign the type does not.
 *
 * THE SIGN AND THE COLOUR BOTH FOLLOW THE DISPLAYED VALUE, not the type. This
 * used to compose `type === 'expense' ? '-' : '+'` onto a formatter that emits
 * its own minus — which was fine while amounts could not be negative and
 * renders "--$12.34" the moment one is. `displayAmount` combines value and
 * type once, `formatSignedCurrency` signs it once, and a refund therefore
 * comes out `+$20.00` in the inflow colour with `AmountSignNote` saying which
 * kind of inflow it is.
 *
 * BOTH LINES TAKE THE SAME TREATMENT. The secondary line is the same money in
 * the currency it was booked in, so it is signed from the same rule — a row
 * reading "+$1.67" over "-150,000.00 LBP" would be two different claims about
 * one transaction.
 */
export function AmountDisplay({
  amount,
  originalAmount,
  originalCurrency,
  type,
  baseCode,
  className,
}: AmountDisplayProps) {
  const value = displayAmount(amount, type);
  const colorClass = value > 0 ? 'text-emerald-500' : 'text-foreground';

  const secondary =
    originalAmount != null &&
    originalCurrency != null &&
    originalCurrency !== baseCode
      ? `${formatSignedAmount(displayAmount(originalAmount, type))} ${originalCurrency}`
      : null;

  return (
    <span
      data-testid="amount-display"
      className={cn(
        'inline-flex flex-col items-end font-mono tabular-nums',
        colorClass,
        className,
      )}
    >
      {/* ABOVE the figure, so the announcement reads "Refund, +$20.00" rather
          than leaving a screen-reader user to hear the number first and the
          correction last — and, on the phone card, so it never sits between
          the base figure and the original-currency line that explains it. */}
      <AmountSignNote amount={amount} type={type} />
      <span>{formatSignedCurrency(value, baseCode)}</span>
      {secondary && (
        <span
          data-testid="amount-display-secondary"
          className="text-xs font-normal text-muted-foreground"
        >
          {secondary}
        </span>
      )}
    </span>
  );
}
