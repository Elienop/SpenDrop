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
