import { QueryClient } from '@tanstack/react-query';

/**
 * The single app-wide TanStack Query client.
 *
 * staleTime is deliberately FINITE (30s), never `Infinity`. The SSE live
 * stream (see `useLiveUpdates`) is best-effort: an event can be missed while
 * the tab is backgrounded or the connection is wedged. A finite staleness
 * window means focus/reconnect refetch (plus the SSE-driven targeted
 * invalidation) keeps data fresh without an explicit poll — the safe combo
 * called out in the live-updates design (§6.1).
 *
 * refetchOnWindowFocus + refetchOnReconnect are the correctness backstop:
 * even if every SSE event is dropped, returning to the tab or regaining the
 * network re-syncs the data. This is the behaviour that retires the
 * hand-rolled `window.addEventListener('focus', …)` listeners the migrated
 * hooks used to carry.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 5 * 60_000,
      refetchOnWindowFocus: true,
      refetchOnReconnect: true,
      // The list/dashboard fetches already surface their own stale
      // indicators; a single automatic retry smooths a transient blip
      // without masking a real outage behind a long backoff.
      retry: 1,
    },
  },
});
