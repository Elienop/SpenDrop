import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

import { api } from '../api/client';
import {
  useYearOverYear,
  useTopMerchants,
  useRecurring,
  useCategoryTrends,
  useIncomeExpenses,
} from './useReports';

const mockedApi = vi.mocked(api);

describe('useReports out-of-order guard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('useGuardedFetch via useYearOverYear: fresh year wins over a stale out-of-order response', async () => {
    // year=2025 returns a deferred promise we resolve LATER; year=2026
    // resolves immediately. The stale 2025 response must not clobber 2026.
    let resolveStale: ((v: unknown) => void) | null = null;
    const staleGate = new Promise<unknown>((r) => {
      resolveStale = r;
    });
    const stalePayload = { current_year: 2025, previous_year: 2024, months: [] };
    const freshPayload = { current_year: 2026, previous_year: 2025, months: [] };

    mockedApi.get.mockImplementation((path: string) =>
      path.includes('year=2026') ? Promise.resolve(freshPayload) : staleGate,
    );

    const { result, rerender } = renderHook(
      ({ year }: { year: number }) => useYearOverYear(year),
      { initialProps: { year: 2025 } },
    );

    rerender({ year: 2026 });

    await waitFor(() => {
      expect(result.current.data).toEqual(freshPayload);
    });

    act(() => {
      resolveStale!(stalePayload);
    });
    await new Promise((r) => setTimeout(r, 5));

    expect(result.current.data).toEqual(freshPayload);
  });

  test('useTopMerchants drops stale response when limit changes mid-flight', async () => {
    let resolveStale: ((v: unknown) => void) | null = null;
    const staleGate = new Promise<unknown>((r) => {
      resolveStale = r;
    });
    const stale = { merchants: [{ name: 'stale', total: 1, count: 1 }] };
    const fresh = { merchants: [{ name: 'fresh', total: 2, count: 2 }] };

    mockedApi.get.mockImplementation((path: string) =>
      path.includes('limit=10') ? Promise.resolve(fresh) : staleGate,
    );

    const { result, rerender } = renderHook(
      ({ limit }: { limit: number }) => useTopMerchants(2026, 4, limit),
      { initialProps: { limit: 5 } },
    );

    rerender({ limit: 10 });

    await waitFor(() => {
      expect(result.current.data).toEqual(fresh.merchants);
    });

    act(() => {
      resolveStale!(stale);
    });
    await new Promise((r) => setTimeout(r, 5));

    expect(result.current.data).toEqual(fresh.merchants);
  });

  test('useRecurring.refetch re-pulls and the latest refetch wins', async () => {
    // Mount resolves A. refetch() fires a slow B, then a second refetch()
    // fires a fast C before B resolves; C must win and B must be dropped.
    let resolveB: ((v: unknown) => void) | null = null;
    const bGate = new Promise<unknown>((r) => {
      resolveB = r;
    });
    const A = { data: [{ description: 'A', amount: 1, count: 1, months: [] }] };
    const B = { data: [{ description: 'B', amount: 2, count: 2, months: [] }] };
    const C = { data: [{ description: 'C', amount: 3, count: 3, months: [] }] };

    let call = 0;
    mockedApi.get.mockImplementation(() => {
      call += 1;
      if (call === 1) return Promise.resolve(A);
      if (call === 2) return bGate; // slow B
      return Promise.resolve(C); // fast C
    });

    const { result } = renderHook(() => useRecurring(2026));

    await waitFor(() => {
      expect(result.current.data).toEqual(A.data);
    });

    act(() => {
      result.current.refetch();
    });
    act(() => {
      result.current.refetch();
    });

    await waitFor(() => {
      expect(result.current.data).toEqual(C.data);
    });

    act(() => {
      resolveB!(B);
    });
    await new Promise((r) => setTimeout(r, 5));

    expect(result.current.data).toEqual(C.data);
  });

  test('maps response envelopes correctly', async () => {
    // res.categories projection
    mockedApi.get.mockResolvedValue({ categories: [{ name: 'Food', months: [] }] });
    const trends = renderHook(() => useCategoryTrends(12));
    await waitFor(() => {
      expect(trends.result.current.data).toEqual([{ name: 'Food', months: [] }]);
    });

    // res.data projection
    mockedApi.get.mockResolvedValue({ data: [{ year: 2026, month: 1, income: 1, expenses: 2 }] });
    const inc = renderHook(() => useIncomeExpenses(12));
    await waitFor(() => {
      expect(inc.result.current.data).toEqual([{ year: 2026, month: 1, income: 1, expenses: 2 }]);
    });

    // res.merchants projection
    mockedApi.get.mockResolvedValue({ merchants: [{ name: 'M', total: 1, count: 1 }] });
    const merch = renderHook(() => useTopMerchants(2026, 1));
    await waitFor(() => {
      expect(merch.result.current.data).toEqual([{ name: 'M', total: 1, count: 1 }]);
    });

    // raw (no envelope) projection
    mockedApi.get.mockResolvedValue({ current_year: 2026, previous_year: 2025, months: [] });
    const yoy = renderHook(() => useYearOverYear(2026));
    await waitFor(() => {
      expect(yoy.result.current.data).toEqual({
        current_year: 2026,
        previous_year: 2025,
        months: [],
      });
    });
  });

  test('sets error from Error.message and leaves prior data', async () => {
    mockedApi.get.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useIncomeExpenses(12));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBe('boom');
    expect(result.current.data).toEqual([]);
  });
});
