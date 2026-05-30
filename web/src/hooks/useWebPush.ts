import { useState, useEffect, useCallback, useRef } from 'react';
import { api } from '@/api/client';
import { urlBase64ToUint8Array } from '@/lib/vapid';

interface VapidKeyResponse {
  publicKey: string;
}

interface PushSubscriptionJSONShape {
  endpoint: string;
  keys: { p256dh: string; auth: string };
}

export interface UseWebPush {
  supported: boolean;
  permission: NotificationPermission;
  subscribed: boolean;
  busy: boolean;
  enable(): Promise<void>;
  disable(): Promise<void>;
  sendTest(): Promise<void>;
}

function pushSupported(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    'serviceWorker' in navigator &&
    typeof window !== 'undefined' &&
    'PushManager' in window &&
    'Notification' in window
  );
}

export function useWebPush(): UseWebPush {
  const supported = pushSupported();
  const [permission, setPermission] = useState<NotificationPermission>(() =>
    supported ? Notification.permission : 'denied',
  );
  const [subscribed, setSubscribed] = useState(false);
  const [busy, setBusy] = useState(false);
  // StrictMode double-invokes effects; this guards the initial read so the two
  // mount passes don't race a setState on an unmounted instance.
  const mountedRef = useRef(true);

  // Initial state read: reflect any pre-existing subscription. Effects (not
  // render) touch navigator so the render stays pure and StrictMode-safe.
  useEffect(() => {
    mountedRef.current = true;
    if (!supported) return;
    setPermission(Notification.permission);
    void navigator.serviceWorker.ready.then(async (reg) => {
      const existing = await reg.pushManager.getSubscription();
      if (mountedRef.current) setSubscribed(existing !== null);
      // Reconcile: a browser can rotate the endpoint while the app was closed,
      // and pushsubscriptionchange may not have fired (or its re-POST failed).
      // Re-POST the current subscription so the server row matches the browser.
      // Idempotent server-side (UpsertPushSubscription ON CONFLICT(endpoint)).
      if (existing && Notification.permission === 'granted') {
        const json = existing.toJSON() as PushSubscriptionJSONShape;
        try {
          await api.post('push/subscriptions', {
            endpoint: json.endpoint,
            keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
          });
        } catch {
          // Best-effort; the explicit enable() path re-registers on next toggle.
        }
      }
    });
    return () => {
      mountedRef.current = false;
    };
  }, [supported]);

  const enable = useCallback(async () => {
    if (!supported || busy) return;
    setBusy(true);
    try {
      let perm = Notification.permission;
      if (perm === 'default') {
        perm = await Notification.requestPermission();
        setPermission(perm);
      }
      if (perm !== 'granted') {
        // Denied (or dismissed) — never POST; the UI surfaces a hint instead.
        setPermission(perm);
        return;
      }
      const reg = await navigator.serviceWorker.ready;
      const { publicKey } = await api.get<VapidKeyResponse>(
        'push/vapid-public-key',
      );
      let sub = await reg.pushManager.getSubscription();
      if (!sub) {
        sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(publicKey),
        });
      }
      const json = sub.toJSON() as PushSubscriptionJSONShape;
      await api.post('push/subscriptions', {
        endpoint: json.endpoint,
        keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
      });
      if (mountedRef.current) setSubscribed(true);
    } finally {
      if (mountedRef.current) setBusy(false);
    }
  }, [supported, busy]);

  const disable = useCallback(async () => {
    if (!supported || busy) return;
    setBusy(true);
    try {
      const reg = await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.getSubscription();
      if (sub) {
        const endpoint = sub.endpoint;
        await sub.unsubscribe();
        await api.del('push/subscriptions', { endpoint });
      }
      if (mountedRef.current) setSubscribed(false);
    } finally {
      if (mountedRef.current) setBusy(false);
    }
  }, [supported, busy]);

  const sendTest = useCallback(async () => {
    if (!supported || busy) return;
    setBusy(true);
    try {
      await api.post('push/test');
    } finally {
      if (mountedRef.current) setBusy(false);
    }
  }, [supported, busy]);

  return { supported, permission, subscribed, busy, enable, disable, sendTest };
}
