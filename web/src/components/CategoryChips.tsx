import type { CSSProperties } from 'react';
import { Check } from 'lucide-react';
import { getCategoryColorVar } from '@/lib/chart-colors';
import { cn } from '@/lib/utils';
import type { Category } from '@/api/types';

interface CategoryChipsProps {
  categories: Category[];
  selectedId: number | null;
  onSelect: (id: number) => void;
}

/**
 * Tap-friendly category selector for the mobile quick-add screen. Each chip
 * is a >=44px touch target (Apple HIG minimum) using the category's derived
 * palette color (mirroring `CategoryBadge`), and acts as a single-select
 * radio-like toggle. Reused by both the Freeform and Tap modes.
 *
 * Rendered as a wrapping flex row so the chips reflow on narrow viewports.
 * Selection is reflected via `aria-pressed` so the active chip is exposed to
 * assistive tech (and queryable in tests).
 */
export function CategoryChips({
  categories,
  selectedId,
  onSelect,
}: CategoryChipsProps) {
  return (
    <div className="flex flex-wrap gap-2" role="group" aria-label="Category">
      {categories.map((cat) => {
        const selected = cat.id === selectedId;
        const style = {
          '--chip-color': getCategoryColorVar(cat),
        } as CSSProperties;
        return (
          <button
            key={cat.id}
            type="button"
            onClick={() => onSelect(cat.id)}
            aria-pressed={selected}
            style={style}
            className={cn(
              'inline-flex min-h-11 items-center gap-1.5 rounded-full border px-4 text-sm font-medium transition-colors',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
              selected
                ? 'border-transparent text-background [background-color:var(--chip-color)]'
                : 'border-[color:color-mix(in_oklab,var(--chip-color)_40%,transparent)] text-[color:var(--chip-color)] [background-color:color-mix(in_oklab,var(--chip-color)_12%,transparent)]',
            )}
          >
            {selected && <Check aria-hidden className="size-4" />}
            {cat.name}
          </button>
        );
      })}
    </div>
  );
}
