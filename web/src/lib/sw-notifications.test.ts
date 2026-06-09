import { describe, it, expect } from 'vitest';
import { buildNotificationOptions } from './sw-notifications';

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
