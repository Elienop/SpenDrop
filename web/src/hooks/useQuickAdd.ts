import { useCallback, useState } from 'react';
import { api } from '@/api/client';
import type { Transaction } from '@/api/types';
import type { CreateTransactionInput } from '@/hooks/useTransactions';
import { enqueue, removeQueued } from '@/lib/offline-queue';
import { TRASH_CHANGED_EVENT } from '@/hooks/useTrashCount';

/**
 * Result of a quick-add submit: either it reached the server (and is undoable
 * by its server id) or it was persisted to the offline queue (undoable by its
 * queue id, until it syncs).
 */
export type QuickAddOutcome =
  | { status: 'saved'; transaction: Transaction }
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
 * than risk a duplicate — `POST /api/transactions` has no content-hash dedup.
 */
export function useQuickAdd(): UseQuickAddResult {
  const [saving, setSaving] = useState(false);

  const create = useCallback(
    async (input: CreateTransactionInput): Promise<QuickAddOutcome> => {
      setSaving(true);
      try {
        if (typeof navigator !== 'undefined' && navigator.onLine === false) {
          const queuedId = await enqueue(input);
          return { status: 'queued', queuedId };
        }
        const transaction = await api.post<Transaction>('transactions', input);
        return { status: 'saved', transaction };
      } finally {
        setSaving(false);
      }
    },
    [],
  );

  const undo = useCallback(async (outcome: QuickAddOutcome): Promise<void> => {
    if (outcome.status === 'saved') {
      await api.del(`transactions/${outcome.transaction.id}`);
      // The soft-delete just moved a row into trash — notify the
      // sidebar badge.
      window.dispatchEvent(new Event(TRASH_CHANGED_EVENT));
    } else {
      await removeQueued(outcome.queuedId);
    }
  }, []);

  return { create, undo, saving };
}
