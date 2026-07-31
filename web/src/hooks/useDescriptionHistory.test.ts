import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import {
  QueryClient,
  QueryClientProvider,
  onlineManager,
} from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { installOnlineTracking } from '@/lib/online';
import type { Transaction } from '@/api/types';

const realOnLine = navigator.onLine;

function setNavigatorOnline(value: boolean): void {
  Object.defineProperty(navigator, 'onLine', { configurable: true, value });
}

const apiGet = vi.fn();
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

// Re-export under the relative specifier the hook uses.
vi.mock('../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

import { useDescriptionHistory } from './useDescriptionHistory';

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

function tx(over: Partial<Transaction>): Transaction {
  return {
    id: 0,
    user_id: 1,
    date: '2026-05-27',
    amount: 1,
    original_amount: null,
    original_currency: null,
    description: '',
    category_id: 1,
    category_name: 'X',
    category_type: 'expense',
    tags: null,
    notes: null,
    created_at: '',
    updated_at: '',
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  setNavigatorOnline(true);
  onlineManager.setOnline(true);
});

afterEach(() => {
  setNavigatorOnline(realOnLine);
  onlineManager.setOnline(true);
});

describe('useDescriptionHistory', () => {
  test('returns empty list before the fetch resolves', () => {
    apiGet.mockImplementation(() => new Promise(() => {})); // never resolves
    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });
    expect(result.current.descriptions).toEqual([]);
  });

  test('ranks distinct descriptions by frequency (desc)', async () => {
    apiGet.mockResolvedValue({
      transactions: [
        tx({ id: 1, description: 'lunch' }),
        tx({ id: 2, description: 'lunch' }),
        tx({ id: 3, description: 'coffee' }),
      ],
    });
    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });
    await waitFor(() => expect(result.current.descriptions.length).toBe(2));
    expect(result.current.descriptions).toEqual(['lunch', 'coffee']);
  });

  test('breaks ties on recency (more recent wins)', async () => {
    // Backend pre-sorts by date desc, so insertion-order index 0 is most recent.
    apiGet.mockResolvedValue({
      transactions: [
        tx({ id: 10, description: 'newer' }), // most recent
        tx({ id: 11, description: 'older' }), // less recent
      ],
    });
    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });
    await waitFor(() => expect(result.current.descriptions.length).toBe(2));
    expect(result.current.descriptions).toEqual(['newer', 'older']);
  });

  test('case-insensitive dedupe — collapses to one entry preserving first-seen casing', async () => {
    apiGet.mockResolvedValue({
      transactions: [
        tx({ id: 1, description: 'Coffee' }),
        tx({ id: 2, description: 'coffee' }),
        tx({ id: 3, description: 'COFFEE' }),
      ],
    });
    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });
    await waitFor(() => expect(result.current.descriptions.length).toBe(1));
    expect(result.current.descriptions).toEqual(['Coffee']);
  });

  // "We couldn't load your history" and "you have no history" look identical
  // to the user unless the hook says which one happened. Suggestions silently
  // vanishing reads as "the app has forgotten everything I ever typed".
  test('reports a fetch error separately from an empty history', async () => {
    apiGet.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });

    await waitFor(() => expect(result.current.failed).toBe(true));
    expect(result.current.descriptions).toEqual([]);
  });

  test('an empty history is not an error', async () => {
    apiGet.mockResolvedValue({ transactions: [] });
    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });

    await waitFor(() => expect(apiGet).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 10));
    expect(result.current.descriptions).toEqual([]);
    expect(result.current.failed).toBe(false);
  });

  test('reports "waiting for a connection" while the query is paused offline', async () => {
    setNavigatorOnline(false);
    installOnlineTracking();
    apiGet.mockResolvedValue({ transactions: [] });

    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });

    await waitFor(() => expect(result.current.waitingForNetwork).toBe(true));
    // Paused, not failed: there is nothing to retry until the radio is back.
    expect(result.current.failed).toBe(false);
    expect(apiGet).not.toHaveBeenCalled();
  });

  test('retry re-runs the fetch after a failure', async () => {
    apiGet.mockRejectedValueOnce(new Error('boom'));
    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });
    await waitFor(() => expect(result.current.failed).toBe(true));

    apiGet.mockResolvedValueOnce({
      transactions: [tx({ id: 1, description: 'coffee' })],
    });
    act(() => {
      result.current.retry();
    });

    await waitFor(() => expect(result.current.descriptions).toEqual(['coffee']));
    expect(result.current.failed).toBe(false);
  });

  test('ignores blank / whitespace-only descriptions', async () => {
    apiGet.mockResolvedValue({
      transactions: [
        tx({ id: 1, description: 'lunch' }),
        tx({ id: 2, description: '   ' }),
        tx({ id: 3, description: '' }),
      ],
    });
    const { result } = renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });
    await waitFor(() => expect(result.current.descriptions.length).toBe(1));
    expect(result.current.descriptions).toEqual(['lunch']);
  });

  test('fetches with per_page=200 sorted by date desc', async () => {
    apiGet.mockResolvedValue({ transactions: [] });
    renderHook(() => useDescriptionHistory(), { wrapper: makeWrapper() });
    await waitFor(() => expect(apiGet).toHaveBeenCalled());
    const path = apiGet.mock.calls[0][0] as string;
    expect(path).toContain('transactions?');
    expect(path).toContain('per_page=200');
    expect(path).toContain('sort_by=date');
    expect(path).toContain('sort_dir=desc');
  });
});
