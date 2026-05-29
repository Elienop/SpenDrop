import { useEffect, useState } from 'react';
import {
  getAllQueued,
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
} {
  const [pending, setPending] = useState<QueuedTransaction[]>([]);

  useEffect(() => {
    if (userId === undefined) {
      setPending([]);
      return;
    }
    let active = true;
    const refresh = (): void => {
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

  return { pending, count: pending.length };
}
