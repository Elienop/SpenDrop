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
  /**
   * Where focus goes when the sheet closes on any path OTHER than delete.
   *
   * Required, not optional: this sheet has no `SheetTrigger`, so Radix has
   * nothing to restore focus to and every close would otherwise land on
   * `<body>`. Making the caller supply a target is what stops that being
   * silently re-introduced.
   */
  onCloseFocus: () => void;
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
  onCloseFocus,
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

  // Selects between the two real focus destinations below. Load-bearing, not
  // a guard against Radix.
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
          // Radix's own restore is a NO-OP here, and it is worth being precise
          // about why rather than assuming it works: DialogContentModal
          // composes `preventDefault(); context.triggerRef.current?.focus()`
          // (@radix-ui/react-dialog), and `triggerRef` is only populated by a
          // `SheetTrigger`. This sheet is opened programmatically from a card
          // tap and has no trigger, so there is nothing to focus and every
          // close — save, cancel, Escape, overlay — lands on <body> unless
          // something here says otherwise.
          //
          // `preventDefault` is belt-and-braces (Radix already calls it) so
          // the outcome does not depend on that continuing to be true.
          e.preventDefault();
          if (deletingRef.current) {
            // The row is on its way out. Focusing the card that is about to
            // unmount would just move focus somewhere doomed; the page parks
            // it on the heading once the delete resolves.
            deletingRef.current = false;
            return;
          }
          onCloseFocus();
        }}
      >
        <SheetHeader>
          <SheetTitle>Edit transaction</SheetTitle>
          <SheetDescription>
            Change this transaction, or move it to Trash.
          </SheetDescription>
        </SheetHeader>
        {rendered != null && (
          // Keyed by id so a refetch replacing the same row keeps the user's
          // in-progress edits while still feeding the fresh values to the
          // storedMoney comparison. The key cannot currently CHANGE in place:
          // the sheet is modal, so there is no way to open a different row
          // without closing this one first, and `rendered` only ever advances
          // to a different id across a close. It is written this way so that
          // stays true if a non-modal or swipe-between-rows variant ever
          // lands, not because a live swap happens today.
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
  extends Omit<TransactionEditSheetProps, 'transaction' | 'onCloseFocus'> {
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
    // `markDeleting` before `onClose` so the shell reads the flag during the
    // close it is about to perform. This does not change where focus ENDS UP —
    // the page's heading focus runs last either way — it avoids focus
    // transiting through a card that is about to unmount, which a screen
    // reader would announce on its way past.
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
