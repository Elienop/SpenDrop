import { useEffect } from 'react';
import { queryClient } from '@/lib/queryClient';
import { useAuth } from '@/hooks/useAuth';
import { trimTrailingSlashes } from '@/lib/url';

// Resolve the SSE endpoint from the same API base the REST client uses
// (web/src/api/client.ts). Default same-origin deployment → '/api/events'.
// EventSource cannot set headers, so it relies on the same-origin session
// cookie (sent because the request is same-origin / withCredentials).
const API_BASE_URL = trimTrailingSlashes(
  (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? '/api',
);
const EVENTS_URL = `${API_BASE_URL}/events`;

// Trailing-debounce window: coalesce a burst of hints for the same resource
// (e.g. a CSV import that touches transactions repeatedly) into one refetch.
const DEBOUNCE_MS = 200;

interface InvalidatePayload {
  resources?: unknown;
}

/**
 * App-wide live-update subscriber. Mount ONCE (App.tsx) beside useOfflineSync.
 *
 * Opens a single EventSource to /api/events when authenticated; the server
 * pushes coarse `{resources:[...]}` invalidation hints (no row data crosses the
 * stream). Each named resource is invalidated through the shared TanStack
 * QueryClient behind a 200ms trailing debounce per resource so bursts coalesce.
 *
 * Two correctness backstops layer on top of the live stream:
 *  - reconnect-after-drop sweep: a connection that dropped may have missed
 *    events, so the first onopen AFTER a prior error invalidates EVERYTHING.
 *    The very first connect is skipped (initial mounts already fetched).
 *  - visibility sweep: returning to a backgrounded tab/PWA (where the browser
 *    may have paused the stream) invalidates everything on visible.
 *
 * Auth-gated like useOfflineSync: the EventSource needs the session cookie, and
 * tearing the effect down when `user` becomes null closes the socket on logout.
 * StrictMode double-mount is safe — the effect cleanup closes the connection.
 */
export function useLiveUpdates(): void {
  const { user } = useAuth();

  useEffect(() => {
    if (!user) return;
    if (typeof EventSource === 'undefined') return;

    const es = new EventSource(EVENTS_URL, { withCredentials: true });

    // Per-resource trailing-debounce timers.
    const timers = new Map<string, ReturnType<typeof setTimeout>>();
    const invalidateResource = (resource: string): void => {
      const existing = timers.get(resource);
      if (existing) clearTimeout(existing);
      timers.set(
        resource,
        setTimeout(() => {
          timers.delete(resource);
          // Query keys' first segment is the resource name, so a bare key
          // array prefix-matches every variant (e.g. ['transactions', filters]).
          void queryClient.invalidateQueries({ queryKey: [resource] });
        }, DEBOUNCE_MS),
      );
    };

    // A bare invalidateQueries() with no filter marks EVERY query stale —
    // used by the reconnect and visibility sweeps to heal missed events.
    const sweepAll = (): void => {
      void queryClient.invalidateQueries();
    };

    // Track whether we have ever errored so the FIRST onopen does not sweep,
    // but a reconnect (onopen after an error) does.
    let hadError = false;

    // The server sends NAMED `invalidate` events (`event: invalidate\ndata: …`).
    // A real EventSource routes a named event to addEventListener(name) — NOT to
    // `onmessage`, which only fires for the default/unnamed (`message`) event.
    // Listening on onmessage here would silently receive nothing in production
    // (a test mock that ignores the event name can hide this). So subscribe to
    // the named event explicitly.
    const onInvalidate = (ev: MessageEvent): void => {
      let payload: InvalidatePayload;
      try {
        payload = JSON.parse(ev.data as string) as InvalidatePayload;
      } catch {
        return; // Malformed frame — ignore; the next event / sweep heals.
      }
      const resources = payload.resources;
      if (!Array.isArray(resources)) return;
      for (const resource of resources) {
        if (typeof resource === 'string' && resource) {
          invalidateResource(resource);
        }
      }
    };
    es.addEventListener('invalidate', onInvalidate);

    es.onerror = (): void => {
      // EventSource auto-reconnects; just remember that a drop happened so the
      // next successful onopen runs the catch-up sweep.
      hadError = true;
    };

    es.onopen = (): void => {
      if (hadError) {
        hadError = false;
        sweepAll(); // reconnect-after-drop: catch anything missed while down.
      }
    };

    // Returning to a backgrounded tab/PWA: the browser may have paused the
    // stream, so refetch everything. (Distinct from the reconnect sweep, which
    // fires on the socket lifecycle.)
    const onVisibility = (): void => {
      if (document.visibilityState === 'visible') sweepAll();
    };
    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      for (const t of timers.values()) clearTimeout(t);
      timers.clear();
      es.onopen = null;
      es.removeEventListener('invalidate', onInvalidate);
      es.onerror = null;
      es.close();
    };
  }, [user]);
}
