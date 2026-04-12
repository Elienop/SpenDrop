import { useEffect, useState } from 'react';
import { api } from '@/api/client';
import type { Currency } from '@/api/types';
import { DEFAULT_CURRENCY } from '@/lib/format';

// Module-level promise cache. The currencies list rarely changes during
// a session, so we fetch it at most once and share the result across
// every call to `useBaseCurrency`. If the user changes their base
// currency in Settings, they'll see the new value after a page reload
// — which matches the rest of the household-settings UX.
let currencyPromise: Promise<string> | null = null;

function fetchBaseCurrency(): Promise<string> {
  if (!currencyPromise) {
    currencyPromise = api
      .get<Currency[]>('currencies')
      .then(
        (list) =>
          list.find((c) => c.is_base)?.code ?? DEFAULT_CURRENCY,
      )
      .catch(() => DEFAULT_CURRENCY);
  }
  return currencyPromise;
}

/**
 * Returns the household's configured base currency code
 * (e.g. `"EUR"`). Falls back to `DEFAULT_CURRENCY` while the initial
 * request is in flight or if the API is unreachable.
 */
export function useBaseCurrency(): string {
  const [code, setCode] = useState<string>(DEFAULT_CURRENCY);

  useEffect(() => {
    let cancelled = false;
    fetchBaseCurrency().then((value) => {
      if (!cancelled) {
        setCode(value);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return code;
}
