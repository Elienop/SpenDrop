import { renderHook, act, waitFor } from '@testing-library/react';
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';

// Mock api/client at module-load. Co-locating these bulk-update tests
// in their own *.tsx file (rather than alongside the existing fetch-spy
// suites in useTransactions.test.ts) keeps the two mocking strategies
// from clashing — vi.mock is hoisted module-wide and would otherwise
// short-circuit the global fetch spy in the sibling file.
vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    del: vi.fn(),
  },
}));

import { api } from '../api/client';
import { useTransactions, RefetchAfterMutationError } from './useTransactions';

const mockedApi = vi.mocked(api);

// Each test gets a fresh QueryClient with retries off and gc disabled so a
// failed query surfaces immediately and cached data never leaks across tests.
function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

const emptyListResponse = {
  transactions: [],
  total: 0,
  page: 1,
  per_page: 25,
};

beforeEach(() => {
  vi.clearAllMocks();
  // Default the mount-effect refetch to a benign empty response so
  // tests that don't override it still settle initialLoad → false.
  mockedApi.get.mockResolvedValue(emptyListResponse);
});

describe('useTransactions stale-while-filtering', () => {
  // B6c. Every filter/page/sort value sits in the query key, so a keystroke
  // in the search box mints a NEW key. Without placeholderData that key
  // starts empty, `isLoading` flips true, and the page swaps the whole table
  // for a skeleton on each keystroke.
  test('keeps the previous rows and holds initialLoad false while a changed filter loads', async () => {
    const firstPage = {
      transactions: [{ id: 1, description: 'sdold' }],
      total: 7,
      page: 1,
      per_page: 25,
    };
    const secondPage = {
      transactions: [{ id: 2, description: 'sdnew' }],
      total: 2,
      page: 1,
      per_page: 25,
    };
    // Hold the post-keystroke fetch open so the in-flight state is
    // observable rather than raced past.
    let releaseSecond: () => void = () => {};
    mockedApi.get
      .mockResolvedValueOnce(firstPage)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            releaseSecond = () => resolve(secondPage);
          }),
      )
      .mockResolvedValue(secondPage);

    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await waitFor(() =>
      expect(result.current.transactions[0]?.description).toBe('sdold'),
    );

    act(() => result.current.setFilter('search', 'new'));

    await waitFor(() => expect(result.current.fetching).toBe(true));
    expect(result.current.initialLoad).toBe(false);
    expect(result.current.transactions[0]?.description).toBe('sdold');
    // `total` is held over too, which is why filter-scoped writes have to
    // sit out this window — see the Replace All guard on the page.
    expect(result.current.total).toBe(7);
    expect(result.current.showingPrevious).toBe(true);

    await act(async () => {
      releaseSecond();
    });

    await waitFor(() =>
      expect(result.current.transactions[0]?.description).toBe('sdnew'),
    );
    expect(result.current.total).toBe(2);
    expect(result.current.showingPrevious).toBe(false);
  });
});

describe('useTransactions search debounce', () => {
  // B6n. Every filter sits in the query key, so before the debounce each
  // keystroke minted a key and fired its own request — six letters, six
  // round trips, five of them already obsolete when they landed.
  test('a burst of keystrokes reaches the query once, with the final term', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.initialLoad).toBe(false));
    const afterMount = mockedApi.get.mock.calls.length;

    // One act() per keystroke, NOT three calls inside one act(): batched
    // into a single commit the search term would only change once, and a
    // hook with no debounce at all would look like it coalesced them.
    act(() => result.current.setSearchInput('s'));
    act(() => result.current.setSearchInput('sp'));
    act(() => result.current.setSearchInput('spi'));

    await waitFor(() =>
      expect(result.current.buildFilterQuery()).toBe('search=spi'),
    );
    await waitFor(() => expect(result.current.fetching).toBe(false));

    const searchFetches = mockedApi.get.mock.calls
      .slice(afterMount)
      .map((call) => String(call[0]));
    expect(searchFetches).toHaveLength(1);
    expect(searchFetches[0]).toContain('search=spi');
  });

  test('echoes the keystroke immediately while the committed term lags', async () => {
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.initialLoad).toBe(false));

    act(() => result.current.setSearchInput('spinney'));

    // The box shows it at once — the input is never debounced.
    expect(result.current.searchInput).toBe('spinney');
    // Nothing filter-scoped can see it yet, and that gap is exactly what
    // the page's Replace All / Select-all-matching gate exists to cover:
    // a write firing here would target the pre-keystroke match set.
    expect(result.current.searchPending).toBe(true);
    expect(result.current.filters.search).toBe('');
    expect(result.current.buildFilterQuery()).toBe('');

    await waitFor(() => expect(result.current.searchPending).toBe(false));
    expect(result.current.buildFilterQuery()).toBe('search=spinney');
  });

  test('a programmatic filter set commits at once and moves the box with it', async () => {
    // Loading a saved filter goes through setFilter, not the box. If it did
    // not drag `searchInput` along, the box would keep showing the old term
    // AND the debounce effect would see the mismatch and write that stale
    // term straight back over the filter that was just applied.
    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.initialLoad).toBe(false));

    act(() => result.current.setFilter('search', 'coffee'));

    expect(result.current.searchInput).toBe('coffee');
    expect(result.current.searchPending).toBe(false);
    expect(result.current.buildFilterQuery()).toBe('search=coffee');

    act(() => result.current.clearFilters());

    expect(result.current.searchInput).toBe('');
    expect(result.current.searchPending).toBe(false);
    expect(result.current.buildFilterQuery()).toBe('');
  });
});

describe('useTransactions bulkUpdate', () => {
  test('returns the visible IDs from the post-PATCH refetch', async () => {
    // First api.get is the mount fetch; second is the post-PATCH refetch.
    mockedApi.get
      .mockResolvedValueOnce(emptyListResponse)
      .mockResolvedValueOnce({
        transactions: [{ id: 1 }, { id: 3 }, { id: 5 }],
        total: 3,
        page: 1,
        per_page: 25,
      });
    mockedApi.post.mockResolvedValueOnce({ updated: 2, skipped: 0 });

    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.initialLoad).toBe(false));

    let response: Awaited<ReturnType<typeof result.current.bulkUpdate>>;
    await act(async () => {
      response = await result.current.bulkUpdate({
        ids: [1, 2, 3],
        patch: { category_id: 5 },
      });
    });

    expect(response!).toEqual({
      updated: 2,
      skipped: 0,
      visibleIds: [1, 3, 5],
    });
    expect(mockedApi.post).toHaveBeenCalledWith('transactions/batch-update', {
      ids: [1, 2, 3],
      patch: { category_id: 5 },
    });
  });

  test('wraps refetch failure in RefetchAfterMutationError', async () => {
    mockedApi.get
      .mockResolvedValueOnce(emptyListResponse)
      .mockRejectedValueOnce(new Error('network blip'));
    mockedApi.post.mockResolvedValueOnce({ updated: 2, skipped: 0 });

    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.initialLoad).toBe(false));

    let err: unknown;
    await act(async () => {
      try {
        await result.current.bulkUpdate({
          ids: [1, 2],
          patch: { category_id: 5 },
        });
      } catch (e) {
        err = e;
      }
    });
    expect(err).toBeInstanceOf(RefetchAfterMutationError);
    expect((err as Error).message).toBe('network blip');
  });
});

describe('useTransactions bulkUpdateByFilter', () => {
  test('posts to update-by-filter and refetches', async () => {
    mockedApi.get
      .mockResolvedValueOnce(emptyListResponse)
      .mockResolvedValueOnce(emptyListResponse);
    mockedApi.post.mockResolvedValueOnce({ updated: 12 });

    const { result } = renderHook(() => useTransactions(), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.initialLoad).toBe(false));

    let response: Awaited<ReturnType<typeof result.current.bulkUpdateByFilter>>;
    await act(async () => {
      response = await result.current.bulkUpdateByFilter({
        filterQuery: 'search=cleaning',
        patch: { category_id: 5 },
      });
    });

    expect(response!).toEqual({ updated: 12 });
    expect(mockedApi.post).toHaveBeenCalledWith(
      'transactions/update-by-filter?search=cleaning',
      { patch: { category_id: 5 } },
    );
  });
});
