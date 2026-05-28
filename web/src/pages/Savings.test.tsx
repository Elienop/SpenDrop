import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

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
import { Savings } from './Savings';

const mockedApi = vi.mocked(api);

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
  return render(
    <MemoryRouter>
      <Savings />
    </MemoryRouter>,
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

  test('deletes a savings goal by setting target_amount to 0 via PUT', async () => {
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
});
