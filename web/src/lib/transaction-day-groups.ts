import type { Transaction } from '@/api/types';

export interface TransactionDayGroup {
  date: string;
  items: Transaction[];
}

/**
 * Splits a page of transactions into RUNS of equal dates rather than grouping
 * by date.
 *
 * The two agree exactly while the rows are date-ordered, which is the only
 * time the phone list asks for day headers. The difference matters if that
 * ever stops holding: a `Map` keyed by date would silently pull two separated
 * runs of the same day under one header and reorder the page to do it, where a
 * run-length split can only ever mislabel a boundary it can already see.
 *
 * Lives in `lib/` rather than beside the list component so that component can
 * stay a components-only module (react-refresh/only-export-components).
 */
export function groupIntoDayRuns(
  transactions: Transaction[],
): TransactionDayGroup[] {
  const groups: TransactionDayGroup[] = [];
  for (const tx of transactions) {
    const last = groups[groups.length - 1];
    if (last != null && last.date === tx.date) {
      last.items.push(tx);
    } else {
      groups.push({ date: tx.date, items: [tx] });
    }
  }
  return groups;
}
