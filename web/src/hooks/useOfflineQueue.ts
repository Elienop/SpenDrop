import { useEffect, useState } from 'react';
import {
  getAllQueued,
  needsSignIn as queueNeedsSignIn,
  subscribe,
  type QueuedTransaction,
} from '@/lib/offline-queue';

/**
 * Live view of the offline write queue for one user. Re-reads whenever the
 * queue changes (enqueue / remove / drain all notify), so the /quick "saved on
 * this device" badge and the "Recently added" panel stay current without
 * polling. The queue is namespaced per user, so `userId` selects which user's
 * captures to show; pass `undefined` (e.g. before auth resolves) for an empty
 * view.
 */
export function useOfflineQueue(userId: number | undefined): {
  pending: QueuedTransaction[];
  count: number;
  /**
   * The server has rejected this identity, so these rows will NOT sync on
   * their own. Surfaced so the screen can ask for a sign-in instead of
   * promising a sync that cannot happen.
   */
  needsSignIn: boolean;
} {
  const [pending, setPending] = useState<QueuedTransaction[]>([]);
  const [held, setHeld] = useState(false);

  useEffect(() => {
    if (userId === undefined) {
      setPending([]);
      setHeld(false);
      return;
    }
    let active = true;
    const refresh = (): void => {
      // The hold changes through the same notify() channel as the rows, so it
      // stays in step with the count without a second subscription.
      if (active) setHeld(queueNeedsSignIn(userId));
      void getAllQueued(userId)
        .then((p) => {
          if (active) setPending(p);
        })
        .catch(() => {
          /* IndexedDB unavailable (e.g. private mode) — treat as empty. */
        });
    };
    refresh();
    const unsubscribe = subscribe(refresh);
    return () => {
      active = false;
      unsubscribe();
    };
  }, [userId]);

  return { pending, count: pending.length, needsSignIn: held };
}
