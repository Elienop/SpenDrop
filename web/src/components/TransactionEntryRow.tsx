import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type MouseEvent,
} from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { toast } from 'sonner';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  useFormField,
} from '@/components/ui/form';
import { Label } from '@/components/ui/label';
import { Input } from '@/components/ui/input';
import { AutocompleteInput } from './AutocompleteInput';
import { Button } from '@/components/ui/button';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Card } from '@/components/ui/card';
import { TagInput } from './TagInput';
import { CategoryBadge } from './CategoryBadge';
import { AmountCurrencyInput } from './AmountCurrencyInput';
import { AmountSignToggle } from './AmountSignToggle';
import type { Category, Transaction } from '../api/types';
import {
  TYPE_EXPENSE,
  type TransactionType,
} from '@/lib/transaction-types';
import { formatYYYYMMDD } from '@/lib/dates';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import { MAX_DESCRIPTION_LENGTH } from '@/lib/constants';
import { charCount } from '@/lib/text';
import { newClientKey } from '@/lib/client-key';
import {
  isRetryableSaveFailure,
  noRateMessage,
  saveFailureMessage,
} from '@/lib/save-failure';
import { applyAmountSign, toCreatePayload } from '@/lib/currency';
import { useCurrencies } from '@/hooks/useCurrencies';
import type { CreateTransactionInput } from '@/hooks/useTransactions';

function todayIso(): string {
  return formatYYYYMMDD(new Date());
}

function getLastDate(): string {
  return (
    localStorage.getItem(STORAGE_KEYS.lastTransactionDate) ?? todayIso()
  );
}

function saveLastDate(value: string) {
  localStorage.setItem(STORAGE_KEYS.lastTransactionDate, value);
}

function getLastCategoryId(): number {
  const raw = localStorage.getItem(STORAGE_KEYS.lastTransactionCategory);
  if (!raw) return 0;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function saveLastCategory(id: number) {
  localStorage.setItem(STORAGE_KEYS.lastTransactionCategory, String(id));
}

function getLastCurrency(fallback: string): string {
  return (
    localStorage.getItem(STORAGE_KEYS.lastTransactionCurrency) ?? fallback
  );
}

function saveLastCurrency(code: string) {
  localStorage.setItem(STORAGE_KEYS.lastTransactionCurrency, code);
}

function EntryLabel({ children }: { children: string }) {
  const { error, formItemId } = useFormField();
  return (
    <Label
      htmlFor={formItemId}
      className={error ? 'text-destructive' : undefined}
    >
      {children}
      {error && (
        <span className="ml-1 font-normal">
          {String(error.message ?? '')}
        </span>
      )}
    </Label>
  );
}

const entrySchema = z.object({
  date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, 'Invalid date'),
  // STILL POSITIVE-ONLY, deliberately, now that the ledger stores signed
  // amounts. A refund is entered by turning the Refund toggle on beside this
  // field, never by typing a minus — so a negative here is a typo and keeps
  // being refused. `submit` applies the toggle's sign to this magnitude on its
  // way to the wire (`applyAmountSign`), which is also what keeps the zero
  // rejection intact: `AmountCurrencyInput` emits 0 for an empty or unparseable
  // box, and `.positive()` is the only thing standing between that and a
  // payload.
  amount: z.number().positive('> 0'),
  currency: z.string().regex(/^[A-Z]{3}$/, 'Invalid currency'),
  /** The Refund toggle. Form state rather than a local `useState` so it resets
   *  with the row after a save and comes back with it on Undo. */
  refund: z.boolean(),
  // 500 CHARACTERS, matching the server (MaxDescriptionLength) and the bulk-edit
  // dialog. This field used to cap at 200, which came in with the component in
  // the design-system v3 PR and was never anything the product claimed: no
  // comment explained it, no message named it (Zod's default "Too big" was all
  // the user got), no `maxLength` stopped the typing, and every other way of
  // writing a description — bulk edit, quick add, import — took 500. So a
  // description pasted into this row failed at submit while the same text saved
  // fine one dialog over.
  //
  // charCount, not Zod's .max(): .max() counts UTF-16 code units, so an emoji
  // costs 2 there and 1 on the server, and the two limits would drift apart on
  // exactly the input that makes them differ.
  description: z
    .string()
    .min(1, 'required')
    .refine((value) => charCount(value) <= MAX_DESCRIPTION_LENGTH, {
      message: `max ${MAX_DESCRIPTION_LENGTH}`,
    }),
  category_id: z.number().int().positive('required'),
  tags: z.string(),
});
type EntryFormValues = z.infer<typeof entrySchema>;

/**
 * One save attempt, built once. `payload` already carries the `client_key`
 * minted when it was assembled, so re-sending THIS object — rather than
 * rebuilding it from the form — is what lets a retry after an ambiguous
 * failure resolve to the row already created instead of a second one.
 * `values` rides along because the success path needs the submitted form
 * state for the sticky writes, the undo buffer, and the post-save reset.
 */
interface EntrySubmission {
  payload: CreateTransactionInput;
  values: EntryFormValues;
  /**
   * Toast slot for this submission — the failure, the "Retrying…" spinner and
   * the eventual result all render into it, so one save never stacks three
   * toasts. Shares the client key's value: one intent, one identity.
   */
  toastId: string;
}

/** Field-by-field equality, so a retry can tell whether the row still holds
 *  the entry it is about to save or a draft the user has started since. */
function sameEntry(a: EntryFormValues, b: EntryFormValues): boolean {
  return (
    a.date === b.date &&
    a.amount === b.amount &&
    a.refund === b.refund &&
    a.currency === b.currency &&
    a.description === b.description &&
    a.category_id === b.category_id &&
    a.tags === b.tags
  );
}

export interface TransactionEntryRowProps {
  categories: Category[];
  onSubmit: (input: CreateTransactionInput) => Promise<Transaction>;
  onDelete: (id: number) => Promise<void>;
  onClose?: () => void;
  descriptionSuggestions?: string[];
  tagSuggestions?: string[];
}

export function TransactionEntryRow({
  categories,
  onSubmit,
  onDelete,
  onClose,
  descriptionSuggestions = [],
  tagSuggestions = [],
}: TransactionEntryRowProps) {
  const formRef = useRef<HTMLFormElement | null>(null);
  const amountRef = useRef<HTMLInputElement | null>(null);
  const [catOpen, setCatOpen] = useState(false);
  const undoBufferRef = useRef<{
    saved: Transaction;
    values: EntryFormValues;
  } | null>(null);
  // Latest send, so the error toast's Retry action can re-run it without
  // making `send` depend on itself.
  const sendRef = useRef<
    (submission: EntrySubmission, isRetry?: boolean) => void
  >(() => {});
  // A save is in flight. The state drives the button; the ref is read
  // synchronously by `submit`, which can fire from the keyboard before React
  // has re-rendered the disabled button.
  const [isSending, setIsSending] = useState(false);
  const sendingRef = useRef(false);

  const { list, baseCode, rateFor, loading: currenciesLoading } = useCurrencies();

  const form = useForm<EntryFormValues>({
    resolver: zodResolver(entrySchema),
    defaultValues: {
      date: getLastDate(),
      amount: 0,
      // Not sticky, unlike the date/category/currency beside it: a refund is
      // the rare entry, and defaulting the next one to it would silently
      // negate an ordinary expense.
      refund: false,
      currency: getLastCurrency(baseCode),
      description: '',
      category_id: getLastCategoryId(),
      tags: '',
    },
    mode: 'onSubmit',
  });

  // `baseCode` from the hook is `DEFAULT_CURRENCY` ("USD") until fetch
  // resolves. Once currencies load, resync the form's default currency
  // to the sticky-localStorage value or the real baseCode — but only
  // once, and only if the user hasn't already changed it.
  const didInitCurrency = useRef(false);
  useEffect(() => {
    if (didInitCurrency.current) return;
    if (currenciesLoading) return;
    didInitCurrency.current = true;
    const current = form.getValues('currency');
    const resolved = getLastCurrency(baseCode);
    if (current !== resolved) {
      form.setValue('currency', resolved, { shouldDirty: false });
    }
  }, [currenciesLoading, baseCode, form]);

  const undoLastSave = useCallback(async () => {
    const buf = undoBufferRef.current;
    if (!buf) return;
    undoBufferRef.current = null;
    try {
      await onDelete(buf.saved.id);
    } catch {
      undoBufferRef.current = buf;
      toast.error('Could not undo');
      return;
    }
    form.reset(buf.values);
    // Defer focus so React commits the form.reset state update and
    // AmountCurrencyInput's value-sync useEffect fires with focused=false,
    // clearing its rawInput buffer. Immediate focus() races against the
    // React commit — onFocus sets focusedRef=true before the useEffect
    // runs, which makes the useEffect skip the sync and strand the
    // pre-reset rawInput in the DOM.
    // Blur first. The deferred focus() alone only helps when focus was
    // elsewhere; submitting with Cmd/Ctrl+Enter FROM the amount field leaves
    // it focused throughout, and AmountCurrencyInput deliberately ignores
    // incoming `value` while focused — so the reset to 0 was swallowed and
    // the old text stayed in the DOM, ready to concatenate into the next
    // transaction's amount. Blur runs its unconditional re-sync from `value`.
    amountRef.current?.blur();
    setTimeout(() => amountRef.current?.focus(), 0);
  }, [onDelete, form]);

  // Sends an already-built save. Takes the submission rather than reading the
  // form, so a retry re-sends exactly what went out before — same body, same
  // client_key — instead of rebuilding it and minting a second identity for a
  // write the server may already have committed.
  const send = useCallback(
    async (submission: EntrySubmission, isRetry = false) => {
      const { payload, values, toastId } = submission;
      let saved: Transaction;
      sendingRef.current = true;
      setIsSending(true);
      try {
        saved = await onSubmit(payload);
      } catch (err) {
        toast.error(saveFailureMessage(err), {
          id: toastId,
          // Never auto-dismiss. This toast holds the only path that cannot
          // duplicate the row; the Add button beside it mints a fresh key and
          // never expires. A window that closes on its own would quietly demote
          // the safe option to the risky one, so the user closes it themselves.
          duration: Infinity,
          closeButton: true,
          // Retry re-sends THIS submission. Pressing Add again instead is a
          // fresh intent with a fresh key — right for a write the server
          // rejected, but a duplicate for one that landed and lost its
          // response. Offered only where sending again could change the answer:
          // a 401 needs a sign-in and a 400 will be refused identically.
          action: isRetryableSaveFailure(err)
            ? {
                label: 'Retry',
                onClick: (event: MouseEvent<HTMLButtonElement>) => {
                  // sonner dismisses a toast after its action runs unless the
                  // event is default-prevented. Keep it, and reuse the slot so
                  // the spinner replaces the error where the user is looking.
                  event.preventDefault();
                  toast.loading('Retrying…', { id: toastId });
                  sendRef.current(submission, true);
                },
              }
            : undefined,
        });
        // Leave the form untouched so the user can retry. Do not clear
        // undoBufferRef — a previous successful save's undo buffer, if any,
        // stays valid because nothing new was saved.
        return;
      } finally {
        sendingRef.current = false;
        setIsSending(false);
      }
      saveLastCategory(values.category_id);
      saveLastDate(values.date);
      saveLastCurrency(values.currency);
      undoBufferRef.current = { saved, values };

      toast.success('Transaction saved', {
        // Same slot as the failure it replaces, so a retry resolves where the
        // user is already looking instead of stacking a third toast.
        id: toastId,
        duration: 4000,
        action: {
          label: 'Undo (\u2318Z)',
          onClick: () => {
            void undoLastSave();
          },
        },
        onAutoClose: () => {
          undoBufferRef.current = null;
        },
      });

      // A retry can succeed long after the failure, by which time the row may
      // hold a different entry the user has started. Clearing it then would
      // destroy work to tidy up after a save they already believe happened. The
      // entry itself is saved either way — only the row is spared.
      if (isRetry && !sameEntry(form.getValues(), values)) return;

      form.reset({
        date: values.date,
        amount: 0,
        refund: false,
        currency: values.currency,
        description: '',
        category_id: values.category_id,
        tags: '',
      });
      // Defer focus so React commits the form.reset state update and
      // AmountCurrencyInput's value-sync useEffect fires with focused=false,
      // clearing its rawInput buffer. Immediate focus() races against the
      // React commit — onFocus sets focusedRef=true before the useEffect
      // runs, which makes the useEffect skip the sync and strand the
      // pre-submit rawInput (e.g. "25") in the DOM.
      // Blur first. The deferred focus() alone only helps when focus was
      // elsewhere; submitting with Cmd/Ctrl+Enter FROM the amount field leaves
      // it focused throughout, and AmountCurrencyInput deliberately ignores
      // incoming `value` while focused — so the reset to 0 was swallowed and
      // the old text stayed in the DOM, ready to concatenate into the next
      // transaction's amount. Blur runs its unconditional re-sync from `value`.
      amountRef.current?.blur();
      setTimeout(() => amountRef.current?.focus(), 0);
    },
    [onSubmit, form, undoLastSave],
  );

  // Keep the error toast's Retry handler pointed at the current send closure.
  // Only the closure is refreshed — the submission it is handed is the one
  // captured when that toast was raised.
  useEffect(() => {
    sendRef.current = (submission, isRetry) => void send(submission, isRetry);
  }, [send]);

  const submit = useCallback(
    async (values: EntryFormValues) => {
      // A second submit while one is in flight would mint a second key and
      // create a genuine duplicate. The Add button is disabled for exactly this
      // window; the guard also covers the Cmd/Ctrl+Enter path, which never
      // touches the button. A RETRY is deliberately not gated — it re-sends the
      // same key, so sending it twice cannot duplicate.
      if (sendingRef.current) return;
      const clientKey = newClientKey();
      let payload: CreateTransactionInput;
      // `refund` is a form field, not a wire field — the server infers nothing
      // from a flag, it just stores the sign. Destructured out rather than
      // left to ride along, because `toCreatePayload` spreads whatever it is
      // handed and an unknown key would go out with the request.
      const { refund, ...entry } = values;
      try {
        // Union-to-optional widening: toCreatePayload returns a discriminated
        // union (collapsed vs. expanded shape), while CreateTransactionInput
        // declares original_amount / original_currency as optional. Both
        // branches are structurally compatible at runtime — the cast only
        // tells the type system that the optional-field form subsumes both.
        //
        // The single client_key mint site for this form: one per submit, so
        // editing the row and saving again is correctly a new intent. A retry
        // of THIS submit goes through `send` and keeps this key.
        //
        // The sign goes on BEFORE the conversion, so a foreign refund divides
        // with it and both halves of the money pair land negative.
        payload = toCreatePayload(
          {
            ...entry,
            amount: applyAmountSign(entry.amount, refund),
            client_key: clientKey,
          },
          baseCode,
          rateFor,
        ) as CreateTransactionInput;
      } catch {
        // Nothing was built and nothing was sent, so there is no Retry to offer
        // — and a generic "failed to save" would send the user looking for a
        // network problem. Name the missing rate instead.
        toast.error(noRateMessage(values.currency));
        return;
      }
      await send({ payload, values, toastId: clientKey });
    },
    [send, baseCode, rateFor],
  );

  const categoryNameById = (id: number): string | undefined =>
    categories.find((c) => c.id === id)?.name;

  const focusFieldByName = (name: string) => {
    const el = formRef.current?.querySelector<HTMLElement>(
      `[data-entry-field="${name}"]`,
    );
    if (!el) return;
    el.focus();
    if (el instanceof HTMLInputElement) {
      el.select();
    }
    if (name === 'category_id') {
      setCatOpen(true);
    }
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLFormElement>) => {
    // Cmd/Ctrl+Enter submits from any field
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      void form.handleSubmit(submit)();
      return;
    }
    if (e.key === 'Escape') {
      // Radix renders PopoverContent/SelectContent through a portal, so it
      // sits outside this form in the DOM — but it is still a React child,
      // and React replays synthetic events along the React tree. An Escape
      // meant to dismiss the Category or Currency picker therefore arrived
      // here and wiped the entire half-typed row before the picker ever saw
      // it.
      //
      // Detect the picker directly instead of testing containment against
      // formRef: that ref can hold a stale detached <form> (see the Cmd/Ctrl+Z
      // guard below, which has the same weakness), so a containment test
      // silently rejects every Escape including the legitimate ones. Radix
      // parks focus inside its popper wrapper while a picker is open, which
      // is precisely the case we must not treat as "cancel the row".
      const active = document.activeElement;
      if (
        active instanceof Element &&
        active.closest(
          '[data-radix-popper-content-wrapper],[role="listbox"],[role="dialog"]',
        )
      ) {
        return;
      }
      e.preventDefault();
      form.reset();
      onClose?.();
      return;
    }
    if (e.key === 'Enter') {
      const target = e.target as HTMLElement;
      if (
        target.tagName === 'BUTTON' &&
        target.getAttribute('type') === 'submit'
      ) {
        return;
      }
      // Field-to-field navigation order. Tags is intentionally excluded
      // because TagInput's internal Enter handler commits a tag in-place.
      const order: string[] = ['date', 'amount', 'currency', 'description', 'category_id'];
      const current = target.getAttribute('data-entry-field');
      if (!current) return;
      const idx = order.indexOf(current);
      if (idx < 0 || idx === order.length - 1) return;
      e.preventDefault();
      focusFieldByName(order[idx + 1]);
    }
  };

  // Global Cmd/Ctrl+Z listener scoped to "undo buffer still populated" and
  // to the owning form — so we don't swallow the browser's native undo when
  // the user is focused outside of the transaction entry row.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z') {
        if (!undoBufferRef.current) return;
        if (!formRef.current?.contains(document.activeElement)) return;
        e.preventDefault();
        void undoLastSave();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [undoLastSave]);

  // Lifted so the rate-missing gate stays in one place — duplicating the
  // condition across `error` and `disabled` lets the two branches drift
  // apart on any future refinement (e.g. stricter `rate <= 0` check).
  // Gated on `!currenciesLoading` so that during the initial fetch — when
  // the hook returns `baseCode = DEFAULT_CURRENCY` and an empty list — a
  // sticky non-USD preference from localStorage does not flash a spurious
  // "No rate configured" error and briefly disable Save before the real
  // base and rate land.
  // `useWatch` rather than `form.watch` so React Compiler can memoize the
  // surrounding component — `form.watch` returns a non-stable function that
  // the compiler refuses to touch (react-hooks/incompatible-library warning).
  const watchedCurrency = useWatch({ control: form.control, name: 'currency' });
  // Which word the sign toggle wears. It follows the CATEGORY the row is being
  // filed under, because that is what decides whether a negative amount is
  // money coming back (a refund on an expense) or income being taken back (a
  // reversal). Before a category is picked the row is an expense as far as the
  // form is concerned, and "Refund" is the case v1 promotes.
  const watchedCategoryId = useWatch({
    control: form.control,
    name: 'category_id',
  });
  const entryType: TransactionType =
    categories.find((c) => c.id === watchedCategoryId)?.type ?? TYPE_EXPENSE;
  const hasNoRate =
    !currenciesLoading &&
    watchedCurrency !== baseCode &&
    rateFor(watchedCurrency) == null;

  return (
    <Card className="p-4">
      <Form {...form}>
        <form
          ref={formRef}
          onSubmit={(e) => {
            void form.handleSubmit(submit)(e);
          }}
          onKeyDown={handleKeyDown}
          // `items-start` (not `items-end`) so the AmountCurrencyInput's
          // optional preview/error row can extend downward without shifting
          // peer fields upward. Every field's label → control stack starts
          // at the same top edge; extra descent in the Amount column stays
          // below that baseline.
          className="flex flex-wrap items-start gap-3"
          noValidate
        >
          <FormField
            control={form.control}
            name="date"
            render={({ field }) => (
              <FormItem className="w-36">
                <EntryLabel>Date</EntryLabel>
                <FormControl>
                  <Input
                    type="date"
                    data-entry-field="date"
                    {...field}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="amount"
            render={({ field }) => (
              <FormItem className="w-56">
                <EntryLabel>Amount</EntryLabel>
                <FormControl>
                  <AmountCurrencyInput
                    value={field.value}
                    onValueChange={(v) => field.onChange(v)}
                    currency={watchedCurrency}
                    onCurrencyChange={(code) =>
                      form.setValue('currency', code, { shouldValidate: true })
                    }
                    baseCode={baseCode}
                    currencies={list}
                    hideInactive={true}
                    rateFor={rateFor}
                    loading={currenciesLoading}
                    error={
                      hasNoRate
                        ? 'No rate configured for this currency. Set one in Settings.'
                        : null
                    }
                    dataEntryField="amount"
                    inputRef={(el) => {
                      field.ref(el);
                      amountRef.current = el;
                    }}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="refund"
            render={({ field }) => (
              <FormItem className="space-y-2">
                {/* Same invisible-label + fixed-height-box trick the submit
                    button's column uses: under `items-start` a control with no
                    label of its own would sit a label's height above its
                    peers. `coarse:h-11` tracks the Input primitive's touch
                    floor for the same reason it does there. */}
                <Label aria-hidden="true">&nbsp;</Label>
                <div className="flex h-10 items-center coarse:h-11">
                  <AmountSignToggle
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    type={entryType}
                  />
                </div>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="description"
            render={({ field }) => (
              <FormItem className="flex-1 min-w-[12rem]">
                <EntryLabel>Description</EntryLabel>
                <FormControl>
                  <AutocompleteInput
                    data-entry-field="description"
                    suggestions={descriptionSuggestions}
                    onAccept={(v) => field.onChange(v)}
                    {...field}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="category_id"
            render={({ field }) => (
              <FormItem className="w-48">
                <EntryLabel>Category</EntryLabel>
                <Popover open={catOpen} onOpenChange={setCatOpen}>
                  <PopoverTrigger asChild>
                    <Button
                      type="button"
                      variant="outline"
                      className="w-full justify-start font-normal"
                      data-entry-field="category_id"
                    >
                      {categoryNameById(field.value) ?? 'Select category'}
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent className="p-0" align="start">
                    <Command>
                      <CommandInput placeholder="Search category..." />
                      <CommandList>
                        <CommandEmpty>No category found.</CommandEmpty>
                        {categories.map((cat) => (
                          <CommandItem
                            key={cat.id}
                            value={cat.name}
                            onSelect={() => {
                              field.onChange(cat.id);
                              setCatOpen(false);
                              focusFieldByName('tags');
                            }}
                          >
                            <CategoryBadge category={cat} />
                            <span className="ml-2">{cat.name}</span>
                          </CommandItem>
                        ))}
                      </CommandList>
                    </Command>
                  </PopoverContent>
                </Popover>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="tags"
            render={({ field }) => (
              <FormItem className="w-56">
                {/* EntryLabel's htmlFor targets the FormItem id; TagInput
                    doesn't forward that id to its inner input, so label
                    clicks don't focus — acceptable because TagInput has
                    its own click-to-focus handler on the wrapper. Using
                    EntryLabel here (rather than a nested <label>) makes
                    the label's inline line-box metrics match peer
                    FormItems so the TagInput's top edge aligns with
                    peer Inputs under items-start. */}
                <EntryLabel>Tags</EntryLabel>
                <TagInput
                  value={field.value}
                  onChange={field.onChange}
                  placeholder="Add tags..."
                  suggestions={tagSuggestions}
                />
              </FormItem>
            )}
          />

          {/*
            Invisible Label placeholder keeps the submit button's column
            aligned with peer FormItems under `items-start`. Must exactly
            mirror the peer FormItem structure — an inline shadcn <Label>
            followed by a BLOCK-level wrapper around the control — so the
            line-box math matches. The inner wrapper is `h-10 flex
            items-center` so the h-8 Button is vertically centered inside
            a 40px box matching peer Input height; otherwise the Button's
            top would align with the Input's top and leave an 8px gap
            below, visually floating the Button toward the top half of
            the Input row.
          */}
          <div className="space-y-2">
            <Label aria-hidden="true">&nbsp;</Label>
            {/* `coarse:h-11` tracks the Input primitive's pointer-gated floor —
                peer inputs are 44px there, and a 40px box would float the
                button ~2px above their midline. */}
            <div className="flex h-10 items-center coarse:h-11">
              <Button
                type="submit"
                size="sm"
                className="h-8 text-xs"
                // Disabled for the in-flight window: a second click there mints
                // a second key, which is a real duplicate rather than a replay.
                disabled={currenciesLoading || hasNoRate || isSending}
                aria-busy={isSending}
              >
                {isSending ? 'Saving…' : 'Add'}
              </Button>
            </div>
          </div>
        </form>
      </Form>
    </Card>
  );
}
