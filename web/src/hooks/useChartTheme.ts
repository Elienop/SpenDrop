import { useMemo } from 'react';

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
  return useMemo(() => ({
    axisStroke: getCSSVar('--text-tertiary', '#58585F'),
    gridStroke: getCSSVar('--border-muted', '#1E1E23'),
    tooltipBg: getCSSVar('--surface-overlay', '#1E1E23'),
    tooltipBorder: getCSSVar('--border-default', '#2A2A30'),
    tooltipText: getCSSVar('--text-primary', '#F5F5F6'),
    hoverBg: getCSSVar('--primary-a8', 'rgba(129,140,248,0.08)'),
    incomeColor: getCSSVar('--color-income', '#7EC89B'),
    expenseColor: getCSSVar('--color-expense', '#E88B9C'),
    categoryColors: [
      getCSSVar('--color-primary', '#818CF8'),
      getCSSVar('--color-income', '#7EC89B'),
      getCSSVar('--color-expense', '#E88B9C'),
      getCSSVar('--color-warning', '#E8A87C'),
      getCSSVar('--color-info', '#7CAFD4'),
      getCSSVar('--text-tertiary', '#58585F'),
    ],
  }), []);
}
