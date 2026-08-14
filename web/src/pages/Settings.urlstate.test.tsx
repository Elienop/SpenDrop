import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router-dom';
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
import { resolveSettingsTab, visibleSettingsSections } from './settings-sections';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);

/**
 * `?tab=` in both directions.
 *
 * It was already honoured INTO the page — validated, role-clamped, with a
 * one-shot toast for the values that moved to their own pages — and never
 * written back OUT, so the section a user chose survived nothing: not a reload,
 * not a share, not a Back press from wherever they went next.
 *
 * Two things make the write non-trivial and both are pinned here. The URL is
 * REPLACED rather than pushed, because a page whose five sections each pushed
 * an entry turns Back into a walk through the user's browsing of one page
 * instead of the exit it is; and nothing is written on mount, so a bare
 * `/settings` stays bare and a retired `?tab=savings` bookmark is still the URL
 * that produced the forwarding toast.
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
    // Loud on purpose: a silent fallback would run every phone case at
    // happy-dom's default 1024, where the picker does not exist at all.
    throw new Error(
      'happy-dom viewport control is unavailable — mobile tests cannot run',
    );
  }
  controllable.happyDOM.setViewport({ width });
}

/**
 * Flip the viewport mid-test, the way a rotation does.
 *
 * happy-dom dispatches `change` from `setViewport`, so
 * `useIsMobileViewport`'s subscription re-renders on its own and `act` flushes
 * it — WITH THE TRAP documented at length in `Settings.apiTokens.mobile.test.tsx`:
 * a listener registered while the query already matches swallows its first
 * unmatch. Every crossing below therefore starts at PHONE width, where
 * `(min-width: 768px)` is false and agrees with happy-dom's hardcoded initial
 * state, so the first flip really fires.
 */
function rotateTo(width: number): void {
  act(() => {
    setViewportWidth(width);
  });
}

function withQueryClient({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

/** The live URL, as a string, so an assertion can name it in full. */
function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="probe-location">
      {location.pathname}
      {location.search}
    </div>
  );
}

/**
 * The two navigations a user makes AROUND this page: leaving it for another
 * route, and pressing Back. Both live outside `<Routes>` so they survive the
 * route swap, and both are named with a `probe:` prefix so they cannot be
 * confused with anything Settings renders.
 */
function ProbeNav() {
  const navigate = useNavigate();
  return (
    <>
      <button type="button" onClick={() => navigate('/budgets')}>
        probe: open budgets
      </button>
      <button type="button" onClick={() => navigate(-1)}>
        probe: back
      </button>
    </>
  );
}

/**
 * Real routes rather than a bare `<Settings />`, because half of what is under
 * test is what happens when the user LEAVES: a Settings page that stayed
 * mounted at `/budgets` would make "Back returns to the section" untestable.
 */
function renderAt(entries: string[], initialIndex = entries.length - 1) {
  return render(
    <MemoryRouter initialEntries={entries} initialIndex={initialIndex}>
      <LocationProbe />
      <ProbeNav />
      <Routes>
        <Route path="/settings" element={<Settings />} />
        <Route path="/budgets" element={<h2>Budgets page</h2>} />
        <Route path="/dashboard" element={<h2>Dashboard page</h2>} />
      </Routes>
    </MemoryRouter>,
    { wrapper: withQueryClient },
  );
}

function currentUrl(): string {
  return screen.getByTestId('probe-location').textContent ?? '';
}

function setup() {
  // An open Radix Select sets `pointer-events: none` on <body>, and happy-dom
  // has no layout engine to tell the portalled content apart.
  return userEvent.setup({ pointerEventsCheck: 0 });
}

/**
 * The full context shape rather than a cast. `as unknown as ReturnType<…>`
 * compiles no matter what the contract grows, so the day `useAuth` gains a
 * required member every test in the file starts feeding it `undefined` and
 * says nothing — the failure would surface as an unrelated render error. Same
 * shape as `Budgets.mobile.test.tsx`.
 */
function asRole(role: 'admin' | 'member') {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 1,
      username: 'elie',
      display_name: 'Elie',
      role,
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

/** Choose a section on whichever surface is mounted. */
async function chooseSection(
  user: ReturnType<typeof userEvent.setup>,
  label: string,
) {
  const picker = screen.queryByRole('combobox', { name: 'Settings section' });
  if (picker) {
    await user.click(picker);
    await user.click(await screen.findByRole('option', { name: label }));
    return;
  }
  await user.click(screen.getByRole('tab', { name: label }));
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  asRole('admin');
  mockedApi.get.mockImplementation((path: string) => {
    if (path.includes('token')) return Promise.resolve({ tokens: [] });
    return Promise.resolve([]);
  });
});

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
});

describe('choosing a section writes it to the URL', () => {
  test('the desktop tab strip puts the section in ?tab=', async () => {
    const user = setup();
    setViewportWidth(DESKTOP_WIDTH);
    renderAt(['/settings']);

    // Positive control on the starting state: the strip is the surface that is
    // mounted, the default section is the selected one, and the URL carries no
    // tab at all — so the assertion after the click is about the click.
    expect(screen.getByRole('tablist')).toBeInTheDocument();
    expect(
      screen.getByRole('tab', { name: 'Account & users', selected: true }),
    ).toBeInTheDocument();
    expect(currentUrl()).toBe('/settings');

    await chooseSection(user, 'Notifications');

    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=notifications');
    });
    expect(
      screen.getByRole('tab', { name: 'Notifications', selected: true }),
    ).toBeInTheDocument();
  });

  test('the phone section picker puts the same value in ?tab=', async () => {
    const user = setup();
    setViewportWidth(PHONE_WIDTH);
    renderAt(['/settings']);

    // The opposite-surface control, both halves: this branch has the picker
    // and no tab strip, so the case cannot silently be a second desktop test.
    expect(
      screen.getByRole('combobox', { name: 'Settings section' }),
    ).toBeInTheDocument();
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(currentUrl()).toBe('/settings');

    await chooseSection(user, 'Notifications');

    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=notifications');
    });
    expect(
      screen.getByRole('region', { name: 'Notifications' }),
    ).toBeInTheDocument();
  });

  test('the written value is one the loader hands straight back', async () => {
    // The round trip, over every section a MEMBER may open — the role the
    // inbound clamp exists for. What is written has to survive
    // `resolveSettingsTab`, or a shared link would silently land somewhere
    // else than the sender's screen.
    const user = setup();
    asRole('member');
    setViewportWidth(DESKTOP_WIDTH);
    renderAt(['/settings']);

    // Visited back to front so the section that is ALREADY selected on arrival
    // comes last: Radix fires no change for a click on the current value, so a
    // front-to-back walk would silently skip the default section instead of
    // covering it.
    expect(
      screen.getByRole('tab', { name: 'Account', selected: true }),
    ).toBeInTheDocument();
    const sections = [...visibleSettingsSections(false)].reverse();
    expect(sections).toHaveLength(5);

    for (const section of sections) {
      const label = section.label(false);
      await chooseSection(user, label);
      await waitFor(() => {
        expect(currentUrl()).toBe(`/settings?tab=${section.value}`);
      });
      const written = new URLSearchParams(currentUrl().split('?')[1]).get('tab');
      expect(resolveSettingsTab(written, false)).toBe(section.value);
    }
  });
});

describe('the URL survives what a URL is for', () => {
  test('a reload lands on the section the user was reading', async () => {
    const user = setup();
    setViewportWidth(DESKTOP_WIDTH);
    renderAt(['/settings']);
    await chooseSection(user, 'Currencies');
    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=currencies');
    });

    // The reload: the same URL, a brand new mount. `cleanup()` rather than
    // `rerender`, because the point is that nothing in memory carries the
    // section across — only the string in the address bar.
    const shared = currentUrl();
    cleanup();
    renderAt([shared]);

    expect(
      screen.getByRole('tab', { name: 'Currencies', selected: true }),
    ).toBeInTheDocument();
    // The control for the control: mounting at a bare `/settings` is what the
    // default looks like, so the assertion above is about the param.
    cleanup();
    renderAt(['/settings']);
    expect(
      screen.getByRole('tab', { name: 'Account & users', selected: true }),
    ).toBeInTheDocument();
  });

  test('Back from another page returns to that section', async () => {
    const user = setup();
    setViewportWidth(DESKTOP_WIDTH);
    renderAt(['/settings']);
    await chooseSection(user, 'Notifications');
    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=notifications');
    });

    await user.click(screen.getByRole('button', { name: 'probe: open budgets' }));
    expect(await screen.findByText('Budgets page')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'probe: back' }));

    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=notifications');
    });
    expect(
      screen.getByRole('tab', { name: 'Notifications', selected: true }),
    ).toBeInTheDocument();
  });

  test('Back does not walk through every section visited on the way', async () => {
    const user = setup();
    setViewportWidth(DESKTOP_WIDTH);
    // Arriving from somewhere else, which is the only way Back has anywhere to
    // go: the entry Settings occupies is index 1.
    renderAt(['/dashboard', '/settings'], 1);

    await chooseSection(user, 'Currencies');
    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=currencies');
    });
    await chooseSection(user, 'Notifications');
    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=notifications');
    });
    await chooseSection(user, 'API tokens');
    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=api-tokens');
    });

    // ONE press leaves the page. Were the write a push instead of a replace,
    // this would land on `?tab=notifications` and the user would need four.
    await user.click(screen.getByRole('button', { name: 'probe: back' }));

    expect(await screen.findByText('Dashboard page')).toBeInTheDocument();
    expect(currentUrl()).toBe('/dashboard');
  });
});

describe('a bare /settings is left alone', () => {
  test('no tab is written until the user chooses one', async () => {
    setViewportWidth(DESKTOP_WIDTH);
    renderAt(['/settings']);

    // The default section renders, as it always did…
    expect(
      screen.getByRole('tab', { name: 'Account & users', selected: true }),
    ).toBeInTheDocument();
    // …and the URL is still the one the user typed. A mount-time canonicalise
    // would have replaced it with `?tab=account` by now, which is the choice
    // this pins against.
    await act(async () => {});
    expect(currentUrl()).toBe('/settings');
  });
});

describe('the write does not fight the inbound forwarder', () => {
  test('a retired ?tab= toasts once and is never written back', async () => {
    const user = setup();
    setViewportWidth(DESKTOP_WIDTH);
    renderAt(['/settings?tab=savings']);

    expect(mockedToast.info).toHaveBeenCalledTimes(1);
    expect(mockedToast.info).toHaveBeenCalledWith(
      'Savings has its own page now',
      expect.any(Object),
    );

    await chooseSection(user, 'Currencies');
    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=currencies');
    });

    // The retired value is gone from the URL rather than re-emitted, and the
    // toast did not fire a second time on the way through.
    expect(currentUrl()).not.toContain('savings');
    expect(mockedToast.info).toHaveBeenCalledTimes(1);
  });

  /**
   * `?tab=` is attacker-choosable, and the forwarding table used to be a plain
   * object literal indexed with the raw value — so every key on
   * `Object.prototype` was a truthy hit. `?tab=constructor` toasted
   * "undefined has its own page now" with an Open button wired to
   * `navigate(undefined)`.
   *
   * Nothing escapes and nothing navigates, so this is cosmetic rather than a
   * vulnerability; it is pinned because the input is chosen by whoever sends
   * the link, and because the same lookup shape is what a future maintainer
   * would copy.
   */
  test.each(['constructor', '__proto__', 'toString', 'valueOf'])(
    '?tab=%s is an unknown value, not a moved tab',
    async (key) => {
      renderAt([`/settings?tab=${key}`]);

      expect(mockedToast.info).not.toHaveBeenCalled();
      // It resolves the way any unrecognised value does: the default section,
      // no crash, and the raw value left in the URL untouched.
      expect(
        screen.getByRole('tab', { name: 'Account & users', selected: true }),
      ).toBeInTheDocument();
      expect(currentUrl()).toBe(`/settings?tab=${key}`);
    },
  );

  test('a REAL moved tab still toasts — the control for the probe above', () => {
    // Without this half, a page that had lost the forwarding toast entirely
    // would satisfy every prototype-key case by doing nothing at all.
    renderAt(['/settings?tab=savings']);
    expect(mockedToast.info).toHaveBeenCalledTimes(1);
    expect(mockedToast.info).toHaveBeenCalledWith(
      'Savings has its own page now',
      expect.any(Object),
    );
  });

  test('an unknown param this page does not own survives the write', async () => {
    const user = setup();
    setViewportWidth(DESKTOP_WIDTH);
    renderAt(['/settings?ref=email']);
    expect(currentUrl()).toBe('/settings?ref=email');

    await chooseSection(user, 'Currencies');

    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?ref=email&tab=currencies');
    });
  });
});

describe('crossing md keeps both the section and the URL', () => {
  test('a rotation in either direction changes neither', async () => {
    const user = setup();
    setViewportWidth(PHONE_WIDTH);
    renderAt(['/settings']);
    await chooseSection(user, 'Notifications');
    await waitFor(() => {
      expect(currentUrl()).toBe('/settings?tab=notifications');
    });

    rotateTo(DESKTOP_WIDTH);

    // The swap really happened — without this half the case would pass on a
    // page that ignored the viewport entirely.
    await screen.findByRole('tablist');
    expect(
      screen.queryByRole('combobox', { name: 'Settings section' }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('tab', { name: 'Notifications', selected: true }),
    ).toBeInTheDocument();
    expect(currentUrl()).toBe('/settings?tab=notifications');

    rotateTo(PHONE_WIDTH);

    await screen.findByRole('combobox', { name: 'Settings section' });
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(
      screen.getByRole('region', { name: 'Notifications' }),
    ).toBeInTheDocument();
    expect(currentUrl()).toBe('/settings?tab=notifications');
  });
});
