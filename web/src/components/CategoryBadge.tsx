import type { CSSProperties } from 'react';
import { getCategoryColorVar } from '../lib/chart-colors';

interface CategoryBadgeProps {
  category: { id: number; name: string };
}

/**
 * Pill-shaped badge that shows a category's name. The color is derived from
 * the category id via the centralized chart-color palette — there is no per-
 * category color field on the client. The background is a 15% wash of
 * `--badge-color` against the surface and the text is the full color.
 */
export function CategoryBadge({ category }: CategoryBadgeProps) {
  // React.CSSProperties doesn't type custom `--` properties, so we cast after composing.
  const style = {
    '--badge-color': getCategoryColorVar(category),
    backgroundColor:
      'color-mix(in oklab, var(--badge-color) 15%, transparent)',
    color: 'var(--badge-color)',
  } as CSSProperties;

  return (
    <span
      style={style}
      className="inline-flex items-center rounded-full border border-transparent px-2.5 py-0.5 text-xs font-medium"
      data-category-id={category.id}
    >
      {category.name}
    </span>
  );
}
