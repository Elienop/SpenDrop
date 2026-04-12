// Single source of truth: category → chart palette slot.
// Stable: a category's color never changes once created.
// 20-slot palette covers all seed categories without collisions.
export function getCategoryColorVar(category: { id: number }): string {
  const slot = ((category.id - 1) % 20) + 1;
  return `hsl(var(--chart-${slot}))`;
}
