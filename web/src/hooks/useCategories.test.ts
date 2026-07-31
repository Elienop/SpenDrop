import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import {
  QueryClient,
  QueryClientProvider,
  onlineManager,
} from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { installOnlineTracking } from '@/lib/online';
import type { Category } from '@/api/types';

const realOnLine = navigator.onLine;

function setNavigatorOnline(value: boolean): void {
  Object.defineProperty(navigator, 'onLine', { configurable: true, value });
}

vi.mock('@/api/client', () => ({
  api: { get: vi.fn() },
}));

import { api } from '@/api/client';
import { useCategories } from './useCategories';

const mockedGet = api.get as unknown as ReturnType<typeof vi.fn>;

const sample: Category[] = [
  { id: 1, name: 'Food', type: 'expense', icon: null, sort_order: 0, is_active: true, created_at: '' },
  { id: 2, name: 'Salary', type: 'income', icon: null, sort_order: 1, is_active: true, created_at: '' },
];

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

describe('useCategories', () => {
  beforeEach(() => {
    mockedGet.mockReset();
    setNavigatorOnline(true);
    onlineManager.setOnline(true);
  });

  afterEach(() => {
    setNavigatorOnline(realOnLine);
    onlineManager.setOnline(true);
  });

  // The service worker serves GET /api/categories from cache when the network
  // is gone (NetworkFirst, spendrop-api-lists) — that cache is the ONLY reason
  // the offline capture screen has categories to tap. A query that pauses
  // itself offline never issues the request, so the cache is never consulted
  // and offline capture is left with no categories at all.
  it('still asks (so the service worker cache can answer) while offline', async () => {
    setNavigatorOnline(false);
    installOnlineTracking();
    mockedGet.mockResolvedValue(sample);

    const { result } = renderHook(() => useCategories(), {
      wrapper: makeWrapper(),
    });

    await waitFor(() => expect(result.current.categories).toEqual(sample));
    expect(mockedGet).toHaveBeenCalledWith('categories');
  });

  it('fetches categories on mount and exposes them', async () => {
    mockedGet.mockResolvedValue(sample);
    const { result } = renderHook(() => useCategories(), { wrapper: makeWrapper() });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(mockedGet).toHaveBeenCalledWith('categories');
    expect(result.current.categories).toEqual(sample);
    expect(result.current.error).toBe('');
  });

  it('starts empty while loading', () => {
    mockedGet.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useCategories(), { wrapper: makeWrapper() });
    expect(result.current.loading).toBe(true);
    expect(result.current.categories).toEqual([]);
  });

  it('exposes the error message and an empty list on failure', async () => {
    mockedGet.mockRejectedValue(new Error('Failed to load categories'));
    const { result } = renderHook(() => useCategories(), { wrapper: makeWrapper() });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe('Failed to load categories');
    expect(result.current.categories).toEqual([]);
  });

  it('refetch re-pulls the list', async () => {
    mockedGet.mockResolvedValue(sample);
    const { result } = renderHook(() => useCategories(), { wrapper: makeWrapper() });
    await waitFor(() => expect(result.current.loading).toBe(false));

    result.current.refetch();
    await waitFor(() => expect(mockedGet).toHaveBeenCalledTimes(2));
  });
});
