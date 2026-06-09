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

  it('persists data.lines so the next rollup can accumulate from it', () => {
    const opts = buildNotificationOptions(
      { body: 'x', url: '/transactions', tag: 'activity' },
      3,
      ['a', 'b'],
    );
    expect((opts.data as { lines?: readonly string[] }).lines).toEqual(['a', 'b']);
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
  it('rolls a burst: COUNT in the title, newest-first item lines in the body', () => {
    // Simulates getNotifications({ tag: 'activity' }) returning one existing
    // notification that carried data.count = 3 and two prior item lines.
    const existing = [{ data: { count: 3, lines: ['old1', 'old2'] } }];
    const result = applyActivityRollup(existing, {
      tag: 'activity',
      url: '/transactions',
      body: 'Sara added $20.00 in Coffee — Latte',
    });
    expect(result.count).toBe(4);
    // Newest item is prepended so the COLLAPSED first body line is the newest.
    expect(result.lines).toEqual([
      'Sara added $20.00 in Coffee — Latte',
      'old1',
      'old2',
    ]);
    expect(result.payload.title).toBe('4 new activities');
    // overflow = running count(4) - shown lines(3) = 1
    expect(result.payload.body).toBe(
      'Sara added $20.00 in Coffee — Latte\nold1\nold2\n+1 more',
    );
    expect(result.payload.tag).toBe('activity'); // other fields preserved
    expect(result.payload.url).toBe('/transactions');
  });

  it('caps accumulated lines at MAX_ROLLUP_LINES (4), newest-first', () => {
    const existing = [{ data: { count: 5, lines: ['l4', 'l3', 'l2', 'l1'] } }];
    const result = applyActivityRollup(existing, { tag: 'activity', body: 'newest' });
    expect(result.count).toBe(6);
    expect(result.lines).toEqual(['newest', 'l4', 'l3', 'l2']);
    // overflow uses the RUNNING count, not the (capped) array length: 6 - 4 = 2
    expect(result.payload.body).toBe('newest\nl4\nl3\nl2\n+2 more');
    expect(result.payload.title).toBe('6 new activities');
  });

  it('omits "+N more" when the running count fits within the shown lines', () => {
    const existing = [{ data: { count: 1, lines: ['old'] } }];
    const result = applyActivityRollup(existing, { tag: 'activity', body: 'new' });
    expect(result.count).toBe(2);
    expect(result.lines).toEqual(['new', 'old']);
    expect(result.payload.body).toBe('new\nold'); // overflow 2 - 2 = 0 => no "+N more"
    expect(result.payload.title).toBe('2 new activities');
  });

  it('keeps the detailed single-event body AND title for the first activity (count 1)', () => {
    const result = applyActivityRollup([], {
      tag: 'activity',
      title: 'SpenDrop',
      body: 'Sara added $1.00 in Groceries — milk',
    });
    expect(result.count).toBe(1);
    expect(result.payload.body).toBe('Sara added $1.00 in Groceries — milk');
    expect(result.payload.title).toBe('SpenDrop'); // unchanged
    expect(result.lines).toEqual(['Sara added $1.00 in Groceries — milk']);
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
  it('rolls up an activity burst: "N new activities" TITLE, newest-first body, persisted lines, badge', async () => {
    const shown: ShowCall[] = [];
    const badged: number[] = [];
    await renderPushNotification(
      {
        getNotifications: async () => [{ data: { count: 2, lines: ['prev'] } }],
        showNotification: async (title, options) => {
          shown.push({ title, options });
        },
      },
      {
        setAppBadge: async (c?: number) => {
          badged.push(c ?? -1);
        },
      },
      { tag: 'activity', body: 'Sara added $20.00 in Coffee — Latte', url: '/transactions' },
      'SpenDrop',
    );
    expect(shown).toHaveLength(1);
    // The COUNT goes in the title; the collapsed first body line is the NEWEST item.
    expect(shown[0].title).toBe('3 new activities');
    expect(shown[0].options.body).toBe('Sara added $20.00 in Coffee — Latte\nprev\n+1 more');
    expect((shown[0].options.data as { count?: number }).count).toBe(3);
    expect((shown[0].options.data as { lines?: readonly string[] }).lines).toEqual([
      'Sara added $20.00 in Coffee — Latte',
      'prev',
    ]);
    expect(badged).toEqual([3]);
  });

  it('shows the detailed single-event body with the original title for the first activity', async () => {
    const shown: ShowCall[] = [];
    await renderPushNotification(
      {
        getNotifications: async () => [],
        showNotification: async (title, options) => {
          shown.push({ title, options });
        },
      },
      { setAppBadge: async () => {} },
      {
        tag: 'activity',
        title: 'SpenDrop',
        body: 'Sara added $1.00 in Groceries — milk',
        url: '/transactions',
      },
      'SpenDrop',
    );
    expect(shown).toHaveLength(1);
    expect(shown[0].title).toBe('SpenDrop');
    expect(shown[0].options.body).toBe('Sara added $1.00 in Groceries — milk');
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
