import { useState, useEffect, useCallback, useId, useMemo, useRef } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import { toast } from 'sonner';
import {
  AlertCircle,
  ArrowUpDown,
  Pencil,
  Replace,
  Trash2,
  X,
} from 'lucide-react';
import { Input } from '@/components/ui/input';
import { api } from '../api/client';
import type { Category, SavedFilter, Transaction } from '../api/types';
import { UNDO_TOAST_MS } from '../lib/undo';
import { useTransactions } from '../hooks/useTransactions';
import type { TransactionFilters } from '../hooks/useTransactions';
import type { SortColumn } from '../hooks/useTransactions';
import type { UpdateTransactionInput } from '../hooks/useTransactions';
import { RefetchAfterMutationError } from '../hooks/useTransactions';
import { BulkEditDialog } from './Transactions.BulkEditDialog';
import { BulkEditConfirmDialog } from './Transactions.BulkEditConfirmDialog';
import type { ComputedPatchResult } from './Transactions.computePatch';
import { useSavedFilters } from '../hooks/useSavedFilters';
import { useSuggestions } from '../hooks/useSuggestions';
import { TransactionToolbar } from '../components/TransactionToolbar';
import { FilterPanel } from '../components/FilterPanel';
import { TransactionEntryRow } from '../components/TransactionEntryRow';
import { TransactionRow } from '../components/TransactionRow';
import { TransactionCardList } from '../components/TransactionCardList';
import { PaginationBar } from '../components/PaginationBar';
import { TransactionEditSheet } from '../components/TransactionEditSheet';
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
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { FORMAT_DATE_SHORT } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { getReadyRegistration } from '@/lib/push-sw';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useIsMobileViewport } from '@/hooks/useIsMobileViewport';
import { TOUCH_TARGET_CHECKBOX } from '@/lib/touch-target';

interface ActiveChip {
  key: string;
  label: string;
  onClear: () => void;
}

function buildActiveChips(
  filters: TransactionFilters,
  categories: Category[],
  setFilter: (key: keyof TransactionFilters, value: string) => void,
  baseCurrency: string,
): ActiveChip[] {
  const chips: ActiveChip[] = [];

  if (filters.dateFrom || filters.dateTo) {
    let label: string;
    if (filters.dateFrom && filters.dateTo) {
      label = `${format(new Date(filters.dateFrom), FORMAT_DATE_SHORT)} - ${format(new Date(filters.dateTo), FORMAT_DATE_SHORT)}`;
    } else if (filters.dateFrom) {
      label = `From ${format(new Date(filters.dateFrom), FORMAT_DATE_SHORT)}`;
    } else {
      label = `Until ${format(new Date(filters.dateTo), FORMAT_DATE_SHORT)}`;
    }
    chips.push({
      key: 'date',
      label,
      onClear: () => {
        setFilter('dateFrom', '');
        setFilter('dateTo', '');
      },
    });
  }

  if (filters.categoryIds || filters.categoryId) {
    const ids = filters.categoryIds
      ? filters.categoryIds.split(',')
      : [filters.categoryId];
    const names = ids
      .map((id) => categories.find((c) => String(c.id) === id)?.name)
      .filter(Boolean);
    chips.push({
      key: 'category',
      label: names.join(', ') || 'Categories',
      onClear: () => {
        setFilter('categoryIds', '');
        setFilter('categoryId', '');
      },
    });
  }

  if (filters.amountMin || filters.amountMax) {
    let label: string;
    if (filters.amountMin && filters.amountMax) {
      label = `${formatCurrency(parseFloat(filters.amountMin), baseCurrency)} - ${formatCurrency(parseFloat(filters.amountMax), baseCurrency)}`;
    } else if (filters.amountMin) {
      label = `Min ${formatCurrency(parseFloat(filters.amountMin), baseCurrency)}`;
    } else {
      label = `Max ${formatCurrency(parseFloat(filters.amountMax), baseCurrency)}`;
    }
    chips.push({
      key: 'amount',
      label,
      onClear: () => {
        setFilter('amountMin', '');
        setFilter('amountMax', '');
      },
    });
  }

  if (filters.tags) {
    chips.push({
      key: 'tags',
      label: filters.tags,
      onClear: () => setFilter('tags', ''),
    });
  }

  return chips;
}

interface SortableHeaderProps {
  label: string;
  column: SortColumn;
  onSort: (column: SortColumn) => void;
  align?: 'left' | 'right';
  className?: string;
}

function SortableHeader({
  label,
  column,
  onSort,
  align = 'left',
  className,
}: SortableHeaderProps) {
  return (
    <TableHead className={cn(align === 'right' ? 'text-right' : undefined, className)}>
      <Button
        variant="ghost"
        size="sm"
        className={cn('h-8', align === 'right' ? '-mr-3' : '-ml-3')}
        onClick={() => onSort(column)}
      >
        {label}
        <ArrowUpDown />
      </Button>
    </TableHead>
  );
}

function TableSkeleton() {
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

export function Transactions() {
  const baseCurrency = useBaseCurrency();
  // Below `md` the seven-column table is replaced by a stacked card list.
  // The two presentations are MOUNTED alternately rather than one being
  // `md:hidden`: rendering both would put two "Select <description>"
  // checkboxes in the a11y tree for every row and pay the render cost twice.
  // Everything else on this page — filters, selection scopes, bulk machinery,
  // pagination, toasts — is shared state that neither branch owns.
  const isMobile = useIsMobileViewport();
  const {
    transactions,
    total,
    page,
    perPage,
    sortBy,
    sortDir,
    filters,
    setFilter,
    clearFilters,
    searchInput,
    setSearchInput,
    clearPanelFilters,
    setPage,
    setPerPage,
    setSort,
    initialLoad,
    showingPrevious,
    searchPending,
    error,
    createTransaction,
    updateTransaction,
    deleteTransaction,
    deleteByFilter,
    bulkUpdate,
    bulkUpdateByFilter,
    buildFilterQuery,
    refetch,
  } = useTransactions();

  // Every control whose scope is "everything matching the current filters"
  // is gated on this one predicate. Two different windows put the visible
  // numbers and the write's actual target out of step, and both are fatal in
  // the same way (B4: a labelled count that describes a different set than
  // the write touches):
  //   showingPrevious — the term has reached the query key but the new page
  //                     has not landed, so `total` still counts the old one.
  //   searchPending   — the user has typed past the committed term, so
  //                     buildFilterQuery() still serializes the old one.
  const filterScopeUnsettled = showingPrevious || searchPending;

  const [suggestionsKey, setSuggestionsKey] = useState(0);
  const suggestions = useSuggestions(suggestionsKey);
  const [showFilters, setShowFilters] = useState(false);
  const [showEntry, setShowEntry] = useState(false);
  const [showReplace, setShowReplace] = useState(false);
  const [replaceText, setReplaceText] = useState('');
  const [replacing, setReplacing] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
  // Phone-only. The table shows a checkbox on every row at all times; a card
  // does not, because the row is one big tap target and a permanent checkbox
  // would eat the width the description needs. Selection mode is what reveals
  // them — entered by a long press on a card or by the toolbar's Select
  // toggle. Ignored entirely by the desktop table.
  const [selectionMode, setSelectionMode] = useState(false);
  // The row whose edit sheet is open (phone). Held as an ID and resolved
  // against the live rows below, not stored as a snapshot.
  const [editingId, setEditingId] = useState<number | null>(null);
  const mobileSelectAllId = useId();
  // Two selection modes:
  //   'page'         — selectedIds holds explicit IDs from the current page.
  //   'all-matching' — user clicked "Select all N matching", selectedIds is
  //                    ignored, and the bulk-delete path uses the filter
  //                    endpoint so the operation stays atomic for 10k+ rows.
  const [selectionScope, setSelectionScope] = useState<'page' | 'all-matching'>(
    'page',
  );
  const [deleting, setDeleting] = useState(false);
  // Confirmation modal for the destructive "delete all matching" path.
  // Per-page batch delete is treated as low-risk (user clicked each row),
  // but the filter-based path can wipe out 10k+ rows — always confirm.
  const [confirmAllMatchingOpen, setConfirmAllMatchingOpen] = useState(false);
  // Bulk-edit dialog state. `bulkConfirm` holds the computed patch while we
  // route the all-matching scope through the AlertDialog confirmation step
  // (spec §3.4); page-mode skips this and dispatches immediately.
  const [bulkEditOpen, setBulkEditOpen] = useState(false);
  const [bulkConfirm, setBulkConfirm] = useState<ComputedPatchResult | null>(
    null,
  );
  // Staged Replace All patch awaiting the BulkEditConfirmDialog. Mirrors
  // `bulkConfirm` for bulk edit: the write is filter-scoped and can span
  // the whole ledger, so it always confirms first (B4).
  const [replaceConfirm, setReplaceConfirm] =
    useState<ComputedPatchResult | null>(null);
  const [rowError, setRowError] = useState('');
  const cardRef = useRef<HTMLDivElement>(null);
  // Focus anchor for after a row delete: the deleted <TransactionRow> (and
  // whatever Radix trigger inside it had auto-refocus) unmounts, which would
  // otherwise drop keyboard/SR focus onto <body> with no context. Mirrors
  // RecentlyAdded's headingRef/restoreFocus pattern.
  const pageHeadingRef = useRef<HTMLHeadingElement>(null);
  const queryClient = useQueryClient();

  // Single-row delete with an undo window. The delete is the same
  // soft-delete as before; the toast makes the Trash detour visible and
  // Undo calls the restore endpoint members can reach since B5. The catch
  // also gives a FAILED delete feedback — previously the rejection from
  // the row's fire-and-forget onDelete was unhandled and the row just
  // stayed put with no explanation.
  const handleRowDelete = useCallback(
    async (id: number) => {
      try {
        await deleteTransaction(id);
      } catch (err) {
        toast.error(err instanceof Error ? err.message : 'Delete failed');
        return;
      }
      pageHeadingRef.current?.focus();
      toast.success('Moved to Trash', {
        duration: UNDO_TOAST_MS,
        action: {
          label: 'Undo',
          onClick: () => {
            void api
              .post(`transactions/${id}/restore`, {})
              .then(() => {
                // Explicit invalidations (rather than waiting on the SSE
                // round-trip) so the ACTING tab — the one where Undo was
                // clicked — gets an immediate badge/list refresh. Other
                // open tabs/devices are covered by the SSE invalidate the
                // backend already publishes on restore
                // (h.publishInvalidate("trash", "transactions", ...)).
                void queryClient.invalidateQueries({
                  queryKey: ['transactions'],
                });
                void queryClient.invalidateQueries({ queryKey: ['trash'] });
                toast.success('Restored');
              })
              .catch((err: unknown) => {
                toast.error(
                  err instanceof Error ? err.message : 'Could not restore',
                );
              });
          },
        },
      });
    },
    [deleteTransaction, queryClient],
  );

  // Shared by both edit surfaces — the desktop inline row and the phone sheet.
  const handleRowUpdate = useCallback(
    async (input: UpdateTransactionInput) => {
      // Capture before awaiting: updateTransaction kicks off
      // a non-awaited refetch that can replace `transactions`
      // in state mid-flight. Reading `original` after the
      // await could race against the refetch.
      const original = transactions.find((t) => t.id === input.id);
      const descriptionChanged =
        !original || original.description !== input.description;
      const tagsChanged =
        !original || (original.tags ?? '') !== (input.tags ?? '');
      await updateTransaction(input);
      // Only refresh the suggestions cache when description or
      // tags actually changed — amount/category/date edits
      // don't affect autocomplete, and refetching on every
      // save is noise.
      if (descriptionChanged || tagsChanged) {
        setSuggestionsKey((k) => k + 1);
      }
    },
    [transactions, updateTransaction],
  );

  // Remembered separately from `editingId` because the sheet's close-focus
  // hook runs AFTER `editingId` has already been cleared.
  const lastEditedIdRef = useRef<number | null>(null);

  const handleCardOpen = useCallback((tx: Transaction) => {
    lastEditedIdRef.current = tx.id;
    setEditingId(tx.id);
  }, []);

  // Where focus goes when the edit sheet closes on save, cancel, Escape or an
  // overlay tap. Back to the card the user opened, which is where their
  // attention was and where the next tap would land; the page heading is the
  // fallback for the case where that row is no longer on the page (a filter
  // moved it, the other member deleted it). Without this the sheet closes onto
  // <body> — Radix's own restore targets a SheetTrigger this sheet does not
  // have. Same heading anchor the delete, Replace-All and bulk-edit paths use.
  const focusAfterSheetClose = useCallback(() => {
    const id = lastEditedIdRef.current;
    const card =
      id == null
        ? null
        : cardRef.current?.querySelector<HTMLElement>(
            `[data-transaction-id="${id}"]`,
          );
    (card ?? pageHeadingRef.current)?.focus();
  }, []);

  const closeEditSheet = useCallback(() => {
    setEditingId(null);
  }, []);

  // Resolved against the LIVE rows rather than snapshotting the transaction:
  // a refetch landing mid-edit must feed the fresh values to the sheet's
  // storedMoney comparison (which is what the server compares against), and a
  // row that leaves the page — deleted, or filtered out — closes the sheet by
  // disappearing instead of leaving the user editing a ghost.
  const editingTx =
    editingId == null
      ? null
      : (transactions.find((t) => t.id === editingId) ?? null);

  // Retire the id once its row has left the page, rather than leaving it set
  // against a row that is no longer there. The sheet closing when the row goes
  // is intended (documented desktop parity); what is not is the id surviving
  // that close — the other member deleting a row and then restoring it would
  // bring the row back into `transactions` and spontaneously REOPEN the sheet
  // over whatever the user had moved on to.
  useEffect(() => {
    if (editingId != null && editingTx == null) {
      setEditingId(null);
    }
  }, [editingId, editingTx]);

  const handlePageChange = useCallback(
    (newPage: number) => {
      setPage(newPage);
      cardRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    },
    [setPage],
  );

  const handleExport = useCallback(() => {
    const params = new URLSearchParams();
    if (filters.dateFrom) params.set('date_from', filters.dateFrom);
    if (filters.dateTo) params.set('date_to', filters.dateTo);
    if (filters.categoryIds) {
      params.set('category_ids', filters.categoryIds);
    } else if (filters.categoryId) {
      params.set('category_id', filters.categoryId);
    }
    if (filters.type) params.set('type', filters.type);
    if (filters.search) params.set('search', filters.search);
    if (filters.amountMin) params.set('amount_min', filters.amountMin);
    if (filters.amountMax) params.set('amount_max', filters.amountMax);
    if (filters.tags) params.set('tags', filters.tags);

    const query = params.toString();
    const url = `/api/export/transactions${query ? `?${query}` : ''}`;
    window.open(url, '_blank');
  }, [filters]);

  // Replace All stages a description-only patch and confirms before
  // sending. The write goes through update-by-filter with the SAME
  // buildFilterQuery() serialization the list count uses — B4's bug was
  // this path sending only the search text, so "Replace All (4)" could
  // rewrite the whole ledger.
  const handleReplaceAll = useCallback(() => {
    const desc = replaceText.trim();
    if (!desc || !filters.search.trim()) return;
    // `total` is still the previous filter's count while the new page is in
    // flight, but the write resolves buildFilterQuery() against the CURRENT
    // filters — firing here would rename a set the "Replace All (N)" label
    // never described. The guard lives in the handler, not just on the
    // button: the text input's Enter key reaches this same path, which is
    // also the whole reason the debounce window has to be covered — type,
    // then Enter inside 250ms, and the button never gets a say.
    if (filterScopeUnsettled) return;
    setReplaceConfirm({ patch: { description: desc } });
  }, [replaceText, filters.search, filterScopeUnsettled]);

  // Retiring the replace bar takes focus with it: the confirm dialog restored
  // focus to the "Replace All" button on close, and that button lives inside
  // the bar we are about to unmount — so without a deliberate target, focus
  // lands on <body>. The page heading is the same anchor a row delete uses.
  const closeReplaceBar = useCallback(() => {
    setShowReplace(false);
    setReplaceText('');
    pageHeadingRef.current?.focus();
  }, []);

  // Fires after the confirm click. Deliberately independent of
  // dispatchBulkEdit: that path clears the row selection on success,
  // which Replace All must not do (it is filter-scoped, not
  // selection-scoped).
  const dispatchReplaceAll = useCallback(
    async (p: ComputedPatchResult) => {
      setReplacing(true);
      try {
        const filterQuery = buildFilterQuery();
        const { updated } = await bulkUpdateByFilter({ filterQuery, ...p });
        toast.success(
          `Renamed ${updated} transaction${updated !== 1 ? 's' : ''}`,
        );
        closeReplaceBar();
      } catch (err) {
        if (err instanceof RefetchAfterMutationError) {
          toast.error(
            `Rename applied, but refresh failed — please reload to see the latest. (${err.message})`,
          );
          closeReplaceBar();
        } else {
          toast.error(
            `Rename failed: ${err instanceof Error ? err.message : String(err)}`,
          );
          // Bar and text stay so the user can retry.
        }
      } finally {
        setReplacing(false);
      }
    },
    [buildFilterQuery, bulkUpdateByFilter, closeReplaceBar],
  );

  const handleSelect = useCallback((id: number, checked: boolean) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
    // Any per-row toggle demotes out of 'all-matching' mode: the user is
    // now curating a specific subset, not operating on the full match set.
    setSelectionScope('page');
  }, []);

  const handleSelectAll = useCallback(
    (checked: boolean) => {
      if (checked) {
        setSelectedIds(new Set(transactions.map((tx) => tx.id)));
      } else {
        setSelectedIds(new Set());
      }
      setSelectionScope('page');
    },
    [transactions],
  );

  const handleSelectAllMatching = useCallback(() => {
    // Defence-in-depth, currently UNREACHABLE: the only caller is the banner
    // button, which the !filterScopeUnsettled render guard hides, and unlike
    // Replace All there is no keyboard path around it — so no test can drive
    // this line and none pins it (deep review, 2026-08-07). It exists for the
    // day the render guard is relaxed from hidden to disabled (the trade-off
    // the Replace All comment weighs): entering 'all-matching' routes the
    // next delete/edit through the filter endpoints, so if this handler ever
    // becomes reachable in the unsettled window, the gate must already be on
    // the state transition. If you make it reachable, copy Replace All's
    // bypass test + positive control.
    if (filterScopeUnsettled) return;
    setSelectionScope('all-matching');
  }, [filterScopeUnsettled]);

  const handleClearSelection = useCallback(() => {
    setSelectedIds(new Set());
    setSelectionScope('page');
    // Clearing the selection is also the way out of the phone's selection
    // mode: leaving the checkboxes on screen with nothing selected would
    // strand the user in a mode whose only other exit is the toolbar toggle.
    setSelectionMode(false);
  }, []);

  const toggleSelectionMode = useCallback(() => {
    if (selectionMode) {
      setSelectionMode(false);
      handleClearSelection();
    } else {
      setSelectionMode(true);
    }
  }, [selectionMode, handleClearSelection]);

  // A long press is the touch dialect for entering selection mode, and it
  // selects the card it started on — coming out of the gesture with an empty
  // selection would read as nothing having happened.
  const handleCardLongPress = useCallback(
    (tx: Transaction) => {
      setSelectionMode(true);
      handleSelect(tx.id, true);
    },
    [handleSelect],
  );

  const selectionCount =
    selectionScope === 'all-matching' ? total : selectedIds.size;

  // executeBulkDelete performs the actual delete. Split out from
  // requestBulkDelete so the confirmation modal can invoke it after the user
  // confirms, without re-running the "open modal vs delete directly" logic.
  const executeBulkDelete = useCallback(async () => {
    if (selectionCount === 0) return;
    setDeleting(true);
    try {
      let deleted: number;
      if (selectionScope === 'all-matching') {
        // Atomic filter-based delete — avoids chunking into 500-row
        // batches and avoids partial-failure states mid-operation.
        deleted = await deleteByFilter();
      } else {
        const res = await api.post<{ deleted: number }>(
          'transactions/batch-delete',
          { ids: Array.from(selectedIds) },
        );
        deleted = res.deleted;
        refetch();
      }
      // No per-toast Undo here (unlike the single-row delete above):
      // delete-by-filter doesn't return the ids it deleted, and a bulk
      // operation can span thousands of rows, so a single-toast Undo
      // wouldn't be meaningful anyway. Bulk recovery deliberately relies
      // on the Trash page instead — spec decision, recorded so it isn't
      // re-raised.
      toast.success(
        `Moved ${deleted} transaction${deleted !== 1 ? 's' : ''} to Trash`,
      );
      setSelectedIds(new Set());
      setSelectionScope('page');
      setConfirmAllMatchingOpen(false);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Delete failed');
    } finally {
      setDeleting(false);
    }
  }, [selectionCount, selectionScope, selectedIds, deleteByFilter, refetch]);

  // requestBulkDelete is the click handler for the Delete button in the
  // selection bar. For per-page selections we delete immediately (user picked
  // each row explicitly). For 'all-matching' we open a confirmation modal
  // because that path can destroy tens of thousands of rows atomically and
  // the action bar alone is too subtle for an irreversible operation.
  const requestBulkDelete = useCallback(() => {
    if (selectionCount === 0) return;
    if (selectionScope === 'all-matching') {
      setConfirmAllMatchingOpen(true);
      return;
    }
    void executeBulkDelete();
  }, [selectionCount, selectionScope, executeBulkDelete]);

  // dispatchBulkEdit fires the actual PATCH and reconciles client state
  // (selection prune, toast copy, dialog close). Per spec §3.5:
  //   - Page mode echoes back `visibleIds`; we intersect `selectedIds`
  //     with that set so rows kicked off the current filter drop out of
  //     the selection silently. The toast names that drop count when it
  //     happens so the user understands why their selection shrank.
  //   - Filter mode skips the prune (scope is filter-defined; refetch
  //     auto-corrects it) and clears selection wholesale.
  // RefetchAfterMutationError gets a differentiated toast so the user
  // knows the mutation landed even though the view is stale.
  const dispatchBulkEdit = useCallback(
    async (p: ComputedPatchResult) => {
      const isFilterMode = selectionScope === 'all-matching';
      // Whether this success leaves the selection empty — which unmounts the
      // selection bar, and with it the "Edit (N)" button the dialog will try
      // to restore focus to. Same failure as Replace All had: Radix aims at a
      // node that no longer exists and focus lands on <body>.
      // Deliberately left uninitialised: both success branches below assign
      // it before the only read, so an initialiser would be dead code — and
      // without one, a future branch that forgets to set it is a TypeScript
      // "used before assigned" error rather than a silent `false` that skips
      // the focus restore.
      let selectionEmptied: boolean;
      try {
        if (isFilterMode) {
          const filterQuery = buildFilterQuery();
          const { updated } = await bulkUpdateByFilter({ filterQuery, ...p });
          const noun = (n: number) =>
            `${n} transaction${n === 1 ? '' : 's'}`;
          toast.success(`Updated ${noun(updated)}`);
          handleClearSelection();
          selectionEmptied = true;
        } else {
          const { updated, skipped, visibleIds } = await bulkUpdate({
            ids: [...selectedIds],
            ...p,
          });
          const visible = new Set(visibleIds);
          const prevSize = selectedIds.size;
          const newSelection = new Set(
            [...selectedIds].filter((id) => visible.has(id)),
          );
          setSelectedIds(newSelection);
          selectionEmptied = newSelection.size === 0;
          const dropped = prevSize - newSelection.size;
          const noun = (n: number) =>
            `${n} transaction${n === 1 ? '' : 's'}`;
          const head =
            updated === 0 && skipped && skipped > 0
              ? `No matches updated; skipped ${noun(skipped)}`
              : `Updated ${noun(updated)}${skipped ? `, skipped ${skipped}` : ''}`;
          const tail =
            dropped > 0
              ? ' (selection cleared — rows no longer match the current filter)'
              : '';
          toast.success(head + tail);
        }
        setBulkEditOpen(false);
        setBulkConfirm(null);
        // Only when the bar is going away. If a page-mode edit left rows
        // selected, the "Edit (N)" trigger is still mounted and Radix's own
        // restore puts focus back on it, which is the better target.
        if (selectionEmptied) pageHeadingRef.current?.focus();
      } catch (err) {
        if (err instanceof RefetchAfterMutationError) {
          toast.error(
            `Update applied, but refresh failed — please reload to see the latest. (${err.message})`,
          );
        } else {
          toast.error(
            `Update failed: ${err instanceof Error ? err.message : String(err)}`,
          );
        }
        // Dialog stays open on failure so the user can retry without
        // re-entering their patch.
      }
    },
    [
      selectionScope,
      selectedIds,
      buildFilterQuery,
      bulkUpdate,
      bulkUpdateByFilter,
      handleClearSelection,
    ],
  );

  const {
    savedFilters,
    saveFilter,
    deleteFilter: deleteSavedFilter,
  } = useSavedFilters();

  // When the activity ledger is opened, the user has seen whatever the rolled-up
  // activity push was counting — clear the PWA app-icon badge. Feature-detected:
  // navigator.clearAppBadge is absent on browsers without the Badging API, so the
  // optional call short-circuits there.
  //
  // Also close the showing tag:"activity" notification. Its data.count is the
  // SOLE baseline the SW rollup reads (applyActivityRollup → activityCount), so
  // leaving it open makes the next activity push over-count ("4 new" instead of
  // "1"). getReadyRegistration() is bounded (it never awaits a never-resolving
  // serviceWorker.ready), getNotifications is feature-detected, and every failure
  // is swallowed — this is best-effort cleanup, never a blocking path.
  useEffect(() => {
    void navigator.clearAppBadge?.();
    void getReadyRegistration()
      .then((reg) => {
        if (!reg || typeof reg.getNotifications !== 'function') return;
        return reg
          .getNotifications({ tag: 'activity' })
          .then((ns) => ns.forEach((n) => n.close()));
      })
      .catch(() => {});
  }, []);

  const [categories, setCategories] = useState<Category[]>([]);

  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch((err) => {
        console.warn('Failed to load categories', err);
      });
  }, []);

  // Clear selection when page or data changes. Also drops
  // selectionScope back to 'page' so "all-matching" mode never
  // silently survives a filter edit or page flip, and leaves the
  // phone's selection mode — the checkboxes would otherwise stay on
  // over a set of rows the user never picked.
  useEffect(() => {
    setSelectedIds(new Set());
    setSelectionScope('page');
    setSelectionMode(false);
  }, [page, filters, perPage]);

  const handleSaveFilter = useCallback(
    (name: string) => {
      saveFilter(name, JSON.stringify(filters));
    },
    [saveFilter, filters],
  );

  const handleLoadFilter = useCallback(
    (sf: SavedFilter) => {
      try {
        const parsed = JSON.parse(sf.filter_json) as Record<string, unknown>;
        clearFilters();
        const knownKeys: (keyof TransactionFilters)[] = [
          'dateFrom', 'dateTo', 'categoryId', 'categoryIds',
          'amountMin', 'amountMax', 'tags', 'type', 'search',
        ];
        for (const key of knownKeys) {
          const value = parsed[key];
          if (typeof value === 'string') {
            setFilter(key, value);
          }
        }
      } catch (err) {
        console.warn('Failed to load saved filter', err);
      }
    },
    [setFilter, clearFilters],
  );

  const handleDeleteFilter = useCallback(
    (id: number) => {
      deleteSavedFilter(id);
    },
    [deleteSavedFilter],
  );

  const activeFilterCount = useMemo(() => {
    let count = 0;
    if (filters.dateFrom || filters.dateTo) count++;
    if (filters.categoryIds || filters.categoryId) count++;
    if (filters.amountMin || filters.amountMax) count++;
    if (filters.tags) count++;
    return count;
  }, [filters]);

  const activeChips = useMemo(
    () =>
      showFilters
        ? []
        : buildActiveChips(filters, categories, setFilter, baseCurrency),
    [filters, showFilters, categories, setFilter, baseCurrency],
  );

  const totalPages = Math.max(1, Math.ceil(total / perPage));

  // One selection bar, rendered in one of two places. Inside the card above
  // the table on desktop; STICKY at the bottom of the phone card, in flow
  // between the list and the bottom pager.
  //
  // Sticky rather than fixed, and this is the whole point: a fixed bar is
  // outside flow, so it covers the last card and the bottom pager for the
  // entire selection session — permanently, not just while scrolling — and
  // compensating with page padding means hard-coding a height that changes
  // when the two-line all-matching banner appears. In flow it contributes its
  // own height, whatever that height happens to be, so it can never occlude
  // anything; and `bottom-0` still pins it to the viewport for as long as its
  // resting place is below the fold, which is the visibility the fixed version
  // was there for. It stops sticking exactly when the pager scrolls into view,
  // which is the moment it must stop covering it.
  //
  // Same trade the QuickAdd footer makes. Requires no `overflow-hidden`
  // ancestor — the phone branch already drops the card's, for the day headers.
  const selectionBar =
    selectionCount > 0 ? (
      <div className="flex flex-col gap-2 rounded-lg border bg-muted/50 px-4 py-2">
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-sm font-medium">
            {selectionScope === 'all-matching'
              ? `All ${total} matching transactions selected`
              : `${selectionCount} selected`}
          </span>
          <Button
            type="button"
            size="sm"
            className="h-11 gap-1.5 text-xs md:h-8"
            onClick={() => setBulkEditOpen(true)}
            disabled={deleting}
          >
            <Pencil />
            Edit ({selectionCount})
          </Button>
          <Button
            type="button"
            variant="destructive"
            size="sm"
            className="h-11 gap-1.5 text-xs md:h-8"
            onClick={requestBulkDelete}
            disabled={deleting}
          >
            <Trash2 />
            {deleting ? 'Deleting...' : 'Delete'}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-11 text-xs md:h-8"
            onClick={handleClearSelection}
          >
            Clear selection
          </Button>
        </div>
        {/*
          Banner prompt: appears only when the user has checked every
          visible row AND there are more matching rows beyond the
          current page. Clicking switches to the atomic filter-based
          delete path. Hidden once scope is already 'all-matching'.

          Also hidden — not merely disabled — for the whole of
          `filterScopeUnsettled`: BOTH numbers it prints belong to the
          filter the rows were fetched under, and the filter-scoped
          delete/edit it opts into resolves against whatever is
          committed when it fires. Disabling would leave the wrong
          count sitting there as if it were a fact.
        */}
        {!filterScopeUnsettled &&
          selectionScope === 'page' &&
          transactions.length > 0 &&
          transactions.every((tx) => selectedIds.has(tx.id)) &&
          total > transactions.length && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span>All {transactions.length} on this page selected.</span>
              <Button
                type="button"
                variant="link"
                size="sm"
                className="h-auto min-h-11 p-0 text-xs md:min-h-0"
                onClick={handleSelectAllMatching}
              >
                Select all {total} matching
              </Button>
            </div>
          )}
      </div>
    ) : null;

  // Entering selection mode and every count change are purely visual otherwise
  // — the checkboxes appear, the bar appears, and a screen-reader user is told
  // nothing. Long-press entry needs this most: it has no button press to
  // self-announce. Empty on desktop, where the mode does not exist.
  //
  // The wording deliberately does NOT repeat the bar's sentence verbatim.
  // Identical sr-only text beside visible text is a duplicate the a11y tree
  // and every future `getByText` on this page both have to disambiguate, and
  // the announcement wants a verb anyway — it is reporting a transition, where
  // the bar is reporting a state.
  //
  // Silent when the phone user did not drive the change: mobile with a page
  // selection but selection mode off is a selection carried across a rotation
  // from the desktop table, which the bar already shows and which nothing just
  // happened to.
  const selectionAnnouncement = !isMobile
    ? ''
    : selectionScope === 'all-matching'
      ? `Selected all ${total} matching transactions`
      : selectionMode
        ? `Selection mode on, ${selectionCount} selected`
        : '';

  return (
    <div className="flex flex-col gap-6">
      {/*
        Mounted unconditionally, and empty rather than absent when there is
        nothing to say. A live region that mounts together with its first
        message is not observed changing, so assistive tech announces nothing —
        the region has to already exist for the text landing in it to register.
      */}
      <div aria-live="polite" className="sr-only">
        {selectionAnnouncement}
      </div>

      <div className="flex items-center justify-between">
        <h1
          ref={pageHeadingRef}
          tabIndex={-1}
          className="text-2xl font-semibold tracking-tight outline-none"
        >
          Transactions
        </h1>
        <Button variant="outline" size="sm" onClick={handleExport}>
          Export Excel
        </Button>
      </div>

      <TransactionToolbar
        search={searchInput}
        onSearchChange={setSearchInput}
        type={filters.type}
        onTypeChange={(v) => setFilter('type', v)}
        activeFilterCount={activeFilterCount}
        showFilters={showFilters}
        onToggleFilters={() => setShowFilters((p) => !p)}
        showEntry={showEntry}
        onToggleEntry={() => setShowEntry((p) => !p)}
        // Only below `md`, where the table header that owns sorting and the
        // always-visible row checkboxes are both gone.
        mobile={
          isMobile
            ? {
                sortBy,
                sortDir,
                onSort: setSort,
                selectionMode,
                onToggleSelectionMode: toggleSelectionMode,
              }
            : undefined
        }
      />

      {filters.search && total > 0 && (
        <div className="flex items-center gap-2 rounded-lg border bg-card p-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-11 gap-1.5 text-xs md:h-8"
            onClick={() => {
              setShowReplace((p) => !p);
              if (showReplace) setReplaceText('');
            }}
          >
            <Replace />
            Replace
          </Button>
          {showReplace && (
            <>
              <div className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />
              <Input
                type="text"
                value={replaceText}
                onChange={(e) => setReplaceText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleReplaceAll();
                }}
                placeholder="New description..."
                className="h-11 max-w-xs text-xs md:h-8"
                disabled={replacing}
              />
              <Button
                type="button"
                size="sm"
                className="h-11 text-xs md:h-8"
                onClick={handleReplaceAll}
                disabled={
                  replacing || filterScopeUnsettled || !replaceText.trim()
                }
              >
                {replacing ? 'Replacing...' : `Replace All (${total})`}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="size-11 md:size-8"
                onClick={() => {
                  setShowReplace(false);
                  setReplaceText('');
                }}
              >
                <X />
              </Button>
            </>
          )}
        </div>
      )}

      {activeChips.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {activeChips.map((chip) => (
            <Badge
              key={chip.key}
              variant="secondary"
              className="gap-1"
            >
              {chip.label}
              <button
                type="button"
                className={cn(
                  'text-muted-foreground hover:text-foreground',
                  'rounded-full p-0.5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                  // Grows the BOX rather than using the hit-area inset: chips
                  // sit 6px apart, so a 44px invisible ring around each X would
                  // overlap its neighbour and let one chip eat a tap meant for
                  // the next. A taller chip row on a phone is the honest cost.
                  'flex min-h-11 min-w-11 items-center justify-center md:min-h-0 md:min-w-0',
                )}
                onClick={chip.onClear}
                aria-label={`Clear ${chip.label} filter`}
              >
                <X className="size-3" />
              </button>
            </Badge>
          ))}
        </div>
      )}

      <Sheet open={showFilters} onOpenChange={setShowFilters}>
        <SheetContent side="right" className="w-full sm:max-w-md">
          <SheetHeader>
            <SheetTitle>Filters</SheetTitle>
            <SheetDescription>
              Narrow the transaction list by date, category, amount, or a saved
              preset.
            </SheetDescription>
          </SheetHeader>
          <div className="mt-4">
            <FilterPanel
              filters={filters}
              setFilter={setFilter}
              clearPanelFilters={clearPanelFilters}
              categories={categories}
              savedFilters={savedFilters}
              onSaveFilter={handleSaveFilter}
              onLoadFilter={handleLoadFilter}
              onDeleteFilter={handleDeleteFilter}
            />
          </div>
        </SheetContent>
      </Sheet>

      {/*
        TransactionEntryRow is shown/hidden via Tailwind `hidden` so that its
        internal form state (date, amount, description, tags) is preserved
        across open/close.
      */}
      <div className={showEntry ? undefined : 'hidden'}>
        {/*
          Raw deleteTransaction, NOT handleRowDelete: the entry row has its
          own save-undo (⌘Z) whose catch requires onDelete to REJECT on
          failure so it can restore the undo buffer and show "Could not
          undo" (see undoLastSave in TransactionEntryRow). handleRowDelete
          swallows the error into a toast instead of rejecting, and would
          also stack a second toast on top of the entry row's own
          save-toast. The table row below keeps handleRowDelete — it has
          no save-undo of its own to protect.
        */}
        <TransactionEntryRow
          categories={categories}
          onSubmit={async (v) => {
            const tx = await createTransaction(v);
            setSuggestionsKey((k) => k + 1);
            return tx;
          }}
          onDelete={deleteTransaction}
          onClose={() => setShowEntry(false)}
          descriptionSuggestions={suggestions.descriptions}
          tagSuggestions={suggestions.tags}
        />
      </div>

      {(error || rowError) && (
        <Alert variant="destructive">
          <AlertCircle className="size-4" />
          <AlertDescription className="flex items-start justify-between gap-3">
            <span>{error || rowError}</span>
            {/*
              Dismiss is offered only for `rowError` — a save failure, which
              is a past event the user can acknowledge. `error` is the list
              query's CURRENT state; hiding it would claim the table loaded
              when it didn't, and the next render would bring it straight
              back. A successful row save also clears rowError on its own
              (TransactionRow.handleSave), so the banner can't outlive the
              failure it describes.
            */}
            {!error && (
              <button
                type="button"
                onClick={() => setRowError('')}
                aria-label="Dismiss error"
                className={cn(
                  'shrink-0 rounded-sm opacity-70 ring-offset-background',
                  'transition-opacity hover:opacity-100',
                  'focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2',
                  'flex min-h-11 min-w-11 items-center justify-center md:min-h-0 md:min-w-0',
                )}
              >
                <X className="size-4" />
              </button>
            )}
          </AlertDescription>
        </Alert>
      )}

      {initialLoad ? (
        <TableSkeleton />
      ) : transactions.length === 0 ? (
        <Card className="p-6 text-center text-sm text-muted-foreground">
          No transactions found. Add one above to get started.
        </Card>
      ) : (
        // Dim + aria-busy answer one question: does what you are looking at
        // still answer what you asked for? Only `showingPrevious` makes it
        // "no" — the rows are held over from the previous filter/page/sort
        // while the new one loads (see `placeholderData` in useTransactions).
        //
        // Deliberately NOT keyed on `fetching`. Every household save reaches
        // this tab as an SSE-driven same-key refetch, and those rows are
        // correct and settled; pulsing the table on each one is noise about
        // work the user neither asked for nor can act on. aria-busy moves
        // with the dim rather than tracking `fetching`, for the same reason:
        // flipping it on every partner save is that pulse in non-visual form.
        <Card
          ref={cardRef}
          aria-busy={showingPrevious || undefined}
          className={cn(
            'transition-opacity duration-200',
            // Clips the desktop table's corners. Dropped on the phone: it
            // would make this card the scrollport for the day headers'
            // `sticky`, and a scrollport that never scrolls is one they never
            // stick to. Below `md` the document is the scroller.
            !isMobile && 'overflow-hidden',
            showingPrevious && 'opacity-60',
          )}
        >
          <PaginationBar
            page={page}
            totalPages={totalPages}
            perPage={perPage}
            onPageChange={handlePageChange}
            onPerPageChange={setPerPage}
            compact={isMobile}
          />

          {!isMobile && selectionBar}

          {isMobile && selectionMode && (
            <div className="flex items-center border-t px-4">
              {/*
                A phone has no column header to hang select-all off, so it gets
                its own strip — and it is mounted for the whole of selection
                mode, not just once something is selected. The bar below
                appears at count>0, which left the entry state with no
                select-all and nothing saying what mode you were in.

                `htmlFor` targets Radix's checkbox button (a labelable
                element), which makes the whole 44px strip a tap target for it.
                The fuller `aria-label` wins as the accessible name and
                contains the visible text, so what a screen reader announces
                still starts with what the eye reads.
              */}
              <label
                htmlFor={mobileSelectAllId}
                className="flex min-h-11 flex-1 items-center gap-3"
              >
                <Checkbox
                  id={mobileSelectAllId}
                  checked={
                    selectionScope === 'all-matching' ||
                    (transactions.length > 0 &&
                      transactions.every((tx) => selectedIds.has(tx.id)))
                  }
                  onCheckedChange={(v) => handleSelectAll(v === true)}
                  aria-label="Select all on this page"
                  className={TOUCH_TARGET_CHECKBOX}
                />
                <span className="text-sm font-medium">Select all</span>
              </label>
            </div>
          )}

          {isMobile ? (
            <TransactionCardList
              transactions={transactions}
              baseCode={baseCurrency}
              selectedIds={selectedIds}
              selectionScope={selectionScope}
              selectionMode={selectionMode}
              onSelect={handleSelect}
              onOpen={handleCardOpen}
              onLongPress={handleCardLongPress}
              // A day header is a claim that the rows beneath it are that
              // day's. Only the date ordering makes that true.
              groupByDay={sortBy === 'date'}
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-10">
                    <Checkbox
                      checked={
                        selectionScope === 'all-matching' ||
                        (transactions.length > 0 &&
                          transactions.every((tx) => selectedIds.has(tx.id)))
                      }
                      onCheckedChange={(v) => handleSelectAll(v === true)}
                      aria-label="Select all"
                    />
                  </TableHead>
                  <SortableHeader label="Date" column="date" onSort={setSort} />
                  <SortableHeader label="Description" column="description" onSort={setSort} />
                  <SortableHeader label="Category" column="category" onSort={setSort} />
                  <SortableHeader label="Tags" column="tags" onSort={setSort} />
                  <SortableHeader label="Amount" column="amount" onSort={setSort} align="right" />
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {transactions.map((tx) => (
                  <TransactionRow
                    key={tx.id}
                    transaction={tx}
                    categories={categories}
                    selected={
                      selectionScope === 'all-matching' ||
                      selectedIds.has(tx.id)
                    }
                    // In all-matching mode, individual row checkboxes are locked.
                    // Toggling a single row can't express "all rows except this
                    // one" cleanly — and silently demoting to page scope would
                    // appear to clear the selection when selectedIds is empty.
                    // The header checkbox still clears the full selection.
                    onSelect={
                      selectionScope === 'all-matching' ? undefined : handleSelect
                    }
                    onUpdate={handleRowUpdate}
                    onDelete={handleRowDelete}
                    onError={setRowError}
                    descriptionSuggestions={suggestions.descriptions}
                    tagSuggestions={suggestions.tags}
                  />
                ))}
              </TableBody>
            </Table>
          )}

          {isMobile && selectionBar && (
            // In flow, so it contributes height and occludes nothing; sticky,
            // so it stays on screen while the list scrolls under it. Sits
            // before the pager, so reaching the pager is exactly what releases
            // it. No shadow — app-chrome bars in this repo separate with a
            // border, and `border-t` on the pager below already does that.
            <div className="sticky bottom-0 z-20 border-t border-border bg-background p-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
              {selectionBar}
            </div>
          )}

          <div className="border-t">
            <PaginationBar
              page={page}
              totalPages={totalPages}
              perPage={perPage}
              onPageChange={handlePageChange}
              onPerPageChange={setPerPage}
              compact={isMobile}
            />
          </div>
        </Card>
      )}

      {/*
        Phone edit surface. Deliberately NOT gated on `isMobile`: it opens only
        from a card tap, so it cannot appear on desktop — and leaving it mounted
        means rotating a phone mid-edit does not discard what has been typed.
      */}
      <TransactionEditSheet
        transaction={editingTx}
        categories={categories}
        onClose={closeEditSheet}
        onUpdate={handleRowUpdate}
        onDelete={handleRowDelete}
        onError={setRowError}
        onCloseFocus={focusAfterSheetClose}
        descriptionSuggestions={suggestions.descriptions}
        tagSuggestions={suggestions.tags}
      />

      <Dialog
        open={confirmAllMatchingOpen}
        onOpenChange={(open) => {
          // Block closing while the delete is in-flight so the user can't
          // dismiss the spinner mid-request and lose track of state.
          if (deleting) return;
          setConfirmAllMatchingOpen(open);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete all matching transactions?</DialogTitle>
            <DialogDescription>
              This will move{' '}
              <span className="font-semibold text-foreground">
                {total.toLocaleString()}
              </span>{' '}
              transaction{total !== 1 ? 's' : ''} matching your current
              filters to Trash, including rows you have not yet viewed. They
              can be restored from Trash later.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setConfirmAllMatchingOpen(false)}
              disabled={deleting}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={() => void executeBulkDelete()}
              disabled={deleting}
            >
              {deleting ? 'Deleting...' : `Delete ${total.toLocaleString()}`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/*
        Bulk-edit dialog. Page mode dispatches the PATCH directly. All-
        matching mode stages the patch into `bulkConfirm`, which renders a
        BulkEditConfirmDialog summarizing the changes before firing — per
        spec §3.4 the filter-defined scope is large and irrecoverable
        enough to deserve a confirmation step.
      */}
      <BulkEditDialog
        open={bulkEditOpen}
        onClose={() => setBulkEditOpen(false)}
        count={selectionCount}
        categories={categories}
        onSubmit={(p) => {
          if (selectionScope === 'all-matching') {
            setBulkConfirm(p);
            return;
          }
          // RETURN the promise. BulkEditDialog awaits onSubmit so RHF holds
          // formState.isSubmitting for the duration, which is what makes the
          // Apply button and the Cmd/Ctrl+Enter chord refuse re-entry. With a
          // discarded `void dispatchBulkEdit(p)` the flag flipped back before
          // the request left, so every extra click fired another bulk PATCH.
          return dispatchBulkEdit(p);
        }}
      />

      {bulkConfirm && (
        <BulkEditConfirmDialog
          open={true}
          onCancel={() => setBulkConfirm(null)}
          // Close-before-dispatch, matching the Replace All call site below.
          // Both dialogs auto-close anyway — AlertDialogAction fires
          // onOpenChange(false) right after onConfirm, which routes through
          // onCancel — so relying on dispatchBulkEdit's own setBulkConfirm(null)
          // meant the confirm-state lifetime was decided by Radix rather than
          // here, and read as "stays open to retry" when it does not. Retry
          // after a failure is offered by the BulkEditDialog underneath, which
          // keeps the user's patch and closes only on success.
          onConfirm={() => {
            const p = bulkConfirm;
            setBulkConfirm(null);
            void dispatchBulkEdit(p);
          }}
          count={selectionCount}
          patch={bulkConfirm}
          categoryName={(id) =>
            categories.find((c) => c.id === id)?.name ?? ''
          }
        />
      )}

      {replaceConfirm && (
        <BulkEditConfirmDialog
          open={true}
          onCancel={() => setReplaceConfirm(null)}
          onConfirm={() => {
            const p = replaceConfirm;
            setReplaceConfirm(null);
            void dispatchReplaceAll(p);
          }}
          count={total}
          patch={replaceConfirm}
          categoryName={(id) =>
            categories.find((c) => c.id === id)?.name ?? ''
          }
        />
      )}
    </div>
  );
}
