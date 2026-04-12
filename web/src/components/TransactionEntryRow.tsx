import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react';
import { useForm } from 'react-hook-form';
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
import type { Category, Transaction } from '../api/types';
import { formatYYYYMMDD } from '@/lib/dates';
import { STORAGE_KEYS } from '@/lib/storage-keys';

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
  description: z.string().min(1, 'required').max(200),
  category_id: z.number().int().positive('required'),
  tags: z.string(),
});
export type EntryFormValues = z.infer<typeof entrySchema>;

export interface TransactionEntryRowProps {
  categories: Category[];
  onSubmit: (input: EntryFormValues) => Promise<Transaction>;
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

  const form = useForm<EntryFormValues>({
    resolver: zodResolver(entrySchema),
    defaultValues: {
      date: getLastDate(),
      amount: 0,
      description: '',
      category_id: getLastCategoryId(),
      tags: '',
    },
    mode: 'onSubmit',
  });

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
    amountRef.current?.focus();
  }, [onDelete, form]);

  const submit = useCallback(
    async (values: EntryFormValues) => {
      let saved: Transaction;
      try {
        saved = await onSubmit(values);
      } catch {
        toast.error('Failed to save transaction');
        // Leave the form untouched so the user can retry. Do not clear
        // undoBufferRef — a previous successful save's undo buffer, if any,
        // stays valid because nothing new was saved.
        return;
      }
      saveLastCategory(values.category_id);
      saveLastDate(values.date);
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
        description: '',
        category_id: values.category_id,
        tags: '',
      });
      amountRef.current?.focus();
    },
    [onSubmit, form, undoLastSave],
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
      const order: string[] = ['date', 'amount', 'description', 'category_id'];
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

  return (
    <Card className="p-4">
      <Form {...form}>
        <form
          ref={formRef}
          onSubmit={(e) => {
            void form.handleSubmit(submit)(e);
          }}
          onKeyDown={handleKeyDown}
          className="flex flex-wrap items-end gap-3"
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
              <FormItem className="w-32">
                <EntryLabel>Amount</EntryLabel>
                <FormControl>
                  <Input
                    type="number"
                    step="0.01"
                    min="0"
                    data-entry-field="amount"
                    className="font-mono tabular-nums"
                    name={field.name}
                    onBlur={(e) => {
                      if (e.target.value === '') field.onChange(0);
                      field.onBlur();
                    }}
                    value={field.value || ''}
                    onChange={(e) =>
                      field.onChange(
                        e.target.value === '' ? 0 : Number(e.target.value),
                      )
                    }
                    ref={(el) => {
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
                {/*
                  Nested-label pattern: wrapping a <label> around the input
                  avoids the htmlFor/id round-trip that shadcn's FormLabel
                  expects. Clicking the visible "Tags" text focuses the
                  first labelable descendant (TagInput's internal input)
                  automatically, and TagInput keeps its own aria-label
                  for screen readers. Do NOT replace with <FormLabel>
                  unless TagInput also accepts an id prop.
                */}
                <label className="text-sm font-medium leading-none">
                  <span className="mb-2 block">Tags</span>
                  <TagInput
                    value={field.value}
                    onChange={field.onChange}
                    placeholder="Add tags..."
                    suggestions={tagSuggestions}
                  />
                </label>
              </FormItem>
            )}
          />

          <Button type="submit" size="sm" className="h-8 text-xs">
            Add
          </Button>
        </form>
      </Form>
    </Card>
  );
}
