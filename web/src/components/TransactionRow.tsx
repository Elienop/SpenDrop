import { useState } from 'react';
import type { FormEvent } from 'react';
import { format } from 'date-fns';
import type { Transaction, Category } from '../api/types';
import { CategoryBadge } from './CategoryBadge';
import { TagInput } from './TagInput';
import styles from '../styles/Transactions.module.css';

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
      <tr className={styles.editRow}>
        <td>
          <input
            type="date"
            className={styles.editInput}
            value={date}
            onChange={(e) => setDate(e.target.value)}
          />
        </td>
        <td>
          <input
            type="text"
            className={styles.editInput}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </td>
        <td>
          <select
            className={styles.editSelect}
            value={categoryId}
            onChange={(e) => setCategoryId(e.target.value)}
          >
            {categories.map((cat) => (
              <option key={cat.id} value={cat.id}>
                {cat.name}
              </option>
            ))}
          </select>
        </td>
        <td>
          <TagInput value={tags} onChange={setTags} placeholder="Add tags..." />
        </td>
        <td>
          <input
            type="number"
            className={styles.editInput}
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            step="0.01"
          />
        </td>
        <td className={styles.actionCell}>
          <form onSubmit={(e) => void handleSave(e)} className={styles.editActions}>
            <button
              type="submit"
              className={styles.saveButton}
              disabled={saving}
            >
              Save
            </button>
            <button
              type="button"
              className={styles.cancelButton}
              onClick={handleCancel}
            >
              Cancel
            </button>
          </form>
        </td>
      </tr>
    );
  }

  return (
    <tr className={styles.row}>
      <td>{format(new Date(transaction.date), 'MMM d, yyyy')}</td>
      <td>{transaction.description}</td>
      <td>
        <CategoryBadge
          name={transaction.category_name}
          color={transaction.category_color}
        />
      </td>
      <td>
        {transaction.tags && transaction.tags.split(',').map((tag) => (
          <span key={tag.trim()} className={styles.tagPill}>{tag.trim()}</span>
        ))}
      </td>
      <td
        className={
          transaction.category_type === 'expense'
            ? styles.expense
            : styles.income
        }
      >
        {transaction.category_type === 'expense' ? '-' : '+'}
        {formatCurrency(transaction.amount)}
      </td>
      <td className={styles.actionCell}>
        <button
          type="button"
          className={styles.editButton}
          onClick={() => setEditing(true)}
          aria-label={`Edit ${transaction.description}`}
        >
          Edit
        </button>
        <button
          type="button"
          className={styles.deleteButton}
          onClick={() => void onDelete(transaction.id)}
          aria-label={`Delete ${transaction.description}`}
        >
          Delete
        </button>
      </td>
    </tr>
  );
}
