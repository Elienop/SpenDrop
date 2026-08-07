import { useCallback, useEffect, useState } from 'react';
import {
  keepPreviousData,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { api } from '../api/client';
import type {
  Transaction,
  PaginatedResponse,
  BatchUpdateRequest,
  BulkUpdateByFilterRequest,
  BulkUpdateResponse,
} from '../api/types';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import {
  TRANSACTION_PAGE_SIZES,
  DEFAULT_TRANSACTIONS_PER_PAGE,
} from '@/lib/constants';

/**
 * RefetchAfterMutationError flags a successful mutation followed by a
 * failed list refetch. The page surfaces a different toast for this
 * case so the user knows their data change landed even though the
 * view is stale. See `2026-05-01-transactions-bulk-edit-design.md`
 * §3.5 — the differentiated copy is the difference between "your edit
 * didn't apply" and "your edit applied, but you need to reload to
 * see it" and is load-bearing for trust in bulk edits.
 */
export class RefetchAfterMutationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RefetchAfterMutationError';
  }
}

export interface TransactionFilters {
  dateFrom: string;
  dateTo: string;
  categoryId: string;
  categoryIds: string;
  amountMin: string;
  amountMax: string;
  tags: string;
  type: string;
  search: string;
}

export interface CreateTransactionInput {
  date: string;
  amount: number;
  description: string;
  category_id: number;
  original_amount?: number;
  original_currency?: string;
  tags?: string;
  notes?: string;
  /**
   * Client-minted idempotency key for one create (see `newClientKey`). The
   * server returns the row already created under this key instead of making a
   * second one, so re-sending a submission whose response was lost is safe.
   * Optional: rows queued offline before this field existed replay without it,
   * and the server treats a missing key as "no replay protection", exactly as
   * before.
   */
  client_key?: string;
}

export interface UpdateTransactionInput extends CreateTransactionInput {
  id: number;
}

export type SortColumn = 'date' | 'description' | 'category' | 'amount' | 'tags';
export type SortDirection = 'asc' | 'desc';

interface UseTransactionsResult {
  transactions: Transaction[];
  total: number;
  page: number;
  perPage: number;
  sortBy: SortColumn;
  sortDir: SortDirection;
  filters: TransactionFilters;
  setFilter: (key: keyof TransactionFilters, value: string) => void;
  clearFilters: () => void;
  /**
   * Live text in the search box. This is what the input renders, so it
   * echoes every keystroke immediately; `filters.search` — the term in the
   * query key AND in `buildFilterQuery()` — trails it by
   * SEARCH_DEBOUNCE_MS. Programmatic sets (`setFilter('search', …)`,
   * `clearFilters()`) move both at once and skip the debounce entirely.
   */
  searchInput: string;
  setSearchInput: (value: string) => void;
  clearPanelFilters: () => void;
  setPage: (page: number) => void;
  setPerPage: (perPage: number) => void;
  setSort: (column: SortColumn) => void;
  initialLoad: boolean;
  /** True during any fetch, including refetches over data already on screen. */
  fetching: boolean;
  /**
   * True while the rows and `total` on screen belong to a previous query key
   * (the user changed a filter and the new page has not landed). Callers must
   * not fire a filter-scoped write while this is true — `total` labels the
   * button but the write would target the NEW filters.
   */
  showingPrevious: boolean;
  /**
   * True while `searchInput` has not reached `filters.search` yet — the
   * window in which the visible search box and the serialized term
   * disagree. Filter-scoped writes must sit out this window for the same
   * reason they sit out `showingPrevious`: `buildFilterQuery()` would
   * resolve against a term the user has already typed over.
   */
  searchPending: boolean;
  error: string;
  refetch: () => void;
  createTransaction: (input: CreateTransactionInput) => Promise<Transaction>;
  updateTransaction: (input: UpdateTransactionInput) => Promise<void>;
  deleteTransaction: (id: number) => Promise<void>;
  /**
   * Deletes every transaction matching the current filters (ignoring
   * pagination). Returns the count of rows actually deleted. Used by
   * the "Select all X across pages" UI path — the ID-based batch delete
   * tops out at 500 rows server-side, which is too small for the
   * "delete all and re-import" workflow.
   */
  deleteByFilter: () => Promise<number>;
  /**
   * Patches a list of transactions by id (max 500 server-side) and
   * refetches the visible page. The returned `visibleIds` is the id
   * set the user can still see after the patch — the page intersects
   * it with the current `selectedIds` to prune rows that no longer
   * match the active filter. Throws `RefetchAfterMutationError` when
   * the PATCH succeeded but the post-PATCH refetch failed (data
   * changed, view stale).
   */
  bulkUpdate: (
    args: BatchUpdateRequest,
  ) => Promise<BulkUpdateResponse & { visibleIds: number[] }>;
  /**
   * Patches every transaction matching `filterQuery` (the same
   * serialized filter querystring used by the list endpoint) and
   * refetches. Filter-mode bypasses the prune step (per spec §3.5)
   * because `selectionScope === 'all-matching'` is filter-defined and
   * auto-corrects on refetch — no `visibleIds` echo needed. Throws
   * `RefetchAfterMutationError` when the PATCH succeeded but the
   * post-PATCH refetch failed.
   */
  bulkUpdateByFilter: (
    args: BulkUpdateByFilterRequest & { filterQuery: string },
  ) => Promise<BulkUpdateResponse>;
  /**
   * Serialize the current filters into the same querystring shape the list
   * endpoint uses (no pagination/sort). Exposed so the page can hand it to
   * `bulkUpdateByFilter` without re-implementing the per-key serialization
   * (which has subtle rules — `categoryIds` overrides `categoryId`, empty
   * fields are omitted, etc.).
   */
  buildFilterQuery: () => string;
}

const defaultFilters: TransactionFilters = {
  dateFrom: '',
  dateTo: '',
  categoryId: '',
  categoryIds: '',
  amountMin: '',
  amountMax: '',
  tags: '',
  type: '',
  search: '',
};

/**
 * Trailing-debounce window for the search box. Long enough to swallow a
 * burst of keystrokes at ordinary typing speed (inter-key gaps sit around
 * 100-200ms), short enough that the results still read as following the
 * typing rather than lagging it.
 */
const SEARCH_DEBOUNCE_MS = 250;

function getInitialPerPage(): number {
  try {
    const stored = localStorage.getItem(STORAGE_KEYS.transactionsPerPage);
    if (stored) {
      const parsed = Number(stored);
      if ((TRANSACTION_PAGE_SIZES as readonly number[]).includes(parsed)) {
        return parsed;
      }
    }
  } catch {
    // localStorage not available
  }
  return DEFAULT_TRANSACTIONS_PER_PAGE;
}

export function useTransactions(): UseTransactionsResult {
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [perPage, setPerPageState] = useState(getInitialPerPage);
  const [sortBy, setSortBy] = useState<SortColumn>('date');
  const [sortDir, setSortDir] = useState<SortDirection>('desc');
  const [filters, setFilters] = useState<TransactionFilters>(defaultFilters);
  // Live search text. `filters.search` is the COMMITTED term — the only one
  // that reaches the query key or buildFilterQuery — and it trails this by
  // SEARCH_DEBOUNCE_MS. Keeping the two apart (rather than debouncing the
  // whole filters object) means everything that DESCRIBES the result set on
  // screen — the count, the export URL, the replace bar — reads the same
  // term the rows were fetched with, while only the input itself shows what
  // the user has typed so far.
  const [searchInput, setSearchInput] = useState('');

  // Trailing debounce: one commit per typing pause, not one per keystroke.
  // Depending on `filters.search` as well as `searchInput` is what makes the
  // effect idempotent — once the commit lands the two are equal and the
  // re-run bails out instead of scheduling a second identical write.
  useEffect(() => {
    if (searchInput === filters.search) return;
    const timer = setTimeout(() => {
      setFilters((prev) => ({ ...prev, search: searchInput }));
      setPage(1);
    }, SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [searchInput, filters.search]);

  // The window where the box and the serialized term disagree. Exposed
  // rather than derived on the page so there is exactly one definition of
  // "the filters a write would resolve against are not what you can see".
  const searchPending = searchInput !== filters.search;

  // Filter-only query string (no pagination/sort). Shared by buildQuery
  // (list endpoint) and deleteByFilter (destructive endpoint) so both
  // paths serialize filters identically — otherwise the "delete all
  // visible rows" action could diverge from what the user sees.
  const buildFilterQuery = useCallback(() => {
    const params = new URLSearchParams();
    if (filters.dateFrom) params.set('date_from', filters.dateFrom);
    if (filters.dateTo) params.set('date_to', filters.dateTo);
    if (filters.categoryIds) {
      params.set('category_ids', filters.categoryIds);
    } else if (filters.categoryId) {
      params.set('category_id', filters.categoryId);
    }
    if (filters.amountMin) params.set('amount_min', filters.amountMin);
    if (filters.amountMax) params.set('amount_max', filters.amountMax);
    if (filters.tags) params.set('tags', filters.tags);
    if (filters.type) params.set('type', filters.type);
    if (filters.search) params.set('search', filters.search);
    return params.toString();
  }, [filters]);

  const buildQuery = useCallback(() => {
    const params = new URLSearchParams(buildFilterQuery());
    params.set('page', String(page));
    params.set('per_page', String(perPage));
    params.set('sort_by', sortBy);
    params.set('sort_dir', sortDir);
    return params.toString();
  }, [buildFilterQuery, page, perPage, sortBy, sortDir]);

  // Stable key with `'transactions'` as the first segment so an SSE-driven
  // invalidateQueries({ queryKey: ['transactions'] }) prefix-matches and
  // refetches the visible page (see useLiveUpdates). TanStack dedups and
  // coalesces concurrent fetches and ignores out-of-order responses, which
  // retires the hand-rolled genRef guard the old hook carried.
  const query = useQuery<PaginatedResponse<Transaction>>({
    queryKey: [
      'transactions',
      filters,
      page,
      perPage,
      sortBy,
      sortDir,
    ],
    queryFn: () =>
      api.get<PaginatedResponse<Transaction>>(`transactions?${buildQuery()}`),
    // The filters/page/sort all sit IN the key, so every keystroke in the
    // search box mints a new key. Without this, each one lands on an empty
    // cache entry, `isLoading` flips true and the whole table is replaced by
    // a skeleton mid-typing. Holding the previous key's rows keeps the list
    // on screen and confines the skeleton to the genuine no-data-yet case.
    // Same-key refetches (the SSE invalidate path) are unaffected: they keep
    // their own data and leave `isPlaceholderData` false.
    placeholderData: keepPreviousData,
  });

  const transactions = query.data?.transactions ?? [];
  const total = query.data?.total ?? 0;
  // `initialLoad` keeps the legacy contract: true until the first settle.
  const initialLoad = query.isLoading;
  const fetching = query.isFetching;
  // True while the rows/total on screen were loaded under a DIFFERENT key
  // than the current filters — i.e. they are the held-over previous page.
  // `total` is stale for exactly this window, so any control whose scope is
  // "everything matching the current filters" has to sit out until it clears
  // (see the Replace All button). A same-key background refetch never sets
  // this, so live updates don't disable anything.
  const showingPrevious = query.isPlaceholderData;
  const error = query.isError
    ? query.error instanceof Error
      ? query.error.message
      : 'Failed to load transactions'
    : '';

  // refetch returns void (the legacy fire-and-forget shape). The awaitable
  // form lives inline in the mutations below via query.refetch.
  const refetch = useCallback(() => {
    void query.refetch();
  }, [query]);

  // Awaitable refetch of the current visible page, used by bulkUpdate to
  // read the freshly-loaded ids for selection pruning (spec §3.5). Throws
  // on failure so the mutation can wrap it in RefetchAfterMutationError.
  const refetchAsync = useCallback(async (): Promise<
    PaginatedResponse<Transaction>
  > => {
    const result = await query.refetch({ throwOnError: true });
    if (!result.data) {
      throw new Error('Failed to load transactions');
    }
    return result.data;
  }, [query]);

  // Programmatic filter sets commit immediately and drag the search box with
  // them. Loading a saved filter or clearing is a discrete choice, not
  // typing — debouncing it would leave the box showing the OLD term for
  // 250ms, and (worse) the debounce effect would then see a mismatch and
  // write the stale box value back over the filter that was just applied.
  const setFilter = useCallback(
    (key: keyof TransactionFilters, value: string) => {
      setFilters((prev) => ({ ...prev, [key]: value }));
      if (key === 'search') setSearchInput(value);
      setPage(1);
    },
    [],
  );

  const clearFilters = useCallback(() => {
    setFilters(defaultFilters);
    setSearchInput(defaultFilters.search);
    setPage(1);
  }, []);

  const setPerPage = useCallback((value: number) => {
    setPerPageState(value);
    setPage(1);
    try {
      localStorage.setItem(STORAGE_KEYS.transactionsPerPage, String(value));
    } catch {
      // localStorage not available
    }
  }, []);

  const setSort = useCallback((column: SortColumn) => {
    setSortBy((prev) => {
      if (prev === column) {
        setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
      } else {
        setSortDir('desc');
      }
      return column;
    });
    setPage(1);
  }, []);

  const clearPanelFilters = useCallback(() => {
    setFilters((prev) => ({
      ...prev,
      dateFrom: '',
      dateTo: '',
      categoryId: '',
      categoryIds: '',
      amountMin: '',
      amountMax: '',
      tags: '',
    }));
    setPage(1);
  }, []);

  // Mutations invalidate the whole `['transactions']` family locally so the
  // acting device updates immediately without waiting for its own SSE echo.
  const createTransaction = useCallback(
    async (input: CreateTransactionInput): Promise<Transaction> => {
      const created = await api.post<Transaction>('transactions', input);
      void queryClient.invalidateQueries({ queryKey: ['transactions'] });
      return created;
    },
    [queryClient],
  );

  const updateTransaction = useCallback(
    async (input: UpdateTransactionInput) => {
      const { id, ...body } = input;
      await api.put(`transactions/${id}`, body);
      void queryClient.invalidateQueries({ queryKey: ['transactions'] });
    },
    [queryClient],
  );

  const deleteTransaction = useCallback(
    async (id: number) => {
      await api.del(`transactions/${id}`);
      void queryClient.invalidateQueries({ queryKey: ['transactions'] });
    },
    [queryClient],
  );

  const deleteByFilter = useCallback(async (): Promise<number> => {
    const qs = buildFilterQuery();
    const path = qs
      ? `transactions/delete-by-filter?${qs}`
      : 'transactions/delete-by-filter';
    // Empty body — filters live in the query string to reuse the same
    // serialization as GET /transactions. api.post sends Content-Type:
    // application/json automatically, which is required by the
    // requireJSONContentType middleware on all mutating API routes.
    const result = await api.post<{ deleted: number }>(path, {});
    void queryClient.invalidateQueries({ queryKey: ['transactions'] });
    return result.deleted;
  }, [buildFilterQuery, queryClient]);

  const bulkUpdate = useCallback(
    async (
      args: BatchUpdateRequest,
    ): Promise<BulkUpdateResponse & { visibleIds: number[] }> => {
      const result = await api.post<BulkUpdateResponse>(
        'transactions/batch-update',
        args,
      );
      // Refetch the visible page so we can read the freshly-loaded ids and
      // echo them as visibleIds. A refetch failure here means the data
      // change landed but the view is stale — the wrapped error tells the
      // page to show the differentiated toast.
      let refreshed: PaginatedResponse<Transaction>;
      try {
        refreshed = await refetchAsync();
      } catch (err) {
        throw new RefetchAfterMutationError(
          err instanceof Error ? err.message : String(err),
        );
      }
      return { ...result, visibleIds: refreshed.transactions.map((t) => t.id) };
    },
    [refetchAsync],
  );

  const bulkUpdateByFilter = useCallback(
    async (
      args: BulkUpdateByFilterRequest & { filterQuery: string },
    ): Promise<BulkUpdateResponse> => {
      const { filterQuery, ...body } = args;
      // Filter-mode reuses the list endpoint's querystring serialization
      // (caller passes it pre-built so the page can drop pagination/sort
      // — same shape as deleteByFilter above). No prune for this path
      // per spec §3.5: scope is filter-defined, not id-defined.
      const result = await api.post<BulkUpdateResponse>(
        `transactions/update-by-filter?${filterQuery}`,
        body,
      );
      try {
        await refetchAsync();
      } catch (err) {
        throw new RefetchAfterMutationError(
          err instanceof Error ? err.message : String(err),
        );
      }
      return result;
    },
    [refetchAsync],
  );

  return {
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
    fetching,
    showingPrevious,
    searchPending,
    error,
    refetch,
    createTransaction,
    updateTransaction,
    deleteTransaction,
    deleteByFilter,
    bulkUpdate,
    bulkUpdateByFilter,
    buildFilterQuery,
  };
}
