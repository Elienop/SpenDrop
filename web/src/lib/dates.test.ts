import { describe, it, expect } from 'vitest';
import { yearOptionsFrom } from './dates';

// `yearOptionsFrom` is the sparse-list replacement for `yearOptions(startYear)`.
// A ledger is not contiguous: a household with rows in 1984 and 2026 and
// nothing between should be offered two years, not forty-three.
//
// The variadic `selected` arguments are the direct replacement for the old
// `Math.min(floorYear, year)` guard at each call site, and they exist for one
// concrete failure: a Radix <Select> holding a `value` with no matching
// <SelectItem> renders a BLANK TRIGGER, silently, with no error and no
// console warning. So whatever the caller currently has selected must always
// survive into the option list, even when a refetch drops it from the ledger.
describe('yearOptionsFrom', () => {
  it('renders the ledger years it is given, newest first', () => {
    expect(yearOptionsFrom([2026, 2024, 1984])).toEqual([
      { value: '2026', label: '2026' },
      { value: '2024', label: '2024' },
      { value: '1984', label: '1984' },
    ]);
  });

  it('keeps gaps in the ledger as gaps', () => {
    // The whole point of the sparse list: 2025 has no rows, so it is not
    // offered. `yearOptions(1984)` would have produced all 43 years.
    expect(yearOptionsFrom([2026, 2024]).map((o) => o.value)).toEqual([
      '2026',
      '2024',
    ]);
  });

  it('sorts descending even when the input is not', () => {
    // The endpoint promises DESC, but the fallback list and the union with
    // `selected` are assembled client-side, so ordering cannot be assumed.
    expect(yearOptionsFrom([1984, 2026, 2024]).map((o) => o.value)).toEqual([
      '2026',
      '2024',
      '1984',
    ]);
  });

  it('deduplicates repeated years', () => {
    expect(yearOptionsFrom([2026, 2026, 2024]).map((o) => o.value)).toEqual([
      '2026',
      '2024',
    ]);
  });

  it('unions a selected year that the ledger no longer has', () => {
    // The blank-trigger case: the last 1984 row was just deleted, so the
    // refetched list no longer contains 1984 — but the Select still holds it.
    expect(yearOptionsFrom([2026, 2024], 1984).map((o) => o.value)).toEqual([
      '2026',
      '2024',
      '1984',
    ]);
  });

  it('does not duplicate a selected year that is already present', () => {
    expect(yearOptionsFrom([2026, 2024], 2024).map((o) => o.value)).toEqual([
      '2026',
      '2024',
    ]);
  });

  it('unions every selected year, not just the first', () => {
    // PatternsTab drives TWO Selects (`year` and `tagYear`) off one list, so
    // a single-selection union would leave the second Select blank.
    expect(
      yearOptionsFrom([2026], 2024, 1984).map((o) => o.value),
    ).toEqual(['2026', '2024', '1984']);
  });

  it('returns an empty list for no years and no selection', () => {
    // Deliberately NOT "fall back to something": the caller decides what a
    // non-empty floor is. `useReportYears` guarantees a non-empty list, so
    // this branch is only reachable by a caller that passes neither.
    expect(yearOptionsFrom([])).toEqual([]);
  });

  it('still offers the selection when the ledger list is empty', () => {
    expect(yearOptionsFrom([], 2026).map((o) => o.value)).toEqual(['2026']);
  });
});
