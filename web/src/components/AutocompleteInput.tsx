import { forwardRef, useMemo, type InputHTMLAttributes } from 'react';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';

interface AutocompleteInputProps
  extends InputHTMLAttributes<HTMLInputElement> {
  suggestions: string[];
  onAccept?: (value: string) => void;
}

export const AutocompleteInput = forwardRef<
  HTMLInputElement,
  AutocompleteInputProps
>(function AutocompleteInput(
  { suggestions, onAccept, value, onChange, onKeyDown, className, ...props },
  ref,
) {
  const text = String(value ?? '');

  const match = useMemo(() => {
    if (!text) return '';
    const lower = text.toLowerCase();
    return (
      suggestions.find((s) => s.toLowerCase().startsWith(lower)) ?? ''
    );
  }, [text, suggestions]);

  const ghost = match ? match.slice(text.length) : '';

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowRight' && ghost && e.currentTarget.selectionStart === text.length) {
      e.preventDefault();
      onAccept?.(match);
    }
    onKeyDown?.(e);
  }

  return (
    <div className="relative rounded-md bg-background">
      {/* Ghost text layer */}
      <div
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-0 flex items-center overflow-hidden whitespace-nowrap px-3 text-sm text-muted-foreground/40',
          className,
        )}
      >
        <span className="invisible">{text}</span>
        <span>{ghost}</span>
      </div>
      <Input
        ref={ref}
        value={value}
        onChange={onChange}
        onKeyDown={handleKeyDown}
        className={cn('bg-transparent', className)}
        autoComplete="off"
        {...props}
      />
    </div>
  );
});
