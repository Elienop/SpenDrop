import { useRef } from 'react';
import { toast } from 'sonner';
import { Trash2, User, WifiOff } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { api } from '@/api/client';
import { cn } from '@/lib/utils';
import { displayAmount, formatSignedCurrency } from '@/lib/format';
import { AmountSignNote } from '@/components/AmountSignNote';
import {
  enqueue,
  removeQueued,
  type QueuedTransaction,
} from '@/lib/offline-queue';
import { useOnline } from '@/hooks/useOnline';
import { useRecentTransactions } from '@/hooks/useRecentTransactions';
import { TYPE_EXPENSE, type TransactionType } from '@/lib/transaction-types';
import { UNDO_TOAST_MS } from '@/lib/undo';
import type { Category } from '@/api/types';
import type { CreateTransactionInput } from '@/hooks/useTransactions';

const RECENT_LIMIT = 5;
const MAX_ROWS = 6;

interface RecentRow {
  key: string;
  kind: 'pending' | 'saved';
  refId: number;
  amount: number;
  currency: string;
  description: string;
  categoryName: string;
  /** Income vs expense, so the amount renders with the same +/− + color cue
   *  as the ledger (AmountDisplay) instead of a kind-ambiguous bare magnitude. */
  type: TransactionType;
  /**
   * Who entered a SAVED row (`Transaction.created_by`, a display name). Absent
   * on pending rows: those are queued locally by the person reading the panel
   * and the queue payload carries no creator, so there is nobody to name.
   * Empty string is the wire's "creator account is gone" value, so the render
   * branches on `kind` — never on this being truthy.
   */
  createdBy?: string;
  /** Present on pending rows so Undo re-queues the exact captured payload. */
  payload?: CreateTransactionInput;
}

interface RecentlyAddedProps {
  /**
   * The authenticated user's id — the offline queue is namespaced per user, so
   * pending-row delete/undo must target this user's database.
   */
  userId: number;
  /** The offline write queue (from useOfflineQueue) — not yet on the server. */
  pending: QueuedTransaction[];
  /** Used to resolve a pending row's category id to a name. */
  categories: Category[];
  /** Household base currency, the fallback when a row has no original currency. */
  baseCode: string;
  /** Bumped by the screen after each successful add, to re-pull saved rows. */
  refreshKey: number;
}

/**
 * "Recently added" panel for the /quick capture screen: the last few entries,
 * pending-sync and saved, each with a one-tap delete so a wrong entry can be
 * removed and re-added (no edit — delete + re-add covers it). Deleting a
 * pending row drops it locally (Undo re-queues it); deleting a saved row
 * soft-deletes it on the server (undoable from the toast, recoverable in
 * Trash). Offline, only pending rows show (saved history lives on the
 * server) plus an offline note.
 */
export function RecentlyAdded({
  userId,
  pending,
  categories,
  baseCode,
  refreshKey,
}: RecentlyAddedProps) {
  const online = useOnline();
  const { recent, refetch } = useRecentTransactions(
    RECENT_LIMIT,
    online,
    refreshKey,
  );
  const headingRef = useRef<HTMLHeadingElement>(null);

  const nameOf = (id: number): string =>
    categories.find((c) => c.id === id)?.name ?? '';

  // A pending row's kind is derived from its category (the same source of
  // truth the backend uses); fall back to expense when the category is unknown.
  const typeOf = (id: number): TransactionType =>
    categories.find((c) => c.id === id)?.type ?? TYPE_EXPENSE;

  // Pending entries are always the newest (they exist only because they
  // couldn't be sent yet), so list them first, then the saved rows in the
  // server's date-desc order. Nothing refetches unsolicited, so the list never
  // reshuffles between the moment the user reads a row and taps its delete.
  const pendingRows: RecentRow[] = [...pending]
    .sort((a, b) => b.queuedAt - a.queuedAt || b.id - a.id)
    .map((q) => ({
      key: `q-${q.id}`,
      kind: 'pending',
      refId: q.id,
      amount: q.payload.original_amount ?? q.payload.amount,
      currency: q.payload.original_currency ?? baseCode,
      description: q.payload.description,
      categoryName: nameOf(q.payload.category_id),
      type: typeOf(q.payload.category_id),
      payload: q.payload,
    }));

  const savedRows: RecentRow[] = recent.map((t) => ({
    key: `t-${t.id}`,
    kind: 'saved',
    refId: t.id,
    amount: t.original_amount ?? t.amount,
    currency: t.original_currency ?? baseCode,
    description: t.description,
    categoryName: t.category_name,
    type: t.category_type,
    createdBy: t.created_by,
  }));

  const rows = [...pendingRows, ...savedRows].slice(0, MAX_ROWS);

  // Keep keyboard/screen-reader focus inside the panel after a row unmounts
  // (otherwise focus falls to <body>). The toast announces the result.
  //
  // This is why the panel renders its own empty state instead of bailing with
  // `return null` on an empty list: deleting the LAST row would unmount the
  // whole section, headingRef with it, and this call would land on nothing —
  // stranding focus on <body> at the exact moment the Undo toast is offered,
  // so the keyboard path to Undo is gone too.
  const restoreFocus = (): void => headingRef.current?.focus();

  const handleDelete = (row: RecentRow): void => {
    if (row.kind === 'pending') {
      const { payload } = row;
      void removeQueued(userId, row.refId)
        .then(() => {
          toast.success('Removed', {
            duration: UNDO_TOAST_MS,
            action: payload
              ? {
                  label: 'Undo',
                  onClick: () =>
                    void enqueue(userId, payload).catch(() =>
                      toast.error('Could not restore'),
                    ),
                }
              : undefined,
          });
        })
        .catch(() => toast.error('Could not remove'));
    } else {
      const savedId = row.refId;
      void api
        .del(`transactions/${savedId}`)
        .then(() => {
          refetch();
          // Undo restores the row in place; past the toast window the row
          // is still recoverable from Trash (own rows, both roles).
          toast.success('Moved to Trash', {
            duration: UNDO_TOAST_MS,
            action: {
              label: 'Undo',
              onClick: () =>
                void api
                  .post(`transactions/${savedId}/restore`, {})
                  .then(() => refetch())
                  .then(() => toast.success('Restored'))
                  // Surface the server's message: the content-hash 409's
                  // copy is written for users and says how to recover.
                  .catch((err: unknown) =>
                    toast.error(
                      err instanceof Error ? err.message : 'Could not restore',
                    ),
                  ),
            },
          });
        })
        .catch((err: unknown) =>
          toast.error(err instanceof Error ? err.message : 'Could not delete'),
        );
    }
    restoreFocus();
  };

  return (
    <section aria-label="Recently added" className="flex flex-col gap-2">
      <h2
        ref={headingRef}
        tabIndex={-1}
        className="text-sm font-medium text-muted-foreground outline-none"
      >
        Recently added
      </h2>
      {rows.length === 0 ? (
        // Deliberately NOT a live region. A role="status" that mounts at the
        // same moment its text appears mostly does not announce at all, and
        // when it does it doubles up against the delete toast. The real
        // signals are the toast (what happened) and the heading taking focus
        // (where you are); this line is what you read once you get there.
        <p className="rounded-lg border border-border px-3 py-6 text-center text-sm text-muted-foreground">
          No recent entries.
        </p>
      ) : (
        <ul className="flex flex-col divide-y divide-border overflow-hidden rounded-lg border border-border">
          {rows.map((row) => (
            <li key={row.key} className="flex items-center gap-3 py-2 pl-3 pr-1">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  {/* Sign and colour follow the DISPLAYED value, not the row
                      type — see `displayAmount`. This panel lists pending
                      offline rows as well as saved ones, and a queued refund
                      has to read the same here as it will once it lands. */}
                  {/* BEFORE the figure, the order `AmountDisplay` pins for the
                      same pair: the correction has to be heard before the
                      number it corrects, not after it. */}
                  <AmountSignNote
                    amount={row.amount}
                    type={row.type}
                    className="shrink-0"
                  />
                  <span
                    className={cn(
                      'shrink-0 font-mono text-sm font-medium tabular-nums',
                      displayAmount(row.amount, row.type) > 0 &&
                        'text-emerald-500',
                    )}
                  >
                    {formatSignedCurrency(
                      displayAmount(row.amount, row.type),
                      row.currency,
                    )}
                  </span>
                  <span className="truncate text-sm">
                    {row.description || '—'}
                  </span>
                  {row.kind === 'pending' && (
                    <Badge variant="secondary" className="shrink-0">
                      Not synced
                    </Badge>
                  )}
                </div>
                {row.categoryName && (
                  <p className="truncate text-xs text-muted-foreground">
                    {row.categoryName}
                  </p>
                )}
                {/* Same attribution idiom as the ledger row (TransactionRow):
                    the panel lists the household's recent entries, not just
                    this device's, so a row here can be the other member's and
                    has to say so without a hover or a tap. Its own line rather
                    than sharing the category's, so neither text starves the
                    other of width on a 390px phone.

                    Saved rows only — a pending row has not left this device,
                    so its creator is by construction whoever is reading the
                    panel. Keyed on `kind`, not on the name being non-empty:
                    "" is the wire's orphaned-creator value and must still
                    render the fallback. */}
                {row.kind === 'saved' && (
                  <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
                    <User className="size-3.5 shrink-0" aria-hidden="true" />
                    <span className="min-w-0 truncate">
                      <span className="sr-only">Entered by </span>
                      {row.createdBy || 'Unknown'}
                    </span>
                  </p>
                )}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="size-11 shrink-0 text-muted-foreground"
                aria-label={
                  row.kind === 'pending'
                    ? `Delete unsynced entry ${row.description || ''}`.trim()
                    : `Delete ${row.description || 'entry'}`
                }
                onClick={() => handleDelete(row)}
              >
                <Trash2 />
              </Button>
            </li>
          ))}
        </ul>
      )}
      {!online && (
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <WifiOff className="size-3.5 shrink-0" aria-hidden="true" />
          You're offline — saved history will appear when you reconnect.
        </p>
      )}
    </section>
  );
}
