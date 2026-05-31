import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';
import type { TransactionSuggestions } from '../api/types';

const EMPTY: TransactionSuggestions = { descriptions: [], tags: [] };

/**
 * Description/tag autocomplete suggestions for the transactions surface.
 * Backed by TanStack Query under `['transactions','suggestions']` — the first
 * key segment is `'transactions'` so the live-update subscriber's
 * `invalidateQueries({ queryKey: ['transactions'] })` refetches it after any
 * transaction mutation. `refreshKey` is folded into the key so a caller bump
 * still forces a re-pull (preserves the previous `useSuggestions(refreshKey)`
 * contract without changing call sites). Non-critical: errors fall back to the
 * empty shape rather than surfacing.
 */
export function useSuggestions(refreshKey = 0): TransactionSuggestions {
  const query = useQuery({
    queryKey: ['transactions', 'suggestions', refreshKey],
    queryFn: () => api.get<TransactionSuggestions>('transactions/suggestions'),
  });

  return query.data ?? EMPTY;
}
