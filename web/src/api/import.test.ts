import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  confirmImport,
  getImportSession,
  patchImportRow,
  FieldTooLongError,
  NotFoundError,
  UnresolvedCollisionsError,
} from './import';

const originalFetch = globalThis.fetch;

// Local interface rather than `Partial<Response> & { body?: unknown }` —
// intersecting with Response lets TS pull in the real `body` typing
// (`ReadableStream<Uint8Array<ArrayBuffer>>` on newer lib.dom.d.ts), and
// the excess-property check rejects `code`/`error`/`import_id` on the
// object literal. The strictness surfaces under `tsc -b` (composite
// build) but not under a bare `tsc --noEmit`. Keep the spec shape
// independent of the `Response` interface to avoid the trap.
interface MockResponseSpec {
  ok?: boolean;
  status?: number;
  body?: unknown;
}

function mockFetch(responses: MockResponseSpec[]) {
  let call = 0;
  const mock = vi.fn().mockImplementation(async () => {
    const r = responses[Math.min(call, responses.length - 1)];
    call += 1;
    return {
      ok: r.ok ?? true,
      status: r.status ?? 200,
      json: () => Promise.resolve(r.body ?? {}),
    } as Response;
  });
  globalThis.fetch = mock;
  return mock;
}

describe('api/import', () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('confirmImport throws UnresolvedCollisionsError on 409 with collision_groups', async () => {
    mockFetch([
      {
        ok: false,
        status: 409,
        body: {
          code: 'UNRESOLVED_COLLISIONS',
          collision_groups: [
            {
              group_id: 'g1',
              reason: 'intra_file',
              member_row_ids: [0, 1],
            },
          ],
        },
      },
    ]);

    // The `instanceof` assertion is the real invariant — a name-string
    // match would pass even if the prototype chain were broken by
    // transpilation, which would then break `instanceof` checks in the
    // hook's confirmImport catch branch.
    let caught: unknown;
    try {
      await confirmImport({
        import_id: 'abc',
        category_map: { Food: 5 },
      });
      throw new Error('expected confirmImport to reject');
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(UnresolvedCollisionsError);
    expect(caught).toBeInstanceOf(Error);
    if (caught instanceof UnresolvedCollisionsError) {
      expect(caught.collision_groups).toHaveLength(1);
      expect(caught.collision_groups[0].group_id).toBe('g1');
      expect(caught.collision_groups[0].member_row_ids).toEqual([0, 1]);
    }
  });

  it('confirmImport throws plain Error (not UnresolvedCollisionsError) on 500', async () => {
    mockFetch([
      {
        ok: false,
        status: 500,
        body: { error: 'internal server error' },
      },
    ]);

    let caught: unknown;
    try {
      await confirmImport({
        import_id: 'abc',
        category_map: {},
      });
      throw new Error('expected confirmImport to reject');
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(Error);
    expect(caught).not.toBeInstanceOf(UnresolvedCollisionsError);
    expect(caught).not.toBeInstanceOf(NotFoundError);
    expect((caught as Error).message).toBe('internal server error');
  });

  it('patchImportRow hits /api/import/{id}/rows/{rowID} with the body', async () => {
    const fetchMock = mockFetch([
      {
        ok: true,
        status: 200,
        body: {
          import_id: 'abc',
          row_count: 1,
          rows: [],
          columns: [],
          unique_categories: [],
          collision_groups: [],
          expires_at: '2099-01-01T00:00:00Z',
        },
      },
    ]);

    await patchImportRow('abc', 3, { field: 'description', value: 'Coffee' });

    expect(fetchMock).toHaveBeenCalledOnce();
    const [url, options] = fetchMock.mock.calls[0];
    expect(url).toContain('/api/import/abc/rows/3');
    expect((options as RequestInit).method).toBe('PATCH');
    expect(JSON.parse((options as RequestInit).body as string)).toEqual({
      field: 'description',
      value: 'Coffee',
    });
  });

  it('confirmImport throws FieldTooLongError carrying field_errors on 409 FIELD_TOO_LONG', async () => {
    mockFetch([
      {
        ok: false,
        status: 409,
        body: {
          code: 'FIELD_TOO_LONG',
          field_errors: [
            { row_id: 2, field: 'description' },
            { row_id: 5, field: 'notes' },
          ],
        },
      },
    ]);

    let caught: unknown;
    try {
      await confirmImport({ import_id: 'abc', category_map: {} });
      throw new Error('expected confirmImport to reject');
    } catch (err) {
      caught = err;
    }

    expect(caught).toBeInstanceOf(FieldTooLongError);
    // Distinct from the sibling 409 — the hook branches on type, so a
    // FieldTooLongError that also satisfied `instanceof
    // UnresolvedCollisionsError` would take the wrong branch and wipe
    // the collision groups.
    expect(caught).not.toBeInstanceOf(UnresolvedCollisionsError);
    if (caught instanceof FieldTooLongError) {
      expect(caught.field_errors).toEqual([
        { row_id: 2, field: 'description' },
        { row_id: 5, field: 'notes' },
      ]);
    }
  });

  it('confirmImport falls through to the generic error for an unrecognised 409 code', async () => {
    // A 409 the client does not model carries no payload it can act on.
    // Treating it as either typed error would look to the hook like "the
    // problem resolved itself", so it must surface the server's message.
    mockFetch([
      {
        ok: false,
        status: 409,
        body: { code: 'SOMETHING_NEW', error: 'session already consumed' },
      },
    ]);

    let caught: unknown;
    try {
      await confirmImport({ import_id: 'abc', category_map: {} });
      throw new Error('expected confirmImport to reject');
    } catch (err) {
      caught = err;
    }

    expect(caught).not.toBeInstanceOf(FieldTooLongError);
    expect(caught).not.toBeInstanceOf(UnresolvedCollisionsError);
    expect((caught as Error).message).toBe('session already consumed');
  });

  it('getImportSession throws NotFoundError on 404', async () => {
    // Use a realistic backend 404 body — NOT a magic "HTTP 404" string.
    // The NotFoundError path must work regardless of the backend's exact
    // error message because we check by type, not by string.
    mockFetch([
      {
        ok: false,
        status: 404,
        body: { error: 'session not found or expired' },
      },
    ]);

    let caught: unknown;
    try {
      await getImportSession('expired-id');
      throw new Error('expected getImportSession to reject');
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(NotFoundError);
    expect(caught).toBeInstanceOf(Error);
    expect(caught).not.toBeInstanceOf(UnresolvedCollisionsError);
    if (caught instanceof NotFoundError) {
      expect(caught.importID).toBe('expired-id');
    }
  });
});
