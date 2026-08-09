import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { CategoryChips } from './CategoryChips';
import type { Category } from '@/api/types';

/**
 * The defect these tests guard is a LAYOUT one and happy-dom has no layout
 * engine, so nothing here can measure a width — the 360px numbers live in the
 * browser pass. What a unit test can pin is the class contract that produces
 * the measured result, and the reason each token is there:
 *
 *   max-w-full               caps an over-long chip at the row's width
 *   [overflow-wrap:anywhere] lets a 100-char name with no spaces break, AND
 *                            shrinks min-content, which is what the flex
 *                            automatic minimum floors the chip at
 *   whitespace-normal        wrapping stated, not inherited from a UA sheet
 *   min-h-11                 the 44px touch floor survives all of the above
 *
 * Tokens are checked with `classList.contains`, never `toContain` — a
 * substring assertion here would match `md:max-w-full` or `hover:min-h-11`
 * and pass on a chip that is unbounded at the width that matters.
 */

const CEILING = 100; // internal/api/limits.go: MaxCategoryNameLength
const LONG_NAME = 'Household Groceries Pharmacy And Everything Else We Buy For The Flat Every Single Month Of The'; // 94
const UNBROKEN_NAME = 'A'.repeat(CEILING);

function cat(id: number, name: string): Category {
  return {
    id,
    name,
    type: 'expense',
    icon: null,
    sort_order: id,
    is_active: true,
    created_at: '2026-01-01T00:00:00Z',
  };
}

function renderChips(categories: Category[], selectedId: number | null = null) {
  const onSelect = vi.fn();
  render(
    <CategoryChips
      categories={categories}
      selectedId={selectedId}
      onSelect={onSelect}
    />,
  );
  return { onSelect };
}

describe('CategoryChips', () => {
  it('bounds every chip to the row width and lets its label wrap', () => {
    renderChips([cat(1, 'Food'), cat(2, LONG_NAME)]);

    for (const name of ['Food', LONG_NAME]) {
      const chip = screen.getByRole('button', { name });
      expect(chip.classList.contains('max-w-full')).toBe(true);
      expect(chip.classList.contains('[overflow-wrap:anywhere]')).toBe(true);
      expect(chip.classList.contains('whitespace-normal')).toBe(true);
      expect(chip.classList.contains('min-h-11')).toBe(true);
    }
  });

  it('renders a name at the 100-character server ceiling in full', () => {
    // No ellipsis, no slice: the accessible name IS the whole category name.
    // `getByRole`'s string matcher is exact, so a chip that shipped
    // `name.slice(0, 20) + '…'` would not be found at all.
    renderChips([cat(1, UNBROKEN_NAME)]);

    const chip = screen.getByRole('button', { name: UNBROKEN_NAME });
    expect(chip.textContent).toBe(UNBROKEN_NAME);
    expect(chip.textContent).toHaveLength(CEILING);
    // The CSS truncation routes are the other way to lose the name; a chip is
    // what you tap to categorise a spend, so two categories sharing a prefix
    // must stay distinguishable.
    expect(chip.classList.contains('truncate')).toBe(false);
    expect(chip.classList.contains('line-clamp-1')).toBe(false);
    expect(chip.classList.contains('overflow-hidden')).toBe(false);
  });

  it('keeps a wrapped chip from stretching its neighbours', () => {
    // Without `items-start` the flex line's default `stretch` makes every chip
    // beside the wrapped one just as tall — one long name would turn a whole
    // row of one-word categories into three-line slabs.
    renderChips([cat(1, 'Food'), cat(2, LONG_NAME)]);

    const row = screen.getByRole('group', { name: 'Category' });
    expect(row.classList.contains('items-start')).toBe(true);
    expect(row.classList.contains('flex-wrap')).toBe(true);
  });

  it('still reports the tapped category', () => {
    // Positive control: the chips are real, selectable controls, so the class
    // assertions above are not passing against an inert render.
    const { onSelect } = renderChips([cat(1, 'Food'), cat(2, LONG_NAME)], 1);

    const selected = screen.getByRole('button', { name: 'Food' });
    expect(selected).toHaveAttribute('aria-pressed', 'true');
    const other = screen.getByRole('button', { name: LONG_NAME });
    expect(other).toHaveAttribute('aria-pressed', 'false');

    other.click();
    expect(onSelect).toHaveBeenCalledWith(2);
  });
});
