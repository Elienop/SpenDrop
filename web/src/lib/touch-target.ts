/**
 * Recipes for reaching the 44px touch floor on phone-width surfaces.
 *
 * Two levers, and which one to reach for is a design question, not a taste
 * one:
 *
 *   - GROW THE BOX (`h-11 md:h-8`, `size-11 md:size-8`). Correct for buttons.
 *     A control that is tapped ought to look tappable, and on a phone there is
 *     room for it. Desktop keeps its denser sizing through the `md:` half.
 *
 *   - GROW ONLY THE HIT AREA (the constants below). Correct where the visible
 *     size is load-bearing — a checkbox that must stay optically aligned with
 *     the text beside it, or an icon pinned to a corner. Padding would move
 *     the thing it is trying to keep still.
 *
 * Shared rather than per-page: Transactions and Trash both grew phone card
 * lists in the same slice, and a second copy of the recipe is a second place
 * for the inset to drift away from the 44px it is meant to produce.
 */

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
