// Pure, side-effect-free notification render helpers shared with the service
// worker (web/src/sw.ts). This module imports NO workbox/webworker globals, so
// a vitest file can import it under happy-dom WITHOUT executing the SW shell
// (precacheAndRoute / self.skipWaiting / self.addEventListener would throw).

export interface PushPayload {
  title?: string;
  body?: string;
  url?: string;
  tag?: string;
}

export function buildNotificationOptions(
  payload: PushPayload,
  count?: number,
  lines?: readonly string[],
): NotificationOptions {
  return {
    body: payload.body ?? '',
    icon: '/pwa-192x192.png',
    // Monochrome white-on-transparent SpenDrop "S" so Android renders the
    // brand mark as a status-bar silhouette (regenerate per the note in sw.ts).
    badge: '/badge-96x96.png',
    tag: payload.tag, // undefined => no collapse (today's behavior)
    renotify: (payload.tag ?? '').startsWith('budget'), // true ONLY for over_budget tags
    // `lines` carries the accumulated per-item detail forward so the next
    // same-tag push can prepend its newest item (see applyActivityRollup).
    data: { url: payload.url ?? '/', count, lines },
  } as NotificationOptions;
}

// Cap on the per-item detail lines carried in an activity rollup body. Web push
// has no list/InboxStyle, so the only lever is a single multi-line `body`; we
// keep the newest few items + a "+N more" tail.
const MAX_ROLLUP_LINES = 4;

// Roll-up arithmetic for collapsing activity pushes. `existing` is the count
// carried on the prior same-tag notification (undefined when none yet); each new
// activity push increments it by one.
export function activityCount(existing: number | undefined): number {
  return (existing ?? 0) + 1;
}

// Structural shape of what registration.getNotifications() returns that we read.
// Kept local + minimal so this module imports no webworker/DOM globals and stays
// importable by vitest. Notification.data is `any`, so Notification[] is assignable.
interface ActivityNotificationLike {
  readonly data?: {
    readonly count?: number;
    readonly lines?: readonly string[];
  } | null;
}

// Given the existing same-tag notifications and an incoming activity payload,
// return the running count, the accumulated per-item lines (newest-first, capped
// at MAX_ROLLUP_LINES), and a payload to show. For a burst (n > 1) the COUNT goes
// in the TITLE and the item lines fill the BODY — so the collapsed first body
// line on Android is the NEWEST item, not the count. For the first add (n === 1)
// the detailed single-event body AND title are returned UNCHANGED. Pure — the SW
// shell supplies `existing` and shows the result.
export function applyActivityRollup(
  existing: ReadonlyArray<ActivityNotificationLike>,
  payload: PushPayload,
): { payload: PushPayload; count: number; lines: readonly string[] } {
  const prior = existing[0]?.data;
  const priorLines = prior?.lines ?? [];
  const count = activityCount(prior?.count);
  // Newest-first so the collapsed first body line is the latest item.
  const lines = [payload.body ?? '', ...priorLines].slice(0, MAX_ROLLUP_LINES);
  if (count === 1) {
    return { payload, count, lines };
  }
  const title = `${count} new activities`;
  // Overflow uses the RUNNING count (not the capped array length) so items that
  // scrolled past the cap are still tallied in the "+N more" tail.
  const overflow = count - lines.length;
  const body = lines.join('\n') + (overflow > 0 ? `\n+${overflow} more` : '');
  return { payload: { ...payload, title, body }, count, lines };
}

// Minimal structural type for the Badging API surface we use. Optional, so a
// WorkerNavigator without setAppBadge (no Badging API) is still assignable and
// the call simply no-ops.
interface BadgeNavigator {
  setAppBadge?: (contents?: number) => Promise<void>;
}

// Feature-detected app-icon badge set. Pure over an injected navigator-like so
// it is unit-testable without the worker global.
export function applyAppBadge(nav: BadgeNavigator, count: number): void {
  if (typeof nav.setAppBadge === 'function') {
    void nav.setAppBadge(count);
  }
}

// Minimal structural surface of ServiceWorkerRegistration that the push render
// path touches. Declared locally (not imported) so this module pulls in no
// webworker globals and stays importable by vitest; ServiceWorkerRegistration is
// structurally assignable to it (Notification.data is `any`).
interface NotificationRegistrationLike {
  getNotifications(filter?: {
    tag?: string;
  }): Promise<ReadonlyArray<ActivityNotificationLike>>;
  showNotification(title: string, options: NotificationOptions): Promise<void>;
}

// Renders one push. Dependencies are injected (the SW shell passes
// self.registration / self.navigator) so the orchestration is unit-testable
// without executing the worker. An `activity` push reads its prior same-tag
// notification, rolls the running count forward, badges the app icon, and shows
// the collapsed "N new activities" row; every other tag (e.g. `digest`,
// `budget*`) is shown verbatim.
//
// #8: the rollup `count` is CLIENT-DERIVED from getNotifications and can
// transiently UNDER-count when several activity pushes interleave between the
// getNotifications read and the showNotification write below — an accepted
// approximation (it lets the server drop a per-recipient counter). It
// self-corrects on the next push, and the 'activity' Topic ("act") narrows the
// race window to same-tag bursts. No behavior change.
//
// Robustness: any rejection in the rollup path (e.g. getNotifications) must NEVER
// drop the user-visible notification. The catch falls back to showing the raw
// payload so activity AND digest pushes still surface.
export async function renderPushNotification(
  registration: NotificationRegistrationLike,
  nav: BadgeNavigator,
  data: PushPayload,
  title: string,
): Promise<void> {
  try {
    let count: number | undefined;
    let lines: readonly string[] | undefined;
    let payload = data;
    if (data.tag === 'activity') {
      const existing = await registration.getNotifications({ tag: 'activity' });
      const rolled = applyActivityRollup(existing, data);
      payload = rolled.payload;
      count = rolled.count;
      lines = rolled.lines;
      // Mirror the running activity count onto the PWA app-icon badge.
      applyAppBadge(nav, count);
    }
    // Show the ROLLED title (e.g. "N new activities" for a burst); falls back to
    // the per-event title arg for the single-event / non-activity paths.
    await registration.showNotification(
      payload.title ?? title,
      buildNotificationOptions(payload, count, lines),
    );
  } catch {
    await registration.showNotification(title, buildNotificationOptions(data));
  }
}
