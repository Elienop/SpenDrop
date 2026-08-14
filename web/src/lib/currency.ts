import type { Transaction } from '@/api/types';

/** Number of decimal places shown in the `≈` preview, and used for rounding
 *  the frontend-computed base-currency amount before submit. Matches the
 *  backend's `math.Round(converted*100)/100` behavior so the pre-save
 *  preview and post-save row agree to the cent. 2-decimal assumption —
 *  see spec §Edge Case 7 for the known limitation around JPY/BHD. */
export const PREVIEW_DECIMALS = 2;

/**
 * `Math.round` with Go's tie-breaking rule, which is a DIFFERENT rule.
 *
 * JS sends a half toward +Infinity (`Math.round(-0.5)` is `-0`); Go's
 * `math.Round` sends it away from zero (`-1`). The two agree on every positive
 * number and disagree on every negative half, so while amounts were required
 * to be positive the difference was unreachable — the comment that used to sit
 * on `dollarsToCents` said exactly that, and deferred this change to the day
 * signed amounts landed. They have: a refund is a negative amount, so the
 * frontend now computes cents for values on the losing side of that rule.
 *
 * It has to be Go's rule and not merely a consistent one, because the two
 * results are compared against each other. `foreignMagnitudeUnchanged` mirrors a
 * server predicate over stored cents, and `toCreatePayload` computes the base
 * amount the server would compute for the same division — a one-cent
 * disagreement re-prices money the user did not touch, or promises a preview
 * figure the save contradicts.
 */
function roundHalfAwayFromZero(n: number): number {
  const rounded = Math.sign(n) * Math.round(Math.abs(n));
  // `Math.sign(-0.4) * 0` is `-0`, which `Object.is` — and therefore
  // `expect(...).toBe(0)` and `Map`/`Set` keying — treats as a value distinct
  // from `0`. Nothing downstream should have to know that.
  return rounded === 0 ? 0 : rounded;
}

function roundToPreview(n: number): number {
  const factor = 10 ** PREVIEW_DECIMALS;
  return roundHalfAwayFromZero(n * factor) / factor;
}

/**
 * Rounds a dollar figure to integer cents exactly the way the wire edge does
 * (`dollarsToCents` in `internal/api/cents.go` stores `math.Round(d * 100)`).
 *
 * Two dollar figures that land on the same cents are the same money as far as
 * the database is concerned, so this is the granularity at which the frontend
 * has to compare money against a stored row — the wire carries dollars, the
 * server compares cents.
 *
 * Negative halves round away from zero, matching Go — see
 * `roundHalfAwayFromZero` for why the plain `Math.round` this used to be is
 * wrong now that a refund can be worth `-0.005`.
 */
export function dollarsToCents(dollars: number): number {
  return roundHalfAwayFromZero(dollars * 100);
}

/**
 * Applies an entry surface's Refund toggle to the magnitude it typed.
 *
 * THE TOGGLE IS THE ONLY SIGN CHANNEL. Every amount input in the app holds a
 * positive magnitude and keeps rejecting a typed minus — that rejection is the
 * typo guard, not an oversight — so this is the one place a transaction amount
 * acquires its sign, and it runs BEFORE `toCreatePayload` so a foreign refund
 * divides with its sign intact (`-150000 LBP / 90000` is a negative base
 * amount; signing afterwards would leave `original_amount` positive and the
 * two halves of the money pair disagreeing, which the import path skips as
 * `sign_mismatch`).
 */
export function applyAmountSign(magnitude: number, negative: boolean): number {
  if (!negative || magnitude === 0) return magnitude;
  return -magnitude;
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
 * Sign is the caller's, applied before it gets here (`applyAmountSign`): the
 * division preserves it because a rate is always > 0, so both halves of the
 * money pair come out with the same sign — the agreement the server requires.
 *
 * Generic over T so `TransactionEntryRow` and the edit form can both
 * pass through their own field set (description, category_id, tags,
 * notes, ...) without losing types. The `currency` key is stripped
 * from the output in both branches.
 *
 * The union return type + `as` casts are load-bearing: TS cannot narrow
 * the discriminant through a rest-spread, so each runtime branch is
 * asserted to its matching union arm. The casts are safe because the
 * runtime shape exactly matches the asserted type on each branch.
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

/** The money an edit surface currently holds: the amount as typed, in
 *  whatever currency the picker is set to. */
export interface EditedMoney {
  amount: number;
  currency: string;
}

/**
 * Derives initial form values from a saved `Transaction` for the edit
 * surface. When the transaction carries both `original_amount` and
 * `original_currency`, round-trips through those; otherwise falls back
 * to the base-currency `amount` and the household's base code.
 *
 * When exactly one of `original_amount` / `original_currency` is set —
 * a data-corruption shape possible after a failed import or partial
 * migration — the fallback silently strips the straggler on next Save.
 * A `console.warn` surfaces the offending row id so future debugging
 * can trace which transaction tripped the partial-null branch.
 */
export function toEditDefaults(tx: Transaction, baseCode: string): EditedMoney {
  if (tx.original_amount != null && tx.original_currency != null) {
    return { amount: tx.original_amount, currency: tx.original_currency };
  }
  if ((tx.original_amount != null) !== (tx.original_currency != null)) {
    console.warn(
      'toEditDefaults: partial original_* fields on transaction',
      tx.id,
    );
  }
  return { amount: tx.amount, currency: baseCode };
}

/**
 * The money a saved transaction already carries — the base-currency value
 * stored on the row, plus the foreign amount and code it was recorded in
 * (both `null` on a base-currency row).
 */
export interface StoredMoney {
  /** Base-currency value stored on the row, in dollars. */
  amount: number;
  original_amount: number | null;
  original_currency: string | null;
}

/**
 * Mirror of `foreignMagnitudeUnchanged` in `internal/database/store.go`. Reports
 * whether saving this edit would merely restate the foreign money the row
 * already carries — same currency code, same original amount to the cent,
 * ignoring sign.
 *
 * The server carries the row's stored base MAGNITUDE forward in that case
 * instead of re-deriving it from today's rate, because saving an edit must
 * never move money the user did not touch. So anything that wants to show what
 * a save will store must not divide by the rate here — after the rate moves,
 * the live conversion is a number the save will not produce. Changing the
 * foreign amount, changing the currency, or switching to or from the base
 * currency all still re-price, because each of those IS the user changing the
 * money.
 *
 * Four details are deliberately copied from the Go predicate, and each one
 * would make the preview promise a number the save contradicts if it drifted:
 *
 *   - SIGN IS IGNORED. Flipping the Refund toggle is a classification change —
 *     the same money went the other way — so the server keeps the booked
 *     magnitude and re-applies the requested direction rather than re-pricing.
 *     A sign-sensitive comparison here would drop the preview back to today's
 *     rate for a save that is going to store the old figure. (The digits in an
 *     amount input are a magnitude and the toggle carries the direction, so the
 *     figure this feeds is signed only because the server compares a signed
 *     column; both sides then take the absolute value.)
 *
 *   - `currency === baseCode` returns false. `toCreatePayload` collapses a
 *     base-currency edit to a bare `{ amount }` with no `original_*`, and the
 *     server's freeze requires them on the request, so a base-currency save is
 *     never frozen however the row was stored.
 *   - The currency code is compared EXACTLY. No writer normalizes its case —
 *     the import path stores whatever the spreadsheet's currency column says,
 *     verbatim — so a row recorded as "lbp" can never match "LBP" and every
 *     save of it re-prices. Case-folding here alone would claim a freeze the
 *     server does not honour.
 *   - Amounts are compared as integer cents, because the server compares
 *     `original_amount_cents`. The wire carries dollars, so a typed
 *     1500000.004 and a stored 1500000 are one and the same row to it.
 */
export function foreignMagnitudeUnchanged(
  stored: StoredMoney,
  edited: EditedMoney,
  baseCode: string,
): boolean {
  if (edited.currency === baseCode) return false;
  if (stored.original_amount == null || stored.original_currency == null) {
    return false;
  }
  if (edited.currency !== stored.original_currency) return false;
  return (
    Math.abs(dollarsToCents(edited.amount)) ===
    Math.abs(dollarsToCents(stored.original_amount))
  );
}
