// Formatting helpers for amounts, currencies, and percentages.
//
// `formatCurrency` is the single entry point for turning a number into
// a currency string. Callers that know the household's base currency
// should pass it in — typically via `useBaseCurrency()` — so reports
// match the user's configured currency instead of hard-coded USD.

import { TYPE_EXPENSE, type TransactionType } from './transaction-types';

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

/**
 * An exchange rate, grouped and unpadded: "89,000", "0.92", "0.000011".
 *
 * A RATE IS NOT MONEY, which is the whole reason this is not
 * `formatAmount`. Two decimals are wrong for it in both directions: they
 * pad "89,000" into "89,000.00", where the cents are noise on a figure
 * nobody quotes to the cent — and, far worse, they render any rate below
 * half a cent as "0.00", i.e. as a rate of zero. `rate_to_base` is
 * foreign units per base unit, so that is not a hypothetical: it is what
 * a currency stronger than the household's base looks like.
 *
 * Six fraction digits is the visible precision, chosen to cover those
 * small rates while stopping well short of printing float noise.
 *
 * IT BOUNDS THE DISPLAY ONLY. The number that travels to the server is
 * the one the caller holds, never this string — the import's "apply
 * today's rate" button shows `formatRate(rate)` and PATCHes the full
 * `rate`, so a currency configured to more than six decimals is recorded
 * at the precision it was configured with, not at the precision it was
 * shown at. Anything that needs the displayed and the sent value to be
 * the same number has to round the value itself, not read this back.
 */
export function formatRate(
  rate: number,
  locale: string = DEFAULT_LOCALE,
): string {
  return rate.toLocaleString(locale, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 6,
  });
}

/* ── Signed amounts (B10) ────────────────────────────────────────────────── */

/**
 * The value a row DISPLAYS, from the amount it stores and its category type.
 *
 * `amount_cents` is SIGNED: a negative amount on an expense row is a refund
 * (money back), a negative income is a reversal. The stored number carries the
 * direction WITHIN its type; the display sign carries the direction of money,
 * so the two have to be combined exactly once:
 *
 *   expense  50  →  -50   a normal expense, rendered as it always was
 *   expense -20  →   20   a refund: money came back
 *   income  100  →  100
 *   income -100  → -100   an income reversal
 *
 * EXACTLY ONCE is the whole point. Four surfaces used to prepend a
 * type-derived '-' or '+' to a formatter that emits its own minus, so the
 * first negative amount to reach any of them would have rendered "--$12.34".
 * Every money render that knows a row's type goes through here and then
 * through `formatSignedCurrency` / `formatSignedAmount`, which are the only
 * things allowed to put a sign character on screen.
 *
 * The zero case returns +0 rather than `-0`, which is not cosmetic: the plain
 * `formatCurrency` renders `-0` as "-$0.00" (Intl treats the negative zero as
 * negative), and a zero amount is reachable in the QuickAdd preview before
 * anything is validated. `formatSignedCurrency` handles `-0` on its own, but
 * callers that do arithmetic with this value should never see one.
 */
export function displayAmount(amount: number, type: TransactionType): number {
  if (amount === 0) return 0;
  return type === TYPE_EXPENSE ? -amount : amount;
}

/**
 * What a row's sign says about it, when the sign disagrees with its type.
 *
 * `null` for the ordinary cases — a positive expense and a positive income
 * need no explanation. A negative one does: a refund renders as a green
 * inflow ON AN EXPENSE ROW, which otherwise reads as income or as a typo'd
 * entry, and an income reversal renders exactly like an ordinary expense.
 * `AmountSignNote` turns this into the visible label.
 */
export type AmountSignKind = 'refund' | 'reversal';

export function amountSignNote(
  amount: number,
  type: TransactionType,
): AmountSignKind | null {
  if (amount >= 0) return null;
  return type === TYPE_EXPENSE ? 'refund' : 'reversal';
}

/**
 * A currency string that carries its own sign in BOTH directions
 * ("+$20.00", "-$50.00", "$0.00").
 *
 * `signDisplay: 'exceptZero'` rather than composing a '+' onto
 * `formatCurrency`: the plus then comes from the same Intl pass as the minus,
 * so there is one sign per string by construction and no call site can add a
 * second. It also renders `-0` as "$0.00" instead of "-$0.00".
 *
 * Feed it `displayAmount(...)`, never a raw stored amount.
 */
export function formatSignedCurrency(
  amount: number,
  currency: string = DEFAULT_CURRENCY,
  locale: string = DEFAULT_LOCALE,
): string {
  return amount.toLocaleString(locale, {
    style: 'currency',
    currency,
    signDisplay: 'exceptZero',
  });
}

/**
 * `formatAmount`'s symbol-free output with the same signing rule as
 * `formatSignedCurrency`.
 *
 * This is what the original-currency line of a converted row renders, and it
 * takes the sign for the same reason the primary line does: one row must not
 * read "+$1.67" over "-150,000.00 LBP". Both lines are the DISPLAY value of
 * the same money.
 */
export function formatSignedAmount(
  amount: number,
  locale: string = DEFAULT_LOCALE,
): string {
  return amount.toLocaleString(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
    signDisplay: 'exceptZero',
  });
}
