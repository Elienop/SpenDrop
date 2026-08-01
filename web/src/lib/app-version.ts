/**
 * What build am I actually running?
 *
 * The installed PWA answers that question badly on its own: the service
 * worker serves the previously-cached shell for one launch after a deploy, so
 * "I updated the server" and "my phone is running the new code" are different
 * facts that used to be indistinguishable from the UI. This module is the
 * single source of the first fact; `useServerVersion` fetches the second.
 */

/**
 * What we display when the build did not stamp a version. Exactly `dev` —
 * never an empty string, never `undefined` — because this string is rendered
 * verbatim and a blank version stamp reads as a broken page, not as a dev
 * build.
 */
export const DEV_VERSION = 'dev';

/**
 * The version this bundle was built from (e.g. `v0.35.0`), or `dev`.
 *
 * Read once at module load: Vite bakes `import.meta.env.VITE_APP_VERSION` in
 * as a literal at build time, so it cannot change while the app runs.
 *
 * Trim-then-`||`, not `??`, and the two halves fix different bugs.
 *
 * `??` only catches `null`/`undefined`. A build arg that is *declared but
 * empty* — `VITE_APP_VERSION=` in the environment, which is what a CI job
 * with an unset variable produces — bakes in as `''` and would sail straight
 * through to render a blank stamp.
 *
 * The trim keeps this in step with the SERVER's answer. `version.Version()`
 * (internal/version/version.go) does `strings.TrimSpace` before its own
 * fallback, so a whitespace-only stamp makes the server report `dev`. Without
 * the trim here the bundle would instead report the whitespace verbatim —
 * rendering "SpenDrop " with a blank where the version goes AND, because
 * `"   " !== "dev"`, claiming a permanent phantom "dev available". Two sides
 * comparing strings have to normalise them the same way or the comparison
 * invents differences.
 *
 * Absent, blank, and whitespace all mean the same thing: nobody told us.
 */
export const BUNDLE_VERSION: string =
  (import.meta.env.VITE_APP_VERSION ?? '').trim() || DEV_VERSION;

/**
 * Whether the running bundle is out of step with the server.
 *
 * Deliberately a plain inequality rather than a semver comparison:
 *
 *  - it needs no dependency and no parsing, so it cannot throw on a version
 *    string neither side anticipated (a tag with a suffix, a commit SHA);
 *  - the user-facing remedy is the same in both directions. After a rollback
 *    the server is *older* than the cached bundle, and reloading is still
 *    exactly what re-syncs the two.
 *
 * Returns false — no nagging — in the two cases where a mismatch means
 * nothing:
 *
 *  - `server` is unknown (offline, a 401, or a server too old to have the
 *    endpoint). Not knowing is not the same as being stale.
 *  - the bundle is a dev build. Its version was never stamped, so it differs
 *    from every server version by construction and would sit permanently
 *    accusing the developer of being out of date.
 */
export function isBundleStale(
  bundle: string,
  server: string | null | undefined,
): boolean {
  if (!server) return false;
  if (bundle === DEV_VERSION) return false;
  return server !== bundle;
}
