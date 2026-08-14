import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, within, cleanup } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement, type ReactNode } from 'react';

vi.mock('../hooks/useAuth', () => ({ useAuth: vi.fn() }));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
    upload: vi.fn(),
  },
}));

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { Budgets } from './Budgets';
import type { Category, CategoryBudget } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

/**
 * Phone-width presentation of the Budgets page's two editors.
 *
 * Both tables are two columns, which is exactly why the defect was easy to
 * miss: measured on the built container at 360px, each table wanted 556px
 * inside a 345px box, so 211px — the whole Amount / Limit column, the only
 * editable thing on either surface — sat behind a horizontal scroll INSIDE the
 * table's own `overflow-auto` wrapper. The page never panned, so nothing looked
 * wrong until you went looking for the field.
 *
 * In its own file rather than added to `Budgets.test.tsx` because the viewport
 * is per-file state in happy-dom: a `setViewport` leaking into the 41 tests
 * next door would move every one of them onto the other branch.
 *
 * WHAT THIS FILE CANNOT PROVE. happy-dom performs no layout and never loads the
 * Tailwind stylesheet, so every width here would be zero. The pan above was
 * measured in Chrome; the width budget is recorded at the call sites in
 * `Budgets.tsx`. What is asserted below is WHICH tree mounts, that each card
 * carries every datum and control its row does, that the role gate survives the
 * swap identically, and that the class tokens the browser measurement depended
 * on are still present. Do not upgrade any assertion here into a claim about
 * pixels.
 */

interface HappyDomWindow {
  happyDOM?: { setViewport: (v: { width?: number; height?: number }) => void };
}

/** Galaxy S24 — the narrowest device this household actually uses. */
const PHONE_WIDTH = 360;
/** happy-dom's default, and what every other test file in this repo runs at. */
const DESKTOP_WIDTH = 1024;

function setViewportWidth(width: number): void {
  const controllable = window as unknown as HappyDomWindow;
  if (!controllable.happyDOM) {
    // Loud on purpose: a silent fallback would run every case below at
    // happy-dom's default 1024, where the card lists do not exist at all.
    throw new Error(
      'happy-dom viewport control is unavailable — mobile tests cannot run',
    );
  }
  controllable.happyDOM.setViewport({ width });
}

const CATEGORIES: Category[] = [
  {
    id: 10,
    name: 'Groceries',
    type: 'expense',
    icon: '🛒',
    sort_order: 1,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 11,
    name: 'Rent',
    type: 'expense',
    icon: '🏠',
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 12,
    name: 'Old Cable',
    type: 'expense',
    icon: null,
    sort_order: 5,
    is_active: false,
    created_at: '2024-01-01',
  },
  {
    id: 20,
    name: 'Salary',
    type: 'income',
    icon: '💰',
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
];

const CATEGORY_LIMITS: CategoryBudget[] = [{ category_id: 10, amount: 400 }];

function defaultGet(path: string): Promise<unknown> {
  // Order matters: "category-budgets" contains "budget", so it has to be
  // matched before the generic budgets branch.
  if (path.startsWith('category-budgets'))
    return Promise.resolve(CATEGORY_LIMITS);
  if (path.startsWith('categories')) return Promise.resolve(CATEGORIES);
  if (path.includes('budget'))
    return Promise.resolve([
      { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
    ]);
  if (path === 'currencies')
    return Promise.resolve([
      {
        code: 'USD',
        name: 'US Dollar',
        symbol: '$',
        rate_to_base: 1,
        is_base: true,
        updated_at: '',
      },
    ]);
  return Promise.resolve([]);
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

function QueryWrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return createElement(QueryClientProvider, { client }, children);
}

function renderBudgets() {
  return render(
    <QueryWrapper>
      <MemoryRouter>
        <Budgets />
      </MemoryRouter>
    </QueryWrapper>,
  );
}

/**
 * Render at phone width, having first proved the presentation actually swapped.
 *
 * THE POSITIVE CONTROL, and it is not decoration. Nearly every assertion in
 * this file is equally true of the table — the month names render either way,
 * the money strings are identical, the fields are the same component. So if
 * `setViewportWidth` ever stops taking (a renamed happy-dom API, a deleted
 * `beforeEach`, a hook that starts answering `false`), the DESKTOP tree mounts
 * and most of this file goes green while asserting the wrong presentation.
 * BOTH halves are checked — the lists are present AND no table survives —
 * because either alone is satisfied by a dual-render, which is the exact thing
 * this fork exists to avoid.
 */
async function renderPhone(): Promise<{
  monthly: HTMLElement;
  limits: HTMLElement;
}> {
  setViewportWidth(PHONE_WIDTH);
  renderBudgets();
  const monthly = await screen.findByRole('list', { name: 'Monthly budgets' });
  const limits = await screen.findByRole('list', { name: 'Category limits' });
  expect(screen.queryAllByRole('table')).toHaveLength(0);
  return { monthly, limits };
}

/** The desktop half of the same control. */
async function renderDesktop(): Promise<HTMLElement[]> {
  setViewportWidth(DESKTOP_WIDTH);
  renderBudgets();
  // Two tables: Monthly Budgets renders immediately, Category Limits only
  // after its fetch resolves. `findAllByRole` settles on the FIRST match, so
  // waiting for it would return the monthly table alone and every assertion
  // about the limits table would run against a skeleton.
  await waitFor(() => expect(screen.getAllByRole('table')).toHaveLength(2));
  const tables = screen.getAllByRole('table');
  expect(
    screen.queryByRole('list', { name: 'Monthly budgets' }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole('list', { name: 'Category limits' }),
  ).not.toBeInTheDocument();
  return tables;
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockedApi.get.mockImplementation(defaultGet);
  // Freeze to a known month so the default month/year, the category-budgets
  // query string, and every accessible name that carries the year are
  // deterministic.
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  // The viewport is window state, not React state — RTL's cleanup does not
  // touch it, and a phone width left behind would follow this file's worker
  // into the next one.
  setViewportWidth(DESKTOP_WIDTH);
});

describe('which presentation mounts', () => {
  beforeEach(asAdmin);

  test('below md both budget tables are replaced by card lists', async () => {
    const { monthly, limits } = await renderPhone();

    // The lists are POPULATED, not merely present — an empty `<ul>` satisfies
    // `findByRole('list')` while showing the user nothing.
    expect(within(monthly).getAllByRole('listitem')).toHaveLength(12);
    // Two active expense categories in the fixture; the inactive one and the
    // income one are excluded on this presentation exactly as on the table.
    expect(within(limits).getAllByRole('listitem')).toHaveLength(2);
    // …and no orphan row survived the swap on either section.
    expect(screen.queryAllByRole('row')).toHaveLength(0);
  });

  test('at md and above both tables are kept, untouched', async () => {
    const [monthlyTable, limitsTable] = await renderDesktop();

    // Header row plus one per month.
    expect(within(monthlyTable).getAllByRole('row')).toHaveLength(13);
    expect(
      within(monthlyTable).getByRole('spinbutton', {
        name: 'Budget for April 2026 in USD',
      }),
    ).toBeInTheDocument();
    // Header row plus one per active expense category.
    expect(within(limitsTable).getAllByRole('row')).toHaveLength(3);
    expect(
      within(limitsTable).getByRole('spinbutton', {
        name: 'Limit for Groceries April 2026 in USD',
      }),
    ).toBeInTheDocument();
  });
});

describe('what a monthly budget card carries — as admin', () => {
  beforeEach(asAdmin);

  test('every month is editable and seeded from the server', async () => {
    const { monthly } = await renderPhone();

    const fields = within(monthly).getAllByRole('spinbutton');
    expect(fields).toHaveLength(12);
    // April is the only budgeted month in the fixture; the other eleven are
    // empty, which is a different thing from zero.
    const april = within(monthly).getByRole('spinbutton', {
      name: 'April budget for 2026 in USD',
    });
    expect((april as HTMLInputElement).value).toBe('3000');
    expect(
      (
        within(monthly).getByRole('spinbutton', {
          name: 'May budget for 2026 in USD',
        }) as HTMLInputElement
      ).value,
    ).toBe('');
  });

  /**
   * The field is named by a real `<Label>`, and the month is on screen while
   * the year and currency — which the table carried per row and in its column
   * header — ride along `sr-only`. An `aria-label` beside visible label text
   * would override it and orphan what is on screen; a bare visible "April"
   * with nothing else would leave a forms-mode reader, who hears only the
   * field's own name, with twelve months and no year among them.
   */
  test('the month field is labelled by visible text, not an aria-label', async () => {
    const { monthly } = await renderPhone();
    const april = within(monthly).getByRole('spinbutton', {
      name: 'April budget for 2026 in USD',
    });

    expect(april).not.toHaveAttribute('aria-label');
    const label = document.querySelector(`label[for="${april.id}"]`);
    expect(label).not.toBeNull();
    // Visible text is the month alone; the rest is present but sr-only. The
    // separating space is a text node OUTSIDE the span deliberately — inside
    // it, an accessible-name implementation that trims each node before
    // joining would produce "Aprilbudget for 2026 in USD".
    expect(label?.textContent).toBe('April budget for 2026 in USD');
    const hidden = label?.querySelector('.sr-only');
    expect(hidden?.textContent).toBe('budget for 2026 in USD');
    expect(april).toHaveAccessibleName('April budget for 2026 in USD');
  });

  test('an amount typed on a card is what Save Budgets sends', async () => {
    mockedApi.put.mockResolvedValue({});
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const { monthly } = await renderPhone();

    const may = within(monthly).getByRole('spinbutton', {
      name: 'May budget for 2026 in USD',
    });
    await user.type(may, '1200');

    // The dirty count the header shows is the one the card list produced.
    expect(
      screen.getByRole('button', { name: 'Save Budgets (1)' }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /save budgets/i }));

    await waitFor(() => {
      expect(mockedApi.put).toHaveBeenCalledWith('budgets/2026/5', {
        amount: 1200,
      });
    });
    // May and only May: a card wired to the wrong index would still produce a
    // green "something was saved" assertion.
    expect(mockedApi.put).toHaveBeenCalledTimes(1);
  });

  /**
   * `min-w-0` is what lets the field shrink. A flex item's automatic minimum is
   * its min-content size, and for an `<input>` that is its intrinsic width from
   * the `size` attribute (~177px), not zero — so without this the field refuses
   * to shrink and the row pans, which is the defect the card list removes.
   * `coarse:min-h-11` is the `Input` primitive's own 44px touch floor, asserted
   * here because it only arrives if the card uses the primitive rather than a
   * raw `<input>`.
   */
  test('the month field can shrink and keeps the 44px coarse floor', async () => {
    const { monthly } = await renderPhone();
    const april = within(monthly).getByRole('spinbutton', {
      name: 'April budget for 2026 in USD',
    });

    expect(april).toHaveClass('min-w-0');
    expect(april).toHaveClass('coarse:min-h-11');
  });
});

describe('what a monthly budget card carries — as member (read-only)', () => {
  beforeEach(asMember);

  test('the card shows the formatted amount and no field at all', async () => {
    const { monthly } = await renderPhone();

    // The role gate has to survive the layout swap in BOTH directions: a
    // member must not gain a control she could never save through.
    expect(within(monthly).queryAllByRole('spinbutton')).toHaveLength(0);
    expect(
      screen.queryByRole('button', { name: /save budgets/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId('budget-dirty-indicator'),
    ).not.toBeInTheDocument();

    const april = within(monthly).getByText('$3,000.00');
    expect(april).toHaveClass('font-mono');
    expect(april).toHaveClass('tabular-nums');
    // `text-sm` is free inside a `<table class="… text-sm">` and has to be
    // written out on a card, or the money renders a size larger than the
    // label beside it.
    expect(april).toHaveClass('text-sm');
    // The value is named by its month in markup, not just by proximity.
    expect(april.tagName).toBe('DD');
    expect(april.previousElementSibling?.tagName).toBe('DT');
    expect(april.previousElementSibling?.textContent).toBe('April');
  });

  test('an unbudgeted month degrades to an em-dash, never to zero', async () => {
    const { monthly } = await renderPhone();

    // Eleven of the twelve months have no row in the fixture. "$0.00" would be
    // a claim the user never made, and `Number('')` is 0 — which is finite —
    // so this is one guard away from being wrong.
    expect(within(monthly).getAllByText('—')).toHaveLength(11);
    expect(within(monthly).queryByText(/NaN/)).not.toBeInTheDocument();
    expect(within(monthly).queryByText('$0.00')).not.toBeInTheDocument();
  });
});

describe('what a category limit card carries — as admin', () => {
  beforeEach(asAdmin);

  test('one editable card per active expense category, in the table order', async () => {
    const { limits } = await renderPhone();

    const fields = within(limits).getAllByRole('spinbutton');
    expect(fields).toHaveLength(2);
    // Rent (sort_order 0) before Groceries (sort_order 1) — the same ordering
    // the table renders, so the swap cannot silently re-sort the list.
    expect(fields[0]).toHaveAccessibleName('Rent limit for April 2026 in USD');
    expect(fields[1]).toHaveAccessibleName(
      'Groceries limit for April 2026 in USD',
    );
    // Groceries has a limit; Rent has none.
    expect((fields[1] as HTMLInputElement).value).toBe('400');
    expect((fields[0] as HTMLInputElement).value).toBe('');

    // Income and inactive categories are excluded here exactly as on the table.
    expect(within(limits).queryByText('Salary')).not.toBeInTheDocument();
    expect(within(limits).queryByText('Old Cable')).not.toBeInTheDocument();
  });

  /**
   * The icon decorates the identity line on the card as it does in the table
   * cell, and stays out of the accessible name — otherwise every field would
   * be announced with an emoji the reader has to guess at.
   */
  test("the card's identity line is the field's label, icon and all", async () => {
    const { limits } = await renderPhone();
    const groceries = within(limits).getByRole('spinbutton', {
      name: 'Groceries limit for April 2026 in USD',
    });

    expect(groceries).not.toHaveAttribute('aria-label');
    const label = document.querySelector(`label[for="${groceries.id}"]`);
    expect(label?.textContent).toBe('🛒Groceries limit for April 2026 in USD');
    expect(label?.querySelector('[aria-hidden="true"]')?.textContent).toBe('🛒');
  });

  test('a limit typed on a card is what Save Category Limits sends', async () => {
    mockedApi.put.mockResolvedValue({});
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const { limits } = await renderPhone();

    await user.type(
      within(limits).getByRole('spinbutton', {
        name: 'Rent limit for April 2026 in USD',
      }),
      '1500',
    );
    await user.click(
      screen.getByRole('button', { name: /save category limits/i }),
    );

    await waitFor(() => {
      expect(mockedApi.put).toHaveBeenCalledWith('category-budgets/2026/4/11', {
        amount: 1500,
      });
    });
    // Rent's id, and only Rent: a card closed over the wrong category would
    // still satisfy a bare "a PUT happened" assertion.
    expect(mockedApi.put).toHaveBeenCalledTimes(1);
  });

  test('the limit field keeps the 44px coarse floor', async () => {
    const { limits } = await renderPhone();
    expect(
      within(limits).getByRole('spinbutton', {
        name: 'Groceries limit for April 2026 in USD',
      }),
    ).toHaveClass('coarse:min-h-11');
  });
});

describe('what a category limit card carries — as member (read-only)', () => {
  beforeEach(asMember);

  test('the card shows the formatted limit and no field at all', async () => {
    const { limits } = await renderPhone();

    expect(within(limits).queryAllByRole('spinbutton')).toHaveLength(0);
    expect(
      screen.queryByRole('button', { name: /save category limits/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId('category-limits-dirty-indicator'),
    ).not.toBeInTheDocument();

    const groceries = within(limits).getByText('$400.00');
    expect(groceries).toHaveClass('font-mono');
    expect(groceries).toHaveClass('tabular-nums');
    expect(groceries).toHaveClass('text-sm');
    expect(groceries.tagName).toBe('DD');
    expect(groceries.previousElementSibling?.textContent).toBe('🛒Groceries');

    // A category with no limit reads as "no limit", not as a zero one.
    expect(within(limits).getAllByText('—')).toHaveLength(1);
  });
});

/**
 * The three utilities that bound a user-supplied category name, written out by
 * hand rather than imported from `Budgets.tsx`.
 *
 * Importing the constant would make this a self-referential oracle: it would
 * assert that whatever the source says equals whatever the source says, and
 * would go on passing with every token deleted. The literal is the whole point.
 */
const IDENTITY_TOKENS = ['min-w-0', 'leading-5', '[overflow-wrap:anywhere]'];

describe('a wrapped category name renders the same for both roles', () => {
  /**
   * READ THE SOURCE COMMENT ON `WRAPPED_CATEGORY_NAME` BEFORE THIS TEST. The
   * `leading-5` token is a pin, not a repair: shadcn's `Label` base carries
   * `leading-none`, but tailwind-merge's `font-size` group overrides `leading`,
   * so the `text-sm` this call site already passed had deleted it before the
   * token existed. Both role views were rendering at 1.25rem and agreeing.
   *
   * That makes the two assertions below different in kind, and neither is
   * decoration:
   *
   *   - the token loop is the PIN, and removing `leading-5` from the shared
   *     constant fails it. That is the regression this guards: the two views
   *     agree because one constant feeds both, not because each was written
   *     the same way twice.
   *   - `not.toHaveClass('leading-none')` is the END STATE — the label must not
   *     render set solid, whatever mechanism gets it there. It passes today for
   *     the tailwind-merge reason and would keep passing with `leading-5`
   *     deleted, so it is NOT evidence for the token. It is evidence for the
   *     invariant, and it is what would catch `CARD_ROW_LABEL_CLASS` losing its
   *     `text-sm` in a future refactor.
   *
   * happy-dom does no layout, so the collision itself stays a browser fact.
   */
  test('both role views carry the same name-bounding utilities', async () => {
    asAdmin();
    const { limits: adminLimits } = await renderPhone();
    const groceries = within(adminLimits).getByRole('spinbutton', {
      name: 'Groceries limit for April 2026 in USD',
    });
    const label = document.querySelector(`label[for="${groceries.id}"]`);
    expect(label).not.toBeNull();
    for (const token of IDENTITY_TOKENS) {
      expect(label).toHaveClass(token);
    }
    // The base's line-height 1 must be GONE, not merely overridden further
    // down the stylesheet — `cn()` runs tailwind-merge, which drops it.
    expect(label).not.toHaveClass('leading-none');

    cleanup();

    asMember();
    const { limits: memberLimits } = await renderPhone();
    const dt = within(memberLimits).getByText('$400.00').previousElementSibling;
    expect(dt?.tagName).toBe('DT');
    for (const token of IDENTITY_TOKENS) {
      expect(dt).toHaveClass(token);
    }
  });
});

describe('the phone card fields ask for the right keypad', () => {
  beforeEach(asAdmin);

  /**
   * `type="number"` says what the value is; `inputMode` says which keypad
   * opens for it, and Android does not reliably infer a decimal one from the
   * type alone. Every amount on both cards has cents, and these cards exist
   * only below `md` — this is the phone-first surface in the app.
   */
  test('both card fields pair type=number with inputmode=decimal', async () => {
    const { monthly, limits } = await renderPhone();

    const april = within(monthly).getByRole('spinbutton', {
      name: 'April budget for 2026 in USD',
    });
    expect(april).toHaveAttribute('type', 'number');
    expect(april).toHaveAttribute('inputmode', 'decimal');

    const groceries = within(limits).getByRole('spinbutton', {
      name: 'Groceries limit for April 2026 in USD',
    });
    expect(groceries).toHaveAttribute('type', 'number');
    expect(groceries).toHaveAttribute('inputmode', 'decimal');
  });
});

describe('the phone says which currency it is asking for', () => {
  beforeEach(asAdmin);

  /**
   * The Category Limits table states the currency in a column header the card
   * list drops, and unlike the Monthly section — whose header still shows
   * "Set all months (USD)" — that left an admin on a phone with no visible
   * currency anywhere. A member was always fine: her values render through
   * `formatCurrency` and carry the symbol. This household enters LBP and USD
   * daily, so which one a bare "400" means is a real question.
   */
  test('the Category Limits description names the base currency', async () => {
    await renderPhone();
    expect(screen.getByText(/Amounts are in USD\./)).toBeInTheDocument();
  });
});

describe('the phone tree carries no width gate of its own', () => {
  beforeEach(asAdmin);

  /**
   * The fork is a JS viewport gate at `md`. A `sm:`-prefixed utility inside
   * either card list would switch at 640px, i.e. in the middle of the range
   * this tree owns, and would disagree with the gate that mounted it — the
   * failure mode the `md:`-only rule exists to prevent. (The two `sm:ml-auto`
   * utilities in the shared card HEADERS predate this fork and are not part of
   * this subtree; they are deliberately left alone.)
   */
  test('no card-list utility is gated on sm:', async () => {
    const { monthly, limits } = await renderPhone();

    for (const list of [monthly, limits]) {
      const gated = [list, ...list.querySelectorAll('*')].filter((el) =>
        Array.from(el.classList).some((token) => token.startsWith('sm:')),
      );
      expect(gated.map((el) => el.className)).toEqual([]);
    }
  });
});
