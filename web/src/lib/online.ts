import { onlineManager } from '@tanstack/react-query';

/**
 * `navigator.onLine`, defaulting to `true` where the property is unavailable
 * (server-side rendering, some test environments). Optimistic by design: a
 * missing signal must not make a healthy device look offline.
 */
export function readNavigatorOnline(): boolean {
  return typeof navigator === 'undefined' ||
    typeof navigator.onLine !== 'boolean'
    ? true
    : navigator.onLine;
}

/**
 * Point TanStack Query's shared `onlineManager` at `navigator.onLine`.
 *
 * Why this exists (verified against @tanstack/query-core's onlineManager.js):
 * the manager initialises its internal state to a hard-coded `#online = true`
 * and its default event listener only ever calls `setOnline` from a
 * subsequent window 'online'/'offline' event. It never reads
 * `navigator.onLine`. Two consequences, both of which the household hits on a
 * phone:
 *
 *  1. An app LAUNCHED with no signal believes it is online. Its queries are
 *     not paused (networkMode 'online' pauses only when the manager says
 *     offline), so they fire, burn their retries, error, and render as "you
 *     have no data".
 *  2. When signal returns, the browser fires 'online' → `setOnline(true)` →
 *     `changed === false` because the manager never left `true` → NO listener
 *     is notified → `refetchOnReconnect` never runs. Nothing self-heals; the
 *     user has to kill and reopen the app.
 *
 * Replacing the event listener with one that seeds from `navigator.onLine`
 * (and re-reads it on every transition) fixes both. `setEventListener` is
 * TanStack's supported customisation point: it tears down the previous
 * listener before installing the new one, so calling this repeatedly is safe.
 */
export function installOnlineTracking(): void {
  onlineManager.setEventListener((setOnline) => {
    const sync = (): void => setOnline(readNavigatorOnline());
    // The seed. This is the line the "app launched offline" bug turns on.
    sync();
    if (typeof window === 'undefined') return;
    window.addEventListener('online', sync);
    window.addEventListener('offline', sync);
    return () => {
      window.removeEventListener('online', sync);
      window.removeEventListener('offline', sync);
    };
  });
}
