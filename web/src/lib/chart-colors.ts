// Single source of truth: category → chart palette slot.
// Stable: a category's color never changes once created.
// Collisions only occur beyond 11 categories in a single chart, which is
// intentionally unsupported (consolidate categories if you hit this).
export function getCategoryColorVar(category: { id: number }): string {
  const slot = ((category.id - 1) % 11) + 1;
  return `hsl(var(--chart-${slot}))`;
}
