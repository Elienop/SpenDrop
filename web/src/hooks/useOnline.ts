import { useEffect, useState } from 'react';

/**
 * Reactive online/offline status. Seeds from `navigator.onLine` and tracks the
 * window `online`/`offline` events. Used by the /quick "Recently added" panel
 * to decide whether saved history can be loaded (it lives on the server) and
 * to show an offline note instead.
 */
export function useOnline(): boolean {
  const [online, setOnline] = useState(() =>
    typeof navigator === 'undefined' ? true : navigator.onLine,
  );

  useEffect(() => {
    const up = (): void => setOnline(true);
    const down = (): void => setOnline(false);
    window.addEventListener('online', up);
    window.addEventListener('offline', down);
    return () => {
      window.removeEventListener('online', up);
      window.removeEventListener('offline', down);
    };
  }, []);

  return online;
}
