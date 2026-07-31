import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { Currency } from '@/api/types';
import { DEFAULT_CURRENCY } from '@/lib/format';

export interface UseCurrenciesResult {
  /** Full list as returned by `GET /api/currencies`. Inactive currencies
   *  are included so the edit surface can render `(inactive)` suffixes
   *  for historical rows; the entry surface filters them out. */
  list: Currency[];
  /** Code of the `is_base` currency, or `DEFAULT_CURRENCY` ("USD") fallback. */
  baseCode: string;
  /** Returns `rate_to_base` for `code`, or `null` if the code is unknown
   *  or has a null / zero / negative rate. Callers gate Save on this
   *  to avoid a silent rate-of-1 fallback (Firefly III #11616). */
  rateFor: (code: string) => number | null;
  loading: boolean;
  error: string | null;
}

/**
 * Household currency list + base code + rate lookup. Backed by TanStack Query
 * under the `['currencies']` key — one shared cache entry across every
 * consumer (incl. `useBaseCurrency`, which derives from this same query), and
 * the live-update subscriber refreshes it after currency CRUD via
 * `invalidateQueries({ queryKey: ['currencies'] })`. (Replaces the previous
 * module-level promise cache and its test-only reset shim.)
 */
export function useCurrencies(): UseCurrenciesResult {
  const query = useQuery({
    queryKey: ['currencies'],
    queryFn: () => api.get<Currency[]>('currencies'),
    // A failed currencies fetch must not re-fire when a second consumer mounts
    // against the same cache (the previous module-promise cache memoized the
    // failure for the session). The focus/reconnect backstop and the SSE
    // `['currencies']` invalidation remain the recovery paths.
    retryOnMount: false,
    // Same reason as useCategories: the service worker caches
    // GET /api/currencies for exactly this case, and a query that pauses
    // itself offline never gives that cache the chance to answer. Without a
    // base currency the capture screen cannot build a payload at all.
    networkMode: 'offlineFirst',
  });

  const list = query.data ?? [];
  const baseCode = list.find((c) => c.is_base)?.code ?? DEFAULT_CURRENCY;

  const rateFor = (code: string): number | null => {
    const c = list.find((x) => x.code === code);
    if (!c) return null;
    if (c.rate_to_base == null || c.rate_to_base <= 0) return null;
    return c.rate_to_base;
  };

  return {
    list,
    baseCode,
    rateFor,
    loading: query.isLoading,
    error: query.error instanceof Error ? query.error.message : null,
  };
}
