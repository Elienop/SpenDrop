import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

// Spread the original so `ApiError` / `NetworkError` stay real classes rather
// than `undefined` — a bare factory mock turns every `instanceof` check in the
// tree under test into a TypeError.
vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/client')>();
  return { ...actual, api: { get: vi.fn() } };
});

/**
 * The version this "bundle" was built from, per test.
 *
 * `BUNDLE_VERSION` is baked at module load from `import.meta.env`, so the only
 * way to render a RELEASE bundle here is to override the module — a mutable
 * holder behind a getter, because the component reads the binding at render
 * time. `isBundleStale` is deliberately left REAL (spread from the original),
 * so these tests exercise the actual staleness rule rather than a stub of it.
 *
 * The genuine unstamped-env path (`import.meta.env.VITE_APP_VERSION` absent or
 * empty → exactly `dev`) is covered in `lib/app-version.test.ts`, which does
 * not mock the module at all.
 */
const bundleVersion = { current: 'dev' };
vi.mock('@/lib/app-version', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/app-version')>();
  return {
    ...actual,
    get BUNDLE_VERSION() {
      return bundleVersion.current;
    },
  };
});

import { api, ApiError, NetworkError } from '@/api/client';
import { AppVersion } from './AppVersion';

const mockedGet = api.get as unknown as ReturnType<typeof vi.fn>;

function renderAppVersion() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <AppVersion />
    </QueryClientProvider>,
  );
  return { client };
}

/** Block until the version query has actually settled, either way. */
async function settled(client: QueryClient) {
  await waitFor(() => {
    const status = client.getQueryState(['version'])?.status;
    expect(status === 'success' || status === 'error').toBe(true);
  });
}

function stamp() {
  return screen.getByTestId('app-version');
}

describe('AppVersion', () => {
  beforeEach(() => {
    // clearAllMocks does NOT drain a mockResolvedValueOnce queue; reset does.
    mockedGet.mockReset();
    bundleVersion.current = 'dev';
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows `dev` for an unstamped bundle', async () => {
    mockedGet.mockResolvedValue({ version: 'v0.35.0' });
    const { client } = renderAppVersion();

    await settled(client);

    // Split text nodes — `SpenDrop `, the version, and the suffix are
    // separate children — so match on the element's text content, not
    // getByText.
    expect(stamp()).toHaveTextContent('SpenDrop dev');
  });

  it('never claims an update for a dev bundle, whatever the server reports', async () => {
    // A dev build differs from every release by construction. Nagging here
    // would leave the state permanently on and therefore meaningless.
    mockedGet.mockResolvedValue({ version: 'v0.35.0' });
    const { client } = renderAppVersion();

    await settled(client);

    expect(stamp()).not.toHaveTextContent(/available/i);
  });

  it('renders the version plainly when the bundle matches the server', async () => {
    bundleVersion.current = 'v0.35.0';
    mockedGet.mockResolvedValue({ version: 'v0.35.0' });
    const { client } = renderAppVersion();

    await settled(client);

    expect(stamp()).toHaveTextContent('SpenDrop v0.35.0');
    expect(stamp()).not.toHaveTextContent(/available/i);
    // Muted is the "nothing to see here" weight; the stale branch swaps it.
    expect(stamp()).toHaveClass('text-muted-foreground');
  });

  it('names the server version in VISIBLE text when the server has moved on', async () => {
    bundleVersion.current = 'v0.34.0';
    mockedGet.mockResolvedValue({ version: 'v0.35.0' });
    const { client } = renderAppVersion();

    await settled(client);

    await waitFor(() =>
      expect(stamp()).toHaveTextContent('SpenDrop v0.34.0 · v0.35.0 available'),
    );
    // Visually distinguishable, not just differently worded.
    expect(stamp()).toHaveClass('text-foreground');
    expect(stamp()).not.toHaveClass('text-muted-foreground');
  });

  it('carries the stale detail in no `title` attribute at all', async () => {
    // A `title` is dead on touch — this household's primary surface is a
    // phone — as well as unreachable by keyboard and silent to screen
    // readers. The server version must be real text, and there must be no
    // second copy in a tooltip to drift out of sync with it.
    bundleVersion.current = 'v0.34.0';
    mockedGet.mockResolvedValue({ version: 'v0.35.0' });
    const { client } = renderAppVersion();

    await settled(client);

    await waitFor(() => expect(stamp()).toHaveTextContent(/v0\.35\.0/));
    expect(stamp().getAttribute('title')).toBeNull();
    expect(stamp().querySelector('[title]')).toBeNull();
  });

  it('stays honest after a rollback, when the server is the OLDER side', async () => {
    // "v0.34.0 available" is true whichever direction the versions moved;
    // "update available" would claim the server had something newer when it
    // does not.
    bundleVersion.current = 'v0.35.0';
    mockedGet.mockResolvedValue({ version: 'v0.34.0' });
    const { client } = renderAppVersion();

    await settled(client);

    await waitFor(() =>
      expect(stamp()).toHaveTextContent('SpenDrop v0.35.0 · v0.34.0 available'),
    );
    expect(stamp()).not.toHaveTextContent(/update available/i);
    expect(stamp()).toHaveClass('text-foreground');
  });

  it('degrades to the bundle version alone when the endpoint 404s', async () => {
    // The server predates this endpoint. That is not an error the user has
    // anything to do with, so it must produce silence.
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    bundleVersion.current = 'v0.35.0';
    mockedGet.mockRejectedValue(new ApiError('HTTP 404', 404));
    const { client } = renderAppVersion();

    await settled(client);

    expect(stamp()).toHaveTextContent('SpenDrop v0.35.0');
    expect(stamp()).not.toHaveTextContent(/available/i);
    expect(stamp()).not.toHaveTextContent(/undefined|null/i);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.queryByRole('status')).toBeNull();
    expect(consoleError).not.toHaveBeenCalled();
    expect(consoleWarn).not.toHaveBeenCalled();
  });

  it('degrades to the bundle version alone when the device is offline', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    bundleVersion.current = 'v0.35.0';
    mockedGet.mockRejectedValue(
      new NetworkError('The device is offline', 'offline'),
    );
    const { client } = renderAppVersion();

    await settled(client);

    expect(stamp()).toHaveTextContent('SpenDrop v0.35.0');
    expect(stamp()).not.toHaveTextContent(/available/i);
    expect(consoleError).not.toHaveBeenCalled();
  });

  it('degrades quietly when the server answers 200 with no version field', async () => {
    // A typed decode would zero-fill this and hand `undefined` to the render.
    bundleVersion.current = 'v0.35.0';
    mockedGet.mockResolvedValue({});
    const { client } = renderAppVersion();

    await settled(client);

    expect(stamp()).toHaveTextContent('SpenDrop v0.35.0');
    expect(stamp()).not.toHaveTextContent(/available|undefined/i);
  });
});
