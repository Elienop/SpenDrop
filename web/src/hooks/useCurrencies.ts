import { useEffect, useState } from 'react';
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

interface CacheEntry {
  list: Currency[];
  baseCode: string;
  error: string | null;
}

// Module-level promise cache — one fetch per session, shared across hook
// consumers. Mirrors `useBaseCurrency.ts`. A separate cache variable
// intentionally; `useBaseCurrency` stays independent so removing this
// hook later is straightforward.
let cachePromise: Promise<CacheEntry> | null = null;

function fetchCurrencies(): Promise<CacheEntry> {
  if (!cachePromise) {
    cachePromise = api
      .get<Currency[]>('currencies')
      .then((list) => {
        const base = list.find((c) => c.is_base)?.code ?? DEFAULT_CURRENCY;
        return { list, baseCode: base, error: null };
      })
      .catch((err: unknown) => {
        const msg = err instanceof Error ? err.message : 'failed to load currencies';
        return { list: [], baseCode: DEFAULT_CURRENCY, error: msg };
      });
  }
  return cachePromise;
}

/** Test-only. Resets the module-level cache between unit tests. */
export function __resetCurrenciesCacheForTests(): void {
  cachePromise = null;
}

export function useCurrencies(): UseCurrenciesResult {
  const [state, setState] = useState<{
    list: Currency[];
    baseCode: string;
    error: string | null;
    loading: boolean;
  }>({ list: [], baseCode: DEFAULT_CURRENCY, error: null, loading: true });

  useEffect(() => {
    let cancelled = false;
    fetchCurrencies().then((entry) => {
      if (cancelled) return;
      setState({ ...entry, loading: false });
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const rateFor = (code: string): number | null => {
    const c = state.list.find((x) => x.code === code);
    if (!c) return null;
    if (c.rate_to_base == null || c.rate_to_base <= 0) return null;
    return c.rate_to_base;
  };

  return {
    list: state.list,
    baseCode: state.baseCode,
    rateFor,
    loading: state.loading,
    error: state.error,
  };
}
