import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { api, ApiError, NetworkError } from './client';

const originalFetch = globalThis.fetch;
const originalOnLine = navigator.onLine;

function setOnline(value: boolean): void {
  Object.defineProperty(navigator, 'onLine', { configurable: true, value });
}

afterEach(() => {
  setOnline(originalOnLine);
});

function mockFetch(response: Partial<Response>) {
  const mock = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: () => Promise.resolve({}),
    ...response,
  });
  globalThis.fetch = mock;
  return mock;
}

describe('ApiClient.request', () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('includes credentials: include on GET requests', async () => {
    const fetchMock = mockFetch({
      json: () => Promise.resolve({ ok: true }),
    });

    await api.get('test');

    expect(fetchMock).toHaveBeenCalledOnce();
    const [, options] = fetchMock.mock.calls[0];
    expect(options.credentials).toBe('include');
  });

  it('includes credentials: include on POST requests', async () => {
    const fetchMock = mockFetch({
      json: () => Promise.resolve({ ok: true }),
    });

    await api.post('test', { data: 1 });

    expect(fetchMock).toHaveBeenCalledOnce();
    const [, options] = fetchMock.mock.calls[0];
    expect(options.credentials).toBe('include');
  });

  it('includes credentials: include on PUT requests', async () => {
    const fetchMock = mockFetch({
      json: () => Promise.resolve({ ok: true }),
    });

    await api.put('test', { data: 1 });

    expect(fetchMock).toHaveBeenCalledOnce();
    const [, options] = fetchMock.mock.calls[0];
    expect(options.credentials).toBe('include');
  });

  it('includes credentials: include on PATCH requests', async () => {
    const fetchMock = mockFetch({
      json: () => Promise.resolve({ ok: true }),
    });

    await api.patch('test', { data: 1 });

    expect(fetchMock).toHaveBeenCalledOnce();
    const [, options] = fetchMock.mock.calls[0];
    expect(options.credentials).toBe('include');
  });

  it('includes credentials: include on DELETE requests', async () => {
    const fetchMock = mockFetch({
      json: () => Promise.resolve(undefined),
    });

    await api.del('test');

    expect(fetchMock).toHaveBeenCalledOnce();
    const [, options] = fetchMock.mock.calls[0];
    expect(options.credentials).toBe('include');
  });

  it('throws an ApiError carrying status 401 on a 401 response, keeping message "Unauthorized"', async () => {
    mockFetch({
      ok: false,
      status: 401,
    });

    const err = await api.get('test').then(
      () => {
        throw new Error('expected rejection');
      },
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    // Backward compat: existing checks key off the bare message string.
    expect((err as ApiError).message).toBe('Unauthorized');
  });

  it('resolves (without parsing the body) on a 204 No Content response', async () => {
    // The DELETE /push/subscriptions handler returns 204 with NO body.
    // A real Response.json() on an empty body rejects with a SyntaxError;
    // request() must short-circuit on 204 so callers like useWebPush.disable()
    // don't see a spurious rejection on a successful unsubscribe.
    mockFetch({
      ok: true,
      status: 204,
      json: () => Promise.reject(new SyntaxError('Unexpected end of JSON input')),
    });

    await expect(api.del('push/subscriptions', { endpoint: 'x' })).resolves.toBeUndefined();
  });

  it('throws an ApiError carrying the HTTP status and server message on other non-ok responses', async () => {
    mockFetch({
      ok: false,
      status: 400,
      json: () => Promise.resolve({ error: 'password too short' }),
    });

    const err = await api.post('test', {}).then(
      () => {
        throw new Error('expected rejection');
      },
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(400);
    expect((err as ApiError).message).toBe('password too short');
  });
});

// An unanswered request must never present as an authenticated "no". Every
// caller that discriminates on `instanceof ApiError` (the offline queue's
// permanent-rejection cap, the auth bootstrap, QuickAdd's retry toast) has to
// be able to tell "the server said no" from "the server never answered".
describe('ApiClient transport failures', () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('converts a failed fetch into a NetworkError, never an ApiError', async () => {
    // What the browser throws when the request never leaves the device.
    globalThis.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));

    const err = await api.get('auth/me').then(
      () => {
        throw new Error('expected rejection');
      },
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(NetworkError);
    expect(err).not.toBeInstanceOf(ApiError);
    expect((err as NetworkError).cause).toBeInstanceOf(TypeError);
  });

  it('classifies a failed fetch as kind="offline" when navigator says the device is offline', async () => {
    setOnline(false);
    globalThis.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));

    const err = (await api.get('auth/me').catch((e: unknown) => e)) as NetworkError;

    expect(err).toBeInstanceOf(NetworkError);
    expect(err.kind).toBe('offline');
  });

  it('classifies a failed fetch as kind="unreachable" when navigator still says online', async () => {
    setOnline(true);
    globalThis.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));

    const err = (await api.get('auth/me').catch((e: unknown) => e)) as NetworkError;

    expect(err.kind).toBe('unreachable');
  });

  // A browser abort throws a generic DOMException, not the app's error type.
  // Converting it is the whole point: the offline queue treats a non-ApiError
  // as "retry later" and an ApiError as "the server rejected this row", so a
  // timeout misfiled as an ApiError would burn the poison-row cap and strand a
  // real purchase behind a permanent "Not synced" badge.
  it('converts an abort/timeout into a NetworkError of kind="timeout"', async () => {
    globalThis.fetch = vi
      .fn()
      .mockRejectedValue(new DOMException('signal timed out', 'TimeoutError'));

    const err = (await api.get('auth/me').catch((e: unknown) => e)) as NetworkError;

    expect(err).toBeInstanceOf(NetworkError);
    expect(err).not.toBeInstanceOf(ApiError);
    expect(err.kind).toBe('timeout');
  });

  it('converts a user-land AbortError into a NetworkError of kind="timeout"', async () => {
    globalThis.fetch = vi
      .fn()
      .mockRejectedValue(new DOMException('The operation was aborted.', 'AbortError'));

    const err = (await api.get('auth/me').catch((e: unknown) => e)) as NetworkError;

    expect(err.kind).toBe('timeout');
  });

  it('passes an abort signal so a hung request cannot wait forever', async () => {
    const fetchMock = mockFetch({ json: () => Promise.resolve({}) });

    await api.get('auth/me');

    const [, options] = fetchMock.mock.calls[0];
    expect(options.signal).toBeInstanceOf(AbortSignal);
  });

  it('converts a failed upload fetch into a NetworkError too', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
    const file = new File(['x'], 'x.csv', { type: 'text/csv' });

    const err = await api.upload('imports', file).then(
      () => {
        throw new Error('expected rejection');
      },
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(NetworkError);
    expect(err).not.toBeInstanceOf(ApiError);
  });
});

describe('ApiClient.upload', () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('sends a POST with FormData containing the file', async () => {
    const mockResponse = { id: 1, filename: 'test.csv' };
    const fetchMock = mockFetch({
      json: () => Promise.resolve(mockResponse),
    });

    const file = new File(['col1,col2\n1,2'], 'test.csv', {
      type: 'text/csv',
    });

    const result = await api.upload<{ id: number; filename: string }>(
      'imports',
      file,
    );

    expect(result).toEqual(mockResponse);

    // Verify fetch was called with correct URL and method
    expect(fetchMock).toHaveBeenCalledOnce();
    const [url, options] = fetchMock.mock.calls[0];
    expect(url).toBe('/api/imports');
    expect(options.method).toBe('POST');

    // Verify body is FormData with the file under default field name
    expect(options.body).toBeInstanceOf(FormData);
    const formData = options.body as FormData;
    expect(formData.get('file')).toBe(file);

    // Verify Content-Type is NOT set (browser sets it with boundary)
    expect(options.headers).toBeUndefined();
  });

  it('uses custom field name when provided', async () => {
    const fetchMock = mockFetch({
      json: () => Promise.resolve({ ok: true }),
    });

    const file = new File(['data'], 'sheet.xlsx', {
      type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    });

    await api.upload('imports', file, 'spreadsheet');

    const [, options] = fetchMock.mock.calls[0];
    const formData = options.body as FormData;
    expect(formData.get('spreadsheet')).toBe(file);
    expect(formData.get('file')).toBeNull();
  });

  it('throws Error with message on non-ok response with JSON error', async () => {
    mockFetch({
      ok: false,
      status: 422,
      json: () => Promise.resolve({ error: 'Invalid file format' }),
    });

    const file = new File(['bad'], 'bad.txt', { type: 'text/plain' });

    await expect(api.upload('imports', file)).rejects.toThrow(
      'Invalid file format',
    );
  });

  it('throws fallback HTTP status message when JSON parse fails', async () => {
    mockFetch({
      ok: false,
      status: 500,
      json: () => Promise.reject(new Error('not json')),
    });

    const file = new File(['data'], 'test.csv', { type: 'text/csv' });

    await expect(api.upload('imports', file)).rejects.toThrow('HTTP 500');
  });

  it('throws an ApiError with status 401 and message "Unauthorized" on 401 status', async () => {
    mockFetch({
      status: 401,
      ok: false,
    });

    const file = new File(['data'], 'test.csv', { type: 'text/csv' });

    const err = await api.upload('imports', file).then(
      () => {
        throw new Error('expected rejection');
      },
      (e: unknown) => e,
    );

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    expect((err as ApiError).message).toBe('Unauthorized');
  });

  it('resolves (without parsing the body) on a 204 No Content response', async () => {
    mockFetch({
      ok: true,
      status: 204,
      json: () => Promise.reject(new SyntaxError('Unexpected end of JSON input')),
    });

    const file = new File(['data'], 'test.csv', { type: 'text/csv' });

    await expect(api.upload('imports', file)).resolves.toBeUndefined();
  });

  it('returns parsed JSON response on success', async () => {
    const payload = {
      id: 42,
      rows_imported: 150,
      warnings: ['skipped row 3'],
    };
    mockFetch({
      json: () => Promise.resolve(payload),
    });

    const file = new File(['csv content'], 'data.csv', { type: 'text/csv' });

    const result = await api.upload<{
      id: number;
      rows_imported: number;
      warnings: string[];
    }>('imports', file);

    expect(result).toEqual(payload);
  });
});
