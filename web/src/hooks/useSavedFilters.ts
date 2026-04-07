import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import type { SavedFilter } from '../api/types';

interface UseSavedFiltersResult {
  savedFilters: SavedFilter[];
  loading: boolean;
  saveFilter: (name: string, filterJSON: string) => Promise<void>;
  deleteFilter: (id: number) => Promise<void>;
  refetch: () => void;
}

export function useSavedFilters(): UseSavedFiltersResult {
  const [savedFilters, setSavedFilters] = useState<SavedFilter[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchFilters = useCallback(() => {
    setLoading(true);
    api
      .get<SavedFilter[]>('filters')
      .then(setSavedFilters)
      .catch(() => {
        /* non-critical — filters remain empty */
      })
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchFilters();
  }, [fetchFilters]);

  const saveFilter = useCallback(
    async (name: string, filterJSON: string) => {
      await api.post('filters', { name, filter_json: filterJSON });
      fetchFilters();
    },
    [fetchFilters],
  );

  const deleteFilter = useCallback(
    async (id: number) => {
      await api.del(`filters/${id}`);
      fetchFilters();
    },
    [fetchFilters],
  );

  return { savedFilters, loading, saveFilter, deleteFilter, refetch: fetchFilters };
}
