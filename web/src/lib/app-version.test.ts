import { describe, it, expect, afterEach, vi } from 'vitest';
import { BUNDLE_VERSION, DEV_VERSION, isBundleStale } from './app-version';

/**
 * Re-import the module with a given `VITE_APP_VERSION` baked in.
 *
 * `BUNDLE_VERSION` is resolved once at module load, so the stub has to be in
 * place BEFORE the import — hence `resetModules()` plus a dynamic import
 * rather than the static one at the top of this file.
 */
async function bundleVersionWith(value: string): Promise<string> {
  vi.stubEnv('VITE_APP_VERSION', value);
  vi.resetModules();
  const mod = await import('./app-version');
  return mod.BUNDLE_VERSION;
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe('BUNDLE_VERSION', () => {
  it("falls back to exactly 'dev' when the build stamped no version", () => {
    // No `.env` file and no stamp in the test environment, so this is the
    // real unstamped path, not a simulated one.
    expect(BUNDLE_VERSION).toBe('dev');
    expect(DEV_VERSION).toBe('dev');
  });

  it('uses the stamped version when the build provides one', async () => {
    await expect(bundleVersionWith('v0.35.0')).resolves.toBe('v0.35.0');
  });

  it("falls back to 'dev' for a declared-but-empty build arg", async () => {
    // A CI job with an unset variable bakes in `''`. `??` would pass that
    // straight through and render a blank stamp; `||` is what stops it.
    await expect(bundleVersionWith('')).resolves.toBe('dev');
  });

  it("falls back to 'dev' for a whitespace-only build arg", async () => {
    // Parity with the server: `version.Version()` TrimSpaces before its own
    // fallback, so a whitespace stamp makes the SERVER report `dev`. If the
    // bundle kept the whitespace verbatim the two could never compare equal,
    // and the stamp would claim a permanent phantom "dev available".
    await expect(bundleVersionWith('   ')).resolves.toBe('dev');
    await expect(bundleVersionWith('\t\n')).resolves.toBe('dev');
  });

  it('trims a padded stamp rather than rendering the padding', async () => {
    await expect(bundleVersionWith('  v0.35.0  ')).resolves.toBe('v0.35.0');
  });
});

describe('isBundleStale', () => {
  it('is true when the server reports a different version', () => {
    expect(isBundleStale('v0.34.0', 'v0.35.0')).toBe(true);
  });

  it('is false when the versions match', () => {
    expect(isBundleStale('v0.35.0', 'v0.35.0')).toBe(false);
  });

  it('is false when the server version is unknown', () => {
    // Offline, 401, or a server too old to have the endpoint. Not knowing is
    // not the same as being stale.
    expect(isBundleStale('v0.35.0', null)).toBe(false);
    expect(isBundleStale('v0.35.0', undefined)).toBe(false);
    expect(isBundleStale('v0.35.0', '')).toBe(false);
  });

  it('is false for a dev bundle, whatever the server reports', () => {
    // A dev build differs from every release by construction; nagging about
    // it would make the state meaningless on the one machine that can't act
    // on it.
    expect(isBundleStale('dev', 'v0.35.0')).toBe(false);
  });

  it('is true after a rollback, when the server is the OLDER side', () => {
    // Direction-agnostic on purpose: reloading is the remedy either way.
    expect(isBundleStale('v0.35.0', 'v0.34.0')).toBe(true);
  });
});
