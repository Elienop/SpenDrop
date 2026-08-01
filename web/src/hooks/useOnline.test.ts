import { describe, test, expect, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { onlineManager } from '@tanstack/react-query';
import { useOnline } from './useOnline';

const realOnLine = navigator.onLine;

function setNavigatorOnline(value: boolean): void {
  Object.defineProperty(navigator, 'onLine', { configurable: true, value });
}

beforeEach(() => {
  setNavigatorOnline(true);
  onlineManager.setOnline(true);
});

afterEach(() => {
  setNavigatorOnline(realOnLine);
  onlineManager.setOnline(true);
});

describe('useOnline', () => {
  test('starts from navigator.onLine', () => {
    setNavigatorOnline(false);
    const { result } = renderHook(() => useOnline());
    expect(result.current).toBe(false);
  });

  test('flips to false on the window offline event', async () => {
    const { result } = renderHook(() => useOnline());
    expect(result.current).toBe(true);

    act(() => {
      setNavigatorOnline(false);
      window.dispatchEvent(new Event('offline'));
    });

    await waitFor(() => expect(result.current).toBe(false));
  });

  test('flips back to true on the window online event', async () => {
    setNavigatorOnline(false);
    const { result } = renderHook(() => useOnline());
    await waitFor(() => expect(result.current).toBe(false));

    act(() => {
      setNavigatorOnline(true);
      window.dispatchEvent(new Event('online'));
    });

    await waitFor(() => expect(result.current).toBe(true));
  });

  // The UI's idea of "offline" and the query layer's idea of "offline" must be
  // the same fact. If they can disagree, a panel can say "you're offline"
  // while its own query is busy failing (or vice versa).
  test('agrees with the query layer that gates fetching', async () => {
    setNavigatorOnline(false);
    const { result } = renderHook(() => useOnline());

    await waitFor(() => expect(result.current).toBe(false));
    expect(onlineManager.isOnline()).toBe(false);
  });
});
