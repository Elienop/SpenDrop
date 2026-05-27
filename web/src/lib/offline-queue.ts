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
// on the next drain. Closing that fully needs a client idempotency key + a
// server-side dedup on the create path (a backend change, deferred). It is
// rare and never loses data; we accept it for v1.
//
// iOS PWAs have no Background Sync API, so replay is foreground-only: it runs
// on app launch and on the window 'online' event (see useOfflineSync).

const DB_NAME = 'spendrop-offline';
const DB_VERSION = 1;
const STORE = 'pending-transactions';

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
   * Wire-Edge DTO discipline — the payload is built by `toCreatePayload` at
   * capture time and stored verbatim, so replay re-sends the identical body.
   */
  payload: CreateTransactionInput;
  /** Capture time (ms epoch) — drives FIFO ordering. */
  queuedAt: number;
  /** Count of non-network replay rejections, for the poison-row cap. */
  attempts: number;
}

// --- IndexedDB plumbing -----------------------------------------------------

let dbPromise: Promise<IDBDatabase> | null = null;

function openDB(): Promise<IDBDatabase> {
  if (dbPromise) return dbPromise;
  dbPromise = new Promise<IDBDatabase>((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE)) {
        db.createObjectStore(STORE, { keyPath: 'id', autoIncrement: true });
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return dbPromise;
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
  payload: CreateTransactionInput,
): Promise<number> {
  const db = await openDB();
  const tx = db.transaction(STORE, 'readwrite');
  const id = (await reqDone(
    tx.objectStore(STORE).add({ payload, queuedAt: Date.now(), attempts: 0 }),
  )) as number;
  await txDone(tx);
  notify();
  return id;
}

export async function getAllQueued(): Promise<QueuedTransaction[]> {
  const db = await openDB();
  const tx = db.transaction(STORE, 'readonly');
  const all = (await reqDone(
    tx.objectStore(STORE).getAll(),
  )) as QueuedTransaction[];
  // FIFO: oldest capture first; id breaks ties within the same millisecond.
  return all.sort((a, b) => a.queuedAt - b.queuedAt || a.id - b.id);
}

export async function removeQueued(id: number): Promise<void> {
  const db = await openDB();
  const tx = db.transaction(STORE, 'readwrite');
  await reqDone(tx.objectStore(STORE).delete(id));
  await txDone(tx);
  notify();
}

export async function countQueued(): Promise<number> {
  const db = await openDB();
  const tx = db.transaction(STORE, 'readonly');
  return (await reqDone(tx.objectStore(STORE).count())) as number;
}

async function bumpAttempts(item: QueuedTransaction): Promise<void> {
  const db = await openDB();
  const tx = db.transaction(STORE, 'readwrite');
  await reqDone(
    tx.objectStore(STORE).put({ ...item, attempts: item.attempts + 1 }),
  );
  await txDone(tx);
}

async function safeCount(): Promise<number> {
  try {
    return await countQueued();
  } catch {
    return 0;
  }
}

function isOffline(): boolean {
  return typeof navigator !== 'undefined' && navigator.onLine === false;
}

// --- Replay -----------------------------------------------------------------

let draining = false;

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
export async function drainQueue(): Promise<DrainResult> {
  if (draining || isOffline()) {
    return { synced: 0, remaining: await safeCount() };
  }
  draining = true;
  let synced = 0;
  try {
    for (const item of await getAllQueued()) {
      if (isOffline()) break;
      if (item.attempts >= MAX_ATTEMPTS) continue; // poison row: kept, skipped
      try {
        await api.post<Transaction>('transactions', item.payload);
        await removeQueued(item.id);
        synced++;
      } catch (err) {
        if (err instanceof ApiError) {
          if (err.status === 401) break;
          // Record the rejection so the poison-row cap can eventually trip. If
          // even this write fails (IndexedDB unavailable mid-drain), stop the
          // run rather than spin — the next 'online'/launch retries.
          try {
            await bumpAttempts(item);
          } catch {
            break;
          }
          continue;
        }
        break; // network error → retry on the next 'online'
      }
    }
  } finally {
    draining = false;
    notify();
  }
  return { synced, remaining: await safeCount() };
}
