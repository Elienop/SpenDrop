import { useCurrencies } from './useCurrencies';

/**
 * Returns the household's configured base currency code (e.g. `"EUR"`).
 * Derives from `useCurrencies` so both share the one `['currencies']` cache
 * entry (no second fetch) and both react to the live-update subscriber's
 * `['currencies']` invalidation. Falls back to `DEFAULT_CURRENCY` while the
 * request is in flight or if the API is unreachable — `useCurrencies.baseCode`
 * already applies that fallback.
 */
export function useBaseCurrency(): string {
  return useCurrencies().baseCode;
}
