import { useCallback, useEffect, useState } from 'react';
import { api } from '@/api/client';
import type { Category } from '@/api/types';

export interface UseCategoriesResult {
  categories: Category[];
  loading: boolean;
  error: string;
  refetch: () => void;
}

/**
 * Fetches the household's categories (active only — the API omits inactive
 * by default). Used by the mobile quick-add screen to render category chips
 * and to feed the freeform parser's name matching. Kept as its own hook so
 * the quick-add surface doesn't depend on the heavier `useTransactions`
 * list state.
 */
export function useCategories(): UseCategoriesResult {
  const [categories, setCategories] = useState<Category[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const fetchCategories = useCallback(() => {
    setLoading(true);
    setError('');
    api
      .get<Category[]>('categories')
      .then((list) => setCategories(list))
      .catch((err) =>
        setError(err instanceof Error ? err.message : 'Failed to load categories'),
      )
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchCategories();
  }, [fetchCategories]);

  return { categories, loading, error, refetch: fetchCategories };
}
