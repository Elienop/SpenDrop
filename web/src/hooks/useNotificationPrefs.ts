import { useState, useEffect, useCallback } from 'react';
import { api } from '@/api/client';
import { useAuth } from './useAuth';
import { isAdmin } from '@/lib/roles';
import type { NotificationSettings } from '@/api/types';

export interface UseNotificationPrefs {
  settings: NotificationSettings | null;
  /** True only until the first load resolves (success or error). */
  loading: boolean;
  error: string;
  /**
   * True iff the current user is an admin. The household settings are
   * read-only for members; `update()` is a no-op for them (mirrors the
   * server's 403). The UI uses this to disable the controls.
   */
  canEdit: boolean;
  /**
   * Merge `partial` over the current settings and PUT the full object.
   * Optimistically applies the merge, then reconciles to the server echo.
   * No-op (resolves immediately, never PUTs) when `canEdit` is false or
   * settings have not loaded yet.
   */
  update(partial: Partial<NotificationSettings>): Promise<void>;
}

export function useNotificationPrefs(): UseNotificationPrefs {
  const { user } = useAuth();
  const canEdit = isAdmin(user);
  const [settings, setSettings] = useState<NotificationSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError('');
    api
      .get<NotificationSettings>('push/preferences')
      .then((data) => {
        if (active) setSettings(data);
      })
      .catch((err) => {
        if (!active) return;
        setError(
          err instanceof Error ? err.message : 'Failed to load preferences',
        );
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  const update = useCallback(
    async (partial: Partial<NotificationSettings>) => {
      // Client-side mirror of the server's admin gate: a member's PUT would
      // 403, so don't fire it. The disabled UI already blocks this path;
      // this is the belt to that suspenders.
      if (!canEdit || !settings) return;
      const next: NotificationSettings = { ...settings, ...partial };
      setSettings(next); // optimistic
      try {
        const echo = await api.put<NotificationSettings>(
          'push/preferences',
          next,
        );
        setSettings(echo); // reconcile to server truth
      } catch (err) {
        setSettings(settings); // roll back on failure
        throw err instanceof Error
          ? err
          : new Error('Failed to update preferences');
      }
    },
    [canEdit, settings],
  );

  return { settings, loading, error, canEdit, update };
}
