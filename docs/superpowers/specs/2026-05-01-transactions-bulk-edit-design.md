# Transactions Bulk-Edit — Design Spec

**Date:** 2026-05-01
**Branch:** `feat/transactions-bulk-edit` (to be created)
**Status:** Design — awaiting implementation plan

---

## 1. Context

The Transactions page already supports bulk-delete with two scopes (selected page IDs via `batch-delete`, and atomic filter-scoped delete via `delete-by-filter`). It does not support bulk-edit.

The user's primary use case: search "cleaning" in the description, select all matching rows (often via the existing "Select all 1,247 matching" banner), and change the category in one click. The current workflow requires opening each row's inline editor and re-applying the same change N times.

This spec mirrors the Lidarr (Sonarr-lineage) bulk-edit pattern that has shipped to production for years — its `'noChange'` sentinel idiom is dramatically simpler than per-field "Apply" toggles or value-diffing, and it integrates cleanly with shadcn `Select`. The endpoint shape is taken from SpenDrop's own `batch-delete` + `delete-by-filter` precedent so that audit invariants and partial-skip semantics stay consistent across destructive bulk operations.

## 2. Goals / Non-goals

### Goals

- User can edit **Date**, **Description**, **Category**, and **Tags** across N selected transactions in one server round-trip (or one filter-scoped UPDATE for the all-matching case).
- "Edit" trigger surfaces in the existing selection action bar at `Transactions.tsx:765-775`, alongside Delete + Clear, only when `selectionCount > 0`.
- The dialog defaults every field to "no change". Only fields the user explicitly modifies are sent to the server.
- The same dialog handles both selection scopes (page IDs and all-matching filter) — the trigger button reads `Edit ({selectionCount})` and the underlying endpoint differs.
- Atomicity matches existing precedent: ID-list mode is a per-row update inside one tx; filter-mode is a single SQL UPDATE inside one tx. Tombstoned / missing / non-owned IDs are skipped, never error.
- Audit: per-row `transaction_audit` rows for ID-list mode (preserves single-update invariant); one summary `RecordBulkTx` row for filter-mode (matches `delete-by-filter` and `bulk-rename` precedent).
- Selection state is pruned-on-refetch (intersect `selectedIds` with the IDs the server returned), so rows that the edit kicked out of the current filter naturally drop out of the selection. No "selected but invisible" state.
- Confirmation step (`AlertDialog`) only when `selectionScope === 'all-matching'`. Page-mode bulk-edit fires immediately. Matches the existing `delete-by-filter` confirm + `batch-delete` no-confirm precedent.

### Non-goals (v1 — deferred until a concrete need lands)

- **Amount / Currency bulk-edit.** Bulk-changing amount across N rows of mixed original values destroys irreplaceable data; bulk-changing currency on rows with mixed `original_currency` is genuinely ambiguous (overwrite original? recompute base? both?). Needs its own design pass.
- **Notes bulk-edit.** Notes are not exposed in the inline-edit row either, only via the import flow.
- **Tag preview pane.** Lidarr's "Selected items will end up with these tags" pane is valuable for small heterogeneous tag sets; for SpenDrop with 1,247-row scope and per-keystroke server roundtrips, the cost is high. Folded into a v2 enhancement that piggybacks on the confirm dialog (computed once, server-side).
- **Bulk-undo.** Per-row audit captures `before` JSON, so manual recovery via `audit_history` admin tooling remains possible. A first-class undo button is out of scope.
- **Smart "match this row's value" mode.** E.g. "set tags for each row to its existing tags + 'tax'" — the `Add` mode already covers this without a discriminator.
- **Per-token rate-limiting bucket.** Bulk-edit endpoints inherit the existing `RequireAuthOrAPIToken` chain. Cap is enforced via `MaxBatchUpdateIDs = 500`, mirroring `MaxBatchDeleteIDs`.
- **Bulk move to trash followed by edit.** Out of scope (workflow lives in Trash page, not here).

## 3. Key Decisions

### 3.1 Dialog field layout — horizontal 4-column grid

Mirrors the inline edit row in `TransactionRow.tsx:166-309` exactly:

```
| Date | Description | Category | Tags |
```

- Dialog `max-w-3xl` (~768 px) — wider than the standard `max-w-lg` to fit four side-by-side fields without crowding.
- Column widths: Date 120 px (fixed), Description `1fr`, Category 140 px (fixed), Tags `1fr`. Same distribution as the inline-edit row.
- Footer: Cancel (`variant="outline"`) + `Apply to ${N}` (primary). Submit disabled when no fields are dirty.

The user explicitly preferred horizontal mirroring over a stacked vertical form — it preserves muscle memory from inline-edit and avoids the "tall form, lots of scrolling" problem that Lidarr's vertical FormGroup layout has on a 27" monitor.

### 3.2 "No change" idiom — Lidarr's `'noChange'` sentinel + placeholder-driven empty

Per field type:

| Field | Idiom | Implementation |
|---|---|---|
| Category | First option `— No change —` (always preselected) | shadcn `Select`, value `'noChange'` (string sentinel), submit excludes when value === `'noChange'` |
| Date | Empty string = no change | `<Input type="date">`, placeholder `— Keep same —`, submit excludes when value is `''` |
| Description | Empty string = no change | `<Input type="text">`, placeholder `— Keep same —`, submit excludes when trimmed value is `''` |
| Tags | Empty input = no change, regardless of mode | `TagInput`, plus an `Add` / `Remove` / `Replace` radio group above. Submit excludes when tags array is empty |

The single-source-of-truth signal is "is this field's value still the no-change sentinel/empty?". On submit the form walks fields, builds the `patch` object with only the dirty ones, and rejects an empty patch client-side (greys the Apply button) and server-side (400).

There is no per-field "Apply this" checkbox. Lidarr's design has held up across years of production use; clutter is the alternative cost.

### 3.3 Tags semantics — `Add` / `Remove` / `Replace` (Lidarr-derived)

Tags in SpenDrop are stored as a comma-separated string (`tags TEXT`, normalized to lowercase, trimmed). Bulk-edit operations:

- **Add:** for each target row, parse existing tags → set-union with new tags → re-serialize. Idempotent per row (re-running the same Add yields the same result).
- **Remove:** for each target row, parse existing tags → set-difference with provided tags → re-serialize.
- **Replace:** unconditionally overwrite the tags column with the provided value (after normalization).

Wire format: `{ tags: "tax,receipt", tagsMode: "add" }`. The mode is required iff `tags` is present. An empty `tags` field is rejected client-side (won't appear in the patch); an empty `tags` field with non-empty mode at the server is a 400.

**Read-then-write requirement for `update-by-filter` + tags.** A single SQL UPDATE cannot express "for each row, compute new tags as old tags ∪ new tags". The filter-path therefore degrades to a server-side enumerate-then-update inside one tx when `tags` is in the patch:

```
SELECT id, tags FROM transactions WHERE <filter>
foreach row: compute new tags, UPDATE WHERE id = ?
INSERT one summary RecordBulkTx audit row
COMMIT
```

The `category_id` / `date` / `description` filter-path stays single-statement when tags is absent. This is documented in the handler comment and tested.

### 3.4 Confirm step — filter-mode only

Mirrors existing precedent:

- `delete-by-filter` always confirms (`Transactions.tsx:894-935`, full-screen modal with in-flight `disabled` guard).
- `batch-delete` of N selected page-rows fires immediately (`Transactions.tsx:475-484`).

Bulk-edit follows the same rule:

- `selectionScope === 'page'` → submit fires the request directly.
- `selectionScope === 'all-matching'` → an `AlertDialog` opens listing the changed fields ("Date → 2026-04-30", "Category → Groceries"), with `Cancel` / `Apply to ${N} transactions` actions. The `AlertDialogAction` uses the existing `destructiveActionClass` palette.

The confirm dialog text reads the count from the live `total` field on the filter response (server-truth), not from `selectedIds.size` (which is only meaningful in page mode).

### 3.5 Selection prune — intersect with refetched IDs

After a successful bulk-edit, `useTransactions.refetch()` returns the new page worth of rows. The page component then prunes:

```ts
setSelectedIds(prev => new Set(
  [...prev].filter(id => visibleIds.has(id))
));
```

Where `visibleIds` is `new Set(transactions.map(t => t.id))` from the refetched response.

Behavior under each scenario:

- **Filter doesn't intersect with patched fields** (e.g. search "cleaning" + change category): the 12 rows still match the description filter, prune is a no-op, selection preserved → user can chain another bulk-edit on the same 12.
- **Filter intersects with patched fields** (e.g. category filter "Cleaning" + change category): the 12 rows no longer match, refetch returns 0 of them, prune zeros out `selectedIds` → toolbar collapses.
- **All-matching mode**: bypasses the prune step. Its scope is `selectionScope === 'all-matching'`, which is filter-defined and auto-corrects on refetch (the new `total` reflects the new matching set).

This is strictly better than "always clear" (penalizes the safe chained-edit case) and "never clear" (creates a selected-but-off-screen footgun).

### 3.6 Audit granularity — per-row for batch-update, summary for update-by-filter

Matches existing precedent:

- **`batch-update`** (ID-list, max 500): per-row `transaction_audit` row with `action='update'`, full `before_json` / `after_json` payload. Plus one summary `RecordBulkTx` row recording skipped count if any IDs were tombstoned / missing / non-owned. Mirrors `handleBatchDeleteTransactions:824-870`.
- **`update-by-filter`** (filter-scoped, no cap): one summary `RecordBulkTx` row with `transaction_id = BulkAuditTransactionID (= 0)`, `action='update'`, `before_json` carrying `{"bulk":true,"count":N,"filter":"<r.URL.RawQuery>","patch":{...}}`. Mirrors `handleDeleteTransactionsByFilter:983` and `handleBulkRename`.

Forensic implication: the filter-mode path cannot reconstruct individual row before-states from the audit table. The patch and the filter querystring are recorded, so a reasonable forensic walk is "find rows currently matching `?search=cleaning&category=2`" — imperfect but matches the existing convention.

This is not a regression; it's parity with `delete-by-filter` and `bulk-rename`. Raising the bar (per-row audit for filter-mode) would be a separate "uplevel audit" story affecting all three filter-scoped bulk endpoints, not a prerequisite for shipping bulk-edit.

### 3.7 Atomicity + partial-skip semantics

Two atomicity boundaries:

- **Per-row data integrity errors** (constraint violation, FK error, sqlc binding error) → roll back the whole tx. Same as `batch-delete`. Mid-batch failures cannot leave the table half-mutated.
- **"Row absent / not yours / tombstoned"** → skipped per-row, batch continues. A summary audit row records the skipped count if any. Matches `handleBatchDeleteTransactions:824-840` exactly.

Returns: `{ "updated": N, "skipped": M }`. The frontend shows a `toast.success` reading "Updated 12 transactions" or "Updated 12, skipped 2 already deleted" based on `M > 0`.

For the filter-path, atomicity is structurally guaranteed by the single SQL UPDATE (or, with tags, the read-then-write loop inside one tx). There is no skip-mode for filter — the WHERE clause already excluded tombstoned / non-owned rows. Returns: `{ "updated": N }`.

### 3.8 Validation chain

Existing `validateTransactionRequest` at `transaction_handlers.go:629` is decomposed into per-field validators:

- `validateDate(string) (sql.NullString, error)`
- `validateDescription(string) (string, error)`
- `validateCategoryID(int64, userID) (int64, error)` — verifies category exists and belongs to the same household
- `validateTagsField(string, mode) (string, error)` — normalizes, deduplicates, length-checks

Each handler walks `req.Patch` and runs the matching validator only for present fields. Currency and amount validators are explicitly NOT called (those fields aren't in scope for v1). Empty patch returns 400.

### 3.9 Authorization + ownership + rate limiting

- Both endpoints sit behind `auth.RequireAuthOrAPIToken` + `requireJSONContentType` (`router.go:109`, `:308-333`). Bearer tokens and session cookies both work.
- Per-user ownership: enforced at SQL for filter-mode (`WHERE user_id = ?` in the existing `buildTransactionWhereClause`); enforced at the per-row check for batch-update (`existing.UserID != user.ID` skip — same as `batch-delete`). Admin (`user.Role == RoleAdmin`) bypass matches existing transaction handlers.
- Rate limiting: not introduced. The `MaxBatchUpdateIDs = 500` cap mirrors `MaxBatchDeleteIDs` (`limits.go:35`); filter-mode is single-statement and its cost is bounded by the database. A dedicated bucket is unnecessary at v1 scale.

### 3.10 No schema migration

This feature adds zero columns and zero tables. The existing `transactions` schema, the existing `transaction_audit` schema, and the existing `RecordBulkTx` helper are sufficient. No migration ordering, no backfill, no data migration risk on first boot after upgrade. This is deliberate — the "no schema change" path is the lowest-risk shape for a feature that operates over a critical data table.

## 4. Frontend Design

### 4.1 Component hierarchy

New components (under `web/src/pages/Transactions/`):

- `BulkEditDialog.tsx` — the dialog itself. Owns RHF `useForm`, the four field controls, the Cancel/Apply footer, the empty-patch guard, the all-matching confirm dispatch.
- `BulkEditConfirmDialog.tsx` — the AlertDialog used only in all-matching mode. Receives `patchSummary` (formatted "Date → ...", "Category → ...") and `count`; calls back into `BulkEditDialog` on confirm.

Modified components:

- `Transactions.tsx` — adds the "Edit" button at line 765, wires up `setBulkEditOpen`. Adds the prune-on-refetch step. Adds the count → patch → endpoint dispatch.
- `useTransactions.ts` — adds `bulkUpdate({ ids, patch, tagsMode })` and `bulkUpdateByFilter({ filterQuery, patch, tagsMode })` methods. Both call `refetch()` on success.
- `web/src/api/types.ts` — adds `BulkUpdateRequest`, `BulkUpdateByFilterRequest`, `BulkUpdateResponse` types. The `Patch` shape is `Partial<Pick<Transaction, 'date' | 'description' | 'category_id' | 'tags'>>`.

### 4.2 Dialog form schema (zod)

```ts
const bulkEditSchema = z.object({
  date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional().or(z.literal('')),
  description: z.string().max(500).optional(),
  category_id: z.union([z.literal('noChange'), z.number().int().positive()]),
  tags: z.string().optional(),
  tagsMode: z.enum(['add', 'remove', 'replace']).optional(),
}).refine(
  (data) => {
    const tagsSet = data.tags && data.tags.trim() !== '';
    const modeSet = data.tagsMode !== undefined;
    return tagsSet === modeSet;
  },
  { message: 'tagsMode required when tags is set, and vice versa' }
);
```

The `category_id: z.literal('noChange') | z.number()` discriminated union is the only field where the sentinel is a non-empty string (because the `Select` value cannot be `''`).

### 4.3 Submit flow

1. RHF `handleSubmit(values)` fires.
2. Build `patch` by walking values: include `date` if non-empty, `description` if non-empty (after trim), `category_id` if not `'noChange'`, `tags` + `tagsMode` if `tags` is non-empty.
3. If `Object.keys(patch).length === 0`, abort with no-op (Apply button is already disabled, this is a defensive guard).
4. If `selectionScope === 'all-matching'`, open `BulkEditConfirmDialog`. Otherwise straight to step 5.
5. Dispatch `bulkUpdate` or `bulkUpdateByFilter` based on scope.
6. On success: close dialog, `refetch()`, prune `selectedIds`, toast `Updated ${updated} transactions${skipped > 0 ? \`, skipped ${skipped}\` : ''}`.
7. On failure: toast `error.message`, leave dialog open, keep form state.

### 4.4 Accessibility

- Dialog uses shadcn `Dialog` (not `AlertDialog`) — same as `delete-by-filter` confirm precedent — so it has `aria-labelledby` and `aria-describedby` automatically wired.
- Each field has a visible `<Label>` and matching `htmlFor`/`id`.
- Tags radio group uses shadcn `RadioGroup` for proper roving-tabindex behavior.
- `Apply to ${N}` button has `aria-label` echoing the count for screen readers.
- `Esc` closes the dialog (Radix default), guarded against when an in-flight request is pending.

## 5. Backend Design

### 5.1 Endpoints

| Method | Path | Body | Response |
|---|---|---|---|
| `POST` | `/api/transactions/batch-update` | `{ ids: int64[], patch: PatchObj, tagsMode?: 'add'|'remove'|'replace' }` | `200 { updated: N, skipped: M }` |
| `POST` | `/api/transactions/update-by-filter?<filter querystring>` | `{ patch: PatchObj, tagsMode?: ... }` | `200 { updated: N }` |

`PatchObj` shape: `{ date?: string, description?: string, category_id?: int64, tags?: string }`. Server enforces at least one key set.

### 5.2 New TransactionStore method — `UpdateTx`

Mirrors the existing `DeleteTx` / `RestoreTx` pattern. Accepts a caller-owned `*sql.Tx` so the batch handler can wrap N updates + N audit rows in one transaction:

```go
func (s *TransactionStore) UpdateTx(
    ctx context.Context,
    tx *sql.Tx,
    actorID int64,
    id int64,
    patch UpdatePatch,
) (Transaction, error)
```

Internal flow per call:

1. `qtx.GetTransactionByID(ctx, id)` (deliberately leaks tombstones — caller's responsibility to skip).
2. If `before.DeletedAt.Valid`, return `ErrTombstoned` (caller skips).
3. If `before.UserID != actorUserID && !isAdmin`, return `ErrNotOwned` (caller skips).
4. Apply the patch fields onto the row.
5. Validate (date format, category ownership, tags normalization).
6. `qtx.UpdateTransaction(ctx, params)`.
7. `qtx.GetTransactionByID(ctx, id)` again for the after-state.
8. `writeUpdateAudit(ctx, qtx, actorID, before, after)`.
9. Return the after-row.

The new `UpdatePatch` struct uses pointer fields so unset means "do not touch":

```go
type UpdatePatch struct {
    Date        *string
    Description *string
    CategoryID  *int64
    Tags        *string
    TagsMode    *string  // required iff Tags != nil
}
```

This is the canonical "partial update" shape and avoids the sentinel-in-data pitfall that `0` / `""` conflate with "real value".

### 5.3 `handleBatchUpdateTransactions`

```go
func (h *Handler) handleBatchUpdateTransactions(w, r) {
    user, ok := auth.GetUser(r)
    if !ok { 401; return }

    var req BatchUpdateRequest
    if err := decodeJSON(w, r, &req); err != nil { 400; return }

    if len(req.IDs) == 0 || len(req.IDs) > MaxBatchUpdateIDs { 400; return }
    patch, err := buildUpdatePatch(req.Patch, req.TagsMode)
    if err != nil { 400; return }
    if patch.IsEmpty() { 400; return }

    var updated, skipped int
    err = withTx(h.db, func(tx *sql.Tx) error {
        for _, id := range req.IDs {
            _, err := h.txnStore.UpdateTx(ctx, tx, user.ID, id, patch)
            switch {
            case errors.Is(err, ErrTombstoned), errors.Is(err, ErrNotOwned), errors.Is(err, ErrNotFound):
                skipped++
            case err != nil:
                return err  // rolls back
            default:
                updated++
            }
        }
        if skipped > 0 {
            return h.txnStore.RecordBulkTx(ctx, tx, user.ID, AuditUpdate, fmt.Sprintf("skipped:%d", skipped))
        }
        return nil
    })

    if err != nil { 500; return }
    writeJSON(w, 200, map[string]int{"updated": updated, "skipped": skipped})
}
```

### 5.4 `handleUpdateTransactionsByFilter`

Filter querystring is parsed via the existing `buildTransactionWhereClause` (`export_handlers.go:23-105`). Patch is validated via the same helpers as the batch path.

Two SQL paths inside one tx:

- **Tags absent** (only date/description/category_id in patch): one `UPDATE transactions SET ... WHERE id IN (SELECT t.id FROM transactions t JOIN ... WHERE <filter>)`. Mirrors `handleDeleteTransactionsByFilter:945-949`.
- **Tags present**: enumerate matching rows (`SELECT id, tags FROM transactions WHERE <filter>`), compute new tags per row, run N small `UPDATE WHERE id = ?` statements. Documented in handler comment as the trade-off for tags semantics.

Both paths emit one `RecordBulkTx` summary audit row at the end:

```go
auditPayload, _ := json.Marshal(map[string]any{
    "bulk":   true,
    "count":  updated,
    "filter": r.URL.RawQuery,
    "patch":  redactedPatch,  // see §5.5
})
h.txnStore.RecordBulkTx(ctx, tx, user.ID, AuditUpdate, string(auditPayload))
```

### 5.5 Audit payload redaction

The audit JSON includes the patch verbatim. For description, this could leak PII (a user might bulk-rename "Therapy" → "Wellness"). The `description` field is already in the row's `before_json`/`after_json` payload, so this is not new exposure — but it does mean filter-mode summary audits embed plaintext description in `before_json`. This is consistent with `handleBulkRename` (which records `search` and `new_description` in plaintext) and is documented as the existing audit convention.

If/when an audit-redaction pass becomes a priority, it touches all three filter-scoped bulk endpoints together.

### 5.6 sqlc additions

A new query in `internal/database/queries.sql`:

```sql
-- name: UpdateTransactionPartial :exec
-- Used by the bulk-edit batch path. Caller passes the same field set as
-- the existing UPDATE plus a NULL-as-no-change column convention. Each
-- column's WHEN NULL THEN existing-value clause makes "do not touch"
-- structurally explicit at the SQL layer.
UPDATE transactions
SET
  date        = COALESCE(sqlc.arg('date'), date),
  description = COALESCE(sqlc.arg('description'), description),
  category_id = COALESCE(sqlc.arg('category_id'), category_id),
  tags        = COALESCE(sqlc.arg('tags'), tags)
WHERE id = sqlc.arg('id')
  AND user_id = sqlc.arg('user_id')
  AND deleted_at IS NULL;
```

Note the `WHERE` clause — it enforces both ownership and live-row at the SQL level (defense-in-depth on top of the handler's per-row check).

For the filter-mode path, the SQL is hand-written in the handler (matches `handleBulkRename` precedent). No new sqlc query is required for filter-mode update.

## 6. Wire Format

### 6.1 Request — `POST /api/transactions/batch-update`

```json
{
  "ids": [3, 7, 12, 18, 42],
  "patch": {
    "category_id": 5,
    "tags": "tax,receipt"
  },
  "tagsMode": "add"
}
```

- `ids`: required, non-empty, ≤ 500 entries, each int64.
- `patch`: required, non-empty object. Keys are `date` (YYYY-MM-DD), `description` (≤ 500 chars), `category_id` (int64, validated against the user's accessible category set), `tags` (comma-separated; will be normalized).
- `tagsMode`: required iff `patch.tags` is set; one of `add`, `remove`, `replace`.

### 6.2 Response — `200 OK`

```json
{ "updated": 4, "skipped": 1 }
```

`skipped` includes tombstoned / missing / non-owned IDs, with no per-row distinction (matches `batch-delete`).

### 6.3 Request — `POST /api/transactions/update-by-filter?search=cleaning`

```json
{
  "patch": { "category_id": 5 }
}
```

Filter is the querystring; body is just the patch + optional `tagsMode`.

### 6.4 Response — `200 OK`

```json
{ "updated": 1247 }
```

### 6.5 Error responses

- `400` — empty patch, missing `tagsMode` when tags set, invalid date format, oversized `ids` array, malformed JSON. Body `{"error": "<reason>"}`.
- `401` — auth missing/invalid (existing middleware).
- `404` — never returned for missing IDs (those are skipped). Returned only for hard route mismatches.
- `500` — SQL error, validation panic, unexpected. Body `{"error": "internal server error"}`. Logged with stack.

## 7. Testing Strategy

### 7.1 Backend

`internal/api/transaction_handlers_test.go` additions:

- `TestBatchUpdate_HappyPath` — 5 IDs, change category, all 5 updated.
- `TestBatchUpdate_PartialSkip_TombstonedIDs_AreSkipped`
- `TestBatchUpdate_PartialSkip_NonOwnedIDs_AreSkipped`
- `TestBatchUpdate_PartialSkip_MissingIDs_AreSkipped`
- `TestBatchUpdate_RejectsEmptyPatch`
- `TestBatchUpdate_RejectsOversizedIDList` (501 IDs → 400)
- `TestBatchUpdate_TagsAdd_AppendsAndDeduplicates`
- `TestBatchUpdate_TagsRemove_FiltersOutMatching`
- `TestBatchUpdate_TagsReplace_OverwritesExistingTags`
- `TestBatchUpdate_TagsModeWithoutTags_Returns400`
- `TestBatchUpdate_TagsWithoutMode_Returns400`
- `TestBatchUpdate_DescriptionEditPreservesUnsetFields`
- `TestUpdateByFilter_HappyPath`
- `TestUpdateByFilter_TagsAdd_PerRowReadThenWrite`
- `TestUpdateByFilter_RespectsOwnershipAtSQLLevel`
- `TestUpdateByFilter_HidesTombstonedFromUpdate` (CLAUDE.md soft-delete invariant)

### 7.2 Audit

`internal/api/transaction_audit_test.go` additions:

- `TestAudit_BatchUpdate_WritesUpdateRowPerID_WithBeforeAndAfter`
- `TestAudit_BatchUpdate_WithSkips_WritesSummaryRow`
- `TestAudit_UpdateByFilter_WritesOneSummaryRow_WithFilterAndPatch`
- `TestAudit_BatchUpdate_OnRollback_LeavesNoOrphanRows`
- `TestAudit_UpdateByFilter_TagsPath_StillWritesOneSummary` (the per-row tags loop must not emit per-row audit rows)

### 7.3 Store

`internal/database/store_transaction_test.go`:

- `TestStoreUpdateTx_AppliesOnlySetFields`
- `TestStoreUpdateTx_RejectsTombstonedRow`
- `TestStoreUpdateTx_RejectsCrossUserRow_NonAdmin`
- `TestStoreUpdateTx_AdminBypassesOwnership`
- `TestStoreUpdateTx_PoisonedRow_RollsBackEntireBatch`

### 7.4 Frontend

`web/src/pages/Transactions.bulkEdit.test.tsx`:

- `'Edit (12)' button only visible when selectionCount > 0`
- `Dialog opens with all fields at noChange / empty`
- `Apply button disabled when no fields are dirty`
- `Submitting only category sends patch with only category_id`
- `Tags mode radio is enabled only when tags input is non-empty`
- `All-matching scope dispatches to update-by-filter, not batch-update`
- `All-matching scope opens confirm AlertDialog before submitting`
- `Successful submit prunes selectedIds to refetched IDs`
- `Skipped count surfaces in the toast`
- `Failure toast leaves dialog open`

### 7.5 Soft-delete invariant tests

Per CLAUDE.md's `*_HidesTombstoned` discipline, both endpoints get a fixture test that seeds one live + one tombstoned row, applies a patch that would change a filterable field, and asserts the tombstoned row is never updated nor surfaced.

## 8. Safety / Operational Considerations

These are the pre-deployment review items the user explicitly flagged ("safety review before TrueNAS"):

1. **No schema change** → no first-boot-after-upgrade migration risk. Container can be rolled back by replacing the image; no data migration to undo.
2. **Bulk SQL is wrapped in `*sql.Tx`** → SIGTERM mid-update rolls back, no partial state.
3. **`MaxBatchUpdateIDs = 500`** caps the worst-case loop iteration and audit insert count for the batch path.
4. **`RequireAuthOrAPIToken` already gates** — no new auth surface introduced. Bearer tokens (which can mint via the new api-tokens feature) get the same access as session cookies, consistent with the rest of `/api/transactions/*`.
5. **Audit completeness** — every successful mutation writes an audit row (per-row for batch, summary for filter). Failed mutations (rolled back) emit no audit row. Both paths preserve the "no audit without mutation, no mutation without audit" invariant.
6. **Soft-delete hides tombstoned rows from updates** — the new sqlc query has explicit `AND deleted_at IS NULL` in the WHERE clause; the handler also pre-checks via `GetTransactionByID + DeletedAt.Valid`. Defense-in-depth.
7. **No raw SQL in handlers except the documented filter-mode UPDATE** — matches `handleBulkRename` / `handleDeleteTransactionsByFilter` precedent. The hand-written SQL is reviewable in one place per handler.
8. **Rate limiting** — not added as a dedicated bucket; the 500-ID cap and the existing per-IP TCP limits are sufficient at v1 scale. If abuse appears, a dedicated bucket is an additive change.
9. **Backup compatibility** — feature does not alter the backup schema. Existing `backup_v2` includes `transactions` and `transaction_audit` already.
10. **TrueNAS rollback path** — `docker compose down`, replace image tag with previous version, `up -d`. No data state to undo because no schema or content shape changed (only new audit rows, which the prior version reads as opaque history).

## 9. Out of Scope / Deferred

(Repeated from §2.Non-goals for visibility — these are explicitly NOT in v1 and have rationale recorded.)

- Amount + Currency bulk-edit
- Notes bulk-edit
- Tag preview pane (server-computed "X already have it" summary for the confirm dialog is the v2 form)
- Bulk-undo button
- Per-row failure surfacing in the response (counter + skipped is sufficient for v1)
- Per-token rate-limiting bucket
- Smart-clear rule with field-level intersection (replaced by prune-on-refetch, which is strictly simpler and covers the same cases)

## 10. Open Questions

None — all design decisions are settled. Ready for implementation plan.

## 11. Implementation Sequencing (high-level)

The implementation plan (separate doc) will decompose this into ~8–10 tasks:

1. Backend store + sqlc — `UpdateTx`, `UpdatePatch`, `UpdateTransactionPartial` query, soft-delete + ownership tests
2. Backend handler — `handleBatchUpdateTransactions`, request/response types, validators, partial-skip + audit summary
3. Backend handler — `handleUpdateTransactionsByFilter` (no-tags fast path)
4. Backend handler — `handleUpdateTransactionsByFilter` (tags read-then-write loop)
5. Audit tests for both endpoints
6. Frontend types + `useTransactions.bulkUpdate` / `bulkUpdateByFilter`
7. Frontend `BulkEditDialog` + zod schema + RHF wiring
8. Frontend `BulkEditConfirmDialog` (all-matching scope only)
9. Frontend `Transactions.tsx` integration: trigger button, scope dispatch, prune-on-refetch
10. Frontend tests + docs (README + DESIGN_GUIDE updates)

Each task follows the project's existing TDD discipline: failing test → minimal implementation → green → commit.
