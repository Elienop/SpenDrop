import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import type { Category } from '@/api/types';

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
