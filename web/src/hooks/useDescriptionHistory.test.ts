import { describe, test, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import type { Transaction } from '@/api/types';

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
});

describe('useDescriptionHistory', () => {
  test('returns empty list before the fetch resolves', () => {
    apiGet.mockImplementation(() => new Promise(() => {})); // never resolves
    const { result } = renderHook(() => useDescriptionHistory());
    expect(result.current).toEqual([]);
  });

  test('ranks distinct descriptions by frequency (desc)', async () => {
    apiGet.mockResolvedValue({
      transactions: [
        tx({ id: 1, description: 'lunch' }),
        tx({ id: 2, description: 'lunch' }),
        tx({ id: 3, description: 'coffee' }),
      ],
    });
    const { result } = renderHook(() => useDescriptionHistory());
    await waitFor(() => expect(result.current.length).toBe(2));
    expect(result.current).toEqual(['lunch', 'coffee']);
  });

  test('breaks ties on recency (more recent wins)', async () => {
    // Backend pre-sorts by date desc, so insertion-order index 0 is most recent.
    apiGet.mockResolvedValue({
      transactions: [
        tx({ id: 10, description: 'newer' }), // most recent
        tx({ id: 11, description: 'older' }), // less recent
      ],
    });
    const { result } = renderHook(() => useDescriptionHistory());
    await waitFor(() => expect(result.current.length).toBe(2));
    expect(result.current).toEqual(['newer', 'older']);
  });

  test('case-insensitive dedupe — collapses to one entry preserving first-seen casing', async () => {
    apiGet.mockResolvedValue({
      transactions: [
        tx({ id: 1, description: 'Coffee' }),
        tx({ id: 2, description: 'coffee' }),
        tx({ id: 3, description: 'COFFEE' }),
      ],
    });
    const { result } = renderHook(() => useDescriptionHistory());
    await waitFor(() => expect(result.current.length).toBe(1));
    expect(result.current).toEqual(['Coffee']);
  });

  test('silent on fetch error — returns [] and never throws', async () => {
    apiGet.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useDescriptionHistory());
    // Give the rejected promise a tick to settle.
    await new Promise((r) => setTimeout(r, 10));
    expect(result.current).toEqual([]);
  });

  test('ignores blank / whitespace-only descriptions', async () => {
    apiGet.mockResolvedValue({
      transactions: [
        tx({ id: 1, description: 'lunch' }),
        tx({ id: 2, description: '   ' }),
        tx({ id: 3, description: '' }),
      ],
    });
    const { result } = renderHook(() => useDescriptionHistory());
    await waitFor(() => expect(result.current.length).toBe(1));
    expect(result.current).toEqual(['lunch']);
  });

  test('fetches with per_page=200 sorted by date desc', async () => {
    apiGet.mockResolvedValue({ transactions: [] });
    renderHook(() => useDescriptionHistory());
    await waitFor(() => expect(apiGet).toHaveBeenCalled());
    const path = apiGet.mock.calls[0][0] as string;
    expect(path).toContain('transactions?');
    expect(path).toContain('per_page=200');
    expect(path).toContain('sort_by=date');
    expect(path).toContain('sort_dir=desc');
  });
});
