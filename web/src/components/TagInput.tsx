import { useState, useRef } from 'react';
import type { KeyboardEvent } from 'react';
import styles from '../styles/Transactions.module.css';

interface TagInputProps {
  value: string; // comma-separated
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
}

export function TagInput({ value, onChange, placeholder = 'Add tag...', className }: TagInputProps) {
  const [input, setInput] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  const tags = value ? value.split(',').map((t) => t.trim()).filter(Boolean) : [];

  function addTag(tag: string) {
    const trimmed = tag.trim();
    if (!trimmed) return;
    if (tags.includes(trimmed)) return;
    onChange([...tags, trimmed].join(','));
    setInput('');
  }

  function removeTag(index: number) {
    const next = tags.filter((_, i) => i !== index);
    onChange(next.join(','));
  }

  function handleKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      addTag(input);
    }
    if (e.key === 'Backspace' && input === '' && tags.length > 0) {
      removeTag(tags.length - 1);
    }
  }

  return (
    <div
      className={`${styles.tagInputWrapper} ${className ?? ''}`}
      onClick={() => inputRef.current?.focus()}
    >
      {tags.map((tag, i) => (
        <span key={tag} className={styles.tagPill}>
          {tag}
          <button
            type="button"
            className={styles.tagRemove}
            onClick={(e) => {
              e.stopPropagation();
              removeTag(i);
            }}
            aria-label={`Remove tag ${tag}`}
          >
            ×
          </button>
        </span>
      ))}
      <input
        ref={inputRef}
        type="text"
        className={styles.tagInput}
        value={input}
        onChange={(e) => setInput(e.target.value)}
        onKeyDown={handleKeyDown}
        onBlur={() => addTag(input)}
        placeholder={tags.length === 0 ? placeholder : ''}
      />
    </div>
  );
}
