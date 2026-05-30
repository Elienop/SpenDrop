import { describe, test, expect, afterEach, vi } from 'vitest';
import { getReadyRegistration } from './push-sw';

// Snapshot the test-setup serviceWorker stub so each test can restore it.
const originalSW = navigator.serviceWorker;

function defineServiceWorker(value: unknown): void {
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value,
  });
}

afterEach(() => {
  defineServiceWorker(originalSW);
  vi.useRealTimers();
});

describe('getReadyRegistration', () => {
  test('returns the active registration from getRegistration without awaiting ready', async () => {
    const active = { active: {} } as unknown as ServiceWorkerRegistration;
    // `ready` is a never-resolving promise to prove we don't depend on it.
    defineServiceWorker({
      ready: new Promise(() => {}),
      getRegistration: vi.fn(async () => active),
    });

    await expect(getReadyRegistration()).resolves.toBe(active);
  });

  test('resolves null within the timeout when ready never resolves and no registration exists', async () => {
    defineServiceWorker({
      ready: new Promise(() => {}), // never resolves — simulates a failed SW register
      getRegistration: vi.fn(async () => undefined),
    });

    vi.useFakeTimers();
    const promise = getReadyRegistration(2000);
    // Let the getRegistration() microtask settle, then fire the timeout.
    await vi.advanceTimersByTimeAsync(2000);
    await expect(promise).resolves.toBeNull();
  });

  test('returns null when serviceWorker is unsupported', async () => {
    // @ts-expect-error deliberately removing serviceWorker for the unsupported branch
    delete navigator.serviceWorker;
    await expect(getReadyRegistration()).resolves.toBeNull();
  });
});
