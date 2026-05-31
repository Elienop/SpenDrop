import { describe, test, expect } from 'vitest';
import { queryClient } from './queryClient';

describe('queryClient', () => {
  test('uses a finite staleTime (not Infinity) so SSE invalidation is the safe combo', () => {
    const defaults = queryClient.getDefaultOptions().queries;
    expect(defaults?.staleTime).toBeTypeOf('number');
    expect(defaults?.staleTime).toBeGreaterThan(0);
    expect(defaults?.staleTime).not.toBe(Infinity);
  });

  test('keeps refetchOnWindowFocus and refetchOnReconnect enabled (the correctness backstop)', () => {
    const defaults = queryClient.getDefaultOptions().queries;
    expect(defaults?.refetchOnWindowFocus).toBe(true);
    expect(defaults?.refetchOnReconnect).toBe(true);
  });

  test('sets a finite gcTime', () => {
    const defaults = queryClient.getDefaultOptions().queries;
    expect(defaults?.gcTime).toBeTypeOf('number');
    expect(defaults?.gcTime).toBeGreaterThan(0);
    expect(defaults?.gcTime).not.toBe(Infinity);
  });
});
