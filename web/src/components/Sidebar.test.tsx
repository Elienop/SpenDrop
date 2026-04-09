import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

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
    localStorage.clear();
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: mockUser,
      loading: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: mockLogout,
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
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(listener).toHaveBeenCalled();
    window.removeEventListener('sidebar-toggle', listener);
  });
});
