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
    axisStroke: getCSSVar('--text-tertiary', '#6E6E79'),
    gridStroke: getCSSVar('--border-muted', '#2A2A2D'),
    tooltipBg: getCSSVar('--surface-overlay', '#222225'),
    tooltipBorder: getCSSVar('--border-default', '#3A3A40'),
    tooltipText: getCSSVar('--text-primary', '#EEEEF0'),
    hoverBg: getCSSVar('--primary-a8', 'rgba(239,243,162,0.08)'),
    incomeColor: getCSSVar('--color-income', '#B5DAA9'),
    expenseColor: getCSSVar('--color-expense', '#D4918A'),
    categoryColors: [
      getCSSVar('--color-primary', '#EFF3A2'),
      getCSSVar('--color-income', '#B5DAA9'),
      getCSSVar('--color-expense', '#D4918A'),
      getCSSVar('--color-warning', '#D4C08A'),
      getCSSVar('--color-info', '#8AB4C8'),
      getCSSVar('--text-tertiary', '#6E6E79'),
    ],
  }), [resolvedTheme]);
}
