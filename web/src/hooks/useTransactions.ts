import { useState, useEffect, useCallback } from 'react';
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
  clearPanelFilters: () => void;
  setPage: (page: number) => void;
  setPerPage: (perPage: number) => void;
  setSort: (column: SortColumn) => void;
  initialLoad: boolean;
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
  const [transactions, setTransactions] = useState<Transaction[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [perPage, setPerPageState] = useState(getInitialPerPage);
  const [sortBy, setSortBy] = useState<SortColumn>('date');
  const [sortDir, setSortDir] = useState<SortDirection>('desc');
  const [filters, setFilters] = useState<TransactionFilters>(defaultFilters);
  const [initialLoad, setInitialLoad] = useState(true);
  const [error, setError] = useState('');

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

  // fetchTransactionsAsync is the awaitable sibling of fetchTransactions.
  // The state-update side effects (setTransactions/setTotal/setError/
  // setInitialLoad) are identical — the only difference is that callers
  // can await the response to read the freshly-loaded ids (see
  // bulkUpdate, which uses the post-PATCH refetch result for selection
  // pruning per spec §3.5). On error the rejection re-throws so callers
  // who awaited can distinguish refetch failure from PATCH failure.
  const fetchTransactionsAsync = useCallback(async (): Promise<
    PaginatedResponse<Transaction>
  > => {
    setError('');
    try {
      const data = await api.get<PaginatedResponse<Transaction>>(
        `transactions?${buildQuery()}`,
      );
      setTransactions(data.transactions);
      setTotal(data.total);
      return data;
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to load transactions',
      );
      throw err;
    } finally {
      setInitialLoad(false);
    }
  }, [buildQuery]);

  // Void-returning wrapper kept for the mount effect and the external
  // `refetch` callers that fire-and-forget. Errors are already handled
  // (setError) inside fetchTransactionsAsync; we swallow the rejection
  // here so the void-returning shape doesn't leak unhandled promise
  // rejections to callers who never opted into awaiting.
  const fetchTransactions = useCallback(() => {
    fetchTransactionsAsync().catch(() => {
      // Intentionally empty — see comment above.
    });
  }, [fetchTransactionsAsync]);

  useEffect(() => {
    fetchTransactions();
  }, [fetchTransactions]);

  const setFilter = useCallback(
    (key: keyof TransactionFilters, value: string) => {
      setFilters((prev) => ({ ...prev, [key]: value }));
      setPage(1);
    },
    [],
  );

  const clearFilters = useCallback(() => {
    setFilters(defaultFilters);
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

  const createTransaction = useCallback(
    async (input: CreateTransactionInput): Promise<Transaction> => {
      const created = await api.post<Transaction>('transactions', input);
      fetchTransactions();
      return created;
    },
    [fetchTransactions],
  );

  const updateTransaction = useCallback(
    async (input: UpdateTransactionInput) => {
      const { id, ...body } = input;
      await api.put(`transactions/${id}`, body);
      fetchTransactions();
    },
    [fetchTransactions],
  );

  const deleteTransaction = useCallback(
    async (id: number) => {
      await api.del(`transactions/${id}`);
      fetchTransactions();
    },
    [fetchTransactions],
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
    fetchTransactions();
    return result.deleted;
  }, [buildFilterQuery, fetchTransactions]);

  const bulkUpdate = useCallback(
    async (
      args: BatchUpdateRequest,
    ): Promise<BulkUpdateResponse & { visibleIds: number[] }> => {
      const result = await api.post<BulkUpdateResponse>(
        'transactions/batch-update',
        args,
      );
      // Refetch via the awaitable sibling so we can read the freshly-
      // loaded ids and echo them as visibleIds. A refetch failure here
      // means the data change landed but the view is stale — the
      // wrapped error tells the page to show the differentiated toast.
      let refreshed: PaginatedResponse<Transaction>;
      try {
        refreshed = await fetchTransactionsAsync();
      } catch (err) {
        throw new RefetchAfterMutationError(
          err instanceof Error ? err.message : String(err),
        );
      }
      return { ...result, visibleIds: refreshed.transactions.map((t) => t.id) };
    },
    [fetchTransactionsAsync],
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
        await fetchTransactionsAsync();
      } catch (err) {
        throw new RefetchAfterMutationError(
          err instanceof Error ? err.message : String(err),
        );
      }
      return result;
    },
    [fetchTransactionsAsync],
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
    clearPanelFilters,
    setPage,
    setPerPage,
    setSort,
    initialLoad,
    error,
    refetch: fetchTransactions,
    createTransaction,
    updateTransaction,
    deleteTransaction,
    deleteByFilter,
    bulkUpdate,
    bulkUpdateByFilter,
    buildFilterQuery,
  };
}
