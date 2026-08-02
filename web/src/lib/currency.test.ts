import { describe, it, expect, vi } from 'vitest';
import {
  toCreatePayload,
  toEditDefaults,
  foreignMoneyUnchanged,
  dollarsToCents,
  PREVIEW_DECIMALS,
  type StoredMoney,
} from './currency';
import type { Transaction } from '@/api/types';

function makeTx(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: 1,
    user_id: 1,
    date: '2026-04-17',
    amount: 100,
    original_amount: null,
    original_currency: null,
    description: 'x',
    category_id: 1,
    category_name: 'c',
    category_type: 'expense',
    tags: null,
    notes: null,
    created_at: '2026-04-17T00:00:00Z',
    updated_at: '2026-04-17T00:00:00Z',
    ...overrides,
  };
}

const rateFor =
  (rates: Record<string, number | null>) =>
  (code: string): number | null =>
    rates[code] ?? null;

describe('PREVIEW_DECIMALS', () => {
  it('is 2', () => {
    expect(PREVIEW_DECIMALS).toBe(2);
  });
});

describe('toCreatePayload', () => {
  const base = 'USD';
  const rates = rateFor({ USD: 1, EUR: 0.9, LBP: 90000, JPY: 150 });

  it('_SameAsBaseCollapsesField: when currency === baseCode, strips currency and emits only amount', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 42.5,
        description: 'd',
        category_id: 1,
        tags: 't',
        currency: 'USD',
      },
      base,
      rates,
    );
    expect(out).toEqual({
      date: '2026-04-17',
      amount: 42.5,
      description: 'd',
      category_id: 1,
      tags: 't',
    });
    // The absent-key invariant: ensure original_* are not present as undefined either.
    expect('currency' in out).toBe(false);
    expect('original_amount' in out).toBe(false);
    expect('original_currency' in out).toBe(false);
  });

  it('_BothOrNeither: when currency !== baseCode, emits both original_amount AND original_currency', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 150000,
        description: 'd',
        category_id: 1,
        tags: 't',
        currency: 'LBP',
      },
      base,
      rates,
    );
    expect(out).toMatchObject({
      amount: expect.any(Number),
      original_amount: 150000,
      original_currency: 'LBP',
    });
    expect('currency' in out).toBe(false);
  });

  it('divides original amount by rate and rounds to 2 decimals', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 150000,
        description: 'd',
        category_id: 1,
        tags: '',
        currency: 'LBP',
      },
      base,
      rates,
    );
    // 150000 / 90000 = 1.666..., rounded to 1.67
    expect(out).toMatchObject({ amount: 1.67 });
  });

  it('_RateOneIsExplicit: non-base currency with rate === 1 still emits original_*', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 50,
        description: 'd',
        category_id: 1,
        tags: '',
        currency: 'EUR',
      },
      'USD',
      rateFor({ USD: 1, EUR: 1 }),
    );
    expect(out).toMatchObject({
      amount: 50,
      original_amount: 50,
      original_currency: 'EUR',
    });
  });

  it('_NoRateThrows: throws when rateFor returns null for a non-base currency', () => {
    expect(() =>
      toCreatePayload(
        {
          date: '2026-04-17',
          amount: 10,
          description: 'd',
          category_id: 1,
          tags: '',
          currency: 'XYZ',
        },
        base,
        rates,
      ),
    ).toThrow(/no rate/i);
  });

  it('_ZeroRateThrows: throws when rateFor returns zero for a non-base currency', () => {
    expect(() =>
      toCreatePayload(
        {
          date: '2026-04-17',
          amount: 10,
          description: 'd',
          category_id: 1,
          tags: '',
          currency: 'ZRO',
        },
        base,
        rateFor({ USD: 1, ZRO: 0 }),
      ),
    ).toThrow(/no rate/i);
  });

  it('preserves extra fields on the values object (generic)', () => {
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: 10,
        description: 'd',
        category_id: 1,
        tags: 'a,b',
        currency: 'USD',
        notes: 'hello',
      },
      base,
      rates,
    );
    expect(out).toMatchObject({ notes: 'hello', tags: 'a,b' });
  });
});

describe('toEditDefaults', () => {
  it('returns original_amount + original_currency when both present', () => {
    const tx = makeTx({ original_amount: 150000, original_currency: 'LBP' });
    expect(toEditDefaults(tx, 'USD')).toEqual({ amount: 150000, currency: 'LBP' });
  });

  it('returns tx.amount + baseCode when original_* fields are null', () => {
    const tx = makeTx({
      amount: 25.5,
      original_amount: null,
      original_currency: null,
    });
    expect(toEditDefaults(tx, 'USD')).toEqual({ amount: 25.5, currency: 'USD' });
  });

  it('falls back to baseCode when only one of the original_* fields is present and warns', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    try {
      const txA = makeTx({
        id: 42,
        original_amount: 100,
        original_currency: null,
      });
      const txB = makeTx({
        id: 43,
        original_amount: null,
        original_currency: 'EUR',
      });
      expect(toEditDefaults(txA, 'USD')).toEqual({ amount: txA.amount, currency: 'USD' });
      expect(toEditDefaults(txB, 'USD')).toEqual({ amount: txB.amount, currency: 'USD' });
      expect(warn).toHaveBeenCalledTimes(2);
      expect(warn).toHaveBeenNthCalledWith(
        1,
        'toEditDefaults: partial original_* fields on transaction',
        42,
      );
      expect(warn).toHaveBeenNthCalledWith(
        2,
        'toEditDefaults: partial original_* fields on transaction',
        43,
      );
    } finally {
      warn.mockRestore();
    }
  });

  it('does not warn when both original_* fields are null', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    try {
      toEditDefaults(
        makeTx({ original_amount: null, original_currency: null }),
        'USD',
      );
      expect(warn).not.toHaveBeenCalled();
    } finally {
      warn.mockRestore();
    }
  });
});

describe('dollarsToCents', () => {
  it('_RoundsAtTheWireEdge: matches math.Round(d * 100) on the binary-float cases', () => {
    // 16.85 * 100 is 1684.9999999999998 in IEEE 754. Truncating instead of
    // rounding would book this row a cent light, and every comparison against
    // a stored `original_amount_cents` would then be off by one.
    expect(dollarsToCents(16.85)).toBe(1685);
    expect(dollarsToCents(1500000)).toBe(150000000);
    expect(dollarsToCents(0.005)).toBe(1);
    expect(dollarsToCents(0)).toBe(0);
  });
});

describe('foreignMoneyUnchanged', () => {
  // A 1,500,000 LBP row booked at 89,000/USD. Today's rate is 90,000, so a
  // live conversion would say $16.67 — this row still holds, and will keep
  // holding, $16.85.
  const storedLbp: StoredMoney = {
    amount: 16.85,
    original_amount: 1500000,
    original_currency: 'LBP',
  };

  it('_RestatedForeignMoneyIsUnchanged: same code, same amount', () => {
    expect(
      foreignMoneyUnchanged(storedLbp, { amount: 1500000, currency: 'LBP' }, 'USD'),
    ).toBe(true);
  });

  it('_CorrectedAmountIsAChange: the user moved the money, so it re-prices', () => {
    expect(
      foreignMoneyUnchanged(storedLbp, { amount: 1600000, currency: 'LBP' }, 'USD'),
    ).toBe(false);
  });

  it('_SwitchedCurrencyIsAChange: same number, different currency', () => {
    expect(
      foreignMoneyUnchanged(storedLbp, { amount: 1500000, currency: 'EUR' }, 'USD'),
    ).toBe(false);
  });

  it('_SwitchToBaseIsAChange: a base-currency save carries no original_* to freeze against', () => {
    // The base-changed / corrupted shape: the row's own original_currency IS
    // the household base. `toCreatePayload` collapses this edit to a bare
    // `{ amount }`, so the server sees no original_* on the request and never
    // freezes — the code and cents comparisons below would both pass, which
    // is exactly why the baseCode guard has to come first.
    const storedInBase: StoredMoney = {
      amount: 25.5,
      original_amount: 25.5,
      original_currency: 'USD',
    };
    expect(
      foreignMoneyUnchanged(storedInBase, { amount: 25.5, currency: 'USD' }, 'USD'),
    ).toBe(false);
  });

  it('_BaseCurrencyRowIsNeverFrozen: a row with no original_* has nothing to carry forward', () => {
    const storedBaseRow: StoredMoney = {
      amount: 25.5,
      original_amount: null,
      original_currency: null,
    };
    expect(
      foreignMoneyUnchanged(storedBaseRow, { amount: 1500000, currency: 'LBP' }, 'USD'),
    ).toBe(false);
  });

  it('_PartialOriginalFieldsAreNotFrozen: the half-null corruption shape re-prices', () => {
    // Not mutation-killable on its own — with either guard removed, the code
    // comparison against `null` already fails. Asserted for the behaviour,
    // not recorded as coverage. Same honesty as the Valid checks in the Go
    // predicate this mirrors.
    expect(
      foreignMoneyUnchanged(
        { amount: 16.85, original_amount: 1500000, original_currency: null },
        { amount: 1500000, currency: 'LBP' },
        'USD',
      ),
    ).toBe(false);
    expect(
      foreignMoneyUnchanged(
        { amount: 16.85, original_amount: null, original_currency: 'LBP' },
        { amount: 1500000, currency: 'LBP' },
        'USD',
      ),
    ).toBe(false);
  });

  it('_SubCentDifferenceIsTheSameMoney: the server compares cents, not floats', () => {
    // Both round to 150,000,000 cents, so the server sees one and the same
    // foreign amount. A float `===` here would claim a re-price the server
    // does not perform.
    expect(
      foreignMoneyUnchanged(storedLbp, { amount: 1500000.004, currency: 'LBP' }, 'USD'),
    ).toBe(true);
  });

  it('_CentDifferenceIsAChange: one cent is enough to move the money', () => {
    expect(
      foreignMoneyUnchanged(storedLbp, { amount: 1500000.01, currency: 'LBP' }, 'USD'),
    ).toBe(false);
  });

  it('_CurrencyCodeCaseIsSignificant: mirrors the exact comparison the server makes', () => {
    // The import path stores whatever the spreadsheet's currency column says,
    // so a row can be recorded as "lbp". The server compares the code
    // byte-for-byte and re-prices such a row on every save; case-folding here
    // would promise a freeze that never happens.
    const lowercased: StoredMoney = { ...storedLbp, original_currency: 'lbp' };
    expect(
      foreignMoneyUnchanged(lowercased, { amount: 1500000, currency: 'LBP' }, 'USD'),
    ).toBe(false);
  });
});
