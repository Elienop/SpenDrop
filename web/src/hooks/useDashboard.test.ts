import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';

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
});
