import { describe, test, expect, afterEach, vi } from 'vitest';
import { monthsToCoverYear } from './utils';
import { MIN_YEAR, yearOptions } from '@/lib/dates';

describe('monthsToCoverYear', () => {
  test('the current year still uses the 24-month floor', () => {
    expect(monthsToCoverYear(2026, 2026)).toBe(24);
  });

  test('last year is fully covered by the floor', () => {
    // Jan 2025 is 19 months before Jul 2026, so 24 already reaches it.
    expect(monthsToCoverYear(2025, 2026)).toBe(24);
  });

  test('a year the 24-month window cannot reach gets a wider window', () => {
    // The regression: with a hardcoded 24, a mid-2026 window started in
    // August 2024, so 2024 rendered five months as though they were the
    // whole year. 36 months reaches January 2024 from any month of 2026.
    expect(monthsToCoverYear(2024, 2026)).toBe(36);
    expect(monthsToCoverYear(2024, 2026)).toBeGreaterThan(24);
  });

  test('scales with the distance to the selected year', () => {
    expect(monthsToCoverYear(2020, 2026)).toBe(84);
  });
});

describe('the window reaches January of every year the UI offers', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  // The clamp used to be Math.min(120, ...), which is exactly
  // (2033 - 2024 + 1) * 12 — sized against the old hard-coded floor. From 2034
  // it starts binding and silently truncates the oldest offered years again —
  // the same defect the helper was written to fix, reintroduced by the safety
  // cap. The previous test pinned the clamp (monthsToCoverYear(1990, 2026) ===
  // 120) instead of flagging it, so it would have gone green through the
  // regression.
  //
  // The floor is no longer a constant: it comes from the ledger via
  // `useReportYearFloor`, which clamps it to MIN_YEAR. So MIN_YEAR is the
  // WIDEST list the Savings year Select can ever render, and iterating
  // `yearOptions(MIN_YEAR)` is the real worst case — strictly wider than the
  // 2024-floored list this used to walk. The clock is pinned per case because
  // both the offered list and the expectation depend on the current year.
  for (const currentYear of [2026, 2033, 2034, 2040, 2060]) {
    test(`current year ${currentYear}`, () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date(Date.UTC(currentYear, 5, 15)));

      const offered = yearOptions(MIN_YEAR);
      expect(offered.length).toBeGreaterThan(0);

      for (const opt of offered) {
        const year = Number(opt.value);
        const months = monthsToCoverYear(year, currentYear);

        // The report window ends at the current month and spans `months`
        // buckets. Worst case within the year is December, so measure from
        // there: the first bucket must land on or before January of `year`.
        const earliest = new Date(Date.UTC(currentYear, 11 - (months - 1), 1))
          .toISOString()
          .slice(0, 7);
        expect(
          earliest <= `${year}-01`
            ? 'reaches January'
            : `truncated: ${year} gets ${months} months, starting ${earliest}`,
        ).toBe('reaches January');
      }
    });
  }
});
