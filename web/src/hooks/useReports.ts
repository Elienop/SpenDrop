import { useState, useEffect, useRef, useCallback } from 'react';
import { api } from '../api/client';
import { TOP_MERCHANTS_DEFAULT_LIMIT } from '@/lib/constants';
import type {
  BudgetVsActualEntry,
  CategoryBreakdownItem,
  CategoryTrendEntry,
  ExpenseVelocityData,
  HeatmapEntry,
  IncomeExpenseEntry,
  RecurringEntry,
  TagBreakdownEntry,
  TopMerchantEntry,
  YoYResponse,
} from '../api/types';

/**
 * Shared out-of-order guard for the report hooks. A monotonic genRef token
 * (same pattern as useTrashCount/useRecentTransactions) ensures only the
 * latest in-flight fetch may commit state, so a slow stale response from a
 * previous year/month/limit can't clobber the current one.
 *
 * Each hook supplies a `useCallback`'d fetcher closing over its deps; the
 * effect re-runs when that fetcher identity changes (keeping the hooks
 * linter's exhaustive-deps happy) and the genRef discards any continuation
 * from a superseded run.
 */
function useGuardedFetch<T>(fetcher: () => Promise<T>, initial: T) {
  const [data, setData] = useState<T>(initial);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');
  const genRef = useRef(0);

  useEffect(() => {
    const gen = ++genRef.current;
    setFetching(true);
    setError('');
    fetcher()
      .then((res) => {
        if (gen === genRef.current) setData(res);
      })
      .catch((err) => {
        if (gen !== genRef.current) return;
        setError(err instanceof Error ? err.message : 'Failed to load');
      })
      .finally(() => {
        if (gen !== genRef.current) return;
        setLoading(false);
        setFetching(false);
      });
  }, [fetcher]);

  return { data, loading, fetching, error };
}

export function useYearOverYear(year: number) {
  const fetcher = useCallback(
    () => api.get<YoYResponse | null>(`reports/year-over-year?year=${year}`),
    [year],
  );
  return useGuardedFetch<YoYResponse | null>(fetcher, null);
}

export function useCategoryTrends(months: number) {
  const fetcher = useCallback(
    () =>
      api
        .get<{ categories: CategoryTrendEntry[] }>(
          `reports/category-trends?months=${months}`,
        )
        .then((res) => res.categories),
    [months],
  );
  return useGuardedFetch<CategoryTrendEntry[]>(fetcher, []);
}

export function useIncomeExpenses(months: number) {
  const fetcher = useCallback(
    () =>
      api
        .get<{ data: IncomeExpenseEntry[] }>(
          `reports/income-expenses?months=${months}`,
        )
        .then((res) => res.data),
    [months],
  );
  return useGuardedFetch<IncomeExpenseEntry[]>(fetcher, []);
}

export function useTopMerchants(
  year: number,
  month: number,
  limit: number = TOP_MERCHANTS_DEFAULT_LIMIT,
) {
  const fetcher = useCallback(
    () =>
      api
        .get<{ merchants: TopMerchantEntry[] }>(
          `reports/top-merchants?year=${year}&month=${month}&limit=${limit}`,
        )
        .then((res) => res.merchants),
    [year, month, limit],
  );
  return useGuardedFetch<TopMerchantEntry[]>(fetcher, []);
}

export function useBudgetVsActual(year: number) {
  const fetcher = useCallback(
    () =>
      api
        .get<{ data: BudgetVsActualEntry[] }>(
          `reports/budget-vs-actual?year=${year}`,
        )
        .then((res) => res.data),
    [year],
  );
  return useGuardedFetch<BudgetVsActualEntry[]>(fetcher, []);
}

export function useExpenseVelocity(year: number, month: number) {
  const fetcher = useCallback(
    () =>
      api.get<ExpenseVelocityData>(
        `reports/expense-velocity?year=${year}&month=${month}`,
      ),
    [year, month],
  );
  return useGuardedFetch<ExpenseVelocityData | null>(fetcher, null);
}

export function useSpendingHeatmap(year: number) {
  const fetcher = useCallback(
    () =>
      api
        .get<{ data: HeatmapEntry[] }>(`reports/spending-heatmap?year=${year}`)
        .then((res) => res.data),
    [year],
  );
  return useGuardedFetch<HeatmapEntry[]>(fetcher, []);
}

export function useRecurring(year: number) {
  const [refreshKey, setRefreshKey] = useState(0);
  const fetcher = useCallback(
    () =>
      api
        .get<{ data: RecurringEntry[] }>(`reports/recurring?year=${year}`)
        .then((res) => res.data),
    // refreshKey is an intentional re-trigger token: refetch() bumps it to
    // produce a fresh fetcher identity (and thus a guarded re-fetch). The
    // linter can't see it referenced in the body, so silence the false
    // "unnecessary dependency" warning here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [year, refreshKey],
  );
  const state = useGuardedFetch<RecurringEntry[]>(fetcher, []);
  const refetch = () => setRefreshKey((k) => k + 1);

  return { ...state, refetch };
}

export function useTagBreakdown(year: number, month: number) {
  const fetcher = useCallback(
    () =>
      api
        .get<{ data: TagBreakdownEntry[] }>(
          `reports/tag-breakdown?year=${year}&month=${month}`,
        )
        .then((res) => res.data),
    [year, month],
  );
  return useGuardedFetch<TagBreakdownEntry[]>(fetcher, []);
}

export async function dismissRecurring(year: number, description: string): Promise<void> {
  await api.post('reports/recurring/dismiss', { year, description });
}

export function useCategoryBreakdown(year: number, month: number) {
  const fetcher = useCallback(
    () =>
      api
        .get<{ categories: CategoryBreakdownItem[] }>(
          `dashboard/categories?year=${year}&month=${month}`,
        )
        .then((res) => res.categories),
    [year, month],
  );
  return useGuardedFetch<CategoryBreakdownItem[]>(fetcher, []);
}
