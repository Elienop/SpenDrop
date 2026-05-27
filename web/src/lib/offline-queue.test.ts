// happy-dom's IndexedDB is incomplete; this provides a spec-compliant one.
// Scoped to this suite (not the global setup) so unrelated tests don't take a
// hard dependency on the polyfill loading.
import 'fake-indexeddb/auto';
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock the API client's `api.post` while keeping the real ApiError class so the
// drain's `err instanceof ApiError` discrimination is exercised faithfully.
vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/client')>();
  return { ...actual, api: { post: vi.fn() } };
});

import { api, ApiError } from '@/api/client';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import {
  enqueue,
  getAllQueued,
  removeQueued,
  countQueued,
  drainQueue,
  subscribe,
} from './offline-queue';

const post = vi.mocked(api.post);

function setOnline(value: boolean): void {
  Object.defineProperty(navigator, 'onLine', { configurable: true, value });
}

function payload(over: Partial<CreateTransactionInput> = {}): CreateTransactionInput {
  return {
    date: '2026-05-27',
    amount: 12.5,
    description: 'coffee',
    category_id: 1,
    ...over,
  };
}

beforeEach(async () => {
  post.mockReset();
  setOnline(true);
  // Clear queue contents between tests (the in-memory fake DB persists).
  for (const item of await getAllQueued()) await removeQueued(item.id);
});

afterEach(() => {
  setOnline(true);
});

describe('offline-queue — storage', () => {
  test('enqueue persists the payload with attempts=0 and a generated id', async () => {
    const id = await enqueue(payload({ description: 'lunch' }));
    expect(id).toBeGreaterThan(0);

    const all = await getAllQueued();
    expect(all).toHaveLength(1);
    expect(all[0]).toMatchObject({
      id,
      attempts: 0,
      payload: { description: 'lunch', amount: 12.5, category_id: 1 },
    });
    expect(await countQueued()).toBe(1);
  });

  test('removeQueued deletes the row', async () => {
    const id = await enqueue(payload());
    expect(await countQueued()).toBe(1);
    await removeQueued(id);
    expect(await countQueued()).toBe(0);
  });

  test('getAllQueued returns rows in FIFO (oldest-first) order', async () => {
    const first = await enqueue(payload({ description: 'first' }));
    const second = await enqueue(payload({ description: 'second' }));
    const all = await getAllQueued();
    expect(all.map((q) => q.id)).toEqual([first, second]);
    expect(all.map((q) => q.payload.description)).toEqual(['first', 'second']);
  });

  test('subscribe fires on enqueue and on removeQueued', async () => {
    const listener = vi.fn();
    const unsubscribe = subscribe(listener);
    const id = await enqueue(payload());
    expect(listener).toHaveBeenCalledTimes(1);
    await removeQueued(id);
    expect(listener).toHaveBeenCalledTimes(2);
    unsubscribe();
    await enqueue(payload());
    expect(listener).toHaveBeenCalledTimes(2); // no longer notified
  });
});

describe('offline-queue — drain', () => {
  test('posts every queued row in order, removes them, and reports synced', async () => {
    await enqueue(payload({ description: 'a' }));
    await enqueue(payload({ description: 'b' }));
    post.mockResolvedValue({} as never);

    const result = await drainQueue();

    expect(result).toEqual({ synced: 2, remaining: 0 });
    expect(post).toHaveBeenCalledTimes(2);
    expect(post.mock.calls[0]).toEqual([
      'transactions',
      expect.objectContaining({ description: 'a' }),
    ]);
    expect(post.mock.calls[1]).toEqual([
      'transactions',
      expect.objectContaining({ description: 'b' }),
    ]);
    expect(await countQueued()).toBe(0);
  });

  test('is a no-op while offline (never posts)', async () => {
    await enqueue(payload());
    setOnline(false);

    const result = await drainQueue();

    expect(post).not.toHaveBeenCalled();
    expect(result.synced).toBe(0);
    expect(await countQueued()).toBe(1);
  });

  test('stops and keeps everything on a 401 (auth expired)', async () => {
    await enqueue(payload({ description: 'a' }));
    await enqueue(payload({ description: 'b' }));
    post.mockRejectedValue(new ApiError('Unauthorized', 401));

    const result = await drainQueue();

    expect(post).toHaveBeenCalledTimes(1); // broke after the first 401
    expect(result.synced).toBe(0);
    expect(await countQueued()).toBe(2); // nothing dropped
  });

  test('stops on a mid-drain network error, keeping the unsent rows', async () => {
    await enqueue(payload({ description: 'a' }));
    await enqueue(payload({ description: 'b' }));
    post
      .mockResolvedValueOnce({} as never)
      .mockRejectedValueOnce(new TypeError('Failed to fetch'));

    const result = await drainQueue();

    expect(result).toEqual({ synced: 1, remaining: 1 });
    expect(await countQueued()).toBe(1);
  });

  test('bumps attempts and continues past a server-rejected (non-401) row', async () => {
    const poison = await enqueue(payload({ description: 'poison' }));
    await enqueue(payload({ description: 'ok' }));
    post.mockImplementation((_path, body) => {
      if ((body as CreateTransactionInput).description === 'poison') {
        return Promise.reject(new ApiError('bad request', 400));
      }
      return Promise.resolve({} as never);
    });

    const result = await drainQueue();

    expect(result.synced).toBe(1); // the healthy row went through
    const remaining = await getAllQueued();
    expect(remaining).toHaveLength(1);
    expect(remaining[0].id).toBe(poison);
    expect(remaining[0].attempts).toBe(1); // bumped, not dropped
  });

  test('caps retries on a persistently-rejected row (MAX_ATTEMPTS=5)', async () => {
    await enqueue(payload({ description: 'poison' }));
    post.mockRejectedValue(new ApiError('bad request', 400));

    // 5 drains each bump attempts; the 6th sees attempts>=5 and skips it.
    for (let i = 0; i < 6; i++) await drainQueue();

    expect(post).toHaveBeenCalledTimes(5);
    expect(await countQueued()).toBe(1); // kept for visibility, never dropped
  });
});
