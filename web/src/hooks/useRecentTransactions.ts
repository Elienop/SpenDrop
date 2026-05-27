import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '@/api/client';
import type { PaginatedResponse, Transaction } from '@/api/types';

export interface UseRecentTransactionsResult {
  /** The most recently-dated saved transactions; empty while offline. */
  recent: Transaction[];
  /** Re-pull the list (after a delete). */
  refetch: () => void;
}

/**
 * Loads the last `limit` saved transactions for the /quick "Recently added"
 * panel. Only fetches when `enabled` (i.e. online) — saved history lives on
 * the server and transaction GETs are deliberately not cached, so offline we
 * clear the list and the panel shows an offline note instead. Re-pulls
 * whenever `refreshSignal` changes (the screen bumps it after each add).
 *
 * A monotonic generation token guards every response: only the latest
 * in-flight request may commit state, so an out-of-order response (a slow
 * earlier fetch resolving after a newer one, or after a re-run/disable) can't
 * clobber fresher state. A response arriving after unmount is a harmless no-op
 * under React 19.
 */
export function useRecentTransactions(
  limit: number,
  enabled: boolean,
  refreshSignal: number,
): UseRecentTransactionsResult {
  const [recent, setRecent] = useState<Transaction[]>([]);
  const genRef = useRef(0);

  const refetch = useCallback(() => {
    const gen = ++genRef.current;
    void api
      .get<PaginatedResponse<Transaction>>(
        `transactions?page=1&per_page=${limit}&sort_by=date&sort_dir=desc`,
      )
      .then((data) => {
        if (gen === genRef.current) setRecent(data.transactions);
      })
      .catch(() => {
        /* Offline/transient — keep quiet; the panel's offline note covers it. */
      });
  }, [limit]);

  useEffect(() => {
    if (!enabled) {
      genRef.current++; // invalidate any in-flight fetch; nothing commits
      setRecent([]);
      return;
    }
    // A re-run (refreshSignal/enabled change) calls refetch(), which bumps the
    // generation — so the previous in-flight response is discarded without a
    // cleanup that reads a ref (which the hooks linter flags).
    refetch();
  }, [enabled, refreshSignal, refetch]);

  return { recent, refetch };
}
