import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, it, expect, vi, type Mock } from 'vitest';
import { createElement, useState, type ReactNode } from 'react';
import { api } from '@/api/client';
import { TransactionEditSheet } from './TransactionEditSheet';
import type { Category, Transaction } from '../api/types';
import type { UpdateTransactionInput } from '../hooks/useTransactions';

// Today's rate. The repriced fixture below was BOOKED at 89,000, so a live
// conversion and the row's stored value disagree — which is the only condition
// under which the freeze contract is observable.
vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(async (path: string) => {
      if (path === 'currencies') {
        return [
          {
            code: 'USD',
            name: 'US Dollar',
            symbol: '$',
            rate_to_base: 1,
            is_base: true,
            updated_at: '2026-04-01T00:00:00Z',
          },
          {
            code: 'LBP',
            name: 'Lebanese Pound',
            symbol: 'LL',
            rate_to_base: 90000,
            is_base: false,
            updated_at: '2026-04-01T00:00:00Z',
          },
        ];
      }
      return [];
    }),
  },
}));

const categories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '2026-01-01',
  },
  {
    id: 2,
    name: 'Transport',
    type: 'expense',
    icon: null,
    sort_order: 2,
    is_active: true,
    created_at: '2026-01-01',
  },
];

function makeTx(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: 7,
    user_id: 1,
    created_by: 'Elie',
    date: '2026-04-01',
    amount: 25.5,
    original_amount: null,
    original_currency: null,
    description: 'Weekly groceries',
    category_id: 1,
    category_name: 'Groceries',
    category_type: 'expense',
    tags: 'food',
    notes: null,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
    ...overrides,
  };
}

/** 1,500,000 LBP booked at 89,000 stores $16.85. Today's 90,000 would make it
 *  $16.67 — the figure the editor must never promise for an untouched save. */
function makeRepricedRow(): Transaction {
  return makeTx({
    amount: 16.85,
    original_amount: 1500000,
    original_currency: 'LBP',
  });
}

interface SheetHarness {
  transaction?: Transaction | null;
  /** Wrapped in a spy by the harness, so callers get call assertions for free
   *  while still supplying their own behaviour. */
  onDelete?: (id: number) => Promise<void>;
  onClose?: () => void;
  /** Overridden only by the sign-toggle tests, which need an income category
   *  in the picker to check the control's wording. */
  categories?: Category[];
}

function renderSheet(overrides: SheetHarness = {}) {
  // Typed spies rather than bare `vi.fn()`: `onUpdate.mock.calls[0][0]` is
  // read as a payload below, and an untyped mock would make those assertions
  // unchecked.
  const onUpdate = vi
    .fn<(input: UpdateTransactionInput) => Promise<void>>()
    .mockResolvedValue(undefined);
  const onDelete = vi.fn<(id: number) => Promise<void>>(
    overrides.onDelete ?? (async () => {}),
  );
  const onClose = vi.fn<() => void>(overrides.onClose ?? (() => {}));
  const onError = vi.fn<(message: string) => void>();
  const onCloseFocus = vi.fn<() => void>();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  const result = render(
    <TransactionEditSheet
      transaction={
        overrides.transaction === undefined ? makeTx() : overrides.transaction
      }
      categories={overrides.categories ?? categories}
      onClose={onClose}
      onUpdate={onUpdate}
      onDelete={onDelete}
      onError={onError}
      onCloseFocus={onCloseFocus}
      descriptionSuggestions={[]}
      tagSuggestions={[]}
    />,
    { wrapper },
  );
  return { ...result, onUpdate, onDelete, onClose, onError, onCloseFocus };
}

describe('TransactionEditSheet shell', () => {
  it('renders nothing until a row is being edited', () => {
    renderSheet({ transaction: null });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  // The sanctioned phone container: full-bleed under `sm`, a bounded panel
  // above it. Both halves of the swap are pinned — asserting only the mobile
  // token would still pass if the base `w-3/4` were left in place.
  it('is a full-width right sheet on a phone and a bounded panel above sm', () => {
    renderSheet();
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveClass('w-full', 'sm:max-w-md');
    expect(dialog).not.toHaveClass('w-3/4');
    expect(dialog).not.toHaveClass('sm:max-w-sm');
  });

  it('names and describes itself for screen readers', () => {
    renderSheet();
    expect(
      screen.getByRole('dialog', { name: 'Edit transaction' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(
      /move it to Trash/i,
    );
  });

  it('prefills every field from the row', async () => {
    renderSheet();
    expect(screen.getByLabelText('Date')).toHaveValue('2026-04-01');
    expect(screen.getByLabelText('Description')).toHaveValue(
      'Weekly groceries',
    );
    // Queried through the trigger rather than by text: Radix Select mirrors
    // the selected label into a hidden native <option> for form compat, so
    // getByText('Groceries') matches twice.
    expect(screen.getByRole('combobox', { name: 'Category' })).toHaveTextContent(
      'Groceries',
    );
    expect(screen.getByText('food')).toBeInTheDocument();
    expect(await screen.findByRole('spinbutton')).toHaveValue(25.5);
  });
});

describe('TransactionEditSheet save', () => {
  it('sends the edited fields and closes', async () => {
    const user = userEvent.setup();
    const { onUpdate, onClose } = renderSheet();

    const description = screen.getByLabelText('Description');
    await user.clear(description);
    await user.type(description, 'Corner shop');

    // Save stays disabled until the currency list lands — the payload is built
    // against the household base code, and building it against the "USD"
    // placeholder would misprice a non-USD household. Waiting on the button's
    // own state rather than on a proxy, because the currency picker renders
    // its (placeholder) code immediately and so proves nothing.
    const save = screen.getByRole('button', { name: 'Save' });
    await waitFor(() => expect(save).toBeEnabled());

    // happy-dom does not run implicit form submission for a click inside a
    // Radix dialog (react-remove-scroll puts pointer-events:none on <body>),
    // so the submit is dispatched directly — the same thing the inline-row
    // tests do. The button's wiring is ASSERTED rather than assumed: a submit
    // event on the form fires onSubmit no matter what, so without these two
    // lines this test would still pass with Save detached from the form.
    expect(save).toHaveAttribute('type', 'submit');
    const form = save.closest('form');
    expect(form).not.toBeNull();
    fireEvent.submit(form!);

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate.mock.calls[0][0]).toMatchObject({
      id: 7,
      date: '2026-04-01',
      description: 'Corner shop',
      category_id: 1,
      tags: 'food',
      amount: 25.5,
    });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('Cancel closes without sending anything', async () => {
    const user = userEvent.setup();
    const { onUpdate, onClose } = renderSheet();

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onClose).toHaveBeenCalled();
    expect(onUpdate).not.toHaveBeenCalled();
  });
});

// The invariant this whole surface is riskiest for. The server carries a
// stored base value forward VERBATIM when a save merely restates the same
// foreign amount in the same currency (database.foreignMagnitudeUnchanged), so an
// edit sheet that re-derived the amount from today's rate would silently move
// money the user never touched. The desktop row has carried this contract
// since it shipped; the sheet must not be a second, weaker copy.
describe('TransactionEditSheet — foreign money is not re-priced', () => {
  it('_PreviewShowsWhatSavingStores: previews the stored value, not today s rate', async () => {
    renderSheet({ transaction: makeRepricedRow() });
    await screen.findByRole('button', { name: /currency: lbp/i });

    const preview = await screen.findByText(/≈/);
    expect(preview).toHaveTextContent(/\$16\.85/);
    expect(preview).not.toHaveTextContent(/\$16\.67/);
    expect(screen.getByText(/as recorded/i)).toBeInTheDocument();
  });

  it('_SaveCarriesTheForeignMoneyThrough: an untouched re-save resends the booked amount', async () => {
    const { onUpdate } = renderSheet({ transaction: makeRepricedRow() });

    // Sync on the state this test DEPENDS on, not on a value that renders
    // before it. The currency trigger shows "LBP" immediately, straight from
    // `toEditDefaults` — it says nothing about whether `useCurrencies` has
    // resolved. Submit inside that window and `rateFor('LBP')` is still null,
    // `toCreatePayload` throws, and `onUpdate` is never called: the test fails
    // for a reason that has nothing to do with the freeze contract it exists
    // to protect. The Save button's enabled state is the real signal, because
    // `saveDisabled` folds in `currenciesLoading`.
    const save = screen.getByRole('button', { name: 'Save' });
    await waitFor(() => expect(save).toBeEnabled());

    fireEvent.submit(save.closest('form')!);

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    // original_* going out unchanged IS the condition the server freezes on.
    expect(onUpdate.mock.calls[0][0]).toMatchObject({
      original_amount: 1500000,
      original_currency: 'LBP',
    });
  });

  // The opposite direction, so the test above cannot pass by pinning a
  // constant: once the foreign amount moves, the server re-prices and the
  // preview has to follow it. 1,600,000 / 90,000 = $17.78.
  it('_PreviewFollowsTheSaveOnceTheAmountIsEdited: a corrected amount re-prices at today s rate', async () => {
    const user = userEvent.setup();
    renderSheet({ transaction: makeRepricedRow() });
    await screen.findByRole('button', { name: /currency: lbp/i });

    const amount = screen.getByRole('spinbutton');
    await user.clear(amount);
    await user.type(amount, '1600000');

    await waitFor(() => {
      expect(screen.getByText(/≈/)).toHaveTextContent(/\$17\.78/);
    });
    expect(screen.queryByText(/as recorded/i)).not.toBeInTheDocument();
  });
});

// data-M2. The sheet builds the payload against the household base code, so a
// row recorded in a currency with no configured rate cannot be saved at all —
// and the user has to be told which, not just handed a dead button.
describe('TransactionEditSheet — currency with no configured rate', () => {
  it('surfaces the reason and refuses the save', async () => {
    // EUR is absent from the mocked currency list, so rateFor('EUR') is null.
    renderSheet({
      transaction: makeTx({
        amount: 30,
        original_amount: 27,
        original_currency: 'EUR',
      }),
    });
    await screen.findByRole('button', { name: /currency: eur/i });

    expect(
      await screen.findByText(/no rate configured for this currency/i),
    ).toBeInTheDocument();

    const save = screen.getByRole('button', { name: 'Save' });
    // Stays disabled rather than settling — the currency fetch resolving is
    // what normally releases it, and that has already happened here.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /currency: eur/i })).toBeEnabled(),
    );
    expect(save).toBeDisabled();
  });
});

// UX-D6. A dimmed control that still reads "Save" says "you cannot", not
// "this is happening".
describe('TransactionEditSheet — in-flight cue', () => {
  it('reads Saving… while the update is in flight', async () => {
    let release: (() => void) | undefined;
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    const { onUpdate } = renderSheet();
    onUpdate.mockReturnValueOnce(pending);

    const save = screen.getByRole('button', { name: 'Save' });
    await waitFor(() => expect(save).toBeEnabled());
    fireEvent.submit(save.closest('form')!);

    const busy = await screen.findByRole('button', { name: 'Saving…' });
    expect(busy).toHaveAttribute('aria-busy', 'true');
    expect(busy).toBeDisabled();

    release!();
    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });
});

describe('TransactionEditSheet delete', () => {
  // Delete lives here because a card has no per-row actions menu. It runs the
  // page's own row-delete handler, so the undo toast and the heading refocus
  // are the table path's, not a second implementation.
  it('routes through the page delete handler and dismisses the sheet first', async () => {
    const user = userEvent.setup();
    const order: string[] = [];
    const onClose = vi.fn(() => {
      order.push('close');
    });
    const onDelete = vi.fn(async () => {
      order.push('delete');
    });
    renderSheet({ onClose, onDelete });

    await user.click(
      screen.getByRole('button', { name: /delete transaction/i }),
    );

    await waitFor(() => expect(onDelete).toHaveBeenCalledWith(7));
    // Closing first releases the sheet's focus trap, so the heading refocus
    // that onDelete performs is not immediately pulled back into a panel that
    // is unmounting.
    expect(order).toEqual(['close', 'delete']);
  });

  it('does not save on the way out', async () => {
    const user = userEvent.setup();
    const { onUpdate } = renderSheet();
    await user.click(
      screen.getByRole('button', { name: /delete transaction/i }),
    );
    expect(onUpdate).not.toHaveBeenCalled();
  });
});

// The reason the refund affordance is REQUIRED on the edit surfaces rather
// than nice to have. The amount box shows a stored refund's MAGNITUDE — it
// refuses a typed minus, and always did — so if nothing else carried the sign,
// fixing a typo in the description would save the row back positive and turn
// money returned into money spent, with no error and nothing on screen having
// said so.
describe('TransactionEditSheet — a stored refund round-trips', () => {
  /** $20 that came back: a NEGATIVE amount on an expense category. */
  function makeRefundRow(): Transaction {
    return makeTx({ amount: -20, description: 'Returned milk' });
  }

  async function submitSheet() {
    const save = screen.getByRole('button', { name: 'Save' });
    await waitFor(() => expect(save).toBeEnabled());
    expect(save).toHaveAttribute('type', 'submit');
    const form = save.closest('form');
    expect(form).not.toBeNull();
    fireEvent.submit(form!);
  }

  it('_OpensWithTheToggleOnAndPositiveDigits: the sign is on the control, not in the box', async () => {
    renderSheet({ transaction: makeRefundRow() });

    expect(await screen.findByRole('spinbutton')).toHaveValue(20);
    expect(screen.getByRole('switch', { name: 'Refund' })).toBeChecked();
  });

  it('_UnrelatedEditKeepsTheSign: a description fix does not un-refund the row', async () => {
    const user = userEvent.setup();
    const { onUpdate } = renderSheet({ transaction: makeRefundRow() });

    const description = screen.getByLabelText('Description');
    await user.clear(description);
    await user.type(description, 'Returned oat milk');
    await submitSheet();

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate.mock.calls[0][0]).toMatchObject({
      id: 7,
      description: 'Returned oat milk',
      amount: -20,
    });
  });

  it('_TurningItOffFlipsTheRowPositive: the correction path works too', async () => {
    const user = userEvent.setup();
    const { onUpdate } = renderSheet({ transaction: makeRefundRow() });

    await user.click(screen.getByRole('switch', { name: 'Refund' }));
    await submitSheet();

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate.mock.calls[0][0]).toMatchObject({ id: 7, amount: 20 });
  });

  it('_TurningItOnMakesARefund: an ordinary row can become one', async () => {
    const user = userEvent.setup();
    const { onUpdate } = renderSheet();

    await user.click(screen.getByRole('switch', { name: 'Refund' }));
    await submitSheet();

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate.mock.calls[0][0]).toMatchObject({ id: 7, amount: -25.5 });
  });

  it('_ForeignRefundKeepsBothHalvesNegative: sign then divide, on the edit path too', async () => {
    const { onUpdate } = renderSheet({
      transaction: makeTx({
        amount: -16.85,
        original_amount: -1500000,
        original_currency: 'LBP',
      }),
    });

    // Opens as 1,500,000 with Refund on — the magnitude in the box, the sign
    // on the control — and an untouched save has to put both back.
    expect(await screen.findByRole('spinbutton')).toHaveValue(1500000);
    expect(screen.getByRole('switch', { name: 'Refund' })).toBeChecked();

    // The preview reads the toggle too: this row was booked at 89,000 and
    // today's rate is 90,000, so the frozen figure ($16.85) and the live one
    // ($16.67) differ — and the comparison that picks between them is over
    // SIGNED cents. In the magnitude the digits above it are written in.
    const preview = await screen.findByText(/≈/);
    expect(preview).toHaveTextContent('≈ $16.85 (as recorded)');

    await submitSheet();

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    // The wire always carries the freshly divided figure — the freeze is the
    // SERVER's, keyed on original_* arriving unchanged (see
    // _SaveCarriesTheForeignMoneyThrough above). What this pins is that the
    // sign went on BEFORE the division: -1,500,000 / 90,000 = -16.67, and both
    // halves of the money pair are negative. Signing afterwards would send a
    // positive original_amount, which is the `sign_mismatch` shape.
    expect(onUpdate.mock.calls[0][0]).toMatchObject({
      id: 7,
      original_amount: -1500000,
      original_currency: 'LBP',
      amount: -16.67,
    });
  });

  it('_LabelFollowsTheCategoryKind: an income row offers a Reversal', async () => {
    renderSheet({
      transaction: makeTx({
        category_id: 3,
        category_name: 'Salary',
        category_type: 'income',
        amount: -100,
      }),
      categories: [
        ...categories,
        {
          id: 3,
          name: 'Salary',
          type: 'income',
          icon: null,
          sort_order: 3,
          is_active: true,
          created_at: '2026-01-01',
        },
      ],
    });

    expect(
      await screen.findByRole('switch', { name: 'Reversal' }),
    ).toBeChecked();
    expect(
      screen.queryByRole('switch', { name: 'Refund' }),
    ).not.toBeInTheDocument();
  });
});

// The one window in which the hook's rehydration effect can change anything:
// the household's real base currency only exists once `useCurrencies`
// resolves, and a refetch landing inside that window replaces the row under an
// open editor. The effect re-derives the form's money from the CURRENT row —
// magnitude and sign together, because splitting them and rehydrating only one
// would leave the box showing one row's number under the other row's sign.
//
// Neither half is reachable from a steady-state test (mutation-checked: both
// the amount branch, which predates signed amounts, and the sign branch
// survive every other test in this file), which is why this drives the timing
// deliberately.
describe('TransactionEditSheet — a refetch inside the currency-loading window', () => {
  function SwapHost({ first, second }: { first: Transaction; second: Transaction }) {
    const [tx, setTx] = useState(first);
    return (
      <>
        {/* fireEvent, not user-event, when this is clicked: the sheet is modal
            and react-remove-scroll puts pointer-events:none on <body>, which
            user-event refuses to click through. */}
        <button type="button" onClick={() => setTx(second)}>
          swap row
        </button>
        <TransactionEditSheet
          transaction={tx}
          categories={categories}
          onClose={vi.fn()}
          onUpdate={vi.fn<(input: UpdateTransactionInput) => Promise<void>>()}
          onDelete={vi.fn<(id: number) => Promise<void>>()}
          onError={vi.fn()}
          onCloseFocus={vi.fn()}
        />
      </>
    );
  }

  it('_RehydratesMagnitudeAndSignTogether: the editor follows the row it is now editing', async () => {
    // Hold the currency fetch open so the effect has not latched yet.
    let releaseCurrencies: (list: unknown) => void = () => {};
    (api.get as Mock).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          releaseCurrencies = resolve;
        }),
    );

    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <SwapHost
        first={makeTx({ amount: 25.5 })}
        second={makeTx({ amount: -20, description: 'Returned milk' })}
      />,
      {
        wrapper: ({ children }: { children: ReactNode }) =>
          createElement(QueryClientProvider, { client }, children),
      },
    );

    // The row is replaced while the fields still hold the old one's money.
    // `hidden: true` because the open sheet is modal: Radix marks everything
    // outside it `aria-hidden`, and the default role query skips those.
    fireEvent.click(
      screen.getByRole('button', { name: 'swap row', hidden: true }),
    );
    expect(await screen.findByRole('spinbutton')).toHaveValue(25.5);

    releaseCurrencies([
      {
        code: 'USD',
        name: 'US Dollar',
        symbol: '$',
        rate_to_base: 1,
        is_base: true,
        updated_at: '2026-04-01T00:00:00Z',
      },
    ]);

    // Both halves move, or the next save writes a number that belongs to
    // neither row.
    await waitFor(() =>
      expect(screen.getByRole('spinbutton')).toHaveValue(20),
    );
    expect(screen.getByRole('switch', { name: 'Refund' })).toBeChecked();
  });
});
