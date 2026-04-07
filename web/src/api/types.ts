export interface User {
  id: number;
  username: string;
  display_name: string;
  role: 'admin' | 'member';
  created_at: string;
}

export interface Transaction {
  id: number;
  user_id: number;
  date: string;
  amount: number;
  original_amount: number | null;
  original_currency: string | null;
  description: string;
  category_id: number;
  category_name: string;
  category_type: 'expense' | 'income';
  category_color: string;
  tags: string | null;
  notes: string | null;
  created_at: string;
  updated_at: string;
}

export interface Category {
  id: number;
  name: string;
  type: 'expense' | 'income';
  color: string;
  icon: string | null;
  sort_order: number;
  is_active: boolean;
  created_at: string;
}

export interface Currency {
  code: string;
  name: string;
  symbol: string;
  rate_to_base: number;
  is_base: boolean;
  updated_at: string;
}

export interface Budget {
  id: number;
  year: number;
  month: number;
  amount: number;
  updated_at: string;
}

export interface SavingsGoal {
  id: number;
  year: number;
  target_amount: number;
  updated_at: string;
}

export interface DashboardSummary {
  year: number;
  month: number;
  budget: number;
  total_spent: number;
  total_income: number;
  remaining: number;
  savings_this_month: number;
  savings_goal: number;
  savings_ytd: number;
  savings_goal_progress: number;
}

export interface DashboardTrendItem {
  year: number;
  month: number;
  total_spent: number;
  total_income: number;
}

export interface CategoryBreakdownItem {
  id: number;
  name: string;
  color: string;
  total: number;
}

export interface SavedFilter {
  id: number;
  user_id: number;
  name: string;
  filter_json: string;
  created_at: string;
  updated_at: string;
}

export interface PaginatedResponse<T> {
  transactions: T[];
  total: number;
  page: number;
  per_page: number;
}

export interface ImportPreview {
  import_id: string;
  row_count: number;
  preview: ImportRow[];
  columns: string[];
  unique_categories: string[];
}

export interface ImportRow {
  date: string;
  description: string;
  amount: number;
  original_amount?: number;
  original_currency?: string;
  category: string;
  tags?: string;
  notes?: string;
}

export interface ImportResult {
  imported: number;
  skipped: number;
  total: number;
}

// Reports
export interface YoYMonthEntry {
  month: number;
  expenses: number;
  income: number;
}

export interface YoYResponse {
  current_year: number;
  previous_year: number;
  current: YoYMonthEntry[];
  previous: YoYMonthEntry[];
}

export interface CategoryTrendEntry {
  id: number;
  name: string;
  color: string;
  type: 'expense' | 'income';
  data: { year: number; month: number; total: number }[];
}

export interface IncomeExpenseEntry {
  year: number;
  month: number;
  income: number;
  expenses: number;
  net: number;
}

export interface TopMerchantEntry {
  description: string;
  tx_count: number;
  total: number;
}
