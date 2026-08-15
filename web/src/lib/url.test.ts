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

  test('a 100k-slash run trims in linear time', () => {
    const started = Date.now();
    expect(trimTrailingSlashes('/'.repeat(100_000))).toBe('');
    expect(Date.now() - started).toBeLessThan(250);
  });

  // The discriminator for the backtracking regex this helper replaced: a long
  // slash run followed by a non-slash makes `/\/+$/` retry from every offset
  // (~3s at this size), while the linear scan bails on the first character.
  test('a 100k-slash run followed by a non-slash returns fast', () => {
    const pathological = '/'.repeat(100_000) + 'x';
    const started = Date.now();
    expect(trimTrailingSlashes(pathological)).toBe(pathological);
    expect(Date.now() - started).toBeLessThan(250);
  });
});
