import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import type { Transaction, PaginatedResponse } from '../api/types';

export interface TransactionFilters {
  dateFrom: string;
  dateTo: string;
  categoryId: string;
  categoryIds: string;
  amountMin: string;
  amountMax: string;
  tags: string;
  type: string;
  search: string;
}

interface CreateTransactionInput {
  date: string;
  amount: number;
  description: string;
  category_id: number;
  original_amount?: number;
  original_currency?: string;
  tags?: string;
  notes?: string;
}

interface UpdateTransactionInput extends CreateTransactionInput {
  id: number;
}

interface UseTransactionsResult {
  transactions: Transaction[];
  total: number;
  page: number;
  perPage: number;
  filters: TransactionFilters;
  setFilter: (key: keyof TransactionFilters, value: string) => void;
  clearFilters: () => void;
  clearPanelFilters: () => void;
  setPage: (page: number) => void;
  loading: boolean;
  error: string;
  refetch: () => void;
  createTransaction: (input: CreateTransactionInput) => Promise<Transaction>;
  updateTransaction: (input: UpdateTransactionInput) => Promise<void>;
  deleteTransaction: (id: number) => Promise<void>;
}

const defaultFilters: TransactionFilters = {
  dateFrom: '',
  dateTo: '',
  categoryId: '',
  categoryIds: '',
  amountMin: '',
  amountMax: '',
  tags: '',
  type: '',
  search: '',
};

export function useTransactions(): UseTransactionsResult {
  const [transactions, setTransactions] = useState<Transaction[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [perPage] = useState(20);
  const [filters, setFilters] = useState<TransactionFilters>(defaultFilters);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const buildQuery = useCallback(() => {
    const params = new URLSearchParams();
    params.set('page', String(page));
    params.set('per_page', String(perPage));
    if (filters.dateFrom) params.set('date_from', filters.dateFrom);
    if (filters.dateTo) params.set('date_to', filters.dateTo);
    if (filters.categoryIds) {
      params.set('category_ids', filters.categoryIds);
    } else if (filters.categoryId) {
      params.set('category_id', filters.categoryId);
    }
    if (filters.amountMin) params.set('amount_min', filters.amountMin);
    if (filters.amountMax) params.set('amount_max', filters.amountMax);
    if (filters.tags) params.set('tags', filters.tags);
    if (filters.type) params.set('type', filters.type);
    if (filters.search) params.set('search', filters.search);
    return params.toString();
  }, [page, perPage, filters]);

  const fetchTransactions = useCallback(() => {
    setLoading(true);
    setError('');
    api
      .get<PaginatedResponse<Transaction>>(`transactions?${buildQuery()}`)
      .then((data) => {
        setTransactions(data.transactions);
        setTotal(data.total);
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : 'Failed to load transactions');
      })
      .finally(() => {
        setLoading(false);
      });
  }, [buildQuery]);

  useEffect(() => {
    fetchTransactions();
  }, [fetchTransactions]);

  const setFilter = useCallback(
    (key: keyof TransactionFilters, value: string) => {
      setFilters((prev) => ({ ...prev, [key]: value }));
      setPage(1);
    },
    [],
  );

  const clearFilters = useCallback(() => {
    setFilters(defaultFilters);
    setPage(1);
  }, []);

  const clearPanelFilters = useCallback(() => {
    setFilters((prev) => ({
      ...prev,
      dateFrom: '',
      dateTo: '',
      categoryId: '',
      categoryIds: '',
      amountMin: '',
      amountMax: '',
      tags: '',
    }));
    setPage(1);
  }, []);

  const createTransaction = useCallback(
    async (input: CreateTransactionInput): Promise<Transaction> => {
      const created = await api.post<Transaction>('transactions', input);
      fetchTransactions();
      return created;
    },
    [fetchTransactions],
  );

  const updateTransaction = useCallback(
    async (input: UpdateTransactionInput) => {
      const { id, ...body } = input;
      await api.put(`transactions/${id}`, body);
      fetchTransactions();
    },
    [fetchTransactions],
  );

  const deleteTransaction = useCallback(
    async (id: number) => {
      await api.del(`transactions/${id}`);
      fetchTransactions();
    },
    [fetchTransactions],
  );

  return {
    transactions,
    total,
    page,
    perPage,
    filters,
    setFilter,
    clearFilters,
    clearPanelFilters,
    setPage,
    loading,
    error,
    refetch: fetchTransactions,
    createTransaction,
    updateTransaction,
    deleteTransaction,
  };
}
