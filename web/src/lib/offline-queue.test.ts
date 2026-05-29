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
  purgeQueue,
  drainQueue,
  subscribe,
} from './offline-queue';

const post = vi.mocked(api.post);

// The queue is namespaced per user; a single test user id is enough for the
// existing storage/drain coverage. The per-user isolation tests use two ids.
const U = 1;

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
  // Clear queue contents between tests (the in-memory fake DB persists). Purge
  // every user id any test touches so cross-user state never leaks.
  for (const uid of [U, 2]) {
    for (const item of await getAllQueued(uid)) await removeQueued(uid, item.id);
  }
});

afterEach(() => {
  setOnline(true);
});

describe('offline-queue — storage', () => {
  test('enqueue persists the payload with attempts=0 and a generated id', async () => {
    const id = await enqueue(U, payload({ description: 'lunch' }));
    expect(id).toBeGreaterThan(0);

    const all = await getAllQueued(U);
    expect(all).toHaveLength(1);
    expect(all[0]).toMatchObject({
      id,
      attempts: 0,
      payload: { description: 'lunch', amount: 12.5, category_id: 1 },
    });
    expect(await countQueued(U)).toBe(1);
  });

  test('removeQueued deletes the row', async () => {
    const id = await enqueue(U, payload());
    expect(await countQueued(U)).toBe(1);
    await removeQueued(U, id);
    expect(await countQueued(U)).toBe(0);
  });

  test('getAllQueued returns rows in FIFO (oldest-first) order', async () => {
    const first = await enqueue(U, payload({ description: 'first' }));
    const second = await enqueue(U, payload({ description: 'second' }));
    const all = await getAllQueued(U);
    expect(all.map((q) => q.id)).toEqual([first, second]);
    expect(all.map((q) => q.payload.description)).toEqual(['first', 'second']);
  });

  test('subscribe fires on enqueue and on removeQueued', async () => {
    const listener = vi.fn();
    const unsubscribe = subscribe(listener);
    const id = await enqueue(U, payload());
    expect(listener).toHaveBeenCalledTimes(1);
    await removeQueued(U, id);
    expect(listener).toHaveBeenCalledTimes(2);
    unsubscribe();
    await enqueue(U, payload());
    expect(listener).toHaveBeenCalledTimes(2); // no longer notified
  });
});

describe('offline-queue — per-user scoping', () => {
  test('enqueue/getAllQueued are isolated per user', async () => {
    await enqueue(1, payload({ description: 'alice' }));
    await enqueue(2, payload({ description: 'bob' }));

    const aliceRows = await getAllQueued(1);
    const bobRows = await getAllQueued(2);

    expect(aliceRows).toHaveLength(1);
    expect(aliceRows[0].payload.description).toBe('alice');
    expect(bobRows).toHaveLength(1);
    expect(bobRows[0].payload.description).toBe('bob');
  });

  test('drain posts only the draining user, leaving other users untouched', async () => {
    await enqueue(1, payload({ description: 'alice' }));
    await enqueue(2, payload({ description: 'bob' }));
    post.mockResolvedValue({} as never);

    const result = await drainQueue(1);

    expect(result.synced).toBe(1);
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0]).toEqual([
      'transactions',
      expect.objectContaining({ description: 'alice' }),
    ]);
    // Bob's row is untouched — namespace isolation, not a stamped-id refusal.
    expect(await countQueued(1)).toBe(0);
    expect(await countQueued(2)).toBe(1);
  });

  test('purgeQueue removes only the target user\'s database', async () => {
    await enqueue(1, payload({ description: 'alice' }));
    await enqueue(2, payload({ description: 'bob' }));

    await purgeQueue(1);

    expect(await countQueued(1)).toBe(0);
    expect(await countQueued(2)).toBe(1);
  });
});

describe('offline-queue — drain', () => {
  test('posts every queued row in order, removes them, and reports synced', async () => {
    await enqueue(U, payload({ description: 'a' }));
    await enqueue(U, payload({ description: 'b' }));
    post.mockResolvedValue({} as never);

    const result = await drainQueue(U);

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
    expect(await countQueued(U)).toBe(0);
  });

  test('is a no-op while offline (never posts)', async () => {
    await enqueue(U, payload());
    setOnline(false);

    const result = await drainQueue(U);

    expect(post).not.toHaveBeenCalled();
    expect(result.synced).toBe(0);
    expect(await countQueued(U)).toBe(1);
  });

  test('stops and keeps everything on a 401 (auth expired)', async () => {
    await enqueue(U, payload({ description: 'a' }));
    await enqueue(U, payload({ description: 'b' }));
    post.mockRejectedValue(new ApiError('Unauthorized', 401));

    const result = await drainQueue(U);

    expect(post).toHaveBeenCalledTimes(1); // broke after the first 401
    expect(result.synced).toBe(0);
    expect(await countQueued(U)).toBe(2); // nothing dropped
  });

  test('stops on a mid-drain network error, keeping the unsent rows', async () => {
    await enqueue(U, payload({ description: 'a' }));
    await enqueue(U, payload({ description: 'b' }));
    post
      .mockResolvedValueOnce({} as never)
      .mockRejectedValueOnce(new TypeError('Failed to fetch'));

    const result = await drainQueue(U);

    expect(result).toEqual({ synced: 1, remaining: 1 });
    expect(await countQueued(U)).toBe(1);
  });

  test('bumps attempts and continues past a server-rejected (non-401) row', async () => {
    const poison = await enqueue(U, payload({ description: 'poison' }));
    await enqueue(U, payload({ description: 'ok' }));
    post.mockImplementation((_path, body) => {
      if ((body as CreateTransactionInput).description === 'poison') {
        return Promise.reject(new ApiError('bad request', 400));
      }
      return Promise.resolve({} as never);
    });

    const result = await drainQueue(U);

    expect(result.synced).toBe(1); // the healthy row went through
    const remaining = await getAllQueued(U);
    expect(remaining).toHaveLength(1);
    expect(remaining[0].id).toBe(poison);
    expect(remaining[0].attempts).toBe(1); // bumped, not dropped
  });

  test('caps retries on a persistently-rejected row (MAX_ATTEMPTS=5)', async () => {
    await enqueue(U, payload({ description: 'poison' }));
    post.mockRejectedValue(new ApiError('bad request', 400));

    // 5 drains each bump attempts; the 6th sees attempts>=5 and skips it.
    for (let i = 0; i < 6; i++) await drainQueue(U);

    expect(post).toHaveBeenCalledTimes(5);
    expect(await countQueued(U)).toBe(1); // kept for visibility, never dropped
  });

  test('a drain requested mid-flight re-runs once after the first completes', async () => {
    await enqueue(U, payload({ description: 'a' }));
    await enqueue(U, payload({ description: 'b' }));

    // Hold the first POST open so a second drainQueue call lands while the
    // first is still in flight (setting rerunRequested).
    let releaseFirst!: () => void;
    const firstGate = new Promise<void>((resolve) => {
      releaseFirst = resolve;
    });
    let call = 0;
    post.mockImplementation(async () => {
      call++;
      if (call === 1) await firstGate;
      return {} as never;
    });

    const firstDrain = drainQueue(U);
    // Second call while draining: no-op now, but flags a re-run.
    const secondDrain = await drainQueue(U);
    expect(secondDrain.synced).toBe(0);

    releaseFirst();
    await firstDrain;
    // The re-run is fire-and-forget inside the finally; let it settle.
    await new Promise((r) => setTimeout(r, 5));

    // Both rows end up posted and the queue is empty.
    expect(await countQueued(U)).toBe(0);
  });
});

describe('offline-queue — openDB latch recovery', () => {
  test('retries after a failed open instead of latching the rejected promise', async () => {
    // A fresh user id so the cached promise map has no prior entry for it.
    const FRESH = 99;
    const realOpen = indexedDB.open.bind(indexedDB);

    // First open: hand back a request object that fires onerror asynchronously,
    // so the cached promise rejects. The fix must delete that cached promise so
    // the next call can open cleanly.
    const spy = vi
      .spyOn(indexedDB, 'open')
      .mockImplementationOnce(() => {
        const req = {
          onsuccess: null as (() => void) | null,
          onerror: null as (() => void) | null,
          onupgradeneeded: null as (() => void) | null,
          onblocked: null as (() => void) | null,
          error: new Error('open failed'),
          result: null,
        } as unknown as IDBOpenDBRequest;
        queueMicrotask(() => req.onerror?.(new Event('error') as never));
        return req;
      });

    await expect(countQueued(FRESH)).rejects.toBeTruthy();

    // Restore the real open and confirm the queue is usable again (the cached
    // rejected promise was cleared, not latched forever).
    spy.mockImplementation(((...args: Parameters<typeof realOpen>) =>
      realOpen(...args)) as typeof indexedDB.open);

    const id = await enqueue(FRESH, payload({ description: 'recovered' }));
    expect(id).toBeGreaterThan(0);
    expect(await countQueued(FRESH)).toBe(1);

    spy.mockRestore();
    // Clean up so the persistent fake DB doesn't leak into later runs.
    for (const item of await getAllQueued(FRESH)) {
      await removeQueued(FRESH, item.id);
    }
  });
});
