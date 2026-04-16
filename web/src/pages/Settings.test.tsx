import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

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
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { Settings } from './Settings';
import type { Category, ImportPreview, ImportResult } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
// The `sonner` module mock above is still required — Settings.tsx itself
// imports `toast` and calls it in budget/currency/goals/users flows. The
// old import-failure tests used to assert on `mockedToast.error`; 3.4b
// routed import errors through `importSession.error` → Alert instead, so
// the test-side binding is dead but the module mock is still live.

function renderSettings() {
  return render(
    <MemoryRouter>
      <Settings />
    </MemoryRouter>,
  );
}

describe('Settings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // useImportSession persists the import_id in localStorage across a
    // successful upload + 60-min resume window. Tests that upload must
    // not leak that key into the next test's mount effect (which would
    // fire a GET /api/import/{id} and race the test's own wiring). Clear
    // explicitly rather than rely on happy-dom teardown.
    localStorage.clear();
    mockedApi.get.mockImplementation((path: string) => {
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
          {
            code: 'EUR',
            name: 'Euro',
            symbol: '\u20AC',
            rate_to_base: 0.92,
            is_base: false,
            updated_at: '',
          },
        ]);
      if (path === 'savings-goals')
        return Promise.resolve([
          { id: 1, year: 2026, target_amount: 6000, updated_at: '' },
        ]);
      if (path === 'users')
        return Promise.resolve([
          {
            id: 1,
            username: 'alice',
            display_name: 'Alice',
            role: 'admin',
            created_at: '2024-01-01',
          },
        ]);
      return Promise.resolve([]);
    });
  });

  describe('as admin', () => {
    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: {
          id: 1,
          username: 'alice',
          display_name: 'Alice',
          role: 'admin',
          created_at: '2024-01-01',
        },
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('renders Settings heading', () => {
      renderSettings();
      expect(
        screen.getByRole('heading', { level: 1, name: /settings/i }),
      ).toBeInTheDocument();
    });

    test('renders tab navigation', () => {
      renderSettings();
      expect(screen.getByRole('tab', { name: /general/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /currencies/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /savings/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /users/i })).toBeInTheDocument();
    });

    test('shows general settings by default', async () => {
      renderSettings();
      await waitFor(() => {
        expect(screen.getByText(/monthly budgets/i)).toBeInTheDocument();
      });
    });

    test('switches to currencies tab', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /currencies/i }));

      await waitFor(() => {
        expect(screen.getByText('USD')).toBeInTheDocument();
        expect(screen.getByText('EUR')).toBeInTheDocument();
      });
    });

    test('switches to savings tab', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /savings/i }));

      await waitFor(() => {
        expect(screen.getByText(/savings goals/i)).toBeInTheDocument();
      });
    });

    test('shows users tab for admin', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /users/i }));

      await waitFor(() => {
        expect(screen.getByText('alice')).toBeInTheDocument();
      });
    });

    test('renders Import / Export tab', () => {
      renderSettings();
      expect(
        screen.getByRole('tab', { name: /import \/ export/i }),
      ).toBeInTheDocument();
    });

    test('switches to data tab and shows export section', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^export$/i })).toBeInTheDocument();
      });
    });

    test('data tab has year input, mode buttons, and export button', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^monthly$/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^yearly$/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^export$/i })).toBeInTheDocument();
      });
    });

    test('export monthly opens correct URL', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
      });

      // Set year
      const yearInput = screen.getByLabelText(/year/i);
      await user.clear(yearInput);
      await user.type(yearInput, '2026');

      // Monthly is the default mode, click Export
      await user.click(screen.getByRole('button', { name: /^export$/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = openSpy.mock.calls[0][0] as string;
      expect(url).toMatch(/\/api\/export\/monthly\/2026\/\d+/);
      expect(openSpy.mock.calls[0][1]).toBe('_blank');
      openSpy.mockRestore();
    });

    test('export yearly opens correct URL', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
      });

      const yearInput = screen.getByLabelText(/year/i);
      await user.clear(yearInput);
      await user.type(yearInput, '2025');

      // Switch to yearly mode then click Export
      await user.click(screen.getByRole('button', { name: /^yearly$/i }));
      await user.click(screen.getByRole('button', { name: /^export$/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = openSpy.mock.calls[0][0] as string;
      expect(url).toBe('/api/export/yearly/2025');
      expect(openSpy.mock.calls[0][1]).toBe('_blank');
      openSpy.mockRestore();
    });

    test('fetches budget from budgets endpoint (not budget)', async () => {
      renderSettings();
      await waitFor(() => {
        expect(mockedApi.get).toHaveBeenCalledWith(
          expect.stringMatching(/^budgets\?year=/)
        );
      });
    });

    test('saves budget via PUT to budgets/{year}/{month}', async () => {
      // Freeze `Date` to April 2026 so `new Date()` in the component lines
      // up with the mocked budget row (year=2026, month=4). Without this,
      // the test passes only by coincidence on days when today's month+year
      // matches the hardcoded mock data. `toFake: ['Date']` leaves
      // setTimeout/setInterval alone so testing-library's waitFor and
      // userEvent's internal timers still work.
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
      try {
        mockedApi.put.mockResolvedValue({});
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderSettings();

        const label = /Budget for April 2026/i;
        await waitFor(() => {
          expect(screen.getByLabelText(label)).toBeInTheDocument();
        });

        const input = screen.getByLabelText(label);
        await user.clear(input);
        await user.type(input, '5000');

        await user.click(screen.getByRole('button', { name: /save budgets/i }));

        await waitFor(() => {
          expect(mockedApi.put).toHaveBeenCalledWith(
            'budgets/2026/4',
            { amount: 5000 },
          );
        });
      } finally {
        vi.useRealTimers();
      }
    });

    test('Set all applies amount to every month and shows Undo', async () => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
      try {
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderSettings();

        // Wait for the initial fetch so baseline is populated.
        await waitFor(() => {
          expect(
            (screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement).value,
          ).toBe('3000');
        });

        const bulk = screen.getByLabelText(/Apply amount to all months of 2026/i);
        await user.clear(bulk);
        await user.type(bulk, '1000');
        await user.click(screen.getByRole('button', { name: /^Apply$/ }));

        // Every month takes the bulk value.
        for (const name of [
          'January', 'February', 'March', 'April', 'May', 'June',
          'July', 'August', 'September', 'October', 'November', 'December',
        ]) {
          const input = screen.getByLabelText(
            new RegExp(`Budget for ${name} 2026`, 'i'),
          ) as HTMLInputElement;
          expect(input.value).toBe('1000');
        }

        // Undo chip is visible.
        expect(
          screen.getByRole('button', { name: /^Undo$/ }),
        ).toBeInTheDocument();
      } finally {
        vi.useRealTimers();
      }
    });

    test('Undo restores pre-bulk amounts', async () => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
      try {
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderSettings();

        const april = () =>
          screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
        const may = () =>
          screen.getByLabelText(/Budget for May 2026/i) as HTMLInputElement;

        await waitFor(() => expect(april().value).toBe('3000'));
        expect(may().value).toBe('');

        const bulk = screen.getByLabelText(/Apply amount to all months of 2026/i);
        await user.type(bulk, '1000');
        await user.click(screen.getByRole('button', { name: /^Apply$/ }));
        expect(april().value).toBe('1000');
        expect(may().value).toBe('1000');

        await user.click(screen.getByRole('button', { name: /^Undo$/ }));

        expect(april().value).toBe('3000');
        expect(may().value).toBe('');
        expect(
          screen.queryByRole('button', { name: /^Undo$/ }),
        ).not.toBeInTheDocument();
      } finally {
        vi.useRealTimers();
      }
    });

    test('Copy from previous year fills months from prior year', async () => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
      try {
        // Override the default mock: return different rows for 2025.
        mockedApi.get.mockImplementation((path: string) => {
          if (path === 'budgets?year=2026')
            return Promise.resolve([
              { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
            ]);
          if (path === 'budgets?year=2025')
            return Promise.resolve([
              { id: 10, year: 2025, month: 1, amount: 800, updated_at: '' },
              { id: 11, year: 2025, month: 2, amount: 900, updated_at: '' },
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
        });

        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderSettings();

        await waitFor(() => {
          expect(
            (screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement).value,
          ).toBe('3000');
        });

        await user.click(screen.getByRole('button', { name: /Copy from 2025/i }));

        await waitFor(() => {
          expect(
            (screen.getByLabelText(/Budget for January 2026/i) as HTMLInputElement).value,
          ).toBe('800');
        });
        expect(
          (screen.getByLabelText(/Budget for February 2026/i) as HTMLInputElement).value,
        ).toBe('900');
        // Months not present in prior year reset to empty (Copy is a replace).
        expect(
          (screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement).value,
        ).toBe('');
        expect(
          screen.getByRole('button', { name: /^Undo$/ }),
        ).toBeInTheDocument();
      } finally {
        vi.useRealTimers();
      }
    });

    test('Copy from previous year shows info toast when prior year is empty', async () => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
      try {
        mockedApi.get.mockImplementation((path: string) => {
          if (path === 'budgets?year=2026')
            return Promise.resolve([
              { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
            ]);
          if (path === 'budgets?year=2025') return Promise.resolve([]);
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
        });

        const { toast } = await import('sonner');
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderSettings();

        await waitFor(() => {
          expect(screen.getByLabelText(/Budget for April 2026/i)).toBeInTheDocument();
        });

        await user.click(screen.getByRole('button', { name: /Copy from 2025/i }));

        await waitFor(() => {
          expect(toast.info).toHaveBeenCalledWith('No budgets found for 2025');
        });
        // April input unchanged (no bulk write happened).
        expect(
          (screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement).value,
        ).toBe('3000');
        expect(
          screen.queryByRole('button', { name: /^Undo$/ }),
        ).not.toBeInTheDocument();
      } finally {
        vi.useRealTimers();
      }
    });

    test('Apply then Copy then Undo restores original baseline', async () => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
      try {
        mockedApi.get.mockImplementation((path: string) => {
          if (path === 'budgets?year=2026')
            return Promise.resolve([
              { id: 1, year: 2026, month: 4, amount: 3000, updated_at: '' },
            ]);
          if (path === 'budgets?year=2025')
            return Promise.resolve([
              { id: 10, year: 2025, month: 1, amount: 800, updated_at: '' },
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
        });

        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderSettings();

        const april = () =>
          screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
        const january = () =>
          screen.getByLabelText(/Budget for January 2026/i) as HTMLInputElement;
        await waitFor(() => expect(april().value).toBe('3000'));

        // First bulk: Apply "1000" → snapshot captures baseline (Apr=3000).
        const bulk = screen.getByLabelText(/Apply amount to all months of 2026/i);
        await user.type(bulk, '1000');
        await user.click(screen.getByRole('button', { name: /^Apply$/ }));
        expect(april().value).toBe('1000');

        // Second bulk: Copy from 2025 → snapshot MUST NOT be overwritten.
        await user.click(screen.getByRole('button', { name: /Copy from 2025/i }));
        await waitFor(() => expect(january().value).toBe('800'));

        // Undo goes back to the ORIGINAL baseline (April=3000, others empty),
        // not the intermediate Apply-filled state.
        await user.click(screen.getByRole('button', { name: /^Undo$/ }));
        expect(april().value).toBe('3000');
        expect(january().value).toBe('');
      } finally {
        vi.useRealTimers();
      }
    });

    test('Undo then Apply takes a fresh snapshot', async () => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
      try {
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderSettings();

        const april = () =>
          screen.getByLabelText(/Budget for April 2026/i) as HTMLInputElement;
        await waitFor(() => expect(april().value).toBe('3000'));

        const bulk = () =>
          screen.getByLabelText(/Apply amount to all months of 2026/i);

        // First cycle: Apply → Undo.
        await user.type(bulk(), '1000');
        await user.click(screen.getByRole('button', { name: /^Apply$/ }));
        await user.click(screen.getByRole('button', { name: /^Undo$/ }));
        expect(april().value).toBe('3000');

        // Second cycle: the snapshot was cleared by Undo, so the next
        // Apply captures the current (post-Undo baseline) state, and
        // Undo correctly returns there — not to some stale snapshot.
        await user.type(bulk(), '2500');
        await user.click(screen.getByRole('button', { name: /^Apply$/ }));
        expect(april().value).toBe('2500');

        await user.click(screen.getByRole('button', { name: /^Undo$/ }));
        expect(april().value).toBe('3000');
      } finally {
        vi.useRealTimers();
      }
    });

    test('annual total reflects per-row edits', async () => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
      try {
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        renderSettings();

        const total = () => screen.getByLabelText(/^Annual total$/);

        await waitFor(() => {
          expect(total().textContent).toBe('$3,000.00');
        });

        const may = screen.getByLabelText(/Budget for May 2026/i);
        await user.type(may, '500');

        await waitFor(() => {
          expect(total().textContent).toBe('$3,500.00');
        });
      } finally {
        vi.useRealTimers();
      }
    });

    test('saves currency rates via PUT with full currency object', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /currencies/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/rate for eur/i)).toBeInTheDocument();
      });

      const rateInput = screen.getByLabelText(/rate for eur/i);
      await user.clear(rateInput);
      await user.type(rateInput, '0.95');

      await user.click(screen.getByRole('button', { name: /save rates/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'currencies/EUR',
          expect.objectContaining({
            name: 'Euro',
            symbol: '\u20AC',
            rate_to_base: 0.95,
            is_base: false,
          }),
        );
      });
    });

    test('adds savings goal via PUT to savings-goals/{year}', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /savings/i }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /add goal/i })).toBeInTheDocument();
      });

      // Open the Add Goal dialog
      await user.click(screen.getByRole('button', { name: /add goal/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/target amount/i)).toBeInTheDocument();
      });

      const yearInput = screen.getByLabelText(/^year$/i);
      await user.clear(yearInput);
      await user.type(yearInput, '2027');

      const amountInput = screen.getByLabelText(/target amount/i);
      await user.type(amountInput, '10000');

      await user.click(screen.getByRole('button', { name: /^add goal$/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'savings-goals/2027',
          { target_amount: 10000 },
        );
      });
    });

    test('deletes savings goal by setting target_amount to 0 via PUT', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /savings/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/delete 2026 goal/i)).toBeInTheDocument();
      });

      await user.click(screen.getByLabelText(/delete 2026 goal/i));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'savings-goals/2026',
          { target_amount: 0 },
        );
      });
    });

    test('changes user role via PUT (not PATCH)', async () => {
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /users/i }));

      await waitFor(() => {
        expect(
          screen.getByRole('combobox', { name: /role for alice/i }),
        ).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('combobox', { name: /role for alice/i }),
      );
      await user.click(screen.getByRole('option', { name: /^member$/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith('users/1', {
          role: 'member',
        });
      });
    });
  });

  describe('as member', () => {
    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: {
          id: 2,
          username: 'bob',
          display_name: 'Bob',
          role: 'member',
          created_at: '2024-01-01',
        },
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('hides users tab for non-admin', () => {
      renderSettings();
      expect(
        screen.queryByRole('tab', { name: /users/i }),
      ).not.toBeInTheDocument();
    });

    test('still shows general, currencies, savings, and data tabs', () => {
      renderSettings();
      expect(screen.getByRole('tab', { name: /general/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /currencies/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /savings/i })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /import \/ export/i })).toBeInTheDocument();
    });
  });

  describe('Import Wizard', () => {
    const mockCategories: Category[] = [
      { id: 1, name: 'Food', type: 'expense', icon: null, sort_order: 1, is_active: true, created_at: '2026-01-01' },
      { id: 2, name: 'Transport', type: 'expense', icon: null, sort_order: 2, is_active: true, created_at: '2026-01-01' },
      { id: 3, name: 'Salary', type: 'income', icon: null, sort_order: 3, is_active: true, created_at: '2026-01-01' },
    ];

    const mockPreview: ImportPreview = {
      import_id: 'abc-123',
      row_count: 5,
      rows: [
        { row_id: 0, skip: false, content_hash: '', date: '2026-01-15', description: 'Grocery Store', amount: 45.50, category: 'Food' },
        { row_id: 1, skip: false, content_hash: '', date: '2026-01-16', description: 'Bus Ticket', amount: 2.50, category: 'Transport' },
        { row_id: 2, skip: false, content_hash: '', date: '2026-01-17', description: 'Coffee Shop', amount: 5.00, category: 'Unknown' },
      ],
      columns: ['date', 'description', 'amount', 'category'],
      unique_categories: ['Food', 'Transport', 'Unknown'],
      collision_groups: [],
      expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
    };

    const mockImportResult: ImportResult = {
      imported: 4,
      skipped: 1,
      total: 5,
    };

    function makeXlsxFile(name = 'transactions.xlsx') {
      return new File(['test'], name, {
        type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      });
    }

    // useImportSession hits raw `fetch` for POST /api/import/confirm (to
    // preserve the 409 UNRESOLVED_COLLISIONS body) rather than going
    // through the mocked `api.post`. These tests stub `globalThis.fetch`
    // per-case with a small helper and restore the original afterwards.
    const originalFetch = globalThis.fetch;

    function mockConfirmFetch(spec: {
      ok?: boolean;
      status?: number;
      body?: unknown;
    }): ReturnType<typeof vi.fn> {
      const fetchMock = vi.fn().mockResolvedValue({
        ok: spec.ok ?? true,
        status: spec.status ?? 200,
        json: () => Promise.resolve(spec.body ?? {}),
      } as Response);
      globalThis.fetch = fetchMock;
      return fetchMock;
    }

    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: {
          id: 1,
          username: 'alice',
          display_name: 'Alice',
          role: 'admin',
          created_at: '2024-01-01',
        },
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    afterEach(() => {
      globalThis.fetch = originalFetch;
    });

    async function goToDataTab() {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();
      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));
      return user;
    }

    test('shows import section with file input when data tab is active', async () => {
      await goToDataTab();

      // CardTitle renders as a <div>, not a heading element
      expect(screen.getByText(/^import$/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/excel file/i)).toBeInTheDocument();
    });

    test('shows info text about required columns', async () => {
      await goToDataTab();

      expect(screen.getByText(/date.*description.*amount/i)).toBeInTheDocument();
    });

    test('uploads file and shows preview on file selection', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();

      const fileInput = screen.getByLabelText(/excel file/i);
      await user.upload(fileInput, makeXlsxFile());

      await waitFor(() => {
        expect(mockedApi.upload).toHaveBeenCalledWith('import/upload', expect.any(File));
      });

      await waitFor(() => {
        expect(screen.getByText(/found 5 rows/i)).toBeInTheDocument();
      });
    });

    test('shows preview table with imported data', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByText('Grocery Store')).toBeInTheDocument();
        expect(screen.getByText('Bus Ticket')).toBeInTheDocument();
        expect(screen.getByText('Coffee Shop')).toBeInTheDocument();
      });
    });

    test('shows default category dropdown in preview step', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByLabelText(/default category/i)).toBeInTheDocument();
      });
    });

    test('shows import and cancel buttons in preview step', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        // Button label dropped the " Rows" suffix in 3.4b — the label now
        // comes from ImportPreviewTable's footer (`Import ${keepCount}`).
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument();
      });
    });

    test('confirms import and shows result', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockConfirmFetch({ body: mockImportResult });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });

      // 3.4b collapsed the confirm modal — clicking the Import button now
      // fires the confirm POST directly. The old two-step (click Import →
      // dialog → click "Confirm and Import") is gone.
      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      await waitFor(() => {
        expect(screen.getByText(/4 imported/i)).toBeInTheDocument();
        expect(screen.getByText(/1 skipped/i)).toBeInTheDocument();
      });
    });

    test('shows "Import Another" button after successful import', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockConfirmFetch({ body: mockImportResult });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /import another/i })).toBeInTheDocument();
      });
    });

    test('resets to upload step when cancel is clicked', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.del.mockResolvedValue(undefined);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /cancel/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/excel file/i)).toBeInTheDocument();
      });
    });

    test('shows error message on upload failure', async () => {
      mockedApi.upload.mockRejectedValue(new Error('Invalid file format'));
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile('bad.xlsx'));

      await waitFor(() => {
        expect(screen.getByText(/invalid file format/i)).toBeInTheDocument();
      });
    });

    test('shows error message on confirm failure', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      // Non-409 confirm failure — the hook surfaces `err.error` from the
      // backend body via `importSession.error`, which the Alert renders
      // inline. 3.4b dropped the old toast path because a destructive
      // alert is more durable than a dismissable toast for a blocking
      // error on a multi-step wizard.
      mockConfirmFetch({
        ok: false,
        status: 500,
        body: { error: 'Import failed: duplicate rows' },
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      await waitFor(() => {
        expect(
          screen.getByText('Import failed: duplicate rows'),
        ).toBeInTheDocument();
      });
    });

    test('resets to upload step when "Import Another" is clicked', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockConfirmFetch({ body: mockImportResult });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /import another/i })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: /import another/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/excel file/i)).toBeInTheDocument();
      });
    });

    test('shows category mapping section in preview', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByText(/category mapping/i)).toBeInTheDocument();
      });
    });

    test('sends import_id when confirming import', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      const fetchMock = mockConfirmFetch({ body: mockImportResult });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+$/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /^import \d+$/i }));

      // `confirmImport` serializes its payload with JSON.stringify before
      // handing it to fetch, so we inspect the body string here rather
      // than relying on a structural matcher against the api.post mock
      // (which the hook bypasses — see web/src/api/import.ts).
      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalled();
      });
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toContain('/api/import/confirm');
      expect(init.method).toBe('POST');
      const parsedBody = JSON.parse(init.body as string) as {
        import_id: string;
      };
      expect(parsedBody.import_id).toBe('abc-123');
    });
  });
});
