import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { subDays } from 'date-fns';
import { TOUCH_TARGET_CHECKBOX } from '@/lib/touch-target';
import { transactionLabel } from '@/lib/transaction-label';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------
//
// `useAuth` and `api` are the two non-trivial collaborators the Trash page
// talks to. `useBaseCurrency` normally hits the `currencies` endpoint; we pin
// it so the amount cell has a stable display and no extra network call has to
// be accounted for in the `api.get` call counters below. `sonner` is mocked to
// keep toast side effects out of jsdom — the toast provider isn't mounted in
// the test tree.

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

vi.mock('@/hooks/useBaseCurrency', () => ({
  useBaseCurrency: () => 'USD',
}));

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import type { DeletedTransactionList } from '../api/types';
import { toast } from 'sonner';
import { Trash } from './Trash';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// Typed against the wire contract on purpose: an untyped object literal
// would let a newly-required field (`created_by` was exactly that) go
// missing here while the fixture still compiled and the page rendered a
// blank cell. The compile error IS the guard.
//
// An admin's trash spans the household, so the two rows have two different
// creators — Alice (the admin herself, per `asAdmin`) and Bob (the member).
// A render that named the logged-in user instead of the row's creator would
// satisfy row 101 and fail row 102.
const defaultDeletedList: DeletedTransactionList = {
  transactions: [
    {
      id: 101,
      user_id: 1,
      created_by: 'Alice',
      date: '2026-04-10',
      amount: 25.5,
      original_amount: null,
      original_currency: null,
      description: 'Weekly groceries',
      category_id: 1,
      category_name: 'Food',
      category_type: 'expense' as const,
      tags: null,
      notes: null,
      created_at: '2026-04-10T00:00:00Z',
      updated_at: '2026-04-10T00:00:00Z',
      deleted_at: '2026-04-13T12:00:00Z',
    },
    {
      id: 102,
      user_id: 2,
      created_by: 'Bob',
      date: '2026-04-05',
      amount: 2500,
      original_amount: null,
      original_currency: null,
      description: 'April salary',
      category_id: 2,
      category_name: 'Salary',
      category_type: 'income' as const,
      tags: null,
      notes: null,
      created_at: '2026-04-05T00:00:00Z',
      updated_at: '2026-04-05T00:00:00Z',
      deleted_at: '2026-04-13T10:00:00Z',
    },
  ],
  total: 2,
  page: 1,
  per_page: 20,
};

// A member's trash is scoped by the backend to rows they created
// (ListDeletedTransactionsByUser), so every row a member sees is their own.
const memberOwnDeletedList: DeletedTransactionList = {
  transactions: [
    {
      ...defaultDeletedList.transactions[0],
      id: 201,
      user_id: 2,
      created_by: 'Bob',
      description: 'Bus pass',
    },
  ],
  total: 1,
  page: 1,
  per_page: 20,
};

// `created_by: ''` is the wire's documented "the creator's user row is
// gone" value — the LEFT JOIN in ListDeletedTransactions found nothing,
// e.g. after a restored backup dropped the user. It is the ONLY case the
// 'Unknown' fallback exists for; the key itself is always present.
const orphanedCreatorDeletedList: DeletedTransactionList = {
  transactions: [
    {
      ...defaultDeletedList.transactions[0],
      id: 301,
      created_by: '',
      description: 'Row from a restored backup',
    },
  ],
  total: 1,
  page: 1,
  per_page: 20,
};

function renderTrash() {
  return render(
    <MemoryRouter initialEntries={['/trash']}>
      <Trash />
    </MemoryRouter>,
  );
}

// ---------------------------------------------------------------------------
// Viewport control
// ---------------------------------------------------------------------------

interface HappyDomWindow {
  happyDOM?: {
    setViewport: (viewport: { width?: number; height?: number }) => void;
  };
}

/** happy-dom's default, and comfortably above Tailwind's `md`. */
const DESKTOP_WIDTH = 1024;
/** iPhone 12/13/14 logical width — the narrowest viewport B9 targets. */
const PHONE_WIDTH = 390;

/**
 * Drive the real viewport rather than stubbing `window.matchMedia`.
 *
 * happy-dom evaluates media queries against `window.innerWidth`, so what
 * runs here is the real gate: `useIsMobileViewport` NEGATES MobileNav's
 * `DESKTOP_QUERY` — `(min-width: 768px)` — rather than mirroring it as a
 * second `max-width` string. (A mirror has to pick a number just below
 * 768 and leaves a fractional width matching neither query; the hook
 * rejects that shape deliberately, so do not go looking for a
 * `max-width` here.) A hand-rolled matchMedia stub — the idiom
 * MobileNav.test.tsx uses, for a different job — returns a fixed
 * `matches` whatever it is asked, and would keep every test below green
 * even if the gate were inverted or sat on the wrong side of the
 * breakpoint.
 */
function setViewportWidth(width: number): void {
  const controllable = window as unknown as HappyDomWindow;
  if (!controllable.happyDOM) {
    // Loud on purpose. Silently falling back would run every mobile test
    // at desktop width, where the card list does not exist and the
    // assertions would be reaching for a table.
    throw new Error(
      'happy-dom viewport control is unavailable — mobile tests cannot run',
    );
  }
  controllable.happyDOM.setViewport({ width });
}

// ---------------------------------------------------------------------------
// Class-token helpers
// ---------------------------------------------------------------------------

/** Class tokens of an element. Exact tokens, never substrings. */
function classes(el: Element): string[] {
  return el.className.split(/\s+/).filter(Boolean);
}

/** The numeric part of the first class token matching `re`, or null. */
function tokenSteps(el: Element, re: RegExp): number | null {
  for (const token of classes(el)) {
    const m = re.exec(token);
    if (m) return Number(m[1]);
  }
  return null;
}

/** Tailwind spacing steps to px — 0.25rem steps against a 16px root. */
function spacingPx(steps: number): number {
  return steps * 4;
}

/**
 * The vertical touch floor of a control in px, derived from its own class
 * tokens. happy-dom runs no layout, so the pixels have to come from the
 * classes — but deriving them beats asserting a literal token: shrink the
 * control and this fails with the size it computed, rather than passing
 * because some other token still matched.
 *
 * `min-h-*` is authoritative when present: CSS clamps a fixed `height` up
 * to `min-height`, which is why shadcn Button's own `h-9` can sit
 * alongside `min-h-11` and the control still renders 44px.
 */
function touchFloorPx(el: Element): number {
  const minH = tokenSteps(el, /^min-h-([\d.]+)$/);
  if (minH !== null) return spacingPx(minH);
  const fixed = tokenSteps(el, /^(?:size|h)-([\d.]+)$/);
  if (fixed === null) {
    throw new Error(`no height token on: ${el.getAttribute('class')}`);
  }
  return spacingPx(fixed);
}

/**
 * The square tap area of a Checkbox in px: its own 16px box plus a
 * `before:-inset-N` pseudo-element on both sides. Derived for the same
 * reason as `touchFloorPx` — halve the inset and this reports 30, not a
 * token mismatch.
 */
function checkboxTapAreaPx(el: Element): number {
  const box = tokenSteps(el, /^h-([\d.]+)$/);
  if (box === null) {
    throw new Error(`no box token on checkbox: ${el.getAttribute('class')}`);
  }
  const inset = tokenSteps(el, /^before:-inset-([\d.]+)$/);
  return spacingPx(box) + (inset === null ? 0 : 2 * spacingPx(inset));
}

/**
 * Render at phone width and hand back the card list, having first proved
 * the swap actually happened.
 *
 * POSITIVE CONTROL, and it earns its keep. Most of what the mobile block
 * asserts — a Restore button exists, a checkbox selects a row, "(no
 * description)" renders, there is a select-all — is equally true of the
 * TABLE. So if `setViewportWidth` ever silently stops taking, the hook
 * stays false, the desktop tree renders, and those tests keep passing
 * while asserting nothing whatsoever about a card. That is not
 * hypothetical: stubbing the `setViewport` call out left FIVE of them
 * green. Asserting the table is ABSENT is what makes the block die loudly
 * instead, and it is why every mobile test starts here rather than
 * calling `renderTrash` directly.
 */
async function renderCardList(): Promise<HTMLElement> {
  renderTrash();
  const list = await screen.findByRole('list', {
    name: /deleted transactions/i,
  });
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  return list;
}

/**
 * A single row whose tombstone is dated N days back from the wall clock.
 *
 * Relative to now, because `formatDistanceToNowStrict` renders against
 * `Date.now()` — the shared fixture's literal 2026-04-13 reads "4 months
 * ago" today and something else next quarter. And via `subDays` rather
 * than `n * 86_400_000`, because the production gate measures with
 * `differenceInCalendarDays`: a DST boundary inside the window would put
 * a fixed-millisecond fixture on the wrong side of the threshold, in one
 * hemisphere, twice a year.
 */
function deletedDaysAgoList(days: number): DeletedTransactionList {
  return {
    ...defaultDeletedList,
    transactions: [
      {
        ...defaultDeletedList.transactions[0],
        deleted_at: subDays(new Date(), days).toISOString(),
      },
    ],
    total: 1,
  };
}

function asAdmin() {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    },
    loading: false,
    unverified: false,
    refreshUser: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

function asMember() {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 2,
      username: 'bob',
      display_name: 'Bob',
      role: 'member',
      created_at: '2024-01-01',
    },
    loading: false,
    unverified: false,
    refreshUser: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

function asLoading() {
  mockedUseAuth.mockReturnValue({
    user: null,
    loading: true,
    unverified: false,
    refreshUser: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('Trash', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Default: a non-empty paginated response. Tests that want empty / error
    // states override with `.mockResolvedValueOnce` / `.mockRejectedValueOnce`.
    mockedApi.get.mockResolvedValue(defaultDeletedList);
  });

  // -------------------------------------------------------------------------
  // As admin — the happy path for the recovery surface
  // -------------------------------------------------------------------------
  describe('as admin', () => {
    beforeEach(asAdmin);

    test('renders the Trash heading and explainer', () => {
      renderTrash();
      expect(
        screen.getByRole('heading', { level: 1, name: /trash/i }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(/recently deleted transactions/i),
      ).toBeInTheDocument();
    });

    test('fetches the first page on mount with default pagination', async () => {
      renderTrash();
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          'transactions/deleted?page=1&per_page=20',
        );
      });
    });

    test('renders deleted transaction rows from the API response', async () => {
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });
      expect(screen.getByText('April salary')).toBeInTheDocument();
      // Category column — from CategoryBadge
      expect(screen.getByText('Food')).toBeInTheDocument();
      expect(screen.getByText('Salary')).toBeInTheDocument();
    });

    test('each row names its own creator under the description', async () => {
      renderTrash();
      const groceriesRow = (
        await screen.findByText('Weekly groceries')
      ).closest('tr');
      const salaryRow = screen.getByText('April salary').closest('tr');
      expect(groceriesRow).not.toBeNull();
      expect(salaryRow).not.toBeNull();

      // Scoped per row rather than page-wide: a page-wide query would also
      // pass for a render that hard-codes the logged-in user (Alice, here)
      // or reuses the first row's creator for every row.
      expect(within(groceriesRow!).getByText('Alice')).toBeInTheDocument();
      expect(within(salaryRow!).getByText('Bob')).toBeInTheDocument();

      // The person icon is aria-hidden decoration, so the name carries no
      // meaning on its own to a screen reader. Assert the announced string,
      // not just the presence of the name.
      expect(within(salaryRow!).getByText('Bob').closest('p')).toHaveTextContent(
        'Entered by Bob',
      );
    });

    test('description cell is width-bounded and keeps the full text on hover', async () => {
      renderTrash();
      // Matches the inner description div: the cell itself no longer has a
      // direct text child, the div does.
      const description = await screen.findByText('Weekly groceries');

      // The hover fallback is the piece a "simplify this cell" edit drops
      // first, and it is the only route back to a long imported description
      // once the cell clips it.
      expect(description).toHaveAttribute('title', 'Weekly groceries');

      // Exact tokens, not substrings — `expect(className).toContain('truncate')`
      // also passes for `truncate-none` or any longer token containing it.
      // Both halves are load-bearing and neither works alone: max-w-md bounds
      // the cell, truncate clips inside it.
      expect(description.className.split(/\s+/)).toContain('truncate');
      expect(description.className.split(/\s+/)).toContain('font-medium');
      const cell = description.closest('td');
      expect(cell).not.toBeNull();
      expect(cell!.className.split(/\s+/)).toContain('max-w-md');
    });

    test('renders "Unknown" when the creator\'s user row is gone', async () => {
      mockedApi.get.mockResolvedValue(orphanedCreatorDeletedList);
      renderTrash();
      const row = (
        await screen.findByText('Row from a restored backup')
      ).closest('tr');
      expect(row).not.toBeNull();

      // The empty string must read as a neutral fallback, never as a blank
      // line the operator has to interpret.
      expect(within(row!).getByText('Unknown')).toBeInTheDocument();
      expect(
        within(row!).getByText('Unknown').closest('p'),
      ).toHaveTextContent('Entered by Unknown');
    });

    test('renders the empty state when the trash list is empty', async () => {
      mockedApi.get.mockResolvedValueOnce({
        transactions: [],
        total: 0,
        page: 1,
        per_page: 20,
      });
      renderTrash();
      expect(await screen.findByText(/trash is empty/i)).toBeInTheDocument();
    });

    test('surfaces load failure in an alert banner', async () => {
      mockedApi.get.mockRejectedValueOnce(new Error('boom'));
      renderTrash();
      const banner = await screen.findByRole('alert');
      expect(banner).toHaveTextContent('boom');
    });

    test('load failure does NOT also render the "Trash is empty" card', async () => {
      // Regression for the case where an error state and an empty rows
      // list stacked a contradictory "Trash is empty" card under the
      // destructive alert — the operator could not tell whether the
      // trash was really empty or the fetch had failed.
      mockedApi.get.mockRejectedValueOnce(new Error('boom'));
      renderTrash();
      await screen.findByRole('alert');
      expect(
        screen.queryByText(/trash is empty/i),
      ).not.toBeInTheDocument();
    });

    test('refetch that empties a page > 1 steps back one page automatically', async () => {
      // Regression: on a recovery surface, landing on a false empty-
      // state card after restoring the last row of page N>1 is a
      // dead-end. `fetchTrash` detects this and re-requests page N-1.
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({});

      // Page 1: full (20 rows, the first being our target on page 2 stub)
      const page1Rows = Array.from({ length: 20 }, (_, i) => ({
        ...defaultDeletedList.transactions[0],
        id: i + 1,
        description: `Row ${i + 1}`,
      }));
      // Page 2: a single row the user will restore
      const page2Rows = [
        {
          ...defaultDeletedList.transactions[0],
          id: 999,
          description: 'Last on page 2',
        },
      ];

      let page2Visits = 0;
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'transactions/deleted?page=1&per_page=20') {
          return Promise.resolve({
            transactions: page1Rows,
            total: 21,
            page: 1,
            per_page: 20,
          });
        }
        if (path === 'transactions/deleted?page=2&per_page=20') {
          page2Visits++;
          // First visit: the single row is still there.
          // Second visit (post-restore): page 2 is now empty, total=20.
          if (page2Visits === 1) {
            return Promise.resolve({
              transactions: page2Rows,
              total: 21,
              page: 2,
              per_page: 20,
            });
          }
          return Promise.resolve({
            transactions: [],
            total: 20,
            page: 2,
            per_page: 20,
          });
        }
        return Promise.resolve(defaultDeletedList);
      });

      renderTrash();

      // Wait for the initial page-1 load
      await waitFor(() => {
        expect(screen.getByText('Row 1')).toBeInTheDocument();
      });

      // Navigate to page 2 — the pager is rendered twice (top + bottom
      // of the card), so `getAllByRole` and click the first match.
      const nextButtons = screen.getAllByRole('button', {
        name: /go to next page/i,
      });
      await user.click(nextButtons[0]);
      await waitFor(() => {
        expect(screen.getByText('Last on page 2')).toBeInTheDocument();
      });

      // Restore the only row on page 2. After the refetch returns empty,
      // the step-back logic should re-request page 1.
      await user.click(
        screen.getByRole('button', { name: /restore last on page 2/i }),
      );

      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          'transactions/deleted?page=1&per_page=20',
        );
      });
      // And the page 1 data should be visible again — no dead-end
      // "Trash is empty" card.
      await waitFor(() => {
        expect(screen.getByText('Row 1')).toBeInTheDocument();
      });
      expect(screen.queryByText(/trash is empty/i)).not.toBeInTheDocument();
    });

    test('clicking Restore on a row POSTs to the restore endpoint and refetches', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({});
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /restore weekly groceries/i }),
      );

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith(
          'transactions/101/restore',
        );
      });
      // Initial load + refetch after restore = 2 gets
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledTimes(2);
      });
    });

    test('clicking Purge opens a confirm dialog without calling the delete endpoint', async () => {
      const user = userEvent.setup();
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /purge weekly groceries/i }),
      );

      // Dialog opens with the irreversible warning copy
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(
        screen.getByText(/permanently delete this transaction/i),
      ).toBeInTheDocument();
      // Delete must not fire until the user confirms
      expect(mockedApi.del).not.toHaveBeenCalled();
    });

    test('confirming purge DELETEs the row and refetches', async () => {
      const user = userEvent.setup();
      mockedApi.del.mockResolvedValue(undefined);
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /purge weekly groceries/i }),
      );
      await user.click(
        screen.getByRole('button', { name: /purge permanently/i }),
      );

      await waitFor(() => {
        expect(mockedApi.del).toHaveBeenCalledWith('transactions/101/purge');
      });
      // Initial load + refetch after purge = 2 gets
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledTimes(2);
      });
    });

    test('cancelling the purge dialog closes it without calling delete', async () => {
      const user = userEvent.setup();
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /purge weekly groceries/i }),
      );
      await user.click(screen.getByRole('button', { name: /^cancel$/i }));

      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
      expect(mockedApi.del).not.toHaveBeenCalled();
    });

    test('batch restore collects selected ids and POSTs restore-batch', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({ restored: 2 });
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      // Select both rows via the per-row checkboxes
      await user.click(
        screen.getByRole('checkbox', { name: /select weekly groceries/i }),
      );
      await user.click(
        screen.getByRole('checkbox', { name: /select april salary/i }),
      );

      // Selection toolbar should show the count
      expect(screen.getByText(/2 selected/i)).toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: /restore 2/i }));

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith(
          'transactions/restore-batch',
          { ids: [101, 102] },
        );
      });
    });

    test('batch restore with conflicted ids names both counts in the toast', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({ restored: 1, conflicted: 1 });
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('checkbox', { name: /select weekly groceries/i }),
      );
      await user.click(
        screen.getByRole('checkbox', { name: /select april salary/i }),
      );
      await user.click(screen.getByRole('button', { name: /restore 2/i }));

      await waitFor(() => {
        expect(mockedToast.success).toHaveBeenCalledWith(
          'Restored 1 — 1 could not be restored (a newer copy already exists)',
        );
      });
    });

    test('batch restore with skipped ids names both counts in the toast', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({ restored: 1, conflicted: 0, skipped: 1 });
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('checkbox', { name: /select weekly groceries/i }),
      );
      await user.click(
        screen.getByRole('checkbox', { name: /select april salary/i }),
      );
      await user.click(screen.getByRole('button', { name: /restore 2/i }));

      await waitFor(() => {
        expect(mockedToast.success).toHaveBeenCalledWith(
          'Restored 1 — 1 skipped (no longer in your trash)',
        );
      });
    });

    test('clicking the select-all checkbox selects every row on the page', async () => {
      const user = userEvent.setup();
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('checkbox', { name: /select all on this page/i }),
      );

      expect(screen.getByText(/2 selected/i)).toBeInTheDocument();
    });

    test('"Clear selection" empties the selection set', async () => {
      const user = userEvent.setup();
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('checkbox', { name: /select all on this page/i }),
      );
      expect(screen.getByText(/2 selected/i)).toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: /clear selection/i }));

      expect(screen.queryByText(/2 selected/i)).not.toBeInTheDocument();
    });

    // -----------------------------------------------------------------------
    // Whole-trash bulk actions — the header "Restore all" / "Purge all"
    // buttons. These fire against a different endpoint pair than the
    // per-page batch toolbar (which scopes to checkbox selection), so
    // both code paths need independent coverage.
    // -----------------------------------------------------------------------

    test('"Restore all" button shows the total count in its label', async () => {
      renderTrash();
      // Total in the default fixture is 2 — the button echoes it so the
      // operator sees the magnitude before clicking.
      expect(
        await screen.findByRole('button', { name: /restore all 2/i }),
      ).toBeInTheDocument();
    });

    test('"Purge all" button shows the total count in its label', async () => {
      renderTrash();
      expect(
        await screen.findByRole('button', { name: /purge all 2/i }),
      ).toBeInTheDocument();
    });

    test('bulk action buttons are hidden when the trash is empty', async () => {
      mockedApi.get.mockResolvedValueOnce({
        transactions: [],
        total: 0,
        page: 1,
        per_page: 20,
      });
      renderTrash();
      await screen.findByText(/trash is empty/i);
      // Neither header button renders when there's nothing to act on —
      // they'd just be confusing no-ops next to the empty-state card.
      expect(
        screen.queryByRole('button', { name: /restore all/i }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: /purge all/i }),
      ).not.toBeInTheDocument();
    });

    test('clicking "Restore all" POSTs to restore-all and refetches without a confirm dialog', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({ restored: 2 });
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /restore all 2/i }),
      );

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith('transactions/restore-all');
      });
      // No dialog should have been opened — restore is reversible and
      // fires directly, matching the single-row and batch-restore flows.
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      // Initial load + refetch after restore-all = 2 gets
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledTimes(2);
      });
    });

    test('"Restore all" with conflicted ids names both counts in the toast', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({ restored: 1, conflicted: 1 });
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /restore all 2/i }),
      );

      await waitFor(() => {
        expect(mockedToast.success).toHaveBeenCalledWith(
          'Restored 1 — 1 could not be restored (a newer copy already exists)',
        );
      });
    });

    test('clicking "Purge all" opens a confirm dialog without calling delete', async () => {
      const user = userEvent.setup();
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /purge all 2/i }),
      );

      // The dialog opens with explicit "everything in the trash" copy so
      // the operator can't confuse it with the single-row purge dialog.
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(
        screen.getByText(/permanently delete everything in the trash/i),
      ).toBeInTheDocument();
      // DELETE must not fire until the user confirms
      expect(mockedApi.del).not.toHaveBeenCalled();
    });

    test('confirming "Purge all" DELETEs trash and refetches', async () => {
      const user = userEvent.setup();
      mockedApi.del.mockResolvedValue({ purged: 2 });
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      // Open the dialog via the header trigger ("Purge all 2"), then
      // confirm via the dialog's distinct "Purge all permanently"
      // button. The two labels deliberately differ so screen readers
      // (and this test) can disambiguate between "open the dialog" and
      // "execute the destructive action".
      await user.click(
        screen.getByRole('button', { name: /purge all 2/i }),
      );
      await user.click(
        screen.getByRole('button', { name: /purge all permanently/i }),
      );

      await waitFor(() => {
        expect(mockedApi.del).toHaveBeenCalledWith('transactions/trash');
      });
      // Initial load + refetch after purge-all = 2 gets
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledTimes(2);
      });
    });

    test('cancelling the "Purge all" dialog closes it without calling delete', async () => {
      const user = userEvent.setup();
      renderTrash();
      await waitFor(() => {
        expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /purge all 2/i }),
      );
      await user.click(screen.getByRole('button', { name: /^cancel$/i }));

      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
      expect(mockedApi.del).not.toHaveBeenCalled();
    });
  });

  // -------------------------------------------------------------------------
  // As member (B5) — the page is member-reachable; only the admin-only
  // controls (Purge, Restore all, Purge all) are hidden. The backend
  // scopes list/restore to the member's own rows, so no control here
  // should ever be able to 403.
  // -------------------------------------------------------------------------
  describe('as member', () => {
    beforeEach(asMember);

    test('renders the trash list for a member instead of redirecting', async () => {
      renderTrash();
      expect(
        await screen.findByRole('heading', { level: 1, name: /trash/i }),
      ).toBeInTheDocument();
      expect(mockedApi.get).toHaveBeenCalled();
    });

    test('member sees Restore but no Purge, Purge all, or Restore all', async () => {
      renderTrash();
      await screen.findByRole('heading', { level: 1, name: /trash/i });
      expect(
        await screen.findAllByRole('button', { name: /^Restore / }),
      ).not.toHaveLength(0);
      expect(
        screen.queryByRole('button', { name: /Purge/i }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: /Restore all/i }),
      ).not.toBeInTheDocument();
    });

    test('member view names the creator too — attribution is not admin-gated', async () => {
      // Every row a member sees is their own, so the name here is always
      // theirs. The point is that the line renders for a non-admin at all:
      // gating it behind `admin` would leave a member's Trash inconsistent
      // with the ledger, which shows attribution to everyone.
      mockedApi.get.mockResolvedValue(memberOwnDeletedList);
      renderTrash();
      const row = (await screen.findByText('Bus pass')).closest('tr');
      expect(row).not.toBeNull();
      expect(within(row!).getByText('Bob')).toBeInTheDocument();
      expect(within(row!).getByText('Bob').closest('p')).toHaveTextContent(
        'Entered by Bob',
      );
    });
  });

  // -------------------------------------------------------------------------
  // While auth is still loading — avoid flash-of-unauthorized on hard reload
  // -------------------------------------------------------------------------
  describe('while auth is loading', () => {
    beforeEach(asLoading);

    test('shows a loading spinner and defers the gating decision', () => {
      renderTrash();

      // Spinner is rendered via role="status" aria-label="Loading"
      expect(
        screen.getByRole('status', { name: /loading/i }),
      ).toBeInTheDocument();
      // Heading must not appear yet — the page is member-reachable (no
      // admin gate since B5), but it still waits on the auth probe so a
      // hard reload doesn't flash the page before `user` resolves.
      expect(
        screen.queryByRole('heading', { level: 1, name: /trash/i }),
      ).not.toBeInTheDocument();
      // And we must not fetch the trash list until the `!user` gate clears
      expect(mockedApi.get).not.toHaveBeenCalled();
    });
  });

  // -------------------------------------------------------------------------
  // The md breakpoint — which presentation renders at which width
  // -------------------------------------------------------------------------
  //
  // Every test above this line runs at happy-dom's default 1024px and so
  // asserts the table; these two are the only ones that pin WHERE the
  // handover happens. The pair straddles the breakpoint by one pixel, so a
  // query that said `768px`, or `min-width`, fails one of them.
  // -------------------------------------------------------------------------
  // Accessible names when the description is empty — the table half
  // -------------------------------------------------------------------------
  //
  // The card half of this sits in the mobile block below. Both presentations
  // build their control names from the same `rowLabel`, and fixing only one
  // would leave the same row announcing differently depending on how wide
  // the window happened to be when you went to restore it.
  describe('a row with no description, in the table', () => {
    beforeEach(asAdmin);

    test('names its row in every control instead of announcing a bare verb', async () => {
      mockedApi.get.mockResolvedValue({
        ...defaultDeletedList,
        transactions: [
          { ...defaultDeletedList.transactions[0], description: '' },
        ],
        total: 1,
      });
      renderTrash();
      const table = await screen.findByRole('table');

      expect(
        within(table).getByRole('checkbox', {
          name: 'Select (no description)',
        }),
      ).toBeInTheDocument();
      expect(
        within(table).getByRole('button', {
          name: 'Restore (no description)',
        }),
      ).toBeInTheDocument();
      expect(
        within(table).getByRole('button', { name: 'Purge (no description)' }),
      ).toBeInTheDocument();
    });
  });

  // -------------------------------------------------------------------------
  // Phone touch floor on the controls BOTH presentations share
  // -------------------------------------------------------------------------
  //
  // These render at every width, so the fix has to raise the phone target
  // without touching the desktop toolbar. The class tokens are static, so
  // this runs at the default width — what is being asserted is the pair,
  // not the rendering.
  describe('44px touch floor on shared controls', () => {
    beforeEach(asAdmin);

    /**
     * Both halves of the swap, and neither works alone: without `min-h-11`
     * the phone target is the base `h-8` (32px, well under the floor);
     * without `md:min-h-0` the 44px floor leaks up into the desktop
     * toolbar these controls have always been 32px in.
     */
    function expectPhoneOnlyTouchFloor(el: Element): void {
      expect(touchFloorPx(el)).toBeGreaterThanOrEqual(44);
      expect(classes(el)).toContain('md:min-h-0');
    }

    test('the whole-trash bulk actions clear it', async () => {
      renderTrash();
      expectPhoneOnlyTouchFloor(
        await screen.findByRole('button', { name: /restore all 2/i }),
      );
      expectPhoneOnlyTouchFloor(
        screen.getByRole('button', { name: /purge all 2/i }),
      );
    });

    test('the selection strip actions clear it', async () => {
      const user = userEvent.setup();
      renderTrash();
      await screen.findByText('Weekly groceries');
      await user.click(
        screen.getByRole('checkbox', { name: /select all on this page/i }),
      );

      expectPhoneOnlyTouchFloor(
        screen.getByRole('button', { name: 'Restore 2' }),
      );
      expectPhoneOnlyTouchFloor(
        screen.getByRole('button', { name: /clear selection/i }),
      );
    });

    test('the purge confirm dialog footer clears it', async () => {
      const user = userEvent.setup();
      renderTrash();
      await screen.findByText('Weekly groceries');
      await user.click(
        screen.getByRole('button', { name: 'Purge Weekly groceries' }),
      );

      const dialog = screen.getByRole('dialog');
      // The destructive confirm especially: it is the last control before
      // an irreversible action, and a 40px mis-tap lands on Cancel.
      expectPhoneOnlyTouchFloor(
        within(dialog).getByRole('button', { name: /purge permanently/i }),
      );
      expectPhoneOnlyTouchFloor(
        within(dialog).getByRole('button', { name: /^cancel$/i }),
      );
    });

    test('the purge-all confirm dialog footer clears it', async () => {
      const user = userEvent.setup();
      renderTrash();
      await screen.findByText('Weekly groceries');
      await user.click(screen.getByRole('button', { name: /purge all 2/i }));

      const dialog = screen.getByRole('dialog');
      expectPhoneOnlyTouchFloor(
        within(dialog).getByRole('button', { name: /purge all permanently/i }),
      );
    });
  });

  // -------------------------------------------------------------------------
  // Phase-2 conversions onto the shared modules
  // -------------------------------------------------------------------------
  describe('shared-module conversions', () => {
    beforeEach(asAdmin);
    afterEach(() => setViewportWidth(DESKTOP_WIDTH));

    test('control names come from the shared transactionLabel', async () => {
      // Pins Trash to the SHARED implementation rather than to the string it
      // happens to produce today: re-localising the fallback, or letting it
      // drift from the Transactions copy, fails here. The empty description
      // is the case the shared function exists for.
      mockedApi.get.mockResolvedValue({
        ...defaultDeletedList,
        transactions: [
          { ...defaultDeletedList.transactions[0], description: '' },
        ],
        total: 1,
      });
      setViewportWidth(PHONE_WIDTH);
      const list = await renderCardList();
      const expected = transactionLabel({ description: '' });

      expect(
        within(list).getByRole('checkbox', { name: `Select ${expected}` }),
      ).toBeInTheDocument();
      expect(
        within(list).getByRole('button', { name: `Restore ${expected}` }),
      ).toBeInTheDocument();
    });

    test('the card checkbox uses the shared touch-target recipe', async () => {
      setViewportWidth(PHONE_WIDTH);
      const list = await renderCardList();
      const checkbox = within(list).getByRole('checkbox', {
        name: 'Select Weekly groceries',
      });

      // Every token of the shared constant, so a local copy that drifts from
      // it (a shrunken inset, a dropped `relative`) fails rather than quietly
      // producing a smaller target than the constant promises.
      for (const token of TOUCH_TARGET_CHECKBOX.split(/\s+/)) {
        expect(classes(checkbox)).toContain(token);
      }
      expect(checkboxTapAreaPx(checkbox)).toBeGreaterThanOrEqual(44);
    });

    test('the phone pager collapses to a compact readout', async () => {
      setViewportWidth(PHONE_WIDTH);
      renderTrash();
      await screen.findByRole('list', { name: /deleted transactions/i });

      // UX-D5: nine 32px numbered buttons plus the rows-per-page control do
      // not fit 390px, and wrapped they cost two extra rows above AND below.
      expect(screen.getAllByText(/^Page 1 of 1$/)).not.toHaveLength(0);
      expect(
        screen.queryByRole('button', { name: '1' }),
      ).not.toBeInTheDocument();
    });

    test('the desktop pager keeps its numbered cluster', async () => {
      renderTrash();
      await screen.findByRole('table');

      expect(
        screen.getAllByRole('button', { name: '1' }),
      ).not.toHaveLength(0);
      expect(screen.queryByText(/^Page 1 of 1$/)).not.toBeInTheDocument();
    });

    test('the phone pager controls clear the 44px floor', async () => {
      // A consumer-level pin, deliberately: <PaginationBar> ships with no test
      // file of its own, so without this the 44px half of UX-D5 is asserted
      // nowhere on this page.
      setViewportWidth(PHONE_WIDTH);
      renderTrash();
      await screen.findByRole('list', { name: /deleted transactions/i });

      const next = screen.getAllByRole('button', {
        name: /go to next page/i,
      })[0];
      expect(touchFloorPx(next)).toBeGreaterThanOrEqual(44);
      expect(classes(next)).toContain('md:size-8');
    });

    test('the selection bar is sticky and sits above the pager on a phone', async () => {
      const user = userEvent.setup();
      setViewportWidth(PHONE_WIDTH);
      const list = await renderCardList();
      await user.click(
        within(list).getByRole('checkbox', { name: 'Select Weekly groceries' }),
      );

      const bar = screen.getByText(/1 selected/i).closest('div');
      const wrapper = bar!.parentElement!;
      expect(classes(wrapper)).toContain('sticky');
      expect(classes(wrapper)).toContain('bottom-0');
      // design-I1: app-chrome bars in this repo separate with a border, not a
      // shadow.
      expect(classes(wrapper).some((c) => c.startsWith('shadow'))).toBe(false);

      // Exactly ONE separator, and the strip owns it. Both carrying `border-t`
      // puts two 1px lines flush against each other — the wrapper has no
      // gutter to hold them apart the way the Transactions bar does.
      expect(classes(bar!)).toContain('border-t');
      expect(classes(wrapper)).not.toContain('border-t');

      // The bottom inset floor. Without the max() the strip's own py-2 is the
      // only bottom spacing on a device reporting no safe-area inset, which is
      // half what the Transactions bar renders.
      expect(classes(wrapper)).toContain(
        'pb-[max(0.5rem,env(safe-area-inset-bottom))]',
      );
      // ...and NOTHING but that. The strip runs edge to edge; any other
      // padding utility here (Transactions carries `p-2`, because its bar is a
      // rounded floating card that needs a gutter) insets it and shows the
      // wrapper's background down both sides. Matches p-/px-/py-/pt-/pl-/pr-
      // but deliberately not pb-, which is the one above.
      expect(classes(wrapper).filter((c) => /^p(?:[xytlr])?-/.test(c))).toEqual(
        [],
      );

      // The whole mechanism: it releases when the pager arrives, which only
      // holds if the pager is what comes next.
      expect(wrapper.nextElementSibling).toContainElement(
        screen.getAllByRole('button', { name: /go to next page/i })[1],
      );
      // ...and it must come after the list, or it occludes the last card.
      expect(
        list.compareDocumentPosition(wrapper) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    });

    test('the selection bar stays in flow on desktop', async () => {
      const user = userEvent.setup();
      renderTrash();
      const table = await screen.findByRole('table');
      await user.click(
        within(table).getByRole('checkbox', { name: 'Select Weekly groceries' }),
      );

      const wrapper = screen.getByText(/1 selected/i).closest('div')!
        .parentElement!;
      // No sticky wrapper at all on a surface with no thumb and no fold.
      expect(classes(wrapper)).not.toContain('sticky');
      expect(
        table.compareDocumentPosition(wrapper) &
          Node.DOCUMENT_POSITION_PRECEDING,
      ).toBeTruthy();
    });
  });

  describe('presentation swap at md', () => {
    beforeEach(asAdmin);
    afterEach(() => setViewportWidth(DESKTOP_WIDTH));

    test('at 768px the table renders and the card list does not', async () => {
      setViewportWidth(768);
      renderTrash();
      expect(await screen.findByRole('table')).toBeInTheDocument();
      expect(
        screen.queryByRole('list', { name: /deleted transactions/i }),
      ).not.toBeInTheDocument();
    });

    test('at 767px the card list renders and the table does not', async () => {
      setViewportWidth(767);
      renderTrash();
      expect(
        await screen.findByRole('list', { name: /deleted transactions/i }),
      ).toBeInTheDocument();
      // Both at once would mean two "Restore Weekly groceries" buttons and
      // two select-alls, and every page-wide query in this file becomes a
      // coin flip over which one it found.
      expect(screen.queryByRole('table')).not.toBeInTheDocument();
    });
  });

  // -------------------------------------------------------------------------
  // Mobile card list (below md)
  // -------------------------------------------------------------------------
  describe('mobile card list', () => {
    beforeEach(() => setViewportWidth(PHONE_WIDTH));
    afterEach(() => setViewportWidth(DESKTOP_WIDTH));

    describe('as admin', () => {
      beforeEach(asAdmin);

      test('renders one card per deleted row', async () => {
        const list = await renderCardList();
        expect(within(list).getAllByRole('listitem')).toHaveLength(2);
        expect(within(list).getByText('Weekly groceries')).toBeInTheDocument();
        expect(within(list).getByText('April salary')).toBeInTheDocument();
      });

      test('the description line is truncated, emphasised, and keeps the full text on hover', async () => {
        const list = await renderCardList();
        const description = within(list).getByText('Weekly groceries');

        // Exact tokens, not substrings — `toContain('truncate')` on the raw
        // className string also passes for `truncate-none`.
        expect(classes(description)).toContain('truncate');
        expect(classes(description)).toContain('font-medium');
        // Without min-w-0 the flex item refuses to shrink below its content
        // and pushes the amount off the card instead of clipping. Neither
        // token does anything without the other.
        expect(classes(description)).toContain('min-w-0');
        // The only route back to a description that import let past the
        // 500-character limit, once the card clips it.
        expect(description).toHaveAttribute('title', 'Weekly groceries');
      });

      test('an empty description falls back rather than leaving a blank line', async () => {
        mockedApi.get.mockResolvedValue({
          ...defaultDeletedList,
          transactions: [
            { ...defaultDeletedList.transactions[0], description: '' },
          ],
          total: 1,
        });
        const list = await renderCardList();
        expect(within(list).getByText('(no description)')).toBeInTheDocument();
      });

      test('the creator line is byte-identical to the table row it replaces', async () => {
        // The idiom is shipped on three surfaces character for character.
        // A fourth that paraphrases it — drops the sr-only prefix, or the
        // aria-hidden on the icon — announces differently on the one
        // surface where attribution decides whether you are about to
        // restore somebody else's row. Comparing the rendered markup
        // catches that; asserting "Bob is on the card" does not.
        setViewportWidth(DESKTOP_WIDTH);
        const desktop = renderTrash();
        const tableRow = (await screen.findByText('April salary')).closest(
          'tr',
        );
        const fromTable = within(tableRow!)
          .getByText('Bob')
          .closest('p')!.outerHTML;
        desktop.unmount();

        setViewportWidth(PHONE_WIDTH);
        const list = await renderCardList();
        const card = within(list).getByText('April salary').closest('li');
        const fromCard = within(card!)
          .getByText('Bob')
          .closest('p')!.outerHTML;

        expect(fromCard).toBe(fromTable);
      });

      test('each card names its own creator, not the logged-in user', async () => {
        const list = await renderCardList();
        const groceries = within(list)
          .getByText('Weekly groceries')
          .closest('li');
        const salary = within(list).getByText('April salary').closest('li');

        // Scoped per card: a page-wide query would also pass for a render
        // that reused the first row's creator for every card.
        expect(within(groceries!).getByText('Alice')).toBeInTheDocument();
        expect(within(salary!).getByText('Bob')).toBeInTheDocument();
        expect(
          within(salary!).getByText('Bob').closest('p'),
        ).toHaveTextContent('Entered by Bob');
      });

      test('a card whose creator row is gone reads "Unknown", not a blank line', async () => {
        // The byte-identical comparison above runs on a row with a real
        // creator, so it cannot see this: drop the `|| 'Unknown'` and both
        // presentations still agree — on a blank. `created_by: ''` is the
        // wire's documented "the LEFT JOIN found nothing" value.
        mockedApi.get.mockResolvedValue(orphanedCreatorDeletedList);
        const list = await renderCardList();
        const card = within(list)
          .getByText('Row from a restored backup')
          .closest('li');
        expect(
          within(card!).getByText('Unknown').closest('p'),
        ).toHaveTextContent('Entered by Unknown');
      });

      test('the card carries the transaction date and how long ago it was deleted', async () => {
        mockedApi.get.mockResolvedValue(deletedDaysAgoList(3));

        // Captured from the table rather than spelled out, so this stays
        // true in any timezone — and so it pins that the card shows the
        // TRANSACTION date, not the deletion date it sits beside.
        setViewportWidth(DESKTOP_WIDTH);
        const desktop = renderTrash();
        const tableRow = (
          await screen.findByText('Weekly groceries')
        ).closest('tr');
        const tableDate = within(tableRow!).getByText(
          /^\w{3} \d+, \d{4}$/,
        ).textContent;
        const deletedTitle = tableRow!
          .querySelector('td[title]')!
          .getAttribute('title');
        desktop.unmount();

        setViewportWidth(PHONE_WIDTH);
        const list = await renderCardList();
        const card = within(list).getByText('Weekly groceries').closest('li');
        // "Deleted" is spelled out because no column header survives the
        // fold to say which of the two dates this one is.
        const line = within(card!).getByText(
          `${tableDate} · Deleted 3 days ago`,
        );
        // The absolute timestamp stays reachable for stale trash, exactly
        // as the table's Deleted cell keeps it.
        expect(line).toHaveAttribute('title', deletedTitle!);
      });

      // The threshold is 7 calendar days. These two straddle it by one day,
      // so widening or narrowing the gate — or flipping the comparison —
      // fails one of them. Neither asserts a re-derived date string: the
      // fresh case pins the age wording and the absence of a date, the
      // stale case pins the date SHAPE and the absence of an age.
      test('a fresh tombstone reads as an age, not a date', async () => {
        mockedApi.get.mockResolvedValue(deletedDaysAgoList(6));
        const list = await renderCardList();
        const card = within(list).getByText('Weekly groceries').closest('li');

        expect(card).toHaveTextContent('Deleted 6 days ago');
        // "3 days ago" is the fastest thing to scan while you still
        // remember the mistake; a date here would be noise.
        expect(card).not.toHaveTextContent(/Deleted on /);
      });

      test('a stale tombstone reads as a date, not an age', async () => {
        mockedApi.get.mockResolvedValue(deletedDaysAgoList(7));
        const list = await renderCardList();
        const card = within(list).getByText('Weekly groceries').closest('li');

        // `title=` cannot carry this on the surface that matters — tooltips
        // never open on touch — so at this age the date is printed outright.
        expect(card).toHaveTextContent(/Deleted on \w{3} \d{1,2}, \d{4}/);
        expect(card).not.toHaveTextContent(/days ago/);
      });

      test('button icons are decorative and let Button own their size', async () => {
        const list = await renderCardList();
        const card = within(list).getByText('Weekly groceries').closest('li');
        const restore = within(card!).getByRole('button', {
          name: 'Restore Weekly groceries',
        });
        const icon = restore.querySelector('svg');
        expect(icon).not.toBeNull();

        // The button already carries the whole accessible name; an
        // unlabelled decorative glyph inside it must not be announced.
        //
        // This pins the RENDERED result, not our prop: lucide-react sets
        // aria-hidden="true" itself (probed), so deleting the explicit
        // attribute in the JSX changes nothing and this assertion would
        // not notice. What it does catch is the cases that matter — a
        // lucide upgrade dropping the default, or someone writing
        // aria-hidden={false} to "fix" a perceived missing label.
        expect(icon).toHaveAttribute('aria-hidden', 'true');
        // Button's own `[&_svg]:size-4` is a descendant selector and so
        // outranks any size class on the icon itself — an explicit one is
        // dead weight that reads as deliberate sizing. Its ABSENCE is the
        // assertion; asserting a particular size token would pin a rule
        // that never applies.
        expect(
          Array.from(icon!.classList).some((c) => /^size-/.test(c)),
        ).toBe(false);
      });

      test('the card shows category and tags', async () => {
        mockedApi.get.mockResolvedValue({
          ...defaultDeletedList,
          transactions: [
            {
              ...defaultDeletedList.transactions[0],
              tags: 'grocery, weekly',
            },
          ],
          total: 1,
        });
        const list = await renderCardList();
        const card = within(list).getByText('Weekly groceries').closest('li');
        expect(within(card!).getByText('Food')).toBeInTheDocument();
        expect(within(card!).getByText('grocery')).toBeInTheDocument();
        expect(within(card!).getByText('weekly')).toBeInTheDocument();
      });

      test('the amount keeps the table sign and colour on both category types', async () => {
        const list = await renderCardList();
        const expense = within(list)
          .getByText('Weekly groceries')
          .closest('li');
        const income = within(list).getByText('April salary').closest('li');

        // Anchored regexes, not toHaveTextContent's default substring
        // match — "-$25.50" is a substring of plenty of wrong renders.
        const expenseAmount = within(expense!).getByTestId('amount-display');
        expect(expenseAmount).toHaveTextContent(/^-\$25\.50$/);
        expect(classes(expenseAmount)).toContain('text-foreground');
        expect(classes(expenseAmount)).toContain('font-mono');
        expect(classes(expenseAmount)).toContain('tabular-nums');

        // Income is the half a "just reuse the expense styling" edit loses:
        // a positive row rendered with a minus sign is a wrong number, not
        // a cosmetic slip.
        const incomeAmount = within(income!).getByTestId('amount-display');
        expect(incomeAmount).toHaveTextContent(/^\+\$2,500\.00$/);
        expect(classes(incomeAmount)).toContain('text-emerald-500');
      });

      test('a foreign-currency card shows what the row was booked in', async () => {
        // The table has never shown this and the card deliberately breaks
        // parity with it: in a household entering LBP and USD daily, the
        // converted figure alone does not identify the row you deleted.
        mockedApi.get.mockResolvedValue({
          ...defaultDeletedList,
          transactions: [
            {
              ...defaultDeletedList.transactions[0],
              original_amount: 2250000,
              original_currency: 'LBP',
            },
          ],
          total: 1,
        });
        const list = await renderCardList();
        const card = within(list).getByText('Weekly groceries').closest('li');

        expect(within(card!).getByTestId('amount-display')).toHaveTextContent(
          '-$25.50',
        );
        expect(
          within(card!).getByTestId('amount-display-secondary'),
        ).toHaveTextContent('2,250,000.00 LBP');
      });

      test('a base-currency card shows no second figure', async () => {
        // The suppression half of the same contract. Without it every row
        // in a single-currency household grows a redundant second line
        // restating the number directly above it.
        mockedApi.get.mockResolvedValue({
          ...defaultDeletedList,
          transactions: [
            {
              ...defaultDeletedList.transactions[0],
              original_amount: 25.5,
              original_currency: 'USD',
            },
          ],
          total: 1,
        });
        const list = await renderCardList();
        const card = within(list).getByText('Weekly groceries').closest('li');

        expect(
          within(card!).queryByTestId('amount-display-secondary'),
        ).not.toBeInTheDocument();
      });

      test('a card with no description still names its row in every control', async () => {
        // `Select ${''}` announces "Select " — a control with no object.
        // Import does not enforce the description limit or require one, so
        // an empty description reaches this surface for real.
        mockedApi.get.mockResolvedValue({
          ...defaultDeletedList,
          transactions: [
            { ...defaultDeletedList.transactions[0], description: '' },
          ],
          total: 1,
        });
        const list = await renderCardList();

        expect(
          within(list).getByRole('checkbox', {
            name: 'Select (no description)',
          }),
        ).toBeInTheDocument();
        expect(
          within(list).getByRole('button', {
            name: 'Restore (no description)',
          }),
        ).toBeInTheDocument();
        expect(
          within(list).getByRole('button', { name: 'Purge (no description)' }),
        ).toBeInTheDocument();
      });

      test('Restore is a visible per-card button that clears the 44px touch floor', async () => {
        const list = await renderCardList();
        const card = within(list).getByText('Weekly groceries').closest('li');
        const restore = within(card!).getByRole('button', {
          name: 'Restore Weekly groceries',
        });
        // There is no detail view to tap through to on this surface, so
        // Restore cannot retreat into a menu — it is the reason the page
        // exists.
        expect(restore).toBeVisible();
        expect(touchFloorPx(restore)).toBeGreaterThanOrEqual(44);
      });

      test('tapping Restore on a card POSTs to the restore endpoint', async () => {
        const user = userEvent.setup();
        mockedApi.post.mockResolvedValue({});
        await renderCardList();

        await user.click(
          screen.getByRole('button', { name: 'Restore Weekly groceries' }),
        );

        await waitFor(() => {
          expect(mockedApi.post).toHaveBeenCalledWith(
            'transactions/101/restore',
          );
        });
      });

      test('a card in flight disables its own controls and says so', async () => {
        const user = userEvent.setup();
        // Never resolves: the card stays in its in-flight state for the
        // whole assertion.
        mockedApi.post.mockReturnValue(new Promise(() => {}));
        await renderCardList();

        await user.click(
          screen.getByRole('button', { name: 'Restore Weekly groceries' }),
        );

        // `isRestoring` and `rowBusy` are separate props on <TrashCard>, and
        // both are the kind of wiring a card can drop while still compiling:
        // hand it `false` and the row invites a second click on a restore
        // that is already running.
        const card = screen.getByText('Weekly groceries').closest('li');
        expect(within(card!).getByText('Restoring...')).toBeInTheDocument();
        expect(
          within(card!).getByRole('button', {
            name: 'Restore Weekly groceries',
          }),
        ).toBeDisabled();
        expect(
          within(card!).getByRole('checkbox', {
            name: 'Select Weekly groceries',
          }),
        ).toBeDisabled();
      });

      test('Purge on a card opens the confirm dialog without deleting', async () => {
        const user = userEvent.setup();
        await renderCardList();

        await user.click(
          screen.getByRole('button', { name: 'Purge Weekly groceries' }),
        );

        expect(screen.getByRole('dialog')).toBeInTheDocument();
        expect(
          screen.getByText(/permanently delete this transaction/i),
        ).toBeInTheDocument();
        expect(mockedApi.del).not.toHaveBeenCalled();
      });

      test('per-card checkboxes feed the shared batch restore', async () => {
        const user = userEvent.setup();
        mockedApi.post.mockResolvedValue({ restored: 2 });
        await renderCardList();

        await user.click(
          screen.getByRole('checkbox', { name: 'Select Weekly groceries' }),
        );
        await user.click(
          screen.getByRole('checkbox', { name: 'Select April salary' }),
        );
        expect(screen.getByText(/2 selected/i)).toBeInTheDocument();

        await user.click(screen.getByRole('button', { name: 'Restore 2' }));

        await waitFor(() => {
          expect(mockedApi.post).toHaveBeenCalledWith(
            'transactions/restore-batch',
            { ids: [101, 102] },
          );
        });
      });

      test('a card checkbox is a 44px tap target without moving', async () => {
        await renderCardList();
        const checkbox = screen.getByRole('checkbox', {
          name: 'Select Weekly groceries',
        });

        // Padding would grow the visible box and shove the text beside it;
        // the pseudo-element grows only the hit area, and needs all three
        // tokens to be laid out at all.
        expect(classes(checkbox)).toContain('relative');
        expect(classes(checkbox)).toContain('before:absolute');
        expect(classes(checkbox)).toContain("before:content-['']");
        expect(checkboxTapAreaPx(checkbox)).toBeGreaterThanOrEqual(44);
      });

      test('the card list has its own select-all, since no column header survives', async () => {
        const user = userEvent.setup();
        await renderCardList();

        await user.click(
          screen.getByRole('checkbox', { name: 'Select all on this page' }),
        );
        expect(screen.getByText(/2 selected/i)).toBeInTheDocument();
      });

      test('the select-all announces a name that contains its visible text', async () => {
        await renderCardList();
        const checkbox = screen.getByRole('checkbox', {
          name: 'Select all on this page',
        });
        const visible = screen.getByText('Select all');

        // WCAG 2.5.3: a fuller accessible name is fine, but it has to
        // contain what the eye reads, or "tap Select all" names something
        // voice control cannot find.
        expect(checkbox.getAttribute('aria-label')).toContain(
          visible.textContent,
        );
        // And the label targets the control, so the whole 44px strip — not
        // just the 16px box — toggles it.
        expect(visible.closest('label')).toHaveAttribute('for', checkbox.id);
        expect(touchFloorPx(visible.closest('label')!)).toBeGreaterThanOrEqual(
          44,
        );
      });
    });

    describe('as member', () => {
      beforeEach(asMember);

      test('a member gets Restore on the card and no Purge anywhere', async () => {
        mockedApi.get.mockResolvedValue(memberOwnDeletedList);
        const list = await renderCardList();
        const card = within(list).getByText('Bus pass').closest('li');

        expect(
          within(card!).getByRole('button', { name: 'Restore Bus pass' }),
        ).toBeInTheDocument();
        // The backend is the real boundary, but a member tapping a Purge
        // the UI should never have drawn gets a 403 on a recovery surface.
        expect(
          screen.queryByRole('button', { name: /purge/i }),
        ).not.toBeInTheDocument();
      });

      test('a member card still names its creator', async () => {
        mockedApi.get.mockResolvedValue(memberOwnDeletedList);
        const list = await renderCardList();
        const card = within(list).getByText('Bus pass').closest('li');
        expect(
          within(card!).getByText('Bob').closest('p'),
        ).toHaveTextContent('Entered by Bob');
      });
    });
  });
});
