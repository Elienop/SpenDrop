import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { api } from './client';

const originalFetch = globalThis.fetch;

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

  it('throws Unauthorized on 401 status', async () => {
    mockFetch({
      status: 401,
      ok: false,
    });

    const file = new File(['data'], 'test.csv', { type: 'text/csv' });

    await expect(api.upload('imports', file)).rejects.toThrow('Unauthorized');
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
