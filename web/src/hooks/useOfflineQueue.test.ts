// happy-dom's IndexedDB is incomplete; this provides a spec-compliant one.
// useOfflineQueue is a thin live-view over the real offline-queue lib, so we
// exercise it end-to-end against fake-indexeddb rather than mocking the lib —
// that is the only way to prove the subscribe/refresh wiring works.
import 'fake-indexeddb/auto';
import { describe, test, expect, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import {
  enqueue,
  removeQueued,
  getAllQueued,
  markNeedsSignIn,
  clearNeedsSignIn,
} from '@/lib/offline-queue';
import { useOfflineQueue } from './useOfflineQueue';
import type { CreateTransactionInput } from '@/hooks/useTransactions';

const U = 1;

function payload(over: Partial<CreateTransactionInput> = {}): CreateTransactionInput {
  return { date: '2026-05-27', amount: 5, description: 'x', category_id: 1, ...over };
}

beforeEach(async () => {
  for (const uid of [U, 2]) {
    for (const item of await getAllQueued(uid)) await removeQueued(uid, item.id);
    clearNeedsSignIn(uid);
  }
});

describe('useOfflineQueue', () => {
  test('starts empty then reflects an enqueue (live via subscribe)', async () => {
    const { result } = renderHook(() => useOfflineQueue(U));
    await waitFor(() => expect(result.current.count).toBe(0));
    await act(async () => {
      await enqueue(U, payload({ description: 'a' }));
    });
    await waitFor(() => expect(result.current.count).toBe(1));
    expect(result.current.pending[0].payload.description).toBe('a');
  });

  test('count drops when a row is removed', async () => {
    const id = await enqueue(U, payload());
    const { result } = renderHook(() => useOfflineQueue(U));
    await waitFor(() => expect(result.current.count).toBe(1));
    await act(async () => {
      await removeQueued(U, id);
    });
    await waitFor(() => expect(result.current.count).toBe(0));
  });

  test('an undefined user id yields an empty view', async () => {
    await enqueue(U, payload());
    const { result } = renderHook(() => useOfflineQueue(undefined));
    await waitFor(() => expect(result.current.count).toBe(0));
    expect(result.current.pending).toEqual([]);
  });

  // "Will sync when you're back online" is a false promise for a queue the
  // server has already refused; the screen needs to know so it can ask for a
  // sign-in instead.
  test('reports when the queue is held for sign-in, live', async () => {
    await enqueue(U, payload());
    const { result } = renderHook(() => useOfflineQueue(U));
    await waitFor(() => expect(result.current.count).toBe(1));
    expect(result.current.needsSignIn).toBe(false);

    act(() => {
      markNeedsSignIn(U);
    });
    await waitFor(() => expect(result.current.needsSignIn).toBe(true));

    act(() => {
      clearNeedsSignIn(U);
    });
    await waitFor(() => expect(result.current.needsSignIn).toBe(false));
  });

  test('the hold of another user never shows here', async () => {
    markNeedsSignIn(2);
    const { result } = renderHook(() => useOfflineQueue(U));
    await waitFor(() => expect(result.current.count).toBe(0));
    expect(result.current.needsSignIn).toBe(false);
  });

  test('shows only the selected user queue (per-user namespacing)', async () => {
    await enqueue(U, payload({ description: 'mine' }));
    await enqueue(2, payload({ description: 'theirs' }));
    const { result } = renderHook(() => useOfflineQueue(U));
    await waitFor(() => expect(result.current.count).toBe(1));
    expect(result.current.pending[0].payload.description).toBe('mine');
  });
});
