import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

import { useAuth } from '../hooks/useAuth';
import { Register } from './Register';

const mockedUseAuth = vi.mocked(useAuth);

function renderRegister() {
  return render(
    <MemoryRouter initialEntries={['/register']}>
      <Register />
    </MemoryRouter>,
  );
}

describe('Register', () => {
  const mockRegister = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: null,
      loading: false,
      unverified: false,
      refreshUser: vi.fn(),
      login: vi.fn(),
      register: mockRegister,
      logout: vi.fn(),
    });
  });

  test('renders register heading', () => {
    renderRegister();
    expect(
      screen.getByRole('heading', { level: 1, name: /spendrop/i }),
    ).toBeInTheDocument();
  });

  test('renders username, password, and display name inputs', () => {
    renderRegister();
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toBeInTheDocument();
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument();
  });

  test('renders submit button', () => {
    renderRegister();
    expect(
      screen.getByRole('button', { name: /create account/i }),
    ).toBeInTheDocument();
  });

  test('renders link to login page', () => {
    renderRegister();
    expect(
      screen.getByRole('link', { name: /log\s*in/i }),
    ).toBeInTheDocument();
  });

  test('the login link carries the pointer-gated 44px floor', () => {
    // WIRING pin, not a pixel proof — the full rationale is on Login's twin
    // test; this one exists because the two pages are separate files and the
    // recipe can rot on one while the other stays pinned. `inline-flex` is
    // load-bearing, not styling: an inline box ignores min-height, so
    // without it the floor token is decorative.
    renderRegister();
    const link = screen.getByRole('link', { name: 'Log in' });

    expect(link.classList.contains('coarse:min-h-11')).toBe(true);
    expect(link.classList.contains('min-h-11')).toBe(false);
    expect(link.classList.contains('md:min-h-11')).toBe(false);
    expect(link.classList.contains('inline-flex')).toBe(true);
    expect(link.classList.contains('items-center')).toBe(true);
  });

  test('calls register with username, password, and display name on submit', async () => {
    mockRegister.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderRegister();

    await user.type(screen.getByLabelText(/username/i), 'bob');
    await user.type(screen.getByLabelText('Password'), 'pass456');
    await user.type(screen.getByLabelText(/display name/i), 'Bob');
    await user.click(screen.getByRole('button', { name: /create account/i }));

    expect(mockRegister).toHaveBeenCalledWith('bob', 'pass456', 'Bob');
  });

  test('displays error message on register failure', async () => {
    mockRegister.mockRejectedValueOnce(new Error('Username taken'));
    const user = userEvent.setup();
    renderRegister();

    await user.type(screen.getByLabelText(/username/i), 'bob');
    await user.type(screen.getByLabelText('Password'), 'pass456');
    await user.type(screen.getByLabelText(/display name/i), 'Bob');
    await user.click(screen.getByRole('button', { name: /create account/i }));

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Username taken');
    });
  });

  test('disables submit button while submitting', async () => {
    mockRegister.mockReturnValue(new Promise(() => {}));
    const user = userEvent.setup();
    renderRegister();

    await user.type(screen.getByLabelText(/username/i), 'bob');
    await user.type(screen.getByLabelText('Password'), 'pass456');
    await user.type(screen.getByLabelText(/display name/i), 'Bob');
    const submit = screen.getByRole('button', { name: /create account/i });
    await user.click(submit);

    await waitFor(() => expect(submit).toBeDisabled());
  });
});
