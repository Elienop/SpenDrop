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

import { api, ApiError, NetworkError } from '@/api/client';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import {
  enqueue,
  getAllQueued,
  removeQueued,
  countQueued,
  purgeQueue,
  drainQueue,
  subscribe,
  needsSignIn,
  markNeedsSignIn,
  clearNeedsSignIn,
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
    clearNeedsSignIn(uid);
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

  test('an in-flight drain for user A does not suppress a drain for user B', async () => {
    await enqueue(1, payload({ description: 'alice' }));
    await enqueue(2, payload({ description: 'bob' }));

    // Hold ONLY alice's POST open. With per-user drain state, bob's drain must
    // run to completion meanwhile; a module-global flag would make bob's call a
    // no-op (its row stranded until the next trigger).
    let releaseAlice!: () => void;
    const aliceGate = new Promise<void>((resolve) => {
      releaseAlice = resolve;
    });
    post.mockImplementation(async (_path, body) => {
      if ((body as CreateTransactionInput).description === 'alice') {
        await aliceGate;
      }
      return {} as never;
    });

    const aliceDrain = drainQueue(1); // in flight, parked on aliceGate
    const bobResult = await drainQueue(2); // must NOT be gated by alice

    expect(bobResult.synced).toBe(1);
    expect(await countQueued(2)).toBe(0); // bob synced independently

    releaseAlice();
    const aliceResult = await aliceDrain;
    expect(aliceResult.synced).toBe(1);
    expect(await countQueued(1)).toBe(0);
  });

  test('a mid-flight rerun re-drains the SAME user it was requested for', async () => {
    await enqueue(1, payload({ description: 'alice-1' }));
    await enqueue(2, payload({ description: 'bob-1' }));

    // Park alice's first POST so a second drainQueue(1) lands mid-flight and
    // flags a rerun for user 1. Enqueue a second alice row during the hold so
    // the rerun has something to pick up — proving the rerun targets user 1,
    // not whoever happened to trigger it.
    let releaseAlice!: () => void;
    const aliceGate = new Promise<void>((resolve) => {
      releaseAlice = resolve;
    });
    let aliceCalls = 0;
    post.mockImplementation(async (_path, body) => {
      if ((body as CreateTransactionInput).description?.startsWith('alice')) {
        aliceCalls++;
        if (aliceCalls === 1) await aliceGate;
      }
      return {} as never;
    });

    const aliceDrain = drainQueue(1); // parked on the first alice POST
    const reentrant = await drainQueue(1); // mid-flight: flags rerun for user 1
    expect(reentrant.synced).toBe(0);
    await enqueue(1, payload({ description: 'alice-2' })); // arrives mid-drain

    releaseAlice();
    await aliceDrain;
    await new Promise((r) => setTimeout(r, 10)); // let the fire-and-forget rerun settle

    // The rerun re-drained user 1 (picking up alice-2), not user 2.
    expect(await countQueued(1)).toBe(0);
    expect(await countQueued(2)).toBe(1); // bob never touched
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

describe('offline-queue — transport failures are not server rejections', () => {
  // The drain treats an ApiError as "the server looked at THIS row and said
  // no" and counts it toward MAX_ATTEMPTS. A request that never got an answer
  // must never be counted that way, or a real purchase ends up stranded behind
  // a permanent "Not synced" badge for a reason that was only ever a flaky
  // radio.
  test('a timed-out POST leaves attempts at 0 and keeps the row', async () => {
    const id = await enqueue(U, payload({ description: 'lunch' }));
    post.mockRejectedValue(
      new NetworkError('The server took too long to answer', 'timeout'),
    );

    const result = await drainQueue(U);

    expect(result).toEqual({ synced: 0, remaining: 1 });
    const rows = await getAllQueued(U);
    expect(rows[0].id).toBe(id);
    expect(rows[0].attempts).toBe(0);
  });

  test('repeated timeouts never trip the poison-row cap', async () => {
    await enqueue(U, payload({ description: 'lunch' }));
    post.mockRejectedValue(
      new NetworkError('The server took too long to answer', 'timeout'),
    );

    // More drains than MAX_ATTEMPTS: a row capped by mistake would stop being
    // POSTed at all, so the call count is the tell.
    for (let i = 0; i < 7; i++) await drainQueue(U);

    expect(post).toHaveBeenCalledTimes(7);
    const rows = await getAllQueued(U);
    expect(rows[0].attempts).toBe(0);
  });

  test('an unreachable server stops the run and keeps the later rows', async () => {
    await enqueue(U, payload({ description: 'a' }));
    await enqueue(U, payload({ description: 'b' }));
    post
      .mockResolvedValueOnce({} as never)
      .mockRejectedValueOnce(
        new NetworkError('Could not reach the server', 'unreachable'),
      );

    const result = await drainQueue(U);

    expect(result).toEqual({ synced: 1, remaining: 1 });
    expect((await getAllQueued(U))[0].attempts).toBe(0);
  });
});

// The queue's promise to replay the payload verbatim is what makes a client
// idempotency key work at all: the key is minted once at capture, and every
// re-POST of that row — after a lost response, or from a second tab — carries
// it unchanged so the server can answer with the row it already created.
describe('offline-queue — the idempotency key survives replay', () => {
  test('re-sends the stored key byte-identically after a lost response', async () => {
    await enqueue(U, payload({ description: 'lunch', client_key: 'key-abc' }));

    // The key lives inside the stored payload, so it survives a reload.
    const [stored] = await getAllQueued(U);
    expect(stored.payload.client_key).toBe('key-abc');

    // The server never answered the first attempt, so the row is kept — it may
    // or may not have committed, which is exactly the case the key covers.
    post.mockRejectedValueOnce(
      new NetworkError('Could not reach the server', 'unreachable'),
    );
    expect((await drainQueue(U)).synced).toBe(0);
    expect(await countQueued(U)).toBe(1);

    post.mockResolvedValueOnce({} as never);
    expect((await drainQueue(U)).synced).toBe(1);

    expect(post).toHaveBeenCalledTimes(2);
    const firstBody = post.mock.calls[0][1] as CreateTransactionInput;
    const secondBody = post.mock.calls[1][1] as CreateTransactionInput;
    expect(secondBody.client_key).toBe('key-abc');
    // Deliberately NOT deduped client-side: sending the same key twice is the
    // mechanism. What must not change is the body.
    expect(JSON.stringify(secondBody)).toBe(JSON.stringify(firstBody));
  });

  test('a row queued before client_key existed still drains, keylessly', async () => {
    // Rows captured by an older build have no key. They must replay exactly as
    // they always did rather than crash the drain and strand every row behind
    // them.
    await enqueue(U, payload({ description: 'legacy' }));
    post.mockResolvedValue({} as never);

    const result = await drainQueue(U);

    expect(result).toEqual({ synced: 1, remaining: 0 });
    expect(post.mock.calls[0][1]).not.toHaveProperty('client_key');
  });
});

describe('offline-queue — needs-sign-in hold', () => {
  test('a 401 holds the queue for sign-in instead of dropping rows', async () => {
    await enqueue(U, payload({ description: 'a' }));
    await enqueue(U, payload({ description: 'b' }));
    post.mockRejectedValue(new ApiError('Unauthorized', 401));

    await drainQueue(U);

    expect(needsSignIn(U)).toBe(true);
    expect(await countQueued(U)).toBe(2); // held, never discarded
  });

  test('a held queue does not POST again until sign-in clears it', async () => {
    await enqueue(U, payload({ description: 'a' }));
    markNeedsSignIn(U);
    post.mockResolvedValue({} as never);

    const result = await drainQueue(U);

    // Replaying under a session the server has already rejected is exactly how
    // a capture ends up filed under the wrong household member.
    expect(post).not.toHaveBeenCalled();
    expect(result).toEqual({ synced: 0, remaining: 1 });
    expect(await countQueued(U)).toBe(1);
  });

  test('clearing the hold lets the same rows drain untouched', async () => {
    await enqueue(U, payload({ description: 'a' }));
    markNeedsSignIn(U);
    post.mockResolvedValue({} as never);

    await drainQueue(U);
    clearNeedsSignIn(U);
    const result = await drainQueue(U);

    expect(result.synced).toBe(1);
    expect(await countQueued(U)).toBe(0);
  });

  test('the hold is per user — one member signing out cannot block another', async () => {
    markNeedsSignIn(1);

    expect(needsSignIn(1)).toBe(true);
    expect(needsSignIn(2)).toBe(false);
  });

  test('a successful drain leaves the hold clear', async () => {
    await enqueue(U, payload());
    post.mockResolvedValue({} as never);

    await drainQueue(U);

    expect(needsSignIn(U)).toBe(false);
  });

  test('purgeQueue (logout) clears the hold along with the rows', async () => {
    await enqueue(U, payload());
    markNeedsSignIn(U);

    await purgeQueue(U);

    expect(needsSignIn(U)).toBe(false);
    expect(await countQueued(U)).toBe(0);
  });

  test('marking / clearing the hold notifies subscribers so the UI updates', () => {
    const listener = vi.fn();
    const unsubscribe = subscribe(listener);
    try {
      markNeedsSignIn(U);
      expect(listener).toHaveBeenCalledTimes(1);
      clearNeedsSignIn(U);
      expect(listener).toHaveBeenCalledTimes(2);
      // Clearing an already-clear hold is not a change; do not churn the UI.
      clearNeedsSignIn(U);
      expect(listener).toHaveBeenCalledTimes(2);
    } finally {
      unsubscribe();
    }
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
