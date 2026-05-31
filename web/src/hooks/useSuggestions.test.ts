import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import type { TransactionSuggestions } from '../api/types';

vi.mock('../api/client', () => ({
  api: { get: vi.fn() },
}));

import { api } from '../api/client';
import { useSuggestions } from './useSuggestions';

const mockedGet = api.get as unknown as ReturnType<typeof vi.fn>;

const sample: TransactionSuggestions = {
  descriptions: ['lunch', 'coffee'],
  tags: ['work', 'personal'],
};

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

describe('useSuggestions', () => {
  beforeEach(() => {
    mockedGet.mockReset();
  });

  it('returns the empty shape before the fetch resolves', () => {
    mockedGet.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useSuggestions(), { wrapper: makeWrapper() });
    expect(result.current).toEqual({ descriptions: [], tags: [] });
  });

  it('returns suggestions after the fetch resolves', async () => {
    mockedGet.mockResolvedValue(sample);
    const { result } = renderHook(() => useSuggestions(), { wrapper: makeWrapper() });

    await waitFor(() => expect(result.current).toEqual(sample));
    expect(mockedGet).toHaveBeenCalledWith('transactions/suggestions');
  });

  it('stays on the empty shape on error (non-critical)', async () => {
    mockedGet.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useSuggestions(), { wrapper: makeWrapper() });

    await new Promise((r) => setTimeout(r, 10));
    expect(result.current).toEqual({ descriptions: [], tags: [] });
  });

  it('changing refreshKey triggers a refetch', async () => {
    mockedGet.mockResolvedValue(sample);
    const { rerender } = renderHook(
      ({ k }: { k: number }) => useSuggestions(k),
      { initialProps: { k: 0 }, wrapper: makeWrapper() },
    );
    await waitFor(() => expect(mockedGet).toHaveBeenCalledTimes(1));

    rerender({ k: 1 });
    await waitFor(() => expect(mockedGet).toHaveBeenCalledTimes(2));
  });
});
