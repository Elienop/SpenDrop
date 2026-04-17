import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent,
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
import type { Category, Transaction } from '../api/types';
import { formatYYYYMMDD } from '@/lib/dates';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import { toCreatePayload } from '@/lib/currency';
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
  amount: z.number().positive('> 0'),
  currency: z.string().regex(/^[A-Z]{3}$/, 'Invalid currency'),
  description: z.string().min(1, 'required').max(200),
  category_id: z.number().int().positive('required'),
  tags: z.string(),
});
type EntryFormValues = z.infer<typeof entrySchema>;

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

  const { list, baseCode, rateFor, loading: currenciesLoading } = useCurrencies();

  const form = useForm<EntryFormValues>({
    resolver: zodResolver(entrySchema),
    defaultValues: {
      date: getLastDate(),
      amount: 0,
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
    setTimeout(() => amountRef.current?.focus(), 0);
  }, [onDelete, form]);

  const submit = useCallback(
    async (values: EntryFormValues) => {
      let payload: CreateTransactionInput;
      try {
        // Union-to-optional widening: toCreatePayload returns a discriminated
        // union (collapsed vs. expanded shape), while CreateTransactionInput
        // declares original_amount / original_currency as optional. Both
        // branches are structurally compatible at runtime — the cast only
        // tells the type system that the optional-field form subsumes both.
        payload = toCreatePayload(values, baseCode, rateFor) as CreateTransactionInput;
      } catch {
        toast.error('Failed to save transaction');
        return;
      }
      let saved: Transaction;
      try {
        saved = await onSubmit(payload);
      } catch {
        toast.error('Failed to save transaction');
        // Leave the form untouched so the user can retry. Do not clear
        // undoBufferRef — a previous successful save's undo buffer, if any,
        // stays valid because nothing new was saved.
        return;
      }
      saveLastCategory(values.category_id);
      saveLastDate(values.date);
      saveLastCurrency(values.currency);
      undoBufferRef.current = { saved, values };

      toast.success('Transaction saved', {
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

      form.reset({
        date: values.date,
        amount: 0,
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
      setTimeout(() => amountRef.current?.focus(), 0);
    },
    [onSubmit, form, undoLastSave, baseCode, rateFor],
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
            Invisible Label placeholder keeps the submit button's top edge
            aligned with peer Inputs under `items-start`. Must exactly
            mirror the peer FormItem structure — an inline shadcn <Label>
            followed by a BLOCK-level wrapper around the control — so the
            line-box math matches. Wrapping <Button> in a <div> forces a
            new line after the inline label (Button itself is inline-flex
            and would otherwise sit on the same baseline as the label).
            Without the wrapper div the label and button share a line box
            and the button renders ~20px above peer control tops.
          */}
          <div className="space-y-2">
            <Label aria-hidden="true">&nbsp;</Label>
            <div>
              <Button
                type="submit"
                size="sm"
                className="h-8 text-xs"
                disabled={currenciesLoading || hasNoRate}
              >
                Add
              </Button>
            </div>
          </div>
        </form>
      </Form>
    </Card>
  );
}
