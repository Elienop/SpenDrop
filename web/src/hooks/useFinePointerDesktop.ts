import { useSyncExternalStore } from 'react';
import { DESKTOP_QUERY } from './useIsMobileViewport';

/**
 * Width AND pointer capability, because for some presentations width alone is
 * the wrong question.
 *
 * Composed from `DESKTOP_QUERY` rather than repeating `768px`, so there is
 * still exactly one place that number lives.
 *
 * A PLAIN `and` CHAIN, AND THAT IS A DECISION ABOUT VERIFIABILITY, NOT TASTE.
 * The arguably safer query is `… and (not (pointer: coarse))`, which treats an
 * UNKNOWN pointer as a desktop instead of demanding positive proof of a fine
 * one — the same outcome for every real touch device, since they all report
 * `coarse`. It was rejected because **happy-dom silently returns `false` for
 * `not (…)` inside `and`, and for comma-separated query lists** (probed, both
 * forms, both pointer states). The hook would then have answered "not dense"
 * in every unit test, the desktop branch would never have rendered, and the
 * suite would have gone dark on the presentation it is meant to protect —
 * while looking green.
 *
 * The environments disagree in the other direction too: plain headless Chrome
 * reports `(pointer: none)` / `(hover: none)`, so THIS query is false there at
 * every width, and `Emulation.setEmulatedMedia` does not override those two
 * features. A browser check of the dense grid needs
 * `--blink-settings=primaryPointerType=4,primaryHoverType=2,availablePointerTypes=4,availableHoverTypes=2`
 * or it will quietly measure the calendar and report it as the desktop.
 *
 * So: neither candidate is verifiable in both environments by default, and
 * this one is the one whose blind spot has a known lever. Do not "simplify" it
 * to the `not (…)` form without re-probing happy-dom first.
 */
export const FINE_POINTER_DESKTOP_QUERY = `${DESKTOP_QUERY} and (hover: hover) and (pointer: fine)`;

function hasMatchMedia(): boolean {
  return (
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
  );
}

function subscribe(onStoreChange: () => void): () => void {
  if (!hasMatchMedia()) return () => {};
  const mql = window.matchMedia(FINE_POINTER_DESKTOP_QUERY);
  mql.addEventListener('change', onStoreChange);
  return () => mql.removeEventListener('change', onStoreChange);
}

function getSnapshot(): boolean {
  if (!hasMatchMedia()) return false;
  return window.matchMedia(FINE_POINTER_DESKTOP_QUERY).matches;
}

function getServerSnapshot(): boolean {
  return false;
}

/**
 * Reports whether this device can use a DENSE, HOVER-DEPENDENT presentation:
 * wide enough, and driven by a pointer that can both hover and hit a small
 * target.
 *
 * THIS IS NOT `useIsMobileViewport`, AND MUST NOT BE FOLDED INTO IT. That hook
 * is a pure negation of one width query, and three pages (Transactions, Trash,
 * Dashboard) choose cards-vs-table on it. Its "gapless by construction"
 * rationale depends on there being exactly one number on both sides of the
 * question; adding pointer terms would silently change what those three pages
 * render on a wide touch device, which is a different decision that nobody
 * asked for. Two questions, two hooks.
 *
 * The question this one answers is narrower: the spending heatmap's 53-column
 * year grid has sub-24px targets at EVERY width, and its only way to read a
 * value is a Radix tooltip — which cannot open on touch at all (see
 * `heatmapGrid.ts`'s note on the cell label). So a touch device must never be
 * given it, however wide the screen. The concrete case is the household's
 * Galaxy Tab S10 FE: ~720px in portrait, ~1152px in landscape, so a
 * width-only gate turns a 60px calendar into 18.4px squares with no day
 * numbers and no reachable values just because the tablet was rotated.
 *
 * Media queries report the PRIMARY pointer, so a laptop with both a touchscreen
 * and a trackpad still reports `fine`/`hover` and correctly keeps the dense
 * grid — the query asks "what does this person mostly point with", not "is a
 * touchscreen present anywhere".
 *
 * Absent `matchMedia`, the answer is `false` — the calendar. That direction is
 * deliberate and opposite to `useIsMobileViewport`'s default: an unknown device
 * getting large, tappable, self-labelling cells is a worse LAYOUT and a better
 * outcome than an unknown device getting 18px squares it may not be able to
 * read at all.
 */
export function useFinePointerDesktop(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
