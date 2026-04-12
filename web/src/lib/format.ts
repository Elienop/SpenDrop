// Formatting helpers for amounts, currencies, and percentages.
//
// `formatCurrency` is the single entry point for turning a number into
// a currency string. Callers that know the household's base currency
// should pass it in — typically via `useBaseCurrency()` — so reports
// match the user's configured currency instead of hard-coded USD.

/**
 * Fallback currency code used when the household base currency has not
 * been loaded yet or the API is unavailable. Matches
 * `DefaultBaseCurrency` on the backend.
 */
export const DEFAULT_CURRENCY = 'USD';

/** Locale used for all `Intl.NumberFormat` / `toLocaleString` calls. */
export const DEFAULT_LOCALE = 'en-US';

/**
 * Format a number as a localized currency string (e.g. "$1,234.56").
 * Currency defaults to USD so components that haven't been updated to
 * read the household base currency still render something reasonable.
 */
export function formatCurrency(
  amount: number,
  currency: string = DEFAULT_CURRENCY,
  locale: string = DEFAULT_LOCALE,
): string {
  return amount.toLocaleString(locale, {
    style: 'currency',
    currency,
  });
}

/**
 * Format a number as a plain fixed-precision amount ("1,234.56") with
 * no currency symbol. Useful when the symbol is rendered separately
 * (e.g. in a table column header).
 */
export function formatAmount(
  amount: number,
  locale: string = DEFAULT_LOCALE,
): string {
  return amount.toLocaleString(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

/**
 * Format a number as a percentage string with one decimal place
 * ("73.2%"). Input is in the 0-100 range, not 0-1.
 */
export function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`;
}
