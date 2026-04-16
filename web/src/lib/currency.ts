import type { Transaction } from '@/api/types';

/** Number of decimal places shown in the `≈` preview, and used for rounding
 *  the frontend-computed base-currency amount before submit. Matches the
 *  backend's `math.Round(converted*100)/100` behavior so the pre-save
 *  preview and post-save row agree to the cent. 2-decimal assumption —
 *  see spec §Edge Case 7 for the known limitation around JPY/BHD. */
export const PREVIEW_DECIMALS = 2;

function roundToPreview(n: number): number {
  const factor = 10 ** PREVIEW_DECIMALS;
  return Math.round(n * factor) / factor;
}

/**
 * Transforms entry-form values into the wire payload for
 * `POST /api/transactions`. Collapses `currency === baseCode` to a bare
 * `{ amount }` (no `original_*`). Otherwise divides the typed amount by
 * the currency's rate-to-base, rounds to 2 decimals, and emits both
 * `original_amount` and `original_currency` alongside the computed
 * `amount`. Throws when the non-base currency has no rate configured —
 * callers gate the Save button on this.
 *
 * Generic over T so `TransactionEntryRow` and the edit form can both
 * pass through their own field set (description, category_id, tags,
 * notes, ...) without losing types. The `currency` key is stripped
 * from the output in both branches.
 */
export function toCreatePayload<
  T extends Record<string, unknown> & { amount: number; currency: string },
>(
  values: T,
  baseCode: string,
  rateFor: (code: string) => number | null,
):
  | (Omit<T, 'currency'> & { amount: number })
  | (Omit<T, 'currency'> & {
      amount: number;
      original_amount: number;
      original_currency: string;
    }) {
  const { currency, ...rest } = values;
  if (currency === baseCode) {
    return { ...rest, amount: values.amount } as Omit<T, 'currency'> & {
      amount: number;
    };
  }
  const rate = rateFor(currency);
  if (rate == null || rate <= 0) {
    throw new Error(`no rate configured for ${currency}`);
  }
  return {
    ...rest,
    amount: roundToPreview(values.amount / rate),
    original_amount: values.amount,
    original_currency: currency,
  } as Omit<T, 'currency'> & {
    amount: number;
    original_amount: number;
    original_currency: string;
  };
}

/**
 * Derives initial form values from a saved `Transaction` for the edit
 * surface. When the transaction carries both `original_amount` and
 * `original_currency`, round-trips through those; otherwise falls back
 * to the base-currency `amount` and the household's base code.
 */
export function toEditDefaults(
  tx: Transaction,
  baseCode: string,
): { amount: number; currency: string } {
  if (tx.original_amount != null && tx.original_currency != null) {
    return { amount: tx.original_amount, currency: tx.original_currency };
  }
  return { amount: tx.amount, currency: baseCode };
}
