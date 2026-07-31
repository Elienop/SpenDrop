import { describe, test, expect, afterEach, vi } from 'vitest';
import { monthsToCoverYear, MAX_REPORT_MONTHS } from './utils';
import { MIN_DATA_YEAR, MAX_DATA_YEAR, yearOptionsFrom } from '@/lib/dates';

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

  test('the safety cap is exactly the data window, never narrower', () => {
    // MAX_REPORT_MONTHS is the sanity cap on the window helper. If it is ever
    // smaller than the span a transaction date may occupy, the oldest
    // selectable years start truncating silently — the exact defect the helper
    // exists to fix, reintroduced by its own guard rail.
    //
    // Both constants mirror Go (`MaxTrendMonths` and `MinDataYear`/
    // `MaxDataYear` in internal/api/limits.go), so widening the data window
    // without widening the cap fails here rather than in production.
    expect(MAX_REPORT_MONTHS).toBe((MAX_DATA_YEAR - MIN_DATA_YEAR + 1) * 12);
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
  // The offered list is no longer a range from a constant: it comes from the
  // ledger via `useReportYears`, which returns only years that hold rows. But
  // the server filters that list to [MinDataYear, MaxDataYear] and caps it at
  // the current year, so MIN_DATA_YEAR is the oldest year the Savings Select
  // can EVER render.
  //
  // This widened from MIN_YEAR (2000) to MIN_DATA_YEAR (1900) when the picker
  // stopped being bounded by the planning window: the reports year params
  // accept 1900, so a legacy 1900 row is now genuinely selectable, and it is
  // the real worst case for the window helper. Walking the whole data window
  // is strictly wider than any actual ledger, which is the point — a sparse
  // list cannot be enumerated, so we bound it instead.
  //
  // The clock is pinned per case because both the offered list and the
  // expectation depend on the current year. 2100 is included deliberately: it
  // is the one year where MAX_REPORT_MONTHS binds EXACTLY, so an off-by-one in
  // the cap shows up as a truncation here rather than years later in prod.
  for (const currentYear of [2026, 2033, 2034, 2040, 2060, MAX_DATA_YEAR]) {
    test(`current year ${currentYear}`, () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date(Date.UTC(currentYear, 5, 15)));

      const everyOfferableYear = Array.from(
        { length: currentYear - MIN_DATA_YEAR + 1 },
        (_, i) => MIN_DATA_YEAR + i,
      );
      const offered = yearOptionsFrom(everyOfferableYear);
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
