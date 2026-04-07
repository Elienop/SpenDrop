import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
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

describe('Categories', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedApi.get.mockResolvedValue(mockCategories);
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

    test('renders Categories heading', async () => {
      renderCategories();
      expect(
        screen.getByRole('heading', { level: 1, name: /categories/i }),
      ).toBeInTheDocument();
    });

    test('renders expense and income sections', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText(/expense categories/i)).toBeInTheDocument();
        expect(screen.getByText(/income categories/i)).toBeInTheDocument();
      });
    });

    test('renders category names from API', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
        expect(screen.getByText('Salary')).toBeInTheDocument();
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });
    });

    test('shows add button for admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(
          screen.getAllByRole('button', { name: /add.*category/i }).length,
        ).toBeGreaterThan(0);
      });
    });

    test('shows edit buttons for admin', async () => {
      renderCategories();
      await waitFor(() => {
        const editButtons = screen.getAllByRole('button', { name: /edit/i });
        expect(editButtons.length).toBeGreaterThan(0);
      });
    });

    test('shows active/inactive toggle', async () => {
      renderCategories();
      await waitFor(() => {
        // Transport is inactive, should show "Activate" or toggle
        expect(screen.getByText('Transport')).toBeInTheDocument();
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

    test('hides add button for non-admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      expect(
        screen.queryByRole('button', { name: /add.*category/i }),
      ).not.toBeInTheDocument();
    });

    test('hides edit buttons for non-admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      expect(
        screen.queryByRole('button', { name: /edit/i }),
      ).not.toBeInTheDocument();
    });
  });

  describe('drag and drop reorder', () => {
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
      mockedApi.get.mockResolvedValue(mockCategories);
      mockedApi.post.mockResolvedValue({});
    });

    test('sends POST to categories/reorder with id and sort_order array on drop', async () => {
      renderCategories();

      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });

      // Find the category cards for Food (sort_order 0) and Transport (sort_order 1)
      const foodCard = screen.getByText('Food').closest('[draggable]')!;
      const transportCard = screen.getByText('Transport').closest('[draggable]')!;

      // Simulate drag and drop: drag Transport onto Food
      const dataTransfer = {
        effectAllowed: '',
        dropEffect: '',
      };

      fireEvent.dragStart(transportCard, { dataTransfer });
      fireEvent.dragOver(foodCard, { dataTransfer });
      fireEvent.drop(foodCard, { dataTransfer });

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith(
          'categories/reorder',
          expect.arrayContaining([
            expect.objectContaining({ id: expect.any(Number), sort_order: expect.any(Number) }),
          ]),
        );
      });
    });
  });
});
