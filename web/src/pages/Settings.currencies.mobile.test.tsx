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
import type { Currency } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

/**
 * The Currencies table's phone presentation: a card list, one editable rate
 * field per card.
 *
 * The table has five columns — Code, Name, Symbol, Rate to Base, Status — and
 * at 360px it panned 129px sideways with 93% of the rate input clipped. The
 * rate is the only editable thing on the surface, so the pan was hiding the
 * point of it.
 *
 * ONE TREE OR THE OTHER, chosen in JS. Every viewport-gated assertion below
 * therefore carries its opposite-width control: a gated block that never
 * renders makes every assertion about it vacuously true, and five tests in
 * this codebase were once green exactly that way. The controls here assert
 * PRESENCE on one side and ABSENCE on the other, because both presentations
 * render the same currencies under the same card heading and a one-sided
 * assertion cannot tell them apart.
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

const usd: Currency = {
  code: 'USD',
  name: 'US Dollar',
  symbol: '$',
  rate_to_base: 1,
  is_base: true,
  updated_at: '2026-01-01T00:00:00Z',
};

const lbp: Currency = {
  code: 'LBP',
  name: 'Lebanese Pound',
  symbol: 'L£',
  rate_to_base: 90000,
  is_base: false,
  updated_at: '2026-01-01T00:00:00Z',
};

function withQueryClient({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function renderSettings() {
  return render(
    <MemoryRouter initialEntries={['/settings?tab=currencies']}>
      <Settings />
    </MemoryRouter>,
    { wrapper: withQueryClient },
  );
}

/** Render at phone width, having first proved the swap actually took. */
async function renderPhone() {
  setViewportWidth(PHONE_WIDTH);
  renderSettings();
  const list = await screen.findByRole('list', { name: 'Currencies' });
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  return list;
}

/** The desktop half of the same control. */
async function renderDesktop() {
  setViewportWidth(DESKTOP_WIDTH);
  renderSettings();
  const table = await screen.findByRole('table');
  expect(
    screen.queryByRole('list', { name: 'Currencies' }),
  ).not.toBeInTheDocument();
  return table;
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockedUseAuth.mockReturnValue({
    user: { id: 1, username: 'elie', display_name: 'Elie', role: 'admin' },
  } as unknown as ReturnType<typeof useAuth>);
  mockedApi.get.mockImplementation((path: string) => {
    if (path === 'currencies') return Promise.resolve([usd, lbp]);
    if (path === 'api-tokens') return Promise.resolve({ tokens: [] });
    return Promise.resolve([]);
  });
  mockedApi.put.mockResolvedValue({});
});

afterEach(() => {
  setViewportWidth(DESKTOP_WIDTH);
});

describe('which presentation mounts', () => {
  test('below md the currency table is replaced by a card list', async () => {
    const list = await renderPhone();
    // The list is populated, not merely present — an empty `<ul>` would
    // satisfy `findByRole('list')` while showing the user nothing.
    expect(within(list).getAllByRole('listitem')).toHaveLength(2);
    // …and no orphan row survived the swap.
    expect(screen.queryAllByRole('row')).toHaveLength(0);
  });

  test('at md and above the table is kept, untouched', async () => {
    const table = await renderDesktop();
    // Header row plus one per currency.
    expect(within(table).getAllByRole('row')).toHaveLength(3);
    expect(
      within(table).getByRole('spinbutton', { name: 'Rate for LBP' }),
    ).toBeInTheDocument();
  });
});

describe('what a currency card carries', () => {
  test('every datum the table row shows survives the fold', async () => {
    const list = await renderPhone();
    const [base, foreign] = within(list).getAllByRole('listitem');

    // `selector: 'div'` picks the card's identity line rather than the code
    // repeated inside the rate field's label ("Rate for LBP"), which renders
    // in a `<span>`. Both are meant to be there — the label has to name which
    // currency it edits — so the query has to say which one it means.
    // Base row: code, name, symbol, the fixed rate, and the Base badge.
    expect(within(base).getByText('USD', { selector: 'div' })).toBeInTheDocument();
    expect(within(base).getByText(/US Dollar/)).toBeInTheDocument();
    expect(within(base).getByText(/\$/)).toBeInTheDocument();
    expect(within(base).getByText('1.0000')).toBeInTheDocument();
    expect(within(base).getByText('Base')).toBeInTheDocument();
    // The base currency has no editable rate on either presentation.
    expect(within(base).queryByRole('spinbutton')).not.toBeInTheDocument();

    // Foreign row: the editable rate, seeded from the server value.
    expect(
      within(foreign).getByText('LBP', { selector: 'div' }),
    ).toBeInTheDocument();
    expect(within(foreign).getByText(/Lebanese Pound/)).toBeInTheDocument();
    expect(within(foreign).queryByText('Base')).not.toBeInTheDocument();
    const rate = within(foreign).getByRole('spinbutton', {
      name: 'Rate for LBP',
    });
    expect((rate as HTMLInputElement).value).toBe('90000');
  });

  /**
   * The card's field is named with a real `<Label>`, and the string is the one
   * the table's `aria-label` uses. Two presentations that name the same datum
   * differently would make every query surface-specific, and an `aria-label`
   * sitting beside visible label text overrides it — orphaning what is on
   * screen from what a screen reader announces.
   */
  test('the rate field is labelled by visible text, not an aria-label', async () => {
    const list = await renderPhone();
    const rate = within(list).getByRole('spinbutton', {
      name: 'Rate for LBP',
    });
    expect(rate).not.toHaveAttribute('aria-label');
    const label = document.querySelector(`label[for="${rate.id}"]`);
    expect(label).not.toBeNull();
    expect(label?.textContent).toBe('Rate for LBP');
  });
});

describe('the card list edits the same form the table did', () => {
  test('a rate typed on a card is what Save Rates sends', async () => {
    const user = userEvent.setup();
    const list = await renderPhone();
    const rate = within(list).getByRole('spinbutton', {
      name: 'Rate for LBP',
    });
    await user.clear(rate);
    await user.type(rate, '89500');
    await user.click(screen.getByRole('button', { name: /save rates/i }));
    await waitFor(() => {
      expect(mockedApi.put).toHaveBeenCalledWith('currencies/LBP', {
        name: 'Lebanese Pound',
        symbol: 'L£',
        rate_to_base: 89500,
        is_base: false,
      });
    });
    // The base currency is skipped on this path, same as on the table.
    expect(mockedApi.put).toHaveBeenCalledTimes(1);
  });
});
