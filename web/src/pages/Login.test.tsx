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
      unverified: false,
      refreshUser: vi.fn(),
      login: mockLogin,
      register: vi.fn(),
      logout: vi.fn(),
    });
  });

  // Signing in needs the server. When the server cannot be reached, the login
  // screen is a dead end — and it is the screen the app lands on precisely
  // when connectivity is broken, which is when offline capture matters most.
  describe('with an identity this device remembers but cannot confirm', () => {
    beforeEach(() => {
      mockedUseAuth.mockReturnValue({
        user: {
          id: 4,
          username: 'alice',
          display_name: 'Alice',
          role: 'member',
          created_at: '2024-01-01',
        },
        loading: false,
        unverified: true,
        refreshUser: vi.fn(),
        login: mockLogin,
        register: vi.fn(),
        logout: vi.fn(),
      });
    });

    test('offers a way through to offline capture', () => {
      renderLogin();
      const link = screen.getByRole('link', { name: /continue as alice/i });
      expect(link).toHaveAttribute('href', '/quick');
    });

    test('says why signing in is unavailable rather than just failing', () => {
      renderLogin();
      expect(screen.getByText(/can’t reach the server/i)).toBeInTheDocument();
    });

    test('still offers the normal sign-in form', () => {
      renderLogin();
      expect(
        screen.getByRole('button', { name: /log\s*in/i }),
      ).toBeInTheDocument();
    });
  });

  test('does not offer offline capture when there is no remembered identity', () => {
    renderLogin();
    expect(screen.queryByRole('link', { name: /continue as/i })).toBeNull();
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

  test('the register link carries the pointer-gated 44px floor', () => {
    // WIRING pin, not a pixel proof — same register as the floor block in
    // dropdown-menu.test.tsx: happy-dom runs no layout and never loads the
    // Tailwind stylesheet, so the token is in the DOM at every pointer state
    // and the 44px itself is the browser pass's job. What this pins is that
    // the smallest target in the app (18px — one line of text-sm) keeps its
    // floor, that the token is the pointer-gated one (not bare, which would
    // change the fine-pointer sentence; not `md:`, which hands the coarse
    // tablet the 18px link), and that the two tokens that make the floor
    // ACT on an inline element are both present — an inline box ignores
    // min-height, so dropping `inline-flex` leaves the token decorative.
    renderLogin();
    const link = screen.getByRole('link', { name: 'Register' });

    expect(link.classList.contains('coarse:min-h-11')).toBe(true);
    expect(link.classList.contains('min-h-11')).toBe(false);
    expect(link.classList.contains('md:min-h-11')).toBe(false);
    expect(link.classList.contains('inline-flex')).toBe(true);
    expect(link.classList.contains('items-center')).toBe(true);
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
