import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import type { Currency } from '@/api/types';

// Mock the api client at module level so each test can control the promise.
vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

import { api } from '@/api/client';
import { useCurrencies, __resetCurrenciesCacheForTests } from './useCurrencies';

const mockedGet = api.get as unknown as ReturnType<typeof vi.fn>;

const sampleCurrencies: Currency[] = [
  { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'EUR', name: 'Euro', symbol: '€', rate_to_base: 0.9, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'ZRO', name: 'ZeroRate', symbol: 'Z', rate_to_base: 0, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
];

describe('useCurrencies', () => {
  beforeEach(() => {
    __resetCurrenciesCacheForTests();
    mockedGet.mockReset();
  });

  it('fetches the list once and reuses the promise across hook consumers', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);

    const { result: r1 } = renderHook(() => useCurrencies());
    const { result: r2 } = renderHook(() => useCurrencies());

    await waitFor(() => {
      expect(r1.current.loading).toBe(false);
      expect(r2.current.loading).toBe(false);
    });

    expect(mockedGet).toHaveBeenCalledTimes(1);
    expect(r1.current.list).toEqual(sampleCurrencies);
    expect(r2.current.list).toEqual(sampleCurrencies);
  });

  it('returns the is_base code as baseCode', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.baseCode).toBe('USD');
  });

  it('falls back to USD when no currency has is_base === true', async () => {
    mockedGet.mockResolvedValue(
      sampleCurrencies.map((c) => ({ ...c, is_base: false })),
    );
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.baseCode).toBe('USD');
  });

  it('rateFor returns rate_to_base for known active currencies', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.rateFor('USD')).toBe(1);
    expect(result.current.rateFor('EUR')).toBe(0.9);
    expect(result.current.rateFor('LBP')).toBe(90000);
  });

  it('rateFor returns null for unknown codes', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.rateFor('XYZ')).toBe(null);
  });

  it('rateFor returns null for zero rate_to_base', async () => {
    mockedGet.mockResolvedValue(sampleCurrencies);
    const { result } = renderHook(() => useCurrencies());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.rateFor('ZRO')).toBe(null);
  });

  it('transitions loading: true → false on resolve', async () => {
    let resolveFn!: (value: Currency[]) => void;
    mockedGet.mockReturnValue(
      new Promise<Currency[]>((resolve) => {
        resolveFn = resolve;
      }),
    );

    const { result } = renderHook(() => useCurrencies());
    expect(result.current.loading).toBe(true);
    expect(result.current.list).toEqual([]);

    resolveFn(sampleCurrencies);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.list).toEqual(sampleCurrencies);
  });

  it('surfaces API errors without throwing during render', async () => {
    mockedGet.mockRejectedValue(new Error('network down'));
    const { result } = renderHook(() => useCurrencies());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toMatch(/network down/);
    expect(result.current.list).toEqual([]);
    expect(result.current.baseCode).toBe('USD'); // DEFAULT_CURRENCY fallback
    expect(result.current.rateFor('EUR')).toBe(null);
  });
});
