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
  };
}
