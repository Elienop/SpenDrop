import { useState, useEffect } from 'react';
import { api } from '../api/client';
import type { TransactionSuggestions } from '../api/types';

export function useSuggestions(refreshKey = 0) {
  const [data, setData] = useState<TransactionSuggestions>({
    descriptions: [],
    tags: [],
  });

  useEffect(() => {
    api
      .get<TransactionSuggestions>('transactions/suggestions')
      .then(setData)
      .catch(() => {
        // Suggestions are non-critical — fail silently
      });
  }, [refreshKey]);

  return data;
}
