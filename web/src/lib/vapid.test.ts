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

  // Byte-for-byte pin. The literal below is the independent decode of
  // PUBLIC_KEY (`Buffer.from(key, 'base64url')`), so any change to the
  // padding / charset / code-unit half of the conversion shows up as a
  // concrete byte diff rather than a length that still looks plausible.
  test('decodes the whole key to the exact expected bytes', () => {
    expect(Array.from(urlBase64ToUint8Array(PUBLIC_KEY))).toEqual([
      4, 73, 122, 218, 37, 24, 129, 72, 175, 196, 137, 47, 235, 220, 149, 136,
      75, 162, 4, 134, 190, 33, 191, 126, 74, 75, 204, 120, 11, 64, 220, 177,
      96, 15, 57, 43, 197, 146, 99, 74, 4, 167, 125, 201, 35, 4, 155, 129, 146,
      189, 234, 5, 70, 8, 28, 20, 5, 45, 118, 41, 228, 217, 44, 135, 195,
    ]);
  });

  test('keeps bytes at and above 0x80 intact', () => {
    // 'AH-A__4' is base64url for 00 7F 80 FF FE — the high half of the byte
    // range, where a 7-bit-safe read (or a charset slip on '-' / '_') would
    // corrupt the value instead of shortening the array.
    expect(Array.from(urlBase64ToUint8Array('AH-A__4'))).toEqual([
      0x00, 0x7f, 0x80, 0xff, 0xfe,
    ]);
  });
});
