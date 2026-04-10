import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { KpiCard } from './KpiCard';

describe('KpiCard', () => {
  test('renders label, dollars, and cents', () => {
    render(
      <KpiCard
        label="Total Balance"
        dollars="$2,847"
        cents=".32"
      />,
    );
    expect(screen.getByText('Total Balance')).toBeInTheDocument();
    expect(screen.getByText('$2,847')).toBeInTheDocument();
    expect(screen.getByText('.32')).toBeInTheDocument();
  });

  test('renders delta badge when positive', () => {
    render(
      <KpiCard
        label="Income"
        dollars="$5,200"
        cents=".00"
        delta={{ percent: 3.2, direction: 'up' }}
      />,
    );
    expect(screen.getByText(/3\.2%/)).toBeInTheDocument();
  });

  test('renders delta badge when negative', () => {
    render(
      <KpiCard
        label="Expenses"
        dollars="$1,200"
        cents=".00"
        delta={{ percent: 8.1, direction: 'down' }}
      />,
    );
    expect(screen.getByText(/8\.1%/)).toBeInTheDocument();
  });

  test('omits the delta badge when delta is null', () => {
    render(
      <KpiCard label="Savings Rate" dollars="45" cents="%" delta={null} />,
    );
    // No Badge element should render — the badge would contain a percentage like "+3.2%"
    expect(screen.queryByText(/\+.*%|−.*%|-.*%/)).not.toBeInTheDocument();
  });

  test('renders footnote when provided', () => {
    render(
      <KpiCard
        label="Total Balance"
        dollars="$100"
        cents=".00"
        footnote="vs last month"
      />,
    );
    expect(screen.getByText('vs last month')).toBeInTheDocument();
  });
});
