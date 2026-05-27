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
