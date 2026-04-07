import { describe, test, expect, vi } from 'vitest';
import { renderHook } from '@testing-library/react';

vi.mock('./useTheme', () => ({
  useTheme: () => ({ theme: 'dark', resolvedTheme: 'dark', setTheme: vi.fn() }),
}));

import { useChartTheme } from './useChartTheme';

describe('useChartTheme', () => {
  test('returns all required theme properties', () => {
    const { result } = renderHook(() => useChartTheme());
    const theme = result.current;

    expect(theme).toHaveProperty('axisStroke');
    expect(theme).toHaveProperty('gridStroke');
    expect(theme).toHaveProperty('tooltipBg');
    expect(theme).toHaveProperty('tooltipBorder');
    expect(theme).toHaveProperty('tooltipText');
    expect(theme).toHaveProperty('hoverBg');
    expect(theme).toHaveProperty('incomeColor');
    expect(theme).toHaveProperty('expenseColor');
    expect(theme).toHaveProperty('categoryColors');
    expect(theme.categoryColors).toHaveLength(6);
  });

  test('returns string values for all color properties', () => {
    const { result } = renderHook(() => useChartTheme());
    const theme = result.current;

    expect(typeof theme.axisStroke).toBe('string');
    expect(typeof theme.incomeColor).toBe('string');
    expect(typeof theme.expenseColor).toBe('string');
  });
});
