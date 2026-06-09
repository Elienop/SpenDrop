/// <reference lib="webworker" />
import {
  precacheAndRoute,
  cleanupOutdatedCaches,
  createHandlerBoundToURL,
} from 'workbox-precaching';
import { registerRoute, NavigationRoute } from 'workbox-routing';
import { StaleWhileRevalidate } from 'workbox-strategies';
import { CacheableResponsePlugin } from 'workbox-cacheable-response';
import { ExpirationPlugin } from 'workbox-expiration';
import { urlBase64ToUint8Array } from './lib/vapid';
import { buildNotificationOptions, type PushPayload } from './lib/sw-notifications';

declare const self: ServiceWorkerGlobalScope & { __WB_MANIFEST: Array<unknown> };

// Precache the built shell/assets the plugin injected at build time.
precacheAndRoute(self.__WB_MANIFEST);
cleanupOutdatedCaches();

// SPA shell fallback for navigations — serve the precached index.html (the old
// generateSW `navigateFallback: 'index.html'`), but never shadow API or health
// routes with it (those must hit the network / 404 honestly).
registerRoute(
  new NavigationRoute(createHandlerBoundToURL('index.html'), {
    denylist: [/^\/api\//, /^\/healthz\//],
  }),
);

// Runtime-cache ONLY the two read-only reference lists the /quick capture
// screen needs offline: categories and currencies. StaleWhileRevalidate serves
// the last-known list instantly and refreshes in the background. Every other
// /api GET falls through to the network so views never show stale figures.
registerRoute(
  ({ url, request }) =>
    request.method === 'GET' &&
    /\/api\/(categories|currencies)$/.test(url.pathname),
  new StaleWhileRevalidate({
    cacheName: 'spendrop-api-lists',
    plugins: [
      new CacheableResponsePlugin({ statuses: [200] }),
      new ExpirationPlugin({ maxEntries: 8, maxAgeSeconds: 60 * 60 * 24 * 7 }),
    ],
  }),
);

// Take over an open tab immediately on activate so the new SW (and, later, its
// push handlers) is in control without requiring a manual reload.
self.skipWaiting();
self.addEventListener('activate', () => {
  void self.clients.claim();
});

// --- Web Push -----------------------------------------------------------------

// PushSubscriptionChangeEvent is not in the default TS DOM lib for all targets;
// declare a minimal local interface so the handler typechecks.
interface PushSubscriptionChangeEvent extends ExtendableEvent {
  readonly oldSubscription: PushSubscription | null;
  readonly newSubscription: PushSubscription | null;
}

self.addEventListener('push', (event) => {
  let data: PushPayload = {};
  if (event.data) {
    try {
      data = event.data.json() as PushPayload;
    } catch {
      data = { body: event.data.text() };
    }
  }
  const title = data.title ?? 'SpenDrop';
  event.waitUntil(
    self.registration.showNotification(title, buildNotificationOptions(data)),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const target =
    (event.notification.data as { url?: string } | undefined)?.url ?? '/';
  event.waitUntil(
    (async () => {
      const all = await self.clients.matchAll({
        type: 'window',
        includeUncontrolled: true,
      });
      // Focus an existing SpenDrop tab if one is open; otherwise open a new one.
      for (const client of all) {
        if ('focus' in client) {
          await client.focus();
          if ('navigate' in client && client.url !== target) {
            try {
              await client.navigate(target);
            } catch {
              // Cross-origin navigate is rejected; the focus alone is enough.
            }
          }
          return;
        }
      }
      await self.clients.openWindow(target);
    })(),
  );
});

// The browser can rotate a subscription's endpoint at any time. Re-subscribe
// with the SAME applicationServerKey (read off the expiring subscription so we
// don't need network for the key), falling back to fetching the server's
// current VAPID key, then re-register the new subscription server-side.
self.addEventListener('pushsubscriptionchange', (event) => {
  const e = event as PushSubscriptionChangeEvent;
  event.waitUntil(
    (async () => {
      let applicationServerKey: ArrayBuffer | null =
        e.oldSubscription?.options.applicationServerKey ?? null;
      if (!applicationServerKey) {
        try {
          const res = await fetch('/api/push/vapid-public-key', {
            credentials: 'include',
          });
          if (res.ok) {
            const { publicKey } = (await res.json()) as { publicKey: string };
            applicationServerKey = urlBase64ToUint8Array(publicKey).buffer;
          }
        } catch {
          return; // No key obtainable; nothing we can do this cycle.
        }
      }
      if (!applicationServerKey) return;
      const sub = await self.registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey,
      });
      const json = sub.toJSON() as {
        endpoint: string;
        keys: { p256dh: string; auth: string };
      };
      await fetch('/api/push/subscriptions', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          endpoint: json.endpoint,
          keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
        }),
      });
    })(),
  );
});
