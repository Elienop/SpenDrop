import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

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
import { toast } from 'sonner';
import { Trash } from './Trash';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const defaultDeletedList = {
  transactions: [
    {
      id: 101,
      user_id: 1,
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
      user_id: 1,
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

function renderTrash() {
  return render(
    <MemoryRouter initialEntries={['/trash']}>
      <Trash />
    </MemoryRouter>,
  );
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
});
