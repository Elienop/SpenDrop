import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ChartTooltip } from './ChartTooltip';

describe('ChartTooltip', () => {
  test('renders nothing when not active', () => {
    const { container } = render(<ChartTooltip active={false} payload={[]} label="" />);
    expect(container.firstChild).toBeNull();
  });

  test('renders label and payload when active', () => {
    const payload = [
      { name: 'Income', value: 3200, color: '#7EC89B' },
      { name: 'Expenses', value: -1248, color: '#E88B9C' },
    ];
    render(<ChartTooltip active={true} payload={payload} label="Apr" />);
    expect(screen.getByText('Apr')).toBeInTheDocument();
    expect(screen.getByText('$3,200')).toBeInTheDocument();
    expect(screen.getByText('$1,248')).toBeInTheDocument();
  });
});
