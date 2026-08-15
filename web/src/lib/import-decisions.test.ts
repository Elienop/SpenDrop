import { describe, it, expect, beforeEach } from 'vitest';
import {
  clearImportDecisions,
  loadImportDecisions,
  saveImportDecisions,
} from './import-decisions';
import { STORAGE_KEYS } from '@/lib/storage-keys';

describe('import decisions', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('round-trips the decisions for the session that made them', () => {
    saveImportDecisions('abc', {
      categoryMap: { Grocries: '2', Petrol: '5' },
      defaultCategoryId: 7,
    });

    expect(loadImportDecisions('abc')).toEqual({
      categoryMap: { Grocries: '2', Petrol: '5' },
      defaultCategoryId: 7,
    });
  });

  it('refuses to hand one session’s decisions to another', () => {
    // The trap this exists to avoid: a second upload whose spreadsheet
    // shares a category name would silently inherit the previous file's
    // destination for it — a decision the user never made about this file.
    saveImportDecisions('abc', {
      categoryMap: { Grocries: '2' },
      defaultCategoryId: null,
    });

    expect(loadImportDecisions('xyz')).toBeNull();
  });

  it('survives a record written by hand, or by an older shape', () => {
    // localStorage is user-editable and outlives deployments. A cast would
    // put `undefined` into a Select value or a string into a category id,
    // and the failure would land far from here.
    localStorage.setItem(
      STORAGE_KEYS.importDecisions,
      JSON.stringify({
        import_id: 'abc',
        categoryMap: { Good: '3', Bad: 4, Empty: '' },
        defaultCategoryId: 'not-a-number',
      }),
    );

    expect(loadImportDecisions('abc')).toEqual({
      categoryMap: { Good: '3' },
      defaultCategoryId: null,
    });
  });

  it('returns null for absent, unparseable, or non-object records', () => {
    expect(loadImportDecisions('abc')).toBeNull();
    localStorage.setItem(STORAGE_KEYS.importDecisions, 'not json');
    expect(loadImportDecisions('abc')).toBeNull();
    localStorage.setItem(STORAGE_KEYS.importDecisions, 'null');
    expect(loadImportDecisions('abc')).toBeNull();
    localStorage.setItem(STORAGE_KEYS.importDecisions, '"a string"');
    expect(loadImportDecisions('abc')).toBeNull();
  });

  it('clears the record outright', () => {
    saveImportDecisions('abc', { categoryMap: { A: '1' }, defaultCategoryId: 1 });
    clearImportDecisions();

    expect(localStorage.getItem(STORAGE_KEYS.importDecisions)).toBeNull();
    expect(loadImportDecisions('abc')).toBeNull();
  });
});
