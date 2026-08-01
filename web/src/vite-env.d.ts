/// <reference types="vite/client" />

/**
 * Typed access to this app's build-time `VITE_*` environment.
 *
 * `vite/client` declares `ImportMetaEnv` with an `[key: string]: any` index
 * signature, so every `import.meta.env.X` read is `any` unless the key is
 * declared — which is why the older call sites carry an
 * `as string | undefined` cast. Declaring the keys here types them at the
 * source instead, so a reader cannot silently treat a string as something
 * else and no cast is needed.
 *
 * Every key is optional on purpose: a `VITE_*` variable that was not passed
 * to the build is simply absent from the baked object, so the type has to
 * force each reader to handle its absence.
 */
interface ImportMetaEnv {
  /**
   * The released version this bundle was built from, e.g. `v0.35.0`. Stamped
   * by the Docker/CI build; ABSENT in a local dev build. Do not read this
   * directly — `@/lib/app-version` owns the fallback.
   */
  readonly VITE_APP_VERSION?: string;
  /**
   * Absolute API origin, for dev setups and reverse-proxied hosts where the
   * frontend is not served by the Go binary. Defaults to the relative `/api`.
   */
  readonly VITE_API_BASE_URL?: string;
}
