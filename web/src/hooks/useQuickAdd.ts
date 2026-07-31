import { useCallback, useState } from 'react';
import { api } from '@/api/client';
import type { CreateTransactionResponse, Transaction } from '@/api/types';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import { enqueue, removeQueued } from '@/lib/offline-queue';
import { useAuth } from '@/hooks/useAuth';

/**
 * Result of a quick-add submit: either it reached the server (and is undoable
 * by its server id) or it was persisted to the offline queue (undoable by its
 * queue id, until it syncs).
 */
export type QuickAddOutcome =
  | {
      status: 'saved';
      transaction: Transaction;
      /**
       * Present when the server reports that the row it just created is
       * identical to one that already existed — which is what a Retry after
       * an ambiguous failure produces. Absent means "not a duplicate", and is
       * also what an older server (which does not report this yet) yields.
       */
      duplicateOfId?: number;
    }
  | { status: 'queued'; queuedId: number };

export interface UseQuickAddResult {
  /** Create one transaction, or queue it locally if the device is offline. */
  create: (input: CreateTransactionInput) => Promise<QuickAddOutcome>;
  /**
   * Undo a just-submitted entry: soft-delete the server row, or drop the
   * queued row if it has not synced yet.
   */
  undo: (outcome: QuickAddOutcome) => Promise<void>;
  /** True while a create request is in flight. */
  saving: boolean;
}

/**
 * Lightweight create/undo wrapper for the mobile quick-add screen. Unlike
 * `useTransactions`, it carries no list/pagination state — quick-add only
 * needs to POST a single transaction and optionally undo it. The payload shape
 * (`CreateTransactionInput`) is reused from `useTransactions` so the wire
 * contract stays single-sourced. `amount` is dollars on the wire (see the
 * Money Wire-Edge DTO discipline); callers build the payload with
 * `toCreatePayload`.
 *
 * Offline: when `navigator.onLine === false` the POST is NOT attempted; the
 * payload is persisted to the IndexedDB offline queue and replayed later (see
 * `offline-queue.ts` / `useOfflineSync`). We gate on `onLine === false` rather
 * than on a fetch failure because only "the browser is certain it is offline"
 * guarantees the request never left the device — so replay creates the row
 * exactly once. An online-but-failed fetch is ambiguous (the write may have
 * landed) and is left to throw so the screen can prompt a manual retry rather
 * than risk a silent duplicate.
 *
 * `POST /api/transactions` DOES compute a content hash and DOES detect that a
 * new manual entry is identical to an existing row — but it deliberately does
 * not refuse it, because real households enter genuine duplicates (see
 * `contentHashForManualEntry`). So the server cannot be relied on to swallow a
 * double-post; what it can do is SAY that it made one, which is what
 * `duplicateOfId` carries back to the screen.
 */
export function useQuickAdd(): UseQuickAddResult {
  const { user } = useAuth();
  const [saving, setSaving] = useState(false);

  // /quick mounts behind ProtectedRoute, so `user` is non-null wherever create/
  // undo run. The offline queue is namespaced per user (so a stale session can
  // never replay another account's captures), so the id is required.
  const userId = user?.id;

  const create = useCallback(
    async (input: CreateTransactionInput): Promise<QuickAddOutcome> => {
      if (userId === undefined) {
        throw new Error('Cannot save a transaction without an authenticated user');
      }
      setSaving(true);
      try {
        if (typeof navigator !== 'undefined' && navigator.onLine === false) {
          const queuedId = await enqueue(userId, input);
          return { status: 'queued', queuedId };
        }
        const created = await api.post<CreateTransactionResponse>(
          'transactions',
          input,
        );
        // The one line that wires the server's duplicate report to the UI.
        const duplicateOfId = created.duplicate_of;
        return duplicateOfId === undefined
          ? { status: 'saved', transaction: created }
          : { status: 'saved', transaction: created, duplicateOfId };
      } finally {
        setSaving(false);
      }
    },
    [userId],
  );

  const undo = useCallback(
    async (outcome: QuickAddOutcome): Promise<void> => {
      if (outcome.status === 'saved') {
        await api.del(`transactions/${outcome.transaction.id}`);
      } else if (userId !== undefined) {
        await removeQueued(userId, outcome.queuedId);
      }
    },
    [userId],
  );

  return { create, undo, saving };
}
