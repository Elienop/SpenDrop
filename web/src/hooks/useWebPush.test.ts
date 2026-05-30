import { describe, test, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { api } from '@/api/client';
import { useWebPush } from './useWebPush';
import { pushTestState, makeSubscription } from '@/test/setup';

vi.mock('@/api/client', () => ({
  api: { get: vi.fn(), post: vi.fn(), del: vi.fn() },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(m: string, s: number) {
      super(m);
      this.status = s;
    }
  },
}));

const mockApi = vi.mocked(api);

beforeEach(() => {
  vi.clearAllMocks();
  pushTestState.permission = 'default';
  pushTestState.subscription = null;
  mockApi.get.mockResolvedValue({
    publicKey:
      'BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8M',
  });
  mockApi.post.mockResolvedValue(undefined);
  mockApi.del.mockResolvedValue(undefined);
});

describe('useWebPush', () => {
  test('reports supported + initial unsubscribed state', async () => {
    const { result } = renderHook(() => useWebPush());
    await waitFor(() => expect(result.current.supported).toBe(true));
    expect(result.current.subscribed).toBe(false);
    expect(result.current.permission).toBe('default');
  });

  test('enable() subscribes and POSTs the toJSON shape', async () => {
    const { result } = renderHook(() => useWebPush());
    await waitFor(() => expect(result.current.supported).toBe(true));
    await act(async () => {
      await result.current.enable();
    });
    expect(mockApi.get).toHaveBeenCalledWith('push/vapid-public-key');
    expect(mockApi.post).toHaveBeenCalledWith(
      'push/subscriptions',
      expect.objectContaining({
        endpoint: 'https://push.example/abc',
        keys: { p256dh: 'p256dh-test', auth: 'auth-test' },
      }),
    );
    await waitFor(() => expect(result.current.subscribed).toBe(true));
  });

  test('enable() does nothing destructive when permission is denied', async () => {
    pushTestState.permission = 'denied';
    const { result } = renderHook(() => useWebPush());
    await waitFor(() => expect(result.current.permission).toBe('denied'));
    await act(async () => {
      await result.current.enable();
    });
    expect(mockApi.post).not.toHaveBeenCalled();
    expect(result.current.subscribed).toBe(false);
  });

  test('already-subscribed read reflects a pre-existing subscription', async () => {
    pushTestState.permission = 'granted';
    pushTestState.subscription = makeSubscription();
    const { result } = renderHook(() => useWebPush());
    await waitFor(() => expect(result.current.subscribed).toBe(true));
  });

  test('disable() unsubscribes and DELETEs by endpoint', async () => {
    pushTestState.permission = 'granted';
    const sub = makeSubscription();
    pushTestState.subscription = sub;
    const { result } = renderHook(() => useWebPush());
    await waitFor(() => expect(result.current.subscribed).toBe(true));
    await act(async () => {
      await result.current.disable();
    });
    expect(sub.unsubscribe).toHaveBeenCalled();
    expect(mockApi.del).toHaveBeenCalledWith('push/subscriptions', {
      endpoint: sub.endpoint,
    });
    await waitFor(() => expect(result.current.subscribed).toBe(false));
  });

  test('reconciles a pre-existing subscription to the server on mount', async () => {
    pushTestState.permission = 'granted';
    pushTestState.subscription = makeSubscription('https://push.example/rotated');
    const { result } = renderHook(() => useWebPush());
    await waitFor(() => expect(result.current.subscribed).toBe(true));
    // Mount-time reconcile re-POSTs the current (possibly rotated) endpoint.
    await waitFor(() =>
      expect(mockApi.post).toHaveBeenCalledWith(
        'push/subscriptions',
        expect.objectContaining({ endpoint: 'https://push.example/rotated' }),
      ),
    );
  });

  test('does NOT reconcile when there is no local subscription', async () => {
    pushTestState.permission = 'granted';
    pushTestState.subscription = null;
    const { result } = renderHook(() => useWebPush());
    await waitFor(() => expect(result.current.supported).toBe(true));
    // Give the mount effect a tick to settle.
    await act(async () => {
      await Promise.resolve();
    });
    expect(mockApi.post).not.toHaveBeenCalled();
  });
});
