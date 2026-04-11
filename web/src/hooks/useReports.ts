import { useState, useEffect } from 'react';
import { api } from '../api/client';
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

export function useYearOverYear(year: number) {
  const [data, setData] = useState<YoYResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<YoYResponse>(`reports/year-over-year?year=${year}`)
      .then(setData)
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [year]);

  return { data, loading, fetching, error };
}

export function useCategoryTrends(months: number) {
  const [data, setData] = useState<CategoryTrendEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<{ categories: CategoryTrendEntry[] }>(`reports/category-trends?months=${months}`)
      .then((res) => setData(res.categories))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [months]);

  return { data, loading, fetching, error };
}

export function useIncomeExpenses(months: number) {
  const [data, setData] = useState<IncomeExpenseEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<{ data: IncomeExpenseEntry[] }>(`reports/income-expenses?months=${months}`)
      .then((res) => setData(res.data))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [months]);

  return { data, loading, fetching, error };
}

export function useTopMerchants(year: number, month: number, limit = 10) {
  const [data, setData] = useState<TopMerchantEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<{ merchants: TopMerchantEntry[] }>(
        `reports/top-merchants?year=${year}&month=${month}&limit=${limit}`,
      )
      .then((res) => setData(res.merchants))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [year, month, limit]);

  return { data, loading, fetching, error };
}

export function useBudgetVsActual(year: number) {
  const [data, setData] = useState<BudgetVsActualEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<{ data: BudgetVsActualEntry[] }>(`reports/budget-vs-actual?year=${year}`)
      .then((res) => setData(res.data))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [year]);

  return { data, loading, fetching, error };
}

export function useExpenseVelocity(year: number, month: number) {
  const [data, setData] = useState<ExpenseVelocityData | null>(null);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<ExpenseVelocityData>(`reports/expense-velocity?year=${year}&month=${month}`)
      .then(setData)
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [year, month]);

  return { data, loading, fetching, error };
}

export function useSpendingHeatmap(year: number) {
  const [data, setData] = useState<HeatmapEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<{ data: HeatmapEntry[] }>(`reports/spending-heatmap?year=${year}`)
      .then((res) => setData(res.data))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [year]);

  return { data, loading, fetching, error };
}

export function useRecurring(year: number) {
  const [data, setData] = useState<RecurringEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<{ data: RecurringEntry[] }>(`reports/recurring?year=${year}`)
      .then((res) => setData(res.data))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [year, refreshKey]);

  const refetch = () => setRefreshKey((k) => k + 1);

  return { data, loading, fetching, error, refetch };
}

export function useTagBreakdown(year: number, month: number) {
  const [data, setData] = useState<TagBreakdownEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<{ data: TagBreakdownEntry[] }>(`reports/tag-breakdown?year=${year}&month=${month}`)
      .then((res) => setData(res.data))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [year, month]);

  return { data, loading, fetching, error };
}

export async function dismissRecurring(year: number, description: string): Promise<void> {
  await api.post('reports/recurring/dismiss', { year, description });
}

export function useCategoryBreakdown(year: number, month: number) {
  const [data, setData] = useState<CategoryBreakdownItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFetching(true);
    setError('');
    api
      .get<{ categories: CategoryBreakdownItem[] }>(`dashboard/categories?year=${year}&month=${month}`)
      .then((res) => setData(res.categories))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => { setLoading(false); setFetching(false); });
  }, [year, month]);

  return { data, loading, fetching, error };
}
