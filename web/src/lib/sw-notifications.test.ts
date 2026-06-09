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
