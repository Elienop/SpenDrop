import { useState, type ReactNode } from 'react';
import { format as formatDate, parseISO } from 'date-fns';
import { AlertCircle, User } from 'lucide-react';
import { CategoryBadge } from '@/components/CategoryBadge';
import { TransactionDescription } from '@/components/TransactionDescription';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useDayExpenses } from '@/hooks/useDayExpenses';
import { FORMAT_DATE_FULL } from '@/lib/dates';
import {
  displayAmount,
  formatSignedAmount,
  formatSignedCurrency,
} from '@/lib/format';
import { AmountSignNote } from '@/components/AmountSignNote';
import { spendingSummaryPhrase } from './heatmapGrid';

/** The tapped cell: its date, the total the cell itself painted, how many rows
 *  it holds, and whether that date has happened yet. */
export interface HeatmapDay {
  date: string;
  /** The cell's own NET total — signed, so refunds can take it to <= 0. */
  total: number;
  /**
   * Live expense rows on the day. THE FETCH GATE, replacing `total > 0`: a day
   * whose refunds netted it to zero has rows, and this sheet is the only path
   * to them on the phone.
   */
  count: number;
  upcoming: boolean;
}

export interface HeatmapDaySheetProps {
  /** `null` while closed. */
  day: HeatmapDay | null;
  /** The heatmap's injected currency formatter. */
  format: (amount: number) => string;
  onClose: () => void;
  /**
   * Where focus goes on close.
   *
   * Required, not optional, for the same reason `TransactionEditSheet`'s is:
   * this sheet has no `SheetTrigger` (it opens from a grid cell), so Radix's
   * own restore composes `preventDefault(); triggerRef.current?.focus()`
   * against a null ref and every close lands on `<body>`. A keyboard user
   * would be dropped out of the grid they were arrowing through.
   */
  onCloseFocus: () => void;
}

/**
 * The expense rows behind one heatmap cell, as a bottom sheet.
 *
 * THE HEADER TOTAL IS THE CELL'S OWN NUMBER, not a sum of the rows below it.
 * `day.total` is the same value the cell painted its intensity from and
 * announced in its `aria-label`, threaded straight through — so "the sheet's
 * total equals the tapped cell" is true by construction rather than by two
 * pieces of arithmetic agreeing. The rows are expenses-only for the same
 * reason (see `useDayExpenses`), which is what keeps them consistent with it.
 */
export function HeatmapDaySheet({
  day,
  format,
  onClose,
  onCloseFocus,
}: HeatmapDaySheetProps) {
  // Radix keeps the panel mounted through its close animation, but `day` is
  // already null by then. Holding the last non-null value keeps the header and
  // list on screen while the sheet slides out instead of flashing empty.
  // Same "adjusting state when a prop changes" pattern as
  // TransactionEditSheet.
  const [rendered, setRendered] = useState(day);
  if (day != null && day !== rendered) {
    setRendered(day);
  }

  const dateLabel =
    rendered == null ? '' : formatDate(parseISO(rendered.date), FORMAT_DATE_FULL);

  return (
    <Sheet
      open={day != null}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent
        side="bottom"
        // `sheetVariants` already caps at 100dvh; 85 leaves the grid behind it
        // visible so the sheet reads as attached to the day that opened it.
        className="max-h-[85dvh]"
        onCloseAutoFocus={(e) => {
          e.preventDefault();
          onCloseFocus();
        }}
        // Outside the body scroller, not a child of it. The date and the total
        // ARE the answer this sheet exists to give: measured at 360px with 20
        // rows, both left the box after 60px of scroll while 8 rows were still
        // on screen. Now they hold at y=142 through the full 1157px scroll.
        header={
          <SheetHeader>
            <SheetTitle>{dateLabel}</SheetTitle>
            <SheetDescription>
              {rendered == null
                ? ''
                : rendered.upcoming
                  ? 'Upcoming'
                  : rendered.count > 0
                    ? // "Expenses only" is on screen because the LIST is
                      // filtered and nothing else says so. A member who knows
                      // she was also paid that day cannot otherwise tell
                      // whether the income is filtered out or missing from the
                      // ledger — and the cell's total is expenses-only too, so
                      // this names the scope of both numbers at once.
                      //
                      // The phrase itself comes from the same function the
                      // cell's `aria-label` uses, so the header cannot say
                      // "no spending" over a list of four transactions.
                      `${spendingSummaryPhrase(rendered.total, rendered.count, format)} · Expenses only`
                    : 'No spending'}
            </SheetDescription>
          </SheetHeader>
        }
      >
        {rendered != null &&
          (rendered.upcoming ? (
            <p className="text-sm text-muted-foreground">
              This day has not happened yet.
            </p>
          ) : /* ROWS, NOT MONEY. Gated on `total > 0` this sheet refused to
                 fetch a day whose refunds cancelled its spending — the rows
                 existed, the cell said "no spending", and the only phone-native
                 path to them denied they were there. */
            rendered.count > 0 ? (
            <DayExpenseList date={rendered.date} dateLabel={dateLabel} />
          ) : (
            <p className="text-sm text-muted-foreground">
              No expenses were recorded on this day.
            </p>
          ))}
      </SheetContent>
    </Sheet>
  );
}

/**
 * Split out so `useDayExpenses` only ever mounts for a day that HAS ROWS and
 * while the sheet is actually open — see the hook's note on why that placement
 * is load-bearing and not just a saved request.
 */
function DayExpenseList({
  date,
  dateLabel,
}: {
  date: string;
  dateLabel: string;
}) {
  const { expenses, matched, loading, error, retry } = useDayExpenses(date);
  // The base currency, for two things: whether a row's original currency is
  // worth showing, and the signed figure itself. The injected `format` belongs
  // to the DAY's money in the header; a row is a ledger entry and takes the
  // ledger's signed form — see the note at the amount below.
  const baseCode = useBaseCurrency();

  // M8: between opening the sheet and the rows landing, a screen-reader user
  // was told NOTHING — the skeleton is `aria-hidden` (it is decoration) and
  // there was no live region, so the panel simply sat silent and then had
  // content the next time it was explored. `role="status"` is polite, and it
  // fires at most twice per open, so it does not have the arrow-key problem
  // that keeps a live region off the grid itself.
  const status = loading
    ? 'Loading expenses'
    : error !== ''
      ? '' // the Alert below is role="alert" and announces itself
      : `${expenses.length} ${expenses.length === 1 ? 'expense' : 'expenses'}`;

  // One shell around every branch, so the status region and `aria-busy` cannot
  // be present on some states and missing on others.
  const shell = (children: ReactNode) => (
    <div className="flex flex-col gap-2" aria-busy={loading}>
      <p role="status" className="sr-only">
        {status}
      </p>
      {children}
    </div>
  );

  if (loading) {
    return shell(
      <div className="flex flex-col gap-2" aria-hidden="true">
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
      </div>,
    );
  }

  if (error !== '') {
    return shell(
      <Alert variant="destructive">
        <AlertCircle className="h-4 w-4" />
        <AlertDescription className="flex flex-wrap items-center gap-3">
          <span>{error}</span>
          {/* Recovery already worked — by closing the sheet and tapping the
              same day again, which nothing on screen suggests. */}
          <Button variant="outline" size="sm" onClick={retry}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>,
    );
  }

  if (expenses.length === 0) {
    // Reachable when the day's rows were deleted between the heatmap's fetch
    // and this one. The cell still paints the stale total, so saying "none
    // now" beats an empty panel with a non-zero header.
    return shell(
      <p className="text-sm text-muted-foreground">
        No expenses were recorded on this day.
      </p>,
    );
  }

  return shell(
    <>
      {matched > expenses.length && (
        <p className="text-xs text-muted-foreground">
          Showing the {expenses.length} largest of {matched} expenses.
        </p>
      )}
      {/* Named with the DATE, not "this day". The header is pinned for a
          sighted user, but a screen-reader user inside the list has no
          equivalent of glancing back up at it — a deictic name leaves the list
          identifying neither the day nor the total. */}
      <ul className="flex flex-col divide-y" aria-label={`Expenses on ${dateLabel}`}>
        {expenses.map((tx) => {
          // Mirrors AmountDisplay's rule rather than reusing the component:
          // the original amount is what makes a row recognisable in a
          // household that enters LBP daily — a row entered as 1,500,000 LBP
          // showing only "$16.76" is not the row the user remembers.
          //
          // SIGNED THROUGH THE SAME HELPER as every other money render. The
          // list being expenses-only used to mean there was no sign to
          // convey; a refund IS an expense row, so the sign is now the only
          // thing separating "spent 20" from "got 20 back" — and it has to
          // point the same way here as it does in the ledger, or the sheet
          // says the opposite of the row it is showing.
          const value = displayAmount(tx.amount, tx.category_type);
          const original =
            tx.original_amount != null &&
            tx.original_currency != null &&
            tx.original_currency !== baseCode
              ? `${formatSignedAmount(displayAmount(tx.original_amount, tx.category_type))} ${tx.original_currency}`
              : null;
          return (
            <li key={tx.id} className="flex items-start gap-3 py-2">
              <div className="flex min-w-0 flex-1 flex-col">
                {/* The shared component, so the bound + title + "(no
                    description)" fallback cannot drift from the phone card
                    list's copy of the same row. */}
                <TransactionDescription transaction={tx} />
                <div className="mt-1 flex min-w-0 items-center gap-1.5 overflow-hidden">
                  <CategoryBadge
                    category={{ id: tx.category_id, name: tx.category_name }}
                  />
                </div>
                {/* Creator attribution, same idiom as TransactionCard: the
                    ledger is household-wide, and an empty `created_by` means
                    the creator's user row is gone — never render a blank. */}
                <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
                  <User className="size-3.5 shrink-0" aria-hidden="true" />
                  <span className="min-w-0 truncate">
                    <span className="sr-only">Entered by </span>
                    {tx.created_by || 'Unknown'}
                  </span>
                </p>
              </div>
              <span className="flex shrink-0 flex-col items-end font-mono text-sm tabular-nums">
                {/* NOT the injected formatter, and this is the one place that
                    departs from it: `format` is the heatmap's — it renders the
                    day's money, which is a magnitude the header calls "spent".
                    A ROW is a ledger entry and takes the ledger's signed form,
                    which needs `signDisplay`. `baseCode` is already read here
                    (the original-currency decision needs it) and is the same
                    currency the injected formatter is bound to, so the two
                    cannot drift onto different currencies. */}
                <AmountSignNote
                  amount={tx.amount}
                  type={tx.category_type}
                  className="justify-end"
                />
                <span>{formatSignedCurrency(value, baseCode)}</span>
                {original !== null && (
                  <span className="text-xs text-muted-foreground">
                    {original}
                  </span>
                )}
              </span>
            </li>
          );
        })}
      </ul>
    </>,
  );
}
