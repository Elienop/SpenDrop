import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';

/**
 * `GET /api/version` → `{"version":"v0.35.0"}`.
 *
 * `version` is typed optional even though the handler always sends it. The
 * response is JSON off the wire, not a value TypeScript checked: a server
 * older than the endpoint answers 404, and a server mid-rollback could answer
 * with a shape this build has never seen. Declaring it required would let
 * `undefined` through the decode and straight into the UI.
 */
interface VersionResponse {
  version?: string;
}

export interface UseServerVersionResult {
  /**
   * The version the SERVER reports, or `null` whenever we do not know it —
   * first paint, offline, 401, 404 on an older server, malformed body. Never
   * `undefined`, so a consumer cannot render the string "undefined" or hand
   * it to something that assumes a value.
   */
  serverVersion: string | null;
}

/**
 * The server's own version, for comparison against the running bundle.
 *
 * FAILURE IS A NON-EVENT. Every consumer of this hook is a passive version
 * stamp, so a failed fetch must produce silence: no toast, no error text, no
 * retry storm. `retry: false` is what makes that true for a household running
 * a server older than this endpoint — with the app-wide default (`retry: 1`)
 * every mount would fire two 404s instead of one, forever. The hook reports
 * `null` and the stamp falls back to showing the bundle version alone.
 *
 * REFETCHING IS ALMOST NEVER USEFUL. The answer changes exactly once per
 * deploy, so the app-wide 30s `staleTime` would re-ask far more often than
 * anything can have changed. 30 minutes suppresses that without a poll, and
 * there is deliberately no `refetchInterval`.
 *
 * WHAT ACTUALLY CATCHES A DEPLOY is the sweep, not the staleTime. Note that a
 * long `staleTime` also SUPPRESSES the inherited focus/reconnect refetch —
 * those honour staleness, so alt-tabbing back inside the window re-asks
 * nothing. The mechanism that does fire is `useLiveUpdates`' `sweepAll()`
 * (useLiveUpdates.ts:68): a bare `invalidateQueries()` with no filter, which
 * marks EVERY key stale regardless of `staleTime` and refetches the active
 * ones. It runs on tab-visible and on SSE reconnect-after-drop — and a deploy
 * drops the stream, so the restart that changes this value is itself what
 * triggers the re-ask. Lengthening `staleTime` further is therefore safe;
 * removing the sweep would strand the stamp until a remount.
 *
 * The `['version']` key sits outside the resource namespaces the SSE stream
 * publishes (`transactions`, `reports`, …), so no household data mutation
 * prefix-matches it — the per-resource `invalidateResource()` path never
 * touches it, which is the point: an expense edit cannot make this value
 * change. That is NOT an exemption from `sweepAll()`, which is unfiltered and
 * deliberately includes this key.
 */
export function useServerVersion(): UseServerVersionResult {
  const query = useQuery({
    queryKey: ['version'],
    queryFn: () => api.get<VersionResponse>('version'),
    retry: false,
    staleTime: 30 * 60_000,
  });

  // Guard the field, not just the request. A 200 whose body lacks `version`
  // (or carries a non-string) is exactly as unknown as a 404, and the
  // difference between the two must not reach the UI as `undefined`.
  const reported = query.data?.version;
  return {
    serverVersion:
      typeof reported === 'string' && reported.length > 0 ? reported : null,
  };
}
