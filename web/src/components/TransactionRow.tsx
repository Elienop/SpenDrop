import { useState, useEffect, useRef, type KeyboardEvent } from 'react';
import { format } from 'date-fns';
import { MoreHorizontal, User } from 'lucide-react';
import type { Transaction, Category } from '../api/types';
import { AmountDisplay } from './AmountDisplay';
import { AmountCurrencyInput } from './AmountCurrencyInput';
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
import { useCurrencies } from '@/hooks/useCurrencies';
import { toCreatePayload, toEditDefaults } from '@/lib/currency';
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
  const { list: currencies, baseCode, rateFor, loading: currenciesLoading } = useCurrencies();
  const [editing, setEditing] = useState(false);
  const [date, setDate] = useState(transaction.date);
  // `baseCode` is `DEFAULT_CURRENCY` ("USD") until the useCurrencies fetch
  // resolves. For rows with original_* === null and a non-USD household
  // base, the initial defaults capture "USD"; the didInitEditCurrency
  // effect below rehydrates once the fetch lands.
  const initialDefaults = toEditDefaults(transaction, baseCode);
  const [editAmount, setEditAmount] = useState<number>(initialDefaults.amount);
  const [editCurrency, setEditCurrency] = useState<string>(initialDefaults.currency);
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
    // cannot cascade. Suppressed per-line rather than by widening the config
    // override to `src/components/**`, which would silence the rule for the
    // many components that have no async source at all.
    if (resolved.currency !== editCurrency) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setEditCurrency(resolved.currency);
    }
    if (resolved.amount !== editAmount) {
      setEditAmount(resolved.amount);
    }
    // editAmount/editCurrency intentionally excluded: this effect must run
    // exactly once per mount, gated by didInitEditCurrency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currenciesLoading, baseCode, transaction]);

  async function handleSave() {
    setSaving(true);
    let payload: UpdateTransactionInput;
    try {
      const wire = toCreatePayload(
        {
          amount: editAmount,
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
      setEditing(false);
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Failed to save');
    } finally {
      setSaving(false);
    }
  }

  function resetEditFields() {
    const resolved = toEditDefaults(transaction, baseCode);
    setDate(transaction.date);
    setEditAmount(resolved.amount);
    setEditCurrency(resolved.currency);
    setDescription(transaction.description);
    setCategoryId(String(transaction.category_id));
    setTags(transaction.tags ?? '');
  }

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
    setEditing(false);
  }

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
              Category / Tags / Amount inputs (not their top edge). */}
          <div className="flex h-10 items-center">
            <Checkbox
              checked={selected}
              disabled={!onSelect}
              onCheckedChange={(v) => onSelect?.(transaction.id, v === true)}
              aria-label={`Select ${transaction.description}`}
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
          <AmountCurrencyInput
            value={editAmount}
            onValueChange={setEditAmount}
            currency={editCurrency}
            onCurrencyChange={setEditCurrency}
            baseCode={baseCode}
            currencies={currencies}
            hideInactive={false}
            rateFor={rateFor}
            loading={currenciesLoading}
            // The money this row already stores. Saving an edit that merely
            // restates the same foreign amount in the same currency keeps that
            // stored value instead of re-pricing at today's rate, so the `≈`
            // preview needs it to promise the number the save will produce.
            // This is the only edit surface — the other two consumers of this
            // component (TransactionEntryRow, QuickAdd) create rows, and a new
            // row genuinely is priced at today's rate.
            storedMoney={{
              amount: transaction.amount,
              original_amount: transaction.original_amount,
              original_currency: transaction.original_currency,
            }}
            error={
              editCurrency !== baseCode && rateFor(editCurrency) == null
                ? 'No rate configured for this currency. Set one in Settings.'
                : null
            }
          />
        </TableCell>
        <TableCell>
          {/* `h-10` on the form matches peer TableCell input height so that
              under the row's [&>td]:align-top, items-center vertically
              centers the h-9 Save/Cancel buttons inside a 40px box —
              their centers line up with the first-line center of the
              Date / Description / Category / Tags / Amount inputs. */}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void handleSave();
            }}
            className="flex h-10 items-center justify-end gap-1"
          >
            <Button
              type="submit"
              size="sm"
              disabled={
                saving ||
                currenciesLoading ||
                (editCurrency !== baseCode && rateFor(editCurrency) == null)
              }
            >
              Save
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
          aria-label={`Select ${transaction.description}`}
        />
      </TableCell>
      <TableCell className="whitespace-nowrap">
        {format(new Date(transaction.date), 'MMM d, yyyy')}
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
              type="button"
              variant="ghost"
              size="icon"
              className="size-8 data-[state=open]:bg-accent"
              aria-label={`Actions for ${transaction.description}`}
            >
              <MoreHorizontal />
              <span className="sr-only">Open menu</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={startEditing}>
              Edit
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              variant="destructive"
              onSelect={() => void onDelete(transaction.id)}
            >
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </TableCell>
    </TableRow>
  );
}
