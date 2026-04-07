import { useState, useEffect } from 'react';
import { api } from '../api/client';
import type {
  DashboardSummary,
  DashboardTrendItem,
  CategoryBreakdownItem,
} from '../api/types';

interface UseDashboardResult {
  summary: DashboardSummary | null;
  trend: DashboardTrendItem[];
  categories: CategoryBreakdownItem[];
  loading: boolean;
  error: string;
}

export function useDashboard(
  year?: number,
  month?: number,
): UseDashboardResult {
  const [summary, setSummary] = useState<DashboardSummary | null>(null);
  const [trend, setTrend] = useState<DashboardTrendItem[]>([]);
  const [categories, setCategories] = useState<CategoryBreakdownItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    setLoading(true);
    setError('');

    const params =
      year !== undefined && month !== undefined
        ? `?year=${year}&month=${month}`
        : '';

    Promise.all([
      api.get<DashboardSummary>(`dashboard/summary${params}`),
      api.get<{ trend: DashboardTrendItem[] }>(`dashboard/trend?months=12${params ? `&year=${year}&month=${month}` : ''}`),
      api.get<{ categories: CategoryBreakdownItem[] }>(`dashboard/categories${params}`),
    ])
      .then(([summaryData, trendData, categoriesData]) => {
        setSummary(summaryData);
        setTrend(trendData.trend);
        setCategories(categoriesData.categories);
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : 'Failed to load dashboard');
      })
      .finally(() => {
        setLoading(false);
      });
  }, [year, month]);

  return { summary, trend, categories, loading, error };
}
