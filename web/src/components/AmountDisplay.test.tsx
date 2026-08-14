import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { AmountDisplay } from './AmountDisplay';

/**
 * REWRITTEN, not patched, for signed amounts.
 *
 * The sign assertions here used to read `textContent` must match `/^-/` for an
 * expense of +25.50 and `/^\+/` for an income — which pinned the DESIGN
 * (compose the sign from the type) rather than the behaviour, and that design
 * renders "--$12.34" for the first refund it meets. What is pinned now is the
 * rule that replaced it: the sign shown is the sign of `value × type`,
 * produced once, and the row says so in words when the two disagree.
 */
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

  // The four quadrants of (type × sign). Anchored to the WHOLE text of the
  // block, so a second sign character, a stray magnitude or a dropped minus
  // all fail — `toHaveTextContent('-$25.50')` matches "--$25.50" happily.
  it('a positive expense renders one minus, in the ordinary colour', () => {
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
    expect(root).toHaveTextContent(/^-\$25\.50$/);
    expect(root).toHaveClass('text-foreground');
    expect(root).not.toHaveClass('text-emerald-500');
    expect(screen.queryByTestId('amount-sign-note')).not.toBeInTheDocument();
  });

  it('positive income renders one plus, in the inflow colour', () => {
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
    expect(root).toHaveTextContent(/^\+\$1,000\.00$/);
    expect(root).toHaveClass('text-emerald-500');
    expect(screen.queryByTestId('amount-sign-note')).not.toBeInTheDocument();
  });

  it('a REFUND — a negative expense — renders as an inflow and says it is a refund', () => {
    // The quadrant the old suite had no case for at all. Under the previous
    // design this rendered "--$20.00" in expense styling.
    render(
      <AmountDisplay
        amount={-20}
        originalAmount={null}
        originalCurrency={null}
        type="expense"
        baseCode="USD"
      />,
    );
    const root = screen.getByTestId('amount-display');
    // The whole block, note included: the figure is "+$20.00" and the word
    // sits with it.
    expect(root).toHaveTextContent(/^Refund\+\$20\.00$/);
    expect(root).toHaveClass('text-emerald-500');
    const note = screen.getByTestId('amount-sign-note');
    expect(note).toHaveTextContent('Refund');
    // THE METADATA REGISTER, and it is one register with one geometry: the
    // creator line and the version line already use `gap-1.5` + `size-3.5`
    // (TransactionCard, RecentlyAdded, AppVersion, each with its own pin).
    // `gap-1` + `size-3` here was a third size tier for the same kind of line,
    // sitting inches from the second one on the same card.
    expect(note.className.split(/\s+/)).toContain('gap-1.5');
    const icon = note.querySelector('svg');
    expect(icon).not.toBeNull();
    expect(icon!.getAttribute('class')?.split(/\s+/)).toEqual(
      expect.arrayContaining(['size-3.5', 'shrink-0']),
    );
  });

  it('an income REVERSAL renders as an outflow and says it is a reversal', () => {
    render(
      <AmountDisplay
        amount={-100}
        originalAmount={null}
        originalCurrency={null}
        type="income"
        baseCode="USD"
      />,
    );
    const root = screen.getByTestId('amount-display');
    expect(root).toHaveTextContent(/^Reversal-\$100\.00$/);
    expect(root).toHaveClass('text-foreground');
    expect(root).not.toHaveClass('text-emerald-500');
  });

  it('both lines of one row point the same way', () => {
    // The half a "sign the primary line" fix loses: a refund reading "+$25.50"
    // over "-2,250,000.00 LBP" is two contradictory claims about one row.
    render(
      <AmountDisplay
        amount={-25.5}
        originalAmount={-2250000}
        originalCurrency="LBP"
        type="expense"
        baseCode="USD"
      />,
    );
    expect(screen.getByTestId('amount-display')).toHaveTextContent(
      /^Refund\+\$25\.50\+2,250,000\.00 LBP$/,
    );
  });

  it('a normal foreign-currency expense signs both lines negative', () => {
    render(
      <AmountDisplay
        amount={25.5}
        originalAmount={2250000}
        originalCurrency="LBP"
        type="expense"
        baseCode="USD"
      />,
    );
    expect(screen.getByTestId('amount-display')).toHaveTextContent(
      /^-\$25\.50-2,250,000\.00 LBP$/,
    );
  });
});
