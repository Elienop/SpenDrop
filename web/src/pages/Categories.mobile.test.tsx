import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({ useAuth: vi.fn() }));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { toast } from 'sonner';
import { Categories } from './Categories';

/**
 * Phone-width behaviour of the Categories page.
 *
 * In its own file rather than added to `Categories.test.tsx` because the
 * viewport is per-file state in happy-dom: a `setViewport` leaking into the
 * desktop tests next door would silently move them onto the card branch.
 *
 * WHAT THIS FILE CANNOT PROVE. happy-dom performs no layout and never loads the
 * Tailwind stylesheet, so every width and height here would be zero. The
 * geometry was measured in Chrome against the built container at a 360px
 * viewport with a confirmed coarse pointer, and the numbers live at the call
 * sites in `Categories.tsx`. What is asserted below is the class CONTRACT and
 * the behaviour: which tree mounts, that the card offers every action the row
 * does, and that the tokens the browser measurement depended on are still
 * present. Do not upgrade any assertion here into a claim about pixels.
 */

interface HappyDomWindow {
  happyDOM?: { setViewport: (v: { width?: number; height?: number }) => void };
}

/** Galaxy S24 — the narrowest device this household actually uses. */
const PHONE_WIDTH = 360;
/** happy-dom's default, and what every other test file in this repo runs at. */
const DESKTOP_WIDTH = 1024;
/** Galaxy Tab S10 FE portrait: below `md`, so it takes the phone branch. */
const TABLET_PORTRAIT = 720;

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

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);

/**
 * A name at the server's ceiling. `MaxCategoryNameLength` = 100
 * (`internal/api/limits.go`), and one unbroken run of it is the shape that
 * pans a phone sideways — the reason the name carries a wrap AND a clamp.
 */
const LONG_NAME = `Groceries${'X'.repeat(91)}`;

const mockCategories = [
  {
    id: 1,
    name: 'Food',
    type: 'expense' as const,
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 2,
    name: LONG_NAME,
    type: 'expense' as const,
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 3,
    name: 'Transport',
    type: 'expense' as const,
    icon: null,
    sort_order: 2,
    is_active: false,
    created_at: '2024-01-01',
  },
  {
    id: 4,
    name: 'Salary',
    type: 'income' as const,
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
];

function asRole(role: 'admin' | 'member') {
  mockedUseAuth.mockReturnValue({
    user: {
      id: role === 'admin' ? 1 : 2,
      username: role === 'admin' ? 'elie' : 'bob',
      display_name: role === 'admin' ? 'Elie' : 'Bob',
      role,
      created_at: '2024-01-01',
    },
    loading: false,
    unverified: false,
    refreshUser: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  } as unknown as ReturnType<typeof useAuth>);
}

function renderCategories() {
  return render(
    <MemoryRouter>
      <Categories />
    </MemoryRouter>,
  );
}

function setup() {
  // An open Radix menu sets `pointer-events: none` on <body>, and happy-dom has
  // no layout engine to tell the portalled content apart.
  return userEvent.setup({ pointerEventsCheck: 0 });
}

/**
 * Render at phone width, having first proved the presentation actually swapped.
 *
 * THE POSITIVE CONTROL, and it is not decoration. Almost every assertion below
 * would be equally true of the table — the names render either way, the actions
 * menu is the same component, "(inactive)" appears in both. So if
 * `setViewportWidth` ever stops taking (a renamed happy-dom API, a different
 * environment, a deleted `beforeEach`), the hook answers `false`, the DESKTOP
 * tree mounts, and most of this file would go green while asserting the wrong
 * presentation entirely. Both halves are checked — the list is present AND the
 * table is gone — because either alone can be satisfied by a dual-render.
 */
async function renderPhone(role: 'admin' | 'member' = 'admin') {
  asRole(role);
  setViewportWidth(PHONE_WIDTH);
  renderCategories();
  await screen.findByRole('list', { name: 'Categories' });
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  return screen.getByRole('list', { name: 'Categories' });
}

/** The desktop half of the same control. */
async function renderDesktop(role: 'admin' | 'member' = 'admin') {
  asRole(role);
  setViewportWidth(DESKTOP_WIDTH);
  renderCategories();
  await screen.findByRole('table');
  expect(
    screen.queryByRole('list', { name: 'Categories' }),
  ).not.toBeInTheDocument();
}

const classes = (el: Element) => el.className.split(/\s+/);

/** The card for one category, found through the text the user reads. */
function cardFor(name: string): HTMLElement {
  const card = screen.getByText(name).closest('li');
  expect(card).not.toBeNull();
  return card as HTMLElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockedApi.get.mockResolvedValue(mockCategories);
  asRole('admin');
});

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
});

describe('which presentation mounts', () => {
  test('below md the table is replaced by a card list', async () => {
    const list = await renderPhone();
    // …and no orphan row survives the swap either.
    expect(screen.queryAllByRole('row')).toHaveLength(0);
    expect(within(list).getAllByRole('listitem')).toHaveLength(
      mockCategories.length,
    );
    // The ATTRIBUTE, not the role — `getByRole('list')` resolves the implicit
    // role of a `<ul>` and passes with `role="list"` deleted. It is not
    // redundant markup: Tailwind's preflight sets `list-style: none` and
    // Safari/VoiceOver drop the list role along with the marker, which is a
    // browser behaviour no assertion in this environment can observe.
    expect(list.getAttribute('role')).toBe('list');
  });

  test('at md and above the table is kept', async () => {
    await renderDesktop();
    // 1 header row + 4 data rows, and no card list beside them.
    expect(screen.getAllByRole('row')).toHaveLength(mockCategories.length + 1);
  });

  test('a 720px tablet in portrait is below md and gets the cards', async () => {
    // The gate is WIDTH, not pointer capability — this is a room question, and
    // the household's Tab S10 FE in portrait is the case that separates the
    // two hooks. (Its ~1130px landscape stays on the table, which is correct:
    // there the room exists.)
    asRole('admin');
    setViewportWidth(TABLET_PORTRAIT);
    renderCategories();
    await screen.findByRole('list', { name: 'Categories' });
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });
});

describe('the card carries what the row carried', () => {
  test('every category is one list item with its name and type', async () => {
    await renderPhone();
    expect(within(cardFor('Food')).getByText('Expense')).toBeInTheDocument();
    expect(within(cardFor('Salary')).getByText('Income')).toBeInTheDocument();
    expect(cardFor(LONG_NAME)).toBeInTheDocument();
  });

  test('a deactivated category is still marked as one', async () => {
    await renderPhone();
    expect(cardFor('Transport')).toHaveTextContent(/\(inactive\)/);
    // Scoped, not page-wide: the marker belongs to the row it describes.
    expect(cardFor('Food')).not.toHaveTextContent(/\(inactive\)/);
  });

  test('an inactive card fades its identity but not its control', async () => {
    // The table fades the whole row. Here the actions trigger is the card's
    // only control and a thumb target, so the fade stops at the identity block.
    //
    // ASSERTED ON THE CARD, not on the trigger, and the difference is the whole
    // test. `opacity` inherits its effect down the subtree — a fade on the `li`
    // dims the button without appearing in the button's own class list — so the
    // obvious `expect(classes(trigger)).not.toContain('opacity-60')` passes
    // with the fade sitting on the container. Mutation-proven: adding
    // `opacity-60` to the `li` left all eighteen tests green until this
    // assertion moved up to the ancestor that would carry it.
    await renderPhone();
    const card = cardFor('Transport');
    expect(classes(screen.getByText('Transport').parentElement!)).toContain(
      'opacity-60',
    );
    expect(classes(card)).not.toContain('opacity-60');
    // Positive control for the line above: an ACTIVE card's identity block has
    // no fade either, so "not on the card" only means something alongside
    // "yes on the inactive identity block".
    expect(classes(screen.getByText('Food').parentElement!)).not.toContain(
      'opacity-60',
    );
  });

  test('the two presentations offer the same three actions', async () => {
    // A card that quietly dropped Delete would leave the phone unable to do
    // something the desktop can, and nothing else here would fail. Compared
    // rather than hard-coded so the check keeps its teeth when a fourth action
    // is added to one surface.
    const user = setup();
    await renderPhone('admin');
    await user.click(screen.getByRole('button', { name: 'Actions for Food' }));
    const phone = screen
      .getAllByRole('menuitem')
      .map((i) => i.textContent);
    expect(phone).toEqual(['Edit', 'Deactivate', 'Delete']);

    // `cleanup()` and a fresh mount: the viewport gate is read at render, so a
    // `rerender` would keep the old tree.
    cleanup();
    await renderDesktop('admin');
    await user.click(screen.getByRole('button', { name: 'Actions for Food' }));
    expect(screen.getAllByRole('menuitem').map((i) => i.textContent)).toEqual(
      phone,
    );
  });

  test('an inactive card offers Activate, as its row does', async () => {
    const user = setup();
    await renderPhone('admin');
    await user.click(
      screen.getByRole('button', { name: 'Actions for Transport' }),
    );
    expect(
      screen.getByRole('menuitem', { name: 'Activate' }),
    ).toBeInTheDocument();
  });

  test('a member gets no actions, and still gets the list', async () => {
    // BOTH halves: asserting only the absence would pass on a page that failed
    // to render anything at all.
    await renderPhone('member');
    expect(screen.getAllByRole('listitem')).toHaveLength(
      mockCategories.length,
    );
    expect(
      screen.queryByRole('button', { name: /^Actions for/ }),
    ).not.toBeInTheDocument();
  });
});

describe('the actions work from a card', () => {
  test('Edit opens the editor seeded with that category', async () => {
    const user = setup();
    await renderPhone('admin');
    await user.click(screen.getByRole('button', { name: 'Actions for Food' }));
    await user.click(screen.getByRole('menuitem', { name: 'Edit' }));

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByLabelText(/name/i)).toHaveValue('Food');
  });

  test('Deactivate PATCHes the category it belongs to', async () => {
    // Named per card, so this also proves the handler is bound to the right
    // row — a shared closure over the wrong `cat` would still open a menu.
    const user = setup();
    mockedApi.patch.mockResolvedValue({});
    await renderPhone('admin');
    await user.click(
      screen.getByRole('button', { name: 'Actions for Salary' }),
    );
    await user.click(screen.getByRole('menuitem', { name: 'Deactivate' }));

    await waitFor(() => {
      expect(mockedApi.patch).toHaveBeenCalledWith('categories/4', {
        is_active: false,
      });
    });
  });

  test('Delete confirms first, then DELETEs the row it was opened for', async () => {
    // The confirm sits above the presentation fork, shared with the desktop
    // table — this proves the phone branch reaches the SAME dialog rather
    // than keeping the old two-tap direct delete, which an agent literally
    // performed by accident while measuring this page.
    const user = setup();
    mockedApi.del.mockResolvedValue(undefined);
    await renderPhone('admin');
    await user.click(screen.getByRole('button', { name: 'Actions for Food' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));

    const confirm = await screen.findByRole('alertdialog');
    expect(confirm).toHaveTextContent('Delete Food?');
    // Opening the confirmation is not the deletion.
    expect(mockedApi.del).not.toHaveBeenCalled();

    await user.click(
      within(confirm).getByRole('button', { name: 'Delete category' }),
    );
    await waitFor(() => {
      expect(mockedApi.del).toHaveBeenCalledWith('categories/1');
    });
  });

  test('a refusal reaches a toast, and the confirm stays open to report it', async () => {
    // A toast rather than the page banner the pre-confirm wiring used: the
    // banner renders BEHIND the confirm's overlay, where the 409's sentence
    // would never be seen.
    const user = setup();
    const refusal =
      'cannot delete category that has transactions — deactivate it instead, or reassign its transactions first';
    mockedApi.del.mockRejectedValueOnce(new Error(refusal));
    await renderPhone('admin');
    await user.click(screen.getByRole('button', { name: 'Actions for Food' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));

    const confirm = await screen.findByRole('alertdialog');
    await user.click(
      within(confirm).getByRole('button', { name: 'Delete category' }),
    );

    await waitFor(() => {
      expect(mockedToast.error).toHaveBeenCalledWith(refusal);
    });
    expect(screen.getByRole('alertdialog')).toBeInTheDocument();
  });
});

describe('focus never drops to <body>', () => {
  /*
    The overlays on this page — the editor sheet and the delete confirm — are
    Trigger-less (`open={…}` with no SheetTrigger/AlertDialogTrigger), so
    Radix's own restore composes `preventDefault(); triggerRef.current?.focus()`
    against a null ref and every close would land on <body>. Each close path
    below therefore has an explicit destination, and each is asserted on
    `document.activeElement` — the same idiom Settings' household-users mobile
    tests use, proven to observe Radix's close-auto-focus in happy-dom.
  */

  test('Escape from the row menu keeps focus on its trigger (Radix restore, unbroken)', async () => {
    // The EXISTING behaviour, pinned: this menu deliberately does NOT opt out
    // of the dropdown primitive's focus restore (commit 2897054's exception is
    // TransactionRow's menu, whose trigger unmounts — this one survives).
    const user = setup();
    await renderPhone('admin');
    const trigger = screen.getByRole('button', { name: 'Actions for Food' });
    await user.click(trigger);
    await screen.findByRole('menu');
    await user.keyboard('{Escape}');

    await waitFor(() => {
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(trigger);
    });
  });

  test('closing the edit sheet returns focus to that row\'s trigger', async () => {
    const user = setup();
    await renderPhone('admin');
    const trigger = screen.getByRole('button', { name: 'Actions for Food' });
    await user.click(trigger);
    await user.click(screen.getByRole('menuitem', { name: 'Edit' }));
    await screen.findByRole('dialog');

    await user.keyboard('{Escape}');

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(trigger);
    });
  });

  test('closing the Add sheet returns focus to the Add category button', async () => {
    // The other anchor: a create was opened from the page button, not a row.
    const user = setup();
    await renderPhone('admin');
    const addButton = screen.getByRole('button', { name: 'Add category' });
    await user.click(addButton);
    await screen.findByRole('dialog');

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(addButton);
    });
  });

  test('Cancel in the delete confirm returns focus to the row trigger', async () => {
    const user = setup();
    mockedApi.del.mockResolvedValue(undefined);
    await renderPhone('admin');
    const trigger = screen.getByRole('button', { name: 'Actions for Food' });
    await user.click(trigger);
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    const confirm = await screen.findByRole('alertdialog');

    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(trigger);
    });
    // The gate itself: backing out never touched the API.
    expect(mockedApi.del).not.toHaveBeenCalled();
  });

  test('a confirmed delete parks focus on the page heading', async () => {
    // The row — and the trigger every other path returns to — is gone, so the
    // heading is the anchor, per the Transactions post-delete precedent.
    const user = setup();
    mockedApi.del.mockResolvedValue(undefined);
    await renderPhone('admin');
    await user.click(screen.getByRole('button', { name: 'Actions for Food' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    const confirm = await screen.findByRole('alertdialog');

    await user.click(
      within(confirm).getByRole('button', { name: 'Delete category' }),
    );

    await waitFor(() => {
      expect(mockedApi.del).toHaveBeenCalledWith('categories/1');
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole('heading', { level: 1, name: 'Categories' }),
      );
    });
  });
});

describe('a 100-character name is bounded in both directions', () => {
  // The pairing, not either half. `overflow-wrap:anywhere` bounds the WIDTH (a
  // `break-words` leaves the min-content contribution at the full token, so the
  // box keeps sizing to it); `line-clamp-2` bounds the HEIGHT that wrapping
  // alone would trade it for. Same rule the reports seam test enforces for the
  // table cells, asserted here because the rule travels with the file it
  // governs. The pixel effect is the browser's job — see this file's header.

  test('the name carries the wrap and the clamp together', async () => {
    await renderPhone();
    const tokens = classes(screen.getByText(LONG_NAME));
    expect(tokens).toContain('[overflow-wrap:anywhere]');
    expect(tokens).toContain('line-clamp-2');
  });

  test('its column can actually shrink', async () => {
    // The load-bearing half, and the one that is invisible when missing: a flex
    // item's automatic minimum is `min-content`, so without `min-w-0` the
    // column refuses to shrink however the text inside it is allowed to wrap,
    // and the clamp above bounds nothing.
    await renderPhone();
    const column = screen.getByText(LONG_NAME).parentElement!;
    expect(classes(column)).toContain('min-w-0');
  });

  test('every card name is bounded, not just the long one', async () => {
    // Positive control against a fixture-shaped fix: the bound belongs to the
    // component, not to the one row this file made long.
    await renderPhone();
    for (const cat of mockCategories) {
      const tokens = classes(screen.getByText(cat.name));
      expect(tokens).toContain('[overflow-wrap:anywhere]');
      expect(tokens).toContain('line-clamp-2');
    }
  });
});

describe('the controls are thumb-sized', () => {
  test('the card trigger takes the 44px floor on both axes', async () => {
    // Square target, so both axes: floored in height alone it is a 44x32
    // rectangle — taller, still a miss. Unconditional rather than `coarse:`
    // because this subtree only exists below `md`, where the tablet-vs-phone
    // question the pointer gate answers cannot arise, and it agrees with the
    // primitive's own coarse floor on the value.
    await renderPhone();
    const tokens = classes(
      screen.getByRole('button', { name: 'Actions for Food' }),
    );
    expect(tokens).toContain('min-h-11');
    expect(tokens).toContain('min-w-11');
    // The desktop density class must NOT have travelled with it: `size-8` and
    // `min-*` are different tailwind-merge groups, so both would survive and
    // the 32px box would be back with a floor bolted on.
    expect(tokens).not.toContain('size-8');
  });

  test('the desktop trigger keeps its density', async () => {
    // The other half of the same control. Without this, applying the phone
    // classes at every width would pass the test above.
    await renderDesktop();
    const tokens = classes(
      screen.getByRole('button', { name: 'Actions for Food' }),
    );
    expect(tokens).toContain('size-8');
    expect(tokens).not.toContain('min-h-11');
  });

  test('the menu items carry the floor, not only the trigger', async () => {
    // Browser-measured before this existed, at 360px with a coarse pointer: the
    // trigger was already 44px while all three items were 32px. The tap that
    // actually performs the action was the one under the floor.
    //
    // `coarse:`-gated because this menu is shared with the desktop table, where
    // an unconditional floor would inflate a mouse surface the owner asked to
    // leave alone.
    const user = setup();
    await renderPhone('admin');
    await user.click(screen.getByRole('button', { name: 'Actions for Food' }));
    const items = screen.getAllByRole('menuitem');
    expect(items).toHaveLength(3);
    for (const item of items) {
      expect(classes(item)).toContain('coarse:min-h-11');
      // Not the width-gated form, which switches OFF above 768px and would
      // hand the household's ~1130px touch tablet the 32px item back.
      expect(classes(item)).not.toContain('md:min-h-0');
    }
  });
});
