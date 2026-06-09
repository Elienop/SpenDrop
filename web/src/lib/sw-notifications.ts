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
): NotificationOptions {
  return {
    body: payload.body ?? '',
    icon: '/pwa-192x192.png',
    // Monochrome white-on-transparent SpenDrop "S" so Android renders the
    // brand mark as a status-bar silhouette (regenerate per the note in sw.ts).
    badge: '/badge-96x96.png',
    tag: payload.tag, // undefined => no collapse (today's behavior)
    renotify: (payload.tag ?? '').startsWith('budget'), // true ONLY for over_budget tags
    data: { url: payload.url ?? '/', count },
  } as NotificationOptions;
}

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
  readonly data?: { readonly count?: number } | null;
}

// Given the existing same-tag notifications and an incoming activity payload,
// return the running count and a payload whose body reads "N new activities"
// for a burst (n > 1), or keeps the detailed single-event body for the first
// add (n === 1). Pure — the SW shell supplies `existing` and shows the result.
export function applyActivityRollup(
  existing: ReadonlyArray<ActivityNotificationLike>,
  payload: PushPayload,
): { payload: PushPayload; count: number } {
  const n = activityCount(existing[0]?.data?.count);
  const body = n > 1 ? `${n} new activities` : payload.body;
  return { payload: { ...payload, body }, count: n };
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
    let payload = data;
    if (data.tag === 'activity') {
      const existing = await registration.getNotifications({ tag: 'activity' });
      const rolled = applyActivityRollup(existing, data);
      payload = rolled.payload;
      count = rolled.count;
      // Mirror the running activity count onto the PWA app-icon badge.
      applyAppBadge(nav, count);
    }
    await registration.showNotification(
      title,
      buildNotificationOptions(payload, count),
    );
  } catch {
    await registration.showNotification(title, buildNotificationOptions(data));
  }
}
