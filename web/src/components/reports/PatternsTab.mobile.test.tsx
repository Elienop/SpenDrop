import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { RecurringEntry } from '@/api/types';

/**
 * Phone-width behaviour of the Reports → Patterns tab's Recurring Expenses
 * card.
 *
 * In its own file rather than added to `PatternsTab.test.tsx` because the
 * viewport is per-file state in happy-dom: a `setViewport` leaking into the
 * tests next door would move their heatmap assertions onto the other branch.
 *
 * WHAT THIS FILE CANNOT PROVE. happy-dom performs no layout and never loads
 * the Tailwind stylesheet, so every width here would be zero. The 102px
 * horizontal pan this card list exists to remove was measured in Chrome
 * against the built container at a 360px viewport, and the width budget is
 * recorded at the call site in `PatternsTab.tsx`. What is asserted below is
 * which tree mounts, that the card carries every datum and action the row
 * does, and that the class tokens the browser measurement depended on are
 * still present. Do not upgrade any assertion here into a claim about pixels.
 */

const refetchRecurring = vi.fn();
const dismissRecurring = vi.fn<(year: number, description: string) => Promise<void>>();

/**
 * Two entries, because almost everything here is about telling one card apart
 * from another — a handler closed over the wrong entry, or a dismiss button
 * whose accessible name does not say which row it dismisses, both survive a
 * one-row fixture.
 *
 * The second description is a 500-character unbroken token: import bypasses
 * the per-row description limit the PATCH route enforces, so a spreadsheet
 * cell really can put one of these in the ledger, and it is the shape that
 * pans a phone sideways.
 */
const LONG_DESCRIPTION = `Netflix${'x'.repeat(493)}`;

/**
 * What that description is allowed to become inside an accessible name: 60
 * characters, the last of them an ellipsis.
 *
 * Built from the fixture and the cap, NOT by calling the component's own
 * shortening helper — a test that recomputes its expectation with the
 * expression under test only proves that expression is deterministic. The
 * length assertion below is what keeps the hand arithmetic honest.
 */
const TRUNCATED_LABEL = `Dismiss Netflix${'x'.repeat(52)}…`;

let recurring: {
  data: RecurringEntry[];
  loading: boolean;
  fetching: boolean;
  error: string;
} = { data: [], loading: false, fetching: false, error: '' };

const ENTRIES: RecurringEntry[] = [
  {
    description: 'Spotify Family',
    monthly_avg: 17.99,
    month_count: 12,
    annual_total: 215.88,
  },
  {
    description: LONG_DESCRIPTION,
    monthly_avg: 1234.5,
    month_count: 9,
    annual_total: 11110.5,
  },
];

const idle = <T,>(data: T) => ({
  data,
  loading: false,
  fetching: false,
  error: '',
});

vi.mock('@/hooks/useReports', () => ({
  useSpendingHeatmap: () => idle([]),
  useTagBreakdown: () => idle([]),
  useRecurring: () => ({ ...recurring, refetch: refetchRecurring }),
  dismissRecurring: (year: number, description: string) =>
    dismissRecurring(year, description),
}));

vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => ({
    years: [2026],
    currentYear: 2026,
    hasTransactions: true,
    outOfRangeYears: [] as number[],
    futureYears: [] as number[],
    loading: false,
  }),
}));

vi.mock('@/hooks/useBaseCurrency', () => ({ useBaseCurrency: () => 'USD' }));

const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastError(...args),
    success: (...args: unknown[]) => toastSuccess(...args),
  },
}));

import { PatternsTab } from './PatternsTab';

interface HappyDomWindow {
  happyDOM?: { setViewport: (v: { width?: number; height?: number }) => void };
}

/** Galaxy S24 — the narrowest device this household actually uses. */
const PHONE_WIDTH = 360;
/** happy-dom's default, and what every other test file in this repo runs at. */
const DESKTOP_WIDTH = 1024;
/** Galaxy Tab S10 FE portrait: below `md`, so it takes the phone branch. */
const TABLET_PORTRAIT = 720;

/** The year both Selects open on, and so the year a dismiss is posted for. */
const CURRENT_YEAR = new Date().getFullYear();

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
 * Render at phone width, having first proved the presentation actually swapped.
 *
 * THE POSITIVE CONTROL, and it is not decoration. Nearly every assertion below
 * is equally true of the table — the descriptions render either way, the money
 * strings are identical, the dismiss button is the same component. So if
 * `setViewportWidth` ever stops taking (a renamed happy-dom API, a different
 * environment, a deleted `beforeEach`), the hook answers `false`, the DESKTOP
 * tree mounts, and most of this file goes green while asserting the wrong
 * presentation. Both halves are checked — the list is present AND the table is
 * gone — because either alone is satisfied by a dual-render, which is the exact
 * thing this fork exists to avoid.
 */
function renderPhone(width: number = PHONE_WIDTH): HTMLElement {
  setViewportWidth(width);
  render(<PatternsTab />);
  const list = screen.getByRole('list', { name: 'Recurring expenses' });
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  return list;
}

/** The desktop half of the same control. */
function renderDesktop(): HTMLElement {
  setViewportWidth(DESKTOP_WIDTH);
  render(<PatternsTab />);
  const table = screen.getByRole('table');
  expect(
    screen.queryByRole('list', { name: 'Recurring expenses' }),
  ).not.toBeInTheDocument();
  return table;
}

const classes = (el: Element) => el.className.split(/\s+/);

/** The card for one entry, found through the text the user reads. */
function cardFor(description: string): HTMLElement {
  const card = screen.getByText(description).closest('li');
  expect(card).not.toBeNull();
  return card as HTMLElement;
}

function setup() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

beforeEach(() => {
  vi.clearAllMocks();
  // `mockReset`, not `clearAllMocks` alone: clearing drains the call log but
  // NOT the queued `*Once` implementations, so a `mockRejectedValueOnce` that
  // its own test never got as far as consuming stays queued and fires in some
  // later test instead. Observed here while mutation-testing — with the
  // presentation fork disabled, the toast test failed before its click and its
  // leftover rejection then failed the desktop dismiss test three cases later,
  // which reads as an unrelated second bug.
  dismissRecurring.mockReset();
  dismissRecurring.mockResolvedValue(undefined);
  recurring = { data: ENTRIES, loading: false, fetching: false, error: '' };
});

afterEach(() => {
  cleanup();
  setViewportWidth(DESKTOP_WIDTH);
});

describe('which presentation mounts', () => {
  test('below md the table is replaced by a card list', () => {
    const list = renderPhone();
    // …and no orphan row survives the swap either. The heatmap beside it is a
    // `role="grid"`, so `row` here can only come from the recurring table.
    expect(within(list).queryAllByRole('row')).toHaveLength(0);
    expect(within(list).getAllByRole('listitem')).toHaveLength(ENTRIES.length);
    // The ATTRIBUTE, not the role — `getByRole('list')` resolves the implicit
    // role of a `<ul>` and would pass with `role="list"` deleted. It is not
    // redundant markup: Tailwind's preflight sets `list-style: none`, and
    // Safari/VoiceOver drop the list role along with the marker.
    expect(list.getAttribute('role')).toBe('list');
  });

  test('at md and above the table is kept', () => {
    const table = renderDesktop();
    // 1 header row + one per entry.
    expect(within(table).getAllByRole('row')).toHaveLength(ENTRIES.length + 1);
  });

  test('a 720px tablet in portrait is below md and gets the cards', () => {
    // The gate is WIDTH, not pointer capability. The household's Tab S10 FE in
    // portrait is the case that separates this fork from the heatmap's beside
    // it — the heatmap swaps on pointer, so its ~1130px landscape still gets
    // the calendar while this table comes back, and that is intended.
    const list = renderPhone(TABLET_PORTRAIT);
    expect(within(list).getAllByRole('listitem')).toHaveLength(ENTRIES.length);
  });

  test('an empty result renders neither presentation, at either width', () => {
    // The states above the fork are shared, so this is the check that the fork
    // was inserted INSIDE the has-data branch rather than around it — a list
    // that renders for zero entries would put an empty box under a heading
    // that already says "No recurring expenses detected yet".
    recurring = { data: [], loading: false, fetching: false, error: '' };
    setViewportWidth(PHONE_WIDTH);
    render(<PatternsTab />);
    expect(
      screen.queryByRole('list', { name: 'Recurring expenses' }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(
      screen.getByText('No recurring expenses detected yet'),
    ).toBeInTheDocument();
  });
});

describe('the card carries what the row carried', () => {
  test('every figure on the row is on the card', () => {
    // Scoped to the card, not the page: both entries render money, and a
    // page-wide `getByText` would pass on the other card's numbers.
    renderPhone();
    const card = cardFor('Spotify Family');
    expect(within(card).getByText('$17.99')).toBeInTheDocument();
    expect(within(card).getByText('12/12 months')).toBeInTheDocument();
    expect(within(card).getByText('$215.88')).toBeInTheDocument();

    const other = cardFor(LONG_DESCRIPTION);
    expect(within(other).getByText('$1,234.50')).toBeInTheDocument();
    expect(within(other).getByText('9/12 months')).toBeInTheDocument();
    expect(within(other).getByText('$11,110.50')).toBeInTheDocument();
  });

  test('each figure is labelled with the table\'s own column heading', () => {
    // A table row is labelled by its column headers; a card is not. Without
    // this the phone reads out three bare numbers, and every assertion above
    // still passes.
    //
    // Compared against the DESKTOP headings rather than hard-coded — but be
    // honest about what that buys: both sides now read one constant, so a
    // rename cannot desynchronise them and this test cannot catch one. What it
    // catches is the card losing its labels, or listing them in an order that
    // no longer matches the columns.
    const card = (renderPhone(), cardFor('Spotify Family'));
    const terms = Array.from(card.querySelectorAll('dt')).map(
      (dt) => dt.textContent,
    );
    expect(terms).toHaveLength(3);
    cleanup();

    const table = renderDesktop();
    const headings = within(table)
      .getAllByRole('columnheader')
      .map((th) => th.textContent)
      // Description is the card's own heading line, and the last column is the
      // unlabelled actions gutter.
      .filter((text) => text !== 'Description' && text !== '');
    expect(terms).toEqual(headings);
  });

  test('a value sits next to the label it belongs to, not just somewhere on the card', () => {
    // The `<dl>` is a grid, so label and value are siblings rather than nested
    // — three `<dt>`s followed by three `<dd>`s in the wrong order would still
    // satisfy both tests above. Read pairwise instead.
    renderPhone();
    const list = cardFor('Spotify Family').querySelector('dl');
    expect(list).not.toBeNull();
    const pairs = Array.from(list!.children).map((el) => el.textContent);
    expect(pairs).toEqual([
      'Monthly Avg',
      '$17.99',
      'Frequency',
      '12/12 months',
      'Annual Total',
      '$215.88',
    ]);
  });

  test('the money keeps the base type size; only the labels are small', () => {
    // The figures were rendered at the `<dl>`'s inherited 12px, two steps below
    // the 14px the desktop table gives them and below every other
    // money-bearing card in the app — the annual total is the number the
    // dismiss decision rests on, and at 12px under a 12px label it read as
    // metadata about the description. Exact tokens, not a substring: `text-sm`
    // is a prefix of nothing here but `toContain` on a class STRING would have
    // matched `text-sm` inside another utility.
    renderPhone();
    const card = cardFor('Spotify Family');
    const [monthly, frequency, annual] = Array.from(
      card.querySelectorAll('dd'),
    );
    expect(classes(monthly)).toContain('text-sm');
    expect(classes(annual)).toContain('text-sm');
    // Frequency is deliberately NOT promoted — it qualifies the two figures
    // rather than competing with them. Asserted so the promotion cannot be
    // silently widened to "everything in the dl".
    expect(classes(frequency)).not.toContain('text-sm');
    // And the labels stay at the small size the container sets.
    for (const dt of card.querySelectorAll('dt')) {
      expect(classes(dt)).toContain('text-muted-foreground');
      expect(classes(dt)).not.toContain('text-sm');
    }
  });

  test('the figure list takes the app-wide card-dl spacing', () => {
    // Converged register across the card `<dl>`s in this app. Pinned as an
    // exact token because the value it replaced (`gap-y-0.5`) also starts with
    // `gap-y-`, so any prefix or substring check would accept the old one.
    renderPhone();
    const list = cardFor('Spotify Family').querySelector('dl')!;
    expect(classes(list)).toContain('gap-y-1');
    expect(classes(list)).not.toContain('gap-y-0.5');
    // Mixed type sizes in one row need a shared baseline, or the 12px label
    // hangs off the top of the 14px figure beside it.
    expect(classes(list)).toContain('items-baseline');
  });

  test('a 500-character description is bounded in both directions', () => {
    // The PAIRING, not either half. `overflow-wrap:anywhere` bounds the WIDTH
    // (Tailwind's `break-words` breaks the token for painting but leaves the
    // element's min-content contribution at its full width, so the column keeps
    // sizing to it); `line-clamp-3` bounds the HEIGHT that wrapping alone
    // trades it for. The pixel effect is the browser's job — see this file's
    // header.
    renderPhone();
    const tokens = classes(screen.getByText(LONG_DESCRIPTION));
    expect(tokens).toContain('[overflow-wrap:anywhere]');
    expect(tokens).toContain('line-clamp-3');
  });

  test('the description column can actually shrink', () => {
    // The load-bearing half, and the one that is invisible when missing: a flex
    // item's automatic minimum is `min-content`, so without `min-w-0` the
    // column refuses to shrink however the text inside it is allowed to wrap,
    // and the clamp above bounds nothing.
    renderPhone();
    const column = screen.getByText(LONG_DESCRIPTION).parentElement!;
    expect(classes(column)).toContain('min-w-0');
  });

  test('every card name is bounded, not just the long one', () => {
    // Positive control against a fixture-shaped fix: the bound belongs to the
    // component, not to the one entry this file made long.
    renderPhone();
    for (const entry of ENTRIES) {
      const tokens = classes(screen.getByText(entry.description));
      expect(tokens).toContain('[overflow-wrap:anywhere]');
      expect(tokens).toContain('line-clamp-3');
    }
  });
});

describe('dismiss works from a card', () => {
  test('it posts the year and the FULL description of the card it was tapped on', async () => {
    // Named per card, so this also proves the handler is bound to the right
    // entry — a shared closure over the last one would still be tappable.
    //
    // The button's NAME is capped but the payload must not be: the server
    // dismisses by exact description string, so a truncated one would either
    // 404 or, worse, match nothing and silently no-op.
    const user = setup();
    renderPhone();
    await user.click(screen.getByRole('button', { name: TRUNCATED_LABEL }));

    await waitFor(() => {
      expect(dismissRecurring).toHaveBeenCalledWith(
        CURRENT_YEAR,
        LONG_DESCRIPTION,
      );
    });
    // The list is server-derived, so the dismissal only shows once the query
    // re-runs. Without this the entry stays on screen and the tap reads as
    // having done nothing.
    await waitFor(() => {
      expect(refetchRecurring).toHaveBeenCalled();
    });
  });

  test('success is announced, not left to the row silently vanishing', async () => {
    // The only feedback there is. Without it a successful dismiss and a tap
    // that missed look identical until the refetch lands, and a screen reader
    // is told nothing at all — the focus move that follows is silent.
    const user = setup();
    renderPhone();
    await user.click(
      screen.getByRole('button', { name: 'Dismiss Spotify Family' }),
    );

    await waitFor(() => {
      expect(toastSuccess).toHaveBeenCalledWith('Recurring expense dismissed');
    });
  });

  test('a failure reaches a toast and does not refetch', async () => {
    const user = setup();
    dismissRecurring.mockRejectedValueOnce(new Error('network'));
    renderPhone();
    await user.click(
      screen.getByRole('button', { name: 'Dismiss Spotify Family' }),
    );

    await waitFor(() => {
      expect(toastError).toHaveBeenCalledWith(
        'Failed to dismiss recurring expense',
      );
    });
    expect(refetchRecurring).not.toHaveBeenCalled();
    // Neither of the success behaviours may fire on the failure path: a
    // "dismissed" toast over a row that is still there is worse than silence,
    // and moving focus off a button the user is about to press again is a
    // gratuitous jump.
    expect(toastSuccess).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Dismiss Spotify Family' }),
    );
    // The entry is still there to try again on.
    expect(cardFor('Spotify Family')).toBeInTheDocument();
  });

  test('both presentations name the button after the entry it dismisses', () => {
    // Every row used to carry the same "Dismiss recurring expense", so a screen
    // reader or voice-control user was offered N identical buttons. Asserted on
    // BOTH surfaces: the phone card is new markup and could have been the only
    // one fixed.
    renderPhone();
    const phone = screen
      .getAllByRole('button', { name: /^Dismiss / })
      .map((b) => b.getAttribute('aria-label'));
    expect(phone).toEqual(['Dismiss Spotify Family', TRUNCATED_LABEL]);
    cleanup();

    renderDesktop();
    expect(
      screen
        .getAllByRole('button', { name: /^Dismiss / })
        .map((b) => b.getAttribute('aria-label')),
    ).toEqual(phone);
  });

  test('the name is capped, and short descriptions are left alone', () => {
    // A 500-character accessible name is read out in full before the user
    // learns what the button does. Both halves: the cap alone could have been a
    // blanket truncation that mangles "Spotify Family" too, and the untouched
    // short name alone proves nothing about the long one.
    renderPhone();
    const label = screen
      .getByRole('button', { name: TRUNCATED_LABEL })
      .getAttribute('aria-label')!;
    // The arithmetic behind TRUNCATED_LABEL, stated where it can fail: 60
    // characters of description, the last an ellipsis, after the verb.
    expect(label.replace('Dismiss ', '')).toHaveLength(60);
    expect(label.endsWith('…')).toBe(true);
    expect(label.length).toBeLessThan(LONG_DESCRIPTION.length);

    expect(
      screen
        .getByRole('button', { name: 'Dismiss Spotify Family' })
        .getAttribute('aria-label'),
    ).toBe('Dismiss Spotify Family');
  });

  test('the desktop table dismisses through the same handler', async () => {
    // The fork must not have left two implementations that can drift into
    // posting different years or forgetting the refetch.
    const user = setup();
    renderDesktop();
    await user.click(
      screen.getByRole('button', { name: 'Dismiss Spotify Family' }),
    );

    await waitFor(() => {
      expect(dismissRecurring).toHaveBeenCalledWith(
        CURRENT_YEAR,
        'Spotify Family',
      );
    });
    await waitFor(() => {
      expect(refetchRecurring).toHaveBeenCalled();
    });
  });
});

describe('focus survives the row it was standing on', () => {
  /*
    The dismiss button lives inside the row the dismiss removes, so once the
    refetch lands the focused element unmounts and focus falls to <body> — no
    context, and a keyboard user restarts from the top of the page. The section
    title is the anchor because it is the one node that survives every outcome,
    including dismissing the LAST entry, which unmounts the list itself.
  */

  test('focus moves to the section title, on the card surface', async () => {
    const user = setup();
    renderPhone();
    const button = screen.getByRole('button', {
      name: 'Dismiss Spotify Family',
    });
    await user.click(button);

    const title = screen.getByText('Recurring Expenses');
    await waitFor(() => {
      expect(document.activeElement).toBe(title);
    });
    // Spelled out because <body> is where this lands when the handoff is
    // missing, and `not.toBe(button)` alone would accept it.
    expect(document.activeElement).not.toBe(document.body);
  });

  test('and on the desktop table, through the same handler', async () => {
    const user = setup();
    renderDesktop();
    await user.click(
      screen.getByRole('button', { name: 'Dismiss Spotify Family' }),
    );

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByText('Recurring Expenses'),
      );
    });
  });

  test('the destination is what names the section, so landing there announces it', () => {
    // Focusing a bare <div> announces its text and nothing else. This one is
    // the region's `aria-labelledby` target, which is what makes the landing
    // say "Recurring Expenses" rather than leaving the user somewhere unnamed.
    // Pinned because the tie is invisible: someone could move the id to a
    // sibling and every focus assertion above would still pass.
    renderPhone();
    const title = screen.getByText('Recurring Expenses');
    const region = screen.getByRole('region', { name: 'Recurring Expenses' });
    expect(region.getAttribute('aria-labelledby')).toBe(title.id);
    // Reachable programmatically, but never in the tab order — it is a
    // destination, not a control.
    expect(title.getAttribute('tabindex')).toBe('-1');
  });

  test('focus moves BEFORE the refetch, not after', async () => {
    // The ordering is the whole fix. Read the active element at the moment
    // `refetch` is called: if the focus move were sequenced after it, the row
    // would already be on its way out and focus would still be on the doomed
    // button. Asserting only the end state cannot tell the two apart here,
    // because this test's refetch is an inert mock that never drops the row.
    const user = setup();
    let focusWhenRefetched: Element | null = null;
    refetchRecurring.mockImplementation(() => {
      focusWhenRefetched = document.activeElement;
    });
    renderPhone();
    const button = screen.getByRole('button', {
      name: 'Dismiss Spotify Family',
    });
    await user.click(button);

    await waitFor(() => {
      expect(refetchRecurring).toHaveBeenCalled();
    });
    expect(focusWhenRefetched).toBe(screen.getByText('Recurring Expenses'));
    expect(focusWhenRefetched).not.toBe(button);
  });
});

describe('the control is thumb-sized', () => {
  test('the card trigger takes the 44px floor on both axes', () => {
    // Square target, so both axes: floored in height alone it is a 44x40
    // rectangle — taller, still a miss horizontally. Unconditional rather than
    // `coarse:` because this subtree only exists below `md`, where the
    // tablet-vs-phone question the pointer gate answers cannot arise, and it
    // agrees with the primitive's own coarse floor on the value.
    renderPhone();
    const tokens = classes(
      screen.getByRole('button', { name: 'Dismiss Spotify Family' }),
    );
    expect(tokens).toContain('min-h-11');
    expect(tokens).toContain('min-w-11');
    // Not the width-gated form, which switches OFF above 768px and would hand
    // the household's ~1130px touch tablet a 40px target back.
    expect(tokens).not.toContain('md:min-h-0');
  });

  test('the desktop trigger keeps the icon density', () => {
    // The other half of the same control. Without it, applying the phone
    // classes at every width would pass the test above.
    renderDesktop();
    const tokens = classes(
      screen.getByRole('button', { name: 'Dismiss Spotify Family' }),
    );
    expect(tokens).toContain('h-10');
    expect(tokens).toContain('w-10');
    expect(tokens).not.toContain('min-h-11');
    // …and it still floors itself for a pointer that needs it, from the
    // primitive rather than from this call site.
    expect(tokens).toContain('coarse:min-h-11');
  });
});
