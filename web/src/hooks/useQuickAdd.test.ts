import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

// Mock the API client (keep the real ApiError so any instanceof checks hold).
const post = vi.fn();
const del = vi.fn();
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: { post: (...a: unknown[]) => post(...a), del: (...a: unknown[]) => del(...a) },
}));

// Mock the offline-queue lib so create/undo branching is observable without
// touching IndexedDB. The queue is namespaced per user, so both take a userId.
const enqueue = vi.fn();
const removeQueued = vi.fn();
vi.mock('@/lib/offline-queue', () => ({
  enqueue: (...a: unknown[]) => enqueue(...a),
  removeQueued: (...a: unknown[]) => removeQueued(...a),
}));

// useQuickAdd reads the authenticated user id; the hook only consumes `.user`.
vi.mock('@/hooks/useAuth', () => {
  const refreshUser = vi.fn();
  return { useAuth: () => ({ user: { id: 1 }, refreshUser }) };
});

import { useQuickAdd } from './useQuickAdd';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import type { Transaction } from '@/api/types';

const setOnline = (v: boolean): void => {
  Object.defineProperty(navigator, 'onLine', { configurable: true, value: v });
};
const input: CreateTransactionInput = { date: '2026-05-27', amount: 5, description: 'x', category_id: 1 };

beforeEach(() => {
  post.mockReset();
  del.mockReset();
  enqueue.mockReset();
  removeQueued.mockReset();
  setOnline(true);
});

describe('useQuickAdd', () => {
  test('online: POSTs and returns status=saved with the server transaction', async () => {
    post.mockResolvedValue({ id: 42 });
    const { result } = renderHook(() => useQuickAdd());
    let out!: Awaited<ReturnType<typeof result.current.create>>;
    await act(async () => {
      out = await result.current.create(input);
    });
    expect(post).toHaveBeenCalledWith('transactions', input);
    expect(enqueue).not.toHaveBeenCalled();
    expect(out).toEqual({ status: 'saved', transaction: { id: 42 } });
  });

  test('offline: enqueues with the user id instead of POSTing and returns status=queued', async () => {
    setOnline(false);
    enqueue.mockResolvedValue(7);
    const { result } = renderHook(() => useQuickAdd());
    let out!: Awaited<ReturnType<typeof result.current.create>>;
    await act(async () => {
      out = await result.current.create(input);
    });
    expect(post).not.toHaveBeenCalled();
    expect(enqueue).toHaveBeenCalledWith(1, input);
    expect(out).toEqual({ status: 'queued', queuedId: 7 });
  });

  test('undo(saved) soft-deletes the server row', async () => {
    del.mockResolvedValue(undefined);
    const { result } = renderHook(() => useQuickAdd());
    await act(async () => {
      await result.current.undo({ status: 'saved', transaction: { id: 9 } as Transaction });
    });
    expect(del).toHaveBeenCalledWith('transactions/9');
    expect(removeQueued).not.toHaveBeenCalled();
  });

  test('undo(queued) drops the queued row for the user, not the server', async () => {
    removeQueued.mockResolvedValue(undefined);
    const { result } = renderHook(() => useQuickAdd());
    await act(async () => {
      await result.current.undo({ status: 'queued', queuedId: 3 });
    });
    expect(removeQueued).toHaveBeenCalledWith(1, 3);
    expect(del).not.toHaveBeenCalled();
  });

  test('online create surfaces a thrown POST error (caller retries manually)', async () => {
    post.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useQuickAdd());
    await expect(
      act(async () => {
        await result.current.create(input);
      }),
    ).rejects.toThrow('boom');
    expect(enqueue).not.toHaveBeenCalled();
  });
});
