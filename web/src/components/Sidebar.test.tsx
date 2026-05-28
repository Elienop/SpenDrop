import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../hooks/useTrashCount', () => ({
  useTrashCount: vi.fn(),
  TRASH_CHANGED_EVENT: 'spendrop-trash-changed',
}));

import { useAuth } from '../hooks/useAuth';
import { useTrashCount } from '../hooks/useTrashCount';
import { ThemeProvider } from './theme-provider';
import { Sidebar } from './Sidebar';

const mockedUseAuth = vi.mocked(useAuth);
const mockedUseTrashCount = vi.mocked(useTrashCount);

const mockUser = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin' as const,
  created_at: '2024-01-01',
};

function renderSidebar(currentPath = '/') {
  return render(
    <ThemeProvider>
      <MemoryRouter initialEntries={[currentPath]}>
        <Sidebar />
      </MemoryRouter>
    </ThemeProvider>,
  );
}

describe('Sidebar', () => {
  const mockLogout = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: mockUser,
      loading: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: mockLogout,
    });
    mockedUseTrashCount.mockReturnValue({
      count: 0,
      loading: false,
      refetch: vi.fn(),
    });
  });

  test('renders all navigation links', () => {
    renderSidebar();
    expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /reports/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /transactions/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /categories/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /settings/i })).toBeInTheDocument();
  });

  test('marks the current route as active via aria-current', () => {
    renderSidebar('/transactions');
    const link = screen.getByRole('link', { name: /transactions/i });
    expect(link).toHaveAttribute('aria-current', 'page');
  });

  test('displays user display name when expanded', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(screen.getByText('Alice')).toBeInTheDocument();
  });

  test('displays user avatar initial', () => {
    renderSidebar();
    expect(screen.getByText('A')).toBeInTheDocument();
  });

  test('calls logout when logout button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByRole('button', { name: /log\s*out/i }));
    expect(mockLogout).toHaveBeenCalledTimes(1);
  });

  test('has semantic nav element', () => {
    renderSidebar();
    expect(screen.getByRole('navigation')).toBeInTheDocument();
  });

  test('renders collapsed by default', () => {
    renderSidebar();
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
  });

  test('expands when toggle button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  test('persists expanded state to localStorage', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(localStorage.getItem('spendrop-sidebar')).toBe('true');
  });

  test('reads initial state from localStorage', () => {
    localStorage.setItem('spendrop-sidebar', 'true');
    renderSidebar();
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  test('dispatches sidebar-toggle event on toggle', async () => {
    const listener = vi.fn();
    window.addEventListener('sidebar-toggle', listener);
    try {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(listener).toHaveBeenCalled();
    } finally {
      window.removeEventListener('sidebar-toggle', listener);
    }
  });

  describe('Trash count badge', () => {
    test('hides the badge entirely when trash count is 0 (expanded)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 0,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const trashLink = screen.getByRole('link', { name: /trash/i });
      // No number rendered next to the label.
      expect(trashLink).not.toHaveTextContent(/\d+/);
    });

    test('renders the count next to the Trash label when admin and count > 0', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 7,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const trashLink = screen.getByRole('link', { name: /trash/i });
      // accessible name includes the count via the visible badge text.
      expect(trashLink).toHaveTextContent('7');
    });

    test('passes enabled=true to useTrashCount for admin users', () => {
      renderSidebar();
      expect(mockedUseTrashCount).toHaveBeenCalledWith(true);
    });

    test('passes enabled=false to useTrashCount for non-admin users', () => {
      mockedUseAuth.mockReturnValue({
        user: { ...mockUser, role: 'member' },
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: mockLogout,
      });
      renderSidebar();
      expect(mockedUseTrashCount).toHaveBeenCalledWith(false);
    });

    test('does not render the Trash link for non-admin users (regression check)', () => {
      mockedUseAuth.mockReturnValue({
        user: { ...mockUser, role: 'member' },
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: mockLogout,
      });
      // Even if the hook returned a positive count for some reason,
      // a non-admin must not see the link.
      mockedUseTrashCount.mockReturnValue({
        count: 99,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      expect(
        screen.queryByRole('link', { name: /trash/i }),
      ).not.toBeInTheDocument();
    });

    test('hides the badge text in the collapsed sidebar even when count > 0', () => {
      // Sidebar starts collapsed (default localStorage). In this state
      // the link is icon-only and there is no room for a number next
      // to the label, so the badge is suppressed.
      mockedUseTrashCount.mockReturnValue({
        count: 5,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      const trashLink = screen.getByRole('link', { name: /trash/i });
      expect(trashLink).not.toHaveTextContent(/5/);
    });

    test('uses an aria-label that names the count for screen readers (plural)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 7,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      expect(
        screen.getByRole('link', { name: 'Trash, 7 items' }),
      ).toBeInTheDocument();
    });

    test('uses a singular aria-label when the count is exactly 1', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 1,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      expect(
        screen.getByRole('link', { name: 'Trash, 1 item' }),
      ).toBeInTheDocument();
    });

    test('caps the visible badge text at "99+" while the aria-label keeps the real count', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 150,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      // aria-label uses the un-capped count for screen-reader fidelity.
      const link = screen.getByRole('link', { name: 'Trash, 150 items' });
      // Visible text is capped — the raw "150" is NOT in the rendered
      // text, but "99+" is.
      expect(link).toHaveTextContent('99+');
      expect(link.textContent).not.toContain('150');
    });

    test('shows the literal "99" at exactly 99 (cap kicks in at 100)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 99,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(
        screen.getByRole('link', { name: 'Trash, 99 items' }),
      ).toHaveTextContent('99');
    });

    test('shows "99+" at exactly 100', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 100,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(
        screen.getByRole('link', { name: 'Trash, 100 items' }),
      ).toHaveTextContent('99+');
    });
  });
});
