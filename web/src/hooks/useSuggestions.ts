import { useState, useEffect } from 'react';
import { api } from '../api/client';
import type { TransactionSuggestions } from '../api/types';

export function useSuggestions(refreshKey = 0) {
  const [data, setData] = useState<TransactionSuggestions>({
    descriptions: [],
    tags: [],
  });

  useEffect(() => {
    let ignore = false;
    api
      .get<TransactionSuggestions>('transactions/suggestions')
      .then((res) => {
        if (!ignore) setData(res);
      })
      .catch(() => {
        // Suggestions are non-critical — fail silently
      });
    return () => {
      ignore = true;
    };
  }, [refreshKey]);

  return data;
}
