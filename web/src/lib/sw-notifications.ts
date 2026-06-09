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
