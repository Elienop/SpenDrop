import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

import { api } from '../api/client';
import { useDashboard } from './useDashboard';
import type { CategoryBreakdownItem } from '../api/types';

const mockedApi = vi.mocked(api);

const mockSummary = {
  year: 2026,
  month: 4,
  budget: 3000,
  total_spent: 1200,
  total_income: 4000,
  remaining: 1800,
  savings_this_month: 500,
  savings_goal: 6000,
  savings_ytd: 2000,
  savings_goal_progress: 33.3,
};

const mockTrend = [
  { year: 2026, month: 3, total_spent: 1100, total_income: 3800 },
  { year: 2026, month: 4, total_spent: 1200, total_income: 4000 },
];

const mockCategories: CategoryBreakdownItem[] = [
  { id: 1, name: 'Food', total: 500, limit: null, over: false },
  { id: 2, name: 'Rent', total: 700, limit: null, over: false },
];

describe('useDashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('fetches summary, trend, and categories in parallel', async () => {
    mockedApi.get.mockImplementation((path: string) => {
      if (path.includes('dashboard/summary')) return Promise.resolve(mockSummary);
      if (path.includes('dashboard/trend')) return Promise.resolve({ trend: mockTrend });
      if (path.includes('dashboard/categories'))
        return Promise.resolve({ categories: mockCategories });
      return Promise.reject(new Error('unknown path'));
    });

    const { result } = renderHook(() => useDashboard(2026, 4));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.summary).toEqual(mockSummary);
    expect(result.current.trend).toEqual(mockTrend);
    expect(result.current.categories).toEqual(mockCategories);
    expect(result.current.error).toBe('');
  });

  test('starts in loading state', () => {
    mockedApi.get.mockReturnValue(new Promise(() => {}));

    const { result } = renderHook(() => useDashboard(2026, 4));

    expect(result.current.loading).toBe(true);
    expect(result.current.summary).toBeNull();
    expect(result.current.trend).toEqual([]);
    expect(result.current.categories).toEqual([]);
  });

  test('sets error on fetch failure', async () => {
    mockedApi.get.mockRejectedValue(new Error('Network error'));

    const { result } = renderHook(() => useDashboard(2026, 4));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBe('Network error');
    expect(result.current.summary).toBeNull();
  });

  test('passes year and month as query params', async () => {
    mockedApi.get.mockImplementation((path: string) => {
      if (path.includes('trend')) return Promise.resolve({ trend: mockTrend });
      if (path.includes('categories')) return Promise.resolve({ categories: mockCategories });
      return Promise.resolve(mockSummary);
    });

    renderHook(() => useDashboard(2026, 4));

    await waitFor(() => {
      expect(mockedApi.get).toHaveBeenCalledWith(
        'dashboard/summary?year=2026&month=4',
      );
    });
  });

  test('keeps the fresh period and drops a stale out-of-order response', async () => {
    // The first (2026,4) trio never resolves until we release it manually;
    // the second (2026,5) trio resolves immediately with fresh sentinel data.
    // When the stale (2026,4) trio finally settles it must NOT clobber the
    // newer period's data.
    let releaseStale: (() => void) | null = null;
    const staleGate = new Promise<void>((r) => {
      releaseStale = r;
    });

    const freshSummary = { ...mockSummary, month: 5, total_spent: 9999 };
    const freshTrend = [{ year: 2026, month: 5, total_spent: 9999, total_income: 5000 }];
    const freshCategories: CategoryBreakdownItem[] = [
      { id: 99, name: 'Fresh', total: 1234, limit: null, over: false },
    ];

    mockedApi.get.mockImplementation((path: string) => {
      const isMay = path.includes('month=5');
      if (path.includes('dashboard/summary')) {
        return isMay
          ? Promise.resolve(freshSummary)
          : staleGate.then(() => mockSummary);
      }
      if (path.includes('dashboard/trend')) {
        return isMay
          ? Promise.resolve({ trend: freshTrend })
          : staleGate.then(() => ({ trend: mockTrend }));
      }
      if (path.includes('dashboard/categories')) {
        return isMay
          ? Promise.resolve({ categories: freshCategories })
          : staleGate.then(() => ({ categories: mockCategories }));
      }
      return Promise.reject(new Error('unknown path'));
    });

    const { result, rerender } = renderHook(
      ({ month }: { month: number }) => useDashboard(2026, month),
      { initialProps: { month: 4 } },
    );

    // Switch to May before the April trio resolves.
    rerender({ month: 5 });

    await waitFor(() => {
      expect(result.current.summary).toEqual(freshSummary);
    });
    expect(result.current.trend).toEqual(freshTrend);
    expect(result.current.categories).toEqual(freshCategories);

    // Release the stale April trio; it must be discarded.
    act(() => {
      releaseStale!();
    });
    await new Promise((r) => setTimeout(r, 5));

    expect(result.current.summary).toEqual(freshSummary);
    expect(result.current.trend).toEqual(freshTrend);
    expect(result.current.categories).toEqual(freshCategories);
    expect(result.current.error).toBe('');
  });

  test('loading stays false across a period change (refetch shows fetching, not loading)', async () => {
    // Locks the documented contract: `loading` is true only until the first
    // trio settles; a subsequent period change shows `fetching` (the dim gate)
    // but NOT `loading` (the skeleton gate). The genRef guard must not regress
    // this.
    let releaseSecond: (() => void) | null = null;
    const secondGate = new Promise<void>((r) => {
      releaseSecond = r;
    });

    mockedApi.get.mockImplementation((path: string) => {
      const isMay = path.includes('month=5');
      const resolveTrio = () => {
        if (path.includes('dashboard/summary')) return mockSummary;
        if (path.includes('dashboard/trend')) return { trend: mockTrend };
        return { categories: mockCategories };
      };
      if (isMay) return secondGate.then(resolveTrio);
      return Promise.resolve(resolveTrio());
    });

    const { result, rerender } = renderHook(
      ({ month }: { month: number }) => useDashboard(2026, month),
      { initialProps: { month: 4 } },
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    rerender({ month: 5 });

    // During the in-flight refetch: fetching true, loading stays false.
    await waitFor(() => {
      expect(result.current.fetching).toBe(true);
    });
    expect(result.current.loading).toBe(false);

    act(() => {
      releaseSecond!();
    });
    await waitFor(() => {
      expect(result.current.fetching).toBe(false);
    });
    expect(result.current.loading).toBe(false);
  });

  test('a stale error does not surface after a fresh success', async () => {
    // The stale (2026,4) trio rejects late; the fresh (2026,5) trio succeeds
    // first. The late rejection must not flip `error`.
    let rejectStale: ((e: Error) => void) | null = null;
    const staleReject = new Promise<never>((_, rej) => {
      rejectStale = rej;
    });

    mockedApi.get.mockImplementation((path: string) => {
      const isMay = path.includes('month=5');
      if (isMay) {
        if (path.includes('dashboard/summary')) return Promise.resolve(mockSummary);
        if (path.includes('dashboard/trend')) return Promise.resolve({ trend: mockTrend });
        return Promise.resolve({ categories: mockCategories });
      }
      // April branch: hang on a rejection we release later.
      return staleReject;
    });

    const { result, rerender } = renderHook(
      ({ month }: { month: number }) => useDashboard(2026, month),
      { initialProps: { month: 4 } },
    );

    rerender({ month: 5 });

    await waitFor(() => {
      expect(result.current.summary).toEqual(mockSummary);
    });

    act(() => {
      rejectStale!(new Error('stale boom'));
    });
    await new Promise((r) => setTimeout(r, 5));

    expect(result.current.error).toBe('');
    expect(result.current.summary).toEqual(mockSummary);
  });
});
