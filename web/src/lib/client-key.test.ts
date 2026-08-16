import { describe, test, expect, afterEach, vi } from 'vitest';
import { newClientKey } from './client-key';

// RFC 4122 v4: version nibble '4', variant nibble one of 8/9/a/b.
const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('newClientKey', () => {
  test('mints a v4 UUID that fits the server cap of 64 characters', () => {
    const key = newClientKey();
    expect(key).toMatch(UUID_V4);
    expect(key.length).toBe(36);
  });

  test('never repeats a key', () => {
    const keys = new Set(Array.from({ length: 500 }, () => newClientKey()));
    expect(keys.size).toBe(500);
  });

  // crypto.randomUUID is secure-context only, so it is missing whenever the
  // app is opened over plain http on a LAN address. Every transaction create
  // mints a key, so an unguarded call there would make saving fail outright.
  test('still mints a key when randomUUID is unavailable (insecure context)', () => {
    vi.stubGlobal('crypto', {
      getRandomValues: (buf: Uint8Array) => {
        for (let i = 0; i < buf.length; i++) buf[i] = i * 7;
        return buf;
      },
    });

    const key = newClientKey();
    expect(key).toMatch(UUID_V4);
  });

  // The two tests below pin a REFUSAL, and the refusal is the safe behaviour.
  //
  // This function used to fall back to `Math.random` when no crypto global was
  // present (SonarQube S2245). Simply deleting that loop is a worse bug than
  // keeping it: `bytes` would stay zero-filled, the version/variant nibbles
  // would still be stamped in, and every caller on every device would receive
  // the byte-identical, perfectly well-formed key below. The server treats a
  // seen `client_key` as "already created", so it would return the first row
  // ever saved instead of writing the new one — real transactions vanishing,
  // with a success toast. There is no such thing as a safe weak idempotency
  // key, so the contract is: mint cryptographically, or throw.
  //
  // Both assertions match a phrase only OUR message carries. A guard deleted
  // from `newClientKey` still throws here — a TypeError from dereferencing the
  // missing function on the next line — so matching on `/getRandomValues/`
  // alone would keep passing with the guard gone.
  const ZERO_FILLED_KEY = '00000000-0000-4000-8000-000000000000';

  test('throws instead of minting a weak key when there is no crypto global', () => {
    vi.stubGlobal('crypto', undefined);

    expect(() => newClientKey()).toThrow(/no cryptographic random source/);
  });

  test('throws when crypto exists but cannot supply random bytes', () => {
    // A `crypto` without `getRandomValues` — the shape the fallback existed
    // for, reached without removing the global itself.
    vi.stubGlobal('crypto', {});

    expect(() => newClientKey()).toThrow(/no cryptographic random source/);
  });

  test('the healthy path never yields the all-zero key', () => {
    // Positive control for the pair above: with a real crypto present the
    // function returns rather than throws, so those tests are pinning the
    // failure branch and not a function that throws unconditionally.
    const keys = Array.from({ length: 100 }, () => newClientKey());
    expect(keys).not.toContain(ZERO_FILLED_KEY);
    expect(keys.every((k) => UUID_V4.test(k))).toBe(true);
  });
});
