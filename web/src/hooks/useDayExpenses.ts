import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { PaginatedResponse, Transaction } from '@/api/types';

/**
 * `MaxPerPage` in `internal/api/limits.go` — the server clamps anything
 * larger rather than rejecting it, so asking for more would silently get 100
 * back and make `matched > expenses.length` unexplainable.
 */
export const DAY_EXPENSES_PER_PAGE = 100;

export interface UseDayExpensesResult {
  /** The day's expense rows, largest first. */
  expenses: Transaction[];
  /**
   * How many rows the server MATCHED, which can exceed `expenses.length` —
   * one page is capped at `DAY_EXPENSES_PER_PAGE`. The sheet says so rather
   * than quietly showing a short list.
   */
  matched: number;
  loading: boolean;
  /** Empty string when there is no error, matching the report hooks. */
  error: string;
  /**
   * Re-run the query after a failure. Recovery already worked by closing the
   * sheet and tapping the day again — which is a recovery path the user has to
   * GUESS at. This makes it an affordance.
   */
  retry: () => void;
}

/**
 * The expense rows behind one heatmap cell.
 *
 * EXPENSES ONLY, and that is not a filter choice — it is what makes the
 * sheet's header total and its list describe the same thing. The heatmap
 * cell's total comes from `SumExpensesByDay`
 * (`internal/database/queries.sql`, `WHERE c.type = 'expense'`), so a list
 * that also contained the day's income would sum to a different number than
 * the cell the user just tapped. `type=expense` is applied server-side by
 * `buildTransactionWhereClause`, the same helper the list endpoint uses for
 * every other filter.
 *
 * The first key segment is `'transactions'` so `useLiveUpdates`'
 * prefix-invalidation (`invalidateQueries({ queryKey: ['transactions'] })`)
 * reaches this too — another household device editing that day refreshes an
 * open sheet. Same convention as `useRecentTransactions`.
 *
 * There is no `enabled` gate here because the CALLER only mounts this hook
 * while the sheet is open on a day that has spending. That placement is
 * load-bearing beyond saving a request: `PatternsTab` is rendered by tests
 * that provide no `QueryClientProvider`, and a `useQuery` that mounted with
 * the card would throw in every one of them.
 */
export function useDayExpenses(date: string): UseDayExpensesResult {
  const query = useQuery<PaginatedResponse<Transaction>>({
    queryKey: ['transactions', 'heatmap-day', date],
    queryFn: () =>
      api.get<PaginatedResponse<Transaction>>(
        // AMOUNT-DESCENDING, not chronological, and that ordering is doing
        // real work: the sheet's header names a total, and the rows that
        // EXPLAIN a heavy day are then the ones above the fold. A later change
        // to `sort_by=date` would be defensible on its own terms but it has
        // this cost — on a 20-row day the largest expense could land anywhere.
        `transactions?date_from=${date}&date_to=${date}&type=expense` +
          `&per_page=${DAY_EXPENSES_PER_PAGE}&sort_by=amount&sort_dir=desc`,
      ),
  });

  return {
    expenses: query.data?.transactions ?? [],
    matched: query.data?.total ?? 0,
    loading: query.isPending,
    error: query.error instanceof Error ? query.error.message : '',
    retry: () => void query.refetch(),
  };
}
