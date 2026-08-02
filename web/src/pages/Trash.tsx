import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { format } from 'date-fns';
import { formatDistanceToNowStrict } from 'date-fns';
import { toast } from 'sonner';
import {
  AlertCircle,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  RotateCcw,
  Trash2,
} from 'lucide-react';
import { api } from '../api/client';
import type {
  DeletedTransaction,
  DeletedTransactionList,
} from '../api/types';
import { useAuth } from '../hooks/useAuth';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { CategoryBadge } from '@/components/CategoryBadge';
import { formatCurrency } from '@/lib/format';
import { isAdmin } from '@/lib/roles';
import { cn } from '@/lib/utils';
import { TYPE_EXPENSE } from '@/lib/transaction-types';
import {
  DEFAULT_TRANSACTIONS_PER_PAGE,
  TRANSACTION_PAGE_SIZES,
} from '@/lib/constants';

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

interface PaginationBarProps {
  page: number;
  totalPages: number;
  perPage: number;
  onPageChange: (page: number) => void;
  onPerPageChange: (perPage: number) => void;
  /**
   * Optional content rendered on the leading (left) side of the bar,
   * immediately after the "Rows per page" cluster. The Trash page uses
   * this to surface whole-trash bulk actions (Restore all / Purge all)
   * in the table toolbar rather than above the card — callers that
   * omit it get the original two-cluster layout unchanged.
   */
  leadingActions?: ReactNode;
}

function getPageNumbers(page: number, totalPages: number): number[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const pages = new Set<number>();
  pages.add(1);
  pages.add(totalPages);
  for (
    let i = Math.max(2, page - 1);
    i <= Math.min(totalPages - 1, page + 1);
    i++
  ) {
    pages.add(i);
  }
  const sorted = [...pages].sort((a, b) => a - b);
  const result: number[] = [];
  for (let i = 0; i < sorted.length; i++) {
    if (i > 0 && sorted[i] - sorted[i - 1] > 1) {
      result.push(-1); // ellipsis sentinel
    }
    result.push(sorted[i]);
  }
  return result;
}

function PaginationBar({
  page,
  totalPages,
  perPage,
  onPageChange,
  onPerPageChange,
  leadingActions,
}: PaginationBarProps) {
  const pageNumbers = getPageNumbers(page, totalPages);

  return (
    // `flex-wrap` + row-gap keeps the toolbar legible once `leadingActions`
    // adds buttons on narrow viewports — without it the page-nav cluster
    // would spill past the card edge instead of wrapping to a new line.
    <div className="flex flex-wrap items-center justify-between gap-y-2 px-4 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <p className="text-sm font-medium">Rows per page</p>
        <Select
          value={String(perPage)}
          onValueChange={(v) => onPerPageChange(Number(v))}
        >
          <SelectTrigger className="h-8 w-[70px]" aria-label="Rows per page">
            <SelectValue />
          </SelectTrigger>
          <SelectContent side="top">
            <SelectGroup>
              {TRANSACTION_PAGE_SIZES.map((opt) => (
                <SelectItem key={opt} value={String(opt)}>
                  {opt}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        {leadingActions}
      </div>
      <div className="flex items-center gap-1">
        <Button
          variant="outline"
          size="icon"
          className="hidden size-8 lg:flex"
          onClick={() => onPageChange(1)}
          disabled={page <= 1}
          aria-label="Go to first page"
        >
          <ChevronsLeft />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="size-8"
          onClick={() => onPageChange(page - 1)}
          disabled={page <= 1}
          aria-label="Go to previous page"
        >
          <ChevronLeft />
        </Button>
        {pageNumbers.map((p, i) =>
          p === -1 ? (
            <span
              key={`ellipsis-${i}`}
              className="flex size-8 items-center justify-center text-sm text-muted-foreground"
              aria-hidden
            >
              ...
            </span>
          ) : (
            <Button
              key={p}
              variant={p === page ? 'outline' : 'ghost'}
              size="icon"
              className="size-8 text-xs"
              onClick={() => onPageChange(p)}
              aria-current={p === page ? 'page' : undefined}
            >
              {p}
            </Button>
          ),
        )}
        <Button
          variant="outline"
          size="icon"
          className="size-8"
          onClick={() => onPageChange(page + 1)}
          disabled={page >= totalPages}
          aria-label="Go to next page"
        >
          <ChevronRight />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="hidden size-8 lg:flex"
          onClick={() => onPageChange(totalPages)}
          disabled={page >= totalPages}
          aria-label="Go to last page"
        >
          <ChevronsRight />
        </Button>
      </div>
    </div>
  );
}

/**
 * Render a tombstoned timestamp as both a relative ("3 minutes ago") and
 * absolute label. The relative phrase is what the recovery operator
 * actually scans for ("what did I just delete?"), but the absolute
 * date-time is kept as a tooltip so stale trash from last week is
 * still legible at a glance.
 */
function formatDeletedAt(iso: string): { relative: string; absolute: string } {
  const d = new Date(iso);
  // `formatDistanceToNowStrict` avoids the "about 3 minutes" / "less than
  // a minute" soft phrasing that the non-strict variant produces. On a
  // recovery surface the operator wants a crisp number, not "about".
  const relative = formatDistanceToNowStrict(d, { addSuffix: true });
  const absolute = format(d, 'PPpp');
  return { relative, absolute };
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
            onClick={onCancel}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
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
            onClick={onCancel}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
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
      const resp = await api.post<{ restored: number }>(
        'transactions/restore-batch',
        { ids },
      );
      toast.success(
        `Restored ${resp.restored} transaction${resp.restored === 1 ? '' : 's'}`,
      );
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
      const resp = await api.post<{ restored: number }>(
        'transactions/restore-all',
      );
      toast.success(
        `Restored ${resp.restored} transaction${resp.restored === 1 ? '' : 's'}`,
      );
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
          className="h-8 gap-1.5 text-xs"
          onClick={() => void handleRestoreAll()}
          disabled={bulkBusy}
        >
          <RotateCcw className="size-3.5" />
          {restoringAll ? 'Restoring...' : `Restore all ${total}`}
        </Button>
        <Button
          type="button"
          variant="destructive"
          size="sm"
          className="h-8 gap-1.5 text-xs"
          onClick={() => setPendingPurgeAll(true)}
          disabled={bulkBusy}
        >
          <Trash2 className="size-3.5" />
          {purgingAll ? 'Purging...' : `Purge all ${total}`}
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
            leadingActions={bulkActions}
          />

          {selectionCount > 0 && (
            <div className="flex flex-wrap items-center gap-3 border-t bg-muted/50 px-4 py-2">
              <span className="text-sm font-medium">
                {selectionCount} selected
              </span>
              <Button
                type="button"
                size="sm"
                className="h-8 gap-1.5 text-xs"
                onClick={() => void handleBatchRestore()}
                disabled={batchRestoring}
              >
                <RotateCcw className="size-3.5" />
                {batchRestoring
                  ? 'Restoring...'
                  : `Restore ${selectionCount}`}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-8 text-xs"
                onClick={() => setSelectedIds(new Set())}
                disabled={batchRestoring}
              >
                Clear selection
              </Button>
            </div>
          )}

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
                        aria-label={`Select ${row.description}`}
                      />
                    </TableCell>
                    <TableCell
                      className="whitespace-nowrap text-sm text-muted-foreground"
                      title={absolute}
                    >
                      {relative}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      {format(new Date(row.date), 'MMM d, yyyy')}
                    </TableCell>
                    <TableCell className="font-medium">
                      {row.description || (
                        <span className="text-muted-foreground">
                          (no description)
                        </span>
                      )}
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
                          aria-label={`Restore ${row.description}`}
                        >
                          <RotateCcw className="size-3.5" />
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
                            aria-label={`Purge ${row.description}`}
                          >
                            <Trash2 className="size-3.5" />
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

          <div className="border-t">
            <PaginationBar
              page={page}
              totalPages={totalPages}
              perPage={perPage}
              onPageChange={handlePageChange}
              onPerPageChange={handlePerPageChange}
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
