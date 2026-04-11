import { describe, test, expect, vi, beforeEach } from 'vitest';
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
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { toast } from 'sonner';
import { Settings } from './Settings';
import type { Category, ImportPreview, ImportResult } from '../api/types';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);

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
        expect(screen.getByLabelText(/monthly budget/i)).toBeInTheDocument();
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

    test('data tab has year input, toggle group, and export button', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await user.click(screen.getByRole('tab', { name: /import \/ export/i }));

      await waitFor(() => {
        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
        expect(screen.getByRole('radio', { name: /monthly/i })).toBeInTheDocument();
        expect(screen.getByRole('radio', { name: /yearly/i })).toBeInTheDocument();
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
      await user.click(screen.getByRole('radio', { name: /yearly/i }));
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
      mockedApi.put.mockResolvedValue({});
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      renderSettings();

      await waitFor(() => {
        expect(screen.getByLabelText(/monthly budget/i)).toBeInTheDocument();
      });

      const input = screen.getByLabelText(/monthly budget/i);
      await user.clear(input);
      await user.type(input, '5000');

      await user.click(screen.getByRole('button', { name: /save budget/i }));

      await waitFor(() => {
        const now = new Date();
        const year = now.getFullYear();
        const month = now.getMonth() + 1;
        expect(mockedApi.put).toHaveBeenCalledWith(
          `budgets/${year}/${month}`,
          { amount: 5000 },
        );
      });
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
        { date: '2026-01-15', description: 'Grocery Store', amount: 45.50, category: 'Food' },
        { date: '2026-01-16', description: 'Bus Ticket', amount: 2.50, category: 'Transport' },
        { date: '2026-01-17', description: 'Coffee Shop', amount: 5.00, category: 'Unknown' },
      ],
      columns: ['date', 'description', 'amount', 'category'],
      unique_categories: ['Food', 'Transport', 'Unknown'],
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
        expect(screen.getByRole('button', { name: /^import \d+ rows$/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument();
      });
    });

    test('confirms import and shows result', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.post.mockImplementation((path: string) => {
        if (path === 'import/confirm') return Promise.resolve(mockImportResult);
        return Promise.resolve({});
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+ rows$/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /^import \d+ rows$/i }));

      // Confirmation dialog should appear
      await waitFor(() => {
        expect(
          screen.getByRole('dialog', { name: /confirm import/i }),
        ).toBeInTheDocument();
      });
      await user.click(
        screen.getByRole('button', { name: /confirm and import/i }),
      );

      await waitFor(() => {
        expect(screen.getByText(/4 imported/i)).toBeInTheDocument();
        expect(screen.getByText(/1 skipped/i)).toBeInTheDocument();
      });
    });

    test('shows "Import Another" button after successful import', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.post.mockImplementation((path: string) => {
        if (path === 'import/confirm') return Promise.resolve(mockImportResult);
        return Promise.resolve({});
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+ rows$/i })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: /^import \d+ rows$/i }));

      // Confirmation dialog should appear
      await waitFor(() => {
        expect(
          screen.getByRole('dialog', { name: /confirm import/i }),
        ).toBeInTheDocument();
      });
      await user.click(
        screen.getByRole('button', { name: /confirm and import/i }),
      );

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
      mockedApi.post.mockImplementation((path: string) => {
        if (path === 'import/confirm') return Promise.reject(new Error('Import failed: duplicate rows'));
        return Promise.resolve({});
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+ rows$/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /^import \d+ rows$/i }));

      // Confirmation dialog should appear
      await waitFor(() => {
        expect(
          screen.getByRole('dialog', { name: /confirm import/i }),
        ).toBeInTheDocument();
      });
      await user.click(
        screen.getByRole('button', { name: /confirm and import/i }),
      );

      await waitFor(() => {
        expect(mockedToast.error).toHaveBeenCalledWith(
          'Import failed: duplicate rows',
        );
      });
    });

    test('resets to upload step when "Import Another" is clicked', async () => {
      mockedApi.upload.mockResolvedValue(mockPreview);
      mockedApi.post.mockImplementation((path: string) => {
        if (path === 'import/confirm') return Promise.resolve(mockImportResult);
        return Promise.resolve({});
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+ rows$/i })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: /^import \d+ rows$/i }));

      // Confirmation dialog should appear
      await waitFor(() => {
        expect(
          screen.getByRole('dialog', { name: /confirm import/i }),
        ).toBeInTheDocument();
      });
      await user.click(
        screen.getByRole('button', { name: /confirm and import/i }),
      );

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
      mockedApi.post.mockImplementation((path: string) => {
        if (path === 'import/confirm') return Promise.resolve(mockImportResult);
        return Promise.resolve({});
      });
      mockedApi.get.mockImplementation((path: string) => {
        if (path === 'categories') return Promise.resolve(mockCategories);
        if (path.includes('budget')) return Promise.resolve([]);
        return Promise.resolve([]);
      });

      const user = await goToDataTab();
      await user.upload(screen.getByLabelText(/excel file/i), makeXlsxFile());

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /^import \d+ rows$/i })).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /^import \d+ rows$/i }));

      // Confirmation dialog should appear
      await waitFor(() => {
        expect(
          screen.getByRole('dialog', { name: /confirm import/i }),
        ).toBeInTheDocument();
      });
      await user.click(
        screen.getByRole('button', { name: /confirm and import/i }),
      );

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith('import/confirm', expect.objectContaining({
          import_id: 'abc-123',
        }));
      });
    });
  });
});
