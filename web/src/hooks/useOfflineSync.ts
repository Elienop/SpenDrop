import { useEffect } from 'react';
import { toast } from 'sonner';
import { drainQueue } from '@/lib/offline-queue';
import { useAuth } from '@/hooks/useAuth';

/**
 * App-wide replay trigger for the offline write queue. Mount once (App.tsx) so
 * queued expenses sync whenever the app is open and connectivity returns — not
 * only while /quick is mounted. iOS PWAs have no Background Sync API, so this
 * foreground replay (on app launch + the window 'online' event) is the
 * reliable mechanism. Replay is gated on an authenticated user because the
 * POST needs the session cookie; logging back in re-runs this effect, which
 * drains anything that 401'd while the session was expired.
 */
export function useOfflineSync(): void {
  const { user } = useAuth();

  useEffect(() => {
    if (!user) return;
    const userId = user.id;

    const run = (): void => {
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

    run(); // launch / login drain (no-ops if empty or offline)
    window.addEventListener('online', run);
    return () => window.removeEventListener('online', run);
  }, [user]);
}
