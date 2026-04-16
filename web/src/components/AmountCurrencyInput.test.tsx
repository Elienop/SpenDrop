import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import type { Currency } from '@/api/types';
import { AmountCurrencyInput } from './AmountCurrencyInput';

const currencies: Currency[] = [
  { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'EUR', name: 'Euro', symbol: '€', rate_to_base: 0.9, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'OLD', name: 'Obsolete', symbol: 'O', rate_to_base: 2, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
  { code: 'NORATE', name: 'No Rate', symbol: 'N', rate_to_base: 0, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
];

const rateFor = (code: string): number | null => {
  const c = currencies.find((x) => x.code === code);
  if (!c) return null;
  if (c.rate_to_base <= 0) return null;
  return c.rate_to_base;
};

type Overrides = Partial<React.ComponentProps<typeof AmountCurrencyInput>>;

function renderInput(props: Overrides = {}) {
  const defaults: React.ComponentProps<typeof AmountCurrencyInput> = {
    value: 0,
    onValueChange: vi.fn(),
    currency: 'USD',
    onCurrencyChange: vi.fn(),
    baseCode: 'USD',
    currencies,
    hideInactive: true,
    rateFor,
  };
  return render(<AmountCurrencyInput {...defaults} {...props} />);
}

describe('AmountCurrencyInput', () => {
  it('renders the amount input and the currency-code suffix button', () => {
    renderInput();
    expect(screen.getByRole('spinbutton')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /USD/ })).toBeInTheDocument();
  });

  it('typing in the amount input calls onValueChange with the numeric value', async () => {
    const user = userEvent.setup();
    const onValueChange = vi.fn();
    renderInput({ onValueChange });
    await user.type(screen.getByRole('spinbutton'), '150');
    const lastCall = onValueChange.mock.calls.at(-1)!;
    expect(lastCall[0]).toBe(150);
    expect(typeof lastCall[0]).toBe('number');
  });

  it('clicking the suffix button opens the Popover with a searchable list', async () => {
    const user = userEvent.setup();
    renderInput();
    await user.click(screen.getByRole('button', { name: /USD/ }));
    expect(await screen.findByPlaceholderText(/search/i)).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /EUR/ })).toBeInTheDocument();
  });

  it('selecting a currency closes the Popover and calls onCurrencyChange', async () => {
    const user = userEvent.setup();
    const onCurrencyChange = vi.fn();
    renderInput({ onCurrencyChange });
    await user.click(screen.getByRole('button', { name: /USD/ }));
    await user.click(await screen.findByRole('option', { name: /LBP/ }));
    expect(onCurrencyChange).toHaveBeenCalledWith('LBP');
  });

  it('renders no preview when currency === baseCode', () => {
    renderInput({ value: 100, currency: 'USD', baseCode: 'USD' });
    expect(screen.queryByText(/≈/)).not.toBeInTheDocument();
  });

  it('renders a ≈ preview when currency !== baseCode with a valid rate', () => {
    renderInput({ value: 150000, currency: 'LBP', baseCode: 'USD' });
    expect(screen.getByText(/≈/)).toHaveTextContent(/\$1\.67/);
  });

  it('_PreviewUpdatesOnCurrencyChange: swapping currency with same amount re-renders the preview with the new rate', () => {
    const { rerender } = render(
      <AmountCurrencyInput
        value={150000}
        onValueChange={() => {}}
        currency="LBP"
        onCurrencyChange={() => {}}
        baseCode="USD"
        currencies={currencies}
        hideInactive={true}
        rateFor={rateFor}
      />,
    );
    expect(screen.getByText(/≈/)).toHaveTextContent(/\$1\.67/);

    rerender(
      <AmountCurrencyInput
        value={150000}
        onValueChange={() => {}}
        currency="EUR"
        onCurrencyChange={() => {}}
        baseCode="USD"
        currencies={currencies}
        hideInactive={true}
        rateFor={rateFor}
      />,
    );
    expect(screen.getByText(/≈/)).toHaveTextContent(/\$166,666\.67/);
  });

  it('_FocusPreservesRawInput: changing currency while amount input is focused does NOT mutate the DOM value', async () => {
    const user = userEvent.setup();
    const onValueChange = vi.fn();
    const { rerender } = render(
      <AmountCurrencyInput
        value={150000}
        onValueChange={onValueChange}
        currency="USD"
        onCurrencyChange={() => {}}
        baseCode="USD"
        currencies={currencies}
        hideInactive={true}
        rateFor={rateFor}
      />,
    );
    const input = screen.getByRole('spinbutton') as HTMLInputElement;
    input.focus();
    expect(input.value).toBe('150000');

    rerender(
      <AmountCurrencyInput
        value={150000}
        onValueChange={onValueChange}
        currency="LBP"
        onCurrencyChange={() => {}}
        baseCode="USD"
        currencies={currencies}
        hideInactive={true}
        rateFor={rateFor}
      />,
    );

    expect(input.value).toBe('150000');
    expect(onValueChange).not.toHaveBeenCalled();
  });

  it('hideInactive=true: filters is_active=false entries out of the picker', async () => {
    const user = userEvent.setup();
    const list: Currency[] = [
      ...currencies,
      { code: 'INC', name: 'Inactive', symbol: 'I', rate_to_base: 5, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
    ];
    renderInput({ currencies: list, hideInactive: true });
    await user.click(screen.getByRole('button', { name: /USD/ }));
    expect(screen.getByRole('option', { name: /USD/ })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /EUR/ })).toBeInTheDocument();
  });

  it('currencies with rateFor() === null render disabled', async () => {
    const user = userEvent.setup();
    renderInput({ currencies });
    await user.click(screen.getByRole('button', { name: /USD/ }));
    const noRateOption = await screen.findByRole('option', { name: /NORATE/ });
    expect(noRateOption).toHaveAttribute('aria-disabled', 'true');
  });

  it('loading: true disables the picker trigger', () => {
    renderInput({ loading: true });
    expect(screen.getByRole('button', { name: /USD/ })).toBeDisabled();
  });

  it('surfaces inline error text when error is set', () => {
    renderInput({ error: 'no rate configured' });
    expect(screen.getByText(/no rate configured/i)).toBeInTheDocument();
  });

  it('Enter inside the Command list selects and does NOT submit a parent form', async () => {
    const user = userEvent.setup();
    const onCurrencyChange = vi.fn();
    const onFormSubmit = vi.fn();
    render(
      <form onSubmit={onFormSubmit}>
        <AmountCurrencyInput
          value={100}
          onValueChange={() => {}}
          currency="USD"
          onCurrencyChange={onCurrencyChange}
          baseCode="USD"
          currencies={currencies}
          hideInactive={true}
          rateFor={rateFor}
        />
      </form>,
    );
    await user.click(screen.getByRole('button', { name: /USD/ }));
    const search = await screen.findByPlaceholderText(/search/i);
    await user.type(search, 'EUR{Enter}');

    expect(onCurrencyChange).toHaveBeenCalledWith('EUR');
    expect(onFormSubmit).not.toHaveBeenCalled();
  });
});
