import { useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { PaginatedResponse, Transaction } from '@/api/types';

export interface UseRecentTransactionsResult {
  /** The most recently-entered saved transactions; empty while offline. */
  recent: Transaction[];
  /** Re-pull the list (after a delete). */
  refetch: () => void;
}

/**
 * Loads the last `limit` saved transactions for the /quick "Recently added"
 * panel, ordered by ENTRY TIME (`created_at`), not transaction date — so a
 * just-added but earlier-dated row (e.g. a salary dated the 1st entered on the
 * 15th) still surfaces at the top of the panel instead of sinking below the
 * cutoff. Only fetches when `enabled` (i.e. online) — saved history lives on
 * the server, so offline the list is empty and the panel shows an offline
 * note. Re-pulls whenever `refreshSignal` changes (the screen bumps it after
 * each add), which is encoded in the query key.
 *
 * The query key's FIRST segment is `'transactions'` so the SSE-driven
 * invalidateQueries({ queryKey: ['transactions'] }) (see useLiveUpdates) also
 * refreshes this panel when another household device adds a row. TanStack's
 * keyed cache and `enabled` gate replace the old genRef out-of-order guard
 * and the manual disable/clear effect.
 */
export function useRecentTransactions(
  limit: number,
  enabled: boolean,
  refreshSignal: number,
): UseRecentTransactionsResult {
  const query = useQuery<PaginatedResponse<Transaction>>({
    queryKey: ['transactions', 'recent', limit, refreshSignal],
    queryFn: () =>
      api.get<PaginatedResponse<Transaction>>(
        `transactions?page=1&per_page=${limit}&sort_by=created_at&sort_dir=desc`,
      ),
    enabled,
  });

  const refetch = useCallback(() => {
    void query.refetch();
  }, [query]);

  // When disabled (offline) the query never fetches and `data` is undefined,
  // so the panel shows the offline note instead of a stale list.
  return { recent: enabled ? (query.data?.transactions ?? []) : [], refetch };
}
