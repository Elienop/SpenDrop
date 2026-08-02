import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';

/**
 * Shape of GET /api/transactions/deleted — only `total` is read here,
 * the rest is realistic noise. The backend scopes the total per-role,
 * so the query only fires when `enabled` is true.
 */
interface DeletedListResponse {
  total: number;
}

/**
 * Subscribe to the count of tombstoned transactions. Backed by TanStack
 * Query under the `['trash']` key, so it refetches automatically on window
 * focus and whenever the live-update subscriber invalidates `['trash']` after
 * a soft-delete / restore / purge anywhere in the household. (The old
 * bespoke `TRASH_CHANGED_EVENT` window-event bus and manual `focus` listener
 * are retired in favour of those two paths.)
 *
 * Gated on `enabled` (user present) — the endpoint serves every authenticated
 * role since B5 and returns a member-scoped total for members. When disabled
 * the query is parked (`enabled: false`) and `count` reports 0.
 *
 * Reuses GET /api/transactions/deleted with per_page=1 — the response
 * envelope already includes a `total` that mirrors `CountDeletedTransactions`,
 * so no new backend route is needed.
 */
export function useTrashCount(enabled: boolean) {
  const query = useQuery({
    queryKey: ['trash'],
    enabled,
    // The badge is non-critical: a failed fetch should not surface an error
    // toast or retry storm — it just leaves the count at 0. The Trash page
    // itself surfaces real errors when the user opens it.
    retry: false,
    queryFn: () =>
      api
        .get<DeletedListResponse>('transactions/deleted?page=1&per_page=1')
        .then((res) => res.total ?? 0),
  });

  return {
    // `query.data` is undefined while disabled or before the first resolve,
    // and we never want a stale count to bleed into a re-mount where
    // `enabled` has just gone false (e.g. logout) — coalesce to 0.
    count: enabled ? query.data ?? 0 : 0,
    loading: query.isLoading,
    refetch: () => {
      void query.refetch();
    },
  };
}
