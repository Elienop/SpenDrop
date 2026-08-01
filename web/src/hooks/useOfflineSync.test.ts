import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

const drainQueue = vi.fn();
vi.mock('@/lib/offline-queue', () => ({ drainQueue: (...a: unknown[]) => drainQueue(...a) }));

const success = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign(() => undefined, { success: (...a: unknown[]) => success(...a) }),
}));

// The hook reads `.user` and `.unverified`; mutable variables drive the cases.
let currentUser: { id: number } | null = { id: 1 };
let currentUnverified = false;
vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => ({ user: currentUser, unverified: currentUnverified }),
}));

import { useOfflineSync } from './useOfflineSync';

beforeEach(() => {
  drainQueue.mockReset();
  success.mockReset();
  currentUser = { id: 1 };
  currentUnverified = false;
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

  // An identity the server has not confirmed must not post anything. The
  // server attributes a created row purely from the session cookie, so
  // replaying under an unconfirmed identity is how a capture lands in the
  // wrong household member's ledger.
  test('does NOT drain while the identity is unverified', async () => {
    currentUnverified = true;
    drainQueue.mockResolvedValue({ synced: 0, remaining: 1 });

    renderHook(() => useOfflineSync());
    await new Promise((r) => setTimeout(r, 5));
    act(() => {
      window.dispatchEvent(new Event('online'));
    });
    await new Promise((r) => setTimeout(r, 5));

    expect(drainQueue).not.toHaveBeenCalled();
  });

  test('drains as soon as the server confirms the identity', async () => {
    currentUnverified = true;
    drainQueue.mockResolvedValue({ synced: 1, remaining: 0 });
    const { rerender } = renderHook(() => useOfflineSync());
    await new Promise((r) => setTimeout(r, 5));
    expect(drainQueue).not.toHaveBeenCalled();

    // Re-verification succeeded — no reload, no user action.
    currentUnverified = false;
    rerender();

    await waitFor(() => expect(drainQueue).toHaveBeenCalledWith(1));
  });

  test('keeps listening for reconnection even when there is nobody to sync for', async () => {
    currentUser = null;
    const { rerender } = renderHook(() => useOfflineSync());

    // A reconnection while signed out must not throw, and must not leave the
    // hook deaf once a user appears.
    act(() => {
      window.dispatchEvent(new Event('online'));
    });
    expect(drainQueue).not.toHaveBeenCalled();

    currentUser = { id: 3 };
    drainQueue.mockResolvedValue({ synced: 0, remaining: 0 });
    rerender();
    await waitFor(() => expect(drainQueue).toHaveBeenCalledWith(3));

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
