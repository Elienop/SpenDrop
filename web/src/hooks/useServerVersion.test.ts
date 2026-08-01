import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';

// Spread the original so `ApiError` / `NetworkError` stay real classes — a
// bare factory mock replaces them with `undefined`, and every
// `instanceof ApiError` in the tree under test becomes a TypeError rather
// than a false.
vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/client')>();
  return { ...actual, api: { get: vi.fn() } };
});

import { api, ApiError, NetworkError } from '@/api/client';
import { useServerVersion } from './useServerVersion';

const mockedGet = api.get as unknown as ReturnType<typeof vi.fn>;

/**
 * A harness that MIRRORS the app-wide `retry: 1` default rather than
 * disabling it, because opting out of retries is a property of this hook that
 * the last test asserts — a harness with `retry: false` would make that test
 * pass no matter what the hook does. `retryDelay: 0` only compresses the
 * one-second backoff so a regression surfaces within the test, not after it.
 */
function makeHarness() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: 1, retryDelay: 0 } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return { client, wrapper };
}

/** Block until the ['version'] query has actually settled. */
async function settled(client: QueryClient, status: 'success' | 'error') {
  await waitFor(() =>
    expect(client.getQueryState(['version'])?.status).toBe(status),
  );
}

describe('useServerVersion', () => {
  beforeEach(() => {
    // clearAllMocks does NOT drain a mockResolvedValueOnce queue; reset does.
    mockedGet.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('reports the version the server sends', async () => {
    mockedGet.mockResolvedValue({ version: 'v0.35.0' });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useServerVersion(), { wrapper });

    await waitFor(() => expect(result.current.serverVersion).toBe('v0.35.0'));
    expect(mockedGet).toHaveBeenCalledWith('version');
  });

  it('starts at null before the first response', () => {
    mockedGet.mockResolvedValue({ version: 'v0.35.0' });
    const { wrapper } = makeHarness();

    const { result } = renderHook(() => useServerVersion(), { wrapper });

    expect(result.current.serverVersion).toBeNull();
  });

  it('reports null for a 200 whose body has no version field', async () => {
    // The decode-level trap: `VersionResponse` would happily "decode" `{}`
    // and hand `undefined` to the UI. The query SUCCEEDS here — only the
    // field guard stands between that body and the rendered stamp.
    mockedGet.mockResolvedValue({});
    const { client, wrapper } = makeHarness();

    const { result } = renderHook(() => useServerVersion(), { wrapper });

    await settled(client, 'success');
    expect(result.current.serverVersion).toBeNull();
  });

  it('reports null when the version field is not a non-empty string', async () => {
    mockedGet.mockResolvedValue({ version: 42 });
    const { client, wrapper } = makeHarness();

    const { result } = renderHook(() => useServerVersion(), { wrapper });

    await settled(client, 'success');
    expect(result.current.serverVersion).toBeNull();
  });

  it('degrades to null on a 404 from a server without the endpoint', async () => {
    mockedGet.mockRejectedValue(new ApiError('HTTP 404', 404));
    const { client, wrapper } = makeHarness();

    const { result } = renderHook(() => useServerVersion(), { wrapper });

    await settled(client, 'error');
    expect(result.current.serverVersion).toBeNull();
  });

  it('degrades to null on a 401 without disturbing anything else', async () => {
    mockedGet.mockRejectedValue(new ApiError('Unauthorized', 401));
    const { client, wrapper } = makeHarness();

    const { result } = renderHook(() => useServerVersion(), { wrapper });

    await settled(client, 'error');
    expect(result.current.serverVersion).toBeNull();
  });

  it('degrades to null when the server could not be reached', async () => {
    mockedGet.mockRejectedValue(
      new NetworkError('The device is offline', 'offline'),
    );
    const { client, wrapper } = makeHarness();

    const { result } = renderHook(() => useServerVersion(), { wrapper });

    await settled(client, 'error');
    expect(result.current.serverVersion).toBeNull();
  });

  it('does not retry a failed fetch', async () => {
    // A household on a server predating this endpoint 404s on every mount.
    // Under the app-wide `retry: 1` the harness applies, that would be two
    // requests each time, forever; this hook opts out.
    mockedGet.mockRejectedValue(new ApiError('HTTP 404', 404));
    const { client, wrapper } = makeHarness();

    renderHook(() => useServerVersion(), { wrapper });

    await settled(client, 'error');
    expect(mockedGet).toHaveBeenCalledTimes(1);
  });
});
