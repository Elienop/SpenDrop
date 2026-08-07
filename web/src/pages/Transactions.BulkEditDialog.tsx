import { useEffect } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';

import type { Category } from '../api/types';
import { MAX_DESCRIPTION_LENGTH } from '../lib/constants';
import { charCount } from '../lib/text';
import {
  computePatch,
  isPatchEmpty,
  type BulkEditFormValues,
  type ComputedPatchResult,
} from './Transactions.computePatch';

// Spec §4.2 — RHF + zod schema. Note: the cross-field rule "tagsMode required
// iff tags non-empty" is enforced by the disabled radios (UX) + computePatch
// (wire shape), so the schema itself is permissive on those two fields.
const bulkEditSchema = z.object({
  setDate: z.boolean(),
  date: z.string(),
  // CHARACTERS, counted with charCount rather than Zod's .max() — .max()
  // measures UTF-16 code units, which disagree with the server on emoji.
  description: z
    .string()
    .refine((value) => charCount(value) <= MAX_DESCRIPTION_LENGTH, {
      message: `Description must be ${MAX_DESCRIPTION_LENGTH} characters or fewer`,
    }),
  category_id: z.union([
    z.literal('noChange'),
    z.number().int().positive(),
  ]),
  tags: z.string(),
  tagsMode: z.enum(['add', 'remove', 'replace']),
});

type BulkEditSchema = z.infer<typeof bulkEditSchema>;

const NO_CHANGE = 'noChange' as const;

interface BulkEditDialogProps {
  open: boolean;
  onClose: () => void;
  count: number;
  categories: Category[];
  /**
   * Submit handler. May return void or a Promise — RHF tracks
   * `formState.isSubmitting` for the duration of a returned promise so the
   * Apply button and the Cmd/Ctrl+Enter chord can both refuse re-entry while
   * a previous submit is still in-flight (Task 9 will plug in an async
   * `bulkUpdate` mutation; the guard is in place ahead of that wiring).
   */
  onSubmit: (result: ComputedPatchResult) => void | Promise<void>;
}

export function BulkEditDialog({
  open,
  onClose,
  count,
  categories,
  onSubmit,
}: BulkEditDialogProps) {
  const form = useForm<BulkEditSchema>({
    resolver: zodResolver(bulkEditSchema),
    defaultValues: {
      setDate: false,
      date: '',
      description: '',
      category_id: NO_CHANGE,
      tags: '',
      tagsMode: 'add',
    },
  });

  // The dialog is mounted for the lifetime of the Transactions page — only
  // `open` toggles — so RHF state survives every close. Without this reset a
  // patch the user typed and then abandoned (Cancel, Escape, backdrop click)
  // stayed in the form and was silently re-applied the next time the dialog
  // was opened, against whatever selection was active by then: a different
  // set of rows, often a different member's, in a different month.
  //
  // Resetting on OPEN rather than on close is deliberate — it is the state
  // the user is about to see, so it stays correct even if a close path is
  // interrupted or a future caller forgets to route through onClose.
  useEffect(() => {
    if (open) form.reset();
  }, [open, form]);

  const values = form.watch();
  const computed = computePatch(values as BulkEditFormValues);
  const canSubmit = !isPatchEmpty(computed);
  const tagsEmpty = !values.tags.trim();
  const dateEnabled = values.setDate;
  const { isSubmitting } = form.formState;

  const submit = form.handleSubmit(async () => {
    // Re-compute at submit time off the latest values to avoid any stale-watch
    // edge case (defensive — the watch above already flushed pre-submit).
    const result = computePatch(form.getValues() as BulkEditFormValues);
    if (isPatchEmpty(result)) return;
    // Awaiting onSubmit lets RHF flip `formState.isSubmitting` for the
    // duration — the Apply button + Cmd/Ctrl+Enter chord both observe this
    // to refuse re-entry while a parent-side async submit is in-flight.
    await onSubmit(result);
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
    >
      <DialogContent className="md:max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            Edit {count} transaction{count === 1 ? '' : 's'}
          </DialogTitle>
          <DialogDescription>
            Only the fields you change are applied. Leave a field untouched to
            keep its current value on every selected transaction.
          </DialogDescription>
        </DialogHeader>
        <form
          onSubmit={submit}
          onKeyDown={(e) => {
            // Spec §4.4 — Cmd/Ctrl+Enter submits from any focused field.
            // Mirror the Apply button's enablement rule (canSubmit AND not
            // already in-flight) so the chord never bypasses the disabled
            // state during an async submit.
            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
              e.preventDefault();
              if (canSubmit && !isSubmitting) submit();
            }
          }}
          className="grid grid-cols-1 md:grid-cols-[120px_1fr_140px_1fr] gap-4"
        >
          {/* --- Date column with "Set date" toggle --- */}
          <div className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
              <Controller
                name="setDate"
                control={form.control}
                render={({ field }) => (
                  <Checkbox
                    id="bulk-set-date"
                    checked={field.value}
                    onCheckedChange={(c) => field.onChange(c === true)}
                  />
                )}
              />
              <Label htmlFor="bulk-set-date" className="text-xs">
                Set date
              </Label>
            </div>
            <Label htmlFor="bulk-date" className="sr-only">
              Date
            </Label>
            <Input
              id="bulk-date"
              type="date"
              {...form.register('date')}
              disabled={!dateEnabled}
            />
          </div>

          {/* --- Description --- */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="bulk-desc">Description</Label>
            <Input
              id="bulk-desc"
              type="text"
              placeholder="— Keep same —"
              {...form.register('description')}
            />
            {/* Without this, an over-long description makes Apply do nothing at
                all: react-hook-form refuses the submit, no field in this dialog
                renders an error, and the user is left clicking a button that
                silently no-ops. */}
            {form.formState.errors.description && (
              <p className="text-sm text-destructive" role="alert">
                {form.formState.errors.description.message}
              </p>
            )}
          </div>

          {/* --- Category --- */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="bulk-cat">Category</Label>
            <Controller
              name="category_id"
              control={form.control}
              render={({ field }) => (
                <Select
                  value={
                    field.value === NO_CHANGE ? NO_CHANGE : String(field.value)
                  }
                  onValueChange={(v) =>
                    field.onChange(v === NO_CHANGE ? NO_CHANGE : Number(v))
                  }
                >
                  <SelectTrigger id="bulk-cat" aria-label="Category">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={NO_CHANGE}>— No change —</SelectItem>
                    {categories.map((c) => (
                      <SelectItem key={c.id} value={String(c.id)}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            />
          </div>

          {/* --- Tags column: input on the same row as the other inputs;
               mode radio drops below so the four input controls align
               horizontally. --- */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="bulk-tags">Tags</Label>
            <Input
              id="bulk-tags"
              type="text"
              placeholder="comma,separated"
              {...form.register('tags')}
            />
            <Controller
              name="tagsMode"
              control={form.control}
              render={({ field }) => (
                <RadioGroup
                  value={field.value}
                  onValueChange={field.onChange}
                  className="flex gap-3 text-xs"
                  aria-labelledby="bulk-tags-mode-legend"
                >
                  <span id="bulk-tags-mode-legend" className="sr-only">
                    Tag operation
                  </span>
                  <div className="flex items-center gap-1">
                    <RadioGroupItem
                      value="add"
                      id="bulk-tags-mode-add"
                      disabled={tagsEmpty}
                    />
                    <Label htmlFor="bulk-tags-mode-add">Add</Label>
                  </div>
                  <div className="flex items-center gap-1">
                    <RadioGroupItem
                      value="remove"
                      id="bulk-tags-mode-remove"
                      disabled={tagsEmpty}
                    />
                    <Label htmlFor="bulk-tags-mode-remove">Remove</Label>
                  </div>
                  <div className="flex items-center gap-1">
                    <RadioGroupItem
                      value="replace"
                      id="bulk-tags-mode-replace"
                      disabled={tagsEmpty}
                    />
                    <Label htmlFor="bulk-tags-mode-replace">Replace</Label>
                  </div>
                </RadioGroup>
              )}
            />
          </div>

          <DialogFooter className="md:col-span-4 flex justify-end gap-2 mt-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            {/*
              Spec §4.4 asked for a fuller sentence than the visible text, and
              it was implemented as an aria-label — but that made the
              accessible name "Apply changes to N transactions" over a visible
              "Apply to N", which does not contain the visible label and so
              fails WCAG 2.5.3. Speech control ("click Apply to 12") then
              matches nothing. Spelling the noun out visibly satisfies both:
              the name is a full phrase AND it is what the user can see. Same
              change as the confirmation dialog's action button.
            */}
            <Button type="submit" disabled={!canSubmit || isSubmitting}>
              Apply to {count} transaction{count === 1 ? '' : 's'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
