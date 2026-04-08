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
    hoverBg: getCSSVar('--primary-a8', 'rgba(83,71,206,0.08)'),
    incomeColor: getCSSVar('--color-primary', '#5347CE'),
    expenseColor: getCSSVar('--cat-3', '#B794D8'),
    categoryColors: [
      getCSSVar('--cat-2', '#5347CE'),
      getCSSVar('--cat-3', '#B794D8'),
      getCSSVar('--cat-5', '#4896FE'),
      getCSSVar('--cat-7', '#16C8C7'),
      getCSSVar('--cat-8', '#3EBD80'),
      getCSSVar('--cat-11', '#F0C84D'),
      getCSSVar('--cat-4', '#7B8AFE'),
      getCSSVar('--cat-6', '#2DB3D9'),
      getCSSVar('--cat-9', '#7EB854'),
      getCSSVar('--cat-10', '#C4B83A'),
      getCSSVar('--cat-1', '#4030A6'),
      getCSSVar('--cat-muted', '#B8BCC8'),
    ],
  }), [resolvedTheme]);
}
