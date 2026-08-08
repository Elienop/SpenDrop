import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, afterEach } from 'vitest';
import { useIsMobileViewport, DESKTOP_QUERY } from './useIsMobileViewport';
// Read as text, not imported as a module: the constant under test is a private
// `const` inside MobileNav, and the point is to notice if someone edits it.
import mobileNavSource from '../components/MobileNav.tsx?raw';

/**
 * The breakpoint this hook reports is the one that decides which presentation
 * a page MOUNTS, so getting it wrong swaps the entire Transactions and Trash
 * layouts. It is shared, which is exactly why it is pinned here rather than
 * only through its consumers.
 */
function setViewportWidth(width: number): void {
  (
    window as unknown as {
      happyDOM: { setViewport: (v: { width: number }) => void };
    }
  ).happyDOM.setViewport({ width });
}

const DESKTOP_WIDTH = 1024;

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
});

describe('useIsMobileViewport breakpoint agreement', () => {
  /**
   * The JS gate decides which presentation MOUNTS; Tailwind's `md:` decides
   * which one the CSS is written for. If the two ever disagree there is a
   * viewport where BOTH or NEITHER renders, and neither failure shows up in a
   * test that only ever runs at one width.
   */
  it('gates on the exact string MobileNav uses', () => {
    const match = mobileNavSource.match(/DESKTOP_QUERY\s*=\s*'([^']+)'/);
    expect(match).not.toBeNull();
    expect(DESKTOP_QUERY).toBe(match![1]);
    // Guards the test itself: a regex that silently stopped matching would
    // leave `match![1]` comparing against nothing meaningful.
    expect(DESKTOP_QUERY).toBe('(min-width: 768px)');
  });

  it('leaves no width where both or neither presentation would render', () => {
    for (const width of [320, 375, 390, 600, 767, 768, 769, 1024, 1440]) {
      setViewportWidth(width);
      const isDesktop = window.matchMedia(DESKTOP_QUERY).matches;
      const { result, unmount } = renderHook(() => useIsMobileViewport());
      // Exactly one of the two is true at every width — the hook is the
      // NEGATION of the desktop query, not a separately-authored mirror of it.
      expect(result.current).toBe(!isDesktop);
      unmount();
    }
  });
});

describe('useIsMobileViewport', () => {
  it('reports phone width', () => {
    setViewportWidth(390);
    const { result } = renderHook(() => useIsMobileViewport());
    expect(result.current).toBe(true);
  });

  it('reports desktop width', () => {
    setViewportWidth(1024);
    const { result } = renderHook(() => useIsMobileViewport());
    expect(result.current).toBe(false);
  });

  // Both sides of the `md` boundary. Tailwind's `md:` utilities switch on at
  // min-width 768px, so 767 must still be phone and 768 must already be
  // desktop — an off-by-one here would leave a 767px tablet rendering a
  // seven-column table.
  it('treats 767px as phone and 768px as desktop', () => {
    setViewportWidth(767);
    const phone = renderHook(() => useIsMobileViewport());
    expect(phone.result.current).toBe(true);
    phone.unmount();

    setViewportWidth(768);
    const desktop = renderHook(() => useIsMobileViewport());
    expect(desktop.result.current).toBe(false);
  });

  // The value is read during the FIRST render, not applied by an effect
  // afterwards — otherwise every phone load paints the desktop table for a
  // frame before swapping.
  it('is correct on the very first render, with no effect pass needed', () => {
    setViewportWidth(390);
    let firstRenderValue: boolean | null = null;
    renderHook(() => {
      const value = useIsMobileViewport();
      firstRenderValue ??= value;
      return value;
    });
    expect(firstRenderValue).toBe(true);
  });

  /**
   * Rotation, driven through a controllable matchMedia.
   *
   * `happyDOM.setViewport` updates `MediaQueryList.matches` live but does NOT
   * dispatch the `change` event under vitest's happy-dom (verified with a
   * probe: `matches` flipped, the listener never ran), so driving this end to
   * end through the viewport would assert nothing about the subscription. The
   * stub is the same idiom MobileNav.test.tsx uses. The real browser rotation
   * remains a browser-pass item.
   */
  it('re-renders through its change subscription, and unsubscribes on unmount', () => {
    const listeners = new Set<(e: MediaQueryListEvent) => void>();
    // The stub stands in for the DESKTOP query, which the hook negates — so
    // `matches: false` is a phone and flipping it to `true` is the rotation.
    let matches = false;
    const original = window.matchMedia;
    window.matchMedia = (() => ({
      get matches() {
        return matches;
      },
      media: DESKTOP_QUERY,
      addEventListener: (_: string, cb: (e: MediaQueryListEvent) => void) => {
        listeners.add(cb);
      },
      removeEventListener: (_: string, cb: (e: MediaQueryListEvent) => void) => {
        listeners.delete(cb);
      },
    })) as unknown as typeof window.matchMedia;

    try {
      const { result, unmount } = renderHook(() => useIsMobileViewport());
      expect(result.current).toBe(true);
      expect(listeners.size).toBe(1);

      act(() => {
        matches = true;
        for (const cb of listeners) {
          cb({ matches: true } as MediaQueryListEvent);
        }
      });
      expect(result.current).toBe(false);

      // A listener left behind would keep calling into an unmounted tree
      // every time a phone rotates.
      unmount();
      expect(listeners.size).toBe(0);
    } finally {
      window.matchMedia = original;
    }
  });

  // The fallback that keeps every pre-existing desktop test on the table path,
  // and that stops the hook throwing anywhere `matchMedia` is absent.
  it('answers desktop when matchMedia is unavailable', () => {
    const original = window.matchMedia;
    // @ts-expect-error deliberately removing a DOM API the hook feature-detects
    delete window.matchMedia;
    try {
      const { result } = renderHook(() => useIsMobileViewport());
      expect(result.current).toBe(false);
    } finally {
      window.matchMedia = original;
    }
  });
});
