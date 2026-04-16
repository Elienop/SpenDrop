import { cn } from '@/lib/utils';
import { formatCurrency, formatAmount } from '@/lib/format';
import { TYPE_EXPENSE, type TransactionType } from '@/lib/transaction-types';

export interface AmountDisplayProps {
  amount: number;
  originalAmount: number | null;
  originalCurrency: string | null;
  type: TransactionType;
  baseCode: string;
  className?: string;
}

export function AmountDisplay({
  amount,
  originalAmount,
  originalCurrency,
  type,
  baseCode,
  className,
}: AmountDisplayProps) {
  const showSecondary =
    originalAmount != null &&
    originalCurrency != null &&
    originalCurrency !== baseCode;

  const sign = type === TYPE_EXPENSE ? '-' : '+';
  const colorClass =
    type === TYPE_EXPENSE ? 'text-foreground' : 'text-emerald-500';

  return (
    <span
      className={cn(
        'inline-flex flex-col items-end font-mono tabular-nums',
        colorClass,
        className,
      )}
    >
      <span>
        {sign}
        {formatCurrency(amount, baseCode)}
      </span>
      {showSecondary && (
        <span
          data-testid="amount-display-secondary"
          className="text-xs font-normal text-muted-foreground"
        >
          {formatAmount(originalAmount!)} {originalCurrency}
        </span>
      )}
    </span>
  );
}
