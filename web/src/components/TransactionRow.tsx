import { useState } from 'react';
import type { FormEvent } from 'react';
import { format } from 'date-fns';
import { MoreHorizontal } from 'lucide-react';
import type { Transaction, Category } from '../api/types';
import { CategoryBadge } from './CategoryBadge';
import { TagInput } from './TagInput';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { TableCell, TableRow } from '@/components/ui/table';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';

function formatCurrency(amount: number): string {
  return amount.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
  });
}

interface TransactionRowProps {
  transaction: Transaction;
  categories: Category[];
  onUpdate: (input: {
    id: number;
    date: string;
    amount: number;
    description: string;
    category_id: number;
    tags: string;
  }) => Promise<void>;
  onDelete: (id: number) => Promise<void>;
}

export function TransactionRow({
  transaction,
  categories,
  onUpdate,
  onDelete,
}: TransactionRowProps) {
  const [editing, setEditing] = useState(false);
  const [date, setDate] = useState(transaction.date);
  const [amount, setAmount] = useState(String(transaction.amount));
  const [description, setDescription] = useState(transaction.description);
  const [categoryId, setCategoryId] = useState(String(transaction.category_id));
  const [tags, setTags] = useState(transaction.tags ?? '');
  const [saving, setSaving] = useState(false);

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    try {
      await onUpdate({
        id: transaction.id,
        date,
        amount: parseFloat(amount),
        description,
        category_id: parseInt(categoryId, 10),
        tags,
      });
      setEditing(false);
    } finally {
      setSaving(false);
    }
  }

  function handleCancel() {
    setDate(transaction.date);
    setAmount(String(transaction.amount));
    setDescription(transaction.description);
    setCategoryId(String(transaction.category_id));
    setTags(transaction.tags ?? '');
    setEditing(false);
  }

  if (editing) {
    return (
      <TableRow>
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
          <Input
            type="number"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            step="0.01"
            className="text-right"
          />
        </TableCell>
        <TableCell>
          <form
            onSubmit={(e) => void handleSave(e)}
            className="flex items-center justify-end gap-1"
          >
            <Button type="submit" size="sm" disabled={saving}>
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
    <TableRow className="hover:bg-muted/50">
      <TableCell className="whitespace-nowrap text-muted-foreground">
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
          transaction.tags.split(',').map((tag) => (
            <span
              key={tag.trim()}
              className="mr-1 inline-flex items-center rounded-md bg-muted px-1.5 py-0.5 text-xs text-muted-foreground"
            >
              {tag.trim()}
            </span>
          ))}
      </TableCell>
      <TableCell
        className={cn(
          'text-right font-mono tabular-nums',
          transaction.category_type === 'expense'
            ? 'text-foreground'
            : 'text-emerald-600 dark:text-emerald-400',
        )}
      >
        {transaction.category_type === 'expense' ? '-' : '+'}
        {formatCurrency(transaction.amount)}
      </TableCell>
      <TableCell className="text-right">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              aria-label={`Actions for ${transaction.description}`}
            >
              <MoreHorizontal className="h-4 w-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => setEditing(true)}>
              Edit
            </DropdownMenuItem>
            <DropdownMenuItem
              onSelect={() => void onDelete(transaction.id)}
              className="text-destructive focus:text-destructive"
            >
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </TableCell>
    </TableRow>
  );
}
