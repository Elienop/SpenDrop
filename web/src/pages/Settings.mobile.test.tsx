import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
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
import { SETTINGS_SECTIONS, visibleSettingsSections } from './settings-sections';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

/**
 * Below `md` the Settings tab strip is replaced by a Select.
 *
 * The strip overflowed: six labels measured 568px of scrollWidth against 313
 * of client at a 360px viewport, and the Reports treatment (smaller tick font
 * plus `px-2`) still came to 461px. That is a layout fact a browser measures,
 * not this file — what is pinned here is that the swap happens, that the two
 * surfaces offer the SAME sections, and that the phone surface does not
 * reproduce the accessibility defect the obvious implementations have.
 */

interface HappyDomWindow {
  happyDOM?: { setViewport: (v: { width?: number; height?: number }) => void };
}

const DESKTOP_WIDTH = 1024;
/** Galaxy S24 — the narrowest device this household actually uses. */
const PHONE_WIDTH = 360;
/** Galaxy Tab S10 FE portrait: below `md`, so it takes the phone branch. */
const TABLET_PORTRAIT = 720;

function setViewportWidth(width: number): void {
  const controllable = window as unknown as HappyDomWindow;
  if (!controllable.happyDOM) {
    // Loud on purpose: a silent fallback would run every case below at
    // happy-dom's default 1024, where the Select does not exist at all.
    throw new Error(
      'happy-dom viewport control is unavailable — mobile tests cannot run',
    );
  }
  controllable.happyDOM.setViewport({ width });
}

function withQueryClient({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function renderSettings(path = '/settings') {
  return render(
    <MemoryRouter initialEntries={[path]}>
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

function asRole(role: 'admin' | 'member') {
  mockedUseAuth.mockReturnValue({
    user: { id: 1, username: 'elie', display_name: 'Elie', role },
  } as unknown as ReturnType<typeof useAuth>);
}

/**
 * Render at phone width, having first proved the swap actually took.
 *
 * BOTH HALVES. The two surfaces are easy to confuse with a weak assertion —
 * both render the same six section bodies under the same page heading — so
 * the control asserts the Select is present AND that no `tablist` exists.
 * Without the absence half, a page that rendered the tab strip at every width
 * would pass most of what follows.
 */
function renderPhone(role: 'admin' | 'member' = 'admin', path = '/settings') {
  asRole(role);
  setViewportWidth(PHONE_WIDTH);
  renderSettings(path);
  expect(
    screen.getByRole('combobox', { name: 'Settings section' }),
  ).toBeInTheDocument();
  expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
  return screen.getByRole('combobox', { name: 'Settings section' });
}

/** The desktop half of the same control. */
function renderDesktop(role: 'admin' | 'member' = 'admin') {
  asRole(role);
  setViewportWidth(DESKTOP_WIDTH);
  renderSettings();
  expect(screen.getByRole('tablist')).toBeInTheDocument();
  expect(
    screen.queryByRole('combobox', { name: 'Settings section' }),
  ).not.toBeInTheDocument();
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  asRole('admin');
  mockedApi.get.mockImplementation((path: string) => {
    if (path === 'currencies') return Promise.resolve([]);
    if (path.startsWith('users')) return Promise.resolve([]);
    if (path.includes('token')) return Promise.resolve({ tokens: [] });
    return Promise.resolve([]);
  });
});

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
});

describe('which control mounts', () => {
  test('below md the tab strip is replaced by a Select', () => {
    renderPhone();
    // …and no orphan tab survives the swap either.
    expect(screen.queryAllByRole('tab')).toHaveLength(0);
  });

  test('at md and above the tab strip is kept', () => {
    renderDesktop();
    expect(screen.getAllByRole('tab').length).toBeGreaterThan(0);
  });

  test('a 720px tablet in portrait is below md and gets the Select', () => {
    // The gate is WIDTH, not pointer capability — unlike the heatmap's year
    // grid. This is the case that separates the two hooks: a tablet reports a
    // coarse pointer AND is under 768, and both answers agree here, so the
    // test that matters is that 720 is treated as narrow at all.
    asRole('admin');
    setViewportWidth(TABLET_PORTRAIT);
    renderSettings();
    expect(
      screen.getByRole('combobox', { name: 'Settings section' }),
    ).toBeInTheDocument();
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
  });
});

describe('the phone surface does not orphan its panels', () => {
  // THE DEFECT THIS DESIGN EXISTS TO AVOID, and it is not hypothetical —
  // probed on this Radix version. Keeping `Tabs`/`TabsContent` while dropping
  // `TabsList` still mounts every panel and still switches them correctly, but
  // each panel keeps `aria-labelledby` pointing at a trigger id that no longer
  // exists, so the IDREF resolves to nothing and every section silently loses
  // its accessible name. `role="tabpanel"` also survives with no `tablist`
  // anywhere in the document. It looks perfect on screen.
  test('no tabpanel role survives without a tablist', () => {
    renderPhone();
    expect(screen.queryAllByRole('tabpanel')).toHaveLength(0);
  });

  test('every aria-labelledby on the page resolves to a real element', () => {
    renderPhone();
    const dangling = [...document.body.querySelectorAll('[aria-labelledby]')]
      .flatMap((el) => el.getAttribute('aria-labelledby')!.split(/\s+/))
      .filter((id) => document.getElementById(id) === null);
    expect(dangling).toEqual([]);
  });

  test('the section is a named region, so it is still a landmark', () => {
    // `renderPhone()` signs in as an admin, so the region takes the merged
    // label. Exact string: `/account/i` would match either label and stop
    // discriminating the two roles.
    renderPhone();
    expect(
      screen.getByRole('region', { name: 'Account & users' }),
    ).toBeInTheDocument();
  });

  test('a member gets the same region under the narrowed name', () => {
    renderPhone('member');
    expect(screen.getByRole('region', { name: 'Account' })).toBeInTheDocument();
    expect(
      screen.queryByRole('region', { name: 'Account & users' }),
    ).not.toBeInTheDocument();
  });
});

describe('the option list matches what the role may open', () => {
  test('an admin is offered every section', async () => {
    const user = setup();
    const trigger = renderPhone('admin');
    await user.click(trigger);
    const options = screen.getAllByRole('option');
    expect(options.map((o) => o.textContent)).toEqual([
      'Account & users',
      'Currencies',
      'API tokens',
      'Notifications',
      'Import / Export',
    ]);
  });

  test('a member gets the same five sections under two narrower names', async () => {
    // Both roles now open every section — `users` was merged into `account`
    // and the admin half is gated inside that panel, so nothing is hidden from
    // the option list at all. What still narrows is the LABEL, on the two
    // sections whose panel holds less for a member.
    const user = setup();
    const trigger = renderPhone('member');
    await user.click(trigger);
    const labels = screen.getAllByRole('option').map((o) => o.textContent);
    expect(labels).toEqual([
      'Account',
      'Currencies',
      'API tokens',
      'Notifications',
      'Export',
    ]);
    expect(labels).not.toContain('Account & users');
    expect(labels).not.toContain('Import / Export');
  });
});

describe('choosing a section', () => {
  test('swaps the panel and renames the region', async () => {
    const user = setup();
    const trigger = renderPhone('admin');
    expect(
      screen.getByRole('region', { name: 'Account & users' }),
    ).toBeInTheDocument();

    await user.click(trigger);
    await user.click(screen.getByRole('option', { name: 'Notifications' }));

    expect(
      screen.getByRole('region', { name: 'Notifications' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('region', { name: 'Account & users' }),
    ).not.toBeInTheDocument();
    // The closed control reads the new section too, so the control and the
    // content cannot disagree about where the user is.
    expect(trigger).toHaveTextContent('Notifications');
  });

  test('focus comes back to the trigger, not to <body>', async () => {
    // CORRECTION (this pass): an earlier note here blamed the browser oracle,
    // reading "CDP-synthesized clicks lie on Radix" into a disagreement that
    // was really a disagreement about CODE — the CDP run measured the surface
    // BEFORE a local `onCloseAutoFocus` was added and this test measured it
    // after. Both oracles agree once they measure the same build: with the
    // shared wrapper's old blanket `preventDefault()` restored, `user-event`
    // also reports `<body>` here.
    //
    // Nothing local fixes it now. `ui/select.tsx` passes `onCloseAutoFocus`
    // straight through, so Radix's own restore runs, and this test covers the
    // phone's only Settings navigation control end to end.
    const user = setup();
    const trigger = renderPhone('admin');
    await user.click(trigger);
    await user.click(screen.getByRole('option', { name: 'Notifications' }));
    await waitFor(() => expect(document.activeElement).toBe(trigger), {
      timeout: 2000,
    });
  });

  test('a ?tab= bookmark opens that section on the phone as well', () => {
    renderPhone('admin', '/settings?tab=api-tokens');
    expect(
      screen.getByRole('region', { name: 'API tokens' }),
    ).toBeInTheDocument();
  });
});

describe('the two surfaces stay in step', () => {
  // A parity test rather than two hand-maintained lists: a section added to
  // one surface and not the other fails here. Same reason `nav-items.ts` has
  // one for the sidebar and the drawer.
  test.each([['admin'], ['member']] as const)(
    'the %s sees the same sections on both surfaces',
    async (role) => {
      const user = setup();
      const trigger = renderPhone(role);
      await user.click(trigger);
      const phone = screen.getAllByRole('option').map((o) => o.textContent);

      // `cleanup()`, not `document.body.innerHTML = ''` — the latter removes
      // React's container from under it and the next unmount throws
      // `removeChild ... not a child of this node`. A fresh mount, because
      // `rerender` keeps the old tree and the viewport gate is read at render.
      cleanup();
      renderDesktop(role);
      const desktop = screen.getAllByRole('tab').map((t) => t.textContent);

      expect(phone).toEqual(desktop);
      expect(phone).toHaveLength(
        visibleSettingsSections(role === 'admin').length,
      );
    },
  );

  test('every section in the source list is reachable on the phone', async () => {
    // Guards the fixture above: if `SETTINGS_SECTIONS` grew an entry that
    // neither surface rendered, the parity test would still pass by agreeing
    // on the same omission.
    const user = setup();
    const trigger = renderPhone('admin');
    await user.click(trigger);
    expect(screen.getAllByRole('option')).toHaveLength(SETTINGS_SECTIONS.length);
  });
});

describe('the control is thumb-sized', () => {
  test('the trigger carries the 44px floor, not the stock h-10', () => {
    // happy-dom lays nothing out, so the token is the assertion and the pixels
    // are a browser check (measured 44px at 360/390/720). `h-11` is 44px;
    // shadcn's SelectTrigger ships `h-10` = 40px, which is under the floor the
    // rest of the phone shell keeps.
    const trigger = renderPhone();
    const tokens = trigger.className.split(/\s+/);
    expect(tokens).toContain('h-11');
    expect(tokens).not.toContain('h-10');
  });

  test('it spans the page rather than sitting at label width', () => {
    expect(renderPhone().className.split(/\s+/)).toContain('w-full');
  });

  test('every OPTION carries the floor too, not just the trigger', async () => {
    // Browser-measured before this existed: the trigger was already 44px while
    // every option in the open list was 32px — `SelectItem`'s stock `py-1.5`
    // around 20px of `text-sm`. The tap that actually chooses a section was
    // the one under the floor, which is the wrong half to get right.
    //
    // The floor now comes from `ui/select.tsx` rather than a `className` here,
    // so this asserts the phone still GETS it — the per-call-site version was
    // removed once the primitive covered all thirteen consumers.
    const user = setup();
    await user.click(renderPhone('admin'));
    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(5);
    for (const option of options) {
      expect(option.className.split(/\s+/)).toContain('min-h-11');
    }
  });
});

describe('the desktop surface is untouched', () => {
  test('its triggers and panels are still wired to each other', () => {
    renderDesktop('admin');
    const tab = screen.getByRole('tab', { name: 'Account & users' });
    const panel = screen.getByRole('tabpanel');
    // The wiring the phone branch deliberately does not fake: the panel is
    // named by its trigger, and the trigger owns the panel.
    expect(panel.getAttribute('aria-labelledby')).toBe(tab.getAttribute('id'));
    expect(tab.getAttribute('aria-controls')).toBe(panel.getAttribute('id'));
    expect(
      within(document.body).getByRole('tabpanel', { name: 'Account & users' }),
    ).toBeInTheDocument();
  });
});
