import { useQuery } from '@tanstack/react-query';
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
 * Shared TanStack-Query primitive for the report hooks. Replaces the old
 * hand-rolled `useGuardedFetch` (useEffect + genRef out-of-order guard):
 * TanStack keys every `(year/month/limit/...)` combination separately, so a
 * slow stale response writes to a *different* cache entry and can never
 * clobber the current key — the genRef guard is now structural.
 *
 * The return shape is preserved verbatim for the report tabs:
 *   { data, loading, fetching, error }
 * `data` is coalesced to `initial` so it is never `undefined` (the tabs call
 * `.data.length` / `.data.reduce` / `.data.map` directly). The first key
 * segment is always `'reports'` so the SSE subscriber's
 * `invalidateQueries({ queryKey: ['reports'] })` prefix-matches every hook.
 */
function useReportQuery<T>(
  queryKey: readonly unknown[],
  fetcher: () => Promise<T>,
  initial: T,
) {
  const query = useQuery({ queryKey, queryFn: fetcher });
  return {
    data: query.data ?? initial,
    loading: query.isLoading,
    fetching: query.isFetching,
    error: query.error instanceof Error ? query.error.message : '',
  };
}

export function useYearOverYear(year: number) {
  return useReportQuery<YoYResponse | null>(
    ['reports', 'year-over-year', year],
    () => api.get<YoYResponse | null>(`reports/year-over-year?year=${year}`),
    null,
  );
}

export function useCategoryTrends(months: number) {
  return useReportQuery<CategoryTrendEntry[]>(
    ['reports', 'category-trends', months],
    () =>
      api
        .get<{ categories: CategoryTrendEntry[] }>(
          `reports/category-trends?months=${months}`,
        )
        .then((res) => res.categories),
    [],
  );
}

export function useIncomeExpenses(months: number) {
  return useReportQuery<IncomeExpenseEntry[]>(
    ['reports', 'income-expenses', months],
    () =>
      api
        .get<{ data: IncomeExpenseEntry[] }>(
          `reports/income-expenses?months=${months}`,
        )
        .then((res) => res.data),
    [],
  );
}

export function useTopMerchants(
  year: number,
  month: number,
  limit: number = TOP_MERCHANTS_DEFAULT_LIMIT,
) {
  return useReportQuery<TopMerchantEntry[]>(
    ['reports', 'top-merchants', year, month, limit],
    () =>
      api
        .get<{ merchants: TopMerchantEntry[] }>(
          `reports/top-merchants?year=${year}&month=${month}&limit=${limit}`,
        )
        .then((res) => res.merchants),
    [],
  );
}

export function useBudgetVsActual(year: number) {
  return useReportQuery<BudgetVsActualEntry[]>(
    ['reports', 'budget-vs-actual', year],
    () =>
      api
        .get<{ data: BudgetVsActualEntry[] }>(
          `reports/budget-vs-actual?year=${year}`,
        )
        .then((res) => res.data),
    [],
  );
}

export function useExpenseVelocity(year: number, month: number) {
  return useReportQuery<ExpenseVelocityData | null>(
    ['reports', 'expense-velocity', year, month],
    () =>
      api.get<ExpenseVelocityData>(
        `reports/expense-velocity?year=${year}&month=${month}`,
      ),
    null,
  );
}

export function useSpendingHeatmap(year: number) {
  return useReportQuery<HeatmapEntry[]>(
    ['reports', 'spending-heatmap', year],
    () =>
      api
        .get<{ data: HeatmapEntry[] }>(`reports/spending-heatmap?year=${year}`)
        .then((res) => res.data),
    [],
  );
}

export function useRecurring(year: number) {
  const query = useQuery({
    queryKey: ['reports', 'recurring', year],
    queryFn: () =>
      api
        .get<{ data: RecurringEntry[] }>(`reports/recurring?year=${year}`)
        .then((res) => res.data),
  });
  return {
    data: query.data ?? [],
    loading: query.isLoading,
    fetching: query.isFetching,
    error: query.error instanceof Error ? query.error.message : '',
    // refetch() bypasses staleTime and re-pulls immediately — used by the
    // PatternsTab "dismiss recurring" flow after a successful dismiss POST.
    refetch: () => {
      void query.refetch();
    },
  };
}

export function useTagBreakdown(year: number, month: number) {
  return useReportQuery<TagBreakdownEntry[]>(
    ['reports', 'tag-breakdown', year, month],
    () =>
      api
        .get<{ data: TagBreakdownEntry[] }>(
          `reports/tag-breakdown?year=${year}&month=${month}`,
        )
        .then((res) => res.data),
    [],
  );
}

export async function dismissRecurring(
  year: number,
  description: string,
): Promise<void> {
  await api.post('reports/recurring/dismiss', { year, description });
}

export function useCategoryBreakdown(year: number, month: number) {
  return useReportQuery<CategoryBreakdownItem[]>(
    ['reports', 'category-breakdown', year, month],
    () =>
      api
        .get<{ categories: CategoryBreakdownItem[] }>(
          `dashboard/categories?year=${year}&month=${month}`,
        )
        .then((res) => res.categories),
    [],
  );
}
