import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

import { api } from '@/api/client';
import { useTrashCount, TRASH_CHANGED_EVENT } from './useTrashCount';

const mockedGet = api.get as unknown as ReturnType<typeof vi.fn>;

function trashListResponse(total: number) {
  // Mirror the deletedTransactionListResponse shape from the backend
  // — only `total` is read by the hook, the rest is realistic
  // surrounding noise.
  return {
    transactions: [],
    total,
    page: 1,
    per_page: 1,
  };
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

    const { result } = renderHook(() => useTrashCount(false));

    // Let any pending microtasks flush so a buggy fetch would have a
    // chance to land.
    await Promise.resolve();
    await Promise.resolve();

    expect(result.current.count).toBe(0);
    expect(mockedGet).not.toHaveBeenCalled();
  });

  it('fetches and exposes the count on mount when enabled', async () => {
    mockedGet.mockResolvedValue(trashListResponse(3));

    const { result } = renderHook(() => useTrashCount(true));

    await waitFor(() => expect(result.current.count).toBe(3));
    // The endpoint asks for one row to keep payload tiny — we read
    // total from the response envelope.
    expect(mockedGet).toHaveBeenCalledWith(
      expect.stringMatching(/^transactions\/deleted\?/),
    );
  });

  it('refetches when TRASH_CHANGED_EVENT fires', async () => {
    mockedGet
      .mockResolvedValueOnce(trashListResponse(1))
      .mockResolvedValueOnce(trashListResponse(5));

    const { result } = renderHook(() => useTrashCount(true));

    await waitFor(() => expect(result.current.count).toBe(1));

    act(() => {
      window.dispatchEvent(new Event(TRASH_CHANGED_EVENT));
    });

    await waitFor(() => expect(result.current.count).toBe(5));
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });

  it('refetches when the window regains focus', async () => {
    mockedGet
      .mockResolvedValueOnce(trashListResponse(2))
      .mockResolvedValueOnce(trashListResponse(9));

    const { result } = renderHook(() => useTrashCount(true));
    await waitFor(() => expect(result.current.count).toBe(2));

    act(() => {
      window.dispatchEvent(new Event('focus'));
    });

    await waitFor(() => expect(result.current.count).toBe(9));
  });

  it('resets count to 0 if enabled flips from true to false', async () => {
    mockedGet.mockResolvedValue(trashListResponse(4));

    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useTrashCount(enabled),
      { initialProps: { enabled: true } },
    );
    await waitFor(() => expect(result.current.count).toBe(4));

    rerender({ enabled: false });

    await waitFor(() => expect(result.current.count).toBe(0));
  });

  it('swallows fetch errors and leaves count at the prior value', async () => {
    mockedGet
      .mockResolvedValueOnce(trashListResponse(6))
      .mockRejectedValueOnce(new Error('network'));

    const { result } = renderHook(() => useTrashCount(true));
    await waitFor(() => expect(result.current.count).toBe(6));

    act(() => {
      window.dispatchEvent(new Event(TRASH_CHANGED_EVENT));
    });

    // Give the rejected refetch time to settle.
    await new Promise((r) => setTimeout(r, 5));

    // Prior value preserved, no thrown rejection bubbled out.
    expect(result.current.count).toBe(6);
  });

  it('ignores a stale (out-of-order) response and keeps the newer count', async () => {
    // Two refetches fire back-to-back; the FIRST resolves AFTER the
    // SECOND (network reorder). The hook must keep the second
    // response's count, not let the older one overwrite it.
    let resolveFirst: ((v: { total: number }) => void) | null = null;
    mockedGet
      .mockReturnValueOnce(
        new Promise<{ total: number }>((r) => {
          resolveFirst = r;
        }),
      )
      .mockResolvedValueOnce(trashListResponse(20));

    const { result } = renderHook(() => useTrashCount(true));

    // The mount-time fetch is in flight (unresolved). Dispatch fires
    // the second fetch, which resolves immediately.
    act(() => {
      window.dispatchEvent(new Event(TRASH_CHANGED_EVENT));
    });

    await waitFor(() => expect(result.current.count).toBe(20));

    // Now resolve the original (older, slower) request with a stale 1.
    act(() => {
      resolveFirst!({ total: 1 });
    });

    // Give the resolved-then microtask a tick.
    await new Promise((r) => setTimeout(r, 5));

    // Count must stay at 20 — the stale 1 is dropped on the floor.
    expect(result.current.count).toBe(20);
  });
});
