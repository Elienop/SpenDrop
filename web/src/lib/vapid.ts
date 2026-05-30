// Convert a base64url-encoded VAPID public key into the Uint8Array
// PushManager.subscribe() expects for `applicationServerKey`. Browsers do not
// accept base64 directly — this restores standard base64 padding/charset and
// decodes via atob. Adapted from the standard web-push reference snippet.
export function urlBase64ToUint8Array(
  base64String: string,
): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
  const rawData = atob(base64);
  // Back the view with an explicit ArrayBuffer (not ArrayBufferLike) so the
  // result satisfies BufferSource for PushManager.subscribe's
  // applicationServerKey under TS's stricter lib.dom typings.
  const outputArray = new Uint8Array(new ArrayBuffer(rawData.length));
  for (let i = 0; i < rawData.length; i++) {
    outputArray[i] = rawData.charCodeAt(i);
  }
  return outputArray;
}
