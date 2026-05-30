// Resolve the active ServiceWorkerRegistration without ever hanging.
//
// `navigator.serviceWorker.ready` only resolves once an ACTIVE service worker
// exists. If registration silently failed (wrong MIME type, register() threw)
// while the app otherwise loaded, `ready` never resolves AND never rejects — a
// bare `await navigator.serviceWorker.ready` then stalls forever. On the logout
// teardown path that traps the whole security-critical teardown (setUser(null)
// / navigate never run); on the push toggle it sticks the control in `busy`.
//
// We first try getRegistration(), which resolves to `undefined` (not pending)
// when there is no registration, then fall back to racing `ready` against a
// short timeout so a registered-but-not-yet-active worker still gets a brief
// chance to activate. Either way this resolves within `timeoutMs`.
export async function getReadyRegistration(
  timeoutMs = 2000,
): Promise<ServiceWorkerRegistration | null> {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) {
    return null;
  }
  // getRegistration() resolves to undefined rather than pending when no SW is
  // registered, so a failed/absent registration returns immediately here.
  try {
    const existing = await navigator.serviceWorker.getRegistration();
    if (existing && existing.active) {
      return existing;
    }
  } catch {
    // Fall through to the raced ready below.
  }
  // A registration exists but may not be active yet (or getRegistration is
  // unavailable in this environment). Give `ready` a bounded chance; never let
  // it stall the caller.
  return Promise.race([
    navigator.serviceWorker.ready,
    new Promise<null>((resolve) => setTimeout(() => resolve(null), timeoutMs)),
  ]);
}
