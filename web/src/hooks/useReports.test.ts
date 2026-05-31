import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';

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

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

describe('useReports (TanStack Query)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('useYearOverYear: a new year fetches under its own key and that data wins', async () => {
    const y2025 = { current_year: 2025, previous_year: 2024, months: [] };
    const y2026 = { current_year: 2026, previous_year: 2025, months: [] };
    mockedApi.get.mockImplementation((path: string) =>
      path.includes('year=2026') ? Promise.resolve(y2026) : Promise.resolve(y2025),
    );

    const { result, rerender } = renderHook(
      ({ year }: { year: number }) => useYearOverYear(year),
      { initialProps: { year: 2025 }, wrapper: makeWrapper() },
    );

    await waitFor(() => expect(result.current.data).toEqual(y2025));

    rerender({ year: 2026 });

    await waitFor(() => expect(result.current.data).toEqual(y2026));
  });

  test('useTopMerchants: changing limit fetches the new key and returns its merchants', async () => {
    const five = { merchants: [{ name: 'five', total: 1, count: 1 }] };
    const ten = { merchants: [{ name: 'ten', total: 2, count: 2 }] };
    mockedApi.get.mockImplementation((path: string) =>
      path.includes('limit=10') ? Promise.resolve(ten) : Promise.resolve(five),
    );

    const { result, rerender } = renderHook(
      ({ limit }: { limit: number }) => useTopMerchants(2026, 4, limit),
      { initialProps: { limit: 5 }, wrapper: makeWrapper() },
    );

    await waitFor(() => expect(result.current.data).toEqual(five.merchants));

    rerender({ limit: 10 });

    await waitFor(() => expect(result.current.data).toEqual(ten.merchants));
  });

  test('useRecurring.refetch re-pulls and exposes the latest response', async () => {
    const A = { data: [{ description: 'A', amount: 1, count: 1, months: [] }] };
    const B = { data: [{ description: 'B', amount: 2, count: 2, months: [] }] };
    let call = 0;
    mockedApi.get.mockImplementation(() => {
      call += 1;
      return Promise.resolve(call === 1 ? A : B);
    });

    const { result } = renderHook(() => useRecurring(2026), {
      wrapper: makeWrapper(),
    });

    await waitFor(() => expect(result.current.data).toEqual(A.data));

    await act(async () => {
      result.current.refetch();
    });

    await waitFor(() => expect(result.current.data).toEqual(B.data));
  });

  test('maps response envelopes correctly', async () => {
    // res.categories projection
    mockedApi.get.mockResolvedValue({ categories: [{ name: 'Food', months: [] }] });
    const trends = renderHook(() => useCategoryTrends(12), {
      wrapper: makeWrapper(),
    });
    await waitFor(() =>
      expect(trends.result.current.data).toEqual([{ name: 'Food', months: [] }]),
    );

    // res.data projection
    mockedApi.get.mockResolvedValue({
      data: [{ year: 2026, month: 1, income: 1, expenses: 2 }],
    });
    const inc = renderHook(() => useIncomeExpenses(12), { wrapper: makeWrapper() });
    await waitFor(() =>
      expect(inc.result.current.data).toEqual([
        { year: 2026, month: 1, income: 1, expenses: 2 },
      ]),
    );

    // res.merchants projection
    mockedApi.get.mockResolvedValue({ merchants: [{ name: 'M', total: 1, count: 1 }] });
    const merch = renderHook(() => useTopMerchants(2026, 1), {
      wrapper: makeWrapper(),
    });
    await waitFor(() =>
      expect(merch.result.current.data).toEqual([{ name: 'M', total: 1, count: 1 }]),
    );

    // raw (no envelope) projection
    mockedApi.get.mockResolvedValue({
      current_year: 2026,
      previous_year: 2025,
      months: [],
    });
    const yoy = renderHook(() => useYearOverYear(2026), { wrapper: makeWrapper() });
    await waitFor(() =>
      expect(yoy.result.current.data).toEqual({
        current_year: 2026,
        previous_year: 2025,
        months: [],
      }),
    );
  });

  test('sets error from Error.message and falls back to the initial data', async () => {
    mockedApi.get.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useIncomeExpenses(12), {
      wrapper: makeWrapper(),
    });

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.error).toBe('boom');
    expect(result.current.data).toEqual([]);
  });
});
