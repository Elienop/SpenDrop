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
