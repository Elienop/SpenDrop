import { describe, test, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useIsCoarsePointer } from './useIsCoarsePointer';

type Listener = (e: MediaQueryListEvent) => void;

interface MqlShape {
  matches: boolean;
  media: string;
  onchange: null;
  addListener: () => void;
  removeListener: () => void;
  addEventListener: (type: string, l: Listener) => void;
  removeEventListener: (type: string, l: Listener) => void;
  dispatchEvent: () => boolean;
  __fire: (matches: boolean) => void;
}

function installMatchMedia(initial: boolean) {
  const listeners = new Set<Listener>();
  const mql: MqlShape = {
    matches: initial,
    media: '(pointer: coarse)',
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: (_type, l) => {
      listeners.add(l);
    },
    removeEventListener: (_type, l) => {
      listeners.delete(l);
    },
    dispatchEvent: () => false,
    __fire: (matches: boolean) => {
      mql.matches = matches;
      for (const l of listeners) {
        l({ matches } as MediaQueryListEvent);
      }
    },
  };
  const original = window.matchMedia;
  window.matchMedia = vi.fn(() => mql as unknown as MediaQueryList);
  return {
    mql,
    restore: () => {
      window.matchMedia = original;
    },
  };
}

describe('useIsCoarsePointer', () => {
  test('returns false when (pointer: coarse) does not match', () => {
    const { restore } = installMatchMedia(false);
    try {
      const { result } = renderHook(() => useIsCoarsePointer());
      expect(result.current).toBe(false);
    } finally {
      restore();
    }
  });

  test('returns true when (pointer: coarse) matches on mount', () => {
    const { restore } = installMatchMedia(true);
    try {
      const { result } = renderHook(() => useIsCoarsePointer());
      expect(result.current).toBe(true);
    } finally {
      restore();
    }
  });

  test('updates when the media query fires a change event', () => {
    const handle = installMatchMedia(false);
    try {
      const { result } = renderHook(() => useIsCoarsePointer());
      expect(result.current).toBe(false);

      act(() => {
        handle.mql.__fire(true);
      });

      expect(result.current).toBe(true);
    } finally {
      handle.restore();
    }
  });
});
