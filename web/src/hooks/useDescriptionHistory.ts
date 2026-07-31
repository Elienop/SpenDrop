import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';
import type { PaginatedResponse, Transaction } from '../api/types';

const FETCH_LIMIT = 200;

export interface UseDescriptionHistoryResult {
  /**
   * Canonical first-seen casing of each distinct description, most-frequent
   * first. Empty while loading, on failure, and when there genuinely is no
   * history — which is exactly why the flags below exist.
   */
  descriptions: string[];
  /**
   * The fetch was attempted and did not succeed. Distinct from an empty
   * `descriptions`: "we could not load your history" and "you have no
   * history" are different sentences and the screen must be able to say the
   * right one.
   */
  failed: boolean;
  /**
   * The query is paused because the device is offline — it has not been
   * attempted, so there is nothing to retry until connectivity returns. Also
   * distinct from `failed`.
   */
  waitingForNetwork: boolean;
  /** Re-run the fetch (the affordance offered alongside `failed`). */
  retry: () => void;
}

/**
 * Ranks distinct recent transaction descriptions by frequency, tie-breaking on
 * recency (backend returns date desc, so insertion order = recency). Backed by
 * TanStack Query under `['transactions','description-history']` — the first key
 * segment is `'transactions'` so the live-update subscriber refreshes it after
 * any transaction mutation.
 *
 * The chip strip is a non-critical affordance, but it must never fail
 * SILENTLY: a strip that disappears when the fetch broke is indistinguishable
 * from one that has nothing to suggest, and the user retypes their whole
 * history believing the app has forgotten it.
 */
export function useDescriptionHistory(): UseDescriptionHistoryResult {
  const query = useQuery({
    queryKey: ['transactions', 'description-history'],
    queryFn: () =>
      api.get<PaginatedResponse<Transaction>>(
        `transactions?per_page=${FETCH_LIMIT}&sort_by=date&sort_dir=desc`,
      ),
    select: (res): string[] => {
      // Walk once: track first-seen casing as canonical, count occurrences,
      // remember first-seen index for the recency tiebreak.
      const seen = new Map<string, number>(); // key -> index in result[]
      const result: {
        key: string;
        original: string;
        count: number;
        firstIndex: number;
      }[] = [];
      res.transactions.forEach((t, idx) => {
        const original = t.description?.trim();
        if (!original) return;
        const key = original.toLowerCase();
        const existingIdx = seen.get(key);
        if (existingIdx !== undefined) {
          result[existingIdx].count += 1;
        } else {
          seen.set(key, result.length);
          result.push({ key, original, count: 1, firstIndex: idx });
        }
      });
      result.sort((a, b) => b.count - a.count || a.firstIndex - b.firstIndex);
      return result.map((r) => r.original);
    },
  });

  return {
    descriptions: query.data ?? [],
    failed: query.isError,
    // 'paused' is TanStack's offline state: the query never ran, so this is
    // "waiting", not "broken".
    waitingForNetwork: query.fetchStatus === 'paused',
    retry: () => {
      void query.refetch();
    },
  };
}
