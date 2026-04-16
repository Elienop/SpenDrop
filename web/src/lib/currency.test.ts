import { describe, it, expect } from 'vitest';
import { toCreatePayload, toEditDefaults, PREVIEW_DECIMALS } from './currency';
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
    expect((out as { amount: number }).amount).toBe(1.67);
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

  it('_NoRateThrows: throws when rateFor returns zero for a non-base currency', () => {
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

  it('falls back to baseCode when only one of the original_* fields is present', () => {
    const txA = makeTx({ original_amount: 100, original_currency: null });
    const txB = makeTx({ original_amount: null, original_currency: 'EUR' });
    expect(toEditDefaults(txA, 'USD')).toEqual({ amount: txA.amount, currency: 'USD' });
    expect(toEditDefaults(txB, 'USD')).toEqual({ amount: txB.amount, currency: 'USD' });
  });
});
