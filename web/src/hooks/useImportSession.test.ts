import { renderHook as rtlRenderHook, act, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement, type ReactNode } from 'react';
import {
  useImportSession,
  type ImportCategoryDecisions,
} from './useImportSession';
import type { Currency } from '@/api/types';
import { STORAGE_KEYS } from '@/lib/storage-keys';

const originalFetch = globalThis.fetch;

/**
 * The hook watches the `['currencies']` query so that adding a missing
 * currency in Settings clears an "unknown currency" flag without a
 * re-upload, which means every render of it now needs a QueryClient.
 *
 * The cache is SEEDED rather than fetched, and `staleTime: Infinity`
 * keeps it that way: `useCurrencies`' queryFn goes through the same
 * global `fetch` these tests replace with a queue of import responses,
 * so a real currencies request would eat a queued reply meant for an
 * upload or a PATCH — and the serialization test counts fetch calls
 * exactly. Seeding also makes the update explicit: a test that wants the
 * currencies to change calls `setQueryData`, which is what the live SSE
 * `invalidateQueries({ queryKey: ['currencies'] })` amounts to here.
 */
const CURRENCIES: Currency[] = [
  {
    code: 'USD',
    name: 'US Dollar',
    symbol: '$',
    rate_to_base: 1,
    is_base: true,
    updated_at: '',
  },
  {
    code: 'LBP',
    name: 'Lebanese Pound',
    symbol: 'L£',
    rate_to_base: 89000,
    is_base: false,
    updated_at: '',
  },
];

let queryClient: QueryClient;

function renderHook<Result, Props>(
  callback: (props: Props) => Result,
  options?: { initialProps?: Props },
) {
  return rtlRenderHook(callback, {
    ...options,
    wrapper: ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: queryClient }, children),
  });
}

/**
 * "The user has decided nothing." Correct for every preview in this file,
 * because none of them carry `unresolved_categories` — a file whose
 * categories all match needs no decisions, and that is the shape these
 * tests are about.
 */
const NO_CATEGORY_DECISIONS: ImportCategoryDecisions = {
  categoryMap: {},
  defaultCategoryId: null,
};

interface MockResponseSpec {
  ok?: boolean;
  status?: number;
  body?: unknown;
  delayMs?: number;
}

/**
 * Builds a queued-response fetch mock. Each call to the mock pops the
 * next spec from the queue in insertion order. Tests that need to
 * assert call-ordering or call-count mid-flight can inspect
 * `fetchMock.mock.calls` directly.
 */
function installFetchQueue(responses: MockResponseSpec[]): ReturnType<typeof vi.fn> {
  let call = 0;
  const fetchMock = vi.fn().mockImplementation(async () => {
    const r = responses[Math.min(call, responses.length - 1)];
    call += 1;
    if (r.delayMs) await new Promise((resolve) => setTimeout(resolve, r.delayMs));
    return {
      ok: r.ok ?? true,
      status: r.status ?? 200,
      json: () => Promise.resolve(r.body ?? {}),
    } as Response;
  });
  globalThis.fetch = fetchMock;
  return fetchMock;
}

/**
 * Builds a successful Response object matching the shape our helpers
 * expect. Used by test #4 where we need to manually resolve a stalled
 * fetch promise rather than going through `installFetchQueue`.
 */
function okResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    json: () => Promise.resolve(body),
  } as Response;
}

function freshPreviewBody(importID: string) {
  return {
    import_id: importID,
    row_count: 2,
    rows: [
      {
        row_id: 0,
        skip: false,
        content_hash: 'h0',
        date: '2025-01-07',
        description: 'Starbucks',
        amount: 5,
        category: 'Food',
      },
      {
        row_id: 1,
        skip: false,
        content_hash: 'h1',
        date: '2025-01-08',
        description: "Trader Joe's",
        amount: 42.1,
        category: 'Food',
      },
    ],
    columns: ['Date', 'Description', 'Amount', 'Category'],
    unique_categories: ['Food'],
    collision_groups: [],
    // Present on EVERY preview the backend emits — upload, PATCH and
    // GET alike — so this fixture carries it too. Omitting it on a
    // PATCH or GET reply would model the disappearing-flag bug rather
    // than the fixed behaviour: `applyResponse` spreads the response
    // wholesale, so a missing key lands as `undefined` and silently
    // clears every flag while confirm keeps refusing the import.
    field_errors: [] as { row_id: number; field: string; message: string }[],
    // Same contract as field_errors above: emitted on every preview the
    // backend produces, so a fixture that omits it models the vanishing
    // -flag bug rather than the fixed behaviour.
    unresolved_categories: [] as {
      name: string;
      reason: 'unmapped' | 'missing';
      row_ids: number[];
    }[],
    expires_at: '2099-01-01T00:00:00Z',
  };
}

/**
 * The exact strings `importFieldLengthMessage` emits in
 * internal/api/import_handlers.go. Mirrored here ONLY as test input —
 * production renders whatever the wire carries — so that fixtures look
 * like real responses. `validateImportField` returns these same strings
 * for its PATCH 400s, which is why a test cannot tell the two sources
 * apart by wording alone.
 */
const SERVER_FIELD_MESSAGES: Record<string, string> = {
  description:
    'Too long for SpenDrop, which stores 500 characters. Shorten it here, or skip this row.',
  tags: "This row's tags are longer than the 500 characters SpenDrop stores. Skip this row, or shorten them in your spreadsheet and upload again.",
  notes:
    "This row's note is longer than the 2,000 characters SpenDrop stores. Skip this row, or shorten the note in your spreadsheet and upload again.",
};

describe('useImportSession', () => {
  beforeEach(() => {
    localStorage.clear();
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: Infinity, refetchOnWindowFocus: false },
      },
    });
    queryClient.setQueryData(['currencies'], CURRENCIES);
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('uploadFile sets preview and writes importId to localStorage', async () => {
    installFetchQueue([{ body: freshPreviewBody('abc') }]);
    const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));

    await act(async () => {
      await result.current.uploadFile(new File(['x'], 'test.xlsx'));
    });

    expect(result.current.preview?.import_id).toBe('abc');
    expect(result.current.importStep).toBe('preview');
    expect(localStorage.getItem(STORAGE_KEYS.importId)).toBe('abc');
  });

  it('mount with stored importId rehydrates via GET', async () => {
    localStorage.setItem(STORAGE_KEYS.importId, 'stored-id');
    const fetchMock = installFetchQueue([{ body: freshPreviewBody('stored-id') }]);

    const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));

    await waitFor(() => {
      expect(result.current.preview?.import_id).toBe('stored-id');
    });

    expect(result.current.importStep).toBe('preview');
    const [url] = fetchMock.mock.calls[0];
    expect(url).toContain('/api/import/stored-id');
  });

  it('mount with stored importId clears localStorage and does NOT surface an error on 404', async () => {
    localStorage.setItem(STORAGE_KEYS.importId, 'expired-id');
    // Use a realistic backend 404 body — NOT the magic string "HTTP 404".
    // The hook's silence logic uses `err instanceof NotFoundError`, so it
    // must work regardless of what the backend returns in the error body.
    // If this test uses a magic string, a regression back to string-match
    // silencing would trivially pass.
    installFetchQueue([
      {
        ok: false,
        status: 404,
        body: { error: 'session not found or expired' },
      },
    ]);

    const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));

    await waitFor(() => {
      expect(localStorage.getItem(STORAGE_KEYS.importId)).toBeNull();
    });

    // Expired sessions are expected — the hook should silently drop
    // back to the upload step without an error banner.
    expect(result.current.error).toBeNull();
    expect(result.current.importStep).toBe('upload');
    expect(result.current.preview).toBeNull();
  });

  it('patchRow serializes cross-row PATCHes through a single queue', async () => {
    // This test proves the concurrency gate: PATCH #2 must NOT be
    // dispatched until PATCH #1's response arrives. The previous
    // implementation relied on fake delays and call-order ordering,
    // which would still pass against a broken concurrent implementation
    // because the test helper records calls at invocation time (in
    // insertion order) regardless of concurrency.
    //
    // Instead we stall PATCH #1 on a manually-resolved promise and
    // inspect fetchMock.mock.calls.length DURING the stall. A broken
    // implementation (parallel fires) would already be at 3 calls; a
    // correct implementation (serialized queue) stays at 2 until we
    // resolve PATCH #1 ourselves.

    let resolvePatch1!: (value: Response) => void;
    const patch1Promise = new Promise<Response>((resolve) => {
      resolvePatch1 = resolve;
    });

    const fetchMock = vi.fn();
    // Call 1: upload — resolves immediately.
    fetchMock.mockResolvedValueOnce(okResponse(freshPreviewBody('abc')));
    // Call 2: PATCH row 0 — stalls until we resolve it manually.
    fetchMock.mockReturnValueOnce(patch1Promise);
    // Call 3: PATCH row 1 — resolves immediately (but the queue
    // must not dispatch it until call 2 settles).
    fetchMock.mockResolvedValueOnce(okResponse(freshPreviewBody('abc')));
    globalThis.fetch = fetchMock;

    const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));

    await act(async () => {
      await result.current.uploadFile(new File(['x'], 'test.xlsx'));
    });

    // Fire both PATCHes back-to-back without awaiting. The hook's
    // patchQueueRef must serialize them — PATCH #2 should wait for
    // PATCH #1's response, which is currently stalled.
    let p1: Promise<void> | undefined;
    let p2: Promise<void> | undefined;
    act(() => {
      p1 = result.current.patchRow(0, 'description', 'Starbucks NYC');
      p2 = result.current.patchRow(1, 'description', 'TJs');
    });

    // Flush the microtask queue so the upload and the first PATCH
    // dispatch have a chance to run. We intentionally do NOT use
    // `await waitFor` here — we want to inspect the mock state in the
    // middle of the stall, not after it completes.
    await Promise.resolve();
    await Promise.resolve();

    // THE critical assertion: while PATCH #1 is stalled, PATCH #2
    // must NOT have been dispatched. Only 2 fetches should have
    // happened so far (upload + PATCH #1).
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(result.current.pendingPatchCount).toBe(2);

    // Now resolve PATCH #1 — PATCH #2 should dispatch immediately after.
    await act(async () => {
      resolvePatch1(okResponse(freshPreviewBody('abc')));
      await Promise.all([p1, p2]);
    });

    // All three fetches have fired. Order is upload → PATCH #1 → PATCH #2.
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const [, patch1Args, patch2Args] = fetchMock.mock.calls;
    expect(patch1Args[0]).toContain('/rows/0');
    expect(patch2Args[0]).toContain('/rows/1');
    expect(result.current.pendingPatchCount).toBe(0);
  });

  it('patchRow 400 error populates cellErrors; a following 200 clears it', async () => {
    const fetchMock = installFetchQueue([
      { body: freshPreviewBody('abc') }, // upload
      { ok: false, status: 400, body: { error: 'INVALID_DATE' } }, // PATCH 1: bad date
      { body: freshPreviewBody('abc') }, // PATCH 2: good date
    ]);

    const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));

    await act(async () => {
      await result.current.uploadFile(new File(['x'], 'test.xlsx'));
    });

    // Bad PATCH: rejects, sets cellError.
    await act(async () => {
      try {
        await result.current.patchRow(0, 'date', 'not-a-date');
      } catch {
        /* expected */
      }
    });

    expect(result.current.cellErrors['0:date']).toBeDefined();
    expect(result.current.cellErrors['0:date']?.message).toBe('INVALID_DATE');

    // Good PATCH: cellError cleared.
    await act(async () => {
      await result.current.patchRow(0, 'date', '2025-01-07');
    });

    expect(result.current.cellErrors['0:date']).toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('confirmImport 409 updates collision_groups WITHOUT clobbering user-edited rows', async () => {
    // This test pins two invariants at once:
    //   A. 409 recovery updates collision_groups to the server's view.
    //   B. User edits made BEFORE the confirm attempt are preserved
    //      across the 409 — the hook must NOT replace rows with the
    //      server's pristine rows on 409.
    //
    // Setup: upload → PATCH row 0 description to a sentinel → confirm
    // → assert both (A) the new collision_groups ARE on the preview
    // and (B) the sentinel description IS still on the preview.

    const patchResponse = {
      ...freshPreviewBody('abc'),
      rows: [
        {
          row_id: 0,
          skip: false,
          content_hash: 'h0-edited',
          date: '2025-01-07',
          description: 'EDITED_SENTINEL',
          amount: 5,
          category: 'Food',
        },
        {
          row_id: 1,
          skip: false,
          content_hash: 'h1',
          date: '2025-01-08',
          description: "Trader Joe's",
          amount: 42.1,
          category: 'Food',
        },
      ],
    };

    installFetchQueue([
      { body: freshPreviewBody('abc') }, // 1. upload
      { body: patchResponse },           // 2. PATCH row 0 → description EDITED_SENTINEL
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
      },                                 // 3. confirm → 409
    ]);

    const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));

    await act(async () => {
      await result.current.uploadFile(new File(['x'], 'test.xlsx'));
    });

    // Edit row 0 to the sentinel BEFORE confirming.
    await act(async () => {
      await result.current.patchRow(0, 'description', 'EDITED_SENTINEL');
    });

    expect(result.current.preview?.rows[0].description).toBe('EDITED_SENTINEL');
    expect(result.current.preview?.collision_groups).toEqual([]);
    expect(result.current.canImport).toBe(true);

    // Confirm hits 409 — hook should update collision_groups in place
    // WITHOUT reverting rows[0] back to the server's pristine "Starbucks".
    await act(async () => {
      await result.current.confirmImport({ Food: 5 }, null);
    });

    // (A) collision_groups updated.
    expect(result.current.preview?.collision_groups).toHaveLength(1);
    expect(result.current.preview?.collision_groups[0].group_id).toBe('g1');
    // With 1 active (non-skipped) collision group, canImport flips false.
    expect(result.current.unresolvedCount).toBe(1);
    expect(result.current.canImport).toBe(false);
    // The user-facing error message is set.
    expect(result.current.error).toContain('Unresolved');

    // (B) THE critical invariant: row 0's description is STILL the
    // sentinel. A regression that did `setPreview(err.body)` or
    // `setPreview({ ...prev, ...err.body })` would clobber rows here
    // because `err.body` doesn't carry rows at all, so the merge would
    // either throw or revert to the last known rows — both of which
    // would lose EDITED_SENTINEL.
    expect(result.current.preview?.rows[0].description).toBe('EDITED_SENTINEL');
    // Row 1 is unchanged by the edit AND unchanged by the 409.
    expect(result.current.preview?.rows[1].description).toBe("Trader Joe's");
  });

  it('skipping every member of a collision group flips unresolvedCount to 0 and canImport to true', async () => {
    // Guards the skip-is-sticky rule (spec §§359–369): a collision
    // group is considered "resolved" when every one of its member rows
    // has `skip === true`, even though the content hashes still
    // collide. Without this test, a regression that counted skipped
    // rows toward unresolved would silently pass every other test in
    // this suite (since all other tests use collision_groups === []).

    // Seed with a collision group covering rows 0 and 1. The initial
    // upload response carries a non-empty collision_groups.
    const withCollision = {
      ...freshPreviewBody('abc'),
      collision_groups: [
        {
          group_id: 'g1',
          reason: 'intra_file' as const,
          member_row_ids: [0, 1],
        },
      ],
    };

    // After skipping row 0, the group still has row 1 active → still
    // unresolved. After skipping row 1 too, the group is resolved.
    const afterSkip0 = {
      ...withCollision,
      rows: [
        { ...withCollision.rows[0], skip: true },
        { ...withCollision.rows[1] },
      ],
    };
    const afterSkip1 = {
      ...withCollision,
      rows: [
        { ...withCollision.rows[0], skip: true },
        { ...withCollision.rows[1], skip: true },
      ],
    };

    installFetchQueue([
      { body: withCollision }, // upload
      { body: afterSkip0 },    // PATCH row 0 skip=true
      { body: afterSkip1 },    // PATCH row 1 skip=true
    ]);

    const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));

    await act(async () => {
      await result.current.uploadFile(new File(['x'], 'test.xlsx'));
    });

    // Before any skip: one active collision group, unresolvedCount=1, cannot import.
    expect(result.current.preview?.collision_groups).toHaveLength(1);
    expect(result.current.unresolvedCount).toBe(1);
    expect(result.current.canImport).toBe(false);

    // Skip row 0 — group still has row 1 active.
    await act(async () => {
      await result.current.patchRow(0, 'skip', true);
    });
    expect(result.current.preview?.rows[0].skip).toBe(true);
    expect(result.current.unresolvedCount).toBe(1); // still 1 — row 1 is live
    expect(result.current.canImport).toBe(false);

    // Skip row 1 — group is now fully skipped → resolved.
    await act(async () => {
      await result.current.patchRow(1, 'skip', true);
    });
    expect(result.current.preview?.rows[1].skip).toBe(true);
    expect(result.current.unresolvedCount).toBe(0); // every member skipped
    expect(result.current.canImport).toBe(true);    // gate is open
    // NB: collision_groups is still length 1 — the group is not
    // removed by the backend, only "resolved" by the user skipping
    // every member. The `canImport` flag is computed from the rows'
    // skip state, not from `collision_groups.length`.
    expect(result.current.preview?.collision_groups).toHaveLength(1);
  });

  describe('over-length field errors', () => {
    /**
     * Preview response carrying the given field errors on the 2-row
     * fixture. Every error gets the server's own message, because the
     * server sends one on every error — a fixture without it would be
     * testing a response shape the backend cannot produce.
     */
    function bodyWithFieldErrors(
      fieldErrors: { row_id: number; field: string }[],
      rowOverrides: Partial<{ skip: boolean }>[] = [],
    ) {
      const base = freshPreviewBody('abc');
      return {
        ...base,
        rows: base.rows.map((r, i) => ({ ...r, ...(rowOverrides[i] ?? {}) })),
        field_errors: fieldErrors.map((fe) => ({
          ...fe,
          message: SERVER_FIELD_MESSAGES[fe.field],
        })),
      };
    }

    async function uploadWith(body: unknown) {
      installFetchQueue([{ body }]);
      const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      return result;
    }

    it('blocks import straight from the upload response, before any confirm', async () => {
      const result = await uploadWith(
        bodyWithFieldErrors([{ row_id: 0, field: 'description' }]),
      );

      expect(result.current.fieldErrorRowCount).toBe(1);
      expect(result.current.canImport).toBe(false);
      // Not a collision — the two blockers are counted separately so the
      // status line can name the right one.
      expect(result.current.unresolvedCount).toBe(0);
    });

    it('counts rows, not errors — one row with two bad fields is one row to fix', async () => {
      const result = await uploadWith(
        bodyWithFieldErrors([
          { row_id: 0, field: 'description' },
          { row_id: 0, field: 'notes' },
        ]),
      );

      expect(result.current.fieldErrorRowCount).toBe(1);
    });

    it('treats a skipped row as resolved, exactly like a fully skipped collision group', async () => {
      const result = await uploadWith(
        bodyWithFieldErrors([{ row_id: 1, field: 'notes' }], [{}, { skip: true }]),
      );

      expect(result.current.fieldErrorRowCount).toBe(0);
      expect(result.current.canImport).toBe(true);
    });

    it('ignores an error naming a row the preview does not hold', async () => {
      // A stale 409 payload must not wedge the gate shut against a row
      // the user has no way to reach.
      const result = await uploadWith(
        bodyWithFieldErrors([{ row_id: 99, field: 'description' }]),
      );

      expect(result.current.fieldErrorRowCount).toBe(0);
      expect(result.current.canImport).toBe(true);
    });

    it('seeds a cell error for description, which has a cell to point at', async () => {
      const result = await uploadWith(
        bodyWithFieldErrors([{ row_id: 0, field: 'description' }]),
      );

      expect(result.current.cellErrors['0:description']).toEqual({
        field: 'description',
        message: SERVER_FIELD_MESSAGES.description,
      });
    });

    it('shows something rather than an empty box if a message is missing', async () => {
      // Defensive: the server populates `message` on every error today,
      // so this path should be unreachable. It exists so a response that
      // somehow omits it does not render a red box with no words in it.
      const result = await uploadWith({
        ...freshPreviewBody('abc'),
        field_errors: [{ row_id: 0, field: 'description' }],
      });

      expect(result.current.cellErrors['0:description'].message).toBe(
        'This value is too long for SpenDrop. Shorten it here, or skip this row.',
      );
      // Distinguishable from the server's sentence on sight. A near-copy
      // would drift invisibly, since nothing renders this in practice.
      expect(result.current.cellErrors['0:description'].message).not.toBe(
        SERVER_FIELD_MESSAGES.description,
      );
      // No number, because this side no longer holds the bounds and a
      // guessed one would be the drift the mirror was removed to avoid.
      expect(result.current.cellErrors['0:description'].message).not.toMatch(
        /\d/,
      );
    });

    it("prefers the server's wording over the local fallback", async () => {
      // The backend emits one string for this condition and reuses it
      // for the PATCH 400, so the cell must read the same whether the
      // user met the error by uploading or by editing. Deliberately
      // unlike the local fallback so a regression to client-composed
      // copy cannot pass.
      const result = await uploadWith({
        ...freshPreviewBody('abc'),
        field_errors: [
          {
            row_id: 0,
            field: 'description',
            message: 'Server says: too long, shorten it here.',
          },
        ],
      });

      expect(result.current.cellErrors['0:description'].message).toBe(
        'Server says: too long, shorten it here.',
      );
    });

    it('seeds no cell error for tags or notes, which have no column', async () => {
      const result = await uploadWith(
        bodyWithFieldErrors([
          { row_id: 0, field: 'notes' },
          { row_id: 1, field: 'tags' },
        ]),
      );

      // Still blocks the import — it is surfaced at row level by the
      // table instead of in a cell that does not exist.
      expect(result.current.fieldErrorRowCount).toBe(2);
      expect(result.current.cellErrors).toEqual({});
    });

    it('lets a rejected PATCH message win over the derived flag on the same cell', async () => {
      // The distinction under test: the PATCH error describes the value
      // the user JUST TYPED, while the derived flag describes the last
      // value the server accepted.
      //
      // It cannot be tested with a too-long PATCH any more. The backend
      // now returns one string for that condition and reuses it for the
      // 400, so both sources would read identically and the assertion
      // would pass whichever won. So this models the other real journey:
      // the user shortens a flagged description all the way to empty,
      // and the live problem is no longer the length.
      installFetchQueue([
        { body: bodyWithFieldErrors([{ row_id: 0, field: 'description' }]) },
        {
          ok: false,
          status: 400,
          body: { error: 'description cannot be empty' },
        },
      ]);
      const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      // The derived flag is what is showing before the edit.
      expect(result.current.cellErrors['0:description'].message).toBe(
        SERVER_FIELD_MESSAGES.description,
      );

      await act(async () => {
        await result.current.patchRow(0, 'description', '   ').catch(() => {});
      });

      expect(result.current.cellErrors['0:description'].message).toBe(
        'description cannot be empty',
      );
    });

    it('clears the flag when the server stops reporting it', async () => {
      const cleaned = {
        ...freshPreviewBody('abc'),
        field_errors: [],
      };
      installFetchQueue([
        { body: bodyWithFieldErrors([{ row_id: 0, field: 'description' }]) },
        { body: cleaned },
      ]);
      const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      expect(result.current.canImport).toBe(false);

      await act(async () => {
        await result.current.patchRow(0, 'description', 'Coffee');
      });

      // Derived from the response, so there is no client-side clear to
      // forget: the flag and the cell error both go with the payload.
      expect(result.current.fieldErrorRowCount).toBe(0);
      expect(result.current.cellErrors['0:description']).toBeUndefined();
      expect(result.current.canImport).toBe(true);
    });

    it('refreshes flags and explains itself on a 409 FIELD_TOO_LONG confirm', async () => {
      installFetchQueue([
        // Upload reports nothing wrong...
        { body: { ...freshPreviewBody('abc'), field_errors: [] } },
        // ...but confirm disagrees.
        {
          ok: false,
          status: 409,
          body: {
            code: 'FIELD_TOO_LONG',
            field_errors: [
              {
                row_id: 1,
                field: 'notes',
                message: SERVER_FIELD_MESSAGES.notes,
              },
            ],
          },
        },
      ]);
      const { result } = renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      expect(result.current.canImport).toBe(true);

      await act(async () => {
        await result.current.confirmImport({}, null);
      });

      expect(result.current.preview?.field_errors).toEqual([
        { row_id: 1, field: 'notes', message: SERVER_FIELD_MESSAGES.notes },
      ]);
      expect(result.current.fieldErrorRowCount).toBe(1);
      expect(result.current.canImport).toBe(false);
      expect(result.current.error).toBe(
        'Some rows are too long — please shorten or skip the highlighted rows',
      );
      // The confirm did NOT succeed — staying on the preview step is
      // what gives the user somewhere to perform the fix.
      expect(result.current.importStep).toBe('preview');
      // Rows survive the 409: only the field_errors slice is replaced.
      expect(result.current.preview?.rows).toHaveLength(2);
    });
  });

  describe('unresolved categories', () => {
    /** A preview whose only problem is a category value nothing matches. */
    function previewWithUnmapped(importID = 'cat-1') {
      return {
        ...freshPreviewBody(importID),
        unresolved_categories: [
          { name: 'Grocries', reason: 'unmapped' as const, row_ids: [0, 1] },
        ],
      };
    }

    it('blocks import straight off the upload response', async () => {
      installFetchQueue([{ body: previewWithUnmapped() }]);
      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );

      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });

      // No confirm round-trip was needed to learn the server would refuse
      // this. The 409 is the backstop, not the experience.
      expect(result.current.unresolvedCategoryCount).toBe(1);
      expect(result.current.canImport).toBe(false);
    });

    it('a mapping for the name clears the block', async () => {
      installFetchQueue([{ body: previewWithUnmapped() }]);
      const { result, rerender } = renderHook(
        (decisions: ImportCategoryDecisions) => useImportSession(decisions),
        { initialProps: NO_CATEGORY_DECISIONS },
      );

      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      expect(result.current.canImport).toBe(false);

      rerender({ categoryMap: { Grocries: '3' }, defaultCategoryId: null });

      expect(result.current.unresolvedCategoryCount).toBe(0);
      expect(result.current.canImport).toBe(true);
    });

    // The heart of it. Choosing a default because some rows have an empty
    // Category cell is not agreeing that a misspelt name should be filed
    // under it — and that fallback is exactly what used to re-home rows
    // silently.
    it('a default does NOT resolve an unmapped name', async () => {
      installFetchQueue([{ body: previewWithUnmapped() }]);
      const { result, rerender } = renderHook(
        (decisions: ImportCategoryDecisions) => useImportSession(decisions),
        { initialProps: NO_CATEGORY_DECISIONS },
      );

      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });

      rerender({ categoryMap: {}, defaultCategoryId: 3 });

      expect(result.current.unresolvedCategoryCount).toBe(1);
      expect(result.current.canImport).toBe(false);
    });

    // An empty cell has no name to decide about, so the default IS the
    // decision — the one place the fallback stays legitimate.
    it('a default DOES resolve rows with an empty category cell', async () => {
      installFetchQueue([
        {
          body: {
            ...freshPreviewBody('cat-2'),
            unresolved_categories: [
              { name: '', reason: 'missing' as const, row_ids: [0] },
            ],
          },
        },
      ]);
      const { result, rerender } = renderHook(
        (decisions: ImportCategoryDecisions) => useImportSession(decisions),
        { initialProps: NO_CATEGORY_DECISIONS },
      );

      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      expect(result.current.canImport).toBe(false);

      rerender({ categoryMap: {}, defaultCategoryId: 3 });

      expect(result.current.unresolvedCategoryCount).toBe(0);
      expect(result.current.canImport).toBe(true);
    });

    it('409 UNRESOLVED_CATEGORIES refreshes the list and stays on the preview', async () => {
      installFetchQueue([
        { body: freshPreviewBody('cat-3') },
        {
          ok: false,
          status: 409,
          body: {
            code: 'UNRESOLVED_CATEGORIES',
            unresolved_categories: [
              { name: 'Grocries', reason: 'unmapped', row_ids: [0] },
            ],
          },
        },
      ]);
      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );

      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      // The upload said nothing needed deciding, so the client let the
      // confirm through — which is precisely when the server's gate has
      // to be the one that holds.
      expect(result.current.canImport).toBe(true);

      await act(async () => {
        await result.current.confirmImport({}, null);
      });

      expect(result.current.preview?.unresolved_categories).toEqual([
        { name: 'Grocries', reason: 'unmapped', row_ids: [0] },
      ]);
      expect(result.current.unresolvedCategoryCount).toBe(1);
      expect(result.current.canImport).toBe(false);
      expect(result.current.error).toBe(
        'Some categories have no destination — choose one for each, or skip those rows',
      );
      // Staying on the preview is what gives the user somewhere to fix it.
      expect(result.current.importStep).toBe('preview');
      // Rows survive the 409: only the unresolved slice is replaced.
      expect(result.current.preview?.rows).toHaveLength(2);
    });
  });

  describe('money errors', () => {
    /**
     * The strings the backend's money resolver emits, mirrored here as
     * test INPUT only — exactly like SERVER_FIELD_MESSAGES above. The
     * hook renders whatever the wire carries and composes none of these,
     * which is why a money fixture without a message would be testing a
     * response the backend cannot produce.
     */
    const SERVER_MONEY_MESSAGES = {
      rate: 'no rate for 1,500,000 LBP — enter one, or apply today’s 89,000',
      original_currency:
        "`LBX` isn't set up — add it under Settings → Currencies",
      amount: '16.00 ≠ 1,500,000 ÷ 89,000 = 16.85',
      rateInvalid: 'A rate must be a positive number.',
    } as const;

    // Spelled out rather than derived from the record above: that record
    // also holds the PATCH-400 sentence, which is not a field name, and a
    // fixture must not be able to flag a row on something the wire has no
    // field for.
    type MoneyField = 'rate' | 'original_currency' | 'amount';

    /**
     * The 2-row fixture with money flags attached. Rows carry the foreign
     * cells a flagged row really has, so a consumer reading the response
     * can tell the original from the stored value.
     */
    function bodyWithMoneyErrors(
      moneyErrors: { row_id: number; field: MoneyField }[],
      rowOverrides: Partial<{ skip: boolean }>[] = [],
    ) {
      const base = freshPreviewBody('money-1');
      return {
        ...base,
        rows: base.rows.map((r, i) => ({
          ...r,
          original_amount: 1500000,
          original_currency: 'LBP',
          ...(rowOverrides[i] ?? {}),
        })),
        field_errors: moneyErrors.map((fe) => ({
          ...fe,
          message: SERVER_MONEY_MESSAGES[fe.field],
        })),
        currencies: [
          { code: 'USD', rate_to_base: 1, is_base: true },
          { code: 'LBP', rate_to_base: 89000, is_base: false },
        ],
      };
    }

    async function uploadWith(body: unknown) {
      const fetchMock = installFetchQueue([{ body }]);
      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      return { result, fetchMock };
    }

    it('blocks import straight from the upload response, before any confirm', async () => {
      const { result } = await uploadWith(
        bodyWithMoneyErrors([{ row_id: 0, field: 'rate' }]),
      );

      expect(result.current.moneyErrorRowCount).toBe(1);
      expect(result.current.canImport).toBe(false);
      // Counted APART from the length family: the status line names a
      // different remedy for each, and a row whose rate is missing is not
      // a row that is too long.
      expect(result.current.fieldErrorRowCount).toBe(0);
      expect(result.current.unresolvedCount).toBe(0);
    });

    it('counts rows, not errors — one row flagged twice is one row to fix', async () => {
      const { result } = await uploadWith(
        bodyWithMoneyErrors([
          { row_id: 0, field: 'rate' },
          { row_id: 0, field: 'amount' },
        ]),
      );

      expect(result.current.moneyErrorRowCount).toBe(1);
    });

    it('treats a skipped row as resolved, exactly like a skipped over-length row', async () => {
      const { result } = await uploadWith(
        bodyWithMoneyErrors([{ row_id: 1, field: 'rate' }], [{}, { skip: true }]),
      );

      expect(result.current.moneyErrorRowCount).toBe(0);
      expect(result.current.canImport).toBe(true);
    });

    it('ignores a money flag naming a row the preview does not hold', async () => {
      const { result } = await uploadWith(
        bodyWithMoneyErrors([{ row_id: 99, field: 'rate' }]),
      );

      expect(result.current.moneyErrorRowCount).toBe(0);
      expect(result.current.canImport).toBe(true);
    });

    it('seeds cell errors for the two money fields with a cell, and not for the one without', async () => {
      const { result } = await uploadWith(
        bodyWithMoneyErrors([
          { row_id: 0, field: 'rate' },
          { row_id: 1, field: 'amount' },
        ]),
      );

      expect(result.current.cellErrors['0:rate']).toEqual({
        field: 'rate',
        message: SERVER_MONEY_MESSAGES.rate,
      });
      expect(result.current.cellErrors['1:amount']).toEqual({
        field: 'amount',
        message: SERVER_MONEY_MESSAGES.amount,
      });
    });

    it('gives an unknown currency no cell — it is resolved outside the session', async () => {
      const { result } = await uploadWith(
        bodyWithMoneyErrors([{ row_id: 0, field: 'original_currency' }]),
      );

      // There is no `original_currency` cell in the preview and PATCH
      // does not accept the field, so a cell error keyed on it would be
      // an error the user has no control to act on.
      expect(result.current.cellErrors['0:original_currency']).toBeUndefined();
      // It still blocks — the remedy is Settings → Currencies, and the
      // table renders the sentence at row level.
      expect(result.current.moneyErrorRowCount).toBe(1);
      expect(result.current.canImport).toBe(false);
    });

    it('a rejected rate PATCH puts the SERVER’s sentence on the rate cell', async () => {
      // The REAL wire body: `{code, field, message}` and no `error` key.
      // A fixture that added one would test a response the backend cannot
      // produce — and it was that phantom key which hid the client reading
      // only `error`, so the cell showed the literal "HTTP 400" on top of a
      // correct preview flag.
      installFetchQueue([
        { body: freshPreviewBody('money-2') },
        {
          ok: false,
          status: 400,
          body: {
            code: 'INVALID_RATE',
            field: 'rate',
            message: SERVER_MONEY_MESSAGES.rateInvalid,
          },
        },
      ]);
      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });

      await act(async () => {
        try {
          await result.current.patchRow(0, 'rate', 'abc');
        } catch {
          /* expected */
        }
      });

      expect(result.current.cellErrors['0:rate']?.field).toBe('rate');
      // The words, not the status code — and the same words the preview
      // flag would have carried, because the server emits one string for
      // both routes.
      expect(result.current.cellErrors['0:rate']?.message).toBe(
        SERVER_MONEY_MESSAGES.rateInvalid,
      );
      expect(result.current.cellErrors['0:rate']?.message).not.toMatch(/^HTTP /);
    });

    it('applyRateToRows fires one string-valued rate PATCH per row, in order', async () => {
      const fetchMock = installFetchQueue([
        { body: bodyWithMoneyErrors([{ row_id: 0, field: 'rate' }]) },
        { body: bodyWithMoneyErrors([]) },
        { body: bodyWithMoneyErrors([]) },
      ]);
      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });

      await act(async () => {
        await result.current.applyRateToRows([0, 1], 89000);
      });

      // Upload + one PATCH per row, and nothing for a row that was not
      // named — a bulk escape that touched clean rows would record a rate
      // against money the sheet already resolved.
      expect(fetchMock).toHaveBeenCalledTimes(3);
      const [, first, second] = fetchMock.mock.calls;
      expect(String(first[0])).toContain('/rows/0');
      expect(String(second[0])).toContain('/rows/1');
      // A STRING value, because that is what the PATCH contract carries
      // for every field — a number here would serialize to `89000` and
      // the server's string switch would reject it.
      expect(JSON.parse(String(first[1].body))).toEqual({
        field: 'rate',
        value: '89000',
      });
      expect(JSON.parse(String(second[1].body))).toEqual({
        field: 'rate',
        value: '89000',
      });
    });

    it('re-reads the session once when the currencies table changes', async () => {
      const cleared = {
        ...bodyWithMoneyErrors([]),
        currencies: [
          { code: 'USD', rate_to_base: 1, is_base: true },
          { code: 'LBP', rate_to_base: 89000, is_base: false },
          { code: 'LBX', rate_to_base: 3.5, is_base: false },
        ],
      };
      const fetchMock = installFetchQueue([
        { body: bodyWithMoneyErrors([{ row_id: 0, field: 'original_currency' }]) },
        { body: cleared },
      ]);
      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      expect(result.current.moneyErrorRowCount).toBe(1);
      // Only the upload so far: mounting must not re-read a session it
      // just received.
      expect(fetchMock).toHaveBeenCalledTimes(1);

      // What adding the missing currency in Settings does to this cache
      // — the SSE subscriber invalidates `['currencies']` and the query
      // lands new data.
      await act(async () => {
        queryClient.setQueryData(['currencies'], [
          ...CURRENCIES,
          {
            code: 'LBX',
            name: 'Test Pound',
            symbol: 'X',
            rate_to_base: 3.5,
            is_base: false,
            updated_at: '',
          },
        ]);
      });

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      expect(String(fetchMock.mock.calls[1][0])).toContain('/import/money-1');
      // The flag cleared without a re-upload — the whole point.
      expect(result.current.moneyErrorRowCount).toBe(0);
      expect(result.current.canImport).toBe(true);

      // ONCE per update: a re-render, and a write of the same currency
      // list, must not fire a second GET. Without a latch this effect
      // re-reads the session on every render the query touches.
      await act(async () => {
        queryClient.setQueryData(['currencies'], [
          ...CURRENCIES,
          {
            code: 'LBX',
            name: 'Test Pound',
            symbol: 'X',
            rate_to_base: 3.5,
            is_base: false,
            updated_at: '',
          },
        ]);
      });
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    it('does not re-read a session it has just resumed, when the currencies land after it', async () => {
      // The ordering the latch exists for, and the only one that
      // reaches it: on a cold page the currencies query is still in
      // flight while the localStorage resume completes, so the FIRST
      // signature this hook ever sees arrives with a session already
      // loaded. Recording it without reading is what makes that a
      // non-event; treating it as a change re-reads a session that is
      // one second old.
      //
      // Routed by URL and asserted on the session calls only, so it does
      // not depend on which of the two mount fetches goes first — the
      // currencies reply is deliberately the slow one.
      localStorage.setItem(STORAGE_KEYS.importId, 'resume-1');
      queryClient = new QueryClient({
        defaultOptions: {
          queries: { retry: false, staleTime: Infinity, refetchOnWindowFocus: false },
        },
      });
      const fetchMock = vi.fn().mockImplementation(async (url: string) => {
        if (String(url).includes('currencies')) {
          await new Promise((resolve) => setTimeout(resolve, 10));
          return okResponse(CURRENCIES);
        }
        return okResponse(freshPreviewBody('resume-1'));
      });
      globalThis.fetch = fetchMock;

      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );

      await waitFor(() => expect(result.current.importStep).toBe('preview'));
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 30));
      });

      const sessionCalls = fetchMock.mock.calls.filter((call) =>
        String(call[0]).includes('/import/'),
      );
      expect(sessionCalls).toHaveLength(1);
    });

    it('does not re-read anything when there is no session to re-read', async () => {
      // The negative control for the test above: the effect must be
      // latched on the currencies, not fire once per mount, and the
      // upload step has no import_id to GET.
      const fetchMock = installFetchQueue([{ body: freshPreviewBody('money-3') }]);
      renderHook(() => useImportSession(NO_CATEGORY_DECISIONS));

      await act(async () => {
        queryClient.setQueryData(['currencies'], []);
      });

      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('the currencies re-read keeps the server state and does not remount untouched rows', async () => {
      // The refetch runs through the PATCH lane and merges like a PATCH
      // response: rows whose values did not move keep their object
      // identity (React keeps the open editor mounted), and an edit the
      // server has already accepted comes back as the server's own value
      // rather than being reverted to what the client uploaded.
      const edited = {
        ...bodyWithMoneyErrors([]),
        rows: bodyWithMoneyErrors([]).rows.map((r, i) =>
          i === 0 ? { ...r, description: 'Starbucks NYC' } : r,
        ),
      };
      const fetchMock = installFetchQueue([
        { body: bodyWithMoneyErrors([{ row_id: 0, field: 'rate' }]) },
        { body: edited }, // PATCH response
        { body: edited }, // currencies-driven GET
      ]);
      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      await act(async () => {
        await result.current.patchRow(0, 'description', 'Starbucks NYC');
      });
      expect(result.current.preview?.rows[0].description).toBe('Starbucks NYC');
      const untouchedBefore = result.current.preview?.rows[1];

      await act(async () => {
        queryClient.setQueryData(['currencies'], [CURRENCIES[0]]);
      });
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));

      expect(result.current.preview?.rows[0].description).toBe('Starbucks NYC');
      expect(result.current.preview?.rows[1]).toBe(untouchedBefore);
    });

    it('409 MONEY_ERRORS refreshes the flags, keeps the rows, and stays on the preview', async () => {
      installFetchQueue([
        { body: freshPreviewBody('money-4') },
        {
          ok: false,
          status: 409,
          body: {
            code: 'MONEY_ERRORS',
            field_errors: [
              {
                row_id: 1,
                field: 'rate',
                message: SERVER_MONEY_MESSAGES.rate,
              },
            ],
          },
        },
      ]);
      const { result } = renderHook(() =>
        useImportSession(NO_CATEGORY_DECISIONS),
      );
      await act(async () => {
        await result.current.uploadFile(new File(['x'], 'test.xlsx'));
      });
      // The upload response carried no money flags, so the client let the
      // confirm through — which is when the server's gate has to hold.
      expect(result.current.canImport).toBe(true);

      await act(async () => {
        await result.current.confirmImport({}, null);
      });

      expect(result.current.preview?.field_errors).toEqual([
        { row_id: 1, field: 'rate', message: SERVER_MONEY_MESSAGES.rate },
      ]);
      expect(result.current.moneyErrorRowCount).toBe(1);
      expect(result.current.canImport).toBe(false);
      expect(result.current.error).toBe(
        'Some rows have money SpenDrop cannot resolve — fix or skip the highlighted rows',
      );
      expect(result.current.importStep).toBe('preview');
      // Rows survive the 409: only the field_errors slice is replaced.
      expect(result.current.preview?.rows).toHaveLength(2);
    });
  });
});
