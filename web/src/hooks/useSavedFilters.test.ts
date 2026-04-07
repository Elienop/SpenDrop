import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    del: vi.fn(),
  },
}));

import { api } from '../api/client';
import { useSavedFilters } from './useSavedFilters';

const mockedApi = vi.mocked(api);

const mockFilters = [
  {
    id: 1,
    user_id: 1,
    name: 'Groceries this month',
    filter_json: '{"categoryId":"3","dateFrom":"2026-04-01"}',
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
  },
  {
    id: 2,
    user_id: 1,
    name: 'Large expenses',
    filter_json: '{"amountMin":"100"}',
    created_at: '2026-04-02T00:00:00Z',
    updated_at: '2026-04-02T00:00:00Z',
  },
];

describe('useSavedFilters', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('fetches saved filters on mount', async () => {
    mockedApi.get.mockResolvedValue(mockFilters);

    const { result } = renderHook(() => useSavedFilters());

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(mockedApi.get).toHaveBeenCalledWith('filters');
    expect(result.current.savedFilters).toEqual(mockFilters);
  });

  test('starts in loading state with empty filters', () => {
    mockedApi.get.mockReturnValue(new Promise(() => {}));

    const { result } = renderHook(() => useSavedFilters());

    expect(result.current.loading).toBe(true);
    expect(result.current.savedFilters).toEqual([]);
  });

  test('handles fetch failure gracefully', async () => {
    mockedApi.get.mockRejectedValue(new Error('Network error'));

    const { result } = renderHook(() => useSavedFilters());

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    // Non-critical: filters remain empty, no error thrown
    expect(result.current.savedFilters).toEqual([]);
  });

  test('saveFilter posts and refetches', async () => {
    mockedApi.get.mockResolvedValue(mockFilters);
    mockedApi.post.mockResolvedValue(undefined);

    const { result } = renderHook(() => useSavedFilters());

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.saveFilter('New filter', '{"search":"test"}');
    });

    expect(mockedApi.post).toHaveBeenCalledWith('filters', {
      name: 'New filter',
      filter_json: '{"search":"test"}',
    });

    // Should have fetched twice: once on mount, once after save
    expect(mockedApi.get).toHaveBeenCalledTimes(2);
  });

  test('deleteFilter calls del and refetches', async () => {
    mockedApi.get.mockResolvedValue(mockFilters);
    mockedApi.del.mockResolvedValue(undefined);

    const { result } = renderHook(() => useSavedFilters());

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.deleteFilter(1);
    });

    expect(mockedApi.del).toHaveBeenCalledWith('filters/1');

    // Should have fetched twice: once on mount, once after delete
    expect(mockedApi.get).toHaveBeenCalledTimes(2);
  });

  test('refetch re-fetches filters', async () => {
    mockedApi.get.mockResolvedValue(mockFilters);

    const { result } = renderHook(() => useSavedFilters());

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    act(() => {
      result.current.refetch();
    });

    await waitFor(() => {
      expect(mockedApi.get).toHaveBeenCalledTimes(2);
    });
  });
});
