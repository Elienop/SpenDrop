import { describe, test, expect, vi, afterEach } from 'vitest';
import { onlineManager } from '@tanstack/react-query';
import { queryClient } from './queryClient';

const realOnLine = navigator.onLine;

afterEach(() => {
  Object.defineProperty(navigator, 'onLine', {
    configurable: true,
    value: realOnLine,
  });
  onlineManager.setOnline(true);
});

describe('queryClient', () => {
  // The wiring seam: online tracking can be perfectly correct in lib/online.ts
  // and still never run in the app. Loading the module that owns the app-wide
  // client must install it.
  test('installs navigator-backed online tracking on import', async () => {
    Object.defineProperty(navigator, 'onLine', {
      configurable: true,
      value: false,
    });
    // Reset to the manager's as-constructed default so the assertion can only
    // pass if importing the module re-seeds it.
    onlineManager.setOnline(true);

    vi.resetModules();
    await import('./queryClient');

    expect(onlineManager.isOnline()).toBe(false);
  });

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
