import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { Mock } from 'vitest';
import { createElement, type ReactNode } from 'react';
import { TransactionRow } from './TransactionRow';
import type { TransactionRowProps } from './TransactionRow';
import type { Transaction, Category } from '../api/types';

vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(async (path: string) => {
      if (path === 'currencies') {
        return [
          { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'LBP', name: 'Lebanese Pound', symbol: 'LL', rate_to_base: 90000, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
        ];
      }
      return [];
    }),
  },
}));

const mockCategories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '2026-01-01',
  },
];

function makeTx(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: 1,
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
    tags: null,
    notes: null,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
    ...overrides,
  };
}

function renderRow(
  transaction: Transaction,
  onUpdate?: TransactionRowProps['onUpdate'],
  onDelete?: TransactionRowProps['onDelete'],
  extraProps: Partial<TransactionRowProps> = {},
) {
  const update = onUpdate ?? vi.fn().mockResolvedValue(undefined);
  const del = onDelete ?? vi.fn().mockResolvedValue(undefined);
  // Fresh QueryClient per render so the `useCurrencies` cache is isolated per
  // test (replacing the old module-cache reset shim). `retry: false` keeps
  // rejected currency fetches from re-firing.
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return render(
    <table>
      <tbody>
        <TransactionRow
          transaction={transaction}
          categories={mockCategories}
          onUpdate={update}
          onDelete={del}
          onError={vi.fn()}
          {...extraProps}
        />
      </tbody>
    </table>,
    { wrapper },
  );
}

async function openActionsMenu(description: string) {
  const user = userEvent.setup();
  await user.click(
    screen.getByRole('button', { name: `Actions for ${description}` }),
  );
  return user;
}

describe('TransactionRow date rendering', () => {
  it('renders the stored day, not the day before it', () => {
    // The desktop table's date cell had NO assertion at all, which is how a
    // UTC-midnight parse survived here while its four siblings were caught:
    // `new Date('2026-04-01')` is midnight UTC, which is March 31st for every
    // user west of GMT. The literal is deliberate — deriving the expectation
    // with the same call the component makes is what let this class of bug
    // hide everywhere else.
    renderRow(makeTx());
    expect(screen.getByText('Apr 1, 2026')).toBeInTheDocument();
    expect(screen.queryByText('Mar 31, 2026')).not.toBeInTheDocument();
  });
});

describe('TransactionRow tags display', () => {
  it('renders tag pills when transaction has tags', () => {
    renderRow(makeTx({ tags: 'groceries,weekly' }));
    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
  });

  it('renders empty cell when tags are null', () => {
    renderRow(makeTx({ tags: null }));
    expect(screen.queryByText('groceries')).not.toBeInTheDocument();
  });

  it('renders empty cell when tags are empty string', () => {
    renderRow(makeTx({ tags: '' }));
    expect(screen.queryByText('groceries')).not.toBeInTheDocument();
  });
});

// B6j. The ledger is household-wide but GET /users is admin-only, so a member
// could not resolve user_id herself — she found out a row was her spouse's when
// Save came back 403. The creator therefore has to be readable on the resting
// row, with no hover and no click (Radix tooltips are dead on touch, and the
// phone is the primary surface).
describe('TransactionRow creator attribution', () => {
  it('shows who entered the row without any interaction', () => {
    // created_by carries display_name, so it renders as a plain name — the
    // Sidebar's "@handle" form belongs to the login identifier, not this.
    renderRow(makeTx({ created_by: 'Partner Name' }));
    const creator = screen.getByText('Partner Name');
    expect(creator).toBeInTheDocument();
    // A bare name in a muted line does not announce what it is.
    expect(creator.closest('p')).toHaveTextContent('Entered by Partner Name');
  });

  it('renders a neutral fallback when the creator account is gone', () => {
    // "" is the backend's documented "creator unknown" value (the list query's
    // LEFT JOIN found no user row). It must never surface as a blank.
    renderRow(makeTx({ created_by: '' }));
    const fallback = screen.getByText('Unknown');
    expect(fallback).toBeInTheDocument();
    expect(fallback.closest('p')).toHaveTextContent('Entered by Unknown');
  });

  it('keeps the description readable alongside the attribution', () => {
    // The creator line shares the description cell, so the cell's truncation
    // (and the title= that makes an over-long imported description reachable)
    // has to survive moving onto an inner element.
    const long = 'x'.repeat(400);
    renderRow(makeTx({ description: long, created_by: 'Elie' }));
    const desc = screen.getByTitle(long);
    expect(desc).toHaveClass('truncate');
    expect(screen.getByText('Elie')).toBeInTheDocument();
  });
});

describe('TransactionRow actions menu', () => {
  it('exposes an actions menu trigger with an explicit aria-label', () => {
    renderRow(makeTx({ description: 'Weekly groceries' }));
    expect(
      screen.getByRole('button', { name: 'Actions for Weekly groceries' }),
    ).toBeInTheDocument();
  });

  it('opens a menu with Edit and Delete items when the trigger is clicked', async () => {
    renderRow(makeTx({ description: 'Weekly groceries' }));
    await openActionsMenu('Weekly groceries');
    expect(screen.getByRole('menuitem', { name: /edit/i })).toBeInTheDocument();
    expect(
      screen.getByRole('menuitem', { name: /delete/i }),
    ).toBeInTheDocument();
  });

  it('calls onDelete when Delete is chosen', async () => {
    const onDelete = vi
      .fn<TransactionRowProps['onDelete']>()
      .mockResolvedValue(undefined);
    renderRow(
      makeTx({ description: 'Weekly groceries' }),
      undefined,
      onDelete,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /delete/i }));
    expect(onDelete).toHaveBeenCalledWith(1);
  });

  // The opt-out from Radix's focus-to-trigger restore is CONDITIONAL. It
  // exists for the two items, whose runs unmount the trigger (Delete removes
  // the row, Edit replaces it with the edit form) — but on a plain dismissal
  // the trigger survives and is exactly what keyboard focus must return to.
  // Measured pre-fix on the built app: Escape after opening a row menu left
  // document.activeElement on <body>.
  it('returns focus to the trigger when the menu is dismissed without an action', async () => {
    renderRow(makeTx({ description: 'Weekly groceries' }));
    const trigger = screen.getByRole('button', {
      name: 'Actions for Weekly groceries',
    });
    const user = await openActionsMenu('Weekly groceries');
    // Positive control: the menu really opened, so the focus assertion below
    // cannot pass because focus never left the trigger in the first place.
    expect(await screen.findByRole('menuitem', { name: /edit/i })).toBeVisible();

    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('menuitem')).toBeNull());

    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  // Where focus goes after Edit is the row's own business, not the opt-out's —
  // the form's date field takes it, asserted separately below. The contract
  // this pins instead is the read-AND-CLEAR half: an action's suppression must
  // not latch, or every dismissal after the first Edit strands focus again.
  // Kills the mutant that sets the ran-flag in the items but never clears it in
  // onCloseAutoFocus.
  it('an action does not latch the opt-out — the next plain dismissal still restores', async () => {
    renderRow(makeTx({ description: 'Weekly groceries' }));
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    // Positive control: Edit really ran — the row is now the edit form.
    expect(
      await screen.findByRole('button', { name: /save/i }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /cancel/i }));

    // Fresh display row, fresh trigger element — grab it after the remount.
    const trigger = screen.getByRole('button', {
      name: 'Actions for Weekly groceries',
    });
    await user.click(trigger);
    expect(await screen.findByRole('menuitem', { name: /edit/i })).toBeVisible();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('menuitem')).toBeNull());

    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });
});

describe('TransactionRow tags editing', () => {
  let onUpdate: Mock<TransactionRowProps['onUpdate']>;

  beforeEach(() => {
    onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockResolvedValue(undefined);
  });

  it('shows TagInput with existing tags in edit mode', async () => {
    renderRow(makeTx({ tags: 'groceries,weekly' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
  });

  it('includes tags in save/update call', async () => {
    renderRow(makeTx({ tags: 'groceries' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith(
        expect.objectContaining({ tags: 'groceries' }),
      );
    });
  });

  // B6d. onError raises a page-level banner. Nothing used to lower it, so a
  // save that failed once left "Failed to save" standing over every later
  // successful edit of any row.
  it('clears the error on a save that succeeds after one that failed', async () => {
    const onError = vi.fn<TransactionRowProps['onError']>();
    const onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate, undefined, { onError });
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);
    await waitFor(() => expect(onError).toHaveBeenCalledWith('boom'));

    // Still in edit mode after the failure, so the retry is the same form.
    fireEvent.submit(form);
    await waitFor(() => expect(onError).toHaveBeenLastCalledWith(''));
  });

  it('resets tags on cancel', async () => {
    renderRow(makeTx({ tags: 'groceries' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    const tagInputEl = screen.getByLabelText('Add tag');
    await user.type(tagInputEl, 'extra{Enter}');
    expect(screen.getByText('extra')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /cancel/i }));

    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.queryByText('extra')).not.toBeInTheDocument();
  });
});

describe('TransactionRow amount display', () => {
  it('renders single-line amount when original_* are null (regression)', () => {
    renderRow(makeTx({ amount: 25.5, original_amount: null, original_currency: null }));
    expect(screen.getByText(/-\$25\.50/)).toBeInTheDocument();
    expect(screen.queryByTestId('amount-display-secondary')).not.toBeInTheDocument();
  });

  it('renders two-line amount when original_currency differs from base', () => {
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
    );
    expect(screen.getByText(/-\$1\.67/)).toBeInTheDocument();
    expect(screen.getByTestId('amount-display-secondary')).toHaveTextContent('LBP');
  });

  it('falls back to single-line when original_currency equals base (defensive)', () => {
    renderRow(
      makeTx({
        amount: 25.5,
        original_amount: 25.5,
        original_currency: 'USD',
      }),
    );
    expect(screen.queryByTestId('amount-display-secondary')).not.toBeInTheDocument();
  });
});

describe('TransactionRow edit — currency', () => {
  it('prefills picker with original_currency when set', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
      onUpdate,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    expect(
      await screen.findByRole('button', { name: /currency: lbp/i }),
    ).toBeInTheDocument();
    expect(screen.getByDisplayValue('150000')).toBeInTheDocument();
  });

  it('prefills picker with baseCode when original_* is null', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    expect(
      await screen.findByRole('button', { name: /currency: usd/i }),
    ).toBeInTheDocument();
  });

  it('_PrefillsBaseWhenOriginalNull_AfterLoad: race guard updates picker once currencies resolve to non-USD base', async () => {
    const client = await import('@/api/client');
    const original = (client.api.get as ReturnType<typeof vi.fn>).getMockImplementation();
    (client.api.get as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path === 'currencies') {
        return [
          { code: 'USD', name: 'US Dollar', symbol: '$', rate_to_base: 1.1, is_base: false, updated_at: '2026-04-01T00:00:00Z' },
          { code: 'EUR', name: 'Euro', symbol: '\u20ac', rate_to_base: 1, is_base: true, updated_at: '2026-04-01T00:00:00Z' },
        ];
      }
      return [];
    });

    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5, original_amount: null, original_currency: null }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    expect(
      await screen.findByRole('button', { name: /currency: eur/i }),
    ).toBeInTheDocument();

    if (original) {
      (client.api.get as ReturnType<typeof vi.fn>).mockImplementation(original);
    }
  });

  it('saves expanded payload when user switches to non-base currency', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: usd/i });

    await user.click(screen.getByRole('button', { name: /currency: usd/i }));
    await user.click(await screen.findByRole('option', { name: /LBP/ }));
    const amountInput = screen.getByRole('spinbutton');
    await user.clear(amountInput);
    await user.type(amountInput, '150000');

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledTimes(1);
    });
    const payload = onUpdate.mock.calls[0][0];
    expect(payload).toMatchObject({
      id: 1,
      amount: 1.67,
      original_amount: 150000,
      original_currency: 'LBP',
    });
    expect(payload).not.toHaveProperty('currency');
  });

  it('saves collapsed payload when user switches back to base currency', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(
      makeTx({
        amount: 1.67,
        original_amount: 150000,
        original_currency: 'LBP',
      }),
      onUpdate,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: lbp/i });

    await user.click(screen.getByRole('button', { name: /currency: lbp/i }));
    await user.click(await screen.findByRole('option', { name: /USD/ }));
    const amountInput = screen.getByRole('spinbutton');
    await user.clear(amountInput);
    await user.type(amountInput, '2');

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledTimes(1);
    });
    const payload = onUpdate.mock.calls[0][0];
    expect(payload).toMatchObject({ id: 1, amount: 2 });
    expect(payload).not.toHaveProperty('original_amount');
    expect(payload).not.toHaveProperty('original_currency');
  });
});

// ---------------------------------------------------------------------------
// Keyboard shortcuts for inline edit (see spec 2026-04-18)
// ---------------------------------------------------------------------------
describe('TransactionRow — keyboard shortcuts', () => {
  // Scoped to this describe intentionally: the body uses `screen.getByRole`,
  // which requires an active `render` that the sibling tests at module scope
  // do not all share. Do not hoist to module level without also factoring in
  // which describe blocks actually render first.
  async function openEditMode(description = 'Weekly groceries') {
    const user = await openActionsMenu(description);
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    return user;
  }

  it('Esc on a clean edit row cancels, does not call onUpdate', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ description: 'Weekly groceries' }), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.keyboard('{Escape}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument();
  });

  it('Esc on a dirty edit row reverts silently to original values', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ description: 'Weekly groceries' }), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.clear(desc);
    await user.type(desc, 'EDITED BUT ABANDONED');
    expect(desc.value).toBe('EDITED BUT ABANDONED');

    await user.keyboard('{Escape}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
    expect(screen.queryByText('EDITED BUT ABANDONED')).not.toBeInTheDocument();
  });

  it('Enter from description input (no ghost) saves the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.keyboard('{Enter}');

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Enter from description with a ghost visible accepts ghost, does NOT save', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ description: 'x' }), onUpdate, undefined, {
      descriptionSuggestions: ['groceries-weekly'],
    });
    const user = await openEditMode('x');

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.clear(desc);
    await user.type(desc, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries-weekly');

    await user.keyboard('{Enter}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(desc.value).toBe('groceries-weekly');

    await user.keyboard('{Enter}');
    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Enter from tag input with typed buffer commits the chip, does NOT save', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ tags: null }), onUpdate);
    const user = await openEditMode();

    const tagInput = screen.getByLabelText('Add tag') as HTMLInputElement;
    await user.click(tagInput);
    await user.type(tagInput, 'urgent');
    await user.keyboard('{Enter}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.getByText('urgent')).toBeInTheDocument();
    expect(tagInput.value).toBe('');

    await user.keyboard('{Enter}');
    await waitFor(() =>
      expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ tags: 'urgent' })),
    );
  });

  it('Enter from amount input saves the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const amount = screen.getByRole('spinbutton') as HTMLInputElement;
    await user.click(amount);
    await user.keyboard('{Enter}');

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Ctrl+Enter force-saves even when a ghost is visible', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate, undefined, {
      descriptionSuggestions: ['groceries-weekly'],
    });
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.clear(desc);
    await user.type(desc, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries-weekly');

    fireEvent.keyDown(desc, { key: 'Enter', ctrlKey: true });

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate.mock.calls[0][0].description).toBe('gro');
  });

  it('Cmd+Enter behaves the same as Ctrl+Enter (macOS)', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    fireEvent.keyDown(desc, { key: 'Enter', metaKey: true });

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Esc inside open category Select closes the Select and does NOT cancel the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    // In Radix react-select ^2.2.6 the SelectTrigger has role=combobox but does
    // NOT set aria-haspopup=listbox. The TagInput and AutocompleteInput in this
    // row are also role=combobox (on <input>s), so we disambiguate by tag — the
    // Radix Select trigger is the only <button role=combobox>.
    //
    // Revisit this selector if Radix Select ever changes its trigger element
    // from <button> (e.g., to a div with role=button) or starts emitting
    // aria-haspopup="listbox" — the semantically stronger selector at that
    // point would be aria-haspopup, matching ARIA 1.2 combobox guidance.
    const trigger = screen
      .getAllByRole('combobox')
      .find((el) => el.tagName === 'BUTTON');
    if (!trigger) throw new Error('Category SelectTrigger not found');
    await user.click(trigger);
    await screen.findByRole('option', { name: 'Groceries' });

    await user.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('option', { name: 'Groceries' })).not.toBeInTheDocument(),
    );
    expect(screen.getByRole('button', { name: /save/i })).toBeInTheDocument();
    expect(onUpdate).not.toHaveBeenCalled();

    // Second Escape: cancels the row (receiver of last resort). In a real
    // browser Radix's FocusScope restores focus to the trigger and
    // user.keyboard would target it; happy-dom doesn't reliably replay that
    // restoration, so dispatch directly to the trigger where focus would be.
    fireEvent.keyDown(trigger, { key: 'Escape' });
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument(),
    );
    expect(onUpdate).not.toHaveBeenCalled();
  });
});

describe('TransactionRow edit — focus handoff', () => {
  // The actions menu suppresses Radix's focus-to-trigger restore when Edit runs
  // (the trigger unmounts with the display row), so the row itself owns both
  // ends of the swap. Measured pre-fix on the built app: Edit from the keyboard
  // left document.activeElement on <body>, and the next Tab restarted at the
  // top of the page.

  /** Open Edit and wait for the form that replaces the row. */
  async function openEdit() {
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    // Positive control: the swap really happened, so a focus assertion below
    // cannot pass against a row that never turned into a form.
    expect(
      await screen.findByRole('button', { name: /save/i }),
    ).toBeInTheDocument();
    return user;
  }

  it('lands focus on the date field when Edit opens', async () => {
    renderRow(makeTx());
    await openEdit();

    const date = screen.getByDisplayValue('2026-04-01');
    await waitFor(() => expect(date).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('hands focus back to the row’s menu trigger when Escape cancels', async () => {
    renderRow(makeTx());
    await openEdit();
    const date = screen.getByDisplayValue('2026-04-01');
    await waitFor(() => expect(date).toHaveFocus());

    // Dispatched at the field focus is actually in — the row's keydown handler
    // is what reads it. user-event's '{Escape}' would do the same thing from
    // the same element, but keyboard synthesis is unreliable in happy-dom.
    fireEvent.keyDown(date, { key: 'Escape' });

    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: /save/i }),
      ).not.toBeInTheDocument(),
    );
    // The display row has remounted, so this is a NEW element — which is why
    // the component cannot restore focus by remembering the old one.
    const trigger = screen.getByRole('button', {
      name: 'Actions for Weekly groceries',
    });
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('hands focus back to the row’s menu trigger after a successful save', async () => {
    const onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    await openEdit();

    const save = screen.getByRole('button', { name: /save/i });
    await waitFor(() => expect(save).toBeEnabled());
    fireEvent.submit(save.closest('form')!);

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: /save/i }),
      ).not.toBeInTheDocument(),
    );
    const trigger = screen.getByRole('button', {
      name: 'Actions for Weekly groceries',
    });
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});

describe('TransactionRow edit — in-flight cue', () => {
  // Parity with the phone edit sheet. The two surfaces run on one hook and
  // should not report the same request differently — a button that stays
  // "Save" while dimmed reads as refusal, not progress.
  it('reads Saving… while the update is in flight', async () => {
    let release: (() => void) | undefined;
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    const onUpdate = vi.fn().mockReturnValueOnce(pending);
    renderRow(makeTx(), onUpdate);

    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    // Same discipline as the sheet's save tests: wait on Save's own enabled
    // state, which folds in `currenciesLoading`, rather than on anything that
    // renders from a placeholder before the fetch resolves.
    const save = screen.getByRole('button', { name: /save/i });
    await waitFor(() => expect(save).toBeEnabled());
    fireEvent.submit(save.closest('form')!);

    const busy = await screen.findByRole('button', { name: 'Saving…' });
    expect(busy).toHaveAttribute('aria-busy', 'true');
    expect(busy).toBeDisabled();

    release!();
    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });
});

describe('TransactionRow edit — preview after a rate change', () => {
  // The mocked currencies list prices LBP at 90,000/USD today. This row was
  // booked at 89,000, so it stores $16.85 for its 1,500,000 LBP — and it keeps
  // storing $16.85 through any edit that does not touch the foreign money
  // (internal/database/store.go, foreignMagnitudeUnchanged). A live conversion
  // would say $16.67, which is the figure the editor must not promise.
  function makeRepricedRow() {
    return makeTx({
      amount: 16.85,
      original_amount: 1500000,
      original_currency: 'LBP',
    });
  }

  it('_PreviewShowsWhatSavingStores: opening the editor previews the stored value, not today s rate', async () => {
    renderRow(makeRepricedRow());
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: lbp/i });

    const preview = await screen.findByText(/≈/);
    expect(preview).toHaveTextContent(/\$16\.85/);
    expect(preview).not.toHaveTextContent(/\$16\.67/);
    expect(screen.getByText(/as recorded/i)).toBeInTheDocument();
  });

  it('_PreviewFollowsTheSaveOnceTheAmountIsEdited: a corrected amount re-prices at today s rate', async () => {
    // The server re-prices whenever the foreign amount moves, so from the
    // first keystroke the stored value stops being the right promise. 1,600,000
    // / 90,000 = $17.78. The qualifier goes with it — nothing is frozen now.
    renderRow(makeRepricedRow());
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: lbp/i });

    const amountInput = screen.getByRole('spinbutton');
    await user.clear(amountInput);
    await user.type(amountInput, '1600000');

    await waitFor(() => {
      expect(screen.getByText(/≈/)).toHaveTextContent(/\$17\.78/);
    });
    expect(screen.queryByText(/as recorded/i)).not.toBeInTheDocument();
  });

  it('_PreviewMatchesTheSavedRow: the previewed figure is the one the payload carries', async () => {
    // Ties the two halves together. The payload's `amount` is the frontend's
    // live conversion ($16.67) and the server overrides it with the stored
    // $16.85 — so the preview agreeing with the payload would be the bug.
    // What must hold is that the preview shows the value the ROW keeps, while
    // original_amount / original_currency go out unchanged, which is the exact
    // condition the server freezes on.
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeRepricedRow(), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    await screen.findByRole('button', { name: /currency: lbp/i });

    expect(await screen.findByText(/≈/)).toHaveTextContent(/\$16\.85/);

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledTimes(1);
    });
    expect(onUpdate.mock.calls[0][0]).toMatchObject({
      original_amount: 1500000,
      original_currency: 'LBP',
    });
  });
});

// The desktop half of the refund round-trip. The phone Sheet has the same
// four quadrants; both run on `useTransactionEditForm`, and the point of
// testing both is that the shared hook is reached through two different
// layouts — the sign toggle has to be MOUNTED in each, which is a per-surface
// fact the hook's own contract cannot enforce.
describe('TransactionRow — a stored refund round-trips', () => {
  async function openEditor(description = 'Weekly groceries') {
    const user = await openActionsMenu(description);
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    return user;
  }

  it('_OpensWithTheToggleOnAndPositiveDigits: the amount box never shows a minus', async () => {
    renderRow(makeTx({ amount: -20 }));
    await openEditor();

    expect(await screen.findByRole('spinbutton')).toHaveValue(20);
    expect(screen.getByRole('switch', { name: 'Refund' })).toBeChecked();
  });

  it('_UnrelatedEditKeepsTheSign: editing the tags does not un-refund the row', async () => {
    const onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockResolvedValue(undefined);
    renderRow(makeTx({ amount: -20, tags: 'groceries' }), onUpdate);
    await openEditor();

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1, amount: -20 }),
    );
  });

  it('_TurningItOffFlipsTheRowPositive: and the toggle is what does it', async () => {
    const onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockResolvedValue(undefined);
    renderRow(makeTx({ amount: -20 }), onUpdate);
    const user = await openEditor();

    await user.click(screen.getByRole('switch', { name: 'Refund' }));
    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1, amount: 20 }),
    );
  });

  it('_TurningItOnMakesARefund: an ordinary row can become one', async () => {
    const onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openEditor();

    await user.click(screen.getByRole('switch', { name: 'Refund' }));
    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1, amount: -25.5 }),
    );
  });

  it('_ClearsTheAmountBoxByMoreThanTheTapBand: the two targets do not overlap', async () => {
    // On a coarse pointer the Switch grows its hit area with a pseudo-element
    // reaching 10px past its border box on every side (ui/switch.tsx), so the
    // gap above it has to be MORE than 10px or the top of that band lands
    // inside the amount input — a tap at the end of the number toggles the
    // sign instead. This shipped at `mt-2` (8px). Neither figure is
    // observable in happy-dom, which lays nothing out, so the class is the
    // assertion and the derivation is the comment.
    renderRow(makeTx({ amount: -20 }));
    await openEditor();
    const root = (await screen.findByRole('switch', { name: 'Refund' }))
      .parentElement as HTMLElement;
    const tokens = root.className.split(/\s+/);
    expect(tokens).toContain('mt-3');
    expect(tokens).not.toContain('mt-2');
  });

  it('_CancelRestoresTheStoredSign: a discarded edit leaves the toggle where the row had it', async () => {
    // `reset()` re-seeds every field from the row, and the sign is a field
    // now. Missing it would leave the next Edit-open showing the toggle the
    // user flipped and abandoned — over an amount that never changed.
    renderRow(makeTx({ amount: -20 }));
    const user = await openEditor();

    await user.click(screen.getByRole('switch', { name: 'Refund' }));
    expect(screen.getByRole('switch', { name: 'Refund' })).not.toBeChecked();

    await user.click(screen.getByRole('button', { name: /cancel/i }));
    await openEditor();

    expect(
      await screen.findByRole('switch', { name: 'Refund' }),
    ).toBeChecked();
  });
});

// An emptied amount box is not a zero the user meant. `AmountCurrencyInput`
// maps '' to 0, the ledger cannot store a zero, and the server's answer —
// "amount must not be zero" — is the wire's vocabulary arriving at somebody who
// typed nothing at all. Both edit surfaces run the same hook, so the gate is
// tested here and mirrored on the Sheet.
describe('TransactionRow — an emptied amount box', () => {
  async function openEditor(description = 'Weekly groceries') {
    const user = await openActionsMenu(description);
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    return user;
  }

  it('_EmptyAmountIsRefusedHere: nothing is sent and the field says why', async () => {
    const onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openEditor();

    await user.clear(await screen.findByRole('spinbutton'));
    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    expect(await screen.findByText('Enter an amount')).toBeInTheDocument();
    // The point of the gate: a round trip could not have changed the answer,
    // so it is not taken.
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('_TheMessageRetiresItself: a figure clears it and the same Save then lands', async () => {
    // The message is derived from the latch AND the live value, so it goes as
    // soon as the box is valid — no second rejected save to make it disappear.
    // It also must not stick to the control: Save stays enabled throughout,
    // because an empty box is three keystrokes from being right.
    const onUpdate = vi
      .fn<TransactionRowProps['onUpdate']>()
      .mockResolvedValue(undefined);
    renderRow(makeTx({ amount: 25.5 }), onUpdate);
    const user = await openEditor();

    const amount = await screen.findByRole('spinbutton');
    await user.clear(amount);
    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);
    expect(await screen.findByText('Enter an amount')).toBeInTheDocument();

    await user.type(amount, '5');
    await waitFor(() =>
      expect(screen.queryByText('Enter an amount')).not.toBeInTheDocument(),
    );

    fireEvent.submit(form);
    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1, amount: 5 }),
    );
  });
});
