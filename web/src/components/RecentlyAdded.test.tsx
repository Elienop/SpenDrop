import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement } from 'react';
import type { ReactNode } from 'react';
import type { Category, Transaction } from '@/api/types';

const apiGet = vi.fn();
const apiDel = vi.fn();
const apiPost = vi.fn();
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    del: (...args: unknown[]) => apiDel(...args),
    post: (...args: unknown[]) => apiPost(...args),
  },
}));

const removeQueued = vi.fn();
const enqueue = vi.fn();
vi.mock('@/lib/offline-queue', () => ({
  removeQueued: (...args: unknown[]) => removeQueued(...args),
  enqueue: (...args: unknown[]) => enqueue(...args),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign(() => undefined, {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  }),
}));

import { RecentlyAdded } from './RecentlyAdded';
import type { QueuedTransaction } from '@/lib/offline-queue';

const categories: Category[] = [
  {
    id: 1,
    name: 'Food',
    type: 'expense',
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '',
  },
  {
    id: 2,
    name: 'Salary',
    type: 'income',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '',
  },
];

function queued(over: Partial<QueuedTransaction> = {}): QueuedTransaction {
  return {
    id: 1,
    payload: {
      date: '2026-05-27',
      amount: 22.22,
      description: 'sdpending',
      category_id: 1,
      tags: '',
    },
    queuedAt: 2000,
    attempts: 0,
    ...over,
  };
}

function savedTxn(over: Partial<Transaction> = {}): Transaction {
  return {
    id: 5,
    user_id: 1,
    created_by: 'Elie',
    date: '2026-05-27',
    amount: 10,
    original_amount: null,
    original_currency: null,
    description: 'sdsaved',
    category_id: 1,
    category_name: 'Food',
    category_type: 'expense',
    tags: null,
    notes: null,
    created_at: '2026-05-27T10:00:00Z',
    updated_at: '',
    ...over,
  };
}

function setOnline(value: boolean): void {
  Object.defineProperty(navigator, 'onLine', { configurable: true, value });
}

// The offline queue is namespaced per user; pending-row delete/undo target it.
const RU = 7;

// A fresh per-test QueryClient (retry off) keeps each render isolated and
// preserves the "two distinct query keys → two apiGet calls" assertions.
function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

function renderPanel(pending: QueuedTransaction[], refreshKey = 0) {
  // Pass the provider via RTL's `wrapper` option (not by wrapping the JSX) so
  // that `rerender` reuses the same provider — the refreshKey test rerenders
  // the bare component, which would otherwise lose the QueryClientProvider.
  return render(
    <RecentlyAdded
      userId={RU}
      pending={pending}
      categories={categories}
      baseCode="USD"
      refreshKey={refreshKey}
    />,
    { wrapper: makeWrapper() },
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  setOnline(true);
  apiGet.mockResolvedValue({
    transactions: [savedTxn()],
    total: 1,
    page: 1,
    per_page: 5,
  });
  apiDel.mockResolvedValue(undefined);
  removeQueued.mockResolvedValue(undefined);
  enqueue.mockResolvedValue(2);
});

afterEach(() => setOnline(true));

describe('RecentlyAdded', () => {
  test('shows both pending and saved rows', async () => {
    renderPanel([queued()]);

    expect(await screen.findByText('sdsaved')).toBeInTheDocument();
    expect(screen.getByText('sdpending')).toBeInTheDocument();
    // Pending rows are labelled; saved rows are not.
    expect(screen.getByText(/not synced/i)).toBeInTheDocument();
  });

  test('renders a saved income row with a + sign and income color', async () => {
    apiGet.mockResolvedValue({
      transactions: [
        savedTxn({
          id: 6,
          description: 'sdincome',
          category_id: 2,
          category_name: 'Salary',
          category_type: 'income',
          amount: 5970,
        }),
      ],
      total: 1,
      page: 1,
      per_page: 5,
    });
    renderPanel([]);
    const li = (await screen.findByText('sdincome')).closest('li');
    const amount = li?.querySelector('span.font-mono');
    // WHOLE-STRING, not `startsWith('+')`: the defect this rule replaces
    // prepends a second sign, and "+-$5,970.00" starts with '+' too.
    expect(amount?.textContent).toBe('+$5,970.00');
    expect(amount?.className).toContain('text-emerald-500');
    expect(within(li!).queryByTestId('amount-sign-note')).toBeNull();
  });

  test('renders an expense row with a minus sign and no income color', async () => {
    renderPanel([]); // default savedTxn 'sdsaved' is an expense row
    const li = (await screen.findByText('sdsaved')).closest('li');
    const amount = li?.querySelector('span.font-mono');
    expect(amount?.textContent).toBe('-$10.00');
    expect(amount?.className).not.toContain('text-emerald-500');
    expect(within(li!).queryByTestId('amount-sign-note')).toBeNull();
  });

  test('a saved REFUND reads as money back, and is labelled', async () => {
    // This panel is the confirmation surface for what was just entered, so a
    // refund has to be recognisable here before anything else. Under the
    // previous rule it rendered "--$10.00" in expense styling.
    apiGet.mockResolvedValue({
      transactions: [savedTxn({ id: 7, description: 'sdrefund', amount: -10 })],
      total: 1,
      page: 1,
      per_page: 5,
    });
    renderPanel([]);
    const li = (await screen.findByText('sdrefund')).closest('li');
    const amount = li?.querySelector('span.font-mono');
    expect(amount?.textContent).toBe('+$10.00');
    expect(amount?.className).toContain('text-emerald-500');
    const note = within(li!).getByTestId('amount-sign-note');
    expect(note).toHaveTextContent('Refund');
    // BEFORE the figure, the order `AmountDisplay` pins for the same pair: the
    // correction has to be announced before the number it corrects. Asserted
    // as adjacency rather than by reading the row's whole text, which also
    // carries the description and the sync badge.
    expect(note.nextElementSibling).toBe(amount);
  });

  test('a PENDING refund reads the same as a saved one', async () => {
    // Offline rows render from the queue payload, not from a server response,
    // and they take a separate path to the same span. A refund captured on the
    // phone with no signal must not change appearance when it lands.
    renderPanel([
      queued({
        payload: {
          date: '2026-05-27',
          amount: -10,
          description: 'sdpendingrefund',
          category_id: 1,
          tags: '',
        },
      }),
    ]);
    const li = (await screen.findByText('sdpendingrefund')).closest('li');
    const amount = li?.querySelector('span.font-mono');
    expect(amount?.textContent).toBe('+$10.00');
    expect(within(li!).getByTestId('amount-sign-note')).toHaveTextContent(
      'Refund',
    );
  });

  test('deleting a pending row drops it from the queue (no server call)', async () => {
    const user = userEvent.setup();
    renderPanel([queued()]);
    await screen.findByText('sdpending');

    await user.click(
      screen.getByRole('button', { name: /delete unsynced entry sdpending/i }),
    );

    await waitFor(() => expect(removeQueued).toHaveBeenCalledWith(RU, 1));
    expect(apiDel).not.toHaveBeenCalled();
    expect(toastSuccess).toHaveBeenCalled();
  });

  test('deleting a saved row soft-deletes it on the server', async () => {
    const user = userEvent.setup();
    renderPanel([]);
    await screen.findByText('sdsaved');

    await user.click(screen.getByRole('button', { name: /delete sdsaved/i }));

    await waitFor(() => expect(apiDel).toHaveBeenCalledWith('transactions/5'));
    expect(removeQueued).not.toHaveBeenCalled();
  });

  test('deleting a saved row removes it via refetch (no window event)', async () => {
    // The old bespoke TRASH_CHANGED_EVENT window-event bus is retired —
    // the saved row is now refetched away locally and the sidebar badge
    // refreshes via TanStack focus-refetch + the SSE `trash` invalidation.
    apiDel.mockResolvedValueOnce(undefined);
    // First load returns the saved row; the post-delete refetch returns empty.
    apiGet
      .mockResolvedValueOnce({
        transactions: [savedTxn()],
        total: 1,
        page: 1,
        per_page: 5,
      })
      .mockResolvedValueOnce({
        transactions: [],
        total: 0,
        page: 1,
        per_page: 5,
      });

    const user = userEvent.setup();
    renderPanel([]);
    await screen.findByText('sdsaved');

    await user.click(screen.getByRole('button', { name: /delete sdsaved/i }));

    await waitFor(() => expect(apiDel).toHaveBeenCalledWith('transactions/5'));
    expect(toastSuccess).toHaveBeenCalledWith(
      'Moved to Trash',
      expect.objectContaining({
        action: expect.objectContaining({ label: 'Undo' }),
      }),
    );
    // The row is refetched away — no leftover saved row in the panel.
    await waitFor(() =>
      expect(screen.queryByText('sdsaved')).not.toBeInTheDocument(),
    );
  });

  test('Undo on a saved-row delete calls the restore endpoint and refetches', async () => {
    apiDel.mockResolvedValueOnce(undefined);
    apiPost.mockResolvedValueOnce({ status: 'restored' });
    // Load → row present; post-delete refetch → empty; post-undo refetch →
    // row back. Explicit three-deep queue: this file's Once-queues are per
    // test, and a leftover would fake a later test's failure.
    apiGet
      .mockResolvedValueOnce({
        transactions: [savedTxn()],
        total: 1,
        page: 1,
        per_page: 5,
      })
      .mockResolvedValueOnce({
        transactions: [],
        total: 0,
        page: 1,
        per_page: 5,
      })
      .mockResolvedValueOnce({
        transactions: [savedTxn()],
        total: 1,
        page: 1,
        per_page: 5,
      });

    const user = userEvent.setup();
    renderPanel([]);
    await screen.findByText('sdsaved');

    await user.click(screen.getByRole('button', { name: /delete sdsaved/i }));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());

    const options = toastSuccess.mock.calls[0][1] as {
      action: { label: string; onClick: () => void };
    };
    options.action.onClick();

    await waitFor(() =>
      expect(apiPost).toHaveBeenCalledWith('transactions/5/restore', {}),
    );
    // The undo refetch brings the row back.
    await waitFor(() => expect(screen.getByText('sdsaved')).toBeInTheDocument());
    // And the success toast (previously silent) fires once the restore
    // resolves — matches Trash.tsx's own restore feedback.
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith('Restored'),
    );
  });

  test('deleting a pending (un-synced) row drops it locally without a network delete', async () => {
    // Pending rows live in local IndexedDB only — they never reached
    // the server, so deleting one is not a server tombstone: it drops
    // the queued row via removeQueued and issues no DELETE.
    const user = userEvent.setup();
    renderPanel([queued()]);
    await user.click(
      screen.getByRole('button', { name: /delete unsynced entry sdpending/i }),
    );
    await waitFor(() => expect(removeQueued).toHaveBeenCalled());
    expect(apiDel).not.toHaveBeenCalled();
  });

  test('offline: shows pending + an offline note and never fetches saved', async () => {
    setOnline(false);
    renderPanel([queued()]);

    expect(await screen.findByText('sdpending')).toBeInTheDocument();
    expect(screen.getByText(/you're offline/i)).toBeInTheDocument();
    expect(screen.queryByText('sdsaved')).not.toBeInTheDocument();
    expect(apiGet).not.toHaveBeenCalled();
  });

  test('renders an empty state, not nothing, when there is no activity', async () => {
    apiGet.mockResolvedValue({
      transactions: [],
      total: 0,
      page: 1,
      per_page: 5,
    });
    const { container } = renderPanel([]);

    await waitFor(() => expect(apiGet).toHaveBeenCalled());
    // The heading is restoreFocus()'s target, so the panel has to survive an
    // empty list — see the focus test below for what unmounting it costs.
    expect(
      screen.getByRole('heading', { name: 'Recently added' }),
    ).toBeInTheDocument();
    expect(screen.getByText('No recent entries.')).toBeInTheDocument();
    expect(container.querySelector('ul')).toBeNull();
  });

  test('deleting the LAST row keeps focus on the heading, not <body>', async () => {
    // One saved row, and the post-delete refetch returns an empty list — the
    // exact moment the panel used to `return null` and unmount the heading
    // that restoreFocus() had just aimed at, dropping focus onto <body> while
    // the Undo toast was still being offered.
    apiGet
      .mockResolvedValueOnce({
        transactions: [savedTxn()],
        total: 1,
        page: 1,
        per_page: 5,
      })
      .mockResolvedValue({
        transactions: [],
        total: 0,
        page: 1,
        per_page: 5,
      });
    const user = userEvent.setup();
    renderPanel([]);
    await screen.findByText('sdsaved');

    await user.click(screen.getByRole('button', { name: /delete sdsaved/i }));

    // Wait on the ROW leaving, which happens either way — the old code
    // unmounted the whole panel, the new one swaps in the empty state. So
    // this gate clears under both and the focus assertions below are what
    // actually distinguishes them.
    await waitFor(() =>
      expect(screen.queryByText('sdsaved')).not.toBeInTheDocument(),
    );
    expect(document.activeElement).not.toBe(document.body);
    expect(
      screen.getByRole('heading', { name: 'Recently added' }),
    ).toHaveFocus();
  });

  test('Undo on a pending delete re-queues the captured payload', async () => {
    const user = userEvent.setup();
    renderPanel([queued()]);
    await screen.findByText('sdpending');

    await user.click(
      screen.getByRole('button', { name: /delete unsynced entry sdpending/i }),
    );

    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
    const [, opts] = toastSuccess.mock.calls[0] as [
      string,
      { action?: { label: string; onClick: () => void } },
    ];
    expect(opts.action?.label).toMatch(/undo/i);

    opts.action?.onClick();
    await waitFor(() => expect(enqueue).toHaveBeenCalled());
    expect(enqueue.mock.calls[0][0]).toBe(RU);
    expect(enqueue.mock.calls[0][1]).toMatchObject({
      description: 'sdpending',
      amount: 22.22,
    });
  });

  test('a failed pending removal surfaces an error toast', async () => {
    removeQueued.mockRejectedValueOnce(new Error('idb'));
    const user = userEvent.setup();
    renderPanel([queued()]);
    await screen.findByText('sdpending');

    await user.click(
      screen.getByRole('button', { name: /delete unsynced entry sdpending/i }),
    );

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(toastSuccess).not.toHaveBeenCalled();
  });

  test('a failed saved delete surfaces an error toast', async () => {
    apiDel.mockRejectedValueOnce(new Error('500'));
    const user = userEvent.setup();
    renderPanel([]);
    await screen.findByText('sdsaved');

    await user.click(screen.getByRole('button', { name: /delete sdsaved/i }));

    // The rejection's own message is surfaced (same ternary as the undo
    // catch two lines above it in the component), not a generic fallback.
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('500'));
  });

  test('bumping refreshKey re-pulls the saved list', async () => {
    const { rerender } = renderPanel([], 0);
    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(1));

    rerender(
      <RecentlyAdded
        userId={RU}
        pending={[]}
        categories={categories}
        baseCode="USD"
        refreshKey={1}
      />,
    );

    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(2));
  });
});

// B19. The panel lists the household's recent entries, not just this device's,
// so a saved row here can be the other member's — the same reason the ledger
// row names its creator. Pending rows are the reader's own un-sent queue and
// carry no creator to name.
describe('RecentlyAdded creator attribution', () => {
  test('a saved row names who entered it', async () => {
    renderPanel([]);

    const creator = await screen.findByText('Elie');
    // A bare name in a muted line does not announce what it is; the icon is
    // aria-hidden decoration, so the sr-only prefix carries the meaning.
    expect(creator.closest('p')).toHaveTextContent('Entered by Elie');
  });

  test('a pending row is not attributed while the saved row beside it is', async () => {
    renderPanel([queued()]);
    // Both kinds on screen at once: the count below is what makes this
    // non-vacuous. Deleting the attribution entirely gives 0, rendering it on
    // pending rows too gives 2 — only the real behaviour gives exactly 1.
    expect(await screen.findByText('sdsaved')).toBeInTheDocument();
    expect(screen.getByText('sdpending')).toBeInTheDocument();

    // RTL matches an element's own text nodes, so this hits one sr-only
    // prefix per attribution line and nothing else.
    const attributions = screen.getAllByText(/entered by/i);
    expect(attributions).toHaveLength(1);
    expect(attributions[0].closest('li')).toHaveTextContent('sdsaved');
  });

  test('a saved row renders a neutral fallback when the creator account is gone', async () => {
    // "" is the backend's documented "creator unknown" value (the list query's
    // LEFT JOIN found no user row). It must never surface as a blank line.
    apiGet.mockResolvedValue({
      transactions: [savedTxn({ created_by: '' })],
      total: 1,
      page: 1,
      per_page: 5,
    });
    renderPanel([]);

    const fallback = await screen.findByText('Unknown');
    expect(fallback.closest('p')).toHaveTextContent('Entered by Unknown');
  });
});
