/**
 * Mints the idempotency key a single transaction create carries on the wire
 * (`client_key`). The server records the key with the row it created and
 * returns that same row for any later create bearing the same key, so a
 * submission whose response was lost can be re-sent without duplicating.
 *
 * One key per user intent, minted where the payload is built — never where it
 * is sent, or a retry would mint a fresh one and defeat the whole mechanism.
 *
 * `crypto.randomUUID` is gated on a secure context, so it is undefined when
 * the app is reached over plain http on a LAN address (`http://192.168.x.y`)
 * rather than over TLS or via localhost. Every create funnels through here, so
 * an unguarded call would make saving a transaction throw outright on those
 * devices. `crypto.getRandomValues` carries no such gate and yields the same
 * 122 random bits; the final `Math.random` arm exists only so a missing
 * `crypto` global can never be the reason a transaction fails to save.
 */
export function newClientKey(): string {
  const webCrypto: Crypto | undefined = globalThis.crypto;
  if (typeof webCrypto?.randomUUID === 'function') {
    return webCrypto.randomUUID();
  }

  const bytes = new Uint8Array(16);
  if (typeof webCrypto?.getRandomValues === 'function') {
    webCrypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i++) {
      bytes[i] = Math.floor(Math.random() * 256);
    }
  }
  // RFC 4122 §4.4: version 4 in the high nibble of byte 6, variant 10x in byte 8.
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;

  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20),
  ].join('-');
}
