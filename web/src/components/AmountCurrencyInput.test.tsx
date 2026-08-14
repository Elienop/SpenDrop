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
    expect(onValueChange).toHaveBeenLastCalledWith(150);
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

  it('_KeepsMinZeroNowThatAmountsCanBeNegative: a typed minus stays invalid', () => {
    // Not leftover strictness. The sign of a transaction comes from the Refund
    // toggle and from nowhere else, so a minus in the digits is always a typo
    // — and on the two EDIT surfaces a typo that got through would silently
    // invert an existing row. Dropping this attribute is the one-character
    // change that would make that possible, so it is pinned.
    renderInput();
    expect(screen.getByRole('spinbutton')).toHaveAttribute('min', '0');
  });

  it('renders no preview when currency === baseCode', () => {
    renderInput({ value: 100, currency: 'USD', baseCode: 'USD' });
    expect(screen.queryByText(/≈/)).not.toBeInTheDocument();
  });

  it('renders a ≈ preview when currency !== baseCode with a valid rate', () => {
    renderInput({ value: 150000, currency: 'LBP', baseCode: 'USD' });
    expect(screen.getByText(/≈/)).toHaveTextContent(/\$1\.67/);
  });

  it('_NoReservationWhenNoPreviewOrError: wrapper does not reserve preview/error space when neither is shown', () => {
    // Regression guard for the "always tall" bug. An earlier fix added an
    // unconditional `pb-5` + absolute-positioned preview/error to keep the
    // wrapper height constant across currency toggles — that made the
    // Amount field permanently ~20px taller than peer fields. This test
    // pins the wrapper to the "no reservation when not needed" contract.
    const { container } = renderInput({ value: 100, currency: 'USD', baseCode: 'USD', error: null });
    const wrapper = container.firstElementChild as HTMLElement;
    // No pb-5 (or equivalent always-on bottom reservation) on the wrapper.
    expect(wrapper.className).not.toMatch(/\bpb-5\b/);
    // And no preview/error children under this state.
    expect(screen.queryByText(/≈/)).not.toBeInTheDocument();
    expect(
      container.querySelector('span.text-destructive'),
    ).not.toBeInTheDocument();
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
      {
        code: 'INC',
        name: 'Inactive',
        symbol: 'I',
        rate_to_base: 5,
        is_base: false,
        is_active: false,
        updated_at: '2026-04-01T00:00:00Z',
      },
    ];
    renderInput({ currencies: list, hideInactive: true });
    await user.click(screen.getByRole('button', { name: /USD/ }));
    expect(screen.getByRole('option', { name: /USD/ })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /EUR/ })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /INC/ })).not.toBeInTheDocument();
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

  // The `≈` preview promises what saving will store. On a NEW transaction that
  // is today's rate. On an EDIT that leaves the foreign money alone it is not:
  // the server carries the row's stored base value forward untouched, so after
  // a rate change the live conversion is a figure the save will not produce.
  //
  // Fixture throughout: 1,500,000 LBP booked at 89,000/USD => $16.85 stored.
  // The `currencies` list above puts today's LBP rate at 90,000, so a live
  // conversion of the same amount reads $16.67 — a different string, which is
  // what makes these assertions able to fail.
  describe('editing an existing row', () => {
    const storedLbp = {
      amount: 16.85,
      original_amount: 1500000,
      original_currency: 'LBP',
    };

    it('_UntouchedForeignMoneyPreviewsTheStoredValue: shows what the save keeps, not today s rate', () => {
      renderInput({
        value: 1500000,
        currency: 'LBP',
        baseCode: 'USD',
        storedMoney: storedLbp,
      });
      const preview = screen.getByText(/≈/);
      expect(preview).toHaveTextContent(/\$16\.85/);
      expect(preview).not.toHaveTextContent(/\$16\.67/);
    });

    it('_EditedAmountPreviewsTodaysRate: touching the money re-prices, so the preview follows', () => {
      // 1,600,000 / 90,000 = 17.777... The server re-prices a corrected
      // foreign amount at today's rate, so the stored $16.85 is now the wrong
      // promise and the live conversion is the right one.
      renderInput({
        value: 1600000,
        currency: 'LBP',
        baseCode: 'USD',
        storedMoney: storedLbp,
      });
      expect(screen.getByText(/≈/)).toHaveTextContent(/\$17\.78/);
    });

    it('_SwitchedCurrencyPreviewsTodaysRate: a different currency re-prices too', () => {
      // Same number of units, now called EUR: 1,500,000 / 0.9.
      renderInput({
        value: 1500000,
        currency: 'EUR',
        baseCode: 'USD',
        storedMoney: storedLbp,
      });
      expect(screen.getByText(/≈/)).toHaveTextContent(/\$1,666,666\.67/);
    });

    it('_QualifiesTheFrozenFigureWhenTheRateHasMoved: says the number came from the row', () => {
      // Without this, the figure reads as a bug against the rate the user set
      // in Settings, and the obvious "fix" — retyping the amount — is the one
      // action that re-prices the row and destroys the value being preserved.
      renderInput({
        value: 1500000,
        currency: 'LBP',
        baseCode: 'USD',
        storedMoney: storedLbp,
      });
      // Whole rendered string, not just the presence of the words: the
      // qualifier lives in a nested span and `toHaveTextContent` normalizes
      // whitespace, so this also pins the space that keeps it off the figure.
      expect(screen.getByText(/≈/)).toHaveTextContent('≈ $16.85 (as recorded)');
    });

    it('_SilentWhenTheRateHasNotMoved: no qualifier when both figures agree', () => {
      // Stored $16.67 is exactly 1,500,000 / 90,000 to the cent, so the
      // frozen figure and today's conversion are indistinguishable on screen
      // and there is nothing to explain.
      renderInput({
        value: 1500000,
        currency: 'LBP',
        baseCode: 'USD',
        storedMoney: { ...storedLbp, amount: 16.67 },
      });
      expect(screen.getByText(/≈/)).toHaveTextContent(/\$16\.67/);
      expect(screen.queryByText(/as recorded/i)).not.toBeInTheDocument();
    });

    // A refund of the same money: 1,500,000 LBP came back, so the row stores
    // -16.85 against a negative original amount. The box shows the MAGNITUDE
    // and the Refund toggle beside it carries the sign, which is why `negative`
    // is what lets this component rebuild the signed figure the save will send.
    // The freeze ANSWER no longer turns on it — the predicate compares
    // magnitudes — but the value handed to it is still the save's own.
    const storedLbpRefund = {
      amount: -16.85,
      original_amount: -1500000,
      original_currency: 'LBP',
    };

    it('_RefundFreezes: magnitude in the box, sign on the toggle', () => {
      renderInput({
        value: 1500000,
        negative: true,
        currency: 'LBP',
        baseCode: 'USD',
        storedMoney: storedLbpRefund,
      });
      // The stored figure, in the magnitude the digits above it are written
      // in — one sign per amount block, and it lives on the toggle.
      expect(screen.getByText(/≈/)).toHaveTextContent('≈ $16.85 (as recorded)');
      expect(screen.getByText(/≈/)).not.toHaveTextContent('-$16.85');
    });

    it('_TurningTheRefundOffKeepsTheBookedFigure: a flip is not a re-price', () => {
      // Nothing was retyped — only the toggle moved — so the same money is
      // being reclassified, not changed. The server keeps the magnitude it
      // booked and re-applies the direction (store.go, foreignMagnitudeUnchanged
      // + withSignOf), so the preview must keep promising $16.85. Today's rate
      // would say $16.67, and that is the number the save will NOT produce.
      //
      // This expected $16.67 while the server compared signed cents; the
      // predicate and this test moved together.
      renderInput({
        value: 1500000,
        negative: false,
        currency: 'LBP',
        baseCode: 'USD',
        storedMoney: storedLbpRefund,
      });
      const preview = screen.getByText(/≈/);
      // The stored value negated back to a purchase is +$16.85, and the figure
      // renders as the magnitude either way — one sign per amount block, on the
      // toggle. The qualifier is what says the number came from the row.
      expect(preview).toHaveTextContent('≈ $16.85 (as recorded)');
      expect(preview).not.toHaveTextContent(/\$16\.67/);
    });

    it('_TurningAnOrdinaryRowIntoARefundKeepsTheBookedFigure: the mirror case', () => {
      renderInput({
        value: 1500000,
        negative: true,
        currency: 'LBP',
        baseCode: 'USD',
        storedMoney: storedLbp,
      });
      expect(screen.getByText(/≈/)).toHaveTextContent('≈ $16.85 (as recorded)');
    });

    it('_FlippedAndCorrectedRePrices: moving both halves is still a money change', () => {
      // The control that keeps the two tests above from reading as "the
      // preview ignores the toggle". Correcting the figure while flipping it
      // fails the magnitude comparison, so today's rate applies: 1,600,000 /
      // 90,000 = $17.78, and nothing is frozen, so no qualifier.
      renderInput({
        value: 1600000,
        negative: true,
        currency: 'LBP',
        baseCode: 'USD',
        storedMoney: storedLbpRefund,
      });
      expect(screen.getByText(/≈/)).toHaveTextContent(/\$17\.78/);
      expect(screen.queryByText(/as recorded/i)).not.toBeInTheDocument();
    });

    it('_CreateSurfaceStillPreviewsTodaysRate: no storedMoney means no freeze', () => {
      // The create surfaces (TransactionEntryRow, QuickAdd) pass no
      // storedMoney. Same amount and currency as the frozen case above; the
      // absence of the prop is the whole difference.
      renderInput({ value: 1500000, currency: 'LBP', baseCode: 'USD' });
      const preview = screen.getByText(/≈/);
      expect(preview).toHaveTextContent(/\$16\.67/);
      expect(screen.queryByText(/as recorded/i)).not.toBeInTheDocument();
    });
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
