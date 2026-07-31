import { useEffect } from 'react';
import { toast } from 'sonner';
import { drainQueue } from '@/lib/offline-queue';
import { useAuth } from '@/hooks/useAuth';

/**
 * App-wide replay trigger for the offline write queue. Mount once (App.tsx) so
 * queued expenses sync whenever the app is open and connectivity returns — not
 * only while /quick is mounted. iOS PWAs have no Background Sync API, so this
 * foreground replay (on app launch + the window 'online' event) is the
 * reliable mechanism.
 *
 * Replay is gated on a SERVER-CONFIRMED user, not merely on a user object.
 * The server attributes a created row purely from the session cookie, so
 * posting captures under an identity the server has not confirmed (see
 * useAuth's `unverified`) is exactly how a row lands in the wrong household
 * member's ledger. Confirmation re-runs this effect and drains everything that
 * was waiting — including rows that 401'd while the session was expired, once
 * signing back in releases their hold.
 *
 * The reconnection listener is attached unconditionally, never behind the
 * auth check: a listener that only exists while things are already working is
 * no use for recovering when they are not.
 */
export function useOfflineSync(): void {
  const { user, unverified } = useAuth();
  const userId = user?.id;
  const canSync = userId !== undefined && !unverified;

  useEffect(() => {
    const run = (): void => {
      if (!canSync || userId === undefined) return;
      void drainQueue(userId)
        .then(({ synced }) => {
          if (synced > 0) {
            toast.success(
              `Synced ${synced} offline ${synced === 1 ? 'entry' : 'entries'}`,
              // Stable id so back-to-back drains (launch + a quick reconnect)
              // collapse into one toast instead of stacking.
              { id: 'offline-sync' },
            );
          }
        })
        .catch(() => {
          /* Transient IndexedDB/network error — the next trigger retries. */
        });
    };

    run(); // launch / login drain (no-ops if empty, offline, or unconfirmed)
    window.addEventListener('online', run);
    return () => window.removeEventListener('online', run);
  }, [canSync, userId]);
}
