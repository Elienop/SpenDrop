import type { ChartConfig } from '@/components/ui/chart';

export const MONTH_NAMES = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

export const MONTH_FULL_NAMES = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

export function formatCurrency(amount: number): string {
  return amount.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
  });
}

export function yearOptions(startYear = 2024): { value: string; label: string }[] {
  const currentYear = new Date().getFullYear();
  const opts: { value: string; label: string }[] = [];
  for (let y = currentYear; y >= startYear; y--) {
    opts.push({ value: String(y), label: String(y) });
  }
  return opts;
}

export const INCEXP_CONFIG = {
  income: { label: 'Income', color: 'hsl(var(--primary))' },
  expenses: { label: 'Expenses', color: 'hsl(var(--primary) / 0.35)' },
} satisfies ChartConfig;
