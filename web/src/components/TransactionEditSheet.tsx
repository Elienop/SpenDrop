import { useId, useRef, useState } from 'react';
import { Trash2 } from 'lucide-react';
import type { Category, Transaction } from '../api/types';
import { AmountCurrencyInput } from './AmountCurrencyInput';
import { AutocompleteInput } from './AutocompleteInput';
import { TagInput } from './TagInput';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { useTransactionEditForm } from '@/hooks/useTransactionEditForm';
import type { UpdateTransactionInput } from '@/hooks/useTransactions';

export interface TransactionEditSheetProps {
  /** The row being edited, or null when the sheet is closed. */
  transaction: Transaction | null;
  categories: Category[];
  onClose: () => void;
  onUpdate: (input: UpdateTransactionInput) => Promise<void>;
  /** The page's row-delete choreography — undo toast and heading refocus. */
  onDelete: (id: number) => Promise<void>;
  onError: (message: string) => void;
  descriptionSuggestions?: string[];
  tagSuggestions?: string[];
}

/**
 * The phone edit surface. The desktop table expands a row into inline inputs
 * across its seven columns; a 390px viewport has no columns to expand into, so
 * a tap on a card opens the same fields here instead.
 *
 * Delete lives in this sheet because the card has no per-row actions menu —
 * the whole card is one tap target. It runs the page's `onDelete`, so the undo
 * toast and the heading refocus are the same ones the table path gets.
 */
export function TransactionEditSheet({
  transaction,
  categories,
  onClose,
  onUpdate,
  onDelete,
  onError,
  descriptionSuggestions = [],
  tagSuggestions = [],
}: TransactionEditSheetProps) {
  // Radix keeps the panel mounted through its close animation, but the page
  // has already dropped the row by then. Holding the last non-null value keeps
  // the fields on screen while the sheet slides out instead of flashing an
  // empty panel. Adjusting state during render is the pattern React documents
  // for this ("Adjusting state when a prop changes") and the one
  // AmountCurrencyInput already uses.
  const [rendered, setRendered] = useState(transaction);
  if (transaction != null && transaction !== rendered) {
    setRendered(transaction);
  }

  // Set for the one close that must NOT restore focus the way every other
  // close does — see onCloseAutoFocus below.
  const deletingRef = useRef(false);

  return (
    <Sheet
      open={transaction != null}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent
        side="right"
        className="w-full sm:max-w-md"
        onCloseAutoFocus={(e) => {
          // Radix restores focus to the element that opened the sheet. On the
          // SAVE path that is the card, which is still there, and the restore
          // is right. On the DELETE path the card is being unmounted, so the
          // restore lands on nothing and focus falls to <body> — and because
          // FocusScope runs it after the close animation, it happens AFTER the
          // page's own heading refocus and silently undoes it. Declining the
          // restore leaves the page's deliberate target standing.
          if (deletingRef.current) {
            deletingRef.current = false;
            e.preventDefault();
          }
        }}
      >
        <SheetHeader>
          <SheetTitle>Edit transaction</SheetTitle>
          <SheetDescription>
            Change this transaction, or move it to Trash.
          </SheetDescription>
        </SheetHeader>
        {rendered != null && (
          // Keyed by id so switching rows starts a clean form, while a refetch
          // that replaces the same row keeps the user's in-progress edits and
          // still feeds the fresh values to the storedMoney comparison.
          <TransactionEditSheetForm
            key={rendered.id}
            transaction={rendered}
            categories={categories}
            onClose={onClose}
            onUpdate={onUpdate}
            onDelete={onDelete}
            onError={onError}
            descriptionSuggestions={descriptionSuggestions}
            tagSuggestions={tagSuggestions}
            markDeleting={() => {
              deletingRef.current = true;
            }}
          />
        )}
      </SheetContent>
    </Sheet>
  );
}

interface TransactionEditSheetFormProps
  extends Omit<TransactionEditSheetProps, 'transaction'> {
  transaction: Transaction;
  /** Tells the shell that the close about to happen is the delete one. */
  markDeleting: () => void;
}

function TransactionEditSheetForm({
  transaction,
  categories,
  onClose,
  onUpdate,
  onDelete,
  onError,
  descriptionSuggestions = [],
  tagSuggestions = [],
  markDeleting,
}: TransactionEditSheetFormProps) {
  const fieldId = useId();
  const {
    date,
    setDate,
    description,
    setDescription,
    categoryId,
    setCategoryId,
    tags,
    setTags,
    save,
    saving,
    amountProps,
    saveDisabled,
  } = useTransactionEditForm({
    transaction,
    onUpdate,
    onError,
    onSaved: onClose,
  });

  async function handleDelete() {
    // Order matters twice over. `markDeleting` first, because the shell reads
    // that flag during the close it is about to perform. Then close, because
    // `onDelete` parks focus on the page heading once the row is gone and the
    // sheet's focus trap would otherwise pull it straight back into a panel
    // that is unmounting.
    markDeleting();
    onClose();
    await onDelete(transaction.id);
  }

  return (
    <form
      className="mt-6 flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <div className="flex flex-col gap-2">
        <Label htmlFor={`${fieldId}-date`}>Date</Label>
        <Input
          id={`${fieldId}-date`}
          type="date"
          value={date}
          onChange={(e) => setDate(e.target.value)}
        />
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor={`${fieldId}-description`}>Description</Label>
        <AutocompleteInput
          id={`${fieldId}-description`}
          suggestions={descriptionSuggestions}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          onAccept={(v) => setDescription(v)}
        />
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor={`${fieldId}-category`}>Category</Label>
        {/*
          Radix react-select does NOT stopPropagation on the Escape keydown it
          uses to close its own popover, so without this capture wrapper the
          first Escape inside an open Select would close the Select AND dismiss
          the whole sheet, discarding every edit. Same guard the inline table
          row carries, detecting "is this Select open" via the trigger's own
          aria-expanded — the trigger is in this subtree, the portalled content
          is not.
        */}
        <div
          onKeyDownCapture={(e) => {
            if (e.key !== 'Escape' && e.key !== 'Enter') return;
            const openTrigger = e.currentTarget.querySelector(
              '[role="combobox"][aria-expanded="true"]',
            );
            if (openTrigger) e.stopPropagation();
          }}
        >
          <Select value={categoryId} onValueChange={setCategoryId}>
            <SelectTrigger id={`${fieldId}-category`}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {categories.map((cat) => (
                  <SelectItem key={cat.id} value={String(cat.id)}>
                    {cat.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        {/* TagInput forwards no id, so this Label carries no htmlFor — the
            inner input is self-labeled "Add tag" and TagInput has its own
            click-to-focus on the wrapper. Same arrangement as the entry row. */}
        <Label>Tags</Label>
        <TagInput
          value={tags}
          onChange={setTags}
          placeholder="Add tags..."
          suggestions={tagSuggestions}
        />
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor={`${fieldId}-amount`}>Amount</Label>
        {/* `amountProps` carries the storedMoney freeze contract in one piece:
            re-saving a foreign row whose amount and currency are untouched must
            keep the base value the row already stores, not re-price it at
            today's rate. See useTransactionEditForm. */}
        <AmountCurrencyInput id={`${fieldId}-amount`} {...amountProps} />
      </div>

      {/*
        The footer scrolls with the form rather than being pinned: SheetContent
        wraps its children in the scroll box, so a sticky footer here would sit
        inside the scrolling area and pin to nothing. At six fields the buttons
        are reachable without scrolling on a 390x844 phone.
      */}
      <div className="mt-2 flex flex-col gap-3 border-t border-border pt-4">
        <div className="flex gap-2">
          {/* A dimmed button that still reads "Save" says "you cannot do this",
              not "this is happening". The repo's idiom for the difference is a
              present-participle label (QuickAdd's "Saving…", Settings'
              "Resetting…"), and aria-busy carries the same thing to a reader
              who is not looking at the dimming. */}
          <Button
            type="submit"
            className="min-h-11 flex-1"
            disabled={saveDisabled}
            aria-busy={saving}
          >
            {saving ? 'Saving…' : 'Save'}
          </Button>
          <Button
            type="button"
            variant="outline"
            className="min-h-11 flex-1"
            onClick={onClose}
          >
            Cancel
          </Button>
        </div>
        <Button
          type="button"
          variant="destructive"
          className="min-h-11"
          onClick={() => void handleDelete()}
        >
          <Trash2 aria-hidden="true" />
          Delete transaction
        </Button>
      </div>
    </form>
  );
}
