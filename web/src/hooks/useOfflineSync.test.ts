import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

const drainQueue = vi.fn();
vi.mock('@/lib/offline-queue', () => ({ drainQueue: (...a: unknown[]) => drainQueue(...a) }));

const success = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign(() => undefined, { success: (...a: unknown[]) => success(...a) }),
}));

// The hook only reads `.user`; a mutable variable drives authed vs null cases.
let currentUser: { id: number } | null = { id: 1 };
vi.mock('@/hooks/useAuth', () => ({ useAuth: () => ({ user: currentUser }) }));

import { useOfflineSync } from './useOfflineSync';

beforeEach(() => {
  drainQueue.mockReset();
  success.mockReset();
  currentUser = { id: 1 };
});

describe('useOfflineSync', () => {
  test('drains on mount when authenticated and toasts on synced>0', async () => {
    drainQueue.mockResolvedValue({ synced: 2, remaining: 0 });
    renderHook(() => useOfflineSync());
    await waitFor(() => expect(drainQueue).toHaveBeenCalledTimes(1));
    expect(drainQueue).toHaveBeenCalledWith(1);
    await waitFor(() => expect(success).toHaveBeenCalledTimes(1));
    expect(success.mock.calls[0][0]).toContain('Synced 2 offline entries');
  });

  test('does NOT drain when unauthenticated', async () => {
    currentUser = null;
    renderHook(() => useOfflineSync());
    await new Promise((r) => setTimeout(r, 5));
    expect(drainQueue).not.toHaveBeenCalled();
  });

  test('no toast when nothing synced', async () => {
    drainQueue.mockResolvedValue({ synced: 0, remaining: 0 });
    renderHook(() => useOfflineSync());
    await waitFor(() => expect(drainQueue).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 5));
    expect(success).not.toHaveBeenCalled();
  });

  test('re-drains on the window online event', async () => {
    drainQueue.mockResolvedValue({ synced: 0, remaining: 0 });
    renderHook(() => useOfflineSync());
    await waitFor(() => expect(drainQueue).toHaveBeenCalledTimes(1));
    act(() => {
      window.dispatchEvent(new Event('online'));
    });
    await waitFor(() => expect(drainQueue).toHaveBeenCalledTimes(2));
  });

  test('swallows a drain rejection without throwing', async () => {
    drainQueue.mockRejectedValue(new Error('idb'));
    renderHook(() => useOfflineSync());
    await waitFor(() => expect(drainQueue).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 5));
    expect(success).not.toHaveBeenCalled();
  });
});
