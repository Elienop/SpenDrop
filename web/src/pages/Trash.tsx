import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { differenceInCalendarDays, format, parseISO } from 'date-fns';
import { formatDistanceToNowStrict } from 'date-fns';
import { toast } from 'sonner';
import { AlertCircle, RotateCcw, Trash2, User } from 'lucide-react';
import { api } from '../api/client';
import type {
  DeletedTransaction,
  DeletedTransactionList,
} from '../api/types';
import { useAuth } from '../hooks/useAuth';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useIsMobileViewport } from '@/hooks/useIsMobileViewport';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { AmountDisplay } from '@/components/AmountDisplay';
import { PaginationBar } from '@/components/PaginationBar';
import { CategoryBadge } from '@/components/CategoryBadge';
import { formatCurrency } from '@/lib/format';
import { isAdmin } from '@/lib/roles';
import { TOUCH_TARGET_CHECKBOX } from '@/lib/touch-target';
import { transactionLabel } from '@/lib/transaction-label';
import { cn } from '@/lib/utils';
import { TYPE_EXPENSE } from '@/lib/transaction-types';
import { DEFAULT_TRANSACTIONS_PER_PAGE } from '@/lib/constants';

/*
 * Trash view — recovery surface for soft-deleted transactions. Members
 * see and restore their own rows; admins see the household and can
 * purge.
 *
 * Paired endpoints on the backend:
 *   GET    /api/transactions/deleted           paginated list
 *   POST   /api/transactions/{id}/restore      single restore
 *   DELETE /api/transactions/{id}/purge        single hard-delete
 *   POST   /api/transactions/restore-batch     bulk restore (<=500 ids)
 *   POST   /api/transactions/restore-all       restore every tombstoned row
 *   DELETE /api/transactions/trash             hard-delete every tombstoned row
 *
 * List, single restore, and batch restore are member-reachable and
 * server-scoped to the caller's own rows (see backend Tasks 2–4).
 * Purge, restore-all, and purge-all are admin routes — the frontend
 * hides those controls for members below, but the 403 the backend
 * would return is the real boundary; the UI gating is purely cosmetic.
 *
 * Design notes:
 *   - Two presentations, one set of state. Below Tailwind's `md` the
 *     eight-column table is replaced by a stacked card list
 *     (<TrashCard>); from `md` up the table is what renders. The choice
 *     is made in JS (`useIsMobileViewport`, shared with the transactions
 *     list) so only one of them is ever in the tree — see that hook for
 *     why not `md:hidden`. Everything either half touches (selection,
 *     pagination, the confirm dialogs, the bulk toolbar) is shared and
 *     width-agnostic.
 *   - No filters, no sorting. The backend pins ordering to
 *     `deleted_at DESC` because the common case is "I just nuked the
 *     wrong filter, undo it NOW" — anything else is noise on the
 *     recovery path.
 *   - Restore is treated as reversible (you can always re-delete) and
 *     fires without a confirm dialog; purge is irreversible and always
 *     walks through <ConfirmPurgeDialog> / <ConfirmPurgeAllDialog>.
 *   - Page-scoped batch restore (checkbox selection) is allowed; page-
 *     scoped batch purge is deliberately NOT exposed — the plan spec
 *     omits it because "restore many at once, purge one at a time" is
 *     the right friction profile at the row level. The "Purge all"
 *     button (rendered into the table toolbar next to "Rows per page"
 *     via the PaginationBar `leadingActions` slot) exists because
 *     wiping the whole trash is an operator intent in its own right
 *     (distinct from "purge these three rows I picked"), and the
 *     confirm dialog gives it teeth.
 */

/**
 * The age at which a tombstone stops reading as an age and starts reading
 * as a date. Below it the operator is triaging a mistake they remember
 * making, and "3 days ago" is the fastest thing to scan. At or above it
 * they are looking for a specific deletion, `formatDistanceToNowStrict`
 * has gone vague ("2 months ago" locates nothing), and the absolute date
 * is the only useful answer.
 */
const RELATIVE_DELETED_AT_MAX_DAYS = 7;

/**
 * Render a tombstoned timestamp as a relative phrase, an absolute label,
 * and the phrase the card actually prints after the word "Deleted".
 *
 * `phrase` exists because the absolute date cannot live only in `title=`
 * on this surface: tooltips never open on touch, and the phone card is the
 * primary way this page gets used. Rather than always printing the date —
 * which is noise in the fresh-mistake case the page exists for — it
 * age-gates on RELATIVE_DELETED_AT_MAX_DAYS. `title` keeps the full
 * date-time in both cases for anyone with a pointer.
 */
function formatDeletedAt(iso: string): {
  relative: string;
  absolute: string;
  phrase: string;
} {
  const d = new Date(iso);
  // `formatDistanceToNowStrict` avoids the "about 3 minutes" / "less than
  // a minute" soft phrasing that the non-strict variant produces. On a
  // recovery surface the operator wants a crisp number, not "about".
  const relative = formatDistanceToNowStrict(d, { addSuffix: true });
  const absolute = format(d, 'PPpp');
  // Calendar days, not elapsed 24h periods: "deleted 6 days ago" should
  // flip to a date on the same calendar boundary the operator perceives,
  // not at whatever hour the deletion happened to occur.
  const age = differenceInCalendarDays(new Date(), d);
  const phrase =
    age < RELATIVE_DELETED_AT_MAX_DAYS
      ? relative
      : `on ${format(d, 'MMM d, yyyy')}`;
  return { relative, absolute, phrase };
}

function TrashSkeleton() {
  return (
    <Card className="overflow-hidden">
      <div className="flex flex-col gap-2 p-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    </Card>
  );
}


interface TrashCardProps {
  row: DeletedTransaction;
  selected: boolean;
  isRestoring: boolean;
  rowBusy: boolean;
  admin: boolean;
  baseCurrency: string;
  onSelect: (id: number, checked: boolean) => void;
  onRestore: (row: DeletedTransaction) => void;
  onPurge: (row: DeletedTransaction) => void;
}

/**
 * One tombstoned row as a stacked card — the below-`md` presentation of
 * the same data the table renders to the right of this file. Eight
 * columns do not survive a 390px viewport: the Date and Deleted columns
 * alone eat half of it, and Restore (the whole point of the surface)
 * ends up off-screen behind a horizontal scroll.
 *
 * Everything here is a re-arrangement, not a re-derivation — the amount
 * sign/colour, the creator idiom and the description fallback are the
 * table's expressions verbatim, so the two presentations cannot drift
 * into disagreeing about the same row.
 *
 * Restore stays a visible button rather than collapsing into a menu:
 * there is no detail view to tap through to on this surface, so a hidden
 * primary action would leave the page with no reachable purpose.
 *
 * Deliberate differences from <TransactionCard>, so a future reviewer
 * reads them as decisions rather than drift. They share the creator idiom
 * and <AmountDisplay> verbatim; everything below diverges because the two
 * surfaces answer different questions.
 *
 *   - ORDERING. The ledger card leads with what you spent. This one leads
 *     with the description too, but carries a deletion age its sibling has
 *     no field for, and that line is what the operator scans — "which of
 *     these did I just nuke?" — so it sits last, closest to the actions.
 *   - DENSITY. This card is looser (an actions row of its own rather than
 *     a tap-through). The ledger card opens an edit sheet on tap, so its
 *     whole body is one target and it can afford to be tight; here the two
 *     actions are irreversible-ish and must be aimed at individually, so
 *     they get their own 44px row instead of sharing the body's tap area.
 *   - TAGS WRAP. The ledger truncates tags to one line to keep row height
 *     uniform for scanning a long list. Trash wraps them: a page holds far
 *     fewer rows, uniform height buys nothing here, and a hidden tag is
 *     exactly the detail that distinguishes two otherwise identical rows
 *     you are deciding between.
 */
function TrashCard({
  row,
  selected,
  isRestoring,
  rowBusy,
  admin,
  baseCurrency,
  onSelect,
  onRestore,
  onPurge,
}: TrashCardProps) {
  const { absolute, phrase } = formatDeletedAt(row.deleted_at);

  return (
    <li
      className={cn(
        'flex flex-col gap-3 p-4',
        selected && 'bg-muted/50',
      )}
    >
      <div className="flex items-start gap-3">
        <Checkbox
          checked={selected}
          onCheckedChange={(v) => onSelect(row.id, v === true)}
          disabled={rowBusy}
          aria-label={`Select ${transactionLabel(row)}`}
          className={cn('mt-1', TOUCH_TARGET_CHECKBOX)}
        />
        {/* min-w-0 is what lets the descendants' `truncate` do anything:
            a flex item defaults to min-width:auto and would rather push
            the amount off the card than clip a long imported
            description. */}
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex items-start justify-between gap-3">
            <div
              className="min-w-0 truncate font-medium"
              title={row.description || undefined}
            >
              {row.description || (
                <span className="text-muted-foreground">
                  (no description)
                </span>
              )}
            </div>
            {/*
              The ledger's <AmountDisplay>, deliberately NOT the table's
              bare base-currency figure. The table has never shown the
              original currency, and copying that here was the wrong
              parity to keep: this household books in LBP and USD daily,
              so a card showing only the converted figure hides the number
              the row was actually entered as — on the one surface where
              you decide whether it was the row you meant to delete.
            */}
            <AmountDisplay
              amount={row.amount}
              originalAmount={row.original_amount}
              originalCurrency={row.original_currency}
              type={row.category_type}
              baseCode={baseCurrency}
              className="shrink-0"
            />
          </div>
          <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
            <User className="size-3.5 shrink-0" aria-hidden="true" />
            <span className="min-w-0 truncate">
              {/* A bare name in a muted line does not announce
                  what it IS, and the icon is aria-hidden
                  decoration. */}
              <span className="sr-only">Entered by </span>
              {row.created_by || 'Unknown'}
            </span>
          </p>
          <div className="flex flex-wrap items-center gap-1">
            <CategoryBadge
              category={{ id: row.category_id, name: row.category_name }}
            />
            {row.tags &&
              row.tags.split(',').map((tag, i) => (
                <Badge
                  key={`${tag.trim()}-${i}`}
                  variant="secondary"
                  className="font-normal"
                >
                  {tag.trim()}
                </Badge>
              ))}
          </div>
          {/*
            The table's Date and Deleted columns, merged into one muted
            line. Both facts have to survive the fold: the transaction
            date is how you tell two similar rows apart, and the deletion
            age is how you find the batch you just fat-fingered. "Deleted"
            is spelled out because, unlike in the table, there is no
            column header left to say which date is which.
          */}
          <p
            className="text-xs text-muted-foreground"
            title={absolute}
          >
            {format(parseISO(row.date), 'MMM d, yyyy')} · Deleted {phrase}
          </p>
        </div>
      </div>
      {/*
        Full-width actions on their own line rather than squeezed beside
        the amount: `min-h-11` is the 44px touch floor, and two of these
        plus a 16px gutter do not fit next to a right-aligned amount at
        390px.

        Kept even though `Button` now floors these on a coarse pointer, because
        the two answer different questions and only overlap: this card renders
        only below `md`, and at those widths it is 44px for a MOUSE too — which
        is the density the owner asked to leave exactly as it was. Safe to have
        both, unlike the `md:min-h-0` half these controls used to carry: same
        property, same specificity, and `md:` is emitted AFTER `coarse:` in the
        built stylesheet (measured: offsets 52141 vs 49393), so that pair did
        not merely look ambiguous — it DEFEATED the primitive's floor on the
        tablet. Two rules agreeing on 44px cannot.
      */}
      <div className="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="min-h-11 flex-1 gap-1.5"
          onClick={() => onRestore(row)}
          disabled={rowBusy}
          aria-label={`Restore ${transactionLabel(row)}`}
        >
          <RotateCcw aria-hidden="true" />
          {isRestoring ? 'Restoring...' : 'Restore'}
        </Button>
        {admin && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="min-h-11 flex-1 gap-1.5 text-destructive hover:text-destructive"
            onClick={() => onPurge(row)}
            disabled={rowBusy}
            aria-label={`Purge ${transactionLabel(row)}`}
          >
            <Trash2 aria-hidden="true" />
            Purge
          </Button>
        )}
      </div>
    </li>
  );
}

interface ConfirmPurgeDialogProps {
  row: DeletedTransaction | null;
  onCancel: () => void;
  onConfirm: () => void;
  busy: boolean;
}

function ConfirmPurgeDialog({
  row,
  onCancel,
  onConfirm,
  busy,
}: ConfirmPurgeDialogProps) {
  const open = row !== null;
  return (
    <Dialog
      open={open}
      onOpenChange={(isOpen) => {
        // Block closing while the purge is in-flight so the user can't
        // dismiss the spinner and lose track of what's in-progress.
        if (busy) return;
        if (!isOpen) onCancel();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Permanently delete this transaction?</DialogTitle>
          <DialogDescription>
            {row ? (
              <>
                This will hard-delete{' '}
                <span className="font-semibold text-foreground">
                  {row.description || '(no description)'}
                </span>{' '}
                from the database. This cannot be undone — restore is no
                longer possible after purge.
              </>
            ) : (
              'Loading...'
            )}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            className="max-md:min-h-11"
            onClick={onCancel}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            className="max-md:min-h-11"
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? 'Purging...' : 'Purge permanently'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface ConfirmPurgeAllDialogProps {
  open: boolean;
  count: number;
  onCancel: () => void;
  onConfirm: () => void;
  busy: boolean;
}

/**
 * Confirm dialog for the whole-trash purge action. Separate from
 * <ConfirmPurgeDialog> (which takes a specific row) because the copy
 * is fundamentally different: this wipes every tombstoned row in the
 * database, not one the operator picked. The count is rendered into
 * the body so the user sees exactly how many rows they're about to
 * annihilate before confirming.
 */
function ConfirmPurgeAllDialog({
  open,
  count,
  onCancel,
  onConfirm,
  busy,
}: ConfirmPurgeAllDialogProps) {
  return (
    <Dialog
      open={open}
      onOpenChange={(isOpen) => {
        // Same block-while-busy guard as the single-row dialog so the
        // operator can't dismiss the spinner mid-purge.
        if (busy) return;
        if (!isOpen) onCancel();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Permanently delete everything in the trash?</DialogTitle>
          <DialogDescription>
            This will hard-delete{' '}
            <span className="font-semibold text-foreground">
              {count} transaction{count === 1 ? '' : 's'}
            </span>{' '}
            from the database. This cannot be undone — restore is no
            longer possible after purge.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            className="max-md:min-h-11"
            onClick={onCancel}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            className="max-md:min-h-11"
            onClick={onConfirm}
            disabled={busy}
          >
            {/*
              Distinct from the toolbar "Purge all N" trigger button so
              the accessible name is unambiguous while the dialog is
              mounted. Matches the single-row dialog's "Purge
              permanently" pattern.
            */}
            {busy ? 'Purging...' : 'Purge all permanently'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function Trash() {
  const { user, loading: authLoading } = useAuth();
  const baseCurrency = useBaseCurrency();
  const isMobile = useIsMobileViewport();
  const mobileSelectAllId = useId();

  const [rows, setRows] = useState<DeletedTransaction[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_TRANSACTIONS_PER_PAGE);
  const [initialLoad, setInitialLoad] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Per-row in-flight flags. Using Sets (not a single boolean) so the
  // user can click Restore on row A and Purge on row B without one
  // disabling the other — the Set membership tracks "is this specific
  // row busy right now?" rather than "is the whole page busy?".
  const [restoringIds, setRestoringIds] = useState<Set<number>>(new Set());
  const [purgingId, setPurgingId] = useState<number | null>(null);
  const [pendingPurge, setPendingPurge] = useState<DeletedTransaction | null>(
    null,
  );

  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
  const [batchRestoring, setBatchRestoring] = useState(false);

  // Whole-trash bulk actions. `restoringAll` and `purgingAll` gate the
  // header buttons (and each other — you can't run both at once), while
  // `pendingPurgeAll` drives the destructive confirm dialog. Restore-
  // all fires directly without a dialog, matching the reversible-op
  // convention used by every other restore entry point on this page.
  const [restoringAll, setRestoringAll] = useState(false);
  const [purgingAll, setPurgingAll] = useState(false);
  const [pendingPurgeAll, setPendingPurgeAll] = useState(false);

  const admin = isAdmin(user);

  const fetchTrash = useCallback(async () => {
    setError(null);
    try {
      const data = await api.get<DeletedTransactionList>(
        `transactions/deleted?page=${page}&per_page=${perPage}`,
      );
      setRows(data.transactions);
      setTotal(data.total);
      // Prune stale selection — if a row just got restored/purged or
      // paged off screen, we don't want the batch toolbar to keep
      // counting it.
      setSelectedIds((prev) => {
        const live = new Set(data.transactions.map((r) => r.id));
        const next = new Set<number>();
        for (const id of prev) {
          if (live.has(id)) next.add(id);
        }
        return next;
      });
      // If a mutation (restore/purge/batch) just drained the current
      // page but earlier pages still hold rows, step back one page so
      // the operator doesn't land on a false "Trash is empty" card
      // with no way to navigate. The setPage call schedules another
      // fetch via the useEffect below, which is cheap.
      if (data.transactions.length === 0 && page > 1) {
        setPage((p) => Math.max(1, p - 1));
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load trash');
    } finally {
      setInitialLoad(false);
    }
  }, [page, perPage]);

  useEffect(() => {
    if (!user) return;
    void fetchTrash();
  }, [user, fetchTrash]);

  const totalPages = useMemo(
    () => Math.max(1, Math.ceil(total / perPage)),
    [total, perPage],
  );

  const handlePageChange = useCallback((next: number) => {
    setPage(next);
  }, []);

  const handlePerPageChange = useCallback((next: number) => {
    setPerPage(next);
    setPage(1);
  }, []);

  const handleRestore = useCallback(
    async (row: DeletedTransaction) => {
      setRestoringIds((prev) => {
        const next = new Set(prev);
        next.add(row.id);
        return next;
      });
      try {
        await api.post(`transactions/${row.id}/restore`);
        toast.success(
          `Restored "${row.description || '(no description)'}"`,
        );
        await fetchTrash();
      } catch (err) {
        toast.error(
          err instanceof Error ? err.message : 'Failed to restore transaction',
        );
      } finally {
        setRestoringIds((prev) => {
          const next = new Set(prev);
          next.delete(row.id);
          return next;
        });
      }
    },
    [fetchTrash],
  );

  const handlePurge = useCallback(
    async (row: DeletedTransaction) => {
      setPurgingId(row.id);
      try {
        await api.del(`transactions/${row.id}/purge`);
        toast.success(
          `Purged "${row.description || '(no description)'}"`,
        );
        setPendingPurge(null);
        await fetchTrash();
      } catch (err) {
        toast.error(
          err instanceof Error ? err.message : 'Failed to purge transaction',
        );
      } finally {
        setPurgingId(null);
      }
    },
    [fetchTrash],
  );

  const handleBatchRestore = useCallback(async () => {
    if (selectedIds.size === 0) return;
    const ids = [...selectedIds];
    setBatchRestoring(true);
    try {
      const resp = await api.post<{
        restored: number;
        conflicted: number;
        skipped: number;
      }>('transactions/restore-batch', { ids });
      // skipped > 0 only happens to a member: a selected row was purged
      // or restored (by someone else, or on another tab) between page
      // load and this click, so the ownership check no longer finds it
      // as hers to restore. Compose with conflicted rather than
      // overwrite it — both can be nonzero in the same batch.
      const notes: string[] = [];
      if (resp.conflicted > 0) {
        notes.push(
          `${resp.conflicted} could not be restored (a newer copy already exists)`,
        );
      }
      if (resp.skipped > 0) {
        notes.push(`${resp.skipped} skipped (no longer in your trash)`);
      }
      if (notes.length > 0) {
        toast.success(`Restored ${resp.restored} — ${notes.join(', ')}`);
      } else {
        toast.success(
          `Restored ${resp.restored} transaction${resp.restored === 1 ? '' : 's'}`,
        );
      }
      setSelectedIds(new Set());
      await fetchTrash();
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to restore batch',
      );
    } finally {
      setBatchRestoring(false);
    }
  }, [selectedIds, fetchTrash]);

  // Whole-trash restore. No confirm dialog — restoring is reversible
  // (you can always re-delete) and the backend tolerates an empty
  // trash by returning 0, so an accidental click is cheap to undo.
  const handleRestoreAll = useCallback(async () => {
    setRestoringAll(true);
    try {
      const resp = await api.post<{ restored: number; conflicted: number }>(
        'transactions/restore-all',
      );
      if (resp.conflicted > 0) {
        toast.success(
          `Restored ${resp.restored} — ${resp.conflicted} could not be restored (a newer copy already exists)`,
        );
      } else {
        toast.success(
          `Restored ${resp.restored} transaction${resp.restored === 1 ? '' : 's'}`,
        );
      }
      // Drop any row-level selection state; the rows it pointed at are
      // about to disappear from the list anyway.
      setSelectedIds(new Set());
      await fetchTrash();
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to restore all',
      );
    } finally {
      setRestoringAll(false);
    }
  }, [fetchTrash]);

  // Whole-trash purge. Always walks through ConfirmPurgeAllDialog —
  // this is the single most destructive action on the page and the
  // confirm button echoes the row count so the operator acknowledges
  // the magnitude before we fire the DELETE.
  const handlePurgeAll = useCallback(async () => {
    setPurgingAll(true);
    try {
      const resp = await api.del<{ purged: number }>('transactions/trash');
      toast.success(
        `Purged ${resp.purged} transaction${resp.purged === 1 ? '' : 's'}`,
      );
      setPendingPurgeAll(false);
      setSelectedIds(new Set());
      await fetchTrash();
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to purge all',
      );
    } finally {
      setPurgingAll(false);
    }
  }, [fetchTrash]);

  const handleSelect = useCallback((id: number, checked: boolean) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (checked) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  }, []);

  // Busy rows (a single restore or purge is mid-flight) have a disabled
  // per-row checkbox, so the header "select all" should also skip them —
  // otherwise the visible disabled row appears selected via the header
  // and the user can accidentally batch-restore a row that already has
  // a single restore in-flight. The filter is recomputed here (and
  // below in `allVisibleSelected`) rather than memoised because `rows`
  // tops out at `MaxPerPage = 100`, so allocating the filtered list on
  // every render is negligible.
  const isRowBusy = useCallback(
    (id: number) => restoringIds.has(id) || purgingId === id,
    [restoringIds, purgingId],
  );

  const handleSelectAll = useCallback(
    (checked: boolean) => {
      if (!checked) {
        setSelectedIds(new Set());
        return;
      }
      setSelectedIds(
        new Set(rows.filter((r) => !isRowBusy(r.id)).map((r) => r.id)),
      );
    },
    [rows, isRowBusy],
  );

  // --- Auth resolution ---
  //
  // Wait for the auth probe to resolve before rendering — otherwise a
  // hard reload on /trash would flash the page with `user` still null
  // before the fetch effect's `!user` gate has had a chance to run.
  // There's no admin/member branch here: the page is member-reachable
  // (backend scopes list/restore per-role), and admin-only controls
  // (Purge, Restore all, Purge all) are conditionally rendered further
  // down instead of gating the whole page.
  if (authLoading) {
    return (
      <div
        role="status"
        aria-label="Loading"
        className="flex min-h-[50vh] items-center justify-center"
      >
        <div className="size-8 animate-spin rounded-full border-[3px] border-border border-t-primary" />
      </div>
    );
  }

  const selectionCount = selectedIds.size;
  // "All selectable rows on this page are selected" — busy rows are
  // excluded from the denominator so the header checkbox matches its
  // disabled per-row cousins after `handleSelectAll`.
  const selectableRows = rows.filter((r) => !isRowBusy(r.id));
  const allVisibleSelected =
    selectableRows.length > 0 &&
    selectableRows.every((r) => selectedIds.has(r.id));

  const bulkBusy = restoringAll || purgingAll;

  // Whole-trash bulk actions (admin only — restore-all and purge are
  // admin routes) rendered into the table toolbar (next to "Rows per
  // page") rather than above the card. Only surfaced when there's
  // actually something in the trash — on an empty trash these buttons
  // would be no-ops that just add visual noise. The buttons mirror the
  // row-level action ordering (restore first, purge second) and the
  // outline/destructive variants so the visual weight matches the
  // consequence. Passed only to the TOP `PaginationBar` so the bottom
  // bar stays a pure pagination strip.
  const bulkActions =
    admin && total > 0 ? (
      <div className="ml-2 flex flex-wrap items-center gap-2 border-l pl-3">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-8 gap-1.5 text-xs max-md:min-h-11"
          onClick={() => void handleRestoreAll()}
          disabled={bulkBusy}
        >
          <RotateCcw aria-hidden="true" />
          {restoringAll ? 'Restoring...' : `Restore all ${total}`}
        </Button>
        <Button
          type="button"
          variant="destructive"
          size="sm"
          className="h-8 gap-1.5 text-xs max-md:min-h-11"
          onClick={() => setPendingPurgeAll(true)}
          disabled={bulkBusy}
        >
          <Trash2 aria-hidden="true" />
          {purgingAll ? 'Purging...' : `Purge all ${total}`}
        </Button>
      </div>
    ) : null;

  // Page-scoped batch actions. Rendered in one of two places depending on
  // width (see both call sites below), so it is built once here rather than
  // duplicated into each branch — two copies would be two sets of accessible
  // names for one selection.
  const selectionBar =
    selectionCount > 0 ? (
      <div className="flex flex-wrap items-center gap-3 border-t bg-muted/50 px-4 py-2">
        <span className="text-sm font-medium">{selectionCount} selected</span>
        <Button
          type="button"
          size="sm"
          className="h-8 gap-1.5 text-xs max-md:min-h-11"
          onClick={() => void handleBatchRestore()}
          disabled={batchRestoring}
        >
          <RotateCcw aria-hidden="true" />
          {batchRestoring ? 'Restoring...' : `Restore ${selectionCount}`}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-8 text-xs max-md:min-h-11"
          onClick={() => setSelectedIds(new Set())}
          disabled={batchRestoring}
        >
          Clear selection
        </Button>
      </div>
    ) : null;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h1 className="text-2xl font-semibold tracking-tight">Trash</h1>
        <p className="max-w-3xl text-sm text-muted-foreground">
          Recently deleted transactions are kept here so a bulk-delete
          mistake can be recovered. Restored rows reappear in the live
          transactions list immediately.
          {admin
            ? ' Purging is permanent — there is no recovery path after that.'
            : ' You see the transactions you deleted yourself; an admin can restore or permanently remove anything in the household trash.'}
        </p>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertCircle className="size-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {/*
        Three mutually-exclusive visual states rendered as sibling
        guards rather than a nested ternary so an error + empty rows
        don't stack a contradictory "Trash is empty" card underneath
        the alert. On a refetch failure with existing data still in
        state, we intentionally keep showing the table so the operator
        doesn't lose their place — the error banner above explains why
        the data is stale.
      */}
      {initialLoad && <TrashSkeleton />}

      {!initialLoad && rows.length === 0 && !error && (
        <Card className="p-6 text-center text-sm text-muted-foreground">
          Trash is empty. Soft-deleted transactions will appear here.
        </Card>
      )}

      {!initialLoad && rows.length > 0 && (
        <Card className="overflow-hidden">
          <PaginationBar
            page={page}
            totalPages={totalPages}
            perPage={perPage}
            onPageChange={handlePageChange}
            onPerPageChange={handlePerPageChange}
            compact={isMobile}
            leadingActions={bulkActions}
          />

          {!isMobile && selectionBar}

          {/*
            --- The presentation fork ---------------------------------
            The eight-column table below cannot be made to fit 390px by
            hiding columns — that is Firefly's approach and the
            documented failure mode — so below `md` it is replaced
            wholesale by stacked cards. Everything above and below this
            fork (selection, pagination, both confirm dialogs, the bulk
            toolbar) is shared and already width-agnostic.

            A flat list, deliberately: the backend pins ordering to
            `deleted_at DESC` with no sort control, so day headers would
            either group by a field that is not the sort key (the
            transaction date, giving headers that jump around) or by the
            deletion day, which every card already carries on its own
            line — and in the common case of trash accumulated one
            mistake at a time, that is one header per row.
          */}
          {isMobile ? (
            <>
              <div className="flex items-center border-t px-4">
                {/*
                  A phone has no column header to hang select-all off, so
                  it gets its own strip. `htmlFor` targets Radix's
                  checkbox button (a labelable element), which makes the
                  whole 44px strip a tap target for it. The fuller
                  `aria-label` wins as the accessible name and contains
                  the visible text, so what a screen reader announces
                  still starts with what the eye reads.
                */}
                <label
                  htmlFor={mobileSelectAllId}
                  className="flex min-h-11 flex-1 items-center gap-3"
                >
                  <Checkbox
                    id={mobileSelectAllId}
                    checked={allVisibleSelected}
                    onCheckedChange={(v) => handleSelectAll(v === true)}
                    aria-label="Select all on this page"
                    className={TOUCH_TARGET_CHECKBOX}
                  />
                  <span className="text-sm font-medium">Select all</span>
                </label>
              </div>

              {/* Tailwind's preflight strips the list-style and Safari
                  drops the list role with it — hence the explicit
                  `role`. */}
              <ul
                role="list"
                aria-label="Deleted transactions"
                className="flex flex-col divide-y border-t"
              >
                {rows.map((row) => (
                  <TrashCard
                    key={row.id}
                    row={row}
                    selected={selectedIds.has(row.id)}
                    isRestoring={restoringIds.has(row.id)}
                    rowBusy={isRowBusy(row.id)}
                    admin={admin}
                    baseCurrency={baseCurrency}
                    onSelect={handleSelect}
                    onRestore={(r) => void handleRestore(r)}
                    onPurge={setPendingPurge}
                  />
                ))}
              </ul>
            </>
          ) : (
            <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <Checkbox
                    checked={allVisibleSelected}
                    onCheckedChange={(v) => handleSelectAll(v === true)}
                    aria-label="Select all on this page"
                  />
                </TableHead>
                <TableHead>Deleted</TableHead>
                <TableHead>Date</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Category</TableHead>
                <TableHead>Tags</TableHead>
                <TableHead className="text-right">Amount</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => {
                const { relative, absolute } = formatDeletedAt(row.deleted_at);
                const isRestoring = restoringIds.has(row.id);
                const isPurging = purgingId === row.id;
                const rowBusy = isRestoring || isPurging;
                return (
                  <TableRow
                    key={row.id}
                    className={cn(
                      'hover:bg-muted/40',
                      selectedIds.has(row.id) && 'bg-muted/50',
                    )}
                  >
                    <TableCell className="w-10">
                      <Checkbox
                        checked={selectedIds.has(row.id)}
                        onCheckedChange={(v) =>
                          handleSelect(row.id, v === true)
                        }
                        disabled={rowBusy}
                        aria-label={`Select ${transactionLabel(row)}`}
                      />
                    </TableCell>
                    <TableCell
                      className="whitespace-nowrap text-sm text-muted-foreground"
                      title={absolute}
                    >
                      {relative}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      {format(parseISO(row.date), 'MMM d, yyyy')}
                    </TableCell>
                    {/* Width-bounded on purpose, exactly as in
                        TransactionRow's description cell: import does not
                        enforce the 500-character description limit the rest of
                        the app does — validateImportField runs only on the
                        per-row edit route — so a spreadsheet cell can put a
                        far longer description into the ledger, and deleting
                        such a row lands it HERE. Unbounded, one of them
                        stretches the table for every row and pushes the
                        Actions column — Restore and Purge, the whole point of
                        the surface — off the right edge behind a horizontal
                        scroll. (Phone width is no longer this cell's problem:
                        below `md` the card list renders instead, and bounds
                        the same text its own way.) title= keeps the full text
                        on hover; max-w-md plus the inner truncate is what
                        makes either truncate do anything at all.

                        The creator rides UNDER the description rather than in
                        a column of its own: a ninth always-on column would
                        cost width this table does not have, and tooltips are
                        dead on touch. It matters more here than in the ledger
                        — an admin's trash spans the whole household, and
                        Restore/Purge are poor things to aim at a row you
                        cannot attribute. */}
                    <TableCell className="max-w-md">
                      <div
                        className="truncate font-medium"
                        title={row.description || undefined}
                      >
                        {row.description || (
                          <span className="text-muted-foreground">
                            (no description)
                          </span>
                        )}
                      </div>
                      <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
                        <User className="size-3.5 shrink-0" aria-hidden="true" />
                        <span className="min-w-0 truncate">
                          {/* A bare name in a muted line does not announce
                              what it IS, and the icon is aria-hidden
                              decoration. */}
                          <span className="sr-only">Entered by </span>
                          {row.created_by || 'Unknown'}
                        </span>
                      </p>
                    </TableCell>
                    <TableCell>
                      <CategoryBadge
                        category={{
                          id: row.category_id,
                          name: row.category_name,
                        }}
                      />
                    </TableCell>
                    <TableCell>
                      {row.tags &&
                        row.tags.split(',').map((tag, i) => (
                          <Badge
                            key={`${tag.trim()}-${i}`}
                            variant="secondary"
                            className="mr-1 font-normal"
                          >
                            {tag.trim()}
                          </Badge>
                        ))}
                    </TableCell>
                    <TableCell
                      className={cn(
                        'whitespace-nowrap text-right font-mono tabular-nums',
                        row.category_type === TYPE_EXPENSE
                          ? 'text-foreground'
                          : 'text-emerald-500',
                      )}
                    >
                      {row.category_type === TYPE_EXPENSE ? '-' : '+'}
                      {formatCurrency(row.amount, baseCurrency)}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="h-8 gap-1.5 text-xs"
                          onClick={() => void handleRestore(row)}
                          disabled={rowBusy}
                          aria-label={`Restore ${transactionLabel(row)}`}
                        >
                          <RotateCcw aria-hidden="true" />
                          {isRestoring ? 'Restoring...' : 'Restore'}
                        </Button>
                        {admin && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            className="h-8 gap-1.5 text-xs text-destructive hover:text-destructive"
                            onClick={() => setPendingPurge(row)}
                            disabled={rowBusy}
                            aria-label={`Purge ${transactionLabel(row)}`}
                          >
                            <Trash2 aria-hidden="true" />
                            Purge
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
            </Table>
          )}

          {isMobile && selectionBar && (
            // In flow, so it contributes height and occludes nothing; sticky,
            // so it stays on screen while the list scrolls under it. Sits
            // before the pager, so reaching the pager is exactly what releases
            // it. No shadow — app-chrome bars in this repo separate with a
            // border.
            //
            // Carries NO border of its own: the strip inside already has
            // `border-t`, and the two would sit flush against each other and
            // render as one 2px line. (The Transactions bar does carry one,
            // because its `p-2` gutter holds a rounded floating card away from
            // it — two separate lines by design. This strip is full-bleed, so
            // the same pair reads as a mistake.)
            //
            // `pb` only, and `max()` not a bare `env()`: the strip's own `py-2`
            // supplies the 8px above the safe area, and the max() floor keeps
            // that same 8px on a device reporting no inset — which is what
            // makes the rendered bottom spacing equal to the Transactions bar's
            // (its `py-2` + its `p-2` bottom) rather than half of it. No `p-2`
            // here: that gutter belongs to a floating card, and adding it would
            // inset a strip meant to run edge to edge.
            <div className="sticky bottom-0 z-20 bg-background pb-[max(0.5rem,env(safe-area-inset-bottom))]">
              {selectionBar}
            </div>
          )}

          <div className="border-t">
            <PaginationBar
              page={page}
              totalPages={totalPages}
              perPage={perPage}
              onPageChange={handlePageChange}
              onPerPageChange={handlePerPageChange}
            compact={isMobile}
            />
          </div>
        </Card>
      )}

      <ConfirmPurgeDialog
        row={pendingPurge}
        busy={purgingId !== null}
        onCancel={() => setPendingPurge(null)}
        onConfirm={() => {
          if (pendingPurge) void handlePurge(pendingPurge);
        }}
      />

      <ConfirmPurgeAllDialog
        open={pendingPurgeAll}
        count={total}
        busy={purgingAll}
        onCancel={() => setPendingPurgeAll(false)}
        onConfirm={() => void handlePurgeAll()}
      />
    </div>
  );
}
