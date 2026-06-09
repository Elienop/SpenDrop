import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { NotificationsSection } from './Settings';
import type { UseWebPush } from '@/hooks/useWebPush';
import type { UseNotificationPrefs } from '@/hooks/useNotificationPrefs';
import type { NotificationSettings } from '@/api/types';

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

const SETTINGS: NotificationSettings = {
  over_budget: true,
  txn_added: false,
  txn_deleted: false,
  txn_edited: false,
  large_txn: false,
  large_txn_threshold_dollars: 500,
  digest_mode: 'off',
  digest_time: '08:00',
  quiet_start: '',
  quiet_end: '',
  quiet_tz: 'UTC',
  quiet_allow_over_budget: true,
};

const prefsHook: UseNotificationPrefs = {
  settings: SETTINGS,
  loading: false,
  error: '',
  canEdit: true,
  update: vi.fn().mockResolvedValue(undefined),
};

vi.mock('@/hooks/useNotificationPrefs', () => ({
  useNotificationPrefs: () => prefsHook,
}));

beforeEach(() => {
  mockHook.supported = true;
  mockHook.permission = 'default';
  mockHook.subscribed = false;
  mockHook.busy = false;
  prefsHook.settings = { ...SETTINGS };
  prefsHook.loading = false;
  prefsHook.error = '';
  prefsHook.canEdit = true;
  prefsHook.update = vi.fn().mockResolvedValue(undefined);
});

describe('NotificationsSection — per-device master toggle', () => {
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

describe('NotificationsSection — household notification types', () => {
  test('admin sees an editable Switch per type and a threshold input', () => {
    prefsHook.canEdit = true;
    render(<NotificationsSection />);

    for (const name of [
      /over budget/i,
      /transaction added/i,
      /transaction deleted/i,
      /transaction edited/i,
      /large transaction/i,
    ]) {
      expect(screen.getByRole('switch', { name })).toBeEnabled();
    }
    expect(
      screen.getByLabelText(/large transaction threshold/i),
    ).toBeEnabled();
    expect(
      screen.queryByText(/managed by your admin/i),
    ).not.toBeInTheDocument();
  });

  test('non-admin sees the type toggles disabled with an admin note', () => {
    prefsHook.canEdit = false;
    render(<NotificationsSection />);

    expect(
      screen.getByRole('switch', { name: /over budget/i }),
    ).toBeDisabled();
    expect(
      screen.getByRole('switch', { name: /large transaction/i }),
    ).toBeDisabled();
    expect(
      screen.getByLabelText(/large transaction threshold/i),
    ).toBeDisabled();
    expect(
      screen.getByText(/managed by your admin/i),
    ).toBeInTheDocument();
  });

  test('renders the error message instead of Loading when prefs fail to load', () => {
    prefsHook.settings = null;
    prefsHook.loading = false;
    prefsHook.error = 'Failed to load preferences';
    render(<NotificationsSection />);

    expect(
      screen.getByText(/failed to load preferences/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/^loading…$/i)).not.toBeInTheDocument();
    // The type toggles must not render when the load failed.
    expect(
      screen.queryByRole('switch', { name: /over budget/i }),
    ).not.toBeInTheDocument();
  });

  test('clearing the threshold input does not push a $0 update', () => {
    prefsHook.canEdit = true;
    const update = vi.fn().mockResolvedValue(undefined);
    prefsHook.update = update;
    render(<NotificationsSection />);

    const input = screen.getByLabelText(/large transaction threshold/i);
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.blur(input);

    expect(update).not.toHaveBeenCalled();
  });
});

describe('NotificationsSection — household controls without local push', () => {
  test('household policy block renders even when web push is unsupported', () => {
    mockHook.supported = false;
    render(<NotificationsSection />);

    // The device-agnostic household controls must remain reachable.
    expect(
      screen.getByRole('switch', { name: /over budget/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText(/large transaction threshold/i),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Daily digest')).toBeInTheDocument();
    expect(screen.getByLabelText('Quiet hours start')).toBeInTheDocument();
  });

  test('per-device push toggle + Send test are hidden when unsupported', () => {
    mockHook.supported = false;
    render(<NotificationsSection />);

    expect(
      screen.getByText(/does not support web push/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('switch', {
        name: /push notifications on this device/i,
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /send test/i }),
    ).not.toBeInTheDocument();
  });
});

describe('NotificationsSection — digest + quiet hours', () => {
  test('renders the digest mode control', () => {
    render(<NotificationsSection />);
    expect(screen.getByLabelText('Daily digest')).toBeInTheDocument();
  });

  test('hides the digest send time when digest mode is off', () => {
    prefsHook.settings = { ...SETTINGS, digest_mode: 'off' };
    render(<NotificationsSection />);
    expect(screen.queryByLabelText('Digest send time')).not.toBeInTheDocument();
  });

  test('shows the digest send time when daily and PUTs a changed time', () => {
    prefsHook.settings = {
      ...SETTINGS,
      digest_mode: 'daily',
      digest_time: '08:00',
    };
    render(<NotificationsSection />);
    const input = screen.getByLabelText('Digest send time');
    fireEvent.change(input, { target: { value: '09:30' } });
    fireEvent.blur(input);
    expect(prefsHook.update).toHaveBeenCalledWith({ digest_time: '09:30' });
  });

  test('does not PUT a cleared (empty) digest send time', () => {
    prefsHook.settings = {
      ...SETTINGS,
      digest_mode: 'daily',
      digest_time: '08:00',
    };
    render(<NotificationsSection />);
    const input = screen.getByLabelText('Digest send time');
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.blur(input);
    expect(prefsHook.update).not.toHaveBeenCalled();
  });

  test('explains that quiet hours need both bounds to take effect', () => {
    render(<NotificationsSection />);
    expect(screen.getByText(/a single bound is ignored/i)).toBeInTheDocument();
  });

  test('warns while exactly one quiet bound is set', () => {
    render(<NotificationsSection />);
    expect(
      screen.queryByText(/fill in the other field/i),
    ).not.toBeInTheDocument();
    const start = screen.getByLabelText('Quiet hours start');
    fireEvent.change(start, { target: { value: '22:30' } });
    expect(screen.getByText(/fill in the other field/i)).toBeInTheDocument();
  });

  test('toggling "Allow over-budget during quiet hours" PUTs the change', () => {
    prefsHook.settings = { ...SETTINGS, quiet_allow_over_budget: true };
    render(<NotificationsSection />);
    fireEvent.click(
      screen.getByRole('switch', { name: /allow over-budget during quiet hours/i }),
    );
    expect(prefsHook.update).toHaveBeenCalledWith({ quiet_allow_over_budget: false });
  });

  test('a lone quiet-start does not PUT a half-set window', () => {
    // From the both-empty default, filling only the start is a transitional
    // half-set state the backend rejects (400). The UI must not send it.
    render(<NotificationsSection />);
    const start = screen.getByLabelText('Quiet hours start');
    fireEvent.change(start, { target: { value: '22:30' } });
    fireEvent.blur(start);
    expect(prefsHook.update).not.toHaveBeenCalled();
  });

  test('setting both bounds PUTs the combined quiet window', () => {
    render(<NotificationsSection />);
    const start = screen.getByLabelText('Quiet hours start');
    const end = screen.getByLabelText('Quiet hours end');
    fireEvent.change(start, { target: { value: '22:30' } });
    fireEvent.blur(start);
    fireEvent.change(end, { target: { value: '08:00' } });
    fireEvent.blur(end);
    expect(prefsHook.update).toHaveBeenCalledTimes(1);
    expect(prefsHook.update).toHaveBeenCalledWith({
      quiet_start: '22:30',
      quiet_end: '08:00',
    });
  });

  test('clearing both bounds PUTs an empty quiet window', () => {
    prefsHook.settings = { ...SETTINGS, quiet_start: '22:00', quiet_end: '08:00' };
    render(<NotificationsSection />);
    const start = screen.getByLabelText('Quiet hours start');
    const end = screen.getByLabelText('Quiet hours end');
    fireEvent.change(start, { target: { value: '' } });
    fireEvent.blur(start);
    fireEvent.change(end, { target: { value: '' } });
    fireEvent.blur(end);
    expect(prefsHook.update).toHaveBeenCalledTimes(1);
    expect(prefsHook.update).toHaveBeenCalledWith({
      quiet_start: '',
      quiet_end: '',
    });
  });
});
