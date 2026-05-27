import { useEffect, useState } from 'react';
import {
  getAllQueued,
  subscribe,
  type QueuedTransaction,
} from '@/lib/offline-queue';

/**
 * Live view of the offline write queue. Re-reads whenever the queue changes
 * (enqueue / remove / drain all notify), so the /quick "saved on this device"
 * badge and the "Recently added" panel stay current without polling.
 */
export function useOfflineQueue(): {
  pending: QueuedTransaction[];
  count: number;
} {
  const [pending, setPending] = useState<QueuedTransaction[]>([]);

  useEffect(() => {
    let active = true;
    const refresh = (): void => {
      void getAllQueued()
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
  }, []);

  return { pending, count: pending.length };
}
