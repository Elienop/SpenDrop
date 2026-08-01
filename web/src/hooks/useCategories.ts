import { useQuery } from '@tanstack/react-query';
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
 * by default). Backed by TanStack Query under the `['categories']` key so the
 * live-update subscriber refetches it after any category CRUD anywhere in the
 * household via `invalidateQueries({ queryKey: ['categories'] })`. Used by the
 * mobile quick-add screen to render category chips and to feed the freeform
 * parser's name matching.
 */
export function useCategories(): UseCategoriesResult {
  const query = useQuery({
    queryKey: ['categories'],
    queryFn: () => api.get<Category[]>('categories'),
    // 'offlineFirst', not the default 'online': the service worker serves
    // GET /api/categories from `spendrop-api-lists` when the network is gone,
    // and that cache is the only reason the offline capture screen has
    // categories to tap. The default would pause the query without ever
    // issuing the request, so the cache would never be consulted and /quick
    // would show "no categories yet" with no way to add anything. A genuine
    // miss (nothing cached) still surfaces as an error with Retry, which is
    // the honest answer — not an empty list.
    networkMode: 'offlineFirst',
  });

  return {
    categories: query.data ?? [],
    loading: query.isLoading,
    error: query.error instanceof Error ? query.error.message : '',
    refetch: () => {
      void query.refetch();
    },
  };
}
