import { renderHook, waitFor } from '@testing-library/react';
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';

vi.mock('../api/client', () => ({
  api: { get: vi.fn() },
}));

import { api } from '../api/client';
import { useRecentTransactions } from './useRecentTransactions';

const mockedApi = vi.mocked(api);

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

const listResponse = {
  transactions: [{ id: 7 }, { id: 9 }],
  total: 2,
  page: 1,
  per_page: 5,
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('useRecentTransactions', () => {
  test('fetches the latest rows when enabled', async () => {
    mockedApi.get.mockResolvedValue(listResponse);

    const { result } = renderHook(
      () => useRecentTransactions(5, true, 0),
      { wrapper: makeWrapper() },
    );

    await waitFor(() => {
      expect(result.current.recent).toEqual(listResponse.transactions);
    });
    expect(mockedApi.get).toHaveBeenCalledWith(
      'transactions?page=1&per_page=5&sort_by=date&sort_dir=desc',
    );
  });

  test('does not fetch and returns empty when disabled (offline)', async () => {
    mockedApi.get.mockResolvedValue(listResponse);

    const { result } = renderHook(
      () => useRecentTransactions(5, false, 0),
      { wrapper: makeWrapper() },
    );

    await new Promise((r) => setTimeout(r, 10));
    expect(result.current.recent).toEqual([]);
    expect(mockedApi.get).not.toHaveBeenCalled();
  });

  test('refetches when refreshSignal changes', async () => {
    mockedApi.get
      .mockResolvedValueOnce(listResponse)
      .mockResolvedValueOnce({
        transactions: [{ id: 11 }],
        total: 1,
        page: 1,
        per_page: 5,
      });

    const { result, rerender } = renderHook(
      ({ signal }: { signal: number }) =>
        useRecentTransactions(5, true, signal),
      { initialProps: { signal: 0 }, wrapper: makeWrapper() },
    );

    await waitFor(() => {
      expect(result.current.recent).toEqual(listResponse.transactions);
    });

    rerender({ signal: 1 });

    await waitFor(() => {
      expect(result.current.recent).toEqual([{ id: 11 }]);
    });
    expect(mockedApi.get).toHaveBeenCalledTimes(2);
  });

  test('exposes an imperative refetch', async () => {
    mockedApi.get.mockResolvedValue(listResponse);

    const { result } = renderHook(
      () => useRecentTransactions(5, true, 0),
      { wrapper: makeWrapper() },
    );

    await waitFor(() => {
      expect(result.current.recent).toEqual(listResponse.transactions);
    });
    expect(typeof result.current.refetch).toBe('function');
    result.current.refetch();
    await waitFor(() => {
      expect(mockedApi.get).toHaveBeenCalledTimes(2);
    });
  });
});
