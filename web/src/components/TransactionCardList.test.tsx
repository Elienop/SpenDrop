import { render, screen, fireEvent, within } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { format } from 'date-fns';
import {
  TransactionCardList,
  type TransactionCardListProps,
} from './TransactionCardList';
import { groupIntoDayRuns } from '@/lib/transaction-day-groups';
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

/** Two days, three rows, date-ordered — the shape the list is grouped under. */
const twoDays: Transaction[] = [
  makeTx({ id: 1, date: '2026-04-02', description: 'Coffee' }),
  makeTx({ id: 2, date: '2026-04-02', description: 'Bus fare' }),
  makeTx({ id: 3, date: '2026-04-01', description: 'Weekly groceries' }),
];

// Day labels are derived with the same expression the component uses rather
// than hardcoded, so the test does not fail in a timezone where a UTC-midnight
// date string renders as the previous day.
function dayLabel(iso: string): string {
  return format(new Date(iso), 'MMM d, yyyy');
}

function renderList(props: Partial<TransactionCardListProps> = {}) {
  return render(
    <TransactionCardList
      transactions={twoDays}
      baseCode="USD"
      selectedIds={new Set()}
      selectionScope="page"
      selectionMode={false}
      onSelect={vi.fn()}
      onOpen={vi.fn()}
      onLongPress={vi.fn()}
      groupByDay={true}
      {...props}
    />,
  );
}

describe('groupIntoDayRuns', () => {
  it('collects consecutive rows of the same date', () => {
    const groups = groupIntoDayRuns(twoDays);
    expect(groups.map((g) => g.date)).toEqual(['2026-04-02', '2026-04-01']);
    expect(groups[0].items.map((t) => t.id)).toEqual([1, 2]);
    expect(groups[1].items.map((t) => t.id)).toEqual([3]);
  });

  // A Map keyed by date would pull the two separated 04-02 rows under one
  // header and silently reorder the page to do it. A run-length split can
  // only ever mislabel a boundary that is actually there.
  it('does not reorder rows to merge separated runs of one date', () => {
    const interleaved = [
      makeTx({ id: 1, date: '2026-04-02' }),
      makeTx({ id: 2, date: '2026-04-01' }),
      makeTx({ id: 3, date: '2026-04-02' }),
    ];
    const groups = groupIntoDayRuns(interleaved);
    expect(groups).toHaveLength(3);
    expect(groups.flatMap((g) => g.items.map((t) => t.id))).toEqual([1, 2, 3]);
  });

  it('returns nothing for an empty page', () => {
    expect(groupIntoDayRuns([])).toEqual([]);
  });
});

describe('TransactionCardList day headers', () => {
  it('groups the page under one sticky header per day', () => {
    renderList();

    const headings = screen.getAllByRole('heading');
    expect(headings.map((h) => h.textContent)).toEqual([
      dayLabel('2026-04-02'),
      dayLabel('2026-04-01'),
    ]);

    // The header parks under MobileNav's h-14 bar rather than sliding beneath
    // it. `sticky` is inert unless an ancestor scrolls, which is why the page
    // drops the card's overflow-hidden on this branch.
    expect(headings[0]).toHaveClass('sticky', 'top-14', 'z-10');
  });

  it('puts each row under its own day', () => {
    renderList();
    const [firstDay, secondDay] = screen.getAllByRole('list');
    expect(within(firstDay).getByText('Coffee')).toBeInTheDocument();
    expect(within(firstDay).getByText('Bus fare')).toBeInTheDocument();
    expect(
      within(secondDay).getByText('Weekly groceries'),
    ).toBeInTheDocument();
  });

  it('drops the per-card date, which the header now carries', () => {
    renderList();
    expect(
      screen.queryAllByText(format(new Date('2026-04-02'), 'MMM d, yyyy')),
    ).toHaveLength(1); // the header itself, and no card repeating it
  });
});

describe('TransactionCardList day-header identity', () => {
  // code-I1. `groupIntoDayRuns` is run-length BY DESIGN, so it can legally
  // return two groups carrying the same date — keying sections and heading ids
  // on the date alone made React collide them and produced two elements
  // sharing one DOM id. Unreachable through today's caller (grouping is gated
  // on a date sort) is not the same as impossible, and the helper's contract
  // is what this component has to hold to.
  it('keeps keys and ids unique when a date legitimately repeats', () => {
    renderList({
      transactions: [
        makeTx({ id: 1, date: '2026-04-02', description: 'First' }),
        makeTx({ id: 2, date: '2026-04-01', description: 'Middle' }),
        makeTx({ id: 3, date: '2026-04-02', description: 'Last' }),
      ],
      groupByDay: true,
    });

    const headings = screen.getAllByRole('heading');
    expect(headings).toHaveLength(3);

    const ids = headings.map((h) => h.id);
    expect(new Set(ids).size).toBe(3);
    // Each list is still labelled by its OWN heading — a duplicate id would
    // point both runs of 04-02 at whichever element won.
    const lists = screen.getAllByRole('list');
    expect(lists.map((l) => l.getAttribute('aria-labelledby'))).toEqual(ids);
  });
});

describe('TransactionCardList list semantics', () => {
  // Tailwind's preflight strips list-style, and Safari/VoiceOver drop the list
  // role with the marker — so the rows stop being announced as a list at all.
  it('declares an explicit labelled list role on both branches', () => {
    const { unmount } = renderList({ groupByDay: false });
    const flat = screen.getByRole('list');
    expect(flat.tagName).toBe('UL');
    expect(flat).toHaveAttribute('role', 'list');
    expect(flat).toHaveAccessibleName('Transactions');
    unmount();

    renderList({ groupByDay: true });
    for (const list of screen.getAllByRole('list')) {
      expect(list).toHaveAttribute('role', 'list');
      // Grouped runs take their name from the day heading above them.
      expect(list).toHaveAccessibleName(/\w/);
    }
  });
});

describe('TransactionCardList without day grouping', () => {
  // Sorted by amount or description, consecutive rows have unrelated dates, so
  // day headers would degenerate into one sticky header per card. The list
  // goes flat and the date moves onto the cards, so it stays reachable in
  // every sort order rather than only in one.
  it('renders one flat list and puts the date back on every card', () => {
    renderList({ groupByDay: false });

    expect(screen.queryByRole('heading')).not.toBeInTheDocument();
    expect(screen.getAllByRole('list')).toHaveLength(1);
    // Year included — sorted by amount, "Apr 3" beside "Apr 3" from another
    // year would be a wrong answer rather than a terse one.
    expect(
      screen.getAllByText(format(new Date('2026-04-02'), 'MMM d, yyyy')),
    ).toHaveLength(2);
    expect(
      screen.getByText(format(new Date('2026-04-01'), 'MMM d, yyyy')),
    ).toBeInTheDocument();
  });
});

describe('TransactionCardList selection contract', () => {
  it('marks selected rows from selectedIds', () => {
    renderList({ selectedIds: new Set([1]), selectionMode: true });
    expect(
      screen.getByRole('checkbox', { name: 'Select Coffee' }),
    ).toBeChecked();
    expect(
      screen.getByRole('checkbox', { name: 'Select Bus fare' }),
    ).not.toBeChecked();
  });

  // The same contract the table rows carry: in all-matching scope the page
  // withholds onSelect, which locks every card. Toggling one row cannot
  // express "all rows except this one", and a silent demotion to page scope
  // would look like the selection had cleared.
  it('locks every card while the scope is all-matching', () => {
    const onSelect = vi.fn();
    const onOpen = vi.fn();
    renderList({
      selectionScope: 'all-matching',
      selectionMode: true,
      onSelect,
      onOpen,
    });

    for (const name of ['Coffee', 'Bus fare', 'Weekly groceries']) {
      const box = screen.getByRole('checkbox', { name: `Select ${name}` });
      expect(box).toBeChecked();
      expect(box).toBeDisabled();
    }

    fireEvent.click(screen.getByRole('button', { name: /coffee/i }));
    expect(onSelect).not.toHaveBeenCalled();
    expect(onOpen).not.toHaveBeenCalled();
  });

  it('withholds the long press once selection mode is on', () => {
    const onLongPress = vi.fn();
    vi.useFakeTimers();
    try {
      renderList({ selectionMode: true, onLongPress });
      fireEvent.pointerDown(screen.getByRole('button', { name: /coffee/i }), {
        clientX: 5,
        clientY: 5,
      });
      vi.advanceTimersByTime(1000);
      expect(onLongPress).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('passes the long press through outside selection mode', () => {
    const onLongPress = vi.fn();
    vi.useFakeTimers();
    try {
      renderList({ selectionMode: false, onLongPress });
      fireEvent.pointerDown(screen.getByRole('button', { name: /coffee/i }), {
        clientX: 5,
        clientY: 5,
      });
      vi.advanceTimersByTime(1000);
      expect(onLongPress).toHaveBeenCalledWith(
        expect.objectContaining({ id: 1 }),
      );
    } finally {
      vi.useRealTimers();
    }
  });
});
