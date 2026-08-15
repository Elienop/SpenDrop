import { useCallback, useEffect, useRef, type PointerEvent } from 'react';
import { format, parseISO } from 'date-fns';
import type { Transaction } from '../api/types';
import { AmountDisplay } from './AmountDisplay';
import { CategoryBadge } from './CategoryBadge';
import { CreatorLabel } from './CreatorLabel';
import { TransactionDescription } from './TransactionDescription';
import { Badge } from '@/components/ui/badge';
import { Checkbox } from '@/components/ui/checkbox';
import { cn } from '@/lib/utils';
import { TOUCH_TARGET_CHECKBOX } from '@/lib/touch-target';
import { transactionLabel } from '@/lib/transaction-label';

/** How long a touch must rest on a card before it opens selection mode. 500ms
 *  is the platform convention on both Android and iOS. */
const LONG_PRESS_MS = 500;
/** A press that travels further than this is the start of a scroll, not a long
 *  press. Without it the timer fires mid-flick and the list starts selecting
 *  rows while the user is trying to move down the page. */
const LONG_PRESS_MOVE_TOLERANCE_PX = 10;

export interface TransactionCardProps {
  transaction: Transaction;
  baseCode: string;
  selected: boolean;
  /** While true the leading checkbox is rendered and a tap toggles selection
   *  instead of opening the edit sheet. */
  selectionMode: boolean;
  /**
   * Undefined LOCKS this card's selection, exactly as it locks a table row:
   * the page passes undefined while `selectionScope` is 'all-matching',
   * because toggling one row cannot express "all rows except this one" and a
   * silent demotion to page scope would look like the selection had cleared.
   */
  onSelect?: (id: number, checked: boolean) => void;
  onOpen: (transaction: Transaction) => void;
  /** Omitted while already in selection mode — there is nothing to enter. */
  onLongPress?: (transaction: Transaction) => void;
  /** Renders the date inline on the metadata line. The list sets this when it
   *  is NOT grouping under day headers, so the date stays reachable in every
   *  sort order rather than only when sorted by date. */
  showDate?: boolean;
}

/**
 * One transaction as a phone-width card. Replaces the 7-column table row
 * below `md`, where the desktop table leaves a 390px viewport nothing to
 * render a date, a description, a category, tags, an amount and an actions
 * menu in.
 *
 * The row is a single tap target rather than a set of small ones: tapping
 * anywhere opens the edit sheet, and there is no per-row actions menu to aim
 * at. Delete moves into that sheet.
 */
export function TransactionCard({
  transaction,
  baseCode,
  selected,
  selectionMode,
  onSelect,
  onOpen,
  onLongPress,
  showDate = false,
}: TransactionCardProps) {
  const timerRef = useRef<number | null>(null);
  const originRef = useRef<{ x: number; y: number } | null>(null);
  // Set when the long-press timer fired, so the `click` that follows the same
  // press does not immediately act again on the row it just selected.
  const firedRef = useRef(false);
  const isTouchRef = useRef(false);

  const clearTimer = useCallback(() => {
    if (timerRef.current != null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  // A card can unmount mid-press (a refetch lands, the filter changes), and a
  // timer that survives it would call into a dead row.
  useEffect(() => clearTimer, [clearTimer]);

  const label = transactionLabel(transaction);

  const tags =
    transaction.tags
      ?.split(',')
      .map((t) => t.trim())
      .filter(Boolean) ?? [];

  function handlePointerDown(e: PointerEvent<HTMLButtonElement>) {
    isTouchRef.current = e.pointerType === 'touch';
    // Cleared BEFORE the early return, not after. A press that fires the timer
    // leaves this true for the click to swallow — but entering selection mode
    // is exactly what makes the parent stop passing `onLongPress`, so the next
    // press on this card returns early. With the reset below the return the
    // flag stayed set and ate the following tap, which in selection mode is
    // the tap that selects the row.
    firedRef.current = false;
    // Long-press is the touch dialect for entering selection mode; the
    // toolbar's Select button is the dialect for everything else. A slow
    // MOUSE click must stay an ordinary click — this is a denylist rather
    // than a `=== 'touch'` allowlist so a pen, or an environment that reports
    // no pointerType at all, still gets the gesture.
    if (!onLongPress || e.pointerType === 'mouse') return;
    originRef.current = { x: e.clientX, y: e.clientY };
    clearTimer();
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      firedRef.current = true;
      onLongPress(transaction);
    }, LONG_PRESS_MS);
  }

  function handlePointerMove(e: PointerEvent<HTMLButtonElement>) {
    const origin = originRef.current;
    if (origin == null || timerRef.current == null) return;
    if (
      Math.abs(e.clientX - origin.x) > LONG_PRESS_MOVE_TOLERANCE_PX ||
      Math.abs(e.clientY - origin.y) > LONG_PRESS_MOVE_TOLERANCE_PX
    ) {
      clearTimer();
    }
  }

  function handleClick() {
    if (firedRef.current) {
      firedRef.current = false;
      return;
    }
    if (selectionMode) {
      // `onSelect` is undefined in all-matching scope — the tap is inert
      // there, matching the disabled checkbox beside it.
      onSelect?.(transaction.id, !selected);
      return;
    }
    onOpen(transaction);
  }

  return (
    <li
      // The row separator lives on the list (`divide-y`), not here — one rule
      // instead of one-per-row plus a `last:` exception. `px-3` matches the day
      // header's inset so the header text and the card text share a left edge.
      className={cn(
        'flex items-center gap-2 px-3',
        selected && 'bg-muted/50',
      )}
    >
      {selectionMode && (
        <span className="flex size-11 shrink-0 items-center justify-center">
          {/* The 44px wrapper reserves the SPACE; TOUCH_TARGET_CHECKBOX is what
              actually catches the taps. A layout box around a control forwards
              nothing — the ~14px ring between the two swallowed every tap that
              missed the 16px checkbox, which on a phone is most of them. The
              pseudo-element grows the checkbox's own hit area to fill the box
              instead, so the visible checkbox stays 16px and optically aligned
              with the description beside it.

              `aria-label` matches the table row's, including the empty-
              description fallback, so one selection test covers both
              presentations. */}
          <Checkbox
            checked={selected}
            disabled={!onSelect}
            onCheckedChange={(v) => onSelect?.(transaction.id, v === true)}
            aria-label={`Select ${label}`}
            className={TOUCH_TARGET_CHECKBOX}
          />
        </span>
      )}
      <button
        type="button"
        // The page needs to find this button again to hand focus back when the
        // edit sheet closes. An attribute rather than an `id` so nothing has to
        // coordinate a prefix, and it doubles as a stable test hook.
        data-transaction-id={transaction.id}
        onClick={handleClick}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={clearTimer}
        onPointerCancel={clearTimer}
        onPointerLeave={clearTimer}
        onContextMenu={(e) => {
          // Android fires contextmenu at the end of a long press, which would
          // drop a text-selection menu on top of the selection the gesture
          // just made. Suppressed for touch only, so right-click stays normal
          // for a mouse at a narrow window width.
          if (isTouchRef.current) e.preventDefault();
        }}
        // Communicates the toggle semantics the row takes on in selection
        // mode. Outside it the row is a plain "open this" button and has no
        // pressed state to report.
        aria-pressed={selectionMode ? selected : undefined}
        className={cn(
          'flex min-h-14 min-w-0 flex-1 select-none items-center gap-3 rounded-md py-2 text-left',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
        )}
      >
        <div className="flex min-w-0 flex-1 flex-col">
          {/* Bound + title + empty-description fallback all come from the
              shared component, so this surface and the Dashboard's cannot
              drift apart on them — the Dashboard's table had none of the three
              and panned to 3,269px because of it. */}
          <TransactionDescription transaction={transaction} />
          <div className="mt-1 flex min-w-0 items-center gap-1.5 overflow-hidden">
            {showDate && (
              // Year included. This branch runs only when the list is NOT
              // grouped by day — i.e. sorted by amount or description — which
              // is exactly when neighbouring rows can be years apart, and
              // "Apr 3" beside "Apr 3" from a different year is a wrong answer
              // rather than a terse one. Same format as the day header and the
              // desktop date column.
              <span className="shrink-0 text-xs text-muted-foreground">
                {format(parseISO(transaction.date), 'MMM d, yyyy')}
              </span>
            )}
            <CategoryBadge
              category={{
                id: transaction.category_id,
                name: transaction.category_name,
              }}
            />
            {tags.map((tag, i) => (
              <Badge
                key={`${tag}-${i}`}
                variant="secondary"
                className="shrink-0 font-normal"
              >
                {tag}
              </Badge>
            ))}
          </div>
          {/* The creator idiom, identical to the table row's — the ledger is
              household-wide, so a member needs to know a row is her spouse's
              BEFORE she edits it and gets a 403. It stays on the collapsed
              card for that reason: moving it into the edit sheet would put a
              member's only warning behind the action it warns about. */}
          <CreatorLabel
            createdBy={transaction.created_by}
            createdByUsername={transaction.created_by_username}
          />
        </div>
        <AmountDisplay
          amount={transaction.amount}
          originalAmount={transaction.original_amount}
          originalCurrency={transaction.original_currency}
          type={transaction.category_type}
          baseCode={baseCode}
          className="shrink-0"
        />
      </button>
    </li>
  );
}
