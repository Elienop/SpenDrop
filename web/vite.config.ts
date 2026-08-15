/// <reference types="vitest/config" />
import path from 'node:path'
import { isAgent } from 'std-env'
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
      // We hand-author the service worker (src/sw.ts) so we can add Web Push
      // handlers alongside the Workbox precache/runtime-cache logic. The plugin
      // only injects the precache manifest (self.__WB_MANIFEST) at build time.
      strategies: 'injectManifest',
      srcDir: 'src',
      filename: 'sw.ts',
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
      // injectManifest controls which built files are precached. Mirror the old
      // generateSW defaults; the SW itself wires navigateFallback + runtime
      // caching in src/sw.ts.
      injectManifest: {
        globPatterns: ['**/*.{js,css,html,svg,png,ico,woff2}'],
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
    // A NEGATIVE-OFFSET zone, on purpose. The date tests exist to catch
    // `new Date('2026-01-01')` (UTC midnight) being used where a local-time
    // parse is required — and that bug is INVISIBLE anywhere at or east of
    // GMT. The dev host runs EEST and CI runners run UTC, so without this the
    // assertions pass no matter which parse the code uses.
    env: { TZ: 'America/Los_Angeles' },
    setupFiles: ['./src/test/setup.ts'],
    // Every run also writes SonarQube's Generic Test Execution XML (test count,
    // pass/fail, duration — the "Unit Tests" figures; coverage below is the
    // other channel) to coverage/, which is gitignored. Setting `reporters`
    // replaces vitest's defaults, so the first entries re-create them exactly
    // as vitest picks them (`agent` under an AI agent, else `default`, plus
    // `github-actions` in CI for PR annotations) — see vitest's resolveConfig.
    reporters: [
      isAgent ? 'agent' : 'default',
      ...(process.env.GITHUB_ACTIONS === 'true' ? (['github-actions'] as const) : []),
      [
        'vitest-sonar-reporter',
        {
          outputFile: 'coverage/sonar-report.xml',
          silent: true,
          // The reporter emits cwd-relative paths; the scanner's base dir is
          // the repo root, so rewrite to `web/src/...` whatever the cwd.
          onWritePath: (p: string) =>
            path.relative(path.resolve(__dirname, '..'), path.resolve(p)),
        },
      ],
    ],
    // `vitest run --coverage` (see `make coverage` at the repo root) writes
    // coverage/lcov.info for SonarQube (sonar.javascript.lcov.reportPaths).
    // Only application source counts; tests and the bundle entry points are
    // not coverable code. components/ui IS counted: half of it is forked.
    coverage: {
      provider: 'v8',
      // lcov paths are written relative to the REPO root (`web/src/...`),
      // which is the scanner's base dir, so every SF: line resolves.
      // `lcovonly`, not `lcov`: the latter also emits an HTML report whose
      // bundled scripts eslint would then lint.
      reporter: [
        'text-summary',
        ['lcovonly', { projectRoot: path.resolve(__dirname, '..') }],
      ],
      reportsDirectory: './coverage',
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.{ts,tsx}',
        'src/**/*.d.ts',
        'src/test/**',
        'src/main.tsx',
        'src/sw.ts',
      ],
    },
    css: {
      modules: {
        classNameStrategy: 'non-scoped',
      },
    },
  },
})
