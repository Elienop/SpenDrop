// Pure, side-effect-free notification render helpers shared with the service
// worker (web/src/sw.ts). This module imports NO workbox/webworker globals, so
// a vitest file can import it under happy-dom WITHOUT executing the SW shell
// (precacheAndRoute / self.skipWaiting / self.addEventListener would throw).

export interface PushPayload {
  title?: string;
  body?: string;
  url?: string;
}

export function buildNotificationOptions(
  payload: PushPayload,
): NotificationOptions {
  return {
    body: payload.body ?? '',
    icon: '/pwa-192x192.png',
    // Monochrome white-on-transparent SpenDrop "S" so Android renders the
    // brand mark as a status-bar silhouette (regenerate per the note in sw.ts).
    badge: '/badge-96x96.png',
    data: { url: payload.url ?? '/' },
  };
}
