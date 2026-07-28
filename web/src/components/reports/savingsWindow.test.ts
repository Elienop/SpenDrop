import { describe, test, expect } from 'vitest';
import { monthsToCoverYear } from './utils';

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

  test('scales with distance and stays within the backend cap', () => {
    expect(monthsToCoverYear(2020, 2026)).toBe(84);
    // MaxTrendMonths is 120 server-side; never ask for more.
    expect(monthsToCoverYear(1990, 2026)).toBe(120);
  });
});
