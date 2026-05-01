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

Mirrors the non-amount columns of the inline edit row in `web/src/components/TransactionRow.tsx:166-309` (the inline editor renders five fields total — Date | Description | Category | Tags | Amount+Currency — but Amount/Currency are deliberately out of v1 scope per §2):

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

Tags in SpenDrop are stored as a comma-separated string (`tags TEXT`, stored verbatim — see the verbatim-storage paragraph below for the canonical statement). Bulk-edit operations:

- **Add:** for each target row, parse existing tags → set-union with new tags → re-serialize. Idempotent per row (re-running the same Add yields the same result).
- **Remove:** for each target row, parse existing tags → set-difference with provided tags → re-serialize.
- **Replace:** unconditionally overwrite the tags column with the provided value as the user typed it (no canonicalization).

Wire format: `{ tags: "tax,receipt", tagsMode: "add" }`. The mode is required iff `tags` is present. An empty `tags` field is rejected client-side (won't appear in the patch); an empty `tags` field with non-empty mode at the server is a 400.

**v1 limitation — no bulk-clear of tags.** Because empty `tags` is rejected, a user cannot bulk-set tags to "" on N rows. The workaround is `Remove` mode listing the tags they want to clear, which works in practice for finite tag vocabularies. A first-class "Clear all tags" affordance is deferred to v2 if needed; the wire shape `{ tags: "", tagsMode: "replace" }` is reserved as the future signal.

**Tag value storage is verbatim — no lowercase / no trim.** The current single-row update path stores tags exactly as the user typed them (length-only validation in `validateTransactionRequest:642-643`). Bulk-edit follows the same convention — input is split on commas and trimmed of leading/trailing whitespace per item only for de-duplication purposes during `Add` / `Remove` set arithmetic, but the final stored string preserves user casing. Introducing canonicalization would create rows whose tags differ by case from rows touched only by the single-row endpoint, violating the "consistent with existing convention" rule. If/when normalization becomes a priority, it's a separate cross-cutting story that retrofits both endpoints together.

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

`useTransactions.refetch` is currently fire-and-forget (`fetchTransactions` at `web/src/hooks/useTransactions.ts:138-152` returns `void`). Bulk-edit cannot rely on observing post-refetch state synchronously, so the prune step is folded into the new hook methods themselves:

```ts
async function bulkUpdate(args): Promise<{ updated: number; skipped: number; visibleIds: number[] }> {
  const result = await api.post<BulkUpdateResponse>('transactions/batch-update', args);
  const refreshed = await fetchTransactionsAsync();  // returns the new page's transactions array
  return { ...result, visibleIds: refreshed.transactions.map(t => t.id) };
}
```

This requires a small refactor: `fetchTransactions` is split into the existing void-returning state-updater (`fetchTransactions`, used by the mount effect and external `refetch` callers) plus a new `fetchTransactionsAsync` that returns the response promise. The state update still happens inside `fetchTransactionsAsync` (so `transactions` state stays in sync), but callers can also `await` and read the response. The existing `deleteByFilter` callsite becomes a candidate for the same refactor in a follow-up — but is not blocked by it.

The page then prunes after `bulkUpdate` resolves:

```ts
const { updated, skipped, visibleIds } = await bulkUpdate({ ids, patch });
const visible = new Set(visibleIds);
setSelectedIds(prev => new Set([...prev].filter(id => visible.has(id))));
toast.success(`Updated ${updated}${skipped ? `, skipped ${skipped}` : ''} transactions`);
```

Prune fires only on a successful response; on failure (caught by the page's try/catch around `bulkUpdate`), `selectedIds` is left untouched and the dialog stays open with the user's form state intact.

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

Per-field validators are decomposed out of `validateTransactionRequest` so the batch path can validate only the fields that are present in the patch. Concrete signatures live in §5.5b. Categories are household-shared (no per-user scoping); description is trimmed and length-checked; tags are length-checked but not case-normalized (§3.3 above).

### 3.9 Authorization + ownership + rate limiting

- Both endpoints sit behind `auth.RequireAuthOrAPIToken` + `requireJSONContentType` (`router.go:109`, `:308-333`). Bearer tokens and session cookies both work.
- Per-user ownership: enforced at SQL for filter-mode (`WHERE user_id = ?` in the existing `buildTransactionWhereClause`); enforced at the per-row check for batch-update (`existing.UserID != user.ID` skip — same as `batch-delete`). Admin (`user.Role == RoleAdmin`) bypass matches existing transaction handlers.
- Rate limiting: not introduced. The `MaxBatchUpdateIDs = 500` cap mirrors `MaxBatchDeleteIDs` and is added to `internal/api/limits.go:35` alongside its sibling (also next to `MaxBatchRestoreIDs` which is already pinned to `MaxBatchDeleteIDs` for the same reason). Filter-mode is single-statement and its cost is bounded by the database. A dedicated rate-limit bucket is unnecessary at v1 scale.

### 3.10 Checkpoint reverification hook

Every existing transaction mutation handler that touches `date` calls `h.verifyAffectedCheckpoints(ctx, earliestDate)` after the data tx commits — this is the Phase 3.3 invariant that keeps `monthly_balance_checkpoints` in sync (`handleUpdateTransaction:471`, `handleBatchCreateTransactions:622`, `handleBatchDeleteTransactions:883`, `handleDeleteTransactionsByFilter:1003`). Bulk-edit must respect this hook on both endpoints when a date change is in the patch, otherwise checkpoint balances drift silently and reports become wrong.

Behavior:

- **`batch-update`**: when the patch includes `date`, the handler tracks `minDate` across all successfully-updated rows as `earliestDate(before.Date, after.Date)` per row, and after the tx commits calls `h.verifyAffectedCheckpoints(ctx, minDate)`. When `date` is not in the patch, the hook is skipped entirely.
- **`update-by-filter` (no-tags fast path)**: the single SQL UPDATE doesn't enumerate dates, so the handler passes the conservative `time.Time{}` sentinel to `verifyAffectedCheckpoints` (matching `handleDeleteTransactionsByFilter:1003`), which triggers a full reverification. Skipped when `date` is not in the patch.
- **`update-by-filter` (tags read-then-write loop)**: the loop already enumerates rows and reads `before.Date`. The handler tracks `minDate = earliestDate(minDate, before.Date, after.Date)` per row and passes it after commit. Skipped when `date` is not in the patch (which it isn't for tags-only patches anyway, since tags doesn't change date).

This is a v1 correctness gate, not a future enhancement. Tested explicitly per §7.

### 3.11 No schema migration

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

The `category_id: z.literal('noChange') | z.number()` discriminated union is the only field where the sentinel is a non-empty string (because the `Select` value cannot be `''`). **The string sentinel is a client-side-only signal — it never reaches the wire.** §4.3 step 2 explicitly excludes it from the patch object before submission, so the server only ever sees `category_id` as `int64` (matching §6.1).

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
) (before, after Transaction, err error)
```

Returning both `before` and `after` lets the caller compute `earliestDate(before.Date, after.Date)` for the checkpoint hook (§3.10). Mirrors the existing single-row `Update` shape (`store.go:100`) which already loads both snapshots internally.

The three sentinel errors `ErrTombstoned`, `ErrNotOwned`, `ErrNotFound` are exported from `internal/database` (placed alongside the existing `database.ErrTokenNotFound` in `store_api_token.go` — same package, same naming convention). The handler imports them as `database.ErrTombstoned` etc., matching the api-token precedent.

Internal flow per call:

1. `qtx.GetTransactionByID(ctx, id)` (deliberately leaks tombstones — caller's responsibility to skip).
2. If `before.DeletedAt.Valid`, return `ErrTombstoned` (caller skips).
3. If `before.UserID != actorUserID && !isAdmin`, return `ErrNotOwned` (caller skips).
4. Apply the patch fields onto the row, producing `merged UpdateTransactionParams`.
5. Per-field validation already happened in the handler (§3.8 / §5.5b) before reaching the store, so this step is just the merge — no additional validation here.
6. `qtx.UpdateTransaction(ctx, merged)`.
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
func (h *Handler) handleBatchUpdateTransactions(w http.ResponseWriter, r *http.Request) {
    user, ok := auth.GetUser(r)
    if !ok { writeError(w, 401, "unauthorized"); return }

    var req BatchUpdateRequest
    if err := decodeJSON(w, r, &req); err != nil { writeError(w, 400, "invalid body"); return }

    if len(req.IDs) == 0 || len(req.IDs) > MaxBatchUpdateIDs {
        writeError(w, 400, "invalid ids"); return
    }
    patch, err := buildUpdatePatch(req.Patch, req.TagsMode)
    if err != nil { writeError(w, 400, err.Error()); return }
    if patch.IsEmpty() { writeError(w, 400, "patch must not be empty"); return }

    var (
        updated, skipped int64
        minDate          time.Time  // zero unless patch.Date is set
    )
    err = h.txnStore.WithTx(r.Context(), func(tx *sql.Tx) error {
        for _, id := range req.IDs {
            before, after, err := h.txnStore.UpdateTx(r.Context(), tx, user.ID, id, patch)
            switch {
            case errors.Is(err, database.ErrTombstoned),
                 errors.Is(err, database.ErrNotOwned),
                 errors.Is(err, database.ErrNotFound):
                skipped++
            case err != nil:
                return err  // SQL/constraint error rolls back the entire tx
            default:
                updated++
                if patch.Date != nil {
                    minDate = earliestDate(minDate, before.Date, after.Date)
                }
            }
        }
        if skipped > 0 {
            // RecordBulkTx wraps the JSON envelope itself — pass a struct, not a marshalled string.
            return h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditUpdate,
                database.BulkAuditSummary{
                    Count:  skipped,
                    Filter: fmt.Sprintf("skipped_during_batch_update:%d_of_%d", skipped, len(req.IDs)),
                })
        }
        return nil
    })
    if err != nil { writeError(w, 500, "internal server error"); return }

    // Checkpoint reverification fires after commit. minDate is zero when patch.Date is unset,
    // which means "no date changed" and the hook is skipped.
    if patch.Date != nil && !minDate.IsZero() {
        h.verifyAffectedCheckpoints(r.Context(), minDate)
    }

    writeJSON(w, 200, map[string]int64{"updated": updated, "skipped": skipped})
}
```

Two notes the plan must preserve:

1. `RecordBulkTx` is called with a `BulkAuditSummary{Count, Filter}` struct — the helper already JSON-encodes the envelope `{bulk:true, count, filter}` itself (`store.go:296-300`). Passing a pre-marshalled string here would double-wrap.
2. `WithTx` (or an equivalent helper) is what the handler uses to scope the multi-row update inside one tx. If `TransactionStore` doesn't already expose `WithTx` publicly, the plan adds it (mirroring the pattern in `store_api_token.go`'s `RevokeAllForUser` which also drives a multi-row mutation under one tx). The exact name is a plan-time decision; what matters here is the tx scoping shape.

### 5.4 `handleUpdateTransactionsByFilter`

Filter querystring is parsed via the existing `buildTransactionWhereClause` (`export_handlers.go:23-105`). Patch is validated via the same helpers as the batch path.

Two SQL paths inside one tx:

- **Tags absent** (only date/description/category_id in patch): one `UPDATE transactions SET ... WHERE id IN (SELECT t.id FROM transactions t JOIN ... WHERE <filter>)`. Mirrors `handleDeleteTransactionsByFilter:945-949`.
- **Tags present**: enumerate matching rows (`SELECT id, tags FROM transactions WHERE <filter>`), compute new tags per row, run N small `UPDATE WHERE id = ?` statements. Documented in handler comment as the trade-off for tags semantics.

Both paths emit one `RecordBulkTx` summary audit row at the end. The patch is embedded into the `Filter` string (matching `handleBulkRename` and `handleDeleteTransactionsByFilter` precedent — those handlers use the `Filter` field as a free-form descriptor, not a JSON struct) so that `RecordBulkTx`'s built-in `{bulk, count, filter}` envelope captures everything in one place without double-encoding:

```go
patchSummary := summarizePatch(patch)  // e.g. "category_id=5; tags=tax,receipt(add)"
filterDesc   := fmt.Sprintf("query=%q patch=%s", r.URL.RawQuery, patchSummary)
err = h.txnStore.RecordBulkTx(ctx, tx, user.ID, database.AuditUpdate,
    database.BulkAuditSummary{Count: updated, Filter: filterDesc})
```

`summarizePatch` is a small helper alongside `buildUpdatePatch` — it formats the patch as a stable, human-readable string for audit-table greppability. Plaintext description in the patch summary is consistent with `handleBulkRename`'s plaintext-search-and-replacement pattern (no new exposure surface — see §5.5).

**Stable format pinned for audit-table greppability:**

```
category_id=5; date=2026-04-30; description="ATM card #4839"; tags=tax,receipt(add)
```

Rules:
- Fields appear in fixed order: `date`, `description`, `category_id`, `tags`.
- Only fields present in the patch render; others are omitted (no `category_id=null` placeholders).
- `description` is double-quoted; embedded `"` is escaped `\"`; embedded `;` is left raw (the parsing convention is "split on `; ` with quote-awareness", but the audit consumer is human / grep, not a parser).
- `tags` always renders the mode in parentheses: `tags=...(add)` / `tags=...(remove)` / `tags=...(replace)`.
- `summarizePatch` output is capped at 1024 chars; longer outputs truncate with `…(truncated)` suffix to keep the audit row's `before_json` envelope bounded on pathological inputs.

A unit test pins this format and is part of task #1 in the implementation plan.

After the commit, `verifyAffectedCheckpoints` fires when `patch.Date != nil`:

```go
// No-tags fast path: single SQL UPDATE doesn't enumerate dates,
// so reverify conservatively (matches handleDeleteTransactionsByFilter:1003).
if patch.Date != nil {
    h.verifyAffectedCheckpoints(r.Context(), time.Time{})
}

// Tags read-then-write path: the loop already touched each row's date,
// so a precise minDate is in hand.
if patch.Date != nil && !minDate.IsZero() {
    h.verifyAffectedCheckpoints(r.Context(), minDate)
}
```

### 5.5 Audit payload redaction (none in v1)

The summary audit's `Filter` string includes the patch description verbatim — see `summarizePatch` in §5.4. For description-changing patches this means the new description text lands in the audit row in plaintext. For per-row audit (batch path), `before_json` and `after_json` already carry the description in plaintext, so the filter-mode summary creates no new exposure surface — it's consistent with the existing single-row update audit shape.

This matches `handleBulkRename`'s precedent (records `search` and `new_description` in plaintext on its summary audit). No redaction is performed in v1. If/when audit redaction becomes a priority, it's a cross-cutting story that retrofits all three filter-scoped bulk endpoints (`handleBulkRename`, `handleDeleteTransactionsByFilter`, `handleUpdateTransactionsByFilter`) and the per-row update audit's `before_json`/`after_json` together — not a v1 prerequisite.

### 5.5b Validation helpers

Existing `validateTransactionRequest` at `transaction_handlers.go:629` is decomposed into per-field validators that bulk-edit can call selectively:

- `validateDate(string) (string, error)` — returns the canonical YYYY-MM-DD form, errors on bad format.
- `validateDescription(string) (string, error)` — trims, length-checks (≤ `MaxDescriptionLength`).
- `validateCategoryID(int64) (int64, error)` — verifies `id > 0`, matching the existing single-row laxity in `validateTransactionRequest:648-650`. Categories are household-shared (`migrations/001_initial_schema.sql:24-33` — there is no `user_id` column on `categories`), so no per-user scoping check is needed. The existence + `is_active` upgrade is **not** added in v1: doing so on bulk-edit alone would create a divergence where bulk-edit rejects `category_id = 47 (inactive)` but the single-row endpoint accepts it. If/when that check becomes a priority, both endpoints get the upgraded validator together as a separate cross-cutting story.
- `validateTagsField(string) (string, error)` — length-checks (≤ `MaxTagsLength`); does NOT lowercase or trim individual items beyond what the existing single-row path already does (per §3.3).

Each handler walks `req.Patch` and runs the matching validator only for present fields. Currency and amount validators are explicitly NOT called (those fields aren't in scope for v1). Empty patch returns 400.

### 5.6 sqlc additions

The batch path reuses the existing `UpdateTransaction` sqlc query (`queries.sql.go:UpdateTransaction`). The flow described in §5.2 (re-read → merge patch onto row → call `UpdateTransaction` with the merged params) means we can build complete `UpdateTransactionParams` from the patched row each iteration. No new sqlc query is needed for the batch path.

The filter-mode no-tags fast path uses hand-written SQL inside the handler (matches `handleBulkRename` and `handleDeleteTransactionsByFilter:917-949` precedent verbatim). Hand-rolled because it's a "list of IDs" via subquery shape that sqlc doesn't model cleanly. The shape mirrors `handleDeleteTransactionsByFilter` — same helpers, same JOIN, same user_id append, same live-filter wrap:

```go
// Build SET clauses from the patch
var setClauses []string
var args []any
if patch.Date != nil        { setClauses = append(setClauses, "date = ?");        args = append(args, *patch.Date) }
if patch.Description != nil { setClauses = append(setClauses, "description = ?"); args = append(args, *patch.Description) }
if patch.CategoryID != nil  { setClauses = append(setClauses, "category_id = ?"); args = append(args, *patch.CategoryID) }
setClauses = append(setClauses, "updated_at = CURRENT_TIMESTAMP")
// (tags is handled in the read-then-write loop branch — never reaches this fast path.)

// Build the filter subquery exactly like handleDeleteTransactionsByFilter:917-938
whereClause, whereArgs := buildTransactionWhereClause(r.URL.Query())  // single arg, takes url.Values
if user.Role != RoleAdmin {
    if whereClause == "" {
        whereClause = " WHERE t.user_id = ?"
    } else {
        whereClause += " AND t.user_id = ?"
    }
    whereArgs = append(whereArgs, user.ID)
}
liveClause := appendLiveTransactionsFilter(whereClause)  // appends " AND t.deleted_at IS NULL" (or " WHERE ..." when whereClause is empty)

query := fmt.Sprintf(
    `UPDATE transactions
     SET %s
     WHERE deleted_at IS NULL AND id IN (
         SELECT t.id FROM transactions t
         JOIN categories c ON t.category_id = c.id %s
     )`,
    strings.Join(setClauses, ", "),
    liveClause,
)
res, err := tx.ExecContext(ctx, query, append(args, whereArgs...)...)
updated, _ := res.RowsAffected()
```

The soft-delete invariant requires both layers:

1. `appendLiveTransactionsFilter` adds `t.deleted_at IS NULL` to the **inner** subquery so only live rows are even considered for the UPDATE — this is the load-bearing predicate.
2. The `AND deleted_at IS NULL` on the **outer** UPDATE is defense-in-depth, mirroring `handleDeleteTransactionsByFilter:945-947`. Removing either layer is a CLAUDE.md soft-delete invariant violation.

The `JOIN categories c ON t.category_id = c.id` is required because `buildTransactionWhereClause` emits predicates against `c.type` (when `?type=expense` is in the filter). Skipping the JOIN would surface as `no such column: c.type` at runtime when a `type=` filter is present.

The tags read-then-write path uses the same scaffolding (same `buildTransactionWhereClause` + user_id append + `appendLiveTransactionsFilter` + categories JOIN) but emits a `SELECT t.id, t.tags FROM transactions t JOIN categories c ON t.category_id = c.id <liveClause>` first, computes new tags per row in Go, then issues `UPDATE transactions SET tags = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?` per row inside the same tx. There is no `qtx.ListTransactionsByFilter` sqlc query — both the SELECT and per-row UPDATE are hand-rolled, matching the no-tags fast path's pattern.

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
- `patch`: required, non-empty object. Keys are `date` (YYYY-MM-DD), `description` (≤ 500 chars), `category_id` (int64, validated as `> 0` matching existing single-row laxity), `tags` (comma-separated; stored verbatim per §3.3 — no case-folding, no trim beyond the per-item trim already done during Add/Remove set arithmetic).
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

With tags:

```json
{
  "patch": { "tags": "tax,receipt" },
  "tagsMode": "add"
}
```

Filter is the querystring; body is just the patch + optional `tagsMode`. `tagsMode` is required iff `patch.tags` is present (server returns 400 if mismatched, same as `batch-update`).

### 6.4 Response — `200 OK`

```json
{ "updated": 1247 }
```

The `update-by-filter` response intentionally omits the `skipped` field that `batch-update` carries. Filter-mode's WHERE clause already excluded tombstoned and non-owned rows server-side, so there's no skip-and-continue surface; the `updated` count is the truth.

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

New file: `internal/database/store_transaction_test.go` (mirrors the api-token sibling — `store_api_token_test.go`, `store_api_token_password_cascade_test.go`). The existing `TransactionStore` lives in `store.go` but currently has no dedicated `_test.go` file beside it; this is the right time to add one.

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

The implementation plan (separate doc) will decompose this into ~10 tasks. Audit tests are co-located with each handler step (TDD-first) — they drive the audit row shape, so writing them after the handler invites the kind of "audit emits but the JSON envelope is wrong" bug the §3.6 + §5.4 discussion is meant to prevent.

1. Backend constants + per-field validators (`MaxBatchUpdateIDs` to `internal/api/limits.go`; `validateDate` / `validateDescription` / `validateCategoryID` / `validateTagsField` extracted from `validateTransactionRequest`).
2. Backend store — `UpdateTx`, `UpdatePatch`, errors (`ErrTombstoned`, `ErrNotOwned`, `ErrNotFound`); store tests covering soft-delete + ownership invariants.
3. Backend handler — `handleBatchUpdateTransactions` + types (`BatchUpdateRequest`, `BulkUpdateResponse`); per-row audit invariant test; partial-skip test; checkpoint hook test.
4. Backend handler — `handleUpdateTransactionsByFilter` (no-tags fast path); summary audit invariant test; soft-delete `*_HidesTombstoned` test; checkpoint hook test.
5. Backend handler — `handleUpdateTransactionsByFilter` (tags read-then-write loop); audit-still-summary test; tags Add/Remove/Replace tests.
6. Frontend types + `useTransactions.bulkUpdate` / `bulkUpdateByFilter` + the `fetchTransactionsAsync` refactor (§3.5).
7. Frontend `BulkEditDialog` + zod schema + RHF wiring.
8. Frontend `BulkEditConfirmDialog` (all-matching scope only).
9. Frontend `Transactions.tsx` integration: trigger button, scope dispatch, prune-on-refetch.
10. Frontend tests + docs (README + DESIGN_GUIDE updates).

Each task follows the project's existing TDD discipline: failing test → minimal implementation → green → commit. Conventional-commit prefix per CLAUDE.md (e.g. `feat(api): add batch-update handler`).
