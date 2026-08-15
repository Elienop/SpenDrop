// Convert a base64url-encoded VAPID public key into the Uint8Array
// PushManager.subscribe() expects for `applicationServerKey`. Browsers do not
// accept base64 directly — this restores standard base64 padding/charset and
// decodes via atob. Adapted from the standard web-push reference snippet.
export function urlBase64ToUint8Array(
  base64String: string,
): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
  // String patterns (not regexes) with replaceAll: same substitution, no
  // pattern to compile, and no /g flag to forget.
  const base64 = (base64String + padding)
    .replaceAll('-', '+')
    .replaceAll('_', '/');
  const rawData = atob(base64);
  // Back the view with an explicit ArrayBuffer (not ArrayBufferLike) so the
  // result satisfies BufferSource for PushManager.subscribe's
  // applicationServerKey under TS's stricter lib.dom typings.
  const outputArray = new Uint8Array(new ArrayBuffer(rawData.length));
  for (let i = 0; i < rawData.length; i++) {
    // atob returns a binary string: every code unit is a byte in 0x00–0xFF, so
    // there are no surrogate pairs and codePointAt is byte-for-byte identical
    // to charCodeAt here. The `?? 0` is unreachable — `i < rawData.length`
    // means the index is always in range, and codePointAt only returns
    // undefined out of range — it exists to satisfy `number | undefined`
    // without a non-null assertion.
    outputArray[i] = rawData.codePointAt(i) ?? 0;
  }
  return outputArray;
}
