/**
 * Recipes for reaching the 44px touch floor.
 *
 * The first question is WHAT TO GATE ON, and the answer is the pointer, not
 * the viewport width. `md:` reads as "phone versus desktop" but it measures
 * neither: the household's Galaxy Tab S10 FE is ~1130px in landscape, so it
 * takes the desktop side of every breakpoint while still being a touch
 * screen — a control sized `h-11 md:h-8` gives that tablet a 32px target,
 * which is the exact hole this file exists to close. `coarse:` (registered in
 * `tailwind.config.ts` as `@media (pointer: coarse)`) asks the question the
 * floor is actually about. Reach for `md:` only when the thing being decided
 * really is about available room — a table collapsing to cards, a sidebar
 * folding away — and never for a tap target.
 *
 * The second question is WHICH LEVER, and it is a design question rather than
 * a taste one:
 *
 *   - RAISE THE FLOOR (`coarse:min-h-11`, or `TOUCH_TARGET_SQUARE` for
 *     something square). Correct for most controls, and it composes: because
 *     `min-h`/`min-w` and `h`/`w` are separate tailwind-merge conflict groups,
 *     a floor set on a shared primitive SURVIVES every call site's own
 *     `h-8`/`h-9`, and CSS clamps the used size up. That is why `SelectTrigger`
 *     carries `coarse:min-h-11` in `components/ui/select.tsx` and not one
 *     Select call site had to change. A floor also lets content grow past it,
 *     which a fixed height does not.
 *
 *   - GROW ONLY THE HIT AREA (`TOUCH_TARGET_CHECKBOX` below). Correct where
 *     the VISIBLE size is load-bearing — a checkbox that must stay optically
 *     aligned with the text beside it, or an icon pinned to a corner. Growing
 *     the box would move the thing it is trying to keep still.
 *
 * What NOT to do: an ungated floor. `twMerge('h-10 w-full min-h-11', 'h-9')`
 * keeps both classes, so an ungated `min-h-11` on a primitive silently
 * inflates every dense trigger on a mouse desktop too. Desktop density is a
 * deliberate constraint here, so the floor is always behind `coarse:`.
 *
 * Shared rather than per-page: Transactions and Trash both grew phone card
 * lists in the same slice, and a second copy of a recipe is a second place for
 * it to drift away from the 44px it is meant to produce.
 */

/**
 * The 44px floor on BOTH axes, for a control whose target is square — an icon
 * button, or a spacer that has to keep its place in a row of them. A square
 * target needs width as well as height; a height-only floor leaves a 32px-wide
 * button that is merely taller.
 *
 * Additive, not a size: the caller keeps its own base `size-*` for fine
 * pointers, and this only clamps upward when the pointer is coarse. So the
 * desktop appearance of anything it is added to is unchanged at every width.
 *
 * Two tokens rather than one because Tailwind 3 has no `min-size-*`.
 */
export const TOUCH_TARGET_SQUARE = "coarse:min-h-11 coarse:min-w-11";

/**
 * Grows a 16px Checkbox to a 44px tap target without moving it: the
 * pseudo-element is absolutely positioned 14px outside the border box on
 * every side (16 + 2×14 = 44). Padding would grow the visible box and
 * shift everything beside it; this only grows the hit area. Same trick,
 * different lever, as the `-m-3.5 p-3.5` pair on DialogContent's close
 * button — there the icon had to stay pinned to a corner, here the
 * checkbox has to stay aligned with the text beside it.
 *
 * Mobile-only by construction at every call site: the desktop tables'
 * checkboxes are mouse targets and carry none of this.
 */
export const TOUCH_TARGET_CHECKBOX =
  "relative before:absolute before:-inset-3.5 before:content-['']";
