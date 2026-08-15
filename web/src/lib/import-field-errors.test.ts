import { describe, it, expect } from 'vitest';
import type { ImportFieldErrorField } from '@/api/types';
import {
  IMPORT_MONEY_FIELDS,
  fallbackFieldErrorMessage,
  isEditableInPreview,
  isMoneyField,
} from './import-field-errors';

/**
 * Every field the wire can flag. Written out rather than derived from the
 * type, because the point of the exhaustive tests below is to notice when
 * the union GROWS — a list that grew with it would keep passing.
 */
const ALL_FIELDS: ImportFieldErrorField[] = [
  'description',
  'tags',
  'notes',
  'rate',
  'original_currency',
  'amount',
];

describe('the two families', () => {
  it('splits money from length, with nothing in both and nothing in neither', () => {
    expect(ALL_FIELDS.filter(isMoneyField)).toEqual([
      'rate',
      'original_currency',
      'amount',
    ]);
    expect(ALL_FIELDS.filter((f) => !isMoneyField(f))).toEqual([
      'description',
      'tags',
      'notes',
    ]);
    expect([...IMPORT_MONEY_FIELDS].every(isMoneyField)).toBe(true);
  });

  it('offers a cell to exactly the fields the preview renders as one', () => {
    expect(ALL_FIELDS.filter(isEditableInPreview)).toEqual([
      'description',
      'rate',
      'amount',
    ]);
  });
});

describe('fallbackFieldErrorMessage', () => {
  // The server sends a `message` with every flag it emits, so none of
  // these should ever reach a screen. They exist because `message` is
  // optional on the wire and an omitted one would otherwise draw an empty
  // red box under a cell — and they are tested because "unreachable"
  // copy that turns out to be reachable is exactly the copy nobody has
  // ever read.

  it('never states a bound, a count, or a rate', () => {
    // The whole reason this module holds no constants: the server's limit
    // is in bytes, the user's is in characters, and anything measured
    // here would be a third number. A rate would be worse — it would be
    // invented.
    for (const field of ALL_FIELDS) {
      expect(fallbackFieldErrorMessage(field)).not.toMatch(/\d/);
    }
  });

  it('tells a money row to fix the money, not to shorten anything', () => {
    // The bug this guards: one fallback for both families told the user
    // of a row with no exchange rate that their value was "too long".
    for (const field of IMPORT_MONEY_FIELDS) {
      const message = fallbackFieldErrorMessage(field);
      expect(message).toMatch(/money/i);
      expect(message).not.toMatch(/too long|shorten/i);
    }
  });

  it('points each money field at the control that can actually fix it', () => {
    // Rate and amount have cells; an unknown currency does not, and is
    // resolved outside the session entirely.
    expect(fallbackFieldErrorMessage('rate')).toMatch(/here/);
    expect(fallbackFieldErrorMessage('amount')).toMatch(/here/);
    expect(fallbackFieldErrorMessage('original_currency')).toMatch(
      /Settings → Currencies/,
    );
    expect(fallbackFieldErrorMessage('original_currency')).not.toMatch(/here/);
  });

  it('keeps the length family’s two remedies apart', () => {
    // Description is editable in the preview; tags and notes are not
    // rendered at all, so their only fix is Skip or the spreadsheet.
    expect(fallbackFieldErrorMessage('description')).toMatch(/Shorten it here/);
    expect(fallbackFieldErrorMessage('notes')).toMatch(/spreadsheet/);
    expect(fallbackFieldErrorMessage('tags')).toMatch(/spreadsheet/);
  });

  it('offers a way out of every flag it can be asked about', () => {
    for (const field of ALL_FIELDS) {
      expect(fallbackFieldErrorMessage(field)).toMatch(/skip this row/i);
    }
  });
});
