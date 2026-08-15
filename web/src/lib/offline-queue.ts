import { api, ApiError } from '@/api/client';
import type { Transaction } from '@/api/types';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import { QUEUE_NEEDS_SIGN_IN_PREFIX } from '@/lib/storage-keys';

// IndexedDB-backed FIFO queue of transaction creates captured while the device
// was offline. The /quick capture screen enqueues a write ONLY when
// navigator.onLine === false (see useQuickAdd) — i.e. when the browser is
// certain the request never left the device.
//
// What the server does with a duplicate create, precisely (handleCreateTransaction
// / contentHashForManualEntry in internal/api/transaction_handlers.go): a manual
// create DOES compute a content hash and DOES look for an existing row with the
// same identity — but a collision is deliberately not an error, because real
// household data contains legitimate duplicates (two identical coffees the same
// day). The colliding row is still created; it just stores content_hash = NULL
// so the first row stays the canonical anchor for import dedupe. So the server
// DETECTS a double-post but does not SWALLOW it. Dup-safety therefore still
// lives in the enqueue gate — an ambiguous online failure must not be queued.
//
// Delivery is at-least-once: the enqueue gate guarantees the row does not yet
// exist server-side, so the common path creates it exactly once. Replays that
// the gate cannot prevent — a successful POST whose `removeQueued` then fails
// (IndexedDB abort/quota mid-drain), or two tabs draining the same row — are
// covered instead by the `client_key` inside the stored payload: it was minted
// once at capture, the payload is replayed verbatim, so every re-POST of that
// row carries the same key and the server answers with the row it already
// created. Do NOT add client-side dedup on top of this; sending the same key
// again is the mechanism, not a bug. Rows captured before `client_key` existed
// have none and fall back to the old at-least-once behavior.
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
   * stored verbatim, so replay re-sends the identical body, `client_key`
   * included (that verbatim replay is what makes the key work). For a non-base
   * currency the stored `amount` is the value this device converted at capture
   * time, but the SERVER DOES NOT USE IT: resolveCurrency ignores the wire
   * `amount` on the foreign branch and re-divides `original_amount` by the rate
   * that is current when the queued row finally POSTs. A queued foreign row is
   * therefore priced — and its booked_rate recorded — at DRAIN time, not at
   * capture time. (This comment used to claim the opposite; it was verified
   * false against transaction_handlers.go's foreign branch during B10.)
   */
  payload: CreateTransactionInput;
  /** Capture time (ms epoch) — drives FIFO ordering. */
  queuedAt: number;
  /** Count of non-network replay rejections, for the poison-row cap. */
  attempts: number;
}

// --- IndexedDB plumbing -----------------------------------------------------

/**
 * The reason to reject an IndexedDB failure with.
 *
 * `IDBRequest.error` / `IDBTransaction.error` are typed `DOMException | null`,
 * and `null` is not a usable rejection reason (typescript:S6671) — a caller
 * that does `catch (err) { err.message }` gets a TypeError instead of the
 * failure. The browser's own `DOMException` IS an Error (lib.dom declares
 * `interface DOMException extends Error`), so it is handed back untouched:
 * re-wrapping it would discard `.name` ('QuotaExceededError', 'AbortError',
 * 'VersionError', …), which is the only part of an IndexedDB failure worth
 * branching on. A fresh Error is constructed ONLY for the null cases the spec
 * allows: an open request blocked by another tab (nothing has failed yet, so
 * `error` is null) and a transaction ended by an explicit `abort()`.
 */
function idbFailure(error: DOMException | null, context: string): Error {
  return error ?? new Error(context);
}

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
      reject(idbFailure(req.error, `IndexedDB open failed: ${name}`));
    };
    req.onblocked = () => {
      dbPromises.delete(name);
      reject(idbFailure(req.error, `IndexedDB open blocked: ${name}`));
    };
  });
  dbPromises.set(name, p);
  return p;
}

function reqDone<T>(req: IDBRequest<T>): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () =>
      reject(idbFailure(req.error, 'IndexedDB request failed'));
  });
}

function txDone(tx: IDBTransaction): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () =>
      reject(idbFailure(tx.error, 'IndexedDB transaction failed'));
    tx.onabort = () =>
      reject(idbFailure(tx.error, 'IndexedDB transaction aborted'));
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

// --- Needs-sign-in hold -----------------------------------------------------

/**
 * A user's queue is HELD when the server has rejected that identity (a real
 * 401), rather than merely being unreachable. Held rows are never discarded
 * and never replayed: replaying them would post captures made by one household
 * member through whatever session the device now has, which is exactly the
 * misattribution the per-user namespacing exists to prevent. The hold is
 * released by a server-confirmed sign-in as that same user (see useAuth).
 *
 * Stored in localStorage rather than in the row itself because it is a
 * property of the identity, not of any one capture — and because the /quick
 * screen must be able to read it without opening IndexedDB.
 */
function holdKey(userId: number): string {
  return `${QUEUE_NEEDS_SIGN_IN_PREFIX}${userId}`;
}

export function needsSignIn(userId: number): boolean {
  try {
    return localStorage.getItem(holdKey(userId)) === '1';
  } catch {
    // Storage disabled (private mode / blocked cookies): fail OPEN. A device
    // that cannot remember the hold must not permanently freeze the queue.
    return false;
  }
}

export function markNeedsSignIn(userId: number): void {
  if (needsSignIn(userId)) return;
  try {
    localStorage.setItem(holdKey(userId), '1');
  } catch {
    return; // Nothing persisted, so nothing changed — do not notify.
  }
  notify();
}

export function clearNeedsSignIn(userId: number): void {
  if (!needsSignIn(userId)) return;
  try {
    localStorage.removeItem(holdKey(userId));
  } catch {
    return;
  }
  notify();
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
  // The rows are gone, so the hold has nothing left to protect; leaving it set
  // would silently freeze this user's next set of captures after they sign
  // back in on this device.
  clearNeedsSignIn(userId);
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
 *  - 401            → the server rejected this identity. Stop the whole run,
 *                     keep everything, and HOLD the queue for sign-in so it is
 *                     not replayed under someone else's session (no loss).
 *  - other ApiError → server rejected THIS row; bump attempts and move on so
 *                     it can't head-of-line block healthy rows. After
 *                     MAX_ATTEMPTS the row is left in place but skipped.
 *  - NetworkError   → the server never answered (offline / unreachable /
 *    or other        timed out). This is NOT the row's fault, so attempts is
 *    non-ApiError    deliberately NOT bumped: counting it would let a flaky
 *                    radio burn the poison-row cap and strand a real purchase
 *                    forever. Stop and keep the rest for the next 'online'.
 */
export async function drainQueue(userId: number): Promise<DrainResult> {
  if (isOffline()) {
    return { synced: 0, remaining: await safeCount(userId) };
  }
  if (needsSignIn(userId)) {
    // Held: the server has rejected this identity and no one has signed back
    // in as them yet. The rows stay exactly where they are.
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
        // `instanceof ApiError` is the ONLY thing that means "the server saw
        // this row and refused it". Transport failures are a different class
        // (see api/client.ts NetworkError) and fall through to the break below
        // without touching `attempts`.
        if (err instanceof ApiError) {
          if (err.status === 401) {
            markNeedsSignIn(userId);
            break;
          }
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
