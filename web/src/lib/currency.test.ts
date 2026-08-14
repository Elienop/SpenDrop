import { describe, it, expect, vi } from 'vitest';
import {
  applyAmountSign,
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
    created_by: 'Elie',
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

  it('_SignedForeignAmountKeepsBothHalvesNegative: a refund typed in LBP', () => {
    // The sign is applied by the caller BEFORE conversion, so the division
    // carries it (a rate is always > 0) and the money pair agrees — which is
    // the state the server requires and the import path's `sign_mismatch`
    // skip names.
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: applyAmountSign(150000, true),
        description: 'd',
        category_id: 1,
        tags: '',
        currency: 'LBP',
      },
      base,
      rates,
    );
    expect(out).toMatchObject({
      amount: -1.67,
      original_amount: -150000,
      original_currency: 'LBP',
    });
  });

  it('_NegativeHalfCentRoundsAwayFromZero: the converted figure matches the server s', () => {
    // -450 LBP at 90,000 is exactly -0.005 base, and `-0.005 * 100` is exactly
    // -0.5 — the tie where `Math.round` and Go's `math.Round` disagree. The
    // old helper produced `-0`, which JSON serialises as `0`, i.e. the one
    // amount the server now refuses outright. This assertion is the reason the
    // preview rounding had to change alongside the cents rounding.
    const out = toCreatePayload(
      {
        date: '2026-04-17',
        amount: -450,
        description: 'd',
        category_id: 1,
        tags: '',
        currency: 'LBP',
      },
      base,
      rates,
    );
    expect(out).toMatchObject({ amount: -0.01, original_amount: -450 });
    expect(JSON.parse(JSON.stringify(out)).amount).toBe(-0.01);
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

  it('_NegativeHalvesRoundAwayFromZero: matches Go on the side JS gets wrong', () => {
    // The whole reason this function stopped being `Math.round`. Both figures
    // below are exact halves in IEEE 754 (`-0.005 * 100` really is `-0.5`), and
    // that is where the two languages part company: JS rounds a half toward
    // +Infinity, Go's `math.Round` rounds it away from zero. These are the
    // cents the SERVER will hold for a refund of that size, so the frontend
    // has to agree or `foreignMoneyUnchanged` claims a freeze the server does
    // not perform.
    expect(dollarsToCents(-0.005)).toBe(-1); // Math.round gives -0
    expect(dollarsToCents(-0.015)).toBe(-2); // Math.round gives -1
    expect(dollarsToCents(-0.105)).toBe(-11); // Math.round gives -10
  });

  it('_MirrorsPositiveMagnitudes: sign is the only difference', () => {
    // A refund of $16.85 is worth the same cents as a spend of $16.85. If the
    // sign-symmetric rewrite had reached for `Math.trunc` or `Math.floor`
    // instead, this is the pair that would stop matching.
    expect(dollarsToCents(-16.85)).toBe(-1685);
    expect(dollarsToCents(-1500000)).toBe(-150000000);
  });

  it('_NegativeZeroIsZero: a value that rounds to nothing is plain 0', () => {
    // `Math.sign(-0.001) * Math.round(0.1)` is `-0`, which `toBe` (Object.is)
    // separates from `0` — and which would then travel into cents comparisons
    // and JSON. Nothing downstream should have to know that.
    expect(dollarsToCents(-0.001)).toBe(0);
    expect(Object.is(dollarsToCents(-0.001), -0)).toBe(false);
  });
});

describe('applyAmountSign', () => {
  it('_ToggleOffIsUntouched: the ordinary entry is not rewritten', () => {
    expect(applyAmountSign(42.5, false)).toBe(42.5);
  });

  it('_ToggleOnNegates: the toggle is the only thing that signs an amount', () => {
    expect(applyAmountSign(42.5, true)).toBe(-42.5);
  });

  it('_ZeroStaysPositiveZero: never emits -0', () => {
    // Zero is refused upstream by every entry gate, so this is about what
    // leaks if one is ever relaxed: `JSON.stringify(-0)` is "0", but `-0`
    // compares unequal under `Object.is` and would make a snapshot or a cache
    // key differ from an identical entry typed the other way round.
    expect(Object.is(applyAmountSign(0, true), 0)).toBe(true);
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

  // A refund of the same money: 1,500,000 LBP came back, so the row stores
  // -16.85 with a negative original amount. The pair moves together or not at
  // all — a stored refund whose original_amount stayed positive is the
  // `sign_mismatch` shape the import path skips.
  const storedLbpRefund: StoredMoney = {
    amount: -16.85,
    original_amount: -1500000,
    original_currency: 'LBP',
  };

  it('_RestatedRefundIsUnchanged: a refund reopened and saved back keeps its stored value', () => {
    expect(
      foreignMoneyUnchanged(
        storedLbpRefund,
        { amount: -1500000, currency: 'LBP' },
        'USD',
      ),
    ).toBe(true);
  });

  it('_FlippedSignIsAChange: turning the refund off re-prices at today s rate', () => {
    // The magnitudes are identical and only the sign differs, which is exactly
    // the case a magnitude-only comparison would call "unchanged". It is not:
    // the request carries `original_amount: +1500000`, the row holds
    // -1,500,000 LBP in cents, the server's predicate fails, and it re-derives
    // the base value from today's rate. A preview promising the stored $16.85
    // there would be contradicted by the save.
    expect(
      foreignMoneyUnchanged(
        storedLbpRefund,
        { amount: 1500000, currency: 'LBP' },
        'USD',
      ),
    ).toBe(false);
  });

  it('_TurningAnOrdinaryRowIntoARefundIsAChange: the mirror of the case above', () => {
    expect(
      foreignMoneyUnchanged(storedLbp, { amount: -1500000, currency: 'LBP' }, 'USD'),
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
