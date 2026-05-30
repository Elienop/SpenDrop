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
