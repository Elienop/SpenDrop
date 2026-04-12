import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { format } from 'date-fns';
import { toast } from 'sonner';
import {
  AlertCircle,
  ArrowUpDown,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Replace,
  Trash2,
  X,
} from 'lucide-react';
import { Input } from '@/components/ui/input';
import { api } from '../api/client';
import type { Category, SavedFilter } from '../api/types';
import { useTransactions } from '../hooks/useTransactions';
import type { TransactionFilters } from '../hooks/useTransactions';
import type { SortColumn } from '../hooks/useTransactions';
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

// TODO(post-migration): Bulk select + bulk categorize (Checkbox column + Dialog)
// are deferred. They require a new `useTransactions` hook method and a new
// backend endpoint — spec §3 line 305 flags this as out of scope for the
// migration. Do not add here without extending the hook.

interface ActiveChip {
  key: string;
  label: string;
  onClear: () => void;
}

function buildActiveChips(
  filters: TransactionFilters,
  categories: Category[],
  setFilter: (key: keyof TransactionFilters, value: string) => void,
): ActiveChip[] {
  const chips: ActiveChip[] = [];

  if (filters.dateFrom || filters.dateTo) {
    let label: string;
    if (filters.dateFrom && filters.dateTo) {
      label = `${format(new Date(filters.dateFrom), 'MMM d')} - ${format(new Date(filters.dateTo), 'MMM d')}`;
    } else if (filters.dateFrom) {
      label = `From ${format(new Date(filters.dateFrom), 'MMM d')}`;
    } else {
      label = `Until ${format(new Date(filters.dateTo), 'MMM d')}`;
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
      label = `$${filters.amountMin} - $${filters.amountMax}`;
    } else if (filters.amountMin) {
      label = `Min $${filters.amountMin}`;
    } else {
      label = `Max $${filters.amountMax}`;
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

const PER_PAGE_OPTIONS = [10, 20, 50, 100] as const;

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
              {PER_PAGE_OPTIONS.map((opt) => (
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
  const [deleting, setDeleting] = useState(false);
  const [rowError, setRowError] = useState('');
  const cardRef = useRef<HTMLDivElement>(null);

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
  }, []);

  const handleSelectAll = useCallback(
    (checked: boolean) => {
      if (checked) {
        setSelectedIds(new Set(transactions.map((tx) => tx.id)));
      } else {
        setSelectedIds(new Set());
      }
    },
    [transactions],
  );

  const handleBulkDelete = useCallback(async () => {
    if (selectedIds.size === 0) return;
    setDeleting(true);
    try {
      const res = await api.post<{ deleted: number }>('transactions/batch-delete', {
        ids: Array.from(selectedIds),
      });
      toast.success(`Deleted ${res.deleted} transaction${res.deleted !== 1 ? 's' : ''}`);
      setSelectedIds(new Set());
      refetch();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Delete failed');
    } finally {
      setDeleting(false);
    }
  }, [selectedIds, refetch]);

  const {
    savedFilters,
    saveFilter,
    deleteFilter: deleteSavedFilter,
  } = useSavedFilters();

  const [categories, setCategories] = useState<Category[]>([]);

  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch((err) => {
        console.warn('Failed to load categories', err);
      });
  }, []);

  // Clear selection when page or data changes
  useEffect(() => {
    setSelectedIds(new Set());
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
    () => (showFilters ? [] : buildActiveChips(filters, categories, setFilter)),
    [filters, showFilters, categories, setFilter],
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
          onDelete={deleteTransaction}
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

          {selectedIds.size > 0 && (
            <div className="flex items-center gap-3 rounded-lg border bg-muted/50 px-4 py-2">
              <span className="text-sm font-medium">
                {selectedIds.size} selected
              </span>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                className="h-8 gap-1.5 text-xs"
                onClick={() => void handleBulkDelete()}
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
                onClick={() => setSelectedIds(new Set())}
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
                    checked={
                      transactions.length > 0 &&
                      transactions.every((tx) => selectedIds.has(tx.id))
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
                  selected={selectedIds.has(tx.id)}
                  onSelect={handleSelect}
                  onUpdate={updateTransaction}
                  onDelete={deleteTransaction}
                  onError={setRowError}
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
    </div>
  );
}
