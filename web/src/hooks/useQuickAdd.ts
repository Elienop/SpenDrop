import { useCallback, useState } from 'react';
import { api } from '@/api/client';
import type { Transaction } from '@/api/types';
import type { CreateTransactionInput } from '@/hooks/useTransactions';

export interface UseQuickAddResult {
  /** Create one transaction. Resolves to the saved row (used for Undo). */
  create: (input: CreateTransactionInput) => Promise<Transaction>;
  /** Soft-delete a just-saved transaction by id (powers the Undo toast). */
  undo: (id: number) => Promise<void>;
  /** True while a create request is in flight. */
  saving: boolean;
}

/**
 * Lightweight create/undo wrapper for the mobile quick-add screen. Unlike
 * `useTransactions`, it carries no list/pagination state — quick-add only
 * needs to POST a single transaction and optionally undo it. The payload
 * shape (`CreateTransactionInput`) is reused from `useTransactions` so the
 * wire contract stays single-sourced. `amount` is dollars on the wire
 * (see the Money Wire-Edge DTO discipline); callers build the payload with
 * `toCreatePayload`.
 */
export function useQuickAdd(): UseQuickAddResult {
  const [saving, setSaving] = useState(false);

  const create = useCallback(
    async (input: CreateTransactionInput): Promise<Transaction> => {
      setSaving(true);
      try {
        return await api.post<Transaction>('transactions', input);
      } finally {
        setSaving(false);
      }
    },
    [],
  );

  const undo = useCallback(async (id: number): Promise<void> => {
    await api.del(`transactions/${id}`);
  }, []);

  return { create, undo, saving };
}
