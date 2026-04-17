import { useState, useRef, useMemo } from 'react';
import type { KeyboardEvent } from 'react';
import { cn } from '@/lib/utils';

interface TagInputProps {
  value: string; // comma-separated
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  suggestions?: string[];
}

export function TagInput({ value, onChange, placeholder = 'Add tag...', className, suggestions = [] }: TagInputProps) {
  const [input, setInput] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  const tags = useMemo(
    () => (value ? value.split(',').map((t) => t.trim()).filter(Boolean) : []),
    [value],
  );

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

  const tagMatch = useMemo(() => {
    if (!input) return '';
    const lower = input.toLowerCase();
    return (
      suggestions.find(
        (s) => s.toLowerCase().startsWith(lower) && !tags.includes(s),
      ) ?? ''
    );
  }, [input, suggestions, tags]);

  const tagGhost = tagMatch ? tagMatch.slice(input.length) : '';

  function handleKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowRight' && tagGhost && e.currentTarget.selectionStart === input.length) {
      e.preventDefault();
      addTag(tagMatch);
      return;
    }
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
      className={cn(
        'flex flex-wrap items-center gap-1 min-h-10 rounded-md border border-input bg-background px-2 py-1 cursor-text transition-colors focus-within:border-ring focus-within:ring-1 focus-within:ring-ring',
        className,
      )}
      onClick={() => inputRef.current?.focus()}
    >
      {tags.map((tag, i) => (
        <span
          key={`${tag}-${i}`}
          className="inline-flex items-center gap-1 rounded-sm bg-primary/15 px-2 py-0.5 text-xs font-semibold text-primary whitespace-nowrap"
        >
          {tag}
          <button
            type="button"
            className="border-0 bg-transparent p-0 text-sm leading-none text-primary opacity-70 transition-opacity hover:opacity-100"
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
      <div className="relative flex-1 min-w-[60px]">
        {tagGhost && (
          <div
            aria-hidden
            className="pointer-events-none absolute inset-0 flex items-center overflow-hidden whitespace-nowrap px-1 py-0.5 text-sm text-muted-foreground/40"
          >
            <span className="invisible">{input}</span>
            <span>{tagGhost}</span>
          </div>
        )}
        <input
          ref={inputRef}
          type="text"
          className="w-full border-0 bg-transparent px-1 py-0.5 text-sm text-foreground outline-none placeholder:text-muted-foreground"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          onBlur={() => addTag(input)}
          placeholder={tags.length === 0 ? placeholder : ''}
          aria-label="Add tag"
          autoComplete="off"
        />
      </div>
    </div>
  );
}
