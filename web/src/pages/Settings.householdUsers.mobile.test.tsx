import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../hooks/useAuth', () => ({ useAuth: vi.fn() }));

vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>();
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      patch: vi.fn(),
      del: vi.fn(),
      upload: vi.fn(),
    },
  };
});

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { Settings } from './Settings';
import type { User } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

/**
 * The household table's phone presentation: a card list, and one dialog behind
 * each card that holds the four controls the table spreads across a row.
 *
 * The table has Username, Display Name, a role Select and three buttons. At
 * 360px that is a horizontal pan, and the buttons — the entire point of the
 * surface — end up off the right edge. So below `md` the rows become cards with
 * one explicit action each.
 *
 * ONE TREE OR THE OTHER, chosen in JS. Every viewport-gated assertion in this
 * file therefore needs its opposite-width control, because a gated block that
 * never renders makes every assertion about it vacuously true — five tests in
 * this codebase were once green that way.
 */

interface HappyDomWindow {
  happyDOM?: { setViewport: (v: { width?: number; height?: number }) => void };
}

const DESKTOP_WIDTH = 1024;
/** Galaxy S24 — the narrowest device this household actually uses. */
const PHONE_WIDTH = 360;

function setViewportWidth(width: number): void {
  const controllable = window as unknown as HappyDomWindow;
  if (!controllable.happyDOM) {
    // Loud on purpose: a silent fallback would run every case below at
    // happy-dom's default 1024, where the card list does not exist at all.
    throw new Error(
      'happy-dom viewport control is unavailable — mobile tests cannot run',
    );
  }
  controllable.happyDOM.setViewport({ width });
}

const alice: User = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin',
  created_at: '2024-01-01',
};

const bob: User = {
  id: 2,
  username: 'bob',
  display_name: 'Bob',
  role: 'member',
  created_at: '2024-01-01',
};

function withQueryClient({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function setup() {
  // An open Radix Select sets `pointer-events: none` on <body>, and happy-dom
  // has no layout engine to tell the portalled content apart.
  return userEvent.setup({ pointerEventsCheck: 0 });
}

function renderSettings() {
  return render(
    <MemoryRouter initialEntries={['/settings?tab=account']}>
      <Settings />
    </MemoryRouter>,
    { wrapper: withQueryClient },
  );
}

/**
 * Render at phone width, having first proved the swap actually took.
 *
 * BOTH HALVES. The two presentations render the same people under the same
 * card heading, so a weak assertion cannot tell them apart — the control
 * asserts the list is present AND that no `table` exists. Without the absence
 * half, a page that rendered the table at every width would pass most of what
 * follows.
 */
async function renderPhone() {
  setViewportWidth(PHONE_WIDTH);
  renderSettings();
  const list = await screen.findByRole('list', { name: 'Household users' });
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  return list;
}

/** The desktop half of the same control. */
async function renderDesktop() {
  setViewportWidth(DESKTOP_WIDTH);
  renderSettings();
  const table = await screen.findByRole('table');
  expect(
    screen.queryByRole('list', { name: 'Household users' }),
  ).not.toBeInTheDocument();
  return table;
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockedUseAuth.mockReturnValue({
    user: alice,
    loading: false,
    unverified: false,
    refreshUser: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
  mockedApi.get.mockImplementation((path: string) => {
    if (path === 'users') return Promise.resolve([alice, bob]);
    if (path === 'api-tokens') return Promise.resolve({ tokens: [] });
    return Promise.resolve([]);
  });
  mockedApi.put.mockResolvedValue({ status: 'updated' });
  mockedApi.del.mockResolvedValue({});
});

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
});

/**
 * Every element that receives focus from now until `stop()`.
 *
 * The focus TRAFFIC rather than the resting state, because the AlertDialog's
 * FocusScope is trapped and pulls focus back on its own — so "where did focus
 * end up" cannot tell a working handoff from a broken one.
 */
function recordFocus() {
  const focused: Element[] = [];
  const record = (e: Event) => focused.push(e.target as Element);
  document.addEventListener('focusin', record);
  return {
    focused,
    stop: () => document.removeEventListener('focusin', record),
  };
}

/** Open the manage dialog for one card. */
async function openManage(
  user: ReturnType<typeof userEvent.setup>,
  username: string,
) {
  const list = await renderPhone();
  const button = within(list).getByRole('button', {
    name: `Manage ${username}`,
  });
  await user.click(button);
  return { dialog: await screen.findByRole('dialog'), button };
}

describe('which presentation mounts', () => {
  test('below md the table is replaced by a card list', async () => {
    await renderPhone();
    // …and no orphan row survives the swap either.
    expect(screen.queryAllByRole('row')).toHaveLength(0);
  });

  test('at md and above the table is kept, untouched', async () => {
    const table = await renderDesktop();
    // The desktop surface keeps all four per-row controls inline — the card
    // list is an addition below `md`, not a replacement of the table's model.
    expect(
      within(table).getByRole('button', { name: 'Edit display name for bob' }),
    ).toBeInTheDocument();
    expect(
      within(table).getByRole('button', { name: 'Reset password for bob' }),
    ).toBeInTheDocument();
    expect(
      within(table).getByRole('button', { name: 'Delete bob' }),
    ).toBeInTheDocument();
    expect(
      within(table).getByRole('combobox', { name: 'Role for bob' }),
    ).toBeInTheDocument();
  });

  test('the phone offers exactly one action per person', async () => {
    const list = await renderPhone();
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(2);
    for (const item of items) {
      expect(within(item).getAllByRole('button')).toHaveLength(1);
    }
    // …and none of the table's inline controls leaked onto the card.
    expect(
      screen.queryByRole('button', { name: 'Reset password for bob' }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('combobox', { name: 'Role for bob' }),
    ).not.toBeInTheDocument();
  });
});

describe('the card', () => {
  test('leads with the username and follows with the display name', async () => {
    const list = await renderPhone();
    const [first] = within(list).getAllByRole('listitem');
    // Order matters: the username is the login and the stable identifier every
    // aria-label on this surface is built from, so it is what the eye should
    // land on first. `textContent` pins the ORDER, which two separate
    // `getByText` calls would not.
    expect(first).toHaveTextContent(/^aliceAlice/);
  });

  test('keeps the table’s font-mono on the username', async () => {
    const list = await renderPhone();
    const [first] = within(list).getAllByRole('listitem');
    const username = within(first).getByText('alice');
    // Exact token, not a substring: `toContain('font-mono')` would also match
    // a hypothetical `md:font-mono`.
    expect(username.className.split(/\s+/)).toContain('font-mono');
  });

  test('the action is thumb-sized and full width', async () => {
    const list = await renderPhone();
    const button = within(list).getByRole('button', { name: 'Manage alice' });
    const tokens = button.className.split(/\s+/);
    // happy-dom lays nothing out, so the token is the assertion and the pixels
    // are a browser check. `min-h-11` is 44px.
    expect(tokens).toContain('min-h-11');
    expect(tokens).toContain('w-full');
    // NOT paired with a `md:min-h-0`: screen variants are emitted AFTER the
    // pointer variants Button's own floor uses, so that pair would defeat the
    // primitive's floor on the tablet rather than merely look redundant.
    expect(tokens).not.toContain('md:min-h-0');
  });

  test('the list carries an explicit role and a name', async () => {
    const list = await renderPhone();
    // `role="list"` is not redundant: Tailwind's preflight strips the
    // list-style and Safari/VoiceOver drop the list role with it.
    expect(list.getAttribute('role')).toBe('list');
  });
});

describe('the manage dialog', () => {
  test('holds every control the table row does', async () => {
    const user = setup();
    const { dialog } = await openManage(user, 'bob');

    expect(within(dialog).getByLabelText('Display name')).toHaveValue('Bob');
    expect(
      within(dialog).getByRole('combobox', { name: 'Role for bob' }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole('button', { name: 'Reset password for bob' }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole('button', { name: 'Delete bob' }),
    ).toBeInTheDocument();
  });

  test('names the person it is pointed at', async () => {
    const user = setup();
    const { dialog } = await openManage(user, 'bob');
    expect(within(dialog).getByRole('heading')).toHaveTextContent('Manage bob');
  });

  test('hides Reset password on your own row, and keeps Delete there', async () => {
    // Reset is hidden because you rotate your own password in the account card
    // above, which runs the same cascade WITH a current-password check — the
    // admin reset deliberately has none. Delete and the role control ARE
    // offered on your own row and are refused by the backend as a toast; that
    // is the table's behaviour and porting it unchanged is deliberate.
    const user = setup();
    const { dialog } = await openManage(user, 'alice');

    expect(
      within(dialog).queryByRole('button', {
        name: 'Reset password for alice',
      }),
    ).not.toBeInTheDocument();
    expect(
      within(dialog).getByRole('button', { name: 'Delete alice' }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole('combobox', { name: 'Role for alice' }),
    ).toBeInTheDocument();
  });
});

describe('the two writes stay two writes', () => {
  test('saving the name PUTs display_name alone, never the role', async () => {
    const user = setup();
    const { dialog } = await openManage(user, 'bob');

    const field = within(dialog).getByLabelText('Display name');
    await user.clear(field);
    await user.type(field, 'Bobby');
    await user.click(
      within(dialog).getByRole('button', { name: 'Save display name' }),
    );

    await waitFor(() => {
      // Exact object, not objectContaining. handleUpdateUser MERGES, so a
      // payload that also carried `role` would still succeed against the
      // server while making this client authoritative on role: a stale
      // snapshot would revert a role change made elsewhere AND sign that user
      // out of every device. A dialog that looks like one form is exactly the
      // shape that invites the combined PUT.
      expect(mockedApi.put).toHaveBeenCalledWith('users/2', {
        display_name: 'Bobby',
      });
    });
    expect(mockedApi.put).toHaveBeenCalledTimes(1);
  });

  test('changing the role PUTs role alone, never the display name', async () => {
    const user = setup();
    const { dialog } = await openManage(user, 'bob');

    await user.click(
      within(dialog).getByRole('combobox', { name: 'Role for bob' }),
    );
    await user.click(screen.getByRole('option', { name: 'Admin' }));

    await waitFor(() => {
      expect(mockedApi.put).toHaveBeenCalledWith('users/2', { role: 'admin' });
    });
    expect(mockedApi.put).toHaveBeenCalledTimes(1);
  });

  test('the open dialog shows the role the server now holds, not the one it opened with', async () => {
    // The dialog stays OPEN after a role change (unlike the name save), so it
    // has to re-read the row from the refetched list. Reading the snapshot it
    // was opened with leaves the Select showing the old value on a change that
    // succeeded — and a second change would then be decided against a role
    // that is no longer true.
    let promoted = false;
    mockedApi.get.mockImplementation((path: string) => {
      if (path === 'users')
        return Promise.resolve([
          alice,
          promoted ? { ...bob, role: 'admin' } : bob,
        ]);
      if (path === 'api-tokens') return Promise.resolve({ tokens: [] });
      return Promise.resolve([]);
    });
    mockedApi.put.mockImplementation(() => {
      promoted = true;
      return Promise.resolve({ status: 'updated' });
    });

    const user = setup();
    const { dialog } = await openManage(user, 'bob');
    const select = within(dialog).getByRole('combobox', {
      name: 'Role for bob',
    });
    expect(select).toHaveTextContent('Member');

    await user.click(select);
    await user.click(screen.getByRole('option', { name: 'Admin' }));

    await waitFor(() => {
      expect(select).toHaveTextContent('Admin');
    });
  });

  test('the name save closes the dialog', async () => {
    const user = setup();
    const { dialog } = await openManage(user, 'bob');

    const field = within(dialog).getByLabelText('Display name');
    await user.clear(field);
    await user.type(field, 'Bobby');
    await user.click(
      within(dialog).getByRole('button', { name: 'Save display name' }),
    );

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });
});

describe('the confirm handoff is sequential, never nested', () => {
  test('Reset password closes the manage dialog and opens the confirm', async () => {
    const user = setup();
    const { dialog } = await openManage(user, 'bob');

    await user.click(
      within(dialog).getByRole('button', { name: 'Reset password for bob' }),
    );

    const confirm = await screen.findByRole('alertdialog');
    expect(
      within(confirm).getByLabelText('New password'),
    ).toBeInTheDocument();
    // THE nesting assertion. Two stacked Radix modals put `aria-hidden` on the
    // outer one via `hideOthers()`, so it would stop being reachable at all —
    // and two `bg-black/80` scrims leave about 4% of the page visible. One
    // layer at a time.
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  test('Delete closes the manage dialog and opens the confirm', async () => {
    const user = setup();
    const { dialog } = await openManage(user, 'bob');

    await user.click(within(dialog).getByRole('button', { name: 'Delete bob' }));

    const confirm = await screen.findByRole('alertdialog');
    expect(
      within(confirm).getByRole('button', { name: 'Delete user' }),
    ).toBeInTheDocument();
    // Opening the confirmation is not the deletion.
    expect(mockedApi.del).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  test('the confirm still deletes the row the card was pointed at', async () => {
    const user = setup();
    const { dialog } = await openManage(user, 'bob');
    await user.click(within(dialog).getByRole('button', { name: 'Delete bob' }));

    const confirm = await screen.findByRole('alertdialog');
    await user.click(
      within(confirm).getByRole('button', { name: 'Delete user' }),
    );

    await waitFor(() => {
      expect(mockedApi.del).toHaveBeenCalledWith('users/2');
    });
  });

  test('the closing manage dialog never focuses the card behind the confirm', async () => {
    // THE RACE this handoff exists for. The manage dialog is controlled and
    // Trigger-less, so Radix's own restore would focus a null `triggerRef` and
    // drop focus on <body>; the local `onCloseAutoFocus` puts it back on the
    // card instead. On this path it must NOT — the confirm has already claimed
    // focus, and the manage dialog's hook runs afterwards, on unmount.
    //
    // ASSERTED ON THE FOCUS TRAFFIC, not on where focus ends up. The
    // AlertDialog's FocusScope is trapped, so it pulls focus back on its own
    // and the RESTING state is inside the confirm whether the guard fires or
    // not — mutation-tested: bypassing the guard leaves an end-state assertion
    // green. What the guard actually prevents is the card behind the modal
    // being focused at all, which on a phone scrolls the page under the scrim
    // and flickers the focus ring. That IS observable.
    const user = setup();
    const { dialog, button } = await openManage(user, 'bob');
    // Recording starts AFTER the dialog is open: the tap that opened it
    // focused the card button, and counting that would make the assertion
    // below fail for a reason that has nothing to do with the handoff.
    const { focused, stop } = recordFocus();
    try {
      await user.click(
        within(dialog).getByRole('button', { name: 'Delete bob' }),
      );

      const confirm = await screen.findByRole('alertdialog');
      await waitFor(() => {
        expect(confirm.contains(document.activeElement)).toBe(true);
      });
      expect(focused).not.toContain(button);
    } finally {
      stop();
    }
  });

  test('…and the card IS focused when the dialog closes without a confirm', async () => {
    // The positive control for the assertion above: the same listener, the
    // same card button, on the path where the guard must NOT fire. Without
    // this, "the button was never focused" would also pass on a page that
    // never focuses anything.
    const user = setup();
    const { button } = await openManage(user, 'bob');
    const { focused, stop } = recordFocus();
    try {
      await user.keyboard('{Escape}');

      await waitFor(() => {
        expect(focused).toContain(button);
      });
    } finally {
      stop();
    }
  });
});

describe('focus comes back to the card', () => {
  test('after an Escape close', async () => {
    const user = setup();
    const { button } = await openManage(user, 'bob');

    await user.keyboard('{Escape}');

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    // Not <body>. Radix's own restore cannot do this: `DialogContentModal`
    // composes `preventDefault(); triggerRef.current?.focus()`, and this dialog
    // has no `DialogTrigger` to populate that ref.
    await waitFor(() => {
      expect(document.activeElement).toBe(button);
    });
  });

  test('after the name save that closes it', async () => {
    const user = setup();
    const { dialog, button } = await openManage(user, 'bob');

    const field = within(dialog).getByLabelText('Display name');
    await user.clear(field);
    await user.type(field, 'Bobby');
    await user.click(
      within(dialog).getByRole('button', { name: 'Save display name' }),
    );

    await waitFor(() => {
      expect(document.activeElement).toBe(button);
    });
  });
});
