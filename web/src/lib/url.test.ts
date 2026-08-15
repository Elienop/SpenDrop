import { describe, test, expect } from 'vitest';
import { trimTrailingSlashes } from './url';

describe('trimTrailingSlashes', () => {
  test('strips a run of trailing slashes', () => {
    expect(trimTrailingSlashes('https://x/api///')).toBe('https://x/api');
  });

  test('strips a single trailing slash', () => {
    expect(trimTrailingSlashes('/api/')).toBe('/api');
  });

  test('leaves a string with no trailing slash untouched', () => {
    expect(trimTrailingSlashes('a')).toBe('a');
    expect(trimTrailingSlashes('/api')).toBe('/api');
    expect(trimTrailingSlashes('https://x/a//b')).toBe('https://x/a//b');
  });

  test('an all-slash string trims to empty', () => {
    expect(trimTrailingSlashes('///')).toBe('');
    expect(trimTrailingSlashes('/')).toBe('');
  });

  test('the empty string stays empty', () => {
    expect(trimTrailingSlashes('')).toBe('');
  });

  // Output only. An all-slash string is NOT a timing discriminator: `/\/+$/`
  // matches it on the first attempt, so the regex is instant here too.
  test('a 100k-slash run trims to empty', () => {
    expect(trimTrailingSlashes('/'.repeat(100_000))).toBe('');
  });

  // This one IS the discriminator for the backtracking regex this helper
  // replaced: a long slash run followed by a NON-slash makes `/\/+$/` retry
  // the greedy run from every offset, while the linear scan bails on the first
  // character it reads.
  //
  // The 250ms budget is deliberately loose, not a tuned figure: the linear
  // scan takes microseconds and the regex takes ~2.4s here, so the margin is
  // ~4 orders of magnitude either way. Do not tighten it — the point is to
  // separate O(n) from O(n^2), not to benchmark the scan.
  test('a 100k-slash run followed by a non-slash returns fast', () => {
    const pathological = '/'.repeat(100_000) + 'x';
    const started = Date.now();
    expect(trimTrailingSlashes(pathological)).toBe(pathological);
    expect(Date.now() - started).toBeLessThan(250);
  });
});
