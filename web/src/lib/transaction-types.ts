// Transaction / category type enum values, matching the backend's
// `CategoryTypeExpense` / `CategoryTypeIncome` constants in
// `internal/api/constants.go`. Use these anywhere the UI compares
// `category.type` or `transaction.category_type` against a literal.

export const TYPE_EXPENSE = 'expense';
export const TYPE_INCOME = 'income';

export type TransactionType = typeof TYPE_EXPENSE | typeof TYPE_INCOME;

export function isExpense(type: TransactionType): boolean {
  return type === TYPE_EXPENSE;
}

export function isIncome(type: TransactionType): boolean {
  return type === TYPE_INCOME;
}
