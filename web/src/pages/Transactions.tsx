import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import { toast } from 'sonner';
import {
  AlertCircle,
  ArrowUpDown,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Pencil,
  Replace,
  Trash2,
  X,
} from 'lucide-react';
import { Input } from '@/components/ui/input';
import { api } from '../api/client';
import type { Category, SavedFilter } from '../api/types';
import { UNDO_TOAST_MS } from '../lib/undo';
import { useTransactions } from '../hooks/useTransactions';
import type { TransactionFilters } from '../hooks/useTransactions';
import type { SortColumn } from '../hooks/useTransactions';
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
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
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
import { TRANSACTION_PAGE_SIZES } from '@/lib/constants';

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

interface PaginationBarProps {
  page: number;
  totalPages: number;
  perPage: number;
  onPageChange: (page: number) => void;
  onPerPageChange: (perPage: number) => void;
}

/**
 * Compute which page numbers to show between prev/next arrows.
 * Always shows first, last, and up to 2 pages around the current page,
 * with -1 as a sentinel for ellipsis gaps.
 */
function getPageNumbers(page: number, totalPages: number): number[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const pages = new Set<number>();
  pages.add(1);
  pages.add(totalPages);
  for (let i = Math.max(2, page - 1); i <= Math.min(totalPages - 1, page + 1); i++) {
    pages.add(i);
  }
  const sorted = [...pages].sort((a, b) => a - b);
  const result: number[] = [];
  for (let i = 0; i < sorted.length; i++) {
    if (i > 0 && sorted[i] - sorted[i - 1] > 1) {
      result.push(-1); // ellipsis
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
}: PaginationBarProps) {
  const pageNumbers = getPageNumbers(page, totalPages);

  return (
    <div className="flex items-center justify-between px-4 py-3">
      <div className="flex items-center gap-2">
        <p className="text-sm font-medium">Rows per page</p>
        <Select
          value={String(perPage)}
          onValueChange={(v) => onPerPageChange(Number(v))}
        >
          <SelectTrigger className="h-8 w-[70px]">
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
      </div>

      <div className="flex items-center gap-1">
        <Button
          variant="outline"
          size="icon"
          className="hidden size-8 lg:flex"
          onClick={() => onPageChange(1)}
          disabled={page <= 1}
        >
          <span className="sr-only">Go to first page</span>
          <ChevronsLeft />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="size-8"
          onClick={() => onPageChange(page - 1)}
          disabled={page <= 1}
        >
          <span className="sr-only">Go to previous page</span>
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
        >
          <span className="sr-only">Go to next page</span>
          <ChevronRight />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="hidden size-8 lg:flex"
          onClick={() => onPageChange(totalPages)}
          disabled={page >= totalPages}
        >
          <span className="sr-only">Go to last page</span>
          <ChevronsRight />
        </Button>
      </div>
    </div>
  );
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
  const {
    transactions,
    total,
    page,
    perPage,
    filters,
    setFilter,
    clearFilters,
    clearPanelFilters,
    setPage,
    setPerPage,
    setSort,
    initialLoad,
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

  const [suggestionsKey, setSuggestionsKey] = useState(0);
  const suggestions = useSuggestions(suggestionsKey);
  const [showFilters, setShowFilters] = useState(false);
  const [showEntry, setShowEntry] = useState(false);
  const [showReplace, setShowReplace] = useState(false);
  const [replaceText, setReplaceText] = useState('');
  const [replacing, setReplacing] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
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
  const [rowError, setRowError] = useState('');
  const cardRef = useRef<HTMLDivElement>(null);
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
      toast.success('Moved to Trash', {
        duration: UNDO_TOAST_MS,
        action: {
          label: 'Undo',
          onClick: () => {
            void api
              .post(`transactions/${id}/restore`, {})
              .then(() => {
                void queryClient.invalidateQueries({
                  queryKey: ['transactions'],
                });
                void queryClient.invalidateQueries({ queryKey: ['trash'] });
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

  const handleReplaceAll = useCallback(async () => {
    if (!replaceText.trim() || !filters.search.trim()) return;
    setReplacing(true);
    try {
      const res = await api.put<{ updated: number }>('transactions/bulk-rename', {
        search: filters.search,
        new_description: replaceText.trim(),
      });
      toast.success(`Renamed ${res.updated} transaction${res.updated !== 1 ? 's' : ''}`);
      setShowReplace(false);
      setReplaceText('');
      refetch();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Rename failed');
    } finally {
      setReplacing(false);
    }
  }, [replaceText, filters.search, refetch]);

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
    setSelectionScope('all-matching');
  }, []);

  const handleClearSelection = useCallback(() => {
    setSelectedIds(new Set());
    setSelectionScope('page');
  }, []);

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
      try {
        if (isFilterMode) {
          const filterQuery = buildFilterQuery();
          const { updated } = await bulkUpdateByFilter({ filterQuery, ...p });
          const noun = (n: number) =>
            `${n} transaction${n === 1 ? '' : 's'}`;
          toast.success(`Updated ${noun(updated)}`);
          handleClearSelection();
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
  // silently survives a filter edit or page flip.
  useEffect(() => {
    setSelectedIds(new Set());
    setSelectionScope('page');
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

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Transactions</h1>
        <Button variant="outline" size="sm" onClick={handleExport}>
          Export Excel
        </Button>
      </div>

      <TransactionToolbar
        search={filters.search}
        onSearchChange={(v) => setFilter('search', v)}
        type={filters.type}
        onTypeChange={(v) => setFilter('type', v)}
        activeFilterCount={activeFilterCount}
        showFilters={showFilters}
        onToggleFilters={() => setShowFilters((p) => !p)}
        showEntry={showEntry}
        onToggleEntry={() => setShowEntry((p) => !p)}
      />

      {filters.search && total > 0 && (
        <div className="flex items-center gap-2 rounded-lg border bg-card p-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 gap-1.5 text-xs"
            onClick={() => {
              setShowReplace((p) => !p);
              if (showReplace) setReplaceText('');
            }}
          >
            <Replace className="size-3.5" />
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
                  if (e.key === 'Enter') void handleReplaceAll();
                }}
                placeholder="New description..."
                className="h-8 max-w-xs text-xs"
                disabled={replacing}
              />
              <Button
                type="button"
                size="sm"
                className="h-8 text-xs"
                onClick={handleReplaceAll}
                disabled={replacing || !replaceText.trim()}
              >
                {replacing ? 'Replacing...' : `Replace All (${total})`}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="size-8"
                onClick={() => {
                  setShowReplace(false);
                  setReplaceText('');
                }}
              >
                <X className="size-3.5" />
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
        <TransactionEntryRow
          categories={categories}
          onSubmit={async (v) => {
            const tx = await createTransaction(v);
            setSuggestionsKey((k) => k + 1);
            return tx;
          }}
          onDelete={handleRowDelete}
          onClose={() => setShowEntry(false)}
          descriptionSuggestions={suggestions.descriptions}
          tagSuggestions={suggestions.tags}
        />
      </div>

      {(error || rowError) && (
        <Alert variant="destructive">
          <AlertCircle className="size-4" />
          <AlertDescription>{error || rowError}</AlertDescription>
        </Alert>
      )}

      {initialLoad ? (
        <TableSkeleton />
      ) : transactions.length === 0 ? (
        <Card className="p-6 text-center text-sm text-muted-foreground">
          No transactions found. Add one above to get started.
        </Card>
      ) : (
        <Card ref={cardRef} className="overflow-hidden">
          <PaginationBar
            page={page}
            totalPages={totalPages}
            perPage={perPage}
            onPageChange={handlePageChange}
            onPerPageChange={setPerPage}
          />

          {selectionCount > 0 && (
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
                  className="h-8 gap-1.5 text-xs"
                  onClick={() => setBulkEditOpen(true)}
                  disabled={deleting}
                >
                  <Pencil className="size-3.5" />
                  Edit ({selectionCount})
                </Button>
                <Button
                  type="button"
                  variant="destructive"
                  size="sm"
                  className="h-8 gap-1.5 text-xs"
                  onClick={requestBulkDelete}
                  disabled={deleting}
                >
                  <Trash2 className="size-3.5" />
                  {deleting ? 'Deleting...' : 'Delete'}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 text-xs"
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
              */}
              {selectionScope === 'page' &&
                transactions.length > 0 &&
                transactions.every((tx) => selectedIds.has(tx.id)) &&
                total > transactions.length && (
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span>
                      All {transactions.length} on this page selected.
                    </span>
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      className="h-auto p-0 text-xs"
                      onClick={handleSelectAllMatching}
                    >
                      Select all {total} matching
                    </Button>
                  </div>
                )}
            </div>
          )}

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
                  onUpdate={async (input) => {
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
                  }}
                  onDelete={handleRowDelete}
                  onError={setRowError}
                  descriptionSuggestions={suggestions.descriptions}
                  tagSuggestions={suggestions.tags}
                />
              ))}
            </TableBody>
          </Table>

          <div className="border-t">
            <PaginationBar
              page={page}
              totalPages={totalPages}
              perPage={perPage}
              onPageChange={handlePageChange}
              onPerPageChange={setPerPage}
            />
          </div>
        </Card>
      )}

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
          onConfirm={() => {
            void dispatchBulkEdit(bulkConfirm);
          }}
          count={selectionCount}
          patch={bulkConfirm}
          categoryName={(id) =>
            categories.find((c) => c.id === id)?.name ?? ''
          }
        />
      )}
    </div>
  );
}
