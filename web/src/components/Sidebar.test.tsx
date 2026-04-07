import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

// Mock useAuth
vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

import { useAuth } from '../hooks/useAuth';
import { Sidebar } from './Sidebar';

const mockedUseAuth = vi.mocked(useAuth);

const mockUser = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin' as const,
  created_at: '2024-01-01',
};

function renderSidebar(currentPath = '/') {
  return render(
    <MemoryRouter initialEntries={[currentPath]}>
      <Sidebar />
    </MemoryRouter>,
  );
}

describe('Sidebar', () => {
  const mockLogout = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: mockUser,
      loading: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: mockLogout,
    });
  });

  test('displays SpenDrop title', () => {
    renderSidebar();
    expect(screen.getByText('SpenDrop')).toBeInTheDocument();
  });

  test('renders all navigation links', () => {
    renderSidebar();
    expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /reports/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /transactions/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /categories/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /settings/i })).toBeInTheDocument();
  });

  test('highlights the active nav link', () => {
    renderSidebar('/transactions');
    const transactionsLink = screen.getByRole('link', { name: /transactions/i });
    expect(transactionsLink.className).toContain('active');
  });

  test('displays user display name', () => {
    renderSidebar();
    expect(screen.getByText('Alice')).toBeInTheDocument();
  });

  test('calls logout when logout button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();

    const logoutButton = screen.getByRole('button', { name: /log\s*out/i });
    await user.click(logoutButton);

    expect(mockLogout).toHaveBeenCalledTimes(1);
  });

  test('has semantic nav element', () => {
    renderSidebar();
    expect(screen.getByRole('navigation')).toBeInTheDocument();
  });
});
