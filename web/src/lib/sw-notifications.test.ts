import { describe, it, expect } from 'vitest';
import { activityCount, buildNotificationOptions } from './sw-notifications';

describe('buildNotificationOptions', () => {
  it('preserves the current notification shape (body, icon, badge, data.url)', () => {
    const opts = buildNotificationOptions({
      title: 'SpenDrop',
      body: 'New expense added',
      url: '/transactions',
    });
    expect(opts.body).toBe('New expense added');
    expect(opts.icon).toBe('/pwa-192x192.png');
    expect(opts.badge).toBe('/badge-96x96.png');
    expect((opts.data as { url: string }).url).toBe('/transactions');
  });

  it('defaults body to empty string and url to / when absent', () => {
    const opts = buildNotificationOptions({});
    expect(opts.body).toBe('');
    expect((opts.data as { url: string }).url).toBe('/');
  });
});

type OptsWithRenotify = NotificationOptions & { renotify?: boolean };

describe('buildNotificationOptions tag + renotify', () => {
  it('passes the tag through and carries data.count', () => {
    const opts = buildNotificationOptions(
      {
        body: 'x',
        url: '/transactions',
        tag: 'activity',
      },
      3,
    );
    expect(opts.tag).toBe('activity');
    expect((opts.data as { count?: number }).count).toBe(3);
  });

  it('sets renotify true ONLY for budget* tags', () => {
    expect((buildNotificationOptions({ tag: 'budget-7-202606' }) as OptsWithRenotify).renotify).toBe(true);
    expect((buildNotificationOptions({ tag: 'budget-summary' }) as OptsWithRenotify).renotify).toBe(true);
    expect((buildNotificationOptions({ tag: 'activity' }) as OptsWithRenotify).renotify).toBe(false);
    expect((buildNotificationOptions({}) as OptsWithRenotify).renotify).toBe(false);
  });

  it('leaves tag undefined (no collapse) when not provided', () => {
    expect(buildNotificationOptions({ body: 'x' }).tag).toBeUndefined();
  });
});

describe('activityCount', () => {
  it('increments the existing rolled-up count by one', () => {
    expect(activityCount(3)).toBe(4);
    expect(activityCount(2)).toBe(3);
  });

  it('starts at 1 when there is no existing activity notification', () => {
    expect(activityCount(undefined)).toBe(1);
  });
});

import { applyActivityRollup } from './sw-notifications';

describe('applyActivityRollup', () => {
  it('increments the prior same-tag count and rewrites the body to "N new activities"', () => {
    // Simulates registration.getNotifications({ tag: 'activity' }) returning one
    // existing notification that carried data.count = 3.
    const existing = [{ data: { count: 3 } }];
    const result = applyActivityRollup(existing, {
      tag: 'activity',
      url: '/transactions',
      body: '$1.00 in Groceries — milk',
    });
    expect(result.count).toBe(4);
    expect(result.payload.body).toBe('4 new activities');
    expect(result.payload.tag).toBe('activity'); // other fields preserved
    expect(result.payload.url).toBe('/transactions');
  });

  it('keeps the detailed single-event body for the first activity (count 1)', () => {
    const result = applyActivityRollup([], {
      tag: 'activity',
      body: '$1.00 in Groceries — milk',
    });
    expect(result.count).toBe(1);
    expect(result.payload.body).toBe('$1.00 in Groceries — milk');
  });

  it('rewrites to the plural rollup once a second activity arrives (count 2)', () => {
    const existing = [{ data: { count: 1 } }];
    const result = applyActivityRollup(existing, { tag: 'activity', body: 'detailed' });
    expect(result.count).toBe(2);
    expect(result.payload.body).toBe('2 new activities');
  });
});

import { vi } from 'vitest';
import { applyAppBadge } from './sw-notifications';

describe('applyAppBadge', () => {
  it('calls setAppBadge with the count when the Badging API is available', () => {
    const setAppBadge = vi.fn(() => Promise.resolve());
    applyAppBadge({ setAppBadge }, 4);
    expect(setAppBadge).toHaveBeenCalledWith(4);
  });

  it('is a no-op (no throw) when the Badging API is unavailable', () => {
    // Browsers without the Badging API (e.g. desktop Firefox) lack setAppBadge.
    expect(() => applyAppBadge({}, 4)).not.toThrow();
  });
});

import { renderPushNotification } from './sw-notifications';

type ShowCall = { title: string; options: NotificationOptions };

describe('renderPushNotification', () => {
  it('rolls up an activity burst into "N new activities" and badges the running count', async () => {
    const shown: ShowCall[] = [];
    const badged: number[] = [];
    await renderPushNotification(
      {
        getNotifications: async () => [{ data: { count: 2 } }],
        showNotification: async (title, options) => {
          shown.push({ title, options });
        },
      },
      {
        setAppBadge: async (c?: number) => {
          badged.push(c ?? -1);
        },
      },
      { tag: 'activity', body: 'detailed', url: '/transactions' },
      'SpenDrop',
    );
    expect(shown).toHaveLength(1);
    expect(shown[0].options.body).toBe('3 new activities');
    expect((shown[0].options.data as { count?: number }).count).toBe(3);
    expect(badged).toEqual([3]);
  });

  it('falls back to the RAW payload (never drops the notification) when getNotifications rejects', async () => {
    const shown: ShowCall[] = [];
    await expect(
      renderPushNotification(
        {
          getNotifications: async () => {
            throw new Error('boom');
          },
          showNotification: async (title, options) => {
            shown.push({ title, options });
          },
        },
        {},
        { tag: 'activity', body: '$1.00 in Groceries — milk', url: '/transactions' },
        'SpenDrop',
      ),
    ).resolves.toBeUndefined();
    // The user still sees a notification — the raw, un-rolled single-event body.
    expect(shown).toHaveLength(1);
    expect(shown[0].options.body).toBe('$1.00 in Groceries — milk');
  });

  it('shows a non-activity (digest) push directly without consulting getNotifications', async () => {
    const shown: ShowCall[] = [];
    let getCalls = 0;
    await renderPushNotification(
      {
        getNotifications: async () => {
          getCalls++;
          return [];
        },
        showNotification: async (title, options) => {
          shown.push({ title, options });
        },
      },
      {},
      { tag: 'digest', body: 'Daily summary', url: '/' },
      'SpenDrop',
    );
    expect(getCalls).toBe(0);
    expect(shown).toHaveLength(1);
    expect(shown[0].options.body).toBe('Daily summary');
  });
});
