import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useTransactions } from './useTransactions';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

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
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
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
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
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
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
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
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
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
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
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
    renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
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
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
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

describe('useTransactions perPage', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>;
  const emptyResponse = {
    transactions: [],
    total: 0,
    page: 1,
    per_page: 20,
  };

  beforeEach(() => {
    localStorage.clear();
    fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(emptyResponse),
    } as Response);
  });

  afterEach(() => {
    fetchSpy.mockRestore();
    localStorage.clear();
  });

  function getLastFetchUrl(): string {
    const lastCall = fetchSpy.mock.calls[fetchSpy.mock.calls.length - 1];
    return lastCall[0] as string;
  }

  it('exposes setPerPage and defaults to 20', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    expect(result.current.perPage).toBe(20);
    expect(typeof result.current.setPerPage).toBe('function');
  });

  it('changes per_page param in query when setPerPage is called', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setPerPage(50);
    });

    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('per_page=50');
    });
  });

  it('resets to page 1 when perPage changes', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    // First go to page 2
    await act(async () => {
      result.current.setPage(2);
    });
    await vi.waitFor(() => {
      expect(getLastFetchUrl()).toContain('page=2');
    });

    // Change perPage — should reset to page 1
    await act(async () => {
      result.current.setPerPage(10);
    });

    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('page=1');
      expect(url).toContain('per_page=10');
    });
  });

  it('persists perPage to localStorage', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setPerPage(50);
    });

    expect(localStorage.getItem('spendrop-tx-per-page')).toBe('50');
  });

  it('reads perPage from localStorage on mount', async () => {
    localStorage.setItem('spendrop-tx-per-page', '100');
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    expect(result.current.perPage).toBe(100);
    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('per_page=100');
    });
  });
});

describe('useTransactions sorting', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>;
  const emptyResponse = {
    transactions: [],
    total: 0,
    page: 1,
    per_page: 20,
  };

  beforeEach(() => {
    localStorage.clear();
    fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(emptyResponse),
    } as Response);
  });

  afterEach(() => {
    fetchSpy.mockRestore();
    localStorage.clear();
  });

  function getLastFetchUrl(): string {
    const lastCall = fetchSpy.mock.calls[fetchSpy.mock.calls.length - 1];
    return lastCall[0] as string;
  }

  it('exposes sortBy and sortDir with defaults date/desc', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    expect(result.current.sortBy).toBe('date');
    expect(result.current.sortDir).toBe('desc');
  });

  it('includes sort_by and sort_dir in query string', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    const url = getLastFetchUrl();
    expect(url).toContain('sort_by=date');
    expect(url).toContain('sort_dir=desc');

    // Verify the exposed values
    expect(result.current.sortBy).toBe('date');
    expect(result.current.sortDir).toBe('desc');
  });

  it('setSort changes column and defaults to desc', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setSort('amount');
    });

    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('sort_by=amount');
      expect(url).toContain('sort_dir=desc');
    });
    expect(result.current.sortBy).toBe('amount');
    expect(result.current.sortDir).toBe('desc');
  });

  it('setSort toggles direction when same column is clicked', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    // Default is date/desc. Clicking date should toggle to asc.
    await act(async () => {
      result.current.setSort('date');
    });

    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('sort_by=date');
      expect(url).toContain('sort_dir=asc');
    });
    expect(result.current.sortDir).toBe('asc');

    // Clicking date again should toggle back to desc.
    await act(async () => {
      result.current.setSort('date');
    });

    await vi.waitFor(() => {
      const url = getLastFetchUrl();
      expect(url).toContain('sort_dir=desc');
    });
  });

  it('exposes setSort function', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    expect(typeof result.current.setSort).toBe('function');
  });
});

describe('useTransactions deleteByFilter', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>;
  const emptyResponse = {
    transactions: [],
    total: 0,
    page: 1,
    per_page: 20,
  };

  beforeEach(() => {
    localStorage.clear();
    fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(
      (input: RequestInfo | URL) => {
        const url = typeof input === 'string' ? input : input.toString();
        // delete-by-filter returns {deleted: n}; list returns the empty
        // paginated payload. Both need a Response-like shape.
        const body = url.includes('delete-by-filter')
          ? { deleted: 42 }
          : emptyResponse;
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve(body),
        } as Response);
      },
    );
  });

  afterEach(() => {
    fetchSpy.mockRestore();
    localStorage.clear();
  });

  function findCall(predicate: (url: string) => boolean):
    | { url: string; init?: RequestInit }
    | undefined {
    for (const call of fetchSpy.mock.calls) {
      const url = String(call[0]);
      if (predicate(url)) {
        return { url, init: call[1] as RequestInit | undefined };
      }
    }
    return undefined;
  }

  it('POSTs to delete-by-filter with no query string when filters are empty', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    let deleted = 0;
    await act(async () => {
      deleted = await result.current.deleteByFilter();
    });

    expect(deleted).toBe(42);
    const call = findCall((u) => u.includes('delete-by-filter'));
    expect(call).toBeDefined();
    // No query string when no filters are set.
    expect(call!.url).toMatch(/delete-by-filter$/);
    expect(call!.init?.method).toBe('POST');
  });

  it('serializes active filters into the query string (no pagination/sort)', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setFilter('dateFrom', '2026-04-01');
      result.current.setFilter('dateTo', '2026-04-30');
      result.current.setFilter('categoryIds', '1,2,3');
      result.current.setFilter('search', 'coffee');
    });

    await act(async () => {
      await result.current.deleteByFilter();
    });

    const call = findCall((u) => u.includes('delete-by-filter'));
    expect(call).toBeDefined();
    const url = call!.url;
    expect(url).toContain('date_from=2026-04-01');
    expect(url).toContain('date_to=2026-04-30');
    expect(url).toContain('category_ids=1%2C2%2C3');
    expect(url).toContain('search=coffee');
    // Pagination and sort do NOT leak into the destructive call.
    expect(url).not.toContain('page=');
    expect(url).not.toContain('per_page=');
    expect(url).not.toContain('sort_by=');
    expect(url).not.toContain('sort_dir=');
  });

  it('prefers category_ids over category_id, matching the list endpoint', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    await act(async () => {
      result.current.setFilter('categoryId', '5');
      result.current.setFilter('categoryIds', '1,2');
    });

    await act(async () => {
      await result.current.deleteByFilter();
    });

    const call = findCall((u) => u.includes('delete-by-filter'));
    expect(call).toBeDefined();
    expect(call!.url).toContain('category_ids=1%2C2');
    expect(call!.url).not.toContain('category_id=5');
  });

  it('triggers a refetch after a successful delete', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await vi.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });
    const callsBefore = fetchSpy.mock.calls.length;

    await act(async () => {
      await result.current.deleteByFilter();
    });

    // Expect at least the DELETE call + a follow-up GET to refresh the list.
    await vi.waitFor(() => {
      expect(fetchSpy.mock.calls.length).toBeGreaterThanOrEqual(
        callsBefore + 2,
      );
    });
    // The last call should be a GET transactions (refetch), not the POST.
    const lastUrl = String(
      fetchSpy.mock.calls[fetchSpy.mock.calls.length - 1][0],
    );
    expect(lastUrl).not.toContain('delete-by-filter');
    expect(lastUrl).toContain('transactions?');
  });
});
