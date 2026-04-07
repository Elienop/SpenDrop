import { useState } from 'react';
import type { FormEvent } from 'react';
import type { Category } from '../api/types';
import { TagInput } from './TagInput';
import styles from '../styles/Transactions.module.css';

interface TransactionInput {
  date: string;
  amount: number;
  description: string;
  category_id: number;
  tags: string;
}

interface TransactionEntryProps {
  categories: Category[];
  onSubmit: (input: TransactionInput) => Promise<void>;
}

const LAST_CATEGORY_KEY = 'spendrop-last-category';

function getLastCategory(): string {
  try {
    return localStorage.getItem(LAST_CATEGORY_KEY) ?? '';
  } catch {
    return '';
  }
}

function saveLastCategory(id: string) {
  try {
    localStorage.setItem(LAST_CATEGORY_KEY, id);
  } catch {
    // localStorage unavailable
  }
}

export function TransactionEntry({ categories, onSubmit }: TransactionEntryProps) {
  const today = new Date().toISOString().slice(0, 10);
  const [date, setDate] = useState(today);
  const [amount, setAmount] = useState('');
  const [description, setDescription] = useState('');
  const [categoryId, setCategoryId] = useState(getLastCategory());
  const [tags, setTags] = useState('');
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!amount || !description || !categoryId) return;

    setSubmitting(true);
    try {
      saveLastCategory(categoryId);
      await onSubmit({
        date,
        amount: parseFloat(amount),
        description,
        category_id: parseInt(categoryId, 10),
        tags,
      });
      // Reset form for rapid entry, keep date and category
      setAmount('');
      setDescription('');
      setTags('');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form
      className={styles.entryForm}
      onSubmit={(e) => void handleSubmit(e)}
    >
      <div className={styles.entryField}>
        <label htmlFor="entry-date" className={styles.entryLabel}>Date</label>
        <input
          id="entry-date"
          type="date"
          className={styles.entryInput}
          value={date}
          onChange={(e) => setDate(e.target.value)}
        />
      </div>

      <div className={styles.entryField}>
        <label htmlFor="entry-amount" className={styles.entryLabel}>Amount</label>
        <input
          id="entry-amount"
          type="number"
          className={styles.entryInput}
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          placeholder="0.00"
          step="0.01"
          min="0"
        />
      </div>

      <div className={styles.entryField}>
        <label htmlFor="entry-description" className={styles.entryLabel}>Description</label>
        <input
          id="entry-description"
          type="text"
          className={styles.entryInput}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What was this for?"
        />
      </div>

      <div className={styles.entryField}>
        <label htmlFor="entry-category" className={styles.entryLabel}>Category</label>
        <select
          id="entry-category"
          className={styles.entrySelect}
          value={categoryId}
          onChange={(e) => setCategoryId(e.target.value)}
        >
          <option value="">Select...</option>
          {categories.map((cat) => (
            <option key={cat.id} value={cat.id}>
              {cat.name}
            </option>
          ))}
        </select>
      </div>

      <div className={styles.entryField}>
        <label htmlFor="entry-tags" className={styles.entryLabel}>Tags</label>
        <TagInput value={tags} onChange={setTags} placeholder="Add tags..." />
      </div>

      <button
        type="submit"
        className={styles.addButton}
        disabled={submitting}
      >
        {submitting ? 'Adding...' : 'Add'}
      </button>
    </form>
  );
}
