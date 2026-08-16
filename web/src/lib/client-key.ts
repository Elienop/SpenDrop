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
 * 122 random bits, so it is the insecure-context path. That asymmetry is in
 * the Web Cryptography API's own IDL, not an implementation quirk:
 *
 *     [Exposed=(Window,Worker)]
 *     interface Crypto {
 *       [SecureContext] readonly attribute SubtleCrypto subtle;
 *       ArrayBufferView getRandomValues(ArrayBufferView array);
 *       [SecureContext] DOMString randomUUID();
 *     };
 *
 * CONTRACT: this function either returns a cryptographically random v4 UUID or
 * it throws. It has no low-entropy fallback, and must never grow one.
 *
 * The `Math.random` arm this replaced (SonarQube S2245) looked like belt and
 * braces and was the opposite. Deleting it without replacing the failure
 * behaviour is worse still: `bytes` would stay zero-filled and every caller,
 * on every device, would get the byte-identical UUID
 * `00000000-0000-4000-8000-000000000000` — a perfectly well-formed key that the
 * server reads as "I have seen this create already", so it would hand back the
 * first row ever saved and silently swallow every real transaction after it.
 * A weak idempotency key does not merely weaken a token; it is indistinguishable
 * from data loss. Refusing to mint is the only failure mode that cannot drop a
 * row.
 *
 * That refusal is unreachable in a browser — `crypto` is exposed on every
 * Window and Worker, and `getRandomValues` sits on it unconditionally — so this
 * is a fail-stop for a platform that does not exist yet, placed so the fault
 * surfaces where it is, rather than as a ledger that quietly stops growing.
 *
 * @throws {Error} if the platform can supply no cryptographic random bytes.
 */
export function newClientKey(): string {
  const webCrypto: Crypto | undefined = globalThis.crypto;
  if (typeof webCrypto?.randomUUID === 'function') {
    return webCrypto.randomUUID();
  }

  if (typeof webCrypto?.getRandomValues !== 'function') {
    // Phrased so the assertion that pins it cannot be satisfied by the
    // TypeError a deleted guard would raise from the very next line.
    throw new Error(
      'newClientKey: no cryptographic random source — neither crypto.randomUUID ' +
        'nor crypto.getRandomValues is available, so no transaction idempotency ' +
        'key can be minted.',
    );
  }

  const bytes = new Uint8Array(16);
  webCrypto.getRandomValues(bytes);
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
