import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { NotificationsSection } from './Settings';
import type { UseWebPush } from '@/hooks/useWebPush';

const mockHook: UseWebPush = {
  supported: true,
  permission: 'default',
  subscribed: false,
  busy: false,
  enable: vi.fn(),
  disable: vi.fn(),
  sendTest: vi.fn(),
};

vi.mock('@/hooks/useWebPush', () => ({
  useWebPush: () => mockHook,
}));

beforeEach(() => {
  mockHook.supported = true;
  mockHook.permission = 'default';
  mockHook.subscribed = false;
  mockHook.busy = false;
});

describe('NotificationsSection', () => {
  test('Send test is disabled until granted AND subscribed', () => {
    render(<NotificationsSection />);
    expect(
      screen.getByRole('button', { name: /send test/i }),
    ).toBeDisabled();
  });

  test('Send test is enabled when granted and subscribed', () => {
    mockHook.permission = 'granted';
    mockHook.subscribed = true;
    render(<NotificationsSection />);
    expect(
      screen.getByRole('button', { name: /send test/i }),
    ).toBeEnabled();
  });

  test('shows a denied hint when permission is denied', () => {
    mockHook.permission = 'denied';
    render(<NotificationsSection />);
    expect(screen.getByText(/blocked notifications/i)).toBeInTheDocument();
  });
});
