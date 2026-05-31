import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';

// --- Mock the shared QueryClient so we assert invalidateQueries calls -------
const invalidateQueries = vi.fn().mockResolvedValue(undefined);
vi.mock('@/lib/queryClient', () => ({
  queryClient: { invalidateQueries: (...a: unknown[]) => invalidateQueries(...a) },
}));

// The hook only reads `.user`; a mutable variable drives authed vs null cases.
let currentUser: { id: number } | null = { id: 1 };
vi.mock('@/hooks/useAuth', () => ({ useAuth: () => ({ user: currentUser }) }));

// --- EventSource test double (happy-dom ships none) -------------------------
class MockEventSource {
  static instances: MockEventSource[] = [];
  url: string;
  withCredentials: boolean;
  readyState = 0;
  onopen: ((this: EventSource, ev: Event) => void) | null = null;
  onmessage: ((this: EventSource, ev: MessageEvent) => void) | null = null;
  onerror: ((this: EventSource, ev: Event) => void) | null = null;
  close = vi.fn(() => {
    this.readyState = 2;
  });
  constructor(url: string, init?: EventSourceInit) {
    this.url = url;
    this.withCredentials = init?.withCredentials ?? false;
    MockEventSource.instances.push(this);
  }
  emitOpen(): void {
    this.readyState = 1;
    this.onopen?.call(this as unknown as EventSource, new Event('open'));
  }
  emitMessage(data: unknown): void {
    this.onmessage?.call(
      this as unknown as EventSource,
      new MessageEvent('message', { data: JSON.stringify(data) }),
    );
  }
  emitError(): void {
    this.onerror?.call(this as unknown as EventSource, new Event('error'));
  }
}

import { useLiveUpdates } from './useLiveUpdates';

let visibility: DocumentVisibilityState = 'visible';
function setVisibility(v: DocumentVisibilityState): void {
  visibility = v;
  document.dispatchEvent(new Event('visibilitychange'));
}

beforeEach(() => {
  vi.useFakeTimers();
  invalidateQueries.mockClear();
  currentUser = { id: 1 };
  MockEventSource.instances = [];
  vi.stubGlobal('EventSource', MockEventSource as unknown as typeof EventSource);
  visibility = 'visible';
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => visibility,
  });
});

afterEach(() => {
  vi.runOnlyPendingTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('useLiveUpdates', () => {
  test('opens one EventSource at /api/events with credentials when authenticated', () => {
    renderHook(() => useLiveUpdates());
    expect(MockEventSource.instances).toHaveLength(1);
    expect(MockEventSource.instances[0].url).toBe('/api/events');
    expect(MockEventSource.instances[0].withCredentials).toBe(true);
  });

  test('does NOT open a connection when unauthenticated', () => {
    currentUser = null;
    renderHook(() => useLiveUpdates());
    expect(MockEventSource.instances).toHaveLength(0);
  });

  test('a message invalidates each resource key with a 200ms trailing debounce', () => {
    renderHook(() => useLiveUpdates());
    const es = MockEventSource.instances[0];
    act(() => {
      es.emitMessage({ resources: ['transactions', 'dashboard'] });
    });
    expect(invalidateQueries).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(invalidateQueries).toHaveBeenCalledTimes(2);
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['transactions'] });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['dashboard'] });
  });

  test('coalesces a burst per resource into one invalidate (trailing debounce)', () => {
    renderHook(() => useLiveUpdates());
    const es = MockEventSource.instances[0];
    act(() => {
      es.emitMessage({ resources: ['transactions'] });
      vi.advanceTimersByTime(50);
      es.emitMessage({ resources: ['transactions'] });
      vi.advanceTimersByTime(50);
      es.emitMessage({ resources: ['transactions'] });
    });
    expect(invalidateQueries).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(invalidateQueries).toHaveBeenCalledTimes(1);
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['transactions'] });
  });

  test('first onopen does NOT trigger an all-resource sweep', () => {
    renderHook(() => useLiveUpdates());
    const es = MockEventSource.instances[0];
    act(() => {
      es.emitOpen();
      vi.advanceTimersByTime(200);
    });
    expect(invalidateQueries).not.toHaveBeenCalledWith();
  });

  test('onopen AFTER a prior error triggers an all-resource sweep', () => {
    renderHook(() => useLiveUpdates());
    const es = MockEventSource.instances[0];
    act(() => {
      es.emitOpen(); // initial connect (no sweep)
      es.emitError(); // connection dropped
      es.emitOpen(); // reconnected → sweep everything
    });
    expect(invalidateQueries).toHaveBeenCalledWith();
  });

  test('visibilitychange→visible triggers an all-resource sweep', () => {
    renderHook(() => useLiveUpdates());
    invalidateQueries.mockClear();
    act(() => {
      setVisibility('hidden');
      setVisibility('visible');
    });
    expect(invalidateQueries).toHaveBeenCalledWith();
  });

  test('closes the connection on unmount (StrictMode-safe cleanup)', () => {
    const { unmount } = renderHook(() => useLiveUpdates());
    const es = MockEventSource.instances[0];
    unmount();
    expect(es.close).toHaveBeenCalledTimes(1);
  });

  test('a remount (double-mount) keeps exactly one live connection', () => {
    const first = renderHook(() => useLiveUpdates());
    expect(MockEventSource.instances).toHaveLength(1);
    const firstEs = MockEventSource.instances[0];
    first.unmount();
    expect(firstEs.close).toHaveBeenCalledTimes(1);
    renderHook(() => useLiveUpdates());
    const open = MockEventSource.instances.filter((e) => e.readyState !== 2);
    expect(open).toHaveLength(1);
  });

  test('closes the connection when the user becomes null (logout)', () => {
    const { rerender } = renderHook(() => useLiveUpdates());
    const es = MockEventSource.instances[0];
    currentUser = null;
    rerender();
    expect(es.close).toHaveBeenCalledTimes(1);
  });

  test('ignores a malformed message without throwing', () => {
    renderHook(() => useLiveUpdates());
    const es = MockEventSource.instances[0];
    act(() => {
      es.onmessage?.call(
        es as unknown as EventSource,
        new MessageEvent('message', { data: 'not-json{' }),
      );
      vi.advanceTimersByTime(200);
    });
    expect(invalidateQueries).not.toHaveBeenCalled();
  });
});
