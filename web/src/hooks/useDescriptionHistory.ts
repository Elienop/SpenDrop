import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';
import type { PaginatedResponse, Transaction } from '../api/types';

const FETCH_LIMIT = 200;

/**
 * Ranks distinct recent transaction descriptions by frequency, tie-breaking on
 * recency (backend returns date desc, so insertion order = recency). Backed by
 * TanStack Query under `['transactions','description-history']` — the first key
 * segment is `'transactions'` so the live-update subscriber refreshes it after
 * any transaction mutation. The chip strip is a non-critical affordance: on
 * error the query falls back to `[]` rather than surfacing a banner.
 *
 * Returns `string[]` (the canonical first-seen casing of each distinct
 * description, most-frequent first).
 */
export function useDescriptionHistory(): string[] {
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

  return query.data ?? [];
}
