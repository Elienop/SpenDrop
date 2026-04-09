import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Wallet } from 'lucide-react';
import { KpiCard } from './KpiCard';

describe('KpiCard', () => {
  test('renders label, dollars, and cents', () => {
    render(
      <KpiCard
        label="Total Balance"
        icon={Wallet}
        dollars="$2,847"
        cents=".32"
      />,
    );
    expect(screen.getByText('Total Balance')).toBeInTheDocument();
    expect(screen.getByText('$2,847')).toBeInTheDocument();
    expect(screen.getByText('.32')).toBeInTheDocument();
  });

  test('renders delta with up arrow when positive', () => {
    render(
      <KpiCard
        label="Income"
        dollars="$5,200"
        cents=".00"
        delta={{ percent: 3.2, direction: 'up' }}
      />,
    );
    expect(screen.getByText(/3\.2%/)).toBeInTheDocument();
    expect(screen.getByText(/vs last month/i)).toBeInTheDocument();
  });

  test('renders delta with down arrow when negative', () => {
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

  test('omits the delta row when delta is null', () => {
    render(
      <KpiCard label="Savings Rate" dollars="45" cents="%" delta={null} />,
    );
    expect(screen.queryByText(/vs last month/i)).not.toBeInTheDocument();
  });

  test('applies featured styling when featured is true', () => {
    const { container } = render(
      <KpiCard label="Total Balance" dollars="$100" cents=".00" featured />,
    );
    const card = container.querySelector('[data-featured="true"]');
    expect(card).not.toBeNull();
  });
});
