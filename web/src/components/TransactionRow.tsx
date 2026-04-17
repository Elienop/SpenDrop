import { useState, useEffect, useRef } from 'react';
import type { FormEvent } from 'react';
import { format } from 'date-fns';
import { MoreHorizontal } from 'lucide-react';
import type { Transaction, Category } from '../api/types';
import { AmountDisplay } from './AmountDisplay';
import { AmountCurrencyInput } from './AmountCurrencyInput';
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
}

export function TransactionRow({
  transaction,
  categories,
  selected,
  onSelect,
  onUpdate,
  onDelete,
  onError,
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
    if (resolved.currency !== editCurrency) {
      setEditCurrency(resolved.currency);
    }
    if (resolved.amount !== editAmount) {
      setEditAmount(resolved.amount);
    }
    // editAmount/editCurrency intentionally excluded: this effect must run
    // exactly once per mount, gated by didInitEditCurrency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currenciesLoading, baseCode, transaction]);

  async function handleSave(e: FormEvent) {
    e.preventDefault();
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

  if (editing) {
    return (
      <TableRow className="[&>td]:align-top">
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
          <Input
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
          />
        </TableCell>
        <TableCell>
          <Input
            type="text"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </TableCell>
        <TableCell>
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
        </TableCell>
        <TableCell>
          <TagInput value={tags} onChange={setTags} placeholder="Add tags..." />
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
            error={
              editCurrency !== baseCode && rateFor(editCurrency) == null
                ? 'No rate configured for this currency. Set one in Settings.'
                : null
            }
          />
        </TableCell>
        <TableCell>
          <form
            onSubmit={(e) => void handleSave(e)}
            className="flex items-center justify-end gap-1"
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
      <TableCell className="font-medium">{transaction.description}</TableCell>
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
