import { renderHook } from '@testing-library/react';
import { describe, it, expect, afterEach } from 'vitest';
import {
  useFinePointerDesktop,
  FINE_POINTER_DESKTOP_QUERY,
} from './useFinePointerDesktop';
import { DESKTOP_QUERY } from './useIsMobileViewport';

/**
 * This hook decides whether a device is shown a 53-column grid whose targets
 * are under 24px at every width and whose only value affordance is a tooltip
 * that cannot open on touch. Getting it wrong on a wide tablet is not a
 * cosmetic miss, so the width axis and the pointer axis are both driven for
 * real here.
 */

const DESKTOP_WIDTH = 1024;
/** Galaxy Tab S10 FE in landscape — wide, and entirely touch-driven. */
const TABLET_LANDSCAPE_WIDTH = 1152;

function setViewportWidth(width: number): void {
  const controllable = window as unknown as {
    happyDOM?: { setViewport: (v: { width: number }) => void };
  };
  if (!controllable.happyDOM) {
    throw new Error('happy-dom viewport control is unavailable');
  }
  controllable.happyDOM.setViewport({ width });
}

/**
 * Drive the POINTER for real, not with a matchMedia stub.
 *
 * happy-dom derives `(hover)` and `(pointer)` from `navigator.maxTouchPoints`
 * (verified in `match-media/MediaQueryItem.js`: `pointer: fine` is
 * `maxTouchPoints === 0`, `coarse` is `> 0`), so setting it leaves the real
 * media-query engine parsing and evaluating the real query string. A
 * hand-rolled stub would answer whatever it is asked and keep every test below
 * green with the query inverted or misspelled — the exact vacuity this repo
 * has lost tests to before.
 *
 * It must be set BEFORE render: happy-dom fires no `change` event for this, so
 * a mid-test flip would not reach `useSyncExternalStore`.
 */
function setPointer(kind: 'fine' | 'coarse'): void {
  Object.defineProperty(window.navigator, 'maxTouchPoints', {
    configurable: true,
    value: kind === 'fine' ? 0 : 5,
  });
}

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
  setPointer('fine');
});

describe('useFinePointerDesktop', () => {
  it('is composed from the shared breakpoint, not a second copy of 768', () => {
    // The width half must not drift from the hook three other pages gate on.
    expect(FINE_POINTER_DESKTOP_QUERY.startsWith(DESKTOP_QUERY)).toBe(true);
    expect(FINE_POINTER_DESKTOP_QUERY).toBe(
      '(min-width: 768px) and (hover: hover) and (pointer: fine)',
    );
  });

  it('drives a real pointer, and the environment answers', () => {
    // The positive control for `setPointer` itself. Every case below is
    // meaningless if this ever stops flipping — and it would fail SILENTLY,
    // by leaving the hook stuck on one answer that happens to match half the
    // expectations.
    setPointer('coarse');
    expect(window.matchMedia('(pointer: coarse)').matches).toBe(true);
    expect(window.matchMedia('(pointer: fine)').matches).toBe(false);
    setPointer('fine');
    expect(window.matchMedia('(pointer: fine)').matches).toBe(true);
    expect(window.matchMedia('(hover: hover)').matches).toBe(true);
  });

  it.each([
    { width: TABLET_LANDSCAPE_WIDTH, pointer: 'fine' as const, dense: true },
    // The case the whole hook exists for: wide enough for the year grid, and
    // physically unable to use it.
    { width: TABLET_LANDSCAPE_WIDTH, pointer: 'coarse' as const, dense: false },
    { width: DESKTOP_WIDTH, pointer: 'fine' as const, dense: true },
    { width: DESKTOP_WIDTH, pointer: 'coarse' as const, dense: false },
    // Width still gates: a phone with a stylus is not a desktop.
    { width: 360, pointer: 'fine' as const, dense: false },
    { width: 360, pointer: 'coarse' as const, dense: false },
    { width: 767, pointer: 'fine' as const, dense: false },
    { width: 768, pointer: 'fine' as const, dense: true },
  ])(
    'at $width with a $pointer pointer, dense = $dense',
    ({ width, pointer, dense }) => {
      setViewportWidth(width);
      setPointer(pointer);
      const { result } = renderHook(() => useFinePointerDesktop());
      expect(result.current).toBe(dense);
    },
  );

  it('answers "not dense" when the environment cannot say', () => {
    // Opposite default to useIsMobileViewport's, deliberately: an unknown
    // device getting large tappable cards is a worse layout and a better
    // outcome than one getting 18px squares it may be unable to read.
    const original = window.matchMedia;
    // @ts-expect-error deleting a DOM global for one assertion
    delete window.matchMedia;
    try {
      const { result } = renderHook(() => useFinePointerDesktop());
      expect(result.current).toBe(false);
    } finally {
      window.matchMedia = original;
    }
  });
});
