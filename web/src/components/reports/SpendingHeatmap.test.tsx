import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { HeatmapEntry, PaginatedResponse, Transaction } from '@/api/types';

/**
 * There was NO coverage of this component before this file: the only test that
 * mounted it (`yearPicker.test.tsx`) mocks `useSpendingHeatmap` to `[]`, so
 * zero cells ever rendered and a complete rewrite passed the whole suite
 * unchanged. Everything here is written from scratch, which is exactly why
 * each viewport-gated block carries a positive control — nothing else would
 * tell us a block had gone vacuous.
 */

const apiGet = vi.fn();
/**
 * ONE spy, path-routed. The sheet reads the household's base currency as well
 * as the day's rows, and a single `mockResolvedValue` served the currencies
 * query a `PaginatedResponse` — `useCurrencies` then called `.find` on it and
 * the whole subtree threw. Routing on the path keeps every assertion about the
 * transactions call (`apiGet.mock.calls`) readable.
 */
vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/client')>();
  return {
    ...actual,
    api: {
      ...actual.api,
      get: (path: string) =>
        path.startsWith('currencies')
          ? Promise.resolve(CURRENCIES)
          : apiGet(path),
    },
  };
});

const CURRENCIES = [
  {
    code: 'USD',
    name: 'US Dollar',
    symbol: '$',
    rate_to_base: 1,
    is_base: true,
    updated_at: '2026-01-01T00:00:00Z',
  },
  {
    code: 'LBP',
    name: 'Lebanese Pound',
    symbol: 'L\u00a3',
    rate_to_base: 0.0000112,
    is_base: false,
    updated_at: '2026-01-01T00:00:00Z',
  },
];

import { SpendingHeatmap } from './SpendingHeatmap';
import { OPACITY_STOPS } from './heatmapGrid';

// ---------------------------------------------------------------------------
// Viewport control
// ---------------------------------------------------------------------------

interface HappyDomWindow {
  happyDOM?: {
    setViewport: (viewport: { width?: number; height?: number }) => void;
  };
}

/** Comfortably above Tailwind's `md`, and happy-dom's own default. */
const DESKTOP_WIDTH = 1024;
/** Galaxy S24 — the narrowest device this household actually uses. */
const PHONE_WIDTH = 360;

/**
 * Drive the real viewport rather than stubbing `window.matchMedia`.
 *
 * happy-dom evaluates media queries against `window.innerWidth`, so what runs
 * here is the real gate — `useIsMobileViewport` NEGATES `(min-width: 768px)`.
 * A hand-rolled matchMedia stub returns a fixed `matches` whatever it is
 * asked, and would keep every phone test below green even if the gate were
 * inverted.
 */
function setViewportWidth(width: number): void {
  const controllable = window as unknown as HappyDomWindow;
  if (!controllable.happyDOM) {
    // Loud on purpose: falling back silently would run every phone test at
    // desktop width, where the month grid does not exist at all.
    throw new Error(
      'happy-dom viewport control is unavailable — mobile tests cannot run',
    );
  }
  controllable.happyDOM.setViewport({ width });
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const YEAR = 2026;
/** Local noon so `formatYYYYMMDD(new Date())` is 2026-03-04 in any timezone. */
const TODAY = new Date(2026, 2, 4, 12, 0, 0);

const usd = (amount: number) =>
  amount.toLocaleString('en-US', { style: 'currency', currency: 'USD' });

/**
 * Eight spending days with distinct totals: four in March and four larger
 * ones in July. The split is deliberate — it is what makes a year-scoped
 * scale and a month-scoped one give DIFFERENT answers for March's days, so
 * the phone's scale can actually be pinned. With March's days alone the two
 * scopes agree and that assertion would be vacuous.
 *
 * 2026-03-04 is today. 2026-03-03 has no row and is PAST (a real zero);
 * 2026-03-05 has no row and is FUTURE (upcoming). The two are deliberately
 * adjacent, because telling them apart is the whole of D2.
 */
const DATA: HeatmapEntry[] = [
  { date: '2026-03-02', total: 10 },
  { date: '2026-03-04', total: 42.5 },
  { date: '2026-03-10', total: 100 },
  { date: '2026-03-20', total: 250 },
  { date: '2026-07-15', total: 500 },
  { date: '2026-07-16', total: 600 },
  { date: '2026-07-17', total: 700 },
  { date: '2026-07-18', total: 800 },
];

function transaction(over: Partial<Transaction>): Transaction {
  return {
    id: 1,
    user_id: 1,
    created_by: 'Elie',
    date: '2026-03-04',
    amount: 42.5,
    original_amount: null,
    original_currency: null,
    description: 'Groceries',
    category_id: 3,
    category_name: 'Food',
    category_type: 'expense',
    tags: null,
    notes: null,
    created_at: '2026-03-04T10:00:00Z',
    updated_at: '2026-03-04T10:00:00Z',
    ...over,
  };
}

function page(rows: Transaction[], total = rows.length): PaginatedResponse<Transaction> {
  return { transactions: rows, total, page: 1, per_page: 100 };
}

function renderHeatmap(year = YEAR, data = DATA) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <SpendingHeatmap data={data} year={year} format={usd} />
    </QueryClientProvider>,
  );
}

function setup() {
  // An open Radix overlay sets `pointer-events: none` on <body>, and happy-dom
  // has no layout engine to tell the portalled content apart. Same reason
  // yearPicker.test.tsx and Reports.test.tsx turn the check off.
  return userEvent.setup({
    advanceTimers: vi.advanceTimersByTime,
    pointerEventsCheck: 0,
  });
}

// ---------------------------------------------------------------------------
// Positive controls — every viewport-gated assertion goes through one of these
// ---------------------------------------------------------------------------

/**
 * Render at phone width and hand back the month grid, having first proved the
 * swap actually took.
 *
 * The two grids look alike to a weak assertion: both are grids of dated cells
 * with the same roles and the same label wording. So the control asserts on
 * something STRUCTURALLY impossible in the other branch — 31 day buttons,
 * which a 365-day year grid can never satisfy — and that the year grid is
 * ABSENT. Without the absence half, a component that mounted the desktop tree
 * at every width would still pass most of what follows.
 */
function renderMonthGrid(year = YEAR, data = DATA): HTMLElement {
  setViewportWidth(PHONE_WIDTH);
  renderHeatmap(year, data);
  const grid = screen.getByRole('grid', {
    name: 'Spending heatmap for March 2026',
  });
  expect(within(grid).getAllByRole('button')).toHaveLength(31);
  expect(
    screen.queryByRole('grid', { name: `Spending heatmap for ${year}` }),
  ).not.toBeInTheDocument();
  return grid;
}

/** The desktop half of the same control. */
function renderYearGrid(year = YEAR, data = DATA): HTMLElement {
  setViewportWidth(DESKTOP_WIDTH);
  renderHeatmap(year, data);
  const grid = screen.getByRole('grid', {
    name: `Spending heatmap for ${year}`,
  });
  expect(within(grid).getAllByRole('button').length).toBeGreaterThan(360);
  expect(screen.queryByRole('group', { name: 'Heatmap month' })).not.toBeInTheDocument();
  return grid;
}

/**
 * Drive the POINTER for real. happy-dom derives `(hover)`/`(pointer)` from
 * `navigator.maxTouchPoints`, so the real query still gets parsed and
 * evaluated — unlike a matchMedia stub, which would answer whatever it is
 * asked and keep these tests green with the gate inverted. Must be set before
 * render: no `change` event fires for it.
 */
function setPointer(kind: 'fine' | 'coarse'): void {
  Object.defineProperty(window.navigator, 'maxTouchPoints', {
    configurable: true,
    value: kind === 'fine' ? 0 : 5,
  });
}

/** Every focusable day cell in the grid, in DOM order. */
function dayButtons(grid: HTMLElement): HTMLElement[] {
  return within(grid).getAllByRole('button');
}

function cell(grid: HTMLElement, date: string): HTMLElement {
  const el = grid.querySelector<HTMLElement>(`[data-heatmap-date="${date}"]`);
  if (el === null) throw new Error(`no cell for ${date}`);
  return el;
}

/**
 * `.focus()` fires the cell's `onFocus`, which moves the roving index — a
 * state update, so it has to be wrapped or React logs an act() warning that
 * the default reporter then buries.
 */
function focusCell(el: HTMLElement): void {
  act(() => {
    el.focus();
  });
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.setSystemTime(TODAY);
  apiGet.mockReset();
  apiGet.mockResolvedValue(page([]));
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  setViewportWidth(DESKTOP_WIDTH);
  setPointer('fine');
});

// ---------------------------------------------------------------------------

describe('which presentation mounts', () => {
  test('below md the card is a month grid with a month picker', () => {
    const grid = renderMonthGrid();
    // Weekday column headers exist only on the phone, where a column IS a
    // weekday and the header is exactly right.
    expect(within(grid).getAllByRole('columnheader')).toHaveLength(7);
    expect(screen.getByRole('group', { name: 'Heatmap month' })).toBeInTheDocument();
  });

  test('at md and above the card keeps the year grid', () => {
    const grid = renderYearGrid();
    expect(within(grid).getAllByRole('row')).toHaveLength(7);
  });

  test('the legend renders on both viewports', () => {
    renderMonthGrid();
    expect(
      screen.getByRole('group', { name: 'Spending intensity legend' }),
    ).toBeInTheDocument();
    cleanup();
    renderYearGrid();
    expect(
      screen.getByRole('group', { name: 'Spending intensity legend' }),
    ).toBeInTheDocument();
  });
});

describe('the year grid derives its column count from the year', () => {
  // The 54-column case is the whole point. 1984 is a leap year starting on a
  // Sunday — 378 cells — and it is the year `yearPicker.test.tsx` already
  // drives this component with, which is how a hardcoded `repeat(53, 1fr)`
  // survived: nothing looked at the columns.
  test.each([
    [2026, 53],
    [1984, 54],
  ])('%i renders %i columns of 7', (year, columns) => {
    const grid = renderYearGrid(year, []);
    const rows = within(grid).getAllByRole('row');
    expect(rows).toHaveLength(7);
    for (const row of rows) {
      expect(within(row).getAllByRole('gridcell')).toHaveLength(columns);
    }
  });

  test('padding cells are gridcells with no control inside', () => {
    // Paired presence + absence, in one test: an absence assertion alone
    // passes when nothing rendered at all.
    const grid = renderYearGrid(2026, []);
    const cells = within(grid).getAllByRole('gridcell');
    expect(cells).toHaveLength(371);
    expect(dayButtons(grid)).toHaveLength(365);
    // 2026 opens on a Thursday, so the first three cells of the Mon/Tue/Wed
    // rows are pads. They stay in the DOM — dropping them would shift every
    // following cell onto the wrong weekday.
    const firstRow = within(grid).getAllByRole('row')[0];
    const firstCell = within(firstRow).getAllByRole('gridcell')[0];
    expect(within(firstCell).queryByRole('button')).not.toBeInTheDocument();
    expect(firstCell).toBeEmptyDOMElement();
  });

  test('month labels are placed once each and kept out of the a11y tree', () => {
    renderYearGrid(2026, []);
    for (const label of ['Jan', 'Mar', 'Dec']) {
      expect(screen.getAllByText(label)).toHaveLength(1);
    }
    // A label sits over the column its month STARTS in, whose first day can be
    // six days earlier — so it must not be announced as a column header.
    const grid = screen.getByRole('grid', { name: 'Spending heatmap for 2026' });
    expect(within(grid).queryAllByRole('columnheader')).toHaveLength(0);
  });
});

describe('every cell says what it is', () => {
  test('a spending day names its weekday, date and amount exactly', () => {
    const grid = renderYearGrid();
    expect(cell(grid, '2026-03-02')).toHaveAttribute(
      'aria-label',
      'Monday, March 2nd, 2026: $10.00 spent',
    );
  });

  test('a day with no expense rows says "no spending", not "$0.00"', () => {
    const grid = renderYearGrid();
    expect(cell(grid, '2026-03-03')).toHaveAttribute(
      'aria-label',
      'Tuesday, March 3rd, 2026: no spending',
    );
  });

  test('today is announced as today, on the phone too', () => {
    const grid = renderMonthGrid();
    expect(cell(grid, '2026-03-04')).toHaveAttribute(
      'aria-label',
      'Today, Wednesday, March 4th, 2026: $42.50 spent',
    );
  });

  test('the phone cell prints the day number under that label', () => {
    const grid = renderMonthGrid();
    expect(cell(grid, '2026-03-04')).toHaveTextContent('4');
  });
});

describe('intensity', () => {
  test('the year’s largest spend paints the darkest stop, the smallest the palest', () => {
    const grid = renderYearGrid();
    const chip = (date: string) =>
      cell(grid, date).querySelector<HTMLElement>('span[aria-hidden="true"]')!;
    expect(chip('2026-07-18').style.opacity).toBe(String(OPACITY_STOPS[3]));
    expect(chip('2026-03-02').style.opacity).toBe(String(OPACITY_STOPS[0]));
  });

  test('a day with no rows carries no inline background at all', () => {
    const grid = renderYearGrid();
    const chip = cell(grid, '2026-03-03').querySelector<HTMLElement>(
      'span[aria-hidden="true"]',
    )!;
    expect(chip.style.backgroundColor).toBe('');
    expect(chip.style.opacity).toBe('');
    expect(chip.className.split(/\s+/)).toContain('bg-muted');
  });

  test('the phone scale is the YEAR’s, so months stay comparable', () => {
    // March's own days are 10 / 42.50 / 100 / 250, and July's four are all
    // larger. Ranked against the whole year, 250 lands in the SECOND quartile
    // and 42.50 in the first; ranked against March alone they would be the
    // darkest and the second-palest. Both assertions therefore fail the moment
    // the scale is scoped to the displayed month.
    const grid = renderMonthGrid();
    const chip = (date: string) =>
      cell(grid, date).querySelector<HTMLElement>('span[aria-hidden="true"]')!;
    expect(chip('2026-03-20').style.opacity).toBe(String(OPACITY_STOPS[1]));
    expect(chip('2026-03-04').style.opacity).toBe(String(OPACITY_STOPS[0]));
  });
});

/**
 * Which colour token applies in each theme, derived from the class list.
 * `dark:` is how Tailwind expresses the theme axis, so parsing it out IS the
 * theme dimension — happy-dom computes no colours, so the RATIOS are a browser
 * check (recorded beside each row) and what is pinned here is the mapping the
 * browser then measures.
 */
function themeTokens(el: Element): { light: string; dark: string } {
  const tokens = el.className.split(/\s+/);
  const base = tokens.find((t) =>
    /^text-(foreground|primary-foreground|muted-foreground)(\/\d+)?$/.test(t),
  );
  const override = tokens
    .find((t) => /^dark:text-/.test(t))
    ?.replace(/^dark:/, '');
  return { light: base ?? 'NONE', dark: override ?? base ?? 'NONE' };
}

function numberSpan(el: HTMLElement): Element {
  const span = [...el.querySelectorAll('span')].find(
    (x) => !x.hasAttribute('aria-hidden'),
  );
  if (span === undefined) throw new Error('no number span');
  return span;
}

describe('the day number stays readable on its own chip', () => {
  // Browser-measured ratios, light / dark at 360px, number vs its own
  // composited chip. The 0.50 row is why this is a per-stop TABLE and not a
  // threshold: the chip is mid-grey in BOTH themes, so the two need opposite
  // tokens, and a single threshold fixes one theme by breaking the other.
  const DAY_STOPS: {
    stop: string;
    date: string;
    month: string;
    light: string;
    dark: string;
    ratios: string;
  }[] = [
    { stop: '0.30', date: '2026-03-02', month: 'March', light: 'text-foreground', dark: 'text-foreground', ratios: '10.23 / 5.85' },
    { stop: '0.50', date: '2026-03-10', month: 'March', light: 'text-foreground', dark: 'text-primary-foreground', ratios: '5.89 / 5.00' },
    { stop: '0.75', date: '2026-07-15', month: 'July', light: 'text-primary-foreground', dark: 'text-primary-foreground', ratios: '7.50 / 9.85' },
    { stop: '1.00', date: '2026-07-18', month: 'July', light: 'text-primary-foreground', dark: 'text-primary-foreground', ratios: '16.97 / 16.97' },
    { stop: 'no-spend', date: '2026-03-03', month: 'March', light: 'text-foreground/60', dark: 'text-foreground/60', ratios: '5.14 / 5.78' },
    { stop: 'upcoming', date: '2026-03-05', month: 'March', light: 'text-muted-foreground', dark: 'text-muted-foreground', ratios: '4.83 / 5.29' },
  ];

  test.each(DAY_STOPS)(
    'day cell at stop $stop is $light / $dark ($ratios)',
    async ({ stop, date, month, light, dark }) => {
      const user = setup();
      renderMonthGrid();
      if (month !== 'March') {
        await user.click(
          screen.getByRole('button', { name: new RegExp(`^${month} 2026:`) }),
        );
      }
      const grid = screen.getByRole('grid', {
        name: `Spending heatmap for ${month} 2026`,
      });
      expect(themeTokens(numberSpan(cell(grid, date)))).toEqual({
        light,
        dark,
      });
      // Guards the table itself: if the fixture drifts and `date` stops
      // landing on `stop`, the row above would still pass while describing
      // the wrong cell.
      const chip = cell(grid, date).querySelector<HTMLElement>(
        'span[aria-hidden="true"]',
      )!;
      const actual =
        chip.style.opacity !== ''
          ? Number(chip.style.opacity).toFixed(2)
          : chip.className.includes('bg-muted')
            ? 'no-spend'
            : 'upcoming';
      expect(actual).toBe(stop === '0.30' ? '0.30' : stop);
    },
  );

  // The SAME function paints the month picker, which is how the 0.50 failure
  // reached three of its twelve chips too. This fixture puts March on 0.50 —
  // the failing stop — and July on 1.00.
  test.each([
    { stop: '0.50', name: 'March', light: 'text-foreground', dark: 'text-primary-foreground' },
    { stop: '1.00', name: 'July', light: 'text-primary-foreground', dark: 'text-primary-foreground' },
    { stop: 'no-spend', name: 'January', light: 'text-foreground/60', dark: 'text-foreground/60' },
  ])(
    'month picker chip for $name (stop $stop) is $light / $dark',
    ({ name, light, dark }) => {
      renderMonthGrid();
      const button = screen.getByRole('button', {
        name: new RegExp(`^${name} 2026:`),
      });
      expect(themeTokens(numberSpan(button))).toEqual({ light, dark });
    },
  );
});

describe('today is marked where the marking can be seen', () => {
  // D1: the ring used to sit on the chip span, which carries the inline
  // `opacity` — and CSS opacity fades an element's own ring with it. Measured
  // ring-vs-chip there: 1.02-1.08:1, i.e. invisible on every day that HAD
  // spending, and visible only on days that had none. A perfect inversion of
  // the intent.
  test.each([
    ['the year grid', renderYearGrid],
    ['the month grid', renderMonthGrid],
  ])('%s rings the BUTTON, never the opacity-carrying chip', (_n, renderGrid) => {
    const grid = renderGrid();
    const today = cell(grid, '2026-03-04');
    const chip = today.querySelector<HTMLElement>('span[aria-hidden="true"]')!;

    expect(today.className.split(/\s+/)).toContain('ring-1');
    expect(chip.className.split(/\s+/)).not.toContain('ring-1');
    // Positive control: today really does have spending here, so a ring on the
    // chip would be the faded, invisible case rather than a coincidence.
    expect(chip.style.opacity).not.toBe('');

    // And no other cell claims to be today.
    expect(
      dayButtons(grid).filter((b) => b.className.split(/\s+/).includes('ring-1')),
    ).toHaveLength(1);
  });
});

describe('a day that has not happened yet', () => {
  // BOTH grids. The desktop year grid runs past today too — a year opened in
  // March has nine months of future cells — and its own copy of the paint
  // condition survived a mutation run when only the phone was covered here.
  test.each([
    ['the month grid', renderMonthGrid],
    ['the year grid', renderYearGrid],
  ])('%s paints no chip at all, so it cannot read as a real zero', (_n, renderGrid) => {
    const grid = renderGrid();
    const chip = (date: string) =>
      cell(grid, date).querySelector<HTMLElement>('span[aria-hidden="true"]')!;
    // 03-05 and 03-03 are adjacent days with identical data (none). Only the
    // paint tells them apart, so both halves are asserted together.
    expect(chip('2026-03-05').className.split(/\s+/)).not.toContain('bg-muted');
    expect(chip('2026-03-05').style.backgroundColor).toBe('');
    expect(chip('2026-03-03').className.split(/\s+/)).toContain('bg-muted');
  });

  test('announces "upcoming" rather than asserting a fact about the future', () => {
    const grid = renderMonthGrid();
    expect(cell(grid, '2026-03-05')).toHaveAttribute(
      'aria-label',
      'Thursday, March 5th, 2026: upcoming',
    );
    expect(cell(grid, '2026-03-03')).toHaveAttribute(
      'aria-label',
      'Tuesday, March 3rd, 2026: no spending',
    );
  });

  test('its sheet says so too, instead of reporting no expenses', async () => {
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-05'));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Upcoming')).toBeInTheDocument();
    expect(
      within(dialog).getByText('This day has not happened yet.'),
    ).toBeInTheDocument();
    expect(apiGet).not.toHaveBeenCalled();
  });
});

describe('the tab budget', () => {
  test.each([
    ['the year grid', renderYearGrid],
    ['the month grid', renderMonthGrid],
  ])('%s has exactly one tab stop', (_name, renderGrid) => {
    const grid = renderGrid();
    const stops = dayButtons(grid).filter((b) => b.tabIndex === 0);
    expect(stops).toHaveLength(1);
    // …and it is today's cell, which is where a keyboard user wants to land.
    expect(stops[0]).toHaveAttribute('data-heatmap-date', '2026-03-04');
  });

  test('a year with no today falls back to the first day of the grid', () => {
    const grid = renderYearGrid(2024, []);
    const stops = dayButtons(grid).filter((b) => b.tabIndex === 0);
    expect(stops).toHaveLength(1);
    expect(stops[0]).toHaveAttribute('data-heatmap-date', '2024-01-01');
  });
});

describe('arrow keys follow the visual axes', () => {
  // `fireEvent.keyDown`, not `user-event`: this project has a recorded finding
  // that user-event's key strings never reach `onKeyDown` under happy-dom, and
  // the whole block would then pass with the handler deleted. The first case
  // doubles as the positive control that the handler fires at all.
  test('on the year grid a row is a WEEKDAY: left/right moves a week', () => {
    const grid = renderYearGrid();
    const start = cell(grid, '2026-03-04');
    focusCell(start);
    fireEvent.keyDown(start, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(cell(grid, '2026-03-11'));
    fireEvent.keyDown(cell(grid, '2026-03-11'), { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(cell(grid, '2026-03-04'));
  });

  test('on the year grid up/down moves a day', () => {
    const grid = renderYearGrid();
    const start = cell(grid, '2026-03-04');
    focusCell(start);
    fireEvent.keyDown(start, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(cell(grid, '2026-03-05'));
    fireEvent.keyDown(cell(grid, '2026-03-05'), { key: 'ArrowUp' });
    expect(document.activeElement).toBe(cell(grid, '2026-03-04'));
  });

  test('on the month grid a row is a WEEK: left/right moves a day, up/down a week', () => {
    const grid = renderMonthGrid();
    const start = cell(grid, '2026-03-04');
    focusCell(start);
    fireEvent.keyDown(start, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(cell(grid, '2026-03-05'));
    fireEvent.keyDown(cell(grid, '2026-03-05'), { key: 'ArrowDown' });
    expect(document.activeElement).toBe(cell(grid, '2026-03-12'));
  });

  test('moving the tab stop moves it — the roving index follows focus', () => {
    const grid = renderMonthGrid();
    const start = cell(grid, '2026-03-04');
    focusCell(start);
    fireEvent.keyDown(start, { key: 'ArrowRight' });
    const stops = dayButtons(grid).filter((b) => b.tabIndex === 0);
    expect(stops).toHaveLength(1);
    expect(stops[0]).toHaveAttribute('data-heatmap-date', '2026-03-05');
  });

  test('focus stops at the edge instead of leaving the grid or landing on a pad', () => {
    const grid = renderMonthGrid();
    // March 2026 opens on a Sunday, so the first six cells of week 1 are pads
    // — the month grid shows no adjacent-month days. ArrowLeft and ArrowUp
    // from the 1st must land on neither a pad nor nothing at all.
    const first = cell(grid, '2026-03-01');
    focusCell(first);
    fireEvent.keyDown(first, { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(first);
    fireEvent.keyDown(first, { key: 'ArrowUp' });
    expect(document.activeElement).toBe(first);
  });

  test('the year grid steps OVER the leading pads rather than onto them', () => {
    // 2026 opens on a Thursday, so Mon/Tue/Wed of column 0 are pads. ArrowLeft
    // from Jan 5 (the first Monday, column 1) has a pad to its left and must
    // not stop there — nor wrap to the end of the row.
    const grid = renderYearGrid();
    const monday = cell(grid, '2026-01-05');
    focusCell(monday);
    fireEvent.keyDown(monday, { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(monday);
  });

  test('on the year grid the row edges are WEEKS apart, and the grid edges are weekday-major', () => {
    // M4: Home/End were only ever driven on the month grid. On the year grid a
    // row is a weekday, so Home/End walk to the first/last occurrence of THAT
    // weekday in the year — and Ctrl+End lands on the last SUNDAY, 2026-12-27,
    // not December 31st. That is the APG behaviour (last cell of the last row,
    // a spatial move) and it is the surprising one, so it is pinned.
    const grid = renderYearGrid();
    const start = cell(grid, '2026-03-04'); // a Wednesday
    focusCell(start);
    fireEvent.keyDown(start, { key: 'Home' });
    expect(document.activeElement).toBe(cell(grid, '2026-01-07'));
    fireEvent.keyDown(document.activeElement!, { key: 'End' });
    expect(document.activeElement).toBe(cell(grid, '2026-12-30'));
    fireEvent.keyDown(document.activeElement!, { key: 'End', ctrlKey: true });
    expect(document.activeElement).toBe(cell(grid, '2026-12-27'));
    fireEvent.keyDown(document.activeElement!, { key: 'Home', ctrlKey: true });
    expect(document.activeElement).toBe(cell(grid, '2026-01-05'));
  });

  test('Home and End walk the row, Ctrl+Home and Ctrl+End the grid', () => {
    const grid = renderMonthGrid();
    const start = cell(grid, '2026-03-04');
    focusCell(start);
    fireEvent.keyDown(start, { key: 'Home' });
    expect(document.activeElement).toBe(cell(grid, '2026-03-02'));
    fireEvent.keyDown(document.activeElement!, { key: 'End' });
    expect(document.activeElement).toBe(cell(grid, '2026-03-08'));
    fireEvent.keyDown(document.activeElement!, { key: 'End', ctrlKey: true });
    expect(document.activeElement).toBe(cell(grid, '2026-03-31'));
    fireEvent.keyDown(document.activeElement!, { key: 'Home', ctrlKey: true });
    expect(document.activeElement).toBe(cell(grid, '2026-03-01'));
  });
});

describe('the month picker', () => {
  test('picking a month swaps the grid and renames it', async () => {
    const user = setup();
    renderMonthGrid();
    await user.click(screen.getByRole('button', { name: /^February 2026:/ }));

    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for February 2026',
    });
    // 28 buttons is a shape neither March (31) nor the year grid (365) has.
    expect(within(grid).getAllByRole('button')).toHaveLength(28);
  });

  test('the shown month is the pressed one', () => {
    renderMonthGrid();
    const march = screen.getByRole('button', { name: /^March 2026:/ });
    const april = screen.getByRole('button', { name: /^April 2026:/ });
    expect(march).toHaveAttribute('aria-pressed', 'true');
    expect(april).toHaveAttribute('aria-pressed', 'false');
  });

  test('the selected month is ringed with an OFFSET, not an inset', () => {
    // Browser-found: an inset ring on the year's busiest month is drawn in
    // `--ring`, which is the same near-white as a full-opacity chip in dark
    // mode — the selection was invisible on exactly the month most likely to
    // be selected. The 2px of card colour between chip and ring is the fix,
    // and it is a class token, so nothing else can see it go.
    renderMonthGrid();
    const tokens = screen
      .getByRole('button', { name: /^March 2026:/ })
      .className.split(/\s+/);
    expect(tokens).toContain('ring-offset-2');
    expect(tokens).toContain('ring-offset-card');
    expect(tokens).not.toContain('ring-inset');
    // The ring is drawn 4px outside the button, so the row's gap has to be
    // wider than the 4px it was before — otherwise it lands on the month next
    // to it. The two tokens are one decision and go stale together.
    expect(
      screen
        .getByRole('group', { name: 'Heatmap month' })
        .className.split(/\s+/),
    ).toContain('gap-1.5');
  });

  test('a month names its own total, so the picker is readable too', () => {
    renderMonthGrid();
    expect(
      screen.getByRole('button', { name: 'March 2026: $402.50 spent' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'January 2026: no spending' }),
    ).toBeInTheDocument();
  });
});

describe('tapping a day', () => {
  test('opens a sheet whose total is the CELL’s total, and lists that day’s expenses', async () => {
    // 30 + 5 = 35, NOT 42.50. The rows deliberately do NOT sum to the cell's
    // total: with 30 and 12.50 they did, and a header that SUMMED THE FETCHED
    // ROWS passed this test under its own name. 42.50 is now reachable only
    // from the cell.
    apiGet.mockResolvedValue(
      page([
        transaction({ id: 7, description: 'Butcher', amount: 30 }),
        transaction({ id: 8, description: 'Bakery', amount: 5 }),
      ]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Wednesday, March 4th, 2026')).toBeInTheDocument();
    // 42.50 is the heatmap cell's own number, threaded straight through — not
    // a sum of the rows below, which is what makes them impossible to disagree.
    expect(
      within(dialog).getByText('$42.50 spent \u00b7 Expenses only'),
    ).toBeInTheDocument();
    expect(await within(dialog).findByText('Butcher')).toBeInTheDocument();
    expect(within(dialog).getByText('Bakery')).toBeInTheDocument();
  });

  test('the fetch is expenses-only and scoped to that one day', async () => {
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    await screen.findByRole('dialog');

    expect(apiGet).toHaveBeenCalledTimes(1);
    const url = apiGet.mock.calls[0][0] as string;
    // Expenses only, because the cell's total came from an expenses-only
    // query. A mixed list would show a total that disagrees with the cell.
    expect(url).toContain('type=expense');
    expect(url).toContain('date_from=2026-03-04');
    expect(url).toContain('date_to=2026-03-04');
    // The sort and the page size are not incidental: "Showing the N largest of
    // M" is only true if the page IS the largest N. Drop `sort_by` and the
    // server falls back to date DESC, which makes that sentence a false claim
    // about an arbitrary hundred rows — with every test still green.
    expect(url).toContain('sort_by=amount');
    expect(url).toContain('sort_dir=desc');
    expect(url).toContain('per_page=100');
  });

  test('a day with no spending opens the sheet without fetching anything', async () => {
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-03'));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('No spending')).toBeInTheDocument();
    expect(
      within(dialog).getByText('No expenses were recorded on this day.'),
    ).toBeInTheDocument();
    expect(apiGet).not.toHaveBeenCalled();
  });

  test('the desktop cell opens the same sheet, so a keyboard user is not stuck', async () => {
    const user = setup();
    const grid = renderYearGrid();
    await user.click(cell(grid, '2026-07-15'));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Wednesday, July 15th, 2026')).toBeInTheDocument();
    expect(
      within(dialog).getByText('$500.00 spent \u00b7 Expenses only'),
    ).toBeInTheDocument();
  });

  test('a row with no creator on the wire still names someone', async () => {
    apiGet.mockResolvedValue(
      page([transaction({ id: 9, description: 'Orphan', created_by: '' })]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));

    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText('Unknown')).toBeInTheDocument();
  });

  test('a day with more rows than one page says so', async () => {
    apiGet.mockResolvedValue(page([transaction({ id: 1 })], 140));
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));

    const dialog = await screen.findByRole('dialog');
    expect(
      await within(dialog).findByText('Showing the 1 largest of 140 expenses.'),
    ).toBeInTheDocument();
  });

  test('the date and the total stay OUT of the body scroller', async () => {
    // They are the answer the sheet exists to give. Inside the scroller they
    // left the screen after 60px of scroll on a 20-row day, with 8 rows still
    // visible — measured at 360px before the fix.
    apiGet.mockResolvedValue(
      page([transaction({ id: 7, description: 'Butcher', amount: 30 })]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');
    const scroller = dialog.querySelector('.overflow-y-auto');
    expect(scroller).not.toBeNull();

    expect(
      scroller!.contains(
        within(dialog).getByText('Wednesday, March 4th, 2026'),
      ),
    ).toBe(false);
    expect(
      scroller!.contains(
        within(dialog).getByText('$42.50 spent \u00b7 Expenses only'),
      ),
    ).toBe(false);
    // The positive control, and it earns its keep: "is outside the scroller"
    // is equally true of an element that never rendered, and of a sheet that
    // lost its scroller entirely.
    expect(
      scroller!.contains(await within(dialog).findByText('Butcher')),
    ).toBe(true);
  });

  test('the list is named with the DATE, not "this day"', async () => {
    apiGet.mockResolvedValue(
      page([transaction({ id: 7, description: 'Butcher', amount: 30 })]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');
    // The pinned header is a SIGHTED affordance. Inside the list there is no
    // equivalent of glancing back up at it, so a deictic name would leave a
    // screen-reader user with neither the day nor the total.
    expect(
      await within(dialog).findByRole('list', {
        name: 'Expenses on Wednesday, March 4th, 2026',
      }),
    ).toBeInTheDocument();
  });

  test('says the list is filtered, because the cell total is filtered too', async () => {
    apiGet.mockResolvedValue(
      page([transaction({ id: 7, description: 'Butcher', amount: 30 })]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');
    // Nothing else on screen said the rows were filtered, so a member who
    // knows she was also PAID that day could not tell whether the income was
    // excluded or missing from the ledger.
    expect(
      within(dialog).getByText('$42.50 spent \u00b7 Expenses only'),
    ).toBeInTheDocument();
  });

  test('a foreign-currency row keeps the amount the user actually entered', async () => {
    // This household enters LBP daily. "$16.76" alone is not the row he
    // remembers as a 1.5M grocery run.
    apiGet.mockResolvedValue(
      page([
        transaction({
          id: 7,
          description: 'Groceries',
          amount: 16.76,
          original_amount: 1500000,
          original_currency: 'LBP',
        }),
      ]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText('$16.76')).toBeInTheDocument();
    expect(within(dialog).getByText('1,500,000.00 LBP')).toBeInTheDocument();
  });

  test('a base-currency row shows no second amount', async () => {
    // The absence half. Without it the test above passes against a component
    // that prints the original line unconditionally, including for USD rows
    // where it would just repeat the number.
    apiGet.mockResolvedValue(
      page([
        transaction({
          id: 8,
          description: 'Bakery',
          amount: 12.5,
          original_amount: 12.5,
          original_currency: 'USD',
        }),
      ]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByText('$12.50');
    expect(within(dialog).queryByText(/USD$/)).not.toBeInTheDocument();
  });

  test('a failed fetch offers Retry, and Retry re-requests', async () => {
    apiGet.mockRejectedValueOnce(new Error('Network unreachable'));
    apiGet.mockResolvedValue(
      page([transaction({ id: 9, description: 'Recovered', amount: 5 })]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');

    expect(await within(dialog).findByText('Network unreachable')).toBeInTheDocument();
    const before = apiGet.mock.calls.length;
    await user.click(within(dialog).getByRole('button', { name: 'Retry' }));

    // Both halves: the row arrives AND a second request was actually made.
    // Recovery already worked by closing and re-tapping, so asserting only
    // that the row appears would pass against a Retry button that did nothing
    // but re-render.
    expect(await within(dialog).findByText('Recovered')).toBeInTheDocument();
    expect(apiGet.mock.calls.length).toBeGreaterThan(before);
  });

  test('closing hands focus back to the cell it opened from', async () => {
    const user = setup();
    const grid = renderMonthGrid();
    const tapped = cell(grid, '2026-03-04');
    await user.click(tapped);
    await screen.findByRole('dialog');

    // This sheet has no SheetTrigger, so Radix's own restore focuses a null
    // ref and focus lands on <body> unless the component says otherwise.
    await user.keyboard('{Escape}');
    expect(document.activeElement).toBe(tapped);
  });
});

describe('the phone grid buys back the room a 44px target needs', () => {
  // happy-dom lays nothing out, so what is pinned here is the MECHANISM that
  // produces the size — the floor token and the two width levers. The rendered
  // pixels at 360 are a browser check, not this.
  test('every tappable thing on the phone carries the 44px floor', () => {
    const grid = renderMonthGrid();
    // `min-h-11` is 11 × 0.25rem = 44px. Exact token membership, never a
    // substring: `toContain('min-h-1')` would also match `min-h-10`.
    for (const button of dayButtons(grid)) {
      expect(button.className.split(/\s+/)).toContain('min-h-11');
    }
    const march = screen.getByRole('button', { name: /^March 2026:/ });
    expect(march.className.split(/\s+/)).toContain('min-h-11');
  });

  test('the grid cancels part of the card padding and caps its width', () => {
    // At 360px the card's own `p-6` leaves 278px, which is 37px per column —
    // under the floor above. `-mx-4` is what buys it back. `max-w-[420px]` is
    // the other end: a 720px tablet is BELOW `md`, so it renders this branch
    // and would otherwise get 95px cells.
    const grid = renderMonthGrid();
    const inner = grid.parentElement!;
    const outer = inner.parentElement!;
    expect(inner.className.split(/\s+/)).toContain('max-w-[420px]');
    expect(outer.className.split(/\s+/)).toContain('-mx-4');
  });
});

describe('the desktop tooltip survives the rebuild', () => {
  test('the year cell is a Radix tooltip trigger and the month cell is not', () => {
    // Radix stamps `data-state` on every Trigger. Asserting it on one branch
    // and its absence on the other pins BOTH halves of the decision: mouse
    // hover keeps working on desktop, and the phone does not carry 31 tooltip
    // roots that can never open on touch.
    const yearGrid = renderYearGrid();
    expect(cell(yearGrid, '2026-03-04')).toHaveAttribute('data-state');
    cleanup();
    const monthGrid = renderMonthGrid();
    expect(cell(monthGrid, '2026-03-04')).not.toHaveAttribute('data-state');
  });
});

describe('the heaviest day is named, because the scale cannot show it', () => {
  // Rank bucketing puts a QUARTER of the spending days in the darkest stop, so
  // no amount of looking at the grid answers "which day was heaviest". This
  // line is the only thing that does — for sighted and screen-reader users
  // alike, which is why it is ordinary text and asserted as such.
  test('the phone names the displayed MONTH\u2019s heaviest day', () => {
    renderMonthGrid();
    // March's largest is 250 on the 20th. The YEAR's largest is 800 in July —
    // if the scope ever widens, this fails with a day that is not on screen.
    expect(screen.getByText(/Heaviest day:/)).toHaveTextContent(
      'Heaviest day: Mar 20 — $250.00',
    );
    expect(screen.queryByText(/\$800\.00/)).not.toBeInTheDocument();
  });

  test('switching month re-scopes it', async () => {
    const user = setup();
    renderMonthGrid();
    await user.click(screen.getByRole('button', { name: /^July 2026:/ }));
    expect(screen.getByText(/Heaviest day:/)).toHaveTextContent(
      'Heaviest day: Jul 18 — $800.00',
    );
  });

  test('the desktop names the YEAR\u2019s heaviest day', () => {
    // Deliberately a different scope from the phone's, because the desktop
    // grid shows a year. Both mean "the heaviest day in what you are looking
    // at"; forcing symmetry would name a day that is not on screen.
    renderYearGrid();
    expect(screen.getByText(/Heaviest day:/)).toHaveTextContent(
      'Heaviest day: Jul 18 — $800.00',
    );
  });

  test('a tie names the earliest day and says how many others tied', () => {
    const tied: HeatmapEntry[] = [
      { date: '2026-03-05', total: 500 },
      { date: '2026-03-11', total: 500 },
      { date: '2026-03-22', total: 500 },
    ];
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, tied);
    expect(screen.getByText(/Heaviest day:/)).toHaveTextContent(
      'Heaviest day: Mar 5 — $500.00 (tied with 2 other days)',
    );
  });

  test('a single tie is singular', () => {
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, [
      { date: '2026-03-05', total: 500 },
      { date: '2026-03-11', total: 500 },
    ]);
    expect(screen.getByText(/Heaviest day:/)).toHaveTextContent(
      '(tied with 1 other day)',
    );
  });

  test('a month with no spending renders no line rather than a zero', () => {
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, []);
    expect(screen.queryByText(/Heaviest day:/)).not.toBeInTheDocument();
    // Positive control: the grid DID render, so the absence above is about the
    // line and not about nothing having mounted.
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for March 2026' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('group', { name: 'Spending intensity legend' }),
    ).toBeInTheDocument();
  });
});

describe('a touch device never gets the year grid, however wide', () => {
  /** Galaxy Tab S10 FE in landscape: wide, and entirely touch-driven. */
  const TABLET_LANDSCAPE = 1152;

  test('a coarse pointer at 1152px gets the calendar', () => {
    // Rotating the household's tablet used to turn a 60px calendar into 18.4px
    // squares with no day numbers, whose only value affordance is a tooltip
    // that cannot open on touch at all.
    setViewportWidth(TABLET_LANDSCAPE);
    setPointer('coarse');
    renderHeatmap();
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for March 2026' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('grid', { name: 'Spending heatmap for 2026' }),
    ).not.toBeInTheDocument();
  });

  test('a fine pointer at the SAME width keeps the year grid', () => {
    // The other half, and the one that makes the pair meaningful: without it a
    // component that had simply stopped rendering the year grid at any width
    // would pass the case above.
    setViewportWidth(TABLET_LANDSCAPE);
    setPointer('fine');
    renderHeatmap();
    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for 2026',
    });
    expect(within(grid).getAllByRole('button').length).toBeGreaterThan(360);
    expect(
      screen.queryByRole('group', { name: 'Heatmap month' }),
    ).not.toBeInTheDocument();
  });

  test('width still gates: a fine pointer on a phone gets the calendar', () => {
    setViewportWidth(PHONE_WIDTH);
    setPointer('fine');
    renderHeatmap();
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for March 2026' }),
    ).toBeInTheDocument();
  });
});

describe('a day that arrives as several groups', () => {
  // `SumExpensesByDay` groups by the raw timestamp column, so one calendar day
  // holding rows at different times of day comes back as several entries that
  // the handler formats to the SAME "YYYY-MM-DD". Building the lookup with
  // `new Map(...)` was last-write-wins, and the three surfaces then disagreed
  // with each other.
  const SPLIT: HeatmapEntry[] = [
    { date: '2026-03-04', total: 30 },
    { date: '2026-03-04', total: 12.5 },
    { date: '2026-03-20', total: 1 },
  ];

  test('the cell announces the whole day, not the last group', () => {
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, SPLIT);
    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for March 2026',
    });
    expect(cell(grid, '2026-03-04')).toHaveAttribute(
      'aria-label',
      'Today, Wednesday, March 4th, 2026: $42.50 spent',
    );
  });

  test('the month chip and the day cells cannot disagree', () => {
    // `monthTotals` already accumulated, so before the fix the chip said
    // $42.50 while its own day cells summed to $12.50. Both are asserted here
    // because the defect was the DISAGREEMENT, not either number alone.
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, SPLIT);
    expect(
      screen.getByRole('button', { name: 'March 2026: $43.50 spent' }),
    ).toBeInTheDocument();
    expect(screen.getByText(/Heaviest day:/)).toHaveTextContent(
      'Heaviest day: Mar 4 — $42.50',
    );
  });

  test('the intensity scale ranks DAYS, not per-timestamp groups', () => {
    // 03-04 arrives as two groups of 5. Ranked over accumulated DAYS
    // ([10,20,30,40], n=4) it is the smallest of four and takes the palest
    // stop. Ranked over the raw entries ([5,5,20,30,40], n=5) its accumulated
    // 10 is absent from the rank map, falls back to n, and paints DARKEST —
    // the quietest day of the month rendered as its heaviest. The earlier
    // fixture in this block cannot see that: it yields the same two opacities
    // either way, which is why this case exists separately.
    const groups: HeatmapEntry[] = [
      { date: '2026-03-04', total: 5 },
      { date: '2026-03-04', total: 5 },
      { date: '2026-03-10', total: 20 },
      { date: '2026-03-20', total: 30 },
      { date: '2026-03-25', total: 40 },
    ];
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, groups);
    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for March 2026',
    });
    const chip = cell(grid, '2026-03-04').querySelector<HTMLElement>(
      'span[aria-hidden="true"]',
    )!;
    expect(chip.style.opacity).toBe(String(OPACITY_STOPS[0]));
  });

  test('the sheet header reports the whole day too', async () => {
    apiGet.mockResolvedValue(
      page([transaction({ id: 7, description: 'Butcher', amount: 30 })]),
    );
    const user = setup();
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, SPLIT);
    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for March 2026',
    });
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText('$42.50 spent \u00b7 Expenses only'),
    ).toBeInTheDocument();
  });
});

describe('changing the year', () => {
  test('resets the month instead of stranding the old one', async () => {
    // M7: deleting the reset left the whole suite green. It has to be a
    // RERENDER of the same instance — a fresh mount initialises `useState`
    // from the new year and passes either way, which is exactly how the first
    // version of this test failed to kill the mutant.
    const user = setup();
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    setViewportWidth(PHONE_WIDTH);
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <SpendingHeatmap data={DATA} year={2026} format={usd} />
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole('button', { name: /^July 2026:/ }));
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for July 2026' }),
    ).toBeInTheDocument();

    // The provider has to be re-supplied: `rerender` replaces the whole tree.
    rerender(
      <QueryClientProvider client={client}>
        <SpendingHeatmap data={[]} year={2024} format={usd} />
      </QueryClientProvider>,
    );
    // January, not the July left over from 2026.
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for January 2024' }),
    ).toBeInTheDocument();
  });
});

describe('a touch device never gets the year grid, however wide', () => {
  /** Galaxy Tab S10 FE in landscape: wide, and entirely touch-driven. */
  const TABLET_LANDSCAPE = 1152;

  test('a coarse pointer at 1152px gets the calendar', () => {
    // Rotating the household's tablet used to turn a 60px calendar into 18.4px
    // squares with no day numbers, whose only value affordance is a tooltip
    // that cannot open on touch at all.
    setViewportWidth(TABLET_LANDSCAPE);
    setPointer('coarse');
    renderHeatmap();
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for March 2026' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('grid', { name: 'Spending heatmap for 2026' }),
    ).not.toBeInTheDocument();
  });

  test('a fine pointer at the SAME width keeps the year grid', () => {
    // The other half, and the one that makes the pair meaningful: without it a
    // component that had simply stopped rendering the year grid at any width
    // would pass the case above.
    setViewportWidth(TABLET_LANDSCAPE);
    setPointer('fine');
    renderHeatmap();
    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for 2026',
    });
    expect(within(grid).getAllByRole('button').length).toBeGreaterThan(360);
    expect(
      screen.queryByRole('group', { name: 'Heatmap month' }),
    ).not.toBeInTheDocument();
  });

  test('width still gates: a fine pointer on a phone gets the calendar', () => {
    setViewportWidth(PHONE_WIDTH);
    setPointer('fine');
    renderHeatmap();
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for March 2026' }),
    ).toBeInTheDocument();
  });
});

describe('a day that arrives as several groups', () => {
  // `SumExpensesByDay` groups by the raw timestamp column, so one calendar day
  // holding rows at different times of day comes back as several entries that
  // the handler formats to the SAME "YYYY-MM-DD". Building the lookup with
  // `new Map(...)` was last-write-wins, and the three surfaces then disagreed
  // with each other.
  const SPLIT: HeatmapEntry[] = [
    { date: '2026-03-04', total: 30 },
    { date: '2026-03-04', total: 12.5 },
    { date: '2026-03-20', total: 1 },
  ];

  test('the cell announces the whole day, not the last group', () => {
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, SPLIT);
    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for March 2026',
    });
    expect(cell(grid, '2026-03-04')).toHaveAttribute(
      'aria-label',
      'Today, Wednesday, March 4th, 2026: $42.50 spent',
    );
  });

  test('the month chip and the day cells cannot disagree', () => {
    // `monthTotals` already accumulated, so before the fix the chip said
    // $42.50 while its own day cells summed to $12.50. Both are asserted here
    // because the defect was the DISAGREEMENT, not either number alone.
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, SPLIT);
    expect(
      screen.getByRole('button', { name: 'March 2026: $43.50 spent' }),
    ).toBeInTheDocument();
    expect(screen.getByText(/Heaviest day:/)).toHaveTextContent(
      'Heaviest day: Mar 4 — $42.50',
    );
  });

  test('the intensity scale ranks DAYS, not per-timestamp groups', () => {
    // 03-04 arrives as two groups of 5. Ranked over accumulated DAYS
    // ([10,20,30,40], n=4) it is the smallest of four and takes the palest
    // stop. Ranked over the raw entries ([5,5,20,30,40], n=5) its accumulated
    // 10 is absent from the rank map, falls back to n, and paints DARKEST —
    // the quietest day of the month rendered as its heaviest. The earlier
    // fixture in this block cannot see that: it yields the same two opacities
    // either way, which is why this case exists separately.
    const groups: HeatmapEntry[] = [
      { date: '2026-03-04', total: 5 },
      { date: '2026-03-04', total: 5 },
      { date: '2026-03-10', total: 20 },
      { date: '2026-03-20', total: 30 },
      { date: '2026-03-25', total: 40 },
    ];
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, groups);
    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for March 2026',
    });
    const chip = cell(grid, '2026-03-04').querySelector<HTMLElement>(
      'span[aria-hidden="true"]',
    )!;
    expect(chip.style.opacity).toBe(String(OPACITY_STOPS[0]));
  });

  test('the sheet header reports the whole day too', async () => {
    apiGet.mockResolvedValue(
      page([transaction({ id: 7, description: 'Butcher', amount: 30 })]),
    );
    const user = setup();
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(YEAR, SPLIT);
    const grid = screen.getByRole('grid', {
      name: 'Spending heatmap for March 2026',
    });
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText('$42.50 spent \u00b7 Expenses only'),
    ).toBeInTheDocument();
  });
});

describe('changing the year', () => {
  test('resets the month instead of stranding the old one', async () => {
    // M7: deleting the reset left the whole suite green. Landing on December
    // 2019 because that is where you happened to be in 2026 is not a sensible
    // default, and the reset is a render-time state adjustment that nothing
    // else exercises.
    const user = setup();
    renderMonthGrid();
    await user.click(screen.getByRole('button', { name: /^July 2026:/ }));
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for July 2026' }),
    ).toBeInTheDocument();

    cleanup();
    setViewportWidth(PHONE_WIDTH);
    renderHeatmap(2024, []);
    // A past year opens on January, not on the month left over from 2026.
    expect(
      screen.getByRole('grid', { name: 'Spending heatmap for January 2024' }),
    ).toBeInTheDocument();
  });
});

describe('the desktop tooltip says what the cell says', () => {
  test('it reuses the cell label instead of printing a raw ISO string', async () => {
    // M10: it used to render `2026-03-02: $10.00` beside an `aria-label`
    // reading "Monday, March 2nd, 2026: $10.00 spent" — the same facts told
    // two ways, and only one of them could say "no spending" or "upcoming".
    //
    // The tooltip has to be OPENED to assert it. An earlier version of this
    // test read `trigger.textContent`, which is the button — Radix keeps the
    // content unmounted until open, so it asserted nothing and the mutant
    // that restored the raw ISO string survived it.
    const grid = renderYearGrid();
    const trigger = cell(grid, '2026-03-02');
    expect(trigger).toHaveAttribute(
      'aria-label',
      'Monday, March 2nd, 2026: $10.00 spent',
    );
    focusCell(trigger);
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('Monday, March 2nd, 2026: $10.00 spent');
    expect(tip.textContent).not.toContain('2026-03-02');
  });
});

describe('the sheet announces its own loading', () => {
  test('a status region reports loading, then the count', async () => {
    // M8: the skeleton is aria-hidden decoration and there was no live region,
    // so between opening the sheet and the rows landing a screen-reader user
    // was told nothing at all.
    // A DEFERRED promise, not `mockResolvedValue`: with an already-resolved
    // mock the query settles before `findByRole('dialog')` returns, and the
    // in-flight state is never observable at all — the first version of this
    // test asserted "Loading expenses" and read "2 expenses".
    let resolveRows: (value: PaginatedResponse<Transaction>) => void = () => {};
    apiGet.mockReturnValue(
      new Promise<PaginatedResponse<Transaction>>((resolve) => {
        resolveRows = resolve;
      }),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');

    // Both states, in order. Asserting only the settled one would pass against
    // a component that announced nothing while the request was in flight.
    expect(within(dialog).getByRole('status')).toHaveTextContent(
      'Loading expenses',
    );
    expect(
      within(dialog).getByRole('status').closest('[aria-busy]'),
    ).toHaveAttribute('aria-busy', 'true');

    await act(async () => {
      resolveRows(
        page([
          transaction({ id: 7, description: 'Butcher', amount: 30 }),
          transaction({ id: 8, description: 'Bakery', amount: 5 }),
        ]),
      );
    });

    expect(await within(dialog).findByText('Butcher')).toBeInTheDocument();
    expect(within(dialog).getByRole('status').textContent).toBe('2 expenses');
    expect(
      within(dialog).getByRole('status').closest('[aria-busy]'),
    ).toHaveAttribute('aria-busy', 'false');
  });

  test('one row is singular', async () => {
    apiGet.mockResolvedValue(
      page([transaction({ id: 7, description: 'Butcher', amount: 30 })]),
    );
    const user = setup();
    const grid = renderMonthGrid();
    await user.click(cell(grid, '2026-03-04'));
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByText('Butcher');
    // ANCHORED. `toHaveTextContent('1 expense')` is a substring match, and
    // "1 expenses" contains it — the plural-dropping mutant survived this
    // assertion until it was anchored.
    expect(within(dialog).getByRole('status').textContent).toBe('1 expense');
  });
});
