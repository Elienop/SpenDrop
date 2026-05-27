/// <reference types="vitest/config" />
import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      // Emit an external, same-origin registerSW.js (<script src>) instead of an
      // inline snippet — the Go server's CSP is `script-src 'self'` with no
      // 'unsafe-inline', so an inline registration would be blocked.
      injectRegister: 'script',
      manifest: {
        // Pin install identity to the app root so it stays stable even as
        // start_url (/quick) evolves on this feature branch — otherwise the
        // install id would default to the provisional start_url.
        id: '/',
        name: 'SpenDrop',
        short_name: 'SpenDrop',
        description: 'Self-hosted household expense tracker — log spending and watch your budgets.',
        display: 'standalone',
        start_url: '/quick',
        scope: '/',
        // Matches the dark theme `--background: 240 3.7% 7.1%` in src/globals.css.
        theme_color: '#111113',
        background_color: '#111113',
        icons: [
          { src: '/pwa-192x192.png', sizes: '192x192', type: 'image/png' },
          { src: '/pwa-512x512.png', sizes: '512x512', type: 'image/png' },
          {
            src: '/maskable-512x512.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'maskable',
          },
        ],
      },
      workbox: {
        // Precache the built shell/assets (plugin defaults handle globbing).
        navigateFallback: 'index.html',
        // Never let the SW shadow API or health routes with the SPA shell.
        navigateFallbackDenylist: [/^\/api/, /^\/healthz/],
        // Runtime-cache ONLY the two read-only reference lists the /quick
        // capture screen needs to render offline: categories and currencies.
        // Both change rarely, so StaleWhileRevalidate serves the last-known
        // list instantly and refreshes it in the background on the next online
        // load. Every other /api GET (dashboard, reports, transactions) is
        // deliberately left uncached so those views never show stale figures —
        // unmatched routes fall through to the network. Workbox runtimeCaching
        // only intercepts GET by default, so POST/PUT/DELETE to these paths
        // (e.g. creating a category) are untouched.
        runtimeCaching: [
          {
            urlPattern: /\/api\/(categories|currencies)$/,
            handler: 'StaleWhileRevalidate',
            options: {
              cacheName: 'spendrop-api-lists',
              // 8 entries is ample headroom for 2 endpoints; the 7-day TTL
              // bounds how stale an offline list can get before it is evicted.
              expiration: { maxEntries: 8, maxAgeSeconds: 60 * 60 * 24 * 7 },
              cacheableResponse: { statuses: [200] },
            },
          },
        ],
      },
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  test: {
    globals: true,
    environment: 'happy-dom',
    setupFiles: ['./src/test/setup.ts'],
    css: {
      modules: {
        classNameStrategy: 'non-scoped',
      },
    },
  },
})
