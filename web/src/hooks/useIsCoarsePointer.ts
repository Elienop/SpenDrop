import { useEffect, useState } from 'react';

/**
 * SSR-safe hook that returns true when the primary pointing device is coarse
 * (touch / stylus). Listens for changes, so desktops that dock/undock a tablet
 * update without a reload.
 */
export function useIsCoarsePointer(): boolean {
  const [coarse, setCoarse] = useState<boolean>(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return false;
    }
    return window.matchMedia('(pointer: coarse)').matches;
  });

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return;
    }
    const mql = window.matchMedia('(pointer: coarse)');
    const onChange = (e: MediaQueryListEvent) => setCoarse(e.matches);
    mql.addEventListener('change', onChange);
    return () => mql.removeEventListener('change', onChange);
  }, []);

  return coarse;
}
