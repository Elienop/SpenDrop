import { api, ApiError } from '@/api/client';
import type { Transaction } from '@/api/types';
import type { CreateTransactionInput } from '@/hooks/useTransactions';

// IndexedDB-backed FIFO queue of transaction creates captured while the device
// was offline. The /quick capture screen enqueues a write ONLY when
// navigator.onLine === false (see useQuickAdd) — i.e. when the browser is
// certain the request never left the device. POST /api/transactions has NO
// content-hash dedup (that is import-only), so we cannot rely on the server to
// swallow a double-post; dup-safety lives in the enqueue gate.
//
// Delivery is at-least-once: the enqueue gate guarantees the row does not yet
// exist server-side, so the common path creates it exactly once. The one
// residual duplicate window is a successful POST whose `removeQueued` then
// fails (IndexedDB abort/quota mid-drain) — the row survives and is re-POSTed
// on the next drain. Closing that fully needs a client idempotency key AND a
// matching server-side dedup on the create path; both land together in Phase 3
// (POST /api/transactions has no content-hash dedup today, so a client key sent
// ahead of the server would be inert dead weight on the wire). It is rare and
// never loses data; we accept it for v1.
//
// iOS PWAs have no Background Sync API, so replay is foreground-only: it runs
// on app launch and on the window 'online' event (see useOfflineSync).
//
// User scoping: the queue is namespaced per authenticated user — each user's
// captures live in their own IndexedDB (`spendrop-offline-<userId>`). The
// server attributes a created row purely from the session cookie, so a queue
// captured by user A and replayed under user B's session would land in B's
// ledger. Per-user databases make a stale session structurally unable to even
// read another user's rows; logout additionally purges the leaving user's DB
// (see `purgeQueue` / useAuth). Rows queued under the legacy global
// 'spendrop-offline' name (pre-scoping) are intentionally NOT migrated: those
// rows carry no captured owner (that absence is the very bug being fixed), so
// auto-attributing them to whoever logs in first would reintroduce the
// misattribution. The realistic queue empties within seconds of reconnect, so
// the upgrade window is negligible.

const DB_PREFIX = 'spendrop-offline';
const DB_VERSION = 1;
const STORE = 'pending-transactions';

function dbName(userId: number): string {
  return `${DB_PREFIX}-${userId}`;
}

// Stop auto-retrying a row the server keeps rejecting (e.g. its category was
// deleted between capture and replay) so one poison row can't wedge the queue.
// The row is kept (never silently dropped) and still counts as pending — so a
// capped row keeps the "waiting to sync" badge lit until manually cleared. A
// review/retry/discard surface for stuck rows is deferred (Phase 3); the
// realistic offline flow (no signal → reconnect) never reaches the cap.
const MAX_ATTEMPTS = 5;

export interface QueuedTransaction {
  /** Auto-increment key; also the stable handle the Undo action removes by. */
  id: number;
  /**
   * Exact wire payload to POST on replay. `amount` is dollars, per the Money
   * Wire-Edge DTO discipline — built by `toCreatePayload` at capture time and
   * stored verbatim, so replay re-sends the identical body. For a non-base
   * currency this freezes the exchange rate at capture time (the dollar
   * `amount` is already converted; `original_amount`/`original_currency` carry
   * what the user typed); replay re-sends that captured-rate value, matching
   * the online entry form, which also converts at entry time.
   */
  payload: CreateTransactionInput;
  /** Capture time (ms epoch) — drives FIFO ordering. */
  queuedAt: number;
  /** Count of non-network replay rejections, for the poison-row cap. */
  attempts: number;
}

// --- IndexedDB plumbing -----------------------------------------------------

// One open handle per per-user DB name. The promise is cached only while it is
// pending or resolved; on error/blocked it is deleted so the next call retries
// a fresh open rather than latching a rejected promise forever (which would
// wedge the queue for the lifetime of the tab after one transient failure).
const dbPromises = new Map<string, Promise<IDBDatabase>>();

function openDB(userId: number): Promise<IDBDatabase> {
  const name = dbName(userId);
  const existing = dbPromises.get(name);
  if (existing) return existing;
  const p = new Promise<IDBDatabase>((resolve, reject) => {
    const req = indexedDB.open(name, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE)) {
        db.createObjectStore(STORE, { keyPath: 'id', autoIncrement: true });
      }
    };
    req.onsuccess = () => resolve(req.result);
    // Drop the cached promise so a later call can retry a clean open. onblocked
    // can fire without onerror (an upgrade held open by another tab) and would
    // otherwise leave a forever-pending promise.
    req.onerror = () => {
      dbPromises.delete(name);
      reject(req.error);
    };
    req.onblocked = () => {
      dbPromises.delete(name);
      reject(req.error);
    };
  });
  dbPromises.set(name, p);
  return p;
}

function reqDone<T>(req: IDBRequest<T>): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function txDone(tx: IDBTransaction): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
    tx.onabort = () => reject(tx.error);
  });
}

// --- Change subscription (live count for the UI badge) ----------------------

type Listener = () => void;
const listeners = new Set<Listener>();

export function subscribe(fn: Listener): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

function notify(): void {
  for (const fn of listeners) fn();
}

// --- Queue operations -------------------------------------------------------

export async function enqueue(
  userId: number,
  payload: CreateTransactionInput,
): Promise<number> {
  const db = await openDB(userId);
  const tx = db.transaction(STORE, 'readwrite');
  const id = (await reqDone(
    tx.objectStore(STORE).add({ payload, queuedAt: Date.now(), attempts: 0 }),
  )) as number;
  await txDone(tx);
  notify();
  return id;
}

export async function getAllQueued(
  userId: number,
): Promise<QueuedTransaction[]> {
  const db = await openDB(userId);
  const tx = db.transaction(STORE, 'readonly');
  const all = (await reqDone(
    tx.objectStore(STORE).getAll(),
  )) as QueuedTransaction[];
  // FIFO: oldest capture first; id breaks ties within the same millisecond.
  return all.sort((a, b) => a.queuedAt - b.queuedAt || a.id - b.id);
}

export async function removeQueued(userId: number, id: number): Promise<void> {
  const db = await openDB(userId);
  const tx = db.transaction(STORE, 'readwrite');
  await reqDone(tx.objectStore(STORE).delete(id));
  await txDone(tx);
  notify();
}

export async function countQueued(userId: number): Promise<number> {
  const db = await openDB(userId);
  const tx = db.transaction(STORE, 'readonly');
  return (await reqDone(tx.objectStore(STORE).count())) as number;
}

/**
 * Delete a user's offline queue database outright. Called on logout so a
 * different account on the same device can never replay the leaving user's
 * captures. Best-effort: a delete blocked by another open tab resolves rather
 * than hanging — the per-user namespace already prevents cross-account replay.
 */
export async function purgeQueue(userId: number): Promise<void> {
  const name = dbName(userId);
  const open = dbPromises.get(name);
  dbPromises.delete(name);
  // Close this tab's open handle first; an open connection makes
  // deleteDatabase fire `onblocked` (and hang) instead of completing.
  if (open) {
    try {
      (await open).close();
    } catch {
      // Already closed / failed to open — nothing to release.
    }
  }
  await new Promise<void>((resolve) => {
    const req = indexedDB.deleteDatabase(name);
    req.onsuccess = () => resolve();
    req.onerror = () => resolve();
    req.onblocked = () => resolve();
  });
  notify();
}

async function bumpAttempts(
  userId: number,
  item: QueuedTransaction,
): Promise<void> {
  const db = await openDB(userId);
  const tx = db.transaction(STORE, 'readwrite');
  await reqDone(
    tx.objectStore(STORE).put({ ...item, attempts: item.attempts + 1 }),
  );
  await txDone(tx);
}

async function safeCount(userId: number): Promise<number> {
  try {
    return await countQueued(userId);
  } catch {
    return 0;
  }
}

function isOffline(): boolean {
  return typeof navigator !== 'undefined' && navigator.onLine === false;
}

// --- Replay -----------------------------------------------------------------

// Drain in-flight / rerun state is keyed PER USER, not module-global: on a
// shared device user A's drain must not suppress or mis-target user B's. A
// userId in `draining` has a drain currently running; a userId in
// `rerunRequested` had a trigger arrive mid-drain and will be re-drained once —
// for that SAME user — after its active drain settles. Both are capped to a
// single re-run per user (a fresh external trigger re-sets it).
const draining = new Set<number>();
const rerunRequested = new Set<number>();

export interface DrainResult {
  /** Rows successfully posted to the server and removed from the queue. */
  synced: number;
  /** Rows still pending after this run (offline-remaining + poison rows). */
  remaining: number;
}

/**
 * Replay queued writes in FIFO order. Safe to call concurrently (a second call
 * while one is in flight is a no-op) and when empty/offline (no-ops).
 *
 * Per-row outcome:
 *  - 2xx            → removed from the queue (counted as synced).
 *  - 401            → stop the whole run, keep everything; a re-login + the
 *                     next 'online'/launch drains them (no loss).
 *  - other ApiError → server rejected THIS row; bump attempts and move on so
 *                     it can't head-of-line block healthy rows. After
 *                     MAX_ATTEMPTS the row is left in place but skipped.
 *  - network reject → connectivity dropped mid-run; stop and keep the rest for
 *                     the next 'online' event.
 */
export async function drainQueue(userId: number): Promise<DrainResult> {
  if (isOffline()) {
    return { synced: 0, remaining: await safeCount(userId) };
  }
  if (draining.has(userId)) {
    // A drain for THIS user is already in flight; ask it to re-run once it
    // settles so this trigger is not silently dropped. A different user's drain
    // is unaffected — its own entry gates it independently.
    rerunRequested.add(userId);
    return { synced: 0, remaining: await safeCount(userId) };
  }
  draining.add(userId);
  let synced = 0;
  try {
    for (const item of await getAllQueued(userId)) {
      if (isOffline()) break;
      if (item.attempts >= MAX_ATTEMPTS) continue; // poison row: kept, skipped
      try {
        await api.post<Transaction>('transactions', item.payload);
        await removeQueued(userId, item.id);
        synced++;
      } catch (err) {
        if (err instanceof ApiError) {
          if (err.status === 401) break;
          // Record the rejection so the poison-row cap can eventually trip. If
          // even this write fails (IndexedDB unavailable mid-drain), stop the
          // run rather than spin — the next 'online'/launch retries.
          try {
            await bumpAttempts(userId, item);
          } catch {
            break;
          }
          continue;
        }
        break; // network error → retry on the next 'online'
      }
    }
    // Compute the result while still holding the guard, so a concurrent
    // drainQueue can't slip in between releasing it and this final read.
    return { synced, remaining: await safeCount(userId) };
  } finally {
    draining.delete(userId);
    notify();
    // If a trigger arrived mid-drain for THIS user and we are still online, run
    // once more so rows skipped by a brief offline blip aren't stranded.
    // Consume this user's flag before recursing so it stays a single-shot
    // re-run, and re-drain the SAME user it was requested for.
    const rerun = rerunRequested.delete(userId);
    if (rerun && !isOffline()) {
      void drainQueue(userId);
    }
  }
}
