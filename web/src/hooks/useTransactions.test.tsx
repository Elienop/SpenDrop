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
