import { useId } from 'react';
import { format } from 'date-fns';
import type { Transaction } from '../api/types';
import { TransactionCard } from './TransactionCard';
import { groupIntoDayRuns } from '@/lib/transaction-day-groups';

export interface TransactionCardListProps {
  transactions: Transaction[];
  baseCode: string;
  selectedIds: Set<number>;
  selectionScope: 'page' | 'all-matching';
  selectionMode: boolean;
  onSelect: (id: number, checked: boolean) => void;
  onOpen: (transaction: Transaction) => void;
  onLongPress: (transaction: Transaction) => void;
  /**
   * Whether to break the page into day headers. True only while the server
   * ordering is by date — see the grouping note below.
   */
  groupByDay: boolean;
}

/**
 * The phone-width transaction list: stacked cards under sticky day headers,
 * replacing the desktop table below `md`.
 *
 * The date column dies here — it is the least information per pixel of the
 * seven, and repeating it on every card wastes the width the description
 * needs. It comes back as a header over each day's run.
 *
 * Grouping is conditional because a day header is a claim about ordering.
 * Sorted by amount or by description, consecutive rows have unrelated dates,
 * so day headers would degenerate into one sticky header per card. In those
 * orders the list stays flat and each card carries its own date instead, which
 * keeps the information reachable in every sort rather than only in one.
 */
export function TransactionCardList({
  transactions,
  baseCode,
  selectedIds,
  selectionScope,
  selectionMode,
  onSelect,
  onOpen,
  onLongPress,
  groupByDay,
}: TransactionCardListProps) {
  const headingIdPrefix = useId();

  function renderCard(transaction: Transaction, showDate: boolean) {
    return (
      <TransactionCard
        key={transaction.id}
        transaction={transaction}
        baseCode={baseCode}
        selected={
          selectionScope === 'all-matching' || selectedIds.has(transaction.id)
        }
        selectionMode={selectionMode}
        // In all-matching scope individual cards are locked, exactly as the
        // table rows are: undefined here disables the checkbox AND makes the
        // whole-card tap inert.
        onSelect={selectionScope === 'all-matching' ? undefined : onSelect}
        onOpen={onOpen}
        // Withheld for two different reasons that must BOTH be checked.
        // `selectionMode` — nothing to enter, and the gesture would fight the
        // tap-to-toggle it enabled. `all-matching` — the lock lives on the
        // SCOPE, not on the mode, and the two come apart: the desktop table
        // never sets selectionMode, so a scope built on desktop and then
        // carried below `md` by a rotation would have left every card locked
        // for tapping while still long-pressable, and the long press routes
        // through handleSelect, which demotes "All N matching" to "1 selected".
        onLongPress={
          selectionMode || selectionScope === 'all-matching'
            ? undefined
            : onLongPress
        }
        showDate={showDate}
      />
    );
  }

  if (!groupByDay) {
    return (
      // `role="list"` is not redundant: Tailwind's preflight sets
      // `list-style: none`, and Safari/VoiceOver drop the list role along with
      // the marker — so without it the rows stop being announced as "list, N
      // items" and lose their position-in-set. `divide-y` rather than a border
      // on each row, so the last row needs no exception.
      <ul
        role="list"
        aria-label="Transactions"
        className="flex flex-col divide-y divide-border"
      >
        {transactions.map((tx) => renderCard(tx, true))}
      </ul>
    );
  }

  return (
    <div className="flex flex-col">
      {groupIntoDayRuns(transactions).map((group) => {
        // The date alone is NOT unique. `groupIntoDayRuns` is run-length by
        // design, so it can legally return two groups carrying the same date —
        // its own test asserts exactly that for interleaved input. Today only
        // date-ordered pages reach here, which makes the collision
        // unreachable rather than impossible; keying on something the helper
        // guarantees unique costs nothing and does not depend on the caller
        // continuing to gate on `sortBy === 'date'`. The first item's id is
        // safe to read: a group only exists because an item was pushed into it.
        const groupKey = `${group.date}-${group.items[0].id}`;
        const headingId = `${headingIdPrefix}-${groupKey}`;
        return (
          <section key={groupKey} aria-labelledby={headingId}>
            {/*
              `top-14` IS MobileNav's `h-14` sticky top bar — the header parks
              directly beneath it instead of sliding under it. Change that bar's
              height and this must change with it.

              Sticky needs an ancestor that actually scrolls, and below `md`
              that is the document: the phone branch deliberately drops the
              desktop Card's `overflow-hidden`, which would otherwise make this
              stick to a box that never moves.
            */}
            <h2
              id={headingId}
              className="sticky top-14 z-10 border-y border-border bg-muted px-3 py-1.5 text-xs font-medium text-muted-foreground"
            >
              {format(new Date(group.date), 'MMM d, yyyy')}
            </h2>
            {/* Same `role="list"` reasoning as the flat branch. Labelled by the
                day heading, so each run announces which day it belongs to
                rather than "list" five times over. */}
            <ul
              role="list"
              aria-labelledby={headingId}
              className="flex flex-col divide-y divide-border"
            >
              {group.items.map((tx) => renderCard(tx, false))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
