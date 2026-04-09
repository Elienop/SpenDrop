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
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { Categories } from './Categories';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

// Fixtures still carry `color` — the backend still returns it until commit 12.
// These tests no longer assert anything about `color` visually.
const mockCategories = [
  {
    id: 1,
    name: 'Food',
    type: 'expense' as const,
    color: '#ff0000',
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 2,
    name: 'Salary',
    type: 'income' as const,
    color: '#00ff00',
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 3,
    name: 'Transport',
    type: 'expense' as const,
    color: '#0000ff',
    icon: null,
    sort_order: 1,
    is_active: false,
    created_at: '2024-01-01',
  },
];

function renderCategories() {
  return render(
    <MemoryRouter>
      <Categories />
    </MemoryRouter>,
  );
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
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

describe('Categories', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedApi.get.mockResolvedValue(mockCategories);
  });

  describe('as admin', () => {
    beforeEach(asAdmin);

    test('renders the Categories heading', async () => {
      renderCategories();
      expect(
        screen.getByRole('heading', { level: 1, name: /categories/i }),
      ).toBeInTheDocument();
    });

    test('renders category rows from API', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
        expect(screen.getByText('Salary')).toBeInTheDocument();
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });
    });

    test('renders a type badge for each category', async () => {
      renderCategories();
      await waitFor(() => {
        // Expense and Income labels appear as badges in rows
        expect(screen.getAllByText(/expense/i).length).toBeGreaterThan(0);
        expect(screen.getAllByText(/income/i).length).toBeGreaterThan(0);
      });
    });

    test('renders Add category button for admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /add category/i }),
        ).toBeInTheDocument();
      });
    });

    test('clicking Add category opens a Sheet with a name input and type select', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));

      // Sheet dialog with form fields
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/type/i)).toBeInTheDocument();
      // Icon input is optional
      expect(screen.getByLabelText(/icon/i)).toBeInTheDocument();
      // No color input anywhere in the form
      expect(screen.queryByLabelText(/color/i)).not.toBeInTheDocument();
    });

    test('submitting the Add Sheet posts to categories without a color field', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));
      await user.type(screen.getByLabelText(/name/i), 'Rent');
      // Type defaults to 'expense' — leave as-is
      await user.click(screen.getByRole('button', { name: /save/i }));

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith(
          'categories',
          expect.objectContaining({ name: 'Rent', type: 'expense' }),
        );
      });
      // Explicit: no color field in the payload
      const payload = mockedApi.post.mock.calls[0][1] as Record<string, unknown>;
      expect(payload).not.toHaveProperty('color');
    });

    test('row kebab menu opens Edit and Activate/Deactivate actions', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      // Each row has an "Actions for {name}" trigger
      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      expect(screen.getByRole('menuitem', { name: /edit/i })).toBeInTheDocument();
      expect(
        screen.getByRole('menuitem', { name: /deactivate/i }),
      ).toBeInTheDocument();
    });

    test('inactive category kebab shows Activate', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for transport/i }),
      );
      expect(
        screen.getByRole('menuitem', { name: /activate/i }),
      ).toBeInTheDocument();
    });

    test('Edit menu item opens a Sheet with existing category values', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /edit/i }));

      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByLabelText(/name/i)).toHaveValue('Food');
      // No color input in edit form either
      expect(screen.queryByLabelText(/color/i)).not.toBeInTheDocument();
      // Type is immutable after creation — no Type select in edit mode
      expect(screen.queryByLabelText(/type/i)).not.toBeInTheDocument();
    });

    test('saving edits posts PUT without color or type fields', async () => {
      const user = userEvent.setup();
      mockedApi.put.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /edit/i }));

      const nameInput = screen.getByLabelText(/name/i);
      await user.clear(nameInput);
      await user.type(nameInput, 'Groceries');
      await user.click(screen.getByRole('button', { name: /save/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'categories/1',
          expect.objectContaining({ name: 'Groceries' }),
        );
      });
      const payload = mockedApi.put.mock.calls[0][1] as Record<string, unknown>;
      // Backend only accepts {name, color, icon}; color is intentionally
      // omitted until commit 12 drops the column, and type is immutable.
      expect(payload).not.toHaveProperty('color');
      expect(payload).not.toHaveProperty('type');
    });

    test('Deactivate menu item PATCHes is_active=false', async () => {
      const user = userEvent.setup();
      mockedApi.patch.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /deactivate/i }));

      await waitFor(() => {
        expect(mockedApi.patch).toHaveBeenCalledWith('categories/1', {
          is_active: false,
        });
      });
    });
  });

  describe('as member', () => {
    beforeEach(asMember);

    test('hides Add category button for non-admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      expect(
        screen.queryByRole('button', { name: /add category/i }),
      ).not.toBeInTheDocument();
    });

    test('hides row kebab menus for non-admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      expect(
        screen.queryByRole('button', { name: /actions for food/i }),
      ).not.toBeInTheDocument();
    });
  });
});
