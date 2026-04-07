import { useState, useEffect } from 'react';
import { api } from '../api/client';
import type {
  YoYResponse,
  CategoryTrendEntry,
  IncomeExpenseEntry,
  TopMerchantEntry,
} from '../api/types';

export function useYearOverYear(year: number) {
  const [data, setData] = useState<YoYResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    setLoading(true);
    setError('');
    api
      .get<YoYResponse>(`reports/year-over-year?year=${year}`)
      .then(setData)
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => setLoading(false));
  }, [year]);

  return { data, loading, error };
}

export function useCategoryTrends(months: number) {
  const [data, setData] = useState<CategoryTrendEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    setLoading(true);
    setError('');
    api
      .get<{ categories: CategoryTrendEntry[] }>(`reports/category-trends?months=${months}`)
      .then((res) => setData(res.categories))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => setLoading(false));
  }, [months]);

  return { data, loading, error };
}

export function useIncomeExpenses(months: number) {
  const [data, setData] = useState<IncomeExpenseEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    setLoading(true);
    setError('');
    api
      .get<{ data: IncomeExpenseEntry[] }>(`reports/income-expenses?months=${months}`)
      .then((res) => setData(res.data))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => setLoading(false));
  }, [months]);

  return { data, loading, error };
}

export function useTopMerchants(year: number, month: number, limit = 10) {
  const [data, setData] = useState<TopMerchantEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    setLoading(true);
    setError('');
    api
      .get<{ merchants: TopMerchantEntry[] }>(
        `reports/top-merchants?year=${year}&month=${month}&limit=${limit}`,
      )
      .then((res) => setData(res.merchants))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => setLoading(false));
  }, [year, month, limit]);

  return { data, loading, error };
}
