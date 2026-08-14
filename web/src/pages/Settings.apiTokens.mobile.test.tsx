import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, render, screen, waitFor, within } from '@testing-library/react';
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
import { toast } from 'sonner';
import { Settings } from './Settings';
import type { ApiToken, CreateTokenResponse } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

/**
 * The API tokens table's phone presentation, and the one-time reveal that has
 * to outlive a rotation.
 *
 * The table has six columns and panned 308px at 360px, with the Actions column
 * — the Revoke button, the only thing this surface can DO — furthest out. The
 * reveal was worse than a pan: crossing `md` swaps `<Tabs>` for the phone's
 * section `<Select>`, which unmounts the whole section, and the plaintext went
 * with it. The server hashes tokens at rest, so that token is gone; the user's
 * only remedy is to revoke and re-mint one they never saw.
 *
 * Viewport-gated blocks get their opposite-width control on every assertion:
 * a block that never renders makes every claim about it vacuously true.
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

/**
 * Flip the viewport mid-test, the way a rotation does.
 *
 * happy-dom's `matchMedia` evaluates against the live `innerWidth` and does
 * dispatch `change` when `setViewport` moves it, so
 * `useIsMobileViewport`'s `useSyncExternalStore` subscription re-renders on
 * its own; `act` is what flushes that render.
 *
 * WITH ONE TRAP, and it decides the shape of the cases below.
 * `MediaQueryList.addEventListener` opens with `let matchesState = false` —
 * hardcoded, rather than seeded from the query's CURRENT value — and only
 * dispatches when `matches !== matchesState`. So a listener registered while
 * the query already matches (i.e. subscribed at desktop width, where
 * `min-width: 768px` is true) has a state variable that disagrees with
 * reality, and the first narrowing to phone width computes `false === false`
 * and is SWALLOWED. The transition back out fires, and every flip after that
 * is correct.
 *
 * Consequence: "started at desktop, narrowed once" is not observable in this
 * environment, so a desktop-ward case has to reach phone width via a round
 * trip rather than a single flip. Measured on happy-dom as vendored here —
 * `node_modules/happy-dom/lib/match-media/MediaQueryList.js`.
 */
function rotateTo(width: number): void {
  act(() => {
    setViewportWidth(width);
  });
}

const PLAINTEXT = 'spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123';

const homepage: ApiToken = {
  id: 7,
  name: 'Homepage dashboard',
  token_prefix: 'spdr_aB3xQ9z7kL',
  created_at: '2026-08-01T10:00:00Z',
  last_used_at: null,
  last_used_ip: null,
  expires_at: null,
};

const backup: ApiToken = {
  id: 8,
  name: 'Backup script',
  token_prefix: 'spdr_ZzYyXxWwVvU',
  created_at: '2026-08-10T10:00:00Z',
  last_used_at: '2026-08-13T10:00:00Z',
  last_used_ip: '192.168.1.42',
  expires_at: '2030-01-01T00:00:00Z',
};

const created: CreateTokenResponse = {
  id: 9,
  name: 'New token',
  token_prefix: 'spdr_aB3xQ9z7kL',
  created_at: '2026-08-14T10:00:00Z',
  last_used_at: null,
  last_used_ip: null,
  expires_at: null,
  token: PLAINTEXT,
};

function withQueryClient({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function renderSettings() {
  return render(
    <MemoryRouter initialEntries={['/settings?tab=api-tokens']}>
      <Settings />
    </MemoryRouter>,
    { wrapper: withQueryClient },
  );
}

function setup() {
  // An open Radix Select sets `pointer-events: none` on <body>, and happy-dom
  // has no layout engine to tell the portalled content apart.
  return userEvent.setup({ pointerEventsCheck: 0 });
}

/** Render at phone width, having first proved the swap actually took. */
async function renderPhone() {
  setViewportWidth(PHONE_WIDTH);
  renderSettings();
  const list = await screen.findByRole('list', { name: 'API tokens' });
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  return list;
}

/** The desktop half of the same control. */
async function renderDesktop() {
  setViewportWidth(DESKTOP_WIDTH);
  renderSettings();
  const table = await screen.findByRole('table');
  expect(
    screen.queryByRole('list', { name: 'API tokens' }),
  ).not.toBeInTheDocument();
  return table;
}

function seedTokens(tokens: ApiToken[]) {
  mockedApi.get.mockImplementation((path: string) => {
    if (path === 'api-tokens') return Promise.resolve({ tokens });
    return Promise.resolve([]);
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockedUseAuth.mockReturnValue({
    user: { id: 1, username: 'elie', display_name: 'Elie', role: 'admin' },
  } as unknown as ReturnType<typeof useAuth>);
  seedTokens([homepage, backup]);
  mockedApi.del.mockResolvedValue({ ok: true });
  mockedApi.post.mockResolvedValue(created);
});

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
});

describe('which presentation mounts', () => {
  test('below md the token table is replaced by a card list', async () => {
    const list = await renderPhone();
    expect(within(list).getAllByRole('listitem')).toHaveLength(2);
    // …and no orphan row survived the swap.
    expect(screen.queryAllByRole('row')).toHaveLength(0);
  });

  test('at md and above the table is kept, untouched', async () => {
    const table = await renderDesktop();
    // Header row plus one per token.
    expect(within(table).getAllByRole('row')).toHaveLength(3);
    expect(
      within(table).getByRole('button', { name: 'Revoke Backup script' }),
    ).toBeInTheDocument();
  });

  test('the empty state is shared — no list, no table, at either width', async () => {
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    expect(await screen.findByText(/no api tokens yet/i)).toBeInTheDocument();
    expect(
      screen.queryByRole('list', { name: 'API tokens' }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });
});

describe('what a token card carries', () => {
  test('all three fact columns survive the fold, each still named', async () => {
    const list = await renderPhone();
    const [, second] = within(list).getAllByRole('listitem');

    expect(within(second).getByText('Backup script')).toBeInTheDocument();
    expect(within(second).getByText('spdr_ZzYyXxWwVvU')).toBeInTheDocument();

    // A table's values are named by column headers; a card that dropped them
    // would leave "Never" sitting on its own with nothing saying which of the
    // three dates it is. The `<dl>` puts the names back.
    const terms = within(second)
      .getAllByRole('term')
      .map((t) => t.textContent);
    expect(terms).toEqual(['Last used', 'Expires', 'Created']);
    const definitions = within(second)
      .getAllByRole('definition')
      .map((d) => d.textContent);
    expect(definitions[0]).toContain('192.168.1.42');
    // Not "Expired": this one runs to 2030.
    expect(definitions[1]).not.toBe('Expired');
  });

  /**
   * The card renders only below `md`, where nothing hovers — so an absolute
   * date parked in a `title` attribute was unreachable on the one presentation
   * that could not reveal it, and reachable on the one that has a mouse.
   */
  test('the absolute created date is readable text, not a hover-only title', async () => {
    const list = await renderPhone();
    const [, second] = within(list).getAllByRole('listitem');
    const created = within(second).getAllByRole('definition')[2];
    // Computed the same way the component does, so the assertion is portable
    // across the runner's locale and timezone rather than pinning a format.
    const absolute = new Date(backup.created_at).toLocaleDateString();
    expect(created.textContent).toContain(absolute);
    // The relative phrase stays — it is what the eye scans for.
    expect(created.textContent).toMatch(/ago|month|year|day/i);
    // And the fact is not hiding in an attribute a thumb cannot open.
    expect(created).not.toHaveAttribute('title');
  });

  test('a token that has never been used and never expires reads as such', async () => {
    const list = await renderPhone();
    const [first] = within(list).getAllByRole('listitem');
    const definitions = within(first)
      .getAllByRole('definition')
      .map((d) => d.textContent);
    expect(definitions[0]).toBe('Never used');
    expect(definitions[1]).toBe('Never');
  });

  /**
   * The table was not the only thing that panned. The card header puts a
   * two-line description and two `whitespace-nowrap` buttons in one row, and
   * each is about half a 360px viewport, so as a single row its min-content
   * ran past the card too. Layout only — no duplicated controls — so it is a
   * CSS gate rather than the JS one the row list uses, and the pin has to
   * carry BOTH halves: the stacked default and the `md:` restoration.
   */
  test('the card header stacks below md and only re-forms a row above it', async () => {
    await renderPhone();
    // Walked structurally rather than by class, so the query does not depend
    // on the very class it is about to assert: description → its wrapper →
    // the CardHeader that lays the wrapper out beside the buttons.
    const header =
      screen.getByText(/personal access tokens for scripts/i).parentElement
        ?.parentElement;
    if (!header) throw new Error('card header not found');
    const tokens = Array.from(header.classList);
    expect(tokens).toContain('flex-col');
    expect(tokens).toContain('md:flex-row');
    // The unprefixed `flex-row` is the mutant this guards: it would restore
    // the single row at every width, phone included.
    expect(tokens).not.toContain('flex-row');
  });

  test('Revoke on a card opens the confirm and revokes that token', async () => {
    const user = setup();
    const list = await renderPhone();
    await user.click(
      within(list).getByRole('button', { name: 'Revoke Backup script' }),
    );
    const confirm = await screen.findByRole('alertdialog');
    // The confirm quotes the name back, so the card and the confirm have to
    // agree on which token is about to stop working.
    expect(within(confirm).getByText(/Backup script/)).toBeInTheDocument();
    await user.click(
      within(confirm).getByRole('button', { name: /^revoke token$/i }),
    );
    await waitFor(() => {
      expect(mockedApi.del).toHaveBeenCalledWith('api-tokens/8');
    });
    await waitFor(() => {
      expect(
        screen.queryByRole('button', { name: 'Revoke Backup script' }),
      ).not.toBeInTheDocument();
    });
    // The other token is untouched — this is a per-row action, not revoke-all.
    expect(
      screen.getByRole('button', { name: 'Revoke Homepage dashboard' }),
    ).toBeInTheDocument();
  });
});

/**
 * Neither revoke confirm has an `AlertDialogTrigger` — none exists anywhere in
 * this app — so Radix's own restore ran `triggerRef.current?.focus()` against
 * null and every close, cancel and confirm alike, dropped focus on `<body>`.
 *
 * Both arms matter and they land in different places. A CANCEL returns to the
 * control that opened the confirm, which is where the user's attention was. A
 * CONFIRMED revoke destroys that control along with its row, so it falls back
 * to the Create token button — the one control on this card that survives
 * every list state, the empty one a revoke-all produces included.
 *
 * The per-token anchor is exercised from BOTH presentations, because the card
 * and the table render different Revoke buttons for the same token and only a
 * shared attribute makes one query find whichever is mounted.
 */
describe('closing a revoke confirm never drops focus on the body', () => {
  async function openRevokeFrom(
    user: ReturnType<typeof userEvent.setup>,
    scope: HTMLElement,
    name: string,
  ) {
    const opener = within(scope).getByRole('button', { name });
    await user.click(opener);
    return { opener, confirm: await screen.findByRole('alertdialog') };
  }

  test('cancel from a CARD returns focus to that card\'s Revoke button', async () => {
    const user = setup();
    const list = await renderPhone();
    const { opener, confirm } = await openRevokeFrom(
      user,
      list,
      'Revoke Backup script',
    );
    await user.click(within(confirm).getByRole('button', { name: /cancel/i }));
    await waitFor(() => {
      expect(document.activeElement).not.toBe(document.body);
      expect(document.activeElement).toBe(opener);
    });
    // Nothing was revoked by cancelling.
    expect(mockedApi.del).not.toHaveBeenCalled();
  });

  test('cancel from a TABLE row returns focus to that row\'s Revoke button', async () => {
    const user = setup();
    const table = await renderDesktop();
    const { opener, confirm } = await openRevokeFrom(
      user,
      table,
      'Revoke Backup script',
    );
    await user.click(within(confirm).getByRole('button', { name: /cancel/i }));
    await waitFor(() => {
      expect(document.activeElement).not.toBe(document.body);
      expect(document.activeElement).toBe(opener);
    });
  });

  test('a CONFIRMED revoke lands focus on Create token, not the body', async () => {
    const user = setup();
    const list = await renderPhone();
    const { confirm } = await openRevokeFrom(
      user,
      list,
      'Revoke Backup script',
    );
    await user.click(
      within(confirm).getByRole('button', { name: /^revoke token$/i }),
    );
    await waitFor(() => {
      expect(mockedApi.del).toHaveBeenCalledWith('api-tokens/8');
    });
    // The opener went with the row it belonged to, so this is the fallback
    // arm — and it has to be a real control, not `<body>`.
    await waitFor(() => {
      expect(document.activeElement).not.toBe(document.body);
      expect(document.activeElement).toBe(
        screen.getByRole('button', { name: /^create token$/i }),
      );
    });
  });

  test('cancel on Revoke all returns focus to the Revoke all button', async () => {
    const user = setup();
    await renderPhone();
    const opener = screen.getByRole('button', { name: /^revoke all \(2\)$/i });
    await user.click(opener);
    const confirm = await screen.findByRole('alertdialog');
    await user.click(within(confirm).getByRole('button', { name: /cancel/i }));
    await waitFor(() => {
      expect(document.activeElement).not.toBe(document.body);
      expect(document.activeElement).toBe(opener);
    });
  });

  test('a CONFIRMED revoke-all lands focus on Create token', async () => {
    const user = setup();
    mockedApi.del.mockResolvedValue({ revoked: 2 });
    await renderPhone();
    await user.click(screen.getByRole('button', { name: /^revoke all \(2\)$/i }));
    const confirm = await screen.findByRole('alertdialog');
    await user.click(
      within(confirm).getByRole('button', { name: /^revoke all \(2\)$/i }),
    );
    await waitFor(() => {
      expect(mockedApi.del).toHaveBeenCalledWith('api-tokens');
    });
    // Revoke all removes ITSELF along with the last row, so even the opener
    // for this dialog is gone by the time it closes.
    await waitFor(() => {
      expect(
        screen.queryByRole('button', { name: /^revoke all/i }),
      ).not.toBeInTheDocument();
      expect(document.activeElement).not.toBe(document.body);
      expect(document.activeElement).toBe(
        screen.getByRole('button', { name: /^create token$/i }),
      );
    });
  });
});

/**
 * A create that FAILS on the way back can still have written the token: the
 * server commits before it responds, so a timeout or a 5xx leaves a live
 * credential the user never saw. Without a re-read it stays invisible — and
 * therefore unrevokable — until something else remounts the section.
 */
describe('a failed create still surfaces an orphan token', () => {
  test('the list is re-read after a create error', async () => {
    const user = setup();
    seedTokens([]);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);

    // The POST fails, but the server had already written the row — so the
    // NEXT list read returns it.
    mockedApi.post.mockRejectedValueOnce(new Error('gateway timeout'));
    seedTokens([homepage]);

    await user.click(screen.getByRole('button', { name: /^create token$/i }));
    await user.type(screen.getByLabelText(/^name$/i), 'New token');
    const dialog = screen.getByRole('dialog');
    await user.click(
      within(dialog).getByRole('button', { name: /^create token$/i }),
    );

    // The failure is reported…
    await waitFor(() => {
      expect(vi.mocked(toast).error).toHaveBeenCalledWith('gateway timeout');
    });
    // …and the list was re-read rather than left as the user last saw it.
    await waitFor(() => {
      expect(
        mockedApi.get.mock.calls.filter(([path]) => path === 'api-tokens'),
      ).toHaveLength(2);
    });

    // The create dialog stays open on failure (the user may want to retry),
    // and a Radix modal puts `aria-hidden` on everything behind it — so the
    // row is asserted after dismissing the dialog, which is also the order a
    // real user meets it in.
    await user.keyboard('{Escape}');
    // The orphan is now reachable with its own Revoke button, which is the
    // whole point: a token you cannot see is a token you cannot revoke.
    expect(
      await screen.findByRole('button', { name: 'Revoke Homepage dashboard' }),
    ).toBeInTheDocument();
  });
});

/**
 * The invariant this whole file exists for.
 *
 * A revealed token must stay fully readable until the user dismisses it,
 * including across an md-cross layout swap in EITHER direction. The reveal is
 * therefore held above the swap, in `NewTokenRevealProvider` — so these cases
 * are not "does it come back", they are "does it ever go away".
 */
describe('the one-time reveal survives a rotation', () => {
  async function createToken(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole('button', { name: /^create token$/i }));
    await user.type(screen.getByLabelText(/^name$/i), 'New token');
    const dialog = screen.getByRole('dialog');
    await user.click(
      within(dialog).getByRole('button', { name: /^create token$/i }),
    );
    await screen.findByRole('heading', { name: /save your new token/i });
  }

  /** The whole plaintext, read off whatever control is presenting it. */
  function revealedValue(): string {
    const field = screen.getByLabelText(
      /your new api token/i,
    ) as HTMLTextAreaElement;
    return field.value;
  }

  test('phone → desktop keeps the whole token on screen', async () => {
    const user = setup();
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await createToken(user);
    expect(revealedValue()).toBe(PLAINTEXT);

    rotateTo(DESKTOP_WIDTH);

    // The swap really happened — without this the case would pass on a page
    // that ignored the viewport entirely.
    //
    // `hidden: true` on both halves: the reveal is a modal, and Radix's
    // `hideOthers()` puts `aria-hidden` on everything behind it. The default
    // queries skip hidden nodes, so the presence half would never find the tab
    // strip AND the absence half would pass on a page that still had the
    // Select — the exact vacuity this control exists to prevent.
    await screen.findByRole('tablist', { hidden: true });
    expect(
      screen.queryByRole('combobox', {
        name: 'Settings section',
        hidden: true,
      }),
    ).not.toBeInTheDocument();
    // …and the reveal is still up, with every character of the token.
    expect(
      screen.getByRole('heading', { name: /save your new token/i }),
    ).toBeInTheDocument();
    expect(revealedValue()).toBe(PLAINTEXT);
  });

  test('desktop → phone keeps the whole token on screen', async () => {
    const user = setup();
    seedTokens([]);
    // Reached by a round trip rather than a single narrowing: happy-dom
    // swallows the first true → false transition of a query that already
    // matched when the listener registered, so a desktop-start narrowing
    // produces no event at all here. See `rotateTo`. The crossing under test
    // is still a genuine desktop → phone one — it is the second flip, which
    // does dispatch — and the extra crossing at the front is if anything a
    // harder case than the one the invariant asks for.
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await createToken(user);
    rotateTo(DESKTOP_WIDTH);
    await screen.findByRole('tablist', { hidden: true });
    expect(revealedValue()).toBe(PLAINTEXT);

    rotateTo(PHONE_WIDTH);

    // `hidden: true` for the same reason as the case above — see its note.
    await screen.findByRole('combobox', {
      name: 'Settings section',
      hidden: true,
    });
    expect(
      screen.queryByRole('tablist', { hidden: true }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: /save your new token/i }),
    ).toBeInTheDocument();
    expect(revealedValue()).toBe(PLAINTEXT);
  });

  test('a rotation between the POST and its response still lands the token', async () => {
    const user = setup();
    seedTokens([]);
    // Hold the create request open so the rotation lands inside the window
    // where the section that fired it is torn down.
    let resolvePost: (body: CreateTokenResponse) => void = () => {};
    mockedApi.post.mockImplementation(
      () =>
        new Promise<CreateTokenResponse>((resolve) => {
          resolvePost = resolve;
        }) as ReturnType<typeof api.post>,
    );
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await user.click(screen.getByRole('button', { name: /^create token$/i }));
    await user.type(screen.getByLabelText(/^name$/i), 'New token');
    const dialog = screen.getByRole('dialog');
    await user.click(
      within(dialog).getByRole('button', { name: /^create token$/i }),
    );

    rotateTo(DESKTOP_WIDTH);
    await act(async () => {
      resolvePost(created);
    });

    expect(
      await screen.findByRole('heading', { name: /save your new token/i }),
    ).toBeInTheDocument();
    expect(revealedValue()).toBe(PLAINTEXT);
  });

  test('dismissing it is still the only way out, and it does not come back', async () => {
    const user = setup();
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await createToken(user);

    // Escape is inert while the plaintext is up — the whole point of a
    // one-time secret is that a stray tap cannot destroy it.
    await user.keyboard('{Escape}');
    expect(revealedValue()).toBe(PLAINTEXT);

    await user.click(
      screen.getByRole('button', { name: /i've saved my token/i }),
    );
    await waitFor(() => {
      expect(
        screen.queryByRole('heading', { name: /save your new token/i }),
      ).not.toBeInTheDocument();
    });
    // The PLAINTEXT itself, not just the heading. A refactor that hid the
    // dialog while keeping the token mounted somewhere — behind an
    // `aria-hidden`, in a stale portal, in the exit-animation cache — would
    // satisfy the heading assertion and still leave the secret in the page.
    expect(document.body.innerHTML).not.toContain(PLAINTEXT);

    // Re-opening the create dialog must not re-summon a token that no longer
    // exists in state.
    await user.click(screen.getByRole('button', { name: /^create token$/i }));
    expect(
      screen.queryByRole('heading', { name: /save your new token/i }),
    ).not.toBeInTheDocument();
    expect(document.body.innerHTML).not.toContain(PLAINTEXT);
  });

  /**
   * Focus has to arrive inside the reveal, because the reveal refuses every
   * exit except its own button — focus left outside a dialog that cannot be
   * dismissed from outside is a keyboard trap in the other direction.
   *
   * WHAT THIS DOES NOT PROVE, stated so nobody reads it as more than it is:
   * it does not exercise `revealHandoffRef`. That ref suppresses the create
   * dialog's own close-restore, which in a browser fires when Radix's
   * `Presence` finally unmounts the content — i.e. after the 200ms exit
   * animation, and therefore after the reveal has already focused itself.
   * happy-dom runs no animation, so that unmount is synchronous and lands
   * BEFORE the reveal mounts; measured both ways, focus ends on the reveal
   * field with the ref and without it. The ref survives as a disclosed
   * mutant rather than a proven line.
   */
  test('focus lands inside the reveal, not on the page behind it', async () => {
    const user = setup();
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await createToken(user);
    const dialog = screen.getByRole('dialog');
    await waitFor(() => {
      expect(document.activeElement).not.toBe(document.body);
      expect(dialog.contains(document.activeElement)).toBe(true);
    });
  });

  /**
   * The fallback arm, exercised by MAKING the condition it guards.
   *
   * It is believed unreachable today — `activeTab` survives a rotation and
   * both layouts render `<ApiTokensSection>`, so the Create token button is
   * always in the document while the reveal is up. "Believed unreachable" is
   * not the same as "harmless if reached", though, and the arm's whole purpose
   * is to not do what Radix would do here: a trigger-less dialog restores
   * focus to a null ref, which lands it on `<body>`.
   *
   * So the anchor is removed by hand, which is the one way this environment
   * can produce the state. Contrived on purpose, and labelled as such.
   */
  test('with no opener left, focus lands on the page heading — never the body', async () => {
    const user = setup();
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await createToken(user);

    // `hidden: true` because the reveal is modal and Radix has already put
    // `aria-hidden` on everything behind it.
    screen
      .getByRole('button', { name: /^create token$/i, hidden: true })
      .remove();

    await user.click(
      screen.getByRole('button', { name: /i've saved my token/i }),
    );
    await waitFor(() => {
      expect(document.activeElement).not.toBe(document.body);
      expect(document.activeElement).toBe(
        screen.getByRole('heading', { name: 'Settings' }),
      );
    });
  });

  test('closing the reveal returns focus to the Create token button', async () => {
    const user = setup();
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await createToken(user);
    await user.click(
      screen.getByRole('button', { name: /i've saved my token/i }),
    );
    // Not `<body>`: there is no DialogTrigger above the reveal, so Radix's own
    // restore would focus a null ref and drop focus out of the page entirely.
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole('button', { name: /^create token$/i }),
      );
    });
  });
});

/**
 * The reveal refuses Escape, a backdrop tap and the X alike — the only exit is
 * its own button. That makes the X a control that looks operable and is not,
 * on the one surface holding a secret the server will never re-issue: the user
 * taps the affordance every other dialog on this page honours and walks away
 * believing the token is saved.
 */
describe('the reveal has no dead close affordance', () => {
  test('no Close control exists while the reveal is up', async () => {
    const user = setup();
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);

    // POSITIVE CONTROL, first and deliberately: the CREATE dialog is an
    // ordinary dialog and must keep its X. Without this half, a change that
    // removed the close button from every dialog in the app would pass the
    // assertion below.
    await user.click(screen.getByRole('button', { name: /^create token$/i }));
    const createDialog = await screen.findByRole('dialog');
    expect(
      within(createDialog).getByRole('button', { name: /close/i }),
    ).toBeInTheDocument();

    await user.type(screen.getByLabelText(/^name$/i), 'New token');
    await user.click(
      within(createDialog).getByRole('button', { name: /^create token$/i }),
    );
    await screen.findByRole('heading', { name: /save your new token/i });

    const reveal = screen.getByRole('dialog');
    expect(
      within(reveal).queryByRole('button', { name: /close/i }),
    ).not.toBeInTheDocument();
    // Not merely hidden from the role query — absent from the document, so it
    // cannot take a tab stop either. A CSS-only hide would leave it here.
    expect(reveal.querySelectorAll('button')).toHaveLength(2);
    const labels = Array.from(reveal.querySelectorAll('button')).map((b) =>
      b.textContent?.trim(),
    );
    expect(labels).toEqual(['Copy', "I've saved my token"]);
  });
});

/**
 * Radix keeps a dialog mounted through its exit animation, but `token` is
 * already null on the commit that starts it — so rendering straight from state
 * faded out an empty bordered box instead of the dialog the user just
 * dismissed.
 *
 * happy-dom runs no animations by default, which is why this went unnoticed:
 * the content unmounts synchronously and the exit window never exists. It CAN
 * be made to exist. `usePresence` decides by reading
 * `getComputedStyle(node).animationName`, and happy-dom does resolve that from
 * a real stylesheet — so injecting one gives the closed state an animation
 * name, Presence suspends the unmount exactly as a browser does, and the
 * window becomes observable. The exit is then completed by hand with the
 * `animationend` Presence is waiting for.
 */
describe('the exit animation fades out the dialog, not an empty shell', () => {
  const EXIT_ANIMATION = 'revealExitProbe';

  function installExitAnimation(): () => void {
    const style = document.createElement('style');
    style.textContent = `
      @keyframes ${EXIT_ANIMATION} { from { opacity: 1 } to { opacity: 0 } }
      [role="dialog"][data-state="closed"] {
        animation-name: ${EXIT_ANIMATION};
        animation-duration: 200ms;
      }
    `;
    document.head.appendChild(style);
    return () => style.remove();
  }

  test('the token is still rendered while the dialog animates away', async () => {
    const user = setup();
    const removeStyle = installExitAnimation();
    try {
      seedTokens([]);
      setViewportWidth(PHONE_WIDTH);
      renderSettings();
      await screen.findByText(/no api tokens yet/i);
      await user.click(screen.getByRole('button', { name: /^create token$/i }));
      await user.type(screen.getByLabelText(/^name$/i), 'New token');
      const createDialog = screen.getByRole('dialog');
      await user.click(
        within(createDialog).getByRole('button', { name: /^create token$/i }),
      );
      await screen.findByRole('heading', { name: /save your new token/i });
      const reveal = screen.getByRole('dialog');

      await user.click(
        screen.getByRole('button', { name: /i've saved my token/i }),
      );

      // Mid-exit: Presence has suspended the unmount, so the dialog is still
      // in the document — and what it is fading out has to be the token, not
      // an empty box.
      expect(reveal).toHaveAttribute('data-state', 'closed');
      expect(reveal).toBeInTheDocument();
      expect(
        within(reveal).getByRole('heading', { name: /save your new token/i }),
      ).toBeInTheDocument();
      expect(
        (
          within(reveal).getByLabelText(
            /your new api token/i,
          ) as HTMLTextAreaElement
        ).value,
      ).toBe(PLAINTEXT);

      // Finish the animation Presence is waiting on. `animationend` has to
      // carry the same name the computed style reports, and to target the
      // animating node itself — Presence checks both.
      await act(async () => {
        const event = new Event('animationend', { bubbles: false });
        Object.defineProperty(event, 'animationName', {
          value: EXIT_ANIMATION,
        });
        reveal.dispatchEvent(event);
      });

      // And once it is over the secret is gone from the page entirely — the
      // render-only cache is not a second home for it.
      await waitFor(() => {
        expect(
          screen.queryByRole('heading', { name: /save your new token/i }),
        ).not.toBeInTheDocument();
      });
      expect(document.body.innerHTML).not.toContain(PLAINTEXT);
    } finally {
      removeStyle();
    }
  });
});

describe('the reveal field can show all 38 characters at 360px', () => {
  /**
   * An `<input>` cannot wrap, so a 38-character token was a window onto the
   * middle of the string. happy-dom has no layout engine and cannot measure
   * the clipping, so what is pinned here is the mechanism that removes it: a
   * wrapping control, carrying the wrap rule that a long unbroken token needs.
   */
  test('it is a wrapping, read-only control — not a single-line input', async () => {
    const user = setup();
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await user.click(screen.getByRole('button', { name: /^create token$/i }));
    await user.type(screen.getByLabelText(/^name$/i), 'New token');
    const dialog = screen.getByRole('dialog');
    await user.click(
      within(dialog).getByRole('button', { name: /^create token$/i }),
    );
    await screen.findByRole('heading', { name: /save your new token/i });

    const field = screen.getByLabelText(/your new api token/i);
    expect(field.tagName).toBe('TEXTAREA');
    expect((field as HTMLTextAreaElement).readOnly).toBe(true);
    expect((field as HTMLTextAreaElement).value).toBe(PLAINTEXT);
    // Exact token, not a substring match: `toContain` on the class string
    // would also pass on `md:[overflow-wrap:anywhere]`, which is the one
    // variant that would leave the phone clipped.
    const tokens = Array.from(field.classList);
    expect(tokens).toContain('[overflow-wrap:anywhere]');
    expect(tokens).toContain('resize-none');
    // The touch floor the surrounding primitives carry. This is a field a
    // thumb drags a selection across on the surface that needs it most.
    expect(tokens).toContain('coarse:min-h-11');
  });

  /**
   * A textarea's value is DOM TEXT CONTENT, not a value attribute — page
   * translators, spell checkers, and password managers walk it the way they
   * walk any prose on the page. That is a difference in kind from the `<input>`
   * this replaced, and it is why the credential attributes are not optional
   * hygiene here: the one string on this surface is a live credential, and it
   * must not be shipped to a translation service, offered for storage, or
   * "corrected". The display-name field's `autoComplete="off"` is the same
   * class of guard on a far less sensitive value.
   */
  test('the reveal field is marked as a credential, not as prose', async () => {
    const user = setup();
    seedTokens([]);
    setViewportWidth(PHONE_WIDTH);
    renderSettings();
    await screen.findByText(/no api tokens yet/i);
    await user.click(screen.getByRole('button', { name: /^create token$/i }));
    await user.type(screen.getByLabelText(/^name$/i), 'New token');
    const dialog = screen.getByRole('dialog');
    await user.click(
      within(dialog).getByRole('button', { name: /^create token$/i }),
    );
    await screen.findByRole('heading', { name: /save your new token/i });

    const field = screen.getByLabelText(/your new api token/i);
    expect(field).toHaveAttribute('spellcheck', 'false');
    expect(field).toHaveAttribute('autocomplete', 'off');
    expect(field).toHaveAttribute('autocorrect', 'off');
    expect(field).toHaveAttribute('autocapitalize', 'off');
    expect(field).toHaveAttribute('translate', 'no');
    // The two password-manager opt-outs, 1Password and LastPass respectively.
    expect(field).toHaveAttribute('data-1p-ignore');
    expect(field).toHaveAttribute('data-lpignore', 'true');
  });
});
