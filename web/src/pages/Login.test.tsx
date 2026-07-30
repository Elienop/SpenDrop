import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

import { useAuth } from '../hooks/useAuth';
import { Login } from './Login';

const mockedUseAuth = vi.mocked(useAuth);

function renderLogin() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <Login />
    </MemoryRouter>,
  );
}

describe('Login', () => {
  const mockLogin = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: null,
      loading: false,
      login: mockLogin,
      register: vi.fn(),
      logout: vi.fn(),
    });
  });

  test('renders login heading', () => {
    renderLogin();
    expect(
      screen.getByRole('heading', { level: 1, name: /spendrop/i }),
    ).toBeInTheDocument();
  });

  test('renders username and password inputs', () => {
    renderLogin();
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toBeInTheDocument();
  });

  test('renders submit button', () => {
    renderLogin();
    expect(
      screen.getByRole('button', { name: /log\s*in/i }),
    ).toBeInTheDocument();
  });

  test('renders link to register page', () => {
    renderLogin();
    expect(
      screen.getByRole('link', { name: /register/i }),
    ).toBeInTheDocument();
  });

  test('calls login with username and password on submit', async () => {
    mockLogin.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderLogin();

    await user.type(screen.getByLabelText(/username/i), 'alice');
    await user.type(screen.getByLabelText('Password'), 'secret');
    await user.click(screen.getByRole('button', { name: /log\s*in/i }));

    expect(mockLogin).toHaveBeenCalledWith('alice', 'secret');
  });

  test('password field is masked by default and reveals on toggle, and the revealed value still submits intact', async () => {
    mockLogin.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderLogin();

    const password = screen.getByLabelText('Password');
    expect(password).toHaveAttribute('type', 'password');

    await user.type(screen.getByLabelText(/username/i), 'alice');
    await user.type(password, 'secret');
    await user.click(screen.getByRole('button', { name: 'Show password' }));
    expect(password).toHaveAttribute('type', 'text');

    await user.click(screen.getByRole('button', { name: /log\s*in/i }));

    // Guards against a call site that forgets to forward {...field}: the
    // exact typed string must still reach the auth hook.
    expect(mockLogin).toHaveBeenCalledWith('alice', 'secret');
  });

  test('displays error message on login failure', async () => {
    mockLogin.mockRejectedValueOnce(new Error('Invalid credentials'));
    const user = userEvent.setup();
    renderLogin();

    await user.type(screen.getByLabelText(/username/i), 'alice');
    await user.type(screen.getByLabelText('Password'), 'wrong');
    await user.click(screen.getByRole('button', { name: /log\s*in/i }));

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(
        'Invalid credentials',
      );
    });
  });

  test('disables submit button while submitting', async () => {
    // Login that never resolves during the test
    mockLogin.mockReturnValue(new Promise(() => {}));
    const user = userEvent.setup();
    renderLogin();

    await user.type(screen.getByLabelText(/username/i), 'alice');
    await user.type(screen.getByLabelText('Password'), 'secret');
    await user.click(screen.getByRole('button', { name: /log/i }));

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /log/i })).toBeDisabled();
    });
  });
});
