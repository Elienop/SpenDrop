import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';

import {
  Dialog,
  DialogContent,
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
  description: z.string().max(500),
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
  onSubmit: (result: ComputedPatchResult) => void;
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

  const values = form.watch();
  const computed = computePatch(values as BulkEditFormValues);
  const canSubmit = !isPatchEmpty(computed);
  const tagsEmpty = !values.tags.trim();
  const dateEnabled = values.setDate;

  const submit = form.handleSubmit(() => {
    // Re-compute at submit time off the latest values to avoid any stale-watch
    // edge case (defensive — the watch above already flushed pre-submit).
    const result = computePatch(form.getValues() as BulkEditFormValues);
    if (isPatchEmpty(result)) return;
    onSubmit(result);
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
          <DialogTitle>Edit {count} transactions</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={submit}
          onKeyDown={(e) => {
            // Spec §4.4 — Cmd/Ctrl+Enter submits from any focused field.
            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
              e.preventDefault();
              if (canSubmit) submit();
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

          {/* --- Tags column with mode radio above --- */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="bulk-tags">Tags</Label>
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
            <Input
              id="bulk-tags"
              type="text"
              placeholder="comma,separated"
              {...form.register('tags')}
            />
          </div>

          <DialogFooter className="md:col-span-4 flex justify-end gap-2 mt-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={!canSubmit}
              // Spec §4.4 — full sentence for screen readers, compact visible
              // text. The aria-label leads with "Apply to N transactions" so
              // queries by visible-text regex still match the accessible name.
              aria-label={`Apply to ${count} transactions`}
            >
              Apply to {count}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
