import { useState, useEffect, useCallback, useRef } from 'react';
import { api } from '../api/client';

/**
 * Shape of GET /api/transactions/deleted — only `total` is read here,
 * the rest is realistic noise. The backend handler is admin-only, so
 * the hook only fires when `enabled` is true.
 */
interface DeletedListResponse {
  total: number;
}

/**
 * Custom event the rest of the app dispatches when trash contents
 * change (soft-delete, restore, purge). Keeps this hook decoupled
 * from the components that mutate trash state — they fire-and-forget
 * a `window.dispatchEvent(new Event(TRASH_CHANGED_EVENT))` after a
 * successful mutation, and the sidebar badge re-renders.
 */
export const TRASH_CHANGED_EVENT = 'spendrop-trash-changed';

/**
 * Subscribe to the count of tombstoned transactions, polled on
 * mount + window focus + `TRASH_CHANGED_EVENT`. Used by the sidebar
 * to badge the "Trash" link.
 *
 * Gated on `enabled` (typically `isAdmin(user)`) so non-admin
 * sessions don't issue a 403 the network panel would surface as a
 * spurious red flag.
 *
 * Reuses the existing `GET /api/transactions/deleted` endpoint with
 * `per_page=1` — the response envelope already includes a `total`
 * that mirrors `CountDeletedTransactions`, so no new backend route
 * is needed.
 */
export function useTrashCount(enabled: boolean) {
  const [count, setCount] = useState(0);
  const [loading, setLoading] = useState(false);
  // Monotonic generation counter for out-of-order response handling.
  // Three rapid mutations fire three overlapping fetches; whichever
  // resolves last would otherwise win, regardless of issue order. We
  // increment per refetch and ignore any response whose generation
  // doesn't match the current value.
  const genRef = useRef(0);

  const refetch = useCallback(async () => {
    if (!enabled) return;
    const gen = ++genRef.current;
    setLoading(true);
    try {
      // per_page=1 minimizes payload — we only read `total`.
      const res = await api.get<DeletedListResponse>(
        'transactions/deleted?page=1&per_page=1',
      );
      if (gen !== genRef.current) return; // stale, newer fetch in flight
      setCount(res.total ?? 0);
    } catch {
      // Silent: the badge is non-critical. A failed fetch just leaves
      // the count at its previous value (or 0 on first load); the
      // Trash page itself surfaces real errors when the user opens it.
      if (gen !== genRef.current) return; // ignore stale errors too
    } finally {
      if (gen === genRef.current) setLoading(false);
    }
  }, [enabled]);

  useEffect(() => {
    if (!enabled) {
      // Flip back to 0 so a previous admin session's lingering count
      // doesn't bleed into a non-admin re-mount.
      setCount(0);
      return;
    }
    void refetch();
    function onFocus() {
      void refetch();
    }
    function onTrashChanged() {
      void refetch();
    }
    window.addEventListener('focus', onFocus);
    window.addEventListener(TRASH_CHANGED_EVENT, onTrashChanged);
    return () => {
      window.removeEventListener('focus', onFocus);
      window.removeEventListener(TRASH_CHANGED_EVENT, onTrashChanged);
    };
  }, [enabled, refetch]);

  return { count, loading, refetch };
}
