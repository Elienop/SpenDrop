import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';

vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

import { api } from '@/api/client';
import { useTrashCount } from './useTrashCount';

const mockedGet = api.get as unknown as ReturnType<typeof vi.fn>;

function trashListResponse(total: number) {
  return { transactions: [], total, page: 1, per_page: 1 };
}

function makeHarness() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return { client, wrapper };
}

describe('useTrashCount', () => {
  beforeEach(() => {
    mockedGet.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('returns count=0 and does NOT call the API when disabled', async () => {
    mockedGet.mockResolvedValue(trashListResponse(7));
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useTrashCount(false), { wrapper });

    await Promise.resolve();
    await Promise.resolve();

    expect(result.current.count).toBe(0);
    expect(mockedGet).not.toHaveBeenCalled();
  });

  it('fetches and exposes the count on mount when enabled', async () => {
    mockedGet.mockResolvedValue(trashListResponse(3));
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useTrashCount(true), { wrapper });

    await waitFor(() => expect(result.current.count).toBe(3));
    expect(mockedGet).toHaveBeenCalledWith(
      expect.stringMatching(/^transactions\/deleted\?/),
    );
  });

  it('refetches when the ["trash"] key is invalidated (the SSE path)', async () => {
    mockedGet
      .mockResolvedValueOnce(trashListResponse(1))
      .mockResolvedValueOnce(trashListResponse(5));
    const { client, wrapper } = makeHarness();

    const { result } = renderHook(() => useTrashCount(true), { wrapper });
    await waitFor(() => expect(result.current.count).toBe(1));

    await client.invalidateQueries({ queryKey: ['trash'] });

    await waitFor(() => expect(result.current.count).toBe(5));
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });

  it('resets count to 0 if enabled flips from true to false', async () => {
    mockedGet.mockResolvedValue(trashListResponse(4));
    const { wrapper } = makeHarness();

    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useTrashCount(enabled),
      { initialProps: { enabled: true }, wrapper },
    );
    await waitFor(() => expect(result.current.count).toBe(4));

    rerender({ enabled: false });

    await waitFor(() => expect(result.current.count).toBe(0));
  });

  it('swallows fetch errors and leaves count at 0', async () => {
    mockedGet.mockRejectedValue(new Error('network'));
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useTrashCount(true), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.count).toBe(0);
  });
});
