import type { Transaction } from '@/api/types';

/**
 * The string that names a transaction to a user — in a visible line, or inside
 * an accessible name like `Select ${…}`.
 *
 * A description CAN be empty: the xlsx import path bypasses the field-length
 * and presence checks the per-row edit route applies (`validateImportField`
 * runs only there), so a spreadsheet with a blank cell puts an empty
 * description into the ledger. Without the fallback, `Select ${description}`
 * announces "Select ", a control naming no object.
 *
 * The fallback is deliberately the same string the row renders visibly, which
 * keeps the accessible name CONTAINING the visible text (WCAG 2.5.3) instead
 * of diverging from it.
 *
 * Shared so the phone card and the desktop table cannot answer this
 * differently — they are two presentations of one row, and a selection test
 * that queries `Select ${…}` has to match whichever one is mounted.
 */
export function transactionLabel(
  transaction: Pick<Transaction, 'description'>,
): string {
  return transaction.description || '(no description)';
}
