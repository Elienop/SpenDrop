import { useEffect, useState } from 'react';
import { api } from '../api/client';
import type { PaginatedResponse, Transaction } from '../api/types';

const FETCH_LIMIT = 200;

/**
 * Pulls the user's recent transactions (sorted desc by date, server-side
 * filtered for tombstones) and ranks distinct descriptions by frequency,
 * tie-breaking on recency. Used by `QuickAddSuggestions` to surface a
 * lightweight chip strip of past phrases in the freeform quick-add input.
 *
 * Returns `[]` until the fetch resolves, and `[]` on any error — the strip
 * is a non-critical UI affordance, so a failed fetch silently hides it
 * rather than surfacing an error banner above the capture field.
 */
export function useDescriptionHistory(): string[] {
  const [items, setItems] = useState<string[]>([]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const res = await api.get<PaginatedResponse<Transaction>>(
          `transactions?per_page=${FETCH_LIMIT}&sort_by=date&sort_dir=desc`,
        );
        if (cancelled) return;
        // Insertion order = recency (backend returns date desc).
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
        result.sort(
          (a, b) => b.count - a.count || a.firstIndex - b.firstIndex,
        );
        setItems(result.map((r) => r.original));
      } catch {
        // Non-critical UI — silent on error; the strip simply won't appear.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return items;
}
