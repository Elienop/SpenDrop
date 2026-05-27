import { describe, it, expect } from 'vitest';
import { parseQuickEntry, type QuickParseOptions } from './quick-parse';
import type { Category, Currency } from '@/api/types';

function cat(id: number, name: string, is_active = true): Category {
  return {
    id,
    name,
    type: 'expense',
    icon: null,
    sort_order: id,
    is_active,
    created_at: '2026-01-01T00:00:00Z',
  };
}

function cur(code: string, symbol: string, is_base = false): Currency {
  return {
    code,
    name: code,
    symbol,
    rate_to_base: is_base ? 1 : 1.1,
    is_base,
    is_active: true,
    updated_at: '2026-01-01T00:00:00Z',
  };
}

const categories: Category[] = [
  cat(1, 'Food'),
  cat(2, 'Groceries'),
  cat(3, 'Transport'),
  cat(4, 'Food & Dining'),
  cat(5, 'Old', false),
];

const currencies: Currency[] = [cur('USD', '$', true), cur('EUR', '€'), cur('GBP', '£')];

const opts: QuickParseOptions = { categories, currencies, baseCurrency: 'USD' };

describe('parseQuickEntry', () => {
  it('returns empty defaults for blank input', () => {
    expect(parseQuickEntry('', opts)).toEqual({
      amount: null,
      currency: 'USD',
      description: '',
      tags: '',
      categoryId: null,
    });
    expect(parseQuickEntry('   ', opts).amount).toBeNull();
  });

  it('parses a simple "description amount" line', () => {
    const r = parseQuickEntry('lunch 12.50', opts);
    expect(r.amount).toBe(12.5);
    expect(r.currency).toBe('USD');
    expect(r.description).toBe('lunch');
    expect(r.tags).toBe('');
    expect(r.categoryId).toBeNull(); // no "lunch" category
  });

  it('matches a category by whole-word name', () => {
    expect(parseQuickEntry('groceries 43', opts)).toMatchObject({
      amount: 43,
      description: 'groceries',
      categoryId: 2,
    });
  });

  it('takes the first number as the amount when text leads', () => {
    expect(parseQuickEntry('45 coffee', opts)).toMatchObject({
      amount: 45,
      description: 'coffee',
    });
  });

  it('reads an explicit 3-letter currency code (case-insensitive) and tags', () => {
    const r = parseQuickEntry('taxi 20 EUR #travel', opts);
    expect(r).toMatchObject({ amount: 20, currency: 'EUR', description: 'taxi', tags: 'travel' });
    expect(parseQuickEntry('taxi 20 eur', opts).currency).toBe('EUR');
  });

  it('collects multiple tags into a space-separated string', () => {
    const r = parseQuickEntry('food 10 #weekly #lunch', opts);
    expect(r).toMatchObject({ amount: 10, description: 'food', tags: 'weekly lunch', categoryId: 1 });
  });

  it('adopts the currency from a leading symbol on the amount', () => {
    expect(parseQuickEntry('$12 lunch', opts)).toMatchObject({ amount: 12, currency: 'USD', description: 'lunch' });
  });

  it('adopts the currency from a trailing symbol on the amount', () => {
    expect(parseQuickEntry('12€ pain', opts)).toMatchObject({ amount: 12, currency: 'EUR', description: 'pain' });
  });

  it('accepts a comma decimal separator', () => {
    expect(parseQuickEntry('12,50 bread', opts).amount).toBe(12.5);
  });

  it('handles a description with no amount', () => {
    expect(parseQuickEntry('transport', opts)).toMatchObject({
      amount: null,
      description: 'transport',
      categoryId: 3,
    });
  });

  it('uses only the first number as amount; later numbers stay in description', () => {
    expect(parseQuickEntry('coffee 2 8', opts)).toMatchObject({ amount: 2, description: 'coffee 8' });
  });

  it('prefers the longest (most specific) category match', () => {
    // "food & dining" contains both "Food" and "Food & Dining"; longest wins.
    expect(parseQuickEntry('food & dining 30', opts)).toMatchObject({
      amount: 30,
      description: 'food & dining',
      categoryId: 4,
    });
  });

  it('does not treat a 3-decimal token as an amount', () => {
    const r = parseQuickEntry('12.345 thing', opts);
    expect(r.amount).toBeNull();
    expect(r.description).toBe('12.345 thing');
  });

  it('parses a tag-only line', () => {
    expect(parseQuickEntry('#work', opts)).toMatchObject({ amount: null, tags: 'work', description: '' });
  });

  it('consumes an explicit base-currency code token', () => {
    expect(parseQuickEntry('lunch 5 USD', opts)).toMatchObject({ amount: 5, currency: 'USD', description: 'lunch' });
  });

  it('ignores inactive categories', () => {
    expect(parseQuickEntry('old stuff 5', opts).categoryId).toBeNull();
  });

  it('does not accept zero or negative amounts (left in description)', () => {
    expect(parseQuickEntry('0 lunch', opts).amount).toBeNull();
    expect(parseQuickEntry('-5 lunch', opts).amount).toBeNull();
  });

  it('does not loose-match a category substring inside a larger word', () => {
    // "foodie" must NOT match the "Food" category (whole-word only).
    expect(parseQuickEntry('foodie 9', opts).categoryId).toBeNull();
  });

  it('does not adopt an ambiguous currency symbol shared by >1 currency', () => {
    // Both USD and CAD use "$" — the symbol can't identify one currency, so a
    // "$"-prefixed token is NOT treated as an amount (type a code instead).
    const ambiguous: QuickParseOptions = {
      categories,
      currencies: [cur('USD', '$', true), cur('CAD', '$')],
      baseCurrency: 'USD',
    };
    const r = parseQuickEntry('$50 coffee', ambiguous);
    expect(r.amount).toBeNull();
    expect(r.description).toBe('$50 coffee');
    // …but an explicit code still resolves the currency.
    expect(parseQuickEntry('50 CAD coffee', ambiguous)).toMatchObject({
      amount: 50,
      currency: 'CAD',
      description: 'coffee',
    });
  });
});
