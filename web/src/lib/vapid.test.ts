import { describe, test, expect } from 'vitest';
import { urlBase64ToUint8Array } from './vapid';

// A real, valid VAPID applicationServerKey (base64url, no padding) — an
// uncompressed P-256 point: 65 bytes, leading 0x04.
const PUBLIC_KEY =
  'BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8M';

describe('urlBase64ToUint8Array', () => {
  test('decodes a VAPID public key to 65 bytes starting with 0x04', () => {
    const bytes = urlBase64ToUint8Array(PUBLIC_KEY);
    expect(bytes).toBeInstanceOf(Uint8Array);
    expect(bytes.length).toBe(65);
    expect(bytes[0]).toBe(0x04);
  });

  test('handles url-safe chars and missing padding', () => {
    // '-' and '_' are the url-safe substitutes for '+' and '/'.
    const bytes = urlBase64ToUint8Array('-_8'); // 0xFB 0xFF
    expect(Array.from(bytes)).toEqual([0xfb, 0xff]);
  });
});
