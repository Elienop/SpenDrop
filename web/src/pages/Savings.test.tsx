import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement, type ReactNode } from 'react';

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

import { api } from '../api/client';
import { toast } from 'sonner';
import { Savings } from './Savings';

const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);

function defaultGet(path: string): Promise<unknown> {
  if (path === 'savings-goals') return Promise.resolve([]);
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

function renderSavings() {
  // Fresh QueryClient per render so the `useBaseCurrency` → `useCurrencies`
  // useQuery (migrated to TanStack Query) has a provider and an isolated cache.
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return render(
    <MemoryRouter>
      <Savings />
    </MemoryRouter>,
    { wrapper },
  );
}

describe('Savings page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    mockedApi.get.mockImplementation(defaultGet);
  });

  test('renders the Savings heading', () => {
    renderSavings();
    expect(
      screen.getByRole('heading', { level: 1, name: /^savings$/i }),
    ).toBeInTheDocument();
  });

  test('renders the Savings Goals card and Add Goal button when there are no goals', async () => {
    renderSavings();
    // CardTitle renders the exact "Savings Goals" string; the empty-state
    // body adds "No savings goals yet" so the looser /savings goals/i
    // regex would match both.
    expect(await screen.findByText(/^Savings Goals$/)).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /add goal/i }),
    ).toBeInTheDocument();
    // Empty-state message renders instead of an empty table.
    expect(screen.getByText(/no savings goals yet/i)).toBeInTheDocument();
  });

  test('renders an existing goal row with the formatted target amount', async () => {
    mockedApi.get.mockImplementation((path: string) => {
      if (path === 'savings-goals')
        return Promise.resolve([
          { id: 1, year: 2025, target_amount: 1000, updated_at: '' },
        ]);
      return defaultGet(path);
    });
    renderSavings();
    await waitFor(() => {
      expect(screen.getByText('2025')).toBeInTheDocument();
    });
    expect(screen.getByText('$1,000.00')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /delete 2025 goal/i }),
    ).toBeInTheDocument();
  });

  test('adds a savings goal via PUT to savings-goals/{year}', async () => {
    mockedApi.put.mockResolvedValue({});
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();

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

  test('clicking row Delete opens a confirm dialog naming year + amount', async () => {
    mockedApi.get.mockImplementation((path: string) => {
      if (path === 'savings-goals')
        return Promise.resolve([
          { id: 1, year: 2026, target_amount: 6000, updated_at: '' },
        ]);
      return defaultGet(path);
    });
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();

    await user.click(
      await screen.findByLabelText(/delete 2026 goal/i),
    );

    const dialog = await screen.findByRole('alertdialog');
    expect(dialog).toHaveTextContent(/delete savings goal/i);
    expect(dialog).toHaveTextContent('2026');
    expect(dialog).toHaveTextContent('$6,000.00');
    expect(mockedApi.put).not.toHaveBeenCalled();
  });

  test('Cancel in the delete confirm dialog leaves the row intact and skips PUT', async () => {
    mockedApi.put.mockResolvedValue({});
    mockedApi.get.mockImplementation((path: string) => {
      if (path === 'savings-goals')
        return Promise.resolve([
          { id: 1, year: 2026, target_amount: 6000, updated_at: '' },
        ]);
      return defaultGet(path);
    });
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();

    await user.click(
      await screen.findByLabelText(/delete 2026 goal/i),
    );
    await user.click(
      await screen.findByRole('button', { name: /^cancel$/i }),
    );

    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(),
    );
    expect(mockedApi.put).not.toHaveBeenCalled();
    // Row stays.
    expect(screen.getByLabelText(/delete 2026 goal/i)).toBeInTheDocument();
  });

  test('Delete in the confirm dialog PUTs target_amount: 0', async () => {
    mockedApi.put.mockResolvedValue({});
    mockedApi.get.mockImplementation((path: string) => {
      if (path === 'savings-goals')
        return Promise.resolve([
          { id: 1, year: 2026, target_amount: 6000, updated_at: '' },
        ]);
      return defaultGet(path);
    });

    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();

    await user.click(
      await screen.findByLabelText(/delete 2026 goal/i),
    );
    // The destructive button inside the AlertDialog has accessible
    // name "Delete" — match that one, not the row trigger.
    const dialog = await screen.findByRole('alertdialog');
    const within = dialog.querySelectorAll('button');
    const destructive = Array.from(within).find(
      (b) => b.textContent === 'Delete',
    );
    expect(destructive).toBeDefined();
    await user.click(destructive as HTMLElement);

    await waitFor(() => {
      expect(mockedApi.put).toHaveBeenCalledWith(
        'savings-goals/2026',
        { target_amount: 0 },
      );
    });
  });

  test('Add Goal with blank target shows a validation error and skips PUT', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();

    await user.click(screen.getByRole('button', { name: /add goal/i }));
    await screen.findByLabelText(/target amount/i);

    // Submit the form without typing a target — the dialog has its
    // own "Add Goal" submit button.
    const submits = screen.getAllByRole('button', { name: /^add goal$/i });
    // The dialog submit is the second one (first is the trigger
    // still in the DOM behind the modal).
    await user.click(submits[submits.length - 1]);

    expect(
      await screen.findByText(/target must be greater than 0|enter a target/i),
    ).toBeInTheDocument();
    expect(mockedApi.put).not.toHaveBeenCalled();
  });

  test('Add Goal with target=0 shows a validation error and skips PUT', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();

    await user.click(screen.getByRole('button', { name: /add goal/i }));
    const amount = await screen.findByLabelText(/target amount/i);
    await user.type(amount, '0');

    const submits = screen.getAllByRole('button', { name: /^add goal$/i });
    await user.click(submits[submits.length - 1]);

    expect(
      await screen.findByText(/target must be greater than 0|enter a target/i),
    ).toBeInTheDocument();
    expect(mockedApi.put).not.toHaveBeenCalled();
  });

  test('replace warning appears when picking an existing year', async () => {
    mockedApi.get.mockImplementation((path: string) => {
      if (path === 'savings-goals')
        return Promise.resolve([
          { id: 1, year: 2026, target_amount: 12000, updated_at: '' },
        ]);
      return defaultGet(path);
    });
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();
    // Wait for the existing 2026 row to land before opening the dialog
    // so the in-form `goals.find(...)` has data to match against.
    await screen.findByText('2026');

    await user.click(screen.getByRole('button', { name: /add goal/i }));
    const dialog = await screen.findByRole('dialog');
    const yearInput = within(dialog).getByLabelText(/^year$/i);
    await user.clear(yearInput);
    await user.type(yearInput, '2026');

    // Warning surfaces with the existing target amount.
    expect(
      await within(dialog).findByText(/already have a goal for 2026/i),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(/replace your current/i),
    ).toHaveTextContent('$12,000.00');

    // Submit button now reads Replace.
    expect(
      within(dialog).getByRole('button', { name: /^replace goal$/i }),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole('button', { name: /^add goal$/i }),
    ).not.toBeInTheDocument();

    // Switching to a year without a goal removes the warning and
    // reverts the button label.
    await user.clear(yearInput);
    await user.type(yearInput, '2030');
    await waitFor(() =>
      expect(
        within(dialog).queryByText(/already have a goal/i),
      ).not.toBeInTheDocument(),
    );
    expect(
      within(dialog).getByRole('button', { name: /^add goal$/i }),
    ).toBeInTheDocument();
  });

  test('replacing fires the same PUT and toasts "replaced"', async () => {
    mockedApi.put.mockResolvedValue({});
    mockedApi.get.mockImplementation((path: string) => {
      if (path === 'savings-goals')
        return Promise.resolve([
          { id: 1, year: 2026, target_amount: 12000, updated_at: '' },
        ]);
      return defaultGet(path);
    });
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();
    await screen.findByText('2026');

    await user.click(screen.getByRole('button', { name: /add goal/i }));
    const dialog = await screen.findByRole('dialog');
    const yearInput = within(dialog).getByLabelText(/^year$/i);
    await user.clear(yearInput);
    await user.type(yearInput, '2026');

    const targetInput = within(dialog).getByLabelText(/target amount/i);
    await user.type(targetInput, '15000');

    await user.click(
      await within(dialog).findByRole('button', { name: /^replace goal$/i }),
    );

    await waitFor(() => {
      expect(mockedApi.put).toHaveBeenCalledWith('savings-goals/2026', {
        target_amount: 15000,
      });
    });
    expect(mockedToast.success).toHaveBeenCalledWith(
      'Savings goal replaced',
    );
  });

  test('Cancel in the Add Goal dialog closes the dialog without a PUT', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSavings();

    await user.click(screen.getByRole('button', { name: /add goal/i }));
    const dialog = await screen.findByRole('dialog');

    // The Cancel button lives in the dialog footer next to the
    // submit. `DialogClose` closes the dialog via the same
    // onOpenChange callback that already resets the form.
    await user.click(
      await within(dialog).findByRole('button', { name: /^cancel$/i }),
    );

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument(),
    );
    expect(mockedApi.put).not.toHaveBeenCalled();
  });
});
