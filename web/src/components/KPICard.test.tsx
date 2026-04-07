import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { KPICard } from './KPICard';

describe('KPICard', () => {
  test('renders label and formatted value', () => {
    render(<KPICard label="Budget" value="$3,000.00" />);

    expect(screen.getByText('Budget')).toBeInTheDocument();
    expect(screen.getByText('$3,000.00')).toBeInTheDocument();
  });

  test('applies accent color to value when provided', () => {
    render(
      <KPICard label="Remaining" value="$1,800.00" color="var(--color-primary)" />,
    );

    const valueEl = screen.getByText('$1,800.00');
    expect(valueEl.style.color).toBe('var(--color-primary)');
  });

  test('renders without color prop (default text color)', () => {
    render(<KPICard label="Spent" value="$1,200.00" />);

    const valueEl = screen.getByText('$1,200.00');
    // No inline color style when prop not provided
    expect(valueEl.style.color).toBe('');
  });
});
