import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import type {
  CollisionGroup,
  ImportFieldError,
  ImportPreview,
  ImportResult,
  ImportRow,
  PatchRowRequest,
  UnresolvedCategory,
} from '../api/types';
import {
  uploadImport,
  getImportSession,
  patchImportRow,
  confirmImport as confirmImportAPI,
  cancelImport as cancelImportAPI,
  FieldTooLongError,
  NotFoundError,
  UnresolvedCategoriesError,
  UnresolvedCollisionsError,
} from '../api/import';
import {
  fallbackFieldTooLongMessage,
  isEditableInPreview,
} from '@/lib/import-field-errors';
import { STORAGE_KEYS } from '@/lib/storage-keys';

export type ImportStep = 'upload' | 'preview' | 'done';

export interface CellError {
  field: PatchRowRequest['field'];
  message: string;
}

/**
 * The category decisions the user has made so far, as the gate needs to see
 * them. Passed IN rather than owned here because the controls that produce
 * them live in the import card next to the preview table.
 *
 * It is a required argument, not an optional one with an empty default. A
 * default would mean "nothing decided", which reads as "everything blocked"
 * — a caller that forgot to thread its state through would get a permanently
 * disabled Import button and no indication why.
 */
export interface ImportCategoryDecisions {
  /** Spreadsheet category name → category id, as the confirm body sends it. */
  categoryMap: Record<string, string>;
  /** Category for rows whose Category cell is empty; null if unchosen. */
  defaultCategoryId: number | null;
}

/**
 * The entries in `unresolved_categories` that the user's choices do NOT yet
 * cover. The server's list is computed as if nothing had been decided, so
 * this is where the local decisions are applied:
 *
 *   - `unmapped` — resolved by an explicit entry in `categoryMap`. NOT by
 *     having chosen a default: picking a default because some rows have an
 *     empty Category cell is not agreeing that a misspelt category name
 *     should be filed under it. The import card offers a one-click "apply
 *     the default to all of these", which fills the map — so accepting the
 *     default stays cheap while remaining something the user did.
 *   - `missing` — resolved by choosing a default. There is no name to decide
 *     about, so the default IS the decision, and the control says so.
 *
 * Exported because the gate (this hook) and the wording the card renders have
 * to agree on what "unresolved" means; deriving it twice is how the button
 * ends up disabled against something nothing on screen is asking for.
 */
export function unresolvedCategoryDecisions(
  unresolved: UnresolvedCategory[] | undefined,
  decisions: ImportCategoryDecisions,
): UnresolvedCategory[] {
  if (!unresolved || unresolved.length === 0) return [];
  return unresolved.filter((entry) =>
    entry.reason === 'missing'
      ? decisions.defaultCategoryId === null
      : !decisions.categoryMap[entry.name],
  );
}

export interface UseImportSessionResult {
  // Core state
  preview: ImportPreview | null;
  importStep: ImportStep;
  result: ImportResult | null;
  error: string | null;

  // PATCH / editing state
  pendingPatchCount: number;
  cellErrors: Record<string, CellError>;

  // Derived state
  unresolvedCount: number;
  /**
   * Distinct non-skipped rows carrying at least one over-length field.
   * Blocks `canImport` on its own, so the button is disabled straight
   * off the upload response rather than only after a failed confirm.
   */
  fieldErrorRowCount: number;
  /**
   * Distinct category values still awaiting a decision. Blocks `canImport`
   * on its own so the button is disabled straight off the upload response,
   * rather than only after the confirm comes back 409.
   */
  unresolvedCategoryCount: number;
  canImport: boolean;

  // Actions
  uploadFile: (file: File) => Promise<void>;
  patchRow: (
    rowID: number,
    field: PatchRowRequest['field'],
    value: string | boolean,
  ) => Promise<void>;
  confirmImport: (
    categoryMap: Record<string, number>,
    defaultCategoryId: number | null,
  ) => Promise<void>;
  cancelImport: () => Promise<void>;
  startOver: () => void;
}

/**
 * Returns the number of collision groups that still need user action.
 * A group "needs action" if AT LEAST ONE of its member rows is not
 * marked as skipped. A group where every member is skipped is
 * considered resolved (the user decided to drop them all), so it does
 * NOT block the Import button.
 *
 * Extracted as a pure function for unit-test clarity — the hook's
 * main body is busy with promise-chain plumbing and localStorage
 * side effects.
 */
/**
 * Narrows the server's `field_errors` to the ones that still block the
 * import: an error on a row the user has skipped is resolved, exactly
 * as a collision group whose members are all skipped is resolved. An
 * error naming a row_id the preview no longer holds is dropped rather
 * than counted, so a stale 409 payload can never wedge the gate shut
 * with a flag the user has no row to act on.
 *
 * Exported because the gate (this hook) and the flagging (the preview
 * table) must agree on the word "active" — deriving it twice is how the
 * two drift and the button disables against rows nothing highlights.
 */
export function activeFieldErrors(
  fieldErrors: ImportFieldError[] | undefined,
  rows: ImportRow[],
): ImportFieldError[] {
  if (!fieldErrors || fieldErrors.length === 0) return [];
  const rowById = new Map<number, ImportRow>();
  for (const row of rows) rowById.set(row.row_id, row);
  return fieldErrors.filter((fe) => {
    const row = rowById.get(fe.row_id);
    return row !== undefined && !row.skip;
  });
}

function computeUnresolvedCount(
  groups: CollisionGroup[],
  rows: ImportRow[],
): number {
  const rowById = new Map<number, ImportRow>();
  for (const row of rows) rowById.set(row.row_id, row);

  let unresolved = 0;
  for (const group of groups) {
    const stillActive = group.member_row_ids.some((id) => {
      const r = rowById.get(id);
      return r !== undefined && !r.skip;
    });
    if (stillActive) unresolved += 1;
  }
  return unresolved;
}

export function useImportSession(
  decisions: ImportCategoryDecisions,
): UseImportSessionResult {
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [importStep, setImportStep] = useState<ImportStep>('upload');
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pendingPatchCount, setPendingPatchCount] = useState(0);
  // Errors from a PATCH the server REJECTED. Kept separate from the
  // server's derived `field_errors`; `cellErrors` below is the merge of
  // the two that consumers actually read.
  const [patchCellErrors, setPatchCellErrors] = useState<
    Record<string, CellError>
  >({});

  // Single-lane PATCH queue. Every enqueued PATCH awaits the previous
  // one's settlement before firing, so the backend never sees two
  // concurrent PATCHes for the same import_id. See design doc
  // "Race prevention (cross-row PATCH ordering)".
  const patchQueueRef = useRef<Promise<void>>(Promise.resolve());

  // ---- localStorage resume on mount ----
  useEffect(() => {
    // `cancelled` gates the setState calls when the component unmounts
    // mid-fetch (e.g. user navigates away from Settings before the
    // resume completes). React 18 no longer warns about setState on an
    // unmounted component, but we still want to avoid the resulting
    // "ghost" state update — it would briefly flip importStep to
    // 'preview' on the NEXT mount even though the user moved on.
    // localStorage.removeItem is NOT gated: the importId is stale either
    // way, and removing it idempotently is safe to do regardless of
    // mount state.
    let cancelled = false;
    const stored = (() => {
      try {
        return localStorage.getItem(STORAGE_KEYS.importId);
      } catch {
        return null;
      }
    })();
    if (!stored) return;

    void getImportSession(stored)
      .then((fresh) => {
        if (cancelled) return;
        setPreview(fresh);
        setImportStep('preview');
      })
      .catch((err) => {
        // Always drop the stale importId — whether the session
        // expired (NotFoundError) or something else went wrong, the
        // stored id is no longer actionable.
        try {
          localStorage.removeItem(STORAGE_KEYS.importId);
        } catch {
          /* ignore */
        }
        if (cancelled) return;
        // 404 (NotFoundError) is the expected outcome after a
        // 60-minute idle — silently drop back to the upload step
        // without an error banner. Any other error surfaces as a
        // banner so the user knows their resume attempt failed.
        // Using `instanceof` (not string matching) means the silence
        // logic is decoupled from the backend's exact error message.
        if (err instanceof NotFoundError) return;
        const message = err instanceof Error ? err.message : 'resume failed';
        setError(message);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  // ---- derived state ----
  const unresolvedCount = useMemo(() => {
    if (!preview) return 0;
    return computeUnresolvedCount(preview.collision_groups, preview.rows);
  }, [preview]);

  // Number of distinct rows still carrying an over-length field. Counted
  // per ROW, not per error, because that is the unit the user acts on:
  // a row whose description AND note are both too long is one row to
  // shorten or skip, and reporting "2" for it would not match anything
  // the table highlights.
  const fieldErrorRowCount = useMemo(() => {
    if (!preview) return 0;
    const active = activeFieldErrors(preview.field_errors, preview.rows);
    return new Set(active.map((fe) => fe.row_id)).size;
  }, [preview]);

  /**
   * Per-cell errors as the table consumes them: the server's own
   * over-length flags merged under any error from a rejected PATCH.
   *
   * Merging here rather than seeding `patchCellErrors` from the upload
   * response keeps the two halves on the lifecycles they actually have.
   * The server half is derived, so a row edited back under the limit
   * loses its flag the moment the PATCH response lands, with no
   * client-side deletion to forget; the PATCH half stays imperative,
   * since a 400 is a fact about a keystroke the server never accepted
   * and no later payload will mention it. Seeding one from the other
   * would give the derived half a manual clear path, which is precisely
   * the stale-flag bug the table's build-from-props rule exists to
   * prevent.
   *
   * Only fields the preview can actually edit appear here — today just
   * `description`, which is the only over-lengthable column rendered as
   * a cell. `tags` and `notes` have nothing to attach to and are
   * surfaced at row level by the table instead.
   *
   * Both halves carry the SERVER's sentence. That is the whole point of
   * the merge being uninteresting: whichever way the user reached the
   * error, they read the same words, because the backend emits one
   * string for the flag and for `validateImportField`'s PATCH 400.
   *
   * A live PATCH error still wins on a collision: it describes the
   * value the user just typed, while the derived flag describes the
   * last value the server accepted.
   */
  const cellErrors = useMemo(() => {
    const merged: Record<string, CellError> = {};
    if (preview) {
      for (const fe of activeFieldErrors(preview.field_errors, preview.rows)) {
        if (!isEditableInPreview(fe.field)) continue;
        merged[`${fe.row_id}:${fe.field}`] = {
          field: fe.field,
          message: fe.message || fallbackFieldTooLongMessage(fe.field),
        };
      }
    }
    return { ...merged, ...patchCellErrors };
  }, [preview, patchCellErrors]);

  // Category decisions the user has not made yet. The 409 the server returns
  // for these is the backstop, not the experience — whatever confirm would
  // refuse, this has already disabled the button for.
  const unresolvedCategoryCount = useMemo(() => {
    if (!preview) return 0;
    return unresolvedCategoryDecisions(preview.unresolved_categories, decisions)
      .length;
  }, [preview, decisions]);

  const canImport =
    preview !== null &&
    unresolvedCount === 0 &&
    fieldErrorRowCount === 0 &&
    unresolvedCategoryCount === 0 &&
    pendingPatchCount === 0;

  // ---- row merge helpers ----
  /**
   * Applies a fresh server response to local state. Preserves object
   * identity for unchanged rows so React reconciliation keeps the
   * just-edited input mounted — Tab/Shift-Tab focus does not jump
   * back to the document root mid-burst. Only the row whose row_id
   * changed gets a new object reference.
   */
  const applyResponse = useCallback(
    (fresh: ImportPreview, patchedRowID: number) => {
      setPreview((prev) => {
        if (!prev) return fresh;
        const mergedRows = prev.rows.map((oldRow) => {
          if (oldRow.row_id !== patchedRowID) return oldRow;
          const updated = fresh.rows.find((r) => r.row_id === patchedRowID);
          return updated ?? oldRow;
        });
        // Handle the corner case where a row was added or removed
        // server-side (should never happen in 3.4b, but defensive):
        // fall back to the fresh rows array directly.
        if (mergedRows.length !== fresh.rows.length) {
          return fresh;
        }
        return {
          ...fresh,
          rows: mergedRows,
        };
      });
    },
    [],
  );

  // ---- actions ----
  const uploadFile = useCallback(async (file: File) => {
    setError(null);
    try {
      const fresh = await uploadImport(file);
      setPreview(fresh);
      setImportStep('preview');
      setPatchCellErrors({});
      try {
        localStorage.setItem(STORAGE_KEYS.importId, fresh.import_id);
      } catch {
        /* ignore quota errors — resume is a nice-to-have */
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Upload failed');
    }
  }, []);

  const patchRow = useCallback(
    async (
      rowID: number,
      field: PatchRowRequest['field'],
      value: string | boolean,
    ): Promise<void> => {
      if (!preview) return;
      const importID = preview.import_id;
      const cellKey = `${rowID}:${field}`;

      setPendingPatchCount((n) => n + 1);

      const next = patchQueueRef.current.then(async () => {
        try {
          const fresh = await patchImportRow(importID, rowID, { field, value });
          applyResponse(fresh, rowID);
          // Clear any prior 400 error on this exact cell. Does NOT
          // clear errors on OTHER cells in the same row — each cell
          // owns its own error lifecycle.
          setPatchCellErrors((prev) => {
            if (!(cellKey in prev)) return prev;
            const next = { ...prev };
            delete next[cellKey];
            return next;
          });
        } catch (err) {
          const message =
            err instanceof Error ? err.message : 'Update failed';
          setPatchCellErrors((prev) => ({
            ...prev,
            [cellKey]: { field, message },
          }));
          // Re-throw so the promise rejects for the caller; the queue
          // tail catches this so subsequent PATCHes still fire.
          throw err;
        }
      });

      // Swallow rejections on the QUEUE TAIL so one failed PATCH
      // does not freeze every subsequent edit. The returned promise
      // still rejects so the caller can surface an inline error.
      patchQueueRef.current = next
        .catch(() => {})
        .finally(() => {
          setPendingPatchCount((n) => Math.max(0, n - 1));
        });

      return next;
    },
    [preview, applyResponse],
  );

  const confirmImport = useCallback(
    async (
      categoryMap: Record<string, number>,
      defaultCategoryId: number | null,
    ): Promise<void> => {
      if (!preview) return;
      setError(null);
      try {
        const payload: {
          import_id: string;
          category_map: Record<string, number>;
          default_category_id?: number;
        } = {
          import_id: preview.import_id,
          category_map: categoryMap,
        };
        if (defaultCategoryId !== null) {
          payload.default_category_id = defaultCategoryId;
        }
        const res = await confirmImportAPI(payload);
        setResult(res);
        setImportStep('done');
        try {
          localStorage.removeItem(STORAGE_KEYS.importId);
        } catch {
          /* ignore */
        }
      } catch (err) {
        if (err instanceof UnresolvedCollisionsError) {
          // Update the local collision_groups to match the server's
          // current view. The rest of preview (rows, row_count,
          // columns, unique_categories) is unchanged — only the
          // collision membership changed.
          setPreview((prev) =>
            prev
              ? { ...prev, collision_groups: err.collision_groups }
              : prev,
          );
          setError(
            'Unresolved collisions — please fix or skip the highlighted rows',
          );
          return;
        }
        if (err instanceof UnresolvedCategoriesError) {
          // Same shape as the two branches around it: replace only the
          // slice the server just recomputed, leaving rows / row_count /
          // columns / unique_categories untouched. Refreshing from the
          // server rather than trusting what we had matters when the two
          // disagree — a row skipped in another tab changes which category
          // values are still in play.
          setPreview((prev) =>
            prev
              ? { ...prev, unresolved_categories: err.unresolved_categories }
              : prev,
          );
          setError(
            'Some categories have no destination — choose one for each, or skip those rows',
          );
          return;
        }
        if (err instanceof FieldTooLongError) {
          // Same shape as the collisions branch above: replace only the
          // field_errors slice, leaving rows / row_count / columns /
          // unique_categories untouched. Refreshing from the server
          // rather than trusting the flags we already had matters when
          // the two disagree — a row edited in another tab, or a
          // backend that started reporting a field the upload response
          // predates.
          setPreview((prev) =>
            prev ? { ...prev, field_errors: err.field_errors } : prev,
          );
          setError(
            'Some rows are too long — please shorten or skip the highlighted rows',
          );
          return;
        }
        setError(err instanceof Error ? err.message : 'Import failed');
      }
    },
    [preview],
  );

  const cancelImport = useCallback(async () => {
    if (preview?.import_id) {
      await cancelImportAPI(preview.import_id);
    }
    try {
      localStorage.removeItem(STORAGE_KEYS.importId);
    } catch {
      /* ignore */
    }
    setPreview(null);
    setImportStep('upload');
    setError(null);
    setPatchCellErrors({});
    setPendingPatchCount(0);
  }, [preview]);

  const startOver = useCallback(() => {
    // Called from the "Import another file" button on the done step.
    // No DELETE needed — the confirm already consumed the session.
    setPreview(null);
    setImportStep('upload');
    setResult(null);
    setError(null);
    setPatchCellErrors({});
    setPendingPatchCount(0);
  }, []);

  return {
    preview,
    importStep,
    result,
    error,
    pendingPatchCount,
    cellErrors,
    unresolvedCount,
    fieldErrorRowCount,
    unresolvedCategoryCount,
    canImport,
    uploadFile,
    patchRow,
    confirmImport,
    cancelImport,
    startOver,
  };
}
