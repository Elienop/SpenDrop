import { useSyncExternalStore } from 'react';
import { onlineManager } from '@tanstack/react-query';
import { installOnlineTracking } from '@/lib/online';

/**
 * Reactive online/offline status.
 *
 * Backed by TanStack Query's `onlineManager` rather than a private
 * `navigator.onLine` copy so the UI's idea of "offline" and the query layer's
 * idea of "offline" are the SAME fact — a panel can never announce "you're
 * offline" while its own query is busy failing, or vice versa. See
 * `lib/online.ts` for why the manager has to be taught about
 * `navigator.onLine` in the first place.
 *
 * Used by the /quick "Recently added" panel to decide whether saved history
 * can be loaded (it lives on the server) and to show an offline note instead.
 */
function subscribe(onStoreChange: () => void): () => void {
  // Re-seed on every subscribe. `installOnlineTracking` is idempotent, and
  // doing it here means a consumer of this hook gets the correct answer even
  // if nothing else in the tree has imported the app-wide query client yet.
  installOnlineTracking();
  return onlineManager.subscribe(onStoreChange);
}

export function useOnline(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => onlineManager.isOnline(),
    () => true,
  );
}
