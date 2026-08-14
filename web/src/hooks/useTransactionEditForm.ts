import { useCallback, useEffect, useRef, useState } from 'react';
import type { Category, Transaction } from '@/api/types';
import type { AmountCurrencyInputProps } from '@/components/AmountCurrencyInput';
import type { AmountSignToggleProps } from '@/components/AmountSignToggle';
import { useCurrencies } from '@/hooks/useCurrencies';
import {
  applyAmountSign,
  toCreatePayload,
  toEditDefaults,
  type StoredMoney,
} from '@/lib/currency';
import type { TransactionType } from '@/lib/transaction-types';
import type { UpdateTransactionInput } from '@/hooks/useTransactions';

/**
 * The props this hook hands straight to `AmountCurrencyInput`.
 *
 * `storedMoney` is `Pick`ed and then re-declared as REQUIRED. On the component
 * it is optional — deliberately, because the create surfaces
 * (TransactionEntryRow, QuickAdd) must omit it — but every EDIT surface has to
 * supply it or the `≈` preview promises today's rate for money the save will
 * carry forward untouched. Making it required here means a surface that drops
 * it fails `tsc`, instead of silently re-pricing a foreign row.
 */
export interface EditAmountProps
  extends Omit<
    Pick<
      AmountCurrencyInputProps,
      | 'value'
      | 'onValueChange'
      | 'currency'
      | 'onCurrencyChange'
      | 'baseCode'
      | 'currencies'
      | 'hideInactive'
      | 'rateFor'
      | 'loading'
      | 'storedMoney'
      | 'negative'
      | 'error'
    >,
    'storedMoney'
  > {
  storedMoney: StoredMoney;
}

/**
 * The props this hook hands straight to `AmountSignToggle`.
 *
 * Same reasoning as `amountProps`: the sign is not exposed as a bare
 * boolean + setter, so a surface cannot wire the control up while bypassing
 * the magnitude/sign split the amount box depends on.
 */
export type EditSignProps = Pick<
  AmountSignToggleProps,
  'checked' | 'onCheckedChange' | 'type'
>;

export interface UseTransactionEditFormArgs {
  transaction: Transaction;
  onUpdate: (input: UpdateTransactionInput) => Promise<void>;
  onError: (message: string) => void;
  /** Runs after a save that resolved. The two edit surfaces close differently
   *  — the table row collapses back to its read view, the sheet dismisses. */
  onSaved: () => void;
  /**
   * The categories the surface offers in its picker. Used for one thing:
   * reading the type of the category currently SELECTED, so the sign toggle
   * says "Refund" on an expense and "Reversal" on an income row even after the
   * user re-files it. Omit it and the label follows the row's stored type,
   * which is right until the moment they change the category.
   */
  categories?: Category[];
}

/**
 * Deliberately does NOT expose the amount/currency pair. Both edit surfaces
 * reach the amount only through `amountProps`, which is what keeps the
 * storedMoney freeze contract at one construction site — handing out
 * `amount`/`setAmount` separately would let a future surface wire the amount
 * up while quietly bypassing that contract, and it would still type-check.
 */
export interface UseTransactionEditForm {
  date: string;
  setDate: (v: string) => void;
  description: string;
  setDescription: (v: string) => void;
  categoryId: string;
  setCategoryId: (v: string) => void;
  tags: string;
  setTags: (v: string) => void;
  /** In-flight, for a surface that shows a pending cue. Distinct from
   *  `saveDisabled`, which also covers "cannot save yet" — a control that is
   *  merely dimmed says "you cannot do this", not "this is happening". */
  saving: boolean;
  baseCode: string;
  /** Re-seeds every field from the current `transaction` prop. */
  reset: () => void;
  save: () => Promise<void>;
  amountProps: EditAmountProps;
  /**
   * The Refund/Reversal toggle. REQUIRED on both edit surfaces, not a nicety:
   * the amount box shows a stored refund's magnitude, so without this control
   * the sign has nowhere to live and an unrelated edit — fixing a typo in the
   * description — would save the row back positive and silently turn money
   * returned into money spent.
   */
  signProps: EditSignProps;
  /** Save is refused while a request is in flight, while the currency list is
   *  still loading, and for a non-base currency with no configured rate. */
  saveDisabled: boolean;
}

/**
 * The edit-a-transaction form, minus its layout.
 *
 * Both edit surfaces run on this: the desktop inline table row and the phone
 * edit Sheet. They lay their fields out in completely different boxes —
 * `<TableCell>`s across one row versus a stacked column — but the field state,
 * the currency rehydration race, the payload construction and the money
 * contract are the same, and each is subtle enough that a second copy would
 * drift. `storedMoney` in particular is built exactly once, here.
 */
export function useTransactionEditForm({
  transaction,
  onUpdate,
  onError,
  onSaved,
  categories,
}: UseTransactionEditFormArgs): UseTransactionEditForm {
  const {
    list: currencies,
    baseCode,
    rateFor,
    loading: currenciesLoading,
  } = useCurrencies();
  const [date, setDate] = useState(transaction.date);
  // `baseCode` is `DEFAULT_CURRENCY` ("USD") until the useCurrencies fetch
  // resolves. For rows with original_* === null and a non-USD household
  // base, the initial defaults capture "USD"; the didInitEditCurrency
  // effect below rehydrates once the fetch lands.
  const initialDefaults = toEditDefaults(transaction, baseCode);
  // The amount splits in two on the way into the form: the box holds the
  // MAGNITUDE and the toggle holds the direction, so a stored refund opens as
  // "20.00" with Refund on rather than as "-20.00" in a field that refuses a
  // minus. `save` puts them back together.
  const [editAmount, setEditAmount] = useState<number>(
    Math.abs(initialDefaults.amount),
  );
  const [isNegative, setIsNegative] = useState<boolean>(
    initialDefaults.amount < 0,
  );
  const [editCurrency, setEditCurrency] = useState<string>(
    initialDefaults.currency,
  );
  const [description, setDescription] = useState(transaction.description);
  const [categoryId, setCategoryId] = useState(String(transaction.category_id));
  const [tags, setTags] = useState(transaction.tags ?? '');
  const [saving, setSaving] = useState(false);

  const didInitEditCurrency = useRef(false);
  useEffect(() => {
    if (didInitEditCurrency.current) return;
    if (currenciesLoading) return;
    didInitEditCurrency.current = true;
    const resolved = toEditDefaults(transaction, baseCode);
    // The setState pair below is the async-resolution pattern the
    // eslint config already blesses for `src/hooks/**` and `src/pages/**`:
    // the household base currency only exists once the `useCurrencies` fetch
    // lands, and a user who opens Edit inside that window is holding fields
    // seeded from the placeholder base. There is no render-time value to
    // derive from — the correction cannot happen before the data does — and
    // the `didInitEditCurrency` latch above bounds it to a single pass, so it
    // cannot cascade.
    if (resolved.currency !== editCurrency) {
      setEditCurrency(resolved.currency);
    }
    // Re-split, not re-assign: the rehydrated value is the row's SIGNED money,
    // and the two pieces have to move together or a foreign refund would
    // rehydrate its magnitude while the toggle kept the placeholder base
    // currency's sign.
    const resolvedMagnitude = Math.abs(resolved.amount);
    if (resolvedMagnitude !== editAmount) {
      setEditAmount(resolvedMagnitude);
    }
    const resolvedNegative = resolved.amount < 0;
    if (resolvedNegative !== isNegative) {
      setIsNegative(resolvedNegative);
    }
    // editAmount/editCurrency/isNegative intentionally excluded: this effect
    // must run exactly once per mount, gated by didInitEditCurrency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currenciesLoading, baseCode, transaction]);

  const save = useCallback(async () => {
    setSaving(true);
    let payload: UpdateTransactionInput;
    try {
      const wire = toCreatePayload(
        {
          // Magnitude and toggle recombined, before the conversion so a
          // foreign refund divides with its sign and both halves of the money
          // pair stay in agreement.
          amount: applyAmountSign(editAmount, isNegative),
          currency: editCurrency,
          date,
          description,
          category_id: parseInt(categoryId, 10),
          tags,
        },
        baseCode,
        rateFor,
      );
      payload = { id: transaction.id, ...wire };
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Invalid currency rate');
      setSaving(false);
      return;
    }
    try {
      await onUpdate(payload);
      // Retire whatever the last failed save left on the page-level banner.
      // Cleared on SUCCESS, not before the request: clearing up front would
      // blank the banner for the duration of a retry and then re-raise it.
      onError('');
      onSaved();
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Failed to save');
    } finally {
      setSaving(false);
    }
  }, [
    editAmount,
    isNegative,
    editCurrency,
    date,
    description,
    categoryId,
    tags,
    baseCode,
    rateFor,
    transaction.id,
    onUpdate,
    onError,
    onSaved,
  ]);

  const reset = useCallback(() => {
    const resolved = toEditDefaults(transaction, baseCode);
    setDate(transaction.date);
    setEditAmount(Math.abs(resolved.amount));
    setIsNegative(resolved.amount < 0);
    setEditCurrency(resolved.currency);
    setDescription(transaction.description);
    setCategoryId(String(transaction.category_id));
    setTags(transaction.tags ?? '');
  }, [transaction, baseCode]);

  const amountProps: EditAmountProps = {
    value: editAmount,
    onValueChange: setEditAmount,
    currency: editCurrency,
    onCurrencyChange: setEditCurrency,
    baseCode,
    currencies,
    hideInactive: false,
    rateFor,
    loading: currenciesLoading,
    // What the toggle beside the box is set to. The preview needs it to
    // rebuild the signed amount the save will send, because the freeze
    // comparison below is over signed cents — see the prop's own doc.
    negative: isNegative,
    // The money this row already stores. Saving an edit that merely
    // restates the same foreign amount in the same currency keeps that
    // stored value instead of re-pricing at today's rate, so the `≈`
    // preview needs it to promise the number the save will produce.
    // Read from the live `transaction` prop rather than a snapshot taken when
    // the editor opened, so a refetch landing mid-edit moves this with the row
    // the server will actually compare against.
    storedMoney: {
      amount: transaction.amount,
      original_amount: transaction.original_amount,
      original_currency: transaction.original_currency,
    },
    error:
      editCurrency !== baseCode && rateFor(editCurrency) == null
        ? 'No rate configured for this currency. Set one in Settings.'
        : null,
  };

  // The word the toggle wears follows the category the row is filed under
  // RIGHT NOW, so re-filing an expense as income relabels it. Falls back to the
  // stored type for a caller that passes no category list, and again if the
  // selected id matches nothing — a picker whose list has not loaded yet must
  // not silently demote an income row to "Refund".
  const signType: TransactionType =
    categories?.find((c) => String(c.id) === categoryId)?.type ??
    transaction.category_type;

  const signProps: EditSignProps = {
    checked: isNegative,
    onCheckedChange: setIsNegative,
    type: signType,
  };

  return {
    date,
    setDate,
    description,
    setDescription,
    categoryId,
    setCategoryId,
    tags,
    setTags,
    saving,
    baseCode,
    reset,
    save,
    amountProps,
    signProps,
    saveDisabled:
      saving ||
      currenciesLoading ||
      (editCurrency !== baseCode && rateFor(editCurrency) == null),
  };
}
