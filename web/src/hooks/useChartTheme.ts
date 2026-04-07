import { useMemo } from 'react';
import { useTheme } from './useTheme';

interface ChartTheme {
  axisStroke: string;
  gridStroke: string;
  tooltipBg: string;
  tooltipBorder: string;
  tooltipText: string;
  hoverBg: string;
  incomeColor: string;
  expenseColor: string;
  categoryColors: string[];
}

function getCSSVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback;
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || fallback;
}

export function useChartTheme(): ChartTheme {
  const { resolvedTheme } = useTheme();

  return useMemo(() => ({
    axisStroke: getCSSVar('--text-tertiary', '#5A5A54'),
    gridStroke: getCSSVar('--border-muted', '#22221F'),
    tooltipBg: getCSSVar('--surface-overlay', '#2D2D29'),
    tooltipBorder: getCSSVar('--border-default', '#2D2D29'),
    tooltipText: getCSSVar('--text-primary', '#F2F2F0'),
    hoverBg: getCSSVar('--primary-a8', 'rgba(122,143,108,0.08)'),
    incomeColor: getCSSVar('--color-income', '#8EBF9A'),
    expenseColor: getCSSVar('--color-expense', '#C9897A'),
    categoryColors: [
      getCSSVar('--color-primary', '#7A8F6C'),
      getCSSVar('--color-income', '#8EBF9A'),
      getCSSVar('--color-expense', '#C9897A'),
      getCSSVar('--color-warning', '#C4A87A'),
      getCSSVar('--color-info', '#7BA8B8'),
      getCSSVar('--text-tertiary', '#5A5A54'),
    ],
  }), [resolvedTheme]);
}
