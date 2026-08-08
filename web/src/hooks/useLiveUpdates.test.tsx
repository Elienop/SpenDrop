import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';

// --- Mock the shared QueryClient so we assert invalidateQueries calls -------
const invalidateQueries = vi.fn().mockResolvedValue(undefined);
vi.mock('@/lib/queryClient', () => ({
  queryClient: { invalidateQueries: (...a: unknown[]) => invalidateQueries(...a) },
}));

// The hook only reads `.user`; a mutable variable drives authed vs null cases.
let currentUser: { id: number } | null = { id: 1 };
// refreshUser lives in the factory closure rather than being minted per call:
// a fresh vi.fn() on every render would churn its identity, which is a trap for
// the first consumer that puts it in a dependency array.
vi.mock('@/hooks/useAuth', () => {
  const refreshUser = vi.fn();
  return { useAuth: () => ({ user: currentUser, refreshUser }) };
});

// --- EventSource test double (happy-dom ships none). Models NAMED events:
// the server sends `event: invalidate`, which a real EventSource routes to
// addEventListener('invalidate', …) — NOT to onmessage. The mock dispatches the
// same way, so an onmessage-based regression fails this suite. ---------------
type SseListener = (ev: MessageEvent) => void;

class MockEventSource {
  static instances: MockEventSource[] = [];
  url: string;
  withCredentials: boolean;
  readyState = 0;
  onopen: ((this: EventSource, ev: Event) => void) | null = null;
  onerror: ((this: EventSource, ev: Event) => void) | null = null;
  private listeners = new Map<string, Set<SseListener>>();
  close = vi.fn(() => {
    this.readyState = 2;
  });
  constructor(url: string, init?: EventSourceInit) {
    this.url = url;
    this.withCredentials = init?.withCredentials ?? false;
    MockEventSource.instances.push(this);
  }
  addEventListener(type: string, cb: SseListener): void {
    const set = this.listeners.get(type) ?? new Set<SseListener>();
    set.add(cb);
    this.listeners.set(type, set);
  }
  removeEventListener(type: string, cb: SseListener): void {
    this.listeners.get(type)?.delete(cb);
  }
  emitOpen(): void {
    this.readyState = 1;
    this.onopen?.call(this as unknown as EventSource, new Event('open'));
  }
  // Dispatch a NAMED `invalidate` event, exactly as the server emits it.
  emitMessage(data: unknown): void {
    this.emitRaw(JSON.stringify(data));
  }
  emitRaw(raw: string): void {
    const ev = new MessageEvent('invalidate', { data: raw });
    this.listeners.get('invalidate')?.forEach((cb) => cb(ev));
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
      es.emitRaw('not-json{');
      vi.advanceTimersByTime(200);
    });
    expect(invalidateQueries).not.toHaveBeenCalled();
  });
});
