import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react';
import { format, parseISO } from 'date-fns';
import { MoreHorizontal, User } from 'lucide-react';
import type { Transaction, Category } from '../api/types';
import { AmountDisplay } from './AmountDisplay';
import { AmountCurrencyInput } from './AmountCurrencyInput';
import { AmountSignToggle } from './AmountSignToggle';
import { AutocompleteInput } from './AutocompleteInput';
import { CategoryBadge } from './CategoryBadge';
import { TagInput } from './TagInput';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Checkbox } from '@/components/ui/checkbox';
import { TableCell, TableRow } from '@/components/ui/table';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';
import { transactionLabel } from '@/lib/transaction-label';
import { useTransactionEditForm } from '@/hooks/useTransactionEditForm';
import type { UpdateTransactionInput } from '@/hooks/useTransactions';

export interface TransactionRowProps {
  transaction: Transaction;
  categories: Category[];
  selected?: boolean;
  onSelect?: (id: number, checked: boolean) => void;
  onUpdate: (input: UpdateTransactionInput) => Promise<void>;
  onDelete: (id: number) => Promise<void>;
  onError: (message: string) => void;
  descriptionSuggestions?: string[];
  tagSuggestions?: string[];
}

export function TransactionRow({
  transaction,
  categories,
  selected,
  onSelect,
  onUpdate,
  onDelete,
  onError,
  descriptionSuggestions = [],
  tagSuggestions = [],
}: TransactionRowProps) {
  // Import bypasses the description checks, so a row can carry an empty one;
  // `Select ` naming no object is the result. Shared with the phone card so
  // one selection test covers whichever presentation is mounted.
  const label = transactionLabel(transaction);
  const [editing, setEditing] = useState(false);
  // True only between a menu item running and the close it causes — the
  // actions menu's onCloseAutoFocus reads and clears it to decide whether
  // Radix's focus-to-trigger restore may run. A ref, not state: it must be
  // visible to the close event of the SAME interaction, and re-rendering
  // over a menu that is mid-close buys nothing.
  const menuActionRanRef = useRef(false);
  // The two ends of the edit swap. Both elements are mounted by the branch the
  // swap is moving TO, so neither can be focused from the handler that starts
  // it — the effect below runs after the render that puts them there.
  const dateInputRef = useRef<HTMLInputElement>(null);
  const actionsTriggerRef = useRef<HTMLButtonElement>(null);
  // Distinguishes "the form closed because the user left it" from the first
  // render of a row that was never editing, which must not steal focus.
  const returnFocusRef = useRef(false);
  const stopEditing = useCallback(() => {
    returnFocusRef.current = true;
    setEditing(false);
  }, []);
  // Field state, the currency rehydration race, the save payload and the
  // storedMoney contract all live in the shared hook — the phone edit Sheet
  // runs on the same one, and every one of those is a place two copies would
  // drift apart. This component owns only the layout below.
  const {
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
    reset: resetEditFields,
    save: handleSave,
    amountProps,
    signProps,
    saveDisabled,
  } = useTransactionEditForm({
    transaction,
    onUpdate,
    onError,
    onSaved: stopEditing,
    // Only so the sign toggle can read the SELECTED category's type — the
    // picker below renders from this same list.
    categories,
  });

  // Reseed edit fields from the current `transaction` prop on every
  // Edit-open, not just mount. Without this, `useState` captures the
  // prop snapshot at first mount — so a parent refetch that lands
  // before the user first opens Edit leaves them editing stale data.
  function startEditing() {
    resetEditFields();
    setEditing(true);
  }

  function handleCancel() {
    resetEditFields();
    returnFocusRef.current = true;
    setEditing(false);
  }

  // The row's half of the focus handoff the actions menu deliberately declines
  // to make. Edit unmounts the trigger it was chosen from, so the menu opts out
  // of Radix's focus-to-trigger restore — which leaves focus on `<body>` unless
  // something moves it into the form that just replaced the row. Date first:
  // it is the form's leading field, the same order a Tab from the row would
  // take.
  //
  // Coming back, the trigger is a NEW element (the display row remounts), so
  // this cannot be done by remembering what was focused before. Only the two
  // deliberate exits arm it — a row that leaves edit mode because the page
  // refetched it away has nothing on screen to focus.
  useEffect(() => {
    if (editing) {
      dateInputRef.current?.focus();
      return;
    }
    if (!returnFocusRef.current) return;
    returnFocusRef.current = false;
    actionsTriggerRef.current?.focus();
  }, [editing]);

  function handleRowKeyDown(e: KeyboardEvent<HTMLTableRowElement>) {
    // Enter save path, collapsed: Cmd/Ctrl+Enter is force-bubbled by children,
    // plain Enter reaches us only when no child consumed it, and Shift/Alt
    // variants are non-standard and ignored defensively.
    if (e.key === 'Enter' && !e.shiftKey && !e.altKey) {
      e.preventDefault();
      e.stopPropagation();
      void handleSave();
      return;
    }
    // Plain Escape cancels. Shift/Alt/Meta/Ctrl variants are non-standard for
    // cancel and are ignored defensively so an accidental modifier chord does
    // not discard the row's edits.
    if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey) {
      e.preventDefault();
      e.stopPropagation();
      handleCancel();
      return;
    }
  }

  if (editing) {
    return (
      <TableRow className="[&>td]:align-top" onKeyDown={handleRowKeyDown}>
        <TableCell className="w-10">
          {/* h-10 flex wrapper vertically centers the 16px Checkbox
              inside a 40px box matching peer Input height, so under
              the row's [&>td]:align-top the checkbox center lines up
              with the first-line center of the Date / Description /
              Category / Tags / Amount inputs (not their top edge).
              `coarse:h-11` tracks the Input primitive's pointer-gated
              floor so the centering holds where inputs are 44px. */}
          <div className="flex h-10 items-center coarse:h-11">
            <Checkbox
              checked={selected}
              disabled={!onSelect}
              onCheckedChange={(v) => onSelect?.(transaction.id, v === true)}
              aria-label={`Select ${label}`}
            />
          </div>
        </TableCell>
        <TableCell>
          {/* Native `<input type="date">` has cross-browser key-swallowing quirks while
              its picker is open: Chrome/Edge and Firefox often do not bubble Enter/Esc
              out to React. That is documented and manually verified — do not attempt
              to force-normalize. Users close the picker (mouse or outside-click) and
              then press Enter/Esc in any other field to save/cancel. */}
          <Input
            ref={dateInputRef}
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
          />
        </TableCell>
        <TableCell>
          <AutocompleteInput
            suggestions={descriptionSuggestions}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            onAccept={(v) => setDescription(v)}
            aria-label="Description"
          />
        </TableCell>
        <TableCell>
          {/* Radix react-select ^2.2.6 does NOT `stopPropagation` on the
              Escape/Enter keydown it uses to close its own popover content,
              so without this capture wrapper the first Escape in an open
              Select would both close the Select AND cancel the row, and a
              first Enter on a highlighted option would both select the
              option AND save the row. We detect "is this Select open" via
              the trigger's own `aria-expanded` attribute — the trigger
              lives in this `<div>`'s subtree (the SelectContent portal
              does not), so querying `e.currentTarget` keeps the guard
              scoped to this wrapper's subtree and cannot be tricked by
              an unrelated open listbox elsewhere (another row, the
              touch autocomplete Command, etc.). Today that subtree
              holds exactly one combobox (the category Select); if a
              maintainer later nests another combobox here the guard
              will correctly suppress for it too. When the trigger is
              closed,
              `aria-expanded` flips to false, the query returns null, and
              Enter/Escape bubble normally so the user can save/cancel
              from the trigger with the keyboard. */}
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
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {categories.map((cat) => (
                  <SelectItem key={cat.id} value={String(cat.id)}>
                    {cat.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </TableCell>
        <TableCell>
          <TagInput
            value={tags}
            onChange={setTags}
            placeholder="Add tags..."
            suggestions={tagSuggestions}
          />
        </TableCell>
        <TableCell className="text-right font-mono tabular-nums">
          {/* `amountProps` carries the storedMoney freeze contract — see
              useTransactionEditForm. The create surfaces (TransactionEntryRow,
              QuickAdd) deliberately omit storedMoney, because a row that does
              not exist yet genuinely is priced at today's rate. */}
          <AmountCurrencyInput {...amountProps} />
          {/* In the amount cell rather than a column of its own: the table's
              seven columns are fixed by the header, and the sign belongs to
              the number above it anyway. `justify-end` because this column is
              right-aligned; `font-sans` comes from the toggle itself, which
              has to undo the cell's `font-mono`.

              `mt-3`, not the `mt-2` this shipped with: on a coarse pointer the
              Switch grows its hit area with a pseudo-element that reaches 10px
              past its border box on every side (ui/switch.tsx), so an 8px gap
              puts the top of that band 2px inside the amount input above it —
              a tap meant for the end of the number toggles the sign. 12px
              keeps the two targets disjoint. */}
          <AmountSignToggle {...signProps} className="mt-3 justify-end" />
        </TableCell>
        <TableCell>
          {/* `h-10` on the form matches peer TableCell input height so that
              under the row's [&>td]:align-top, items-center vertically
              centers the h-9 Save/Cancel buttons inside a 40px box —
              their centers line up with the first-line center of the
              Date / Description / Category / Tags / Amount inputs.

              Stays `h-10` on a coarse pointer, where Button's 44px touch floor
              makes those two 4px TALLER than this box. `items-center` keeps
              them centred on it, so the contract this comment is about — the
              centres agreeing with the inputs' first line — still holds, and
              the cost is 2px of symmetric overhang into TableCell's padding.
              Growing the box to 44px on a coarse pointer instead would move its
              centre to 22px while the peer Inputs stay at 20px, i.e. it would
              break the alignment in order to tidy the overhang. (Written out
              rather than as the class token it would be: Tailwind scans
              comments too, and naming a utility nothing uses emits a real,
              dead rule into the stylesheet.) */}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void handleSave();
            }}
            className="flex h-10 items-center justify-end gap-1 coarse:h-11"
          >
            {/* Same in-flight cue as the phone sheet. A dimmed control that
                still reads "Save" says "you cannot do this", not "this is
                happening" — and the two edit surfaces giving different
                feedback for the same request is the sibling asymmetry this
                slice kept turning up. Repo idiom: "Saving…" / "Resetting…". */}
            <Button
              type="submit"
              size="sm"
              disabled={saveDisabled}
              aria-busy={saving}
            >
              {saving ? 'Saving…' : 'Save'}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={handleCancel}
            >
              Cancel
            </Button>
          </form>
        </TableCell>
      </TableRow>
    );
  }

  return (
    <TableRow className={cn('hover:bg-muted/50', selected && 'bg-muted/50')}>
      <TableCell className="w-10">
        <Checkbox
          checked={selected}
          disabled={!onSelect}
          onCheckedChange={(v) => onSelect?.(transaction.id, v === true)}
          aria-label={`Select ${label}`}
        />
      </TableCell>
      <TableCell className="whitespace-nowrap">
        {format(parseISO(transaction.date), 'MMM d, yyyy')}
      </TableCell>
      {/* Width-bounded on purpose. Import does not enforce the 500-character
          description limit the rest of the app does — validateImportField runs
          only on the per-row edit route — so a spreadsheet cell can put a far
          longer description into the ledger. Unbounded, one such row stretches
          the table for every row and both members. title= keeps the full text
          reachable on hover.

          The creator rides UNDER the description rather than in a column of
          its own: the ledger is household-wide, so a member needs to know a
          row is her spouse's BEFORE she edits it and gets a 403 — but a
          seventh always-on column would cost width the 390px phone layout
          does not have. A second muted line costs row height instead, which
          the phone has. Not a tooltip: those are dead on touch. */}
      <TableCell className="max-w-md">
        <div className="truncate font-medium" title={transaction.description}>
          {transaction.description}
        </div>
        <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
          <User className="size-3.5 shrink-0" aria-hidden="true" />
          <span className="min-w-0 truncate">
            {/* A bare name in a muted line does not announce what it IS, and
                the icon is aria-hidden decoration. */}
            <span className="sr-only">Entered by </span>
            {transaction.created_by || 'Unknown'}
          </span>
        </p>
      </TableCell>
      <TableCell>
        <CategoryBadge
          category={{ id: transaction.category_id, name: transaction.category_name }}
        />
      </TableCell>
      <TableCell>
        {transaction.tags &&
          transaction.tags.split(',').map((tag, i) => (
            <Badge
              key={`${tag.trim()}-${i}`}
              variant="secondary"
              className="mr-1 font-normal"
            >
              {tag.trim()}
            </Badge>
          ))}
      </TableCell>
      <TableCell className="whitespace-nowrap text-right">
        <AmountDisplay
          amount={transaction.amount}
          originalAmount={transaction.original_amount}
          originalCurrency={transaction.original_currency}
          type={transaction.category_type}
          baseCode={baseCode}
        />
      </TableCell>
      <TableCell className="text-right">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              ref={actionsTriggerRef}
              type="button"
              variant="ghost"
              size="icon"
              className="size-8 data-[state=open]:bg-accent"
              aria-label={`Actions for ${label}`}
              /* The page's anchor for a delete the server REFUSED: the row
                 survives, so this trigger is still the right place to be, but
                 the menu has already opted out of restoring to it. Queried by
                 attribute because the page cannot reach a ref that lives in
                 here, and because the element is a fresh one per render. */
              data-txn-actions-id={transaction.id}
            >
              <MoreHorizontal />
              <span className="sr-only">Open menu</span>
            </Button>
          </DropdownMenuTrigger>
          {/* This menu opts out of Radix's focus-to-trigger restore ONLY on a
              close caused by one of its items running. Both items unmount the
              trigger they were opened from — Delete removes the row, Edit
              replaces it with the edit form — so on those closes the restore
              would aim at a disappearing element and, worse, override the
              deliberate move the page has already made (`onDelete` sends focus
              to the page heading precisely because the row is gone).

              A plain dismissal — Escape, click-outside — leaves the trigger
              mounted, and a blanket opt-out there stranded keyboard focus on
              <body> (measured on the built app at 1130 coarse). Hence the
              read-and-clear ref: only an item sets it, and clearing it on
              every close means a later dismissal restores again.

              The primitive itself no longer defaults the opt-out, because
              defaulting it dropped focus to <body> at the other eleven
              trigger sites, whose triggers survive their own action. */}
          <DropdownMenuContent
            align="end"
            onCloseAutoFocus={(e) => {
              if (!menuActionRanRef.current) return;
              menuActionRanRef.current = false;
              e.preventDefault();
            }}
          >
            <DropdownMenuItem
              onSelect={() => {
                menuActionRanRef.current = true;
                startEditing();
              }}
            >
              Edit
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              variant="destructive"
              onSelect={() => {
                menuActionRanRef.current = true;
                void onDelete(transaction.id);
              }}
            >
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </TableCell>
    </TableRow>
  );
}
