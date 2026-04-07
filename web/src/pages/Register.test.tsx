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
      login: vi.fn(),
      register: mockRegister,
      logout: vi.fn(),
    });
  });

  test('renders register heading', () => {
    renderRegister();
    expect(
      screen.getByRole('heading', { level: 1, name: /register/i }),
    ).toBeInTheDocument();
  });

  test('renders username, password, and display name inputs', () => {
    renderRegister();
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
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

  test('calls register with username, password, and display name on submit', async () => {
    mockRegister.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderRegister();

    await user.type(screen.getByLabelText(/username/i), 'bob');
    await user.type(screen.getByLabelText(/password/i), 'pass456');
    await user.type(screen.getByLabelText(/display name/i), 'Bob');
    await user.click(screen.getByRole('button', { name: /create account/i }));

    expect(mockRegister).toHaveBeenCalledWith('bob', 'pass456', 'Bob');
  });

  test('displays error message on register failure', async () => {
    mockRegister.mockRejectedValueOnce(new Error('Username taken'));
    const user = userEvent.setup();
    renderRegister();

    await user.type(screen.getByLabelText(/username/i), 'bob');
    await user.type(screen.getByLabelText(/password/i), 'pass456');
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
    await user.type(screen.getByLabelText(/password/i), 'pass456');
    await user.type(screen.getByLabelText(/display name/i), 'Bob');
    await user.click(screen.getByRole('button', { name: /create account/i }));

    expect(
      screen.getByRole('button', { name: /creating/i }),
    ).toBeDisabled();
  });
});
