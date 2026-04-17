import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { AmountDisplay } from './AmountDisplay';

describe('AmountDisplay', () => {
  it('renders a single formatted line when originalAmount is null', () => {
    render(
      <AmountDisplay
        amount={25.5}
        originalAmount={null}
        originalCurrency={null}
        type="expense"
        baseCode="USD"
      />,
    );
    expect(screen.getByText(/\$25\.50/)).toBeInTheDocument();
    expect(screen.queryByText(/LBP|EUR/)).not.toBeInTheDocument();
  });

  it('renders a single line when originalCurrency equals baseCode (defensive fallback)', () => {
    render(
      <AmountDisplay
        amount={25.5}
        originalAmount={25.5}
        originalCurrency="USD"
        type="expense"
        baseCode="USD"
      />,
    );
    expect(screen.getByText(/\$25\.50/)).toBeInTheDocument();
    expect(screen.queryByTestId('amount-display-secondary')).not.toBeInTheDocument();
  });

  it('renders two lines when originalCurrency differs from baseCode', () => {
    render(
      <AmountDisplay
        amount={1.67}
        originalAmount={150000}
        originalCurrency="LBP"
        type="expense"
        baseCode="USD"
      />,
    );
    expect(screen.getByText(/\$1\.67/)).toBeInTheDocument();
    const secondary = screen.getByTestId('amount-display-secondary');
    expect(secondary).toHaveTextContent('150,000');
    expect(secondary).toHaveTextContent('LBP');
  });

  it('applies expense styling (negative sign + default foreground)', () => {
    render(
      <AmountDisplay
        amount={25.5}
        originalAmount={null}
        originalCurrency={null}
        type="expense"
        baseCode="USD"
      />,
    );
    const root = screen.getByTestId('amount-display');
    expect(root.textContent).toMatch(/^-/);
  });

  it('applies income styling (positive sign + green class)', () => {
    render(
      <AmountDisplay
        amount={1000}
        originalAmount={null}
        originalCurrency={null}
        type="income"
        baseCode="USD"
      />,
    );
    const root = screen.getByTestId('amount-display');
    expect(root.textContent).toMatch(/^\+/);
    expect(root).toHaveClass('text-emerald-500');
  });
});
