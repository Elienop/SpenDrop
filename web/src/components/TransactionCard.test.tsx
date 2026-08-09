import { render, screen, fireEvent, within, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { TransactionCard, type TransactionCardProps } from './TransactionCard';
import type { Transaction } from '../api/types';

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

function renderCard(props: Partial<TransactionCardProps> = {}) {
  const transaction = props.transaction ?? makeTx();
  const result = render(
    <ul>
      <TransactionCard
        transaction={transaction}
        baseCode="USD"
        selected={false}
        selectionMode={false}
        onSelect={vi.fn()}
        onOpen={vi.fn()}
        onLongPress={vi.fn()}
        {...props}
      />
    </ul>,
  );
  return { ...result, transaction };
}

/** The whole-row tap target. Deliberately found by role rather than by test
 *  id, so the test fails if the row stops being a real button. */
function rowButton(): HTMLElement {
  return screen.getByRole('button', { name: /weekly groceries/i });
}

describe('TransactionCard anatomy', () => {
  it('stacks description, category + tags, and the creator on one card', () => {
    renderCard({ transaction: makeTx({ tags: 'food,weekly' }) });

    const description = screen.getByText('Weekly groceries');
    expect(description).toHaveClass('truncate', 'font-medium');
    // The bound is not decorative: import bypasses the 500-char description
    // limit, so `truncate` is what stops one spreadsheet row stretching every
    // card. `title` keeps the full text reachable.
    expect(description).toHaveAttribute('title', 'Weekly groceries');

    expect(screen.getByText('Groceries')).toBeInTheDocument();
    expect(screen.getByText('food')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
    expect(screen.getByTestId('amount-display')).toHaveTextContent('-$25.50');
  });

  // The creator line is a member's only warning that a row is her spouse's
  // BEFORE she edits it and gets a 403, so it has to survive the collapse to a
  // card intact — not move into the edit sheet behind the action it warns
  // about. Pinned element-by-element rather than by a substring class check.
  it('carries the creator idiom in the same shape the table row uses', () => {
    renderCard();

    const label = screen.getByText('Entered by');
    expect(label).toHaveClass('sr-only');

    const line = label.closest('p');
    expect(line).not.toBeNull();
    expect(line).toHaveAttribute(
      'class',
      'mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground',
    );
    expect(line).toHaveTextContent('Entered by Elie');

    // The name truncates independently of the icon, which stays decoration.
    expect(label.parentElement).toHaveAttribute('class', 'min-w-0 truncate');
    const icon = line!.querySelector('svg');
    expect(icon).toHaveAttribute('aria-hidden', 'true');
    expect(icon).toHaveClass('size-3.5', 'shrink-0');
  });

  it('names a deleted account holder rather than showing a blank line', () => {
    renderCard({ transaction: makeTx({ created_by: '' }) });
    expect(screen.getByText('Entered by').parentElement).toHaveTextContent(
      'Entered by Unknown',
    );
  });

  it('renders the dual-currency amount block unchanged', () => {
    renderCard({
      transaction: makeTx({
        amount: 16.85,
        original_amount: 1500000,
        original_currency: 'LBP',
      }),
    });
    expect(screen.getByTestId('amount-display')).toHaveTextContent('-$16.85');
    expect(screen.getByTestId('amount-display-secondary')).toHaveTextContent(
      '1,500,000.00 LBP',
    );
  });

  it('gives the whole row a touch-sized tap target that cannot be text-selected', () => {
    renderCard();
    // min-h-14 is 56px — the row-height floor — and comfortably over the 44px
    // touch minimum. select-none stops a long press starting a text selection
    // on top of the gesture.
    expect(rowButton()).toHaveClass('min-h-14', 'select-none', 'min-w-0');
  });

  it('shows the date inline only when the list is not grouping by day', () => {
    // With the year: this branch runs only when the list is NOT grouped by
    // day, which is exactly when neighbouring rows can be years apart.
    // The literal, not `format(new Date('2026-04-01'), …)`: deriving it the
    // way the component derives it made this assertion agree with the
    // UTC-midnight off-by-one instead of catching it.
    const expected = 'Apr 1, 2026';

    const { unmount } = renderCard({ showDate: true });
    expect(screen.getByText(expected)).toBeInTheDocument();
    unmount();

    renderCard({ showDate: false });
    expect(screen.queryByText(expected)).not.toBeInTheDocument();
  });
});

describe('TransactionCard selection', () => {
  // UX-B2. The size-11 wrapper is layout only — a box around a control
  // forwards no clicks — so the ~14px ring around the 16px checkbox swallowed
  // taps. The pseudo-element grows the checkbox's OWN hit area to fill it.
  it('grows the checkbox hit area to the touch floor without moving it', () => {
    renderCard({ selectionMode: true });
    const box = screen.getByRole('checkbox', {
      name: 'Select Weekly groceries',
    });
    expect(box).toHaveClass(
      'relative',
      'before:absolute',
      'before:-inset-3.5',
    );
  });

  it('has no checkbox until selection mode is on', () => {
    const { unmount } = renderCard({ selectionMode: false });
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(rowButton()).not.toHaveAttribute('aria-pressed');
    unmount();

    renderCard({ selectionMode: true });
    expect(
      screen.getByRole('checkbox', { name: 'Select Weekly groceries' }),
    ).toBeInTheDocument();
    expect(rowButton()).toHaveAttribute('aria-pressed', 'false');
  });

  it('tapping the row opens the editor outside selection mode', () => {
    const onOpen = vi.fn();
    const onSelect = vi.fn();
    const { transaction } = renderCard({ onOpen, onSelect });

    fireEvent.click(rowButton());

    expect(onOpen).toHaveBeenCalledWith(transaction);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('tapping the row toggles selection while in selection mode', () => {
    const onOpen = vi.fn();
    const onSelect = vi.fn();
    renderCard({ selectionMode: true, selected: false, onOpen, onSelect });

    fireEvent.click(rowButton());

    expect(onSelect).toHaveBeenCalledWith(1, true);
    expect(onOpen).not.toHaveBeenCalled();
  });

  it('untoggles an already-selected row', () => {
    const onSelect = vi.fn();
    renderCard({ selectionMode: true, selected: true, onSelect });
    fireEvent.click(rowButton());
    expect(onSelect).toHaveBeenCalledWith(1, false);
  });

  // The all-matching contract, card side. The page passes no onSelect while
  // selectionScope is 'all-matching' — exactly as it does for table rows —
  // and BOTH the checkbox and the whole-row tap have to go inert. A locked
  // checkbox beside a live row tap would silently demote the scope.
  it('locks both the checkbox and the row tap when onSelect is withheld', () => {
    const onOpen = vi.fn();
    renderCard({
      selectionMode: true,
      selected: true,
      onSelect: undefined,
      onOpen,
    });

    expect(
      screen.getByRole('checkbox', { name: 'Select Weekly groceries' }),
    ).toBeDisabled();

    fireEvent.click(rowButton());
    expect(onOpen).not.toHaveBeenCalled();
  });
});

describe('TransactionCard long press', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  function press(el: HTMLElement, init: Record<string, unknown> = {}) {
    fireEvent.pointerDown(el, { clientX: 10, clientY: 10, ...init });
  }

  it('entering selection mode takes a sustained press', () => {
    const onLongPress = vi.fn();
    const { transaction } = renderCard({ onLongPress });
    const row = rowButton();

    press(row);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(onLongPress).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(150);
    });
    expect(onLongPress).toHaveBeenCalledWith(transaction);
  });

  // A press that turns into a flick is the user scrolling the list, not
  // reaching for selection mode. Without the movement cancel, scrolling a
  // long list starts selecting rows out from under the scroll.
  it('a press that travels into a scroll does not fire', () => {
    const onLongPress = vi.fn();
    renderCard({ onLongPress });
    const row = rowButton();

    press(row);
    fireEvent.pointerMove(row, { clientX: 10, clientY: 60 });
    act(() => {
      vi.advanceTimersByTime(1000);
    });

    expect(onLongPress).not.toHaveBeenCalled();
  });

  it('a small tremor within tolerance still fires', () => {
    const onLongPress = vi.fn();
    renderCard({ onLongPress });
    const row = rowButton();

    press(row);
    fireEvent.pointerMove(row, { clientX: 13, clientY: 14 });
    act(() => {
      vi.advanceTimersByTime(600);
    });

    expect(onLongPress).toHaveBeenCalledTimes(1);
  });

  it('lifting the finger early cancels', () => {
    const onLongPress = vi.fn();
    renderCard({ onLongPress });
    const row = rowButton();

    press(row);
    fireEvent.pointerUp(row);
    act(() => {
      vi.advanceTimersByTime(1000);
    });

    expect(onLongPress).not.toHaveBeenCalled();
  });

  // The click that ends a long press must not also act on the row: without
  // the suppression the gesture would select the card and then immediately
  // open its edit sheet on top of the selection it just made.
  it('the click ending the press does not also open the editor', () => {
    const onLongPress = vi.fn();
    const onOpen = vi.fn();
    renderCard({ onLongPress, onOpen });
    const row = rowButton();

    press(row);
    act(() => {
      vi.advanceTimersByTime(600);
    });
    fireEvent.click(row);

    expect(onLongPress).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();

    // ...and the NEXT, separate tap works normally again.
    fireEvent.click(row);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  // code-M1. Entering selection mode is exactly what makes the parent stop
  // passing `onLongPress`, so the next press on this card takes the early
  // return. With the flag reset BELOW that return it stayed true and swallowed
  // the following tap — which in selection mode is the tap that selects.
  it('does not eat the next tap after the gesture that entered selection mode', () => {
    const onSelect = vi.fn();
    const { rerender } = renderCard({ onLongPress: vi.fn() });
    const row = rowButton();

    press(row);
    act(() => {
      vi.advanceTimersByTime(600);
    });

    // The parent flips into selection mode and withholds onLongPress.
    rerender(
      <ul>
        <TransactionCard
          transaction={makeTx()}
          baseCode="USD"
          selected={false}
          selectionMode={true}
          onSelect={onSelect}
          onOpen={vi.fn()}
          onLongPress={undefined}
        />
      </ul>,
    );

    press(row);
    fireEvent.click(rowButton());
    expect(onSelect).toHaveBeenCalledWith(1, true);
  });

  // M4. Android raises contextmenu at the end of a long press, which would
  // drop a text-selection menu on top of the selection the gesture just made.
  // Suppressed for TOUCH only — right-click must stay normal for a mouse at a
  // narrow window width.
  it('suppresses the Android long-press context menu, but only for touch', () => {
    renderCard({ onLongPress: vi.fn() });
    const row = rowButton();

    press(row, { pointerType: 'touch' });
    const touchMenu = fireEvent.contextMenu(row);
    // fireEvent returns false when a handler called preventDefault.
    expect(touchMenu).toBe(false);

    press(row, { pointerType: 'mouse' });
    const mouseMenu = fireEvent.contextMenu(row);
    expect(mouseMenu).toBe(true);
  });

  it('a slow mouse click stays an ordinary click', () => {
    const onLongPress = vi.fn();
    renderCard({ onLongPress });
    const row = rowButton();

    press(row, { pointerType: 'mouse' });
    act(() => {
      vi.advanceTimersByTime(1000);
    });

    expect(onLongPress).not.toHaveBeenCalled();
  });

  it('offers no long press once selection mode is already on', () => {
    const onLongPress = vi.fn();
    renderCard({ selectionMode: true, onLongPress: undefined });
    const row = rowButton();

    press(row);
    act(() => {
      vi.advanceTimersByTime(1000);
    });

    expect(onLongPress).not.toHaveBeenCalled();
  });
});

describe('TransactionCard empty description', () => {
  // UX-P3. Import bypasses the description checks, so a row can carry an empty
  // one — `Select ` then names no object, and the visible line is blank.
  it('names the row rather than announcing "Select "', () => {
    renderCard({
      transaction: makeTx({ description: '' }),
      selectionMode: true,
    });
    expect(
      screen.getByRole('checkbox', { name: 'Select (no description)' }),
    ).toBeInTheDocument();
    // The accessible name CONTAINS the visible text (WCAG 2.5.3) because the
    // fallback is the same string the row renders.
    expect(screen.getByText('(no description)')).toBeInTheDocument();
  });
});

describe('TransactionCard tags', () => {
  it('drops empty segments rather than rendering blank pills', () => {
    renderCard({ transaction: makeTx({ tags: 'food, ,weekly,' }) });
    const row = rowButton();
    // The metadata line holds the category badge and exactly two tag pills —
    // the stray whitespace-only and trailing segments contribute nothing. A
    // child count is the assertion, because an empty pill renders no text to
    // query for.
    const metadataLine = within(row).getByText('Groceries').parentElement;
    expect(metadataLine).not.toBeNull();
    expect(metadataLine!.children).toHaveLength(3);
    expect(metadataLine).toHaveTextContent('Groceriesfoodweekly');
  });
});
