import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ChartCard } from './ChartCard';

describe('ChartCard', () => {
  test('renders subtitle when provided', () => {
    render(
      <ChartCard title="Cash Flow" subtitle="Income vs expenses">
        <div />
      </ChartCard>,
    );
    expect(screen.getByText('Income vs expenses')).toBeInTheDocument();
  });

  test('renders action slot when provided', () => {
    render(
      <ChartCard
        title="Cash Flow"
        action={<button type="button">6M</button>}
      >
        <div />
      </ChartCard>,
    );
    expect(screen.getByRole('button', { name: '6M' })).toBeInTheDocument();
  });

  test('renders skeleton (not children) when loading', () => {
    render(
      <ChartCard title="Cash Flow" loading>
        <div data-testid="chart-body">chart here</div>
      </ChartCard>,
    );
    expect(screen.queryByTestId('chart-body')).not.toBeInTheDocument();
    expect(screen.getByTestId('chart-loading')).toBeInTheDocument();
  });
});
