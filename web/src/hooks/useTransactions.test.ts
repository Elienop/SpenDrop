import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useTransactions } from './useTransactions';

describe('useTransactions filters', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>;
  const emptyResponse = {
    transactions: [],
    total: 0,
    page: 1,
    per_page: 20,
  };

  beforeEach(() => {
    fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(emptyResponse),
    } as Response);
  });

  afterEach(() => {
    fetchSpy.mockRestore();
  });

  function getLastFetchUrl(): string {
    const lastCall = fetchSpy.mock.calls[fetchSpy.mock.calls.length - 1];
    return lastCall[0] as string;
  }

  it('includes categoryIds as category_ids param when set', async () => {
    const { result } = renderHook(() => useTransactions());
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setFilter('categoryIds', '1,2,3');
    });

    await vi.waitFor(() => {
      expect(fetchSpy.mock.calls.length).toBeGreaterThanOrEqual(2);
    });

    const url = getLastFetchUrl();
    expect(url).toContain('category_ids=1%2C2%2C3');
  });

  it('skips category_id when categoryIds is present', async () => {
    const { result } = renderHook(() => useTransactions());
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setFilter('categoryId', '5');
    });
    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('category_id=5');
    });

    await act(async () => {
      result.current.setFilter('categoryIds', '1,2');
    });
    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('category_ids=1%2C2');
      expect(url).not.toContain('category_id=5');
    });
  });

  it('includes amountMin as amount_min param when set', async () => {
    const { result } = renderHook(() => useTransactions());
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setFilter('amountMin', '100');
    });

    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('amount_min=100');
    });
  });

  it('includes amountMax as amount_max param when set', async () => {
    const { result } = renderHook(() => useTransactions());
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setFilter('amountMax', '500');
    });

    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('amount_max=500');
    });
  });

  it('includes tags param when set', async () => {
    const { result } = renderHook(() => useTransactions());
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setFilter('tags', 'groceries,utilities');
    });

    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('tags=groceries%2Cutilities');
    });
  });

  it('omits new filter params when empty', async () => {
    renderHook(() => useTransactions());
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    const url = getLastFetchUrl();
    expect(url).not.toContain('category_ids');
    expect(url).not.toContain('amount_min');
    expect(url).not.toContain('amount_max');
    expect(url).not.toContain('tags');
  });

  it('clears new filter fields on clearFilters', async () => {
    const { result } = renderHook(() => useTransactions());
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setFilter('amountMin', '100');
    });
    await vi.waitFor(() => {
      expect(getLastFetchUrl()).toContain('amount_min=100');
    });

    await act(async () => {
      result.current.clearFilters();
    });
    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).not.toContain('amount_min');
    });
  });
});
