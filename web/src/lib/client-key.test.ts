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

  test('still mints a key when there is no crypto global at all', () => {
    vi.stubGlobal('crypto', undefined);

    expect(newClientKey()).toMatch(UUID_V4);
    expect(newClientKey()).not.toBe(newClientKey());
  });
});
