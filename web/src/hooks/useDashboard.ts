import { useState, useEffect, useRef } from 'react';
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

export function useDashboard(
  year?: number,
  month?: number,
): UseDashboardResult {
  const [summary, setSummary] = useState<DashboardSummary | null>(null);
  const [trend, setTrend] = useState<DashboardTrendItem[]>([]);
  const [categories, setCategories] = useState<CategoryBreakdownItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');
  // Monotonic generation token for out-of-order response handling: a fast
  // period change re-runs this effect and issues a fresh trio; without this
  // guard a slower earlier trio could resolve last and overwrite the current
  // period. We bump per run and ignore any continuation whose gen is stale.
  // (Same pattern as useTrashCount/useRecentTransactions.)
  const genRef = useRef(0);

  useEffect(() => {
    const gen = ++genRef.current;
    setFetching(true);
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
        if (gen !== genRef.current) return; // stale trio, newer period in flight
        setSummary(summaryData);
        setTrend(trendData.trend);
        setCategories(categoriesData.categories);
      })
      .catch((err) => {
        if (gen !== genRef.current) return; // ignore stale errors too
        setError(err instanceof Error ? err.message : 'Failed to load dashboard');
      })
      .finally(() => {
        if (gen !== genRef.current) return; // don't clear flags for a stale run
        setLoading(false);
        setFetching(false);
      });
  }, [year, month]);

  return { summary, trend, categories, loading, fetching, error };
}
