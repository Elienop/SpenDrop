import { useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';
import type {
  DashboardSummary,
  DashboardTrendItem,
  CategoryBreakdownItem,
} from '../api/types';

export interface UseDashboardResult {
  summary: DashboardSummary | null;
  trend: DashboardTrendItem[];
  categories: CategoryBreakdownItem[];
  /** True only on the very first load (no data yet). */
  loading: boolean;
  /** True during any fetch (including refetches). */
  fetching: boolean;
  error: string;
  /**
   * Re-runs the current period's trio. This is the error state's recovery
   * verb, and it is deliberately a REFETCH and not `window.location.reload()`:
   * one failed query does not justify tearing down the client, and a reload
   * loses every unsaved control state on the page (and the focus with it).
   */
  refetch: () => void;
}

interface DashboardData {
  summary: DashboardSummary;
  trend: DashboardTrendItem[];
  categories: CategoryBreakdownItem[];
}

export function useDashboard(
  year?: number,
  month?: number,
): UseDashboardResult {
  // Key first segment is `'dashboard'` so an SSE invalidateQueries({
  // queryKey: ['dashboard'] }) prefix-matches every period and refetches.
  // TanStack keys the cache per (year, month) and ignores responses for a
  // superseded key, which retires the hand-rolled genRef out-of-order guard
  // the old hook carried.
  const query = useQuery<DashboardData>({
    queryKey: ['dashboard', year, month],
    queryFn: async () => {
      const params =
        year !== undefined && month !== undefined
          ? `?year=${year}&month=${month}`
          : '';
      const [summary, trendData, categoriesData] = await Promise.all([
        api.get<DashboardSummary>(`dashboard/summary${params}`),
        api.get<{ trend: DashboardTrendItem[] }>(
          `dashboard/trend?months=12${params ? `&year=${year}&month=${month}` : ''}`,
        ),
        api.get<{ categories: CategoryBreakdownItem[] }>(
          `dashboard/categories${params}`,
        ),
      ]);
      return {
        summary,
        trend: trendData.trend,
        categories: categoriesData.categories,
      };
    },
  });

  // Fire-and-forget, matching `useTransactions`'s `refetch`. The rejection is
  // not dropped on the floor — a failed refetch lands back in `query.error`
  // and re-renders the same alert the user just retried from.
  const refetch = useCallback(() => {
    void query.refetch();
  }, [query]);

  return {
    summary: query.data?.summary ?? null,
    trend: query.data?.trend ?? [],
    categories: query.data?.categories ?? [],
    loading: query.isLoading,
    fetching: query.isFetching,
    error: query.isError
      ? query.error instanceof Error
        ? query.error.message
        : 'Failed to load dashboard'
      : '',
    refetch,
  };
}
