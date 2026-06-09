import { describe, test, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { api } from '@/api/client';
import { useAuth } from './useAuth';
import { useNotificationPrefs } from './useNotificationPrefs';
import type { NotificationSettings } from '@/api/types';
import type { User } from '@/api/types';

vi.mock('@/api/client', () => ({
  api: { get: vi.fn(), put: vi.fn() },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(m: string, s: number) {
      super(m);
      this.status = s;
    }
  },
}));

vi.mock('./useAuth', () => ({ useAuth: vi.fn() }));

const mockApi = vi.mocked(api);
const mockUseAuth = vi.mocked(useAuth);

const ADMIN: User = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin',
  created_at: '2024-01-01',
};
const MEMBER: User = { ...ADMIN, id: 2, username: 'bob', role: 'member' };

const SETTINGS: NotificationSettings = {
  over_budget: true,
  txn_added: false,
  txn_deleted: false,
  txn_edited: false,
  large_txn: false,
  large_txn_threshold_dollars: 500,
  digest_mode: 'off',
  quiet_start: '',
  quiet_end: '',
  quiet_tz: 'UTC',
  quiet_allow_over_budget: true,
};

function asAuth(user: User) {
  return {
    user,
    loading: false,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockApi.get.mockResolvedValue(SETTINGS);
  mockApi.put.mockResolvedValue(SETTINGS);
  mockUseAuth.mockReturnValue(asAuth(ADMIN));
});

describe('useNotificationPrefs', () => {
  test('loads household settings from GET push/preferences on mount', async () => {
    const { result } = renderHook(() => useNotificationPrefs());
    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(mockApi.get).toHaveBeenCalledWith('push/preferences');
    expect(result.current.settings).toEqual(SETTINGS);
  });

  test('canEdit is true for an admin', async () => {
    const { result } = renderHook(() => useNotificationPrefs());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.canEdit).toBe(true);
  });

  test('admin update(partial) PUTs the merged settings and stores the echo', async () => {
    const updated: NotificationSettings = { ...SETTINGS, txn_added: true };
    mockApi.put.mockResolvedValue(updated);
    const { result } = renderHook(() => useNotificationPrefs());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.update({ txn_added: true });
    });

    expect(mockApi.put).toHaveBeenCalledWith('push/preferences', {
      over_budget: true,
      txn_added: true,
      txn_deleted: false,
      txn_edited: false,
      large_txn: false,
      large_txn_threshold_dollars: 500,
      digest_mode: 'off',
      quiet_start: '',
      quiet_end: '',
      quiet_tz: 'UTC',
      quiet_allow_over_budget: true,
    });
    expect(result.current.settings).toEqual(updated);
  });

  test('non-admin update(partial) is a no-op and never PUTs', async () => {
    mockUseAuth.mockReturnValue(asAuth(MEMBER));
    const { result } = renderHook(() => useNotificationPrefs());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.canEdit).toBe(false);

    await act(async () => {
      await result.current.update({ txn_added: true });
    });

    expect(mockApi.put).not.toHaveBeenCalled();
    // Optimistic local state is unchanged when blocked.
    expect(result.current.settings).toEqual(SETTINGS);
  });
});
