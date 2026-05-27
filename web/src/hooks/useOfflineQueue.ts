import { useEffect, useState } from 'react';
import { countQueued, subscribe } from '@/lib/offline-queue';

/**
 * Live count of transactions waiting in the offline queue. Re-reads the count
 * whenever the queue changes (enqueue / remove / drain all notify), so the
 * /quick "waiting to sync" badge stays current without polling.
 */
export function useOfflineQueue(): { count: number } {
  const [count, setCount] = useState(0);

  useEffect(() => {
    let active = true;
    const refresh = (): void => {
      void countQueued()
        .then((n) => {
          if (active) setCount(n);
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

  return { count };
}
