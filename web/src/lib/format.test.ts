import { describe, test, expect } from 'vitest';
import {
  amountSignNote,
  displayAmount,
  formatCurrency,
  formatRate,
  formatSignedAmount,
  formatSignedCurrency,
} from './format';
import { TYPE_EXPENSE, TYPE_INCOME } from './transaction-types';

/**
 * The one-sign rule, at its source.
 *
 * Everything else in the app renders money by calling `displayAmount` and then
 * one of the signed formatters, so this file is where the four quadrants of
 * (type × sign) are enumerated once. The surface tests assert that each screen
 * ROUTES through the rule; these assert what the rule IS.
 */
describe('displayAmount', () => {
  // The four quadrants, spelled out rather than derived, because "value × type"
  // is exactly the arithmetic a mutant gets subtly wrong (`Math.abs`, a flipped
  // ternary, a sign applied twice).
  test.each([
    ['a normal expense displays negative', 50, TYPE_EXPENSE, -50],
    ['a refund — a negative expense — displays POSITIVE', -20, TYPE_EXPENSE, 20],
    ['normal income displays positive', 100, TYPE_INCOME, 100],
    ['an income reversal displays NEGATIVE', -100, TYPE_INCOME, -100],
  ] as const)('%s', (_name, amount, type, expected) => {
    expect(displayAmount(amount, type)).toBe(expected);
  });

  test('an expense of zero is +0, never -0', () => {
    // Not pedantry: `formatCurrency(-0)` is "-$0.00" — Intl signs the negative
    // zero — and a zero amount reaches the QuickAdd preview before anything is
    // validated. `Object.is` because `-0 === 0` is true and would let the
    // mutant through.
    expect(Object.is(displayAmount(0, TYPE_EXPENSE), 0)).toBe(true);
    expect(formatCurrency(displayAmount(0, TYPE_EXPENSE))).toBe('$0.00');
    // The control: without the guard this is what the value would format as.
    expect(formatCurrency(-0)).toBe('-$0.00');
  });

  test('applying it twice does not cancel the sign back out', () => {
    // The shape of the defect it replaces — a hand-composed sign on top of a
    // formatter that emits its own. Two applications must not round-trip to
    // the stored value, or a caller could double up without noticing.
    expect(displayAmount(displayAmount(50, TYPE_EXPENSE), TYPE_EXPENSE)).toBe(50);
    expect(displayAmount(50, TYPE_EXPENSE)).not.toBe(50);
  });
});

describe('amountSignNote', () => {
  test.each([
    ['a positive expense needs no explanation', 50, TYPE_EXPENSE, null],
    ['a positive income needs no explanation', 100, TYPE_INCOME, null],
    ['a negative expense is a refund', -20, TYPE_EXPENSE, 'refund'],
    ['a negative income is a reversal', -100, TYPE_INCOME, 'reversal'],
  ] as const)('%s', (_name, amount, type, expected) => {
    expect(amountSignNote(amount, type)).toBe(expected);
  });

  test('zero is not an anomaly', () => {
    // Zero is illegal server-side, so if one ever arrives it is a bug to
    // surface elsewhere — not a row to label "Refund".
    expect(amountSignNote(0, TYPE_EXPENSE)).toBeNull();
    expect(amountSignNote(0, TYPE_INCOME)).toBeNull();
  });
});

describe('formatRate', () => {
  test('groups a large rate without padding it to the cent', () => {
    // The number the import offers as "today's rate" and then records as
    // the row's booked_rate. "89,000.00" reads as money it is not.
    expect(formatRate(89000)).toBe('89,000');
    expect(formatRate(0.92)).toBe('0.92');
  });

  test('a rate below half a cent survives, where a money formatter would render zero', () => {
    // THE reason this helper exists rather than a call to formatAmount:
    // `rate_to_base` is foreign units per base unit, so a currency
    // stronger than the base has a rate under 0.01 — and two decimals
    // would tell the user the rate is 0.
    expect(formatRate(0.000011)).toBe('0.000011');
    expect(formatRate(0.000011)).not.toBe('0.00');
  });
});

describe('the signed formatters', () => {
  test('a positive value carries an explicit plus, a negative its minus', () => {
    expect(formatSignedCurrency(2500, 'USD')).toBe('+$2,500.00');
    expect(formatSignedCurrency(-50, 'USD')).toBe('-$50.00');
  });

  test('zero carries no sign at all, from either direction', () => {
    expect(formatSignedCurrency(0, 'USD')).toBe('$0.00');
    // `-0` reaches this only if a caller skipped `displayAmount`; the
    // formatter is the second line of defence and must not print "-$0.00".
    expect(formatSignedCurrency(-0, 'USD')).toBe('$0.00');
  });

  test('exactly ONE sign character, whatever the input', () => {
    // The regression this whole rule exists for: "--$12.34" and "+-$5.00" were
    // both reachable by composing a type-derived sign onto a signed formatter.
    for (const value of [-9999.99, -0.01, 0, 0.01, 9999.99]) {
      const rendered = formatSignedCurrency(value, 'USD');
      expect(rendered.match(/[+-]/g)?.length ?? 0).toBeLessThanOrEqual(1);
      expect(rendered).not.toMatch(/[+-]{2}/);
    }
  });

  test('the symbol-free form signs the same way, for the original-currency line', () => {
    expect(formatSignedAmount(150000)).toBe('+150,000.00');
    expect(formatSignedAmount(-150000)).toBe('-150,000.00');
    expect(formatSignedAmount(0)).toBe('0.00');
  });

  test('a refund pair renders both lines in the same direction', () => {
    // A row entered as 2,250,000 LBP and refunded: the base figure and the
    // original figure must agree. They read from the same helper, so the pin
    // is that neither one is left raw.
    const amount = -25.5;
    const original = -2250000;
    expect(formatSignedCurrency(displayAmount(amount, TYPE_EXPENSE), 'USD')).toBe(
      '+$25.50',
    );
    expect(formatSignedAmount(displayAmount(original, TYPE_EXPENSE))).toBe(
      '+2,250,000.00',
    );
  });
});
