import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';
import type { SavedFilter } from '../api/types';

interface UseSavedFiltersResult {
  savedFilters: SavedFilter[];
  loading: boolean;
  saveFilter: (name: string, filterJSON: string) => Promise<void>;
  deleteFilter: (id: number) => Promise<void>;
  refetch: () => void;
}

/**
 * Per-user saved transaction filters. Backed by TanStack Query under the
 * `['filters']` key. Saved filters are per-user UI state (not household ledger
 * data), so there is no SSE resource for them — the only refresh path is the
 * local `invalidateQueries(['filters'])` fired by the save/delete mutations,
 * exactly mirroring the previous post/del-then-refetch behavior. Non-critical:
 * a failed read leaves the list empty without surfacing an error.
 */
export function useSavedFilters(): UseSavedFiltersResult {
  const queryClient = useQueryClient();

  const query = useQuery({
    queryKey: ['filters'],
    queryFn: () => api.get<SavedFilter[]>('filters'),
  });

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['filters'] });

  const saveMutation = useMutation({
    mutationFn: ({ name, filterJSON }: { name: string; filterJSON: string }) =>
      api.post('filters', { name, filter_json: filterJSON }),
    onSuccess: invalidate,
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.del(`filters/${id}`),
    onSuccess: invalidate,
  });

  return {
    savedFilters: query.data ?? [],
    loading: query.isLoading,
    saveFilter: async (name: string, filterJSON: string) => {
      await saveMutation.mutateAsync({ name, filterJSON });
    },
    deleteFilter: async (id: number) => {
      await deleteMutation.mutateAsync(id);
    },
    refetch: () => {
      void query.refetch();
    },
  };
}
