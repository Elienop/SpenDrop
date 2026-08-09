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
 *
 * A chip's LABEL wraps; it is never truncated. The server accepts category
 * names up to `MaxCategoryNameLength` = 100 characters, and at 360px one such
 * name rendered a single 489.5px chip against a 328px content box — measured,
 * `documentElement.scrollWidth` 507 vs `clientWidth` 360, so the whole capture
 * screen panned sideways. Truncating was rejected: a chip is the control you
 * tap to categorise a spend, so two categories sharing a prefix would become
 * indistinguishable, and a `title` tooltip conveys nothing on touch. Wrapping
 * keeps the full name readable and costs only vertical space on the one long
 * name. Mechanically that needs all three of:
 *   - `max-w-full` so an over-long chip is capped at the row's width,
 *   - `overflow-wrap: anywhere` so a 100-character name with no spaces still
 *     breaks — unlike `break-words`/`break-word`, `anywhere` also shrinks the
 *     intrinsic min-content size, which is what the flex automatic minimum
 *     (`min-width: auto`) floors the chip at, and
 *   - `whitespace-normal`, stated rather than inherited from the UA sheet.
 * `items-start` on the row so a wrapped chip does not stretch its neighbours
 * to match, and `rounded-3xl` rather than `rounded-full`: at one line both
 * clamp to the same 22px pill, but the 100-character name wraps to four lines
 * (measured 328x98 at 360px) and there a fully-round end curve reaches into
 * the `px-4` text column.
 */
export function CategoryChips({
  categories,
  selectedId,
  onSelect,
}: CategoryChipsProps) {
  return (
    <div
      className="flex flex-wrap items-start gap-2"
      role="group"
      aria-label="Category"
    >
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
              'inline-flex min-h-11 max-w-full items-center gap-1.5 whitespace-normal rounded-3xl border px-4 py-2 text-left text-sm font-medium transition-colors [overflow-wrap:anywhere]',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
              selected
                ? 'border-transparent text-background [background-color:var(--chip-color)]'
                : 'border-[color:color-mix(in_oklab,var(--chip-color)_40%,transparent)] text-[color:var(--chip-color)] [background-color:color-mix(in_oklab,var(--chip-color)_12%,transparent)]',
            )}
          >
            {selected && <Check aria-hidden className="size-4 shrink-0" />}
            {cat.name}
          </button>
        );
      })}
    </div>
  );
}
