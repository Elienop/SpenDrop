import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import {
  QueryClient,
  QueryClientProvider,
  onlineManager,
  useQuery,
} from '@tanstack/react-query';
import { installOnlineTracking, readNavigatorOnline } from './online';

const realOnLine = navigator.onLine;

function setNavigatorOnline(value: boolean): void {
  Object.defineProperty(navigator, 'onLine', { configurable: true, value });
}

/** Flip the radio the way a browser does: property first, then the event. */
function goOnline(): void {
  setNavigatorOnline(true);
  window.dispatchEvent(new Event('online'));
}

// `onlineManager` is a module singleton shared by every test in this file, so
// reset it to its as-constructed state (`true`) between tests. Without this a
// test that leaves it `false` makes the next one pass for the wrong reason.
beforeEach(() => {
  setNavigatorOnline(true);
  onlineManager.setOnline(true);
});

afterEach(() => {
  setNavigatorOnline(realOnLine);
  onlineManager.setOnline(true);
});

describe('installOnlineTracking', () => {
  test('readNavigatorOnline reflects navigator.onLine', () => {
    setNavigatorOnline(false);
    expect(readNavigatorOnline()).toBe(false);
    setNavigatorOnline(true);
    expect(readNavigatorOnline()).toBe(true);
  });

  // TanStack's OnlineManager initialises `#online = true` and only ever
  // changes it from a subsequent window 'online'/'offline' event. An app
  // LAUNCHED with no signal therefore never observes itself go offline.
  test('seeds the shared onlineManager from navigator.onLine at install time', () => {
    setNavigatorOnline(false);

    installOnlineTracking();

    expect(onlineManager.isOnline()).toBe(false);
  });

  // The consequence of the un-seeded default: `setOnline(true)` on the
  // 'online' event is a no-op because the manager already thought it was
  // true, so nothing is notified and nothing refetches. The app never heals
  // without a manual reload.
  test('notifies subscribers when connectivity returns after launching offline', async () => {
    setNavigatorOnline(false);
    installOnlineTracking();
    const seen: boolean[] = [];
    const unsubscribe = onlineManager.subscribe((online) => {
      seen.push(online);
    });

    try {
      goOnline();
      expect(onlineManager.isOnline()).toBe(true);
      expect(seen).toContain(true);
    } finally {
      unsubscribe();
    }
  });

  test('tracks a later drop to offline as well', () => {
    installOnlineTracking();
    const unsubscribe = onlineManager.subscribe(() => {});
    try {
      setNavigatorOnline(false);
      window.dispatchEvent(new Event('offline'));
      expect(onlineManager.isOnline()).toBe(false);
    } finally {
      unsubscribe();
    }
  });

  test('is idempotent — installing twice does not double-handle events', () => {
    installOnlineTracking();
    installOnlineTracking();
    const seen: boolean[] = [];
    const unsubscribe = onlineManager.subscribe((online) => {
      seen.push(online);
    });
    try {
      setNavigatorOnline(false);
      window.dispatchEvent(new Event('offline'));
      // setOnline() only notifies on a real change, so a duplicated listener
      // cannot double-notify; the assertion that matters is that the state is
      // correct and settled after a repeat install.
      expect(seen).toEqual([false]);
      expect(onlineManager.isOnline()).toBe(false);
    } finally {
      unsubscribe();
    }
  });
});

describe('offline queries pause instead of failing', () => {
  function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client }, children);
  }

  test('a query started offline is paused, never fetched, never errored', async () => {
    setNavigatorOnline(false);
    installOnlineTracking();
    const queryFn = vi.fn().mockRejectedValue(new Error('should not run'));
    const client = new QueryClient({
      defaultOptions: { queries: { retry: 1 } },
    });

    const { result } = renderHook(
      () => useQuery({ queryKey: ['paused-probe'], queryFn }),
      { wrapper: wrapper(client) },
    );

    await waitFor(() => {
      expect(result.current.fetchStatus).toBe('paused');
    });
    expect(queryFn).not.toHaveBeenCalled();
    expect(result.current.isError).toBe(false);
    client.clear();
  });

  test('the paused query runs by itself once connectivity returns', async () => {
    setNavigatorOnline(false);
    installOnlineTracking();
    const queryFn = vi.fn().mockResolvedValue('history');
    const client = new QueryClient({
      defaultOptions: { queries: { retry: 0 } },
    });

    const { result } = renderHook(
      () => useQuery({ queryKey: ['resume-probe'], queryFn }),
      { wrapper: wrapper(client) },
    );

    await waitFor(() => {
      expect(result.current.fetchStatus).toBe('paused');
    });

    act(() => {
      goOnline();
    });

    await waitFor(() => {
      expect(result.current.data).toBe('history');
    });
    expect(queryFn).toHaveBeenCalledTimes(1);
    client.clear();
  });
});
