// Shared style strings. Keep this file narrow — only patterns reused
// across pages should live here.

/**
 * Tailwind classes for an AlertDialogAction (or any button) that
 * performs a destructive action — Delete, Discard, Purge, etc.
 * Includes `focus-visible:ring-destructive` so keyboard users see the
 * focus state on the dark button background.
 *
 * Hoisted to this module (was previously duplicated in
 * `pages/Settings.tsx` and `pages/Savings.tsx`, and inlined as a
 * literal in `pages/Budgets.tsx`'s discard dialog) after the design
 * enforcer flagged the divergence — Settings's local copy had drifted
 * and was missing the focus ring.
 */
export const destructiveActionClass =
  'bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive';
