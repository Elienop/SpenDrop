import '@testing-library/jest-dom/vitest';
import { vi } from 'vitest';

// --- Web Push test doubles (happy-dom ships none of these) -------------------
// Mutable so individual tests can flip permission / pre-seed a subscription.
export const pushTestState: {
  permission: NotificationPermission;
  subscription: PushSubscription | null;
} = { permission: 'default', subscription: null };

export function makeSubscription(
  endpoint = 'https://push.example/abc',
): PushSubscription {
  return {
    endpoint,
    options: {
      applicationServerKey: new Uint8Array([4, 1, 2]).buffer,
    } as PushSubscriptionOptions,
    toJSON: () => ({
      endpoint,
      keys: { p256dh: 'p256dh-test', auth: 'auth-test' },
    }),
    unsubscribe: vi.fn().mockResolvedValue(true),
  } as unknown as PushSubscription;
}

const pushManager = {
  getSubscription: vi.fn(async () => pushTestState.subscription),
  subscribe: vi.fn(async () => {
    pushTestState.subscription = makeSubscription();
    return pushTestState.subscription;
  }),
};

const registration = {
  pushManager,
  showNotification: vi.fn(async () => undefined),
  // getReadyRegistration() prefers getRegistration() (which resolves to a value
  // or undefined, never pending) over awaiting `ready`. Expose an active
  // registration so the helper's primary path is exercised in hook tests.
  active: {} as ServiceWorker,
};

Object.defineProperty(navigator, 'serviceWorker', {
  configurable: true,
  value: {
    ready: Promise.resolve(registration as unknown as ServiceWorkerRegistration),
    register: vi.fn(async () => registration),
    getRegistration: vi.fn(
      async () => registration as unknown as ServiceWorkerRegistration,
    ),
    controller: {} as ServiceWorker,
    addEventListener: vi.fn(),
  },
});

class MockNotification {
  static get permission(): NotificationPermission {
    return pushTestState.permission;
  }
  static requestPermission = vi.fn(
    async (): Promise<NotificationPermission> => {
      pushTestState.permission = 'granted';
      return pushTestState.permission;
    },
  );
}
Object.defineProperty(window, 'Notification', {
  configurable: true,
  value: MockNotification,
});

// PushManager must exist on window for the hook's feature-detect.
Object.defineProperty(window, 'PushManager', {
  configurable: true,
  value: function PushManager() {},
});
