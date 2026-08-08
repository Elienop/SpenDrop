import { useSyncExternalStore } from 'react';

/**
 * Tailwind's `md` breakpoint, verbatim — the same string `MobileNav` gates its
 * own `DESKTOP_QUERY` on, and the same 768px that `md:` utilities switch on at.
 *
 * "Phone" is derived by NEGATING this, never by mirroring it as a second
 * `max-width` string. A mirror has to pick a number just below 768, and every
 * choice leaves a hairline where neither query matches — `max-width: 767.98px`
 * paired with `min-width: 768px` leaves 767.99px matching NOTHING, which is a
 * width at which neither presentation would render. Negation is gapless by
 * construction, and it removes the drift risk entirely: there is one number,
 * in one place, on both sides of the question.
 *
 * Exported so a test can assert it still equals MobileNav's copy — that file
 * is not mine to edit, so the agreement is pinned rather than shared.
 */
export const DESKTOP_QUERY = '(min-width: 768px)';

function hasMatchMedia(): boolean {
  return (
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
  );
}

function subscribe(onStoreChange: () => void): () => void {
  if (!hasMatchMedia()) return () => {};
  const mql = window.matchMedia(DESKTOP_QUERY);
  mql.addEventListener('change', onStoreChange);
  return () => mql.removeEventListener('change', onStoreChange);
}

function getSnapshot(): boolean {
  if (!hasMatchMedia()) return false;
  return !window.matchMedia(DESKTOP_QUERY).matches;
}

/**
 * Reports whether the viewport is below the `md` breakpoint.
 *
 * Callers use this to MOUNT one presentation or the other, rather than
 * rendering both and hiding one with `md:hidden`. Two trees for the same rows
 * would duplicate every accessible name (each row's "Select <description>"
 * checkbox twice) and double the per-row render work, and `display: none`
 * only removes the loser from the a11y tree — never from React.
 *
 * `useSyncExternalStore` rather than `useState` + an effect: the snapshot is
 * read during the first render, so a phone never paints the desktop table for
 * a frame before swapping to cards, and there is no `set-state-in-effect` to
 * suppress. Where `matchMedia` is missing the answer is `false` (desktop),
 * which is also what keeps every pre-existing test on the table path.
 */
export function useIsMobileViewport(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

function getServerSnapshot(): boolean {
  return false;
}
