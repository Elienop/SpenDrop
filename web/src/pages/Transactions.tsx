import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { format } from 'date-fns';
import {
  AlertCircle,
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  X,
} from 'lucide-react';
import { api } from '../api/client';
import type { Category, SavedFilter } from '../api/types';
import { useTransactions } from '../hooks/useTransactions';
import type { TransactionFilters } from '../hooks/useTransactions';
import type { SortColumn } from '../hooks/useTransactions';
import { useSavedFilters } from '../hooks/useSavedFilters';
import { TransactionToolbar } from '../components/TransactionToolbar';
import { FilterPanel } from '../components/FilterPanel';
import { TransactionEntryRow } from '../components/TransactionEntryRow';
import { TransactionRow } from '../components/TransactionRow';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
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

function PaginationBar({
  page,
  totalPages,
  perPage,
  onPageChange,
  onPerPageChange,
}: PaginationBarProps) {
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

      <div className="flex items-center gap-6 lg:gap-8">
        <div className="flex w-[100px] items-center justify-center text-sm font-medium">
          Page {page} of {totalPages}
        </div>
        <div className="flex items-center gap-2">
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
    </div>
  );
}

interface SortableHeaderProps {
  label: string;
  column: SortColumn;
  activeSortBy: SortColumn;
  activeSortDir: 'asc' | 'desc';
  onSort: (column: SortColumn) => void;
  className?: string;
}

function SortableHeader({
  label,
  column,
  activeSortBy,
  activeSortDir,
  onSort,
  className,
}: SortableHeaderProps) {
  const isActive = activeSortBy === column;

  return (
    <TableHead className={className}>
      <Button
        variant="ghost"
        size="sm"
        className="-ml-3"
        onClick={() => onSort(column)}
      >
        {label}
        {isActive ? (
          activeSortDir === 'asc' ? (
            <ArrowUp className="ml-2 size-4" />
          ) : (
            <ArrowDown className="ml-2 size-4" />
          )
        ) : (
          <ArrowUpDown className="ml-2 size-4" />
        )}
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
    sortBy,
    sortDir,
    filters,
    setFilter,
    clearFilters,
    clearPanelFilters,
    setPage,
    setPerPage,
    setSort,
    loading,
    error,
    createTransaction,
    updateTransaction,
    deleteTransaction,
  } = useTransactions();

  const [showFilters, setShowFilters] = useState(false);
  const [showEntry, setShowEntry] = useState(false);
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
        // Only apply keys that are part of our known filter shape and carry strings.
        const knownKeys = Object.keys(filters) as (keyof typeof filters)[];
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
    [setFilter, clearFilters, filters],
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
    <div className="flex flex-col gap-4">
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
          onSubmit={createTransaction}
          onDelete={deleteTransaction}
        />
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertCircle className="size-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {loading ? (
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

          <Table>
            <TableHeader>
              <TableRow>
                <SortableHeader
                  label="Date"
                  column="date"
                  activeSortBy={sortBy}
                  activeSortDir={sortDir}
                  onSort={setSort}
                />
                <SortableHeader
                  label="Description"
                  column="description"
                  activeSortBy={sortBy}
                  activeSortDir={sortDir}
                  onSort={setSort}
                />
                <SortableHeader
                  label="Category"
                  column="category"
                  activeSortBy={sortBy}
                  activeSortDir={sortDir}
                  onSort={setSort}
                />
                <TableHead>Tags</TableHead>
                <SortableHeader
                  label="Amount"
                  column="amount"
                  activeSortBy={sortBy}
                  activeSortDir={sortDir}
                  onSort={setSort}
                  className="text-right"
                />
                <TableHead className="w-10 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {transactions.map((tx) => (
                <TransactionRow
                  key={tx.id}
                  transaction={tx}
                  categories={categories}
                  onUpdate={updateTransaction}
                  onDelete={deleteTransaction}
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
