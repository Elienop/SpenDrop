# Import Collision Resolution UI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users resolve SHA-256 content-hash collisions during `xlsx` import by editing rows inline in the preview table instead of silently dropping duplicates.

**Architecture:** Extend the existing in-memory `importStore` session to carry per-row `row_id` stable keys and a computed `collision_groups` view. Add a new `PATCH /api/import/{importID}/rows/{rowID}` endpoint that re-validates, re-hashes, and re-groups the full row list on every field edit, plus a `GET /api/import/{importID}` endpoint for F5-resume. Harden `POST /api/import/{importID}/confirm` to return `409` if any unresolved non-skipped collision remains. Replace the upload response's dead `predicted_skips` field with the richer `collision_groups` shape. Delete the unreachable `force_add` / " (N)" suffix code path. On the frontend, build a `useImportSession` hook that serializes all PATCH writes through a single promise chain and exposes a `cellErrors` map for inline 400 rendering. Render an `ImportPreviewTable` component with collision rows frozen at top, group header rows carrying a `Skip all in group` button, and inline DB-match previews.

**Tech Stack:**
- Backend: Go 1.26, chi router, sqlc + SQLite (existing `internal/api`, `internal/database` packages)
- Frontend: React 18 + TypeScript + Vite, shadcn/ui components, Vitest + happy-dom + `@testing-library/react` + `user-event`
- Reuses: `database.ComputeContentHash`, `queries.GetTransactionByContentHash`, `parseImportDate`, `parseImportAmount`, existing `importStore sync.Map`

**Spec:** `docs/superpowers/specs/2026-04-15-import-collision-resolution-design.md`

**Running Go tests:** This repo runs Go tests inside a Docker container because the host lacks a C compiler (sqlite3 cgo). Wrap any `go test` invocation as:

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./..."
```

When a step below says "Run: `go test ./internal/api -run TestName -v`", execute it inside the same container. All `go build`, `go vet`, and fuzz invocations follow the same rule.

---

## File Structure

### Backend — Go

**Modified:**

- `internal/api/import_handlers.go`
  - Add `RowID int` field to `importRow` struct (line 35) — populated at upload time with the 0-based row index, used as the React key, PATCH path parameter, and session merge key
  - Bump `importTTL` constant (line 50) from `30 * time.Minute` → `60 * time.Minute`
  - Add `loadImportEntryForUser(w, r, importID)` helper that does the store lookup, ownership check, and expiry check currently inlined at lines 604–623 (confirm) and 727–738 (cancel). Returns `(*importEntry, bool)` — `false` means the helper has already written the appropriate HTTP error and the caller should return.
  - Delete `resolveForceAddSuffix` function (lines 1146–1167), `forceAddSuffixCap` constant (line 1132), `errForceAddExhausted` sentinel (line 582), `ForceAdd` field on `importConfirmRequest` (line 473), and `ForceAddSet` field on `importProcessInput` (line 571). Remove all force-add branches inside `processImportRows` (lines 870–922) and the force-add set materialization at lines 644–651.
  - Delete the `skipReasonForceAddCollision` constant (line 497) — unused once `resolveForceAddSuffix` is gone.
  - Delete `predictDuplicateSkips` function (lines 369–432) and `predictedSkip` type (lines 434–457). Replace the callsite at line 336 with `buildCollisionGroups`.
  - Add `collisionGroup` and `dbMatchPreview` types.
  - Add `buildCollisionGroups(ctx, queries, rows, categoryMap, defaultCategoryID, catNameToID, catIDToName) ([]collisionGroup, error)` — pure(ish) function that computes collision groups. Takes the current row list plus the already-resolved category lookups, runs one-shot `GetTransactionByContentHash` lookups per row, groups by content hash.
  - Update `handleImportUpload` response shape (lines 338–345): emit `collision_groups` instead of `predicted_skips`, precompute category lookups the same way `handleImportConfirm` does so the group builder sees canonical category names.
  - Tighten `handleImportConfirm` (line 586): after loading the entry via the new helper, call `buildCollisionGroups` against `entry.Rows`; if any unresolved non-skipped collision remains, return **409 Conflict** with `{code: "UNRESOLVED_COLLISIONS", collision_groups: [...]}`. Skip rows where the session marks `Skip == true`.
  - Add `handleImportPatchRow` handler that re-validates one field, overwrites the corresponding slice entry in place, re-runs `buildCollisionGroups`, and returns `{rows, collision_groups}`. Uses the shared validator helpers below.
  - Add `handleImportGetSession` handler that returns the full current session snapshot (`rows`, `collision_groups`, `import_id`, `unique_categories`) via `loadImportEntryForUser`.
  - Add a `Skip bool` field on `importRow` and pipe it through `processImportRows` so skipped rows are excluded from inserts in confirm.
  - Add `validateImportField(field, value)` pure helper that covers the per-field rules in the spec's Validation field rules table, returning `(normalizedValue any, errCode string, message string)`. Reused by the PATCH handler.

- `internal/api/router.go` (lines 195–198)
  - Register `r.Patch("/import/{importID}/rows/{rowID}", h.handleImportPatchRow)`
  - Register `r.Get("/import/{importID}", h.handleImportGetSession)`
  - Rename existing `Delete("/import/{id}", ...)` path param to `{importID}` for consistency — update `chi.URLParam(r, "id")` → `chi.URLParam(r, "importID")` inside `handleImportCancel`.

**Modified (tests):**

- `internal/api/import_handlers_test.go`
  - **Remove** `TestHandleImport_ForceAdd_AppendsSuffixAndInserts` and any other test whose name contains `ForceAdd` (grep before deleting — see Task 3 Step 2).
  - **Update** any test that asserts on `predicted_skips` to assert on `collision_groups` instead. Update any test that passes `ForceAdd: []int{...}` in `importConfirmRequest` to remove the field.
  - **Add** nine new tests numbered 1–9 in the Testing Strategy below, all using the existing `clearImportStore` / `setupTestDB` / `seedTestUser` / `withUser` / `withUserAndURLParams` helpers.

- `internal/api/import_handlers_property_test.go` (if referenced by conservation/reason-discipline properties)
  - Update imports if `skipReasonForceAddCollision` is referenced. The property test fuzzer should still pass without any force-add mix input.

### Frontend — TypeScript

**Modified:**

- `web/src/api/client.ts`
  - Add an `ApiError extends Error` class that carries `status: number` and `body: unknown`. Replace the two `throw new Error(...)` call sites in `request()` and `upload()` (lines 34–37 and 86–94) to throw `ApiError` instead. Preserve backwards compatibility: the `.message` field still resolves to the body's `error` field (or the HTTP fallback), so existing call sites that do `err instanceof Error ? err.message` continue to work.
  - Export `ApiError` from the module.

- `web/src/api/types.ts` (lines 127–150)
  - Extend `ImportPreview` with `collision_groups: CollisionGroup[]` and `rows: ImportRow[]` (already present). Add `RowID` on `ImportRow` matching the backend's JSON-tagged `row_id`. Add `Skip` on `ImportRow`.
  - Add `CollisionGroup`, `DbMatchPreview`, `PatchRowRequest`, `PatchRowResponse`, `ImportConfirmErrorBody` interfaces per the spec's API contract additions.

- `web/src/pages/Settings.tsx` (lines 1016–1300)
  - Replace `ImportPreviewStep` function (line 1027) with a thin wrapper that delegates state to `useImportSession` and renders the new `ImportPreviewTable` below the existing category-mapping UI.
  - Inside `DataSection` (line 1210): on mount, if `localStorage.spendrop_import_id` is set, call `getImportSession(importID)` through the new hook; on 200 rehydrate state and jump to `importStep = 'preview'`; on 404 clear storage and show "Your previous import session expired — please re-upload".
  - Replace the inline `api.post<ImportResult>('import/confirm', ...)` call at line 1278 with a version that handles the new 409 `UNRESOLVED_COLLISIONS` body by merging the returned `collision_groups` back into the hook's state (so stale UI snaps to truth if the user raced a re-upload).
  - On successful `/confirm`, call `localStorage.removeItem('spendrop_import_id')`.

**New:**

- `web/src/api/import.ts`
  - `uploadImport(file: File): Promise<ImportPreview>` — calls `api.upload<ImportPreview>('import/upload', file)`; exists so the Settings page consumes a single typed import namespace rather than inlining `api.upload` with a string path.
  - `getImportSession(importID: string): Promise<ImportPreview>` — wraps `api.get<ImportPreview>('import/' + importID)`.
  - `patchImportRow(importID, rowID, body): Promise<PatchRowResponse>` — wraps `api.patch<PatchRowResponse>('import/' + importID + '/rows/' + rowID, body)`.
  - `cancelImport(importID: string): Promise<void>` — wraps `api.del('import/' + importID)`. Replaces the inline `api.del(...)` in `Settings.handleCancelImport`.
  - `confirmImport(importID, categoryMap, defaultCategoryID?): Promise<ImportResult>` — wraps the existing `api.post<ImportResult>('import/confirm', ...)` payload builder. Keeps the 409 handling contract in one place.

- `web/src/hooks/useImportSession.ts`
  - Owns the canonical session state: `rows`, `collisionGroups`, `importId`, `pendingPatchCount`, `cellErrors` (keyed `"<rowID>:<field>"`).
  - Holds `patchQueueRef = useRef<Promise<void>>(Promise.resolve())`. `enqueuePatch({rowID, field, value})` chains a new `.then()` onto the ref, increments `pendingPatchCount` synchronously before the fetch, decrements in `.finally()`, merges the response into state on 200, and on 400 (caught `ApiError` with `status === 400`) writes to `cellErrors`.
  - `enqueueBulkSkip(groupID)`: looks up the group's `member_row_ids`, then iterates `enqueuePatch({rowID, field: 'skip', value: true})` — the promise chain naturally serializes them.
  - `clearCellError(rowID, field)` — explicit action the UI calls after a successful PATCH response for that cell (also invoked automatically by `enqueuePatch` on success).
  - `unresolvedNonSkippedCollisions` memo — counts rows whose `row_id` belongs to some `collision_group` and whose `skip !== true`.
  - `canImport` memo — `unresolvedNonSkippedCollisions === 0 && pendingPatchCount === 0`.
  - `loadFromSession(importID)` — called at mount from Settings when `localStorage.spendrop_import_id` is set. Calls `getImportSession(importID)`; on `ApiError` with `status === 404`, clears localStorage and returns `{ expired: true }` so the caller can transition back to the file-drop step.
  - `loadFromPreview(preview)` — takes the upload response and bootstraps state.
  - `clear()` — called on cancel/confirm-success. Resets state and clears localStorage.

- `web/src/components/ImportPreviewTable.tsx`
  - Props: `{ rows, collisionGroups, cellErrors, pendingPatchCount, onEditCell, onToggleSkip, onSkipAllInGroup, categoryDisplayMap }`.
  - Renders rows sorted so that rows in any collision group come first (stable sort by `row_id` within and across groups; no re-sort after edits).
  - Group header rows: for each `collision_group`, emit a non-data header row above the first member row in sorted order. Header contains `⚠ N rows collide — Skip all in group` with a button. If `reason === 'db_match'`, render an expandable muted sub-row immediately beneath the header showing `db_match.date | description | amount | category_name`.
  - Cell editing: double-click to enter edit, Escape cancels, Enter commits → `onEditCell(rowID, field, value)`, Tab commits and moves focus right, Shift+Tab moves left. Use `shadcn` `<Input>` components with the dense classes from the spec.
  - 400 error UX: if `cellErrors['<rowID>:<field>']` is set, show a red ring + one-line error message below the cell.
  - Collision row visual: amber background + warn icon. Skipped row: strikethrough + muted. Focused cell gets ring.
  - Footer: `⚠ Fix or skip N of M collisions to enable import` progress line + `<div aria-live="polite" class="sr-only">N collisions remaining</div>` for screen readers + `Import K` button disabled while `!canImport`.

**New (tests):**

- `web/src/components/ImportPreviewTable.test.tsx` — frontend tests #10–#15 from the spec's Testing Strategy.

---

## Chunk 1: Backend Foundation

This chunk makes `importRow` carry its own row id + skip flag, extends the upload preview to build and emit `collision_groups`, extracts the ownership/expiry check into a shared helper, and **removes the dead `force_add` / " (N)" suffix code path**. It also covers the two tests that pin down the new initial collision grouping (#9) and the upload-side soft-delete invariant (#8 Part A).

At the end of this chunk the existing Go test suite passes and the upload response emits `collision_groups`, but the PATCH/GET endpoints and the tightened confirm are NOT yet wired up (confirm still does the old per-row dup-check-inside-qtx behavior, just without force-add). That's fine — Chunks 2 and 3 complete the backend.

### Task 1: Add `row_id` + `skip` fields to `importRow` and bump TTL

**Files:**
- Modify: `internal/api/import_handlers.go:35-44` (struct), `:50` (TTL constant)

- [ ] **Step 1.1: Edit the `importRow` struct**

Open `internal/api/import_handlers.go`. Replace the struct at line 35 with:

```go
// importRow represents a single parsed row from the Excel file. RowID is
// a stable 0-based positional index assigned at upload time by the preview
// builder; it never renumbers, and the PATCH endpoint uses it as the merge
// key so the frontend can address a row unambiguously after edits. Skip
// marks a row as excluded from the confirm-time insert loop.
type importRow struct {
	RowID            int     `json:"row_id"`
	Date             string  `json:"date"`
	Description      string  `json:"description"`
	Amount           float64 `json:"amount"`
	Category         string  `json:"category"`
	Tags             string  `json:"tags,omitempty"`
	Notes            string  `json:"notes,omitempty"`
	OriginalAmount   float64 `json:"original_amount,omitempty"`
	OriginalCurrency string  `json:"original_currency,omitempty"`
	Skip             bool    `json:"skip"`
}
```

- [ ] **Step 1.2: Populate `RowID` at upload time**

In `handleImportUpload`, the row-accumulator loop uses `ir` as the `importRow` local (see `internal/api/import_handlers.go:217`, where `ir := importRow{}` is declared inside the `for _, sec := range sections` body). The append at line 268 is:

```go
parsedRows = append(parsedRows, ir)
```

Replace that line with two lines — assign `RowID` right before appending, so the 0-based position is the index of the `ir` that is about to land in the slice:

```go
ir.RowID = len(parsedRows)
parsedRows = append(parsedRows, ir)
```

Confirm you're editing the correct append site (there is only one) with:

```bash
grep -n "parsedRows = append" internal/api/import_handlers.go
```

Expected: one match at `internal/api/import_handlers.go:268`.

- [ ] **Step 1.3: Bump TTL from 30min → 60min**

Edit line 50:

```go
// importTTL is how long an import entry stays valid before expiry. A full
// hour is fixed (not activity-based) to avoid a memory-leak class where an
// idle tab holds a session alive forever.
const importTTL = 60 * time.Minute
```

- [ ] **Step 1.4: Verify build still passes**

Run:

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go build ./..."
```

Expected: no output (build succeeds). Existing tests may now fail if any asserts on the row shape; we'll fix those in Task 3.

- [ ] **Step 1.5: Commit**

```bash
git add internal/api/import_handlers.go
git commit -m "feat(import): add row_id + skip fields to importRow, bump TTL to 60m"
```

### Task 2: Extract shared `loadImportEntryForUser` helper

**Files:**
- Modify: `internal/api/import_handlers.go` (new helper + two callsites at `:604-623` and `:727-738`)

- [ ] **Step 2.1: Add the helper**

Place this function just above `handleImportUpload` (above the existing handler block so all four future handlers can see it):

```go
// loadImportEntryForUser fetches an import entry from importStore, enforces
// ownership, and checks TTL expiry. On any failure it writes the
// appropriate HTTP error and returns ok=false — the caller must return
// immediately. Used by every handler that touches a specific import
// session (confirm, cancel, GET, PATCH) so the ownership/expiry contract
// lives in exactly one place.
func loadImportEntryForUser(w http.ResponseWriter, r *http.Request, importID string) (*importEntry, bool) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	if len(importID) != 32 {
		writeError(w, http.StatusBadRequest, "invalid import_id")
		return nil, false
	}
	val, found := importStore.Load(importID)
	if !found {
		writeError(w, http.StatusNotFound, "import not found or expired")
		return nil, false
	}
	entry := val.(*importEntry)
	if entry.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return nil, false
	}
	if time.Since(entry.CreatedAt) > importTTL {
		importStore.Delete(importID)
		writeError(w, http.StatusNotFound, "import not found or expired")
		return nil, false
	}
	return entry, true
}
```

**Note on status code unification:** the new helper collapses two existing divergent cases in `handleImportCancel` and `handleImportConfirm` into a single `404 not found` response.

1. **Expiry** — existing `handleImportConfirm` at line 621 returns `410 Gone` when `time.Since(entry.CreatedAt) > importTTL`; the helper returns `404`.
2. **Already-gone entries** — existing `handleImportCancel` at lines 727–731 returns `204 NoContent` when `importStore.Load` returns `!found` (it treats "already gone" as idempotent success); the helper returns `404`.

Both changes are silent at the test level — there is no test asserting either 410 or 204-on-missing — so the helper rewrite is an invisible protocol shift. The new unified `404` is consistent with the PATCH and GET endpoints coming in Chunks 2 and 3 (both of which use the same helper and must return 404 for missing sessions because they mutate/read state that must exist). If the frontend cancel path needs idempotent "already gone" behavior later, it can handle `404` the same way it would handle `204`: by treating cancel as complete and clearing local state.

- [ ] **Step 2.2: Rewrite `handleImportConfirm` to use the helper**

In `handleImportConfirm`, replace lines 587–623 (from `user, ok := auth.GetUser(r)` through the `if time.Since(entry.CreatedAt) > importTTL` block) with:

```go
	var req importConfirmRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	entry, ok := loadImportEntryForUser(w, r, req.ImportID)
	if !ok {
		return
	}
```

The existing user/import_id reads inside the handler body (e.g. `entry.Rows`, `entry.UserID`) keep working — `entry` is the same type.

- [ ] **Step 2.3: Rewrite `handleImportCancel` to use the helper**

Replace the body of `handleImportCancel` (lines 714–742) with:

```go
func (h *Handler) handleImportCancel(w http.ResponseWriter, r *http.Request) {
	importID := chi.URLParam(r, "importID")
	if _, ok := loadImportEntryForUser(w, r, importID); !ok {
		return
	}
	importStore.Delete(importID)
	w.WriteHeader(http.StatusNoContent)
}
```

We discard the returned `*importEntry` via `_` because cancel doesn't need to read any session fields — it just needs the ownership/expiry gate. Keeping the `_` inline avoids an unused-local-variable lint warning and makes the guard-and-delete pattern read as one unit.

Also update `router.go:198` to use `{importID}` instead of `{id}`:

```go
r.Delete("/import/{importID}", h.handleImportCancel)
```

- [ ] **Step 2.4: Verify no existing test locks in the old 410 expiry code**

Search for any test asserting 410:

```bash
grep -n "StatusGone\|http.Status.*410" internal/api/import_handlers_test.go
```

Expected: **no matches.** The current `handleImportConfirm` does return 410 on expiry, but no test pins that behavior, so the rename from 410 → 404 in Step 2.1's helper is an invisible shift in observable protocol. The build-and-test cycle in Step 2.5 will still prove the handler compiles and every test that does exercise confirm's happy path keeps passing. If this grep DOES hit unexpectedly, stop and read the hit — it's either a test that needs its expected status flipped to `http.StatusNotFound` (add comment `// 3.4b: unified with PATCH/GET expiry — both return 404, not 410`) or a test that was added after this plan was written and you need to re-evaluate.

Also search for any existing cancel test that uses the `{id}` URL param and update to `{importID}`:

```bash
grep -n "withUserAndURLParam.*\"id\"" internal/api/import_handlers_test.go
```

Expected: any matches must be updated to use the key `"importID"` so the `chi.URLParam(r, "importID")` lookup in the rewritten cancel handler finds the value. There is no other place in the test file that calls `chi.URLParam` directly — the test router is mocked through `withUserAndURLParam(s)`.

- [ ] **Step 2.5: Run Go tests**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImportConfirm|TestHandleImportCancel' -v"
```

Expected: all existing confirm/cancel tests pass (after the 410→404 rename and the URL param rename).

- [ ] **Step 2.6: Commit**

```bash
git add internal/api/import_handlers.go internal/api/router.go internal/api/import_handlers_test.go
git commit -m "refactor(import): extract loadImportEntryForUser helper, unify expiry to 404"
```

### Task 3: Remove the `force_add` code path entirely

**Files:**
- Modify: `internal/api/import_handlers.go` — delete `ForceAdd` field, `ForceAddSet`, `resolveForceAddSuffix`, `forceAddSuffixCap`, `errForceAddExhausted`, `skipReasonForceAddCollision`, and all force-add branches
- Modify: `internal/api/import_handlers_test.go` — delete force-add tests

- [ ] **Step 3.1: Identify force-add tests before deleting**

```bash
grep -n "ForceAdd\|force_add\|forceAdd" internal/api/import_handlers_test.go
```

List every hit. Anything that's purely asserting on force-add behavior (test function names containing "ForceAdd") is deleted. Test bodies that just happen to set `ForceAdd: nil` or `ForceAdd: []int{}` in their request payload lose only that field.

Also run the web-side grep to confirm the safety rationale in the spec:

```bash
grep -rn "force_add\|forceAdd" web/src/
```

Expected: **no matches**. This is the invariant that justifies the deletion — if this grep returns anything, stop and surface the finding before proceeding.

- [ ] **Step 3.2: Delete force-add tests**

For every test function whose name contains `ForceAdd`, delete the function body, its `t.Run` sub-tests if any, and any helper it's the only caller of. Use multi-line Edit operations so the diff is reviewable.

Then simplify the shared `uploadAndConfirmImport` helper at `internal/api/import_handlers_test.go:1110`. It currently has a trailing `forceAdd []int` parameter that writes `confirmBodyMap["force_add"] = forceAdd` at line 1147. Once the backend stops reading the `force_add` JSON field, a body containing it is silently ignored — but leaving a dead parameter confuses future readers. Apply this three-part edit:

1. Change the helper signature from:
   ```go
   func uploadAndConfirmImport(t *testing.T, h *Handler, user database.User, xlsxData []byte, forceAdd []int) map[string]any {
   ```
   to:
   ```go
   func uploadAndConfirmImport(t *testing.T, h *Handler, user database.User, xlsxData []byte) map[string]any {
   ```

2. Delete the `if forceAdd != nil { confirmBodyMap["force_add"] = forceAdd }` block at lines 1146–1148.

3. Update every caller to drop the trailing `nil` or slice argument:
   ```bash
   grep -n "uploadAndConfirmImport(" internal/api/import_handlers_test.go
   ```
   Every hit is a callsite that needs the last argument removed. Expected pattern: `uploadAndConfirmImport(t, h, user, xlsxData, nil)` → `uploadAndConfirmImport(t, h, user, xlsxData)`. There are no callers passing a non-nil slice outside the force-add tests being deleted, so every remaining call ends in `nil` and the fix is mechanical.

- [ ] **Step 3.3: Delete `resolveForceAddSuffix` and friends from `import_handlers.go`**

Delete in this order (easier-to-harder to preserve referential integrity mid-edit):

1. `forceAddSuffixCap` constant (~line 1132)
2. `resolveForceAddSuffix` function (~lines 1146–1167)
3. `errForceAddExhausted` var (~line 582)
4. `skipReasonForceAddCollision` constant from the `const` block (~line 497)
5. `ForceAdd []int \`json:"force_add"\`` field on `importConfirmRequest` (~line 473)
6. `ForceAddSet map[int]struct{}` field on `importProcessInput` (~line 571)
7. The `forceAddSet := make(map[int]struct{}, ...)` materialization loop in `handleImportConfirm` (~lines 643–651) — delete entirely
8. Inside `processImportRows`, remove the force-add branch (~lines 870–922). The surrounding context looks like:
   ```go
   _, forceAdd := in.ForceAddSet[i]
   if !forceAdd {
       // ordinary path
       ...
   } else {
       // force-add path — resolveForceAddSuffix
       ...
   }
   ```
   After the edit, the ordinary path becomes the only path. The resulting block is simply:
   ```go
   _, lookupErr := qtx.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true})
   if lookupErr == nil {
       result.Skipped = append(result.Skipped, importSkipped{
           RowIndex: i,
           Reason:   skipReasonDuplicate,
       })
       continue
   }
   if !errors.Is(lookupErr, sql.ErrNoRows) {
       log.Printf("import: content hash lookup failed (row=%d): %v", i, lookupErr)
       result.Errored = append(result.Errored, importErrored{
           RowIndex: i,
           Reason:   sanitizeLogValue(lookupErr.Error()),
       })
       continue
   }
   ```

- [ ] **Step 3.4: Update callsite in `handleImportConfirm` that builds `importProcessInput`**

Find the `processImportRows(...)` call (~line 669). Remove the `ForceAddSet: forceAddSet,` line from the struct literal. Confirm the struct literal still compiles by cross-referencing against the updated `importProcessInput` definition.

- [ ] **Step 3.5: Clean up `import_handlers_property_test.go`**

The property-test file has five load-bearing force-add references that MUST be edited together or the file will not compile once `ForceAddSet`, `skipReasonForceAddCollision`, and `errForceAddExhausted` are gone. Start by grepping:

```bash
grep -n "ForceAdd\|force_add\|skipReasonForceAddCollision\|errForceAddExhausted" internal/api/import_handlers_property_test.go
```

Expected hits (exact line numbers):

1. **Line 128** — inside `propertyFixture.runProcess`'s `importProcessInput` literal. The field `ForceAddSet: nil,` must be deleted once `ForceAddSet` is removed from `importProcessInput`; otherwise the literal references a non-existent field.

2. **Line 365** — inside `TestImportProperty_NoSilentDrops`, the `validReasons` map contains an entry `skipReasonForceAddCollision: {},`. Delete this entry — the constant no longer exists. The map remaining is `{skipReasonDuplicate: {}, skipReasonNegativeAmount: {}, ...}` minus the force-add entry.

3. **Line 611** — inside the `force_add_collision_cap_exhausted` subtest, an `importProcessInput` literal contains `ForceAddSet: map[int]struct{}{0: {}},`. This reference is deleted as part of deleting the entire subtest in the next sub-step.

4. **Lines 564–618** — the entire `t.Run("force_add_collision_cap_exhausted", func(t *testing.T) { ... })` block (inside the larger `TestImportProperty_ReasonDiscipline` or equivalent parent test — read lines 560–562 to see the parent). Delete the whole `t.Run` block including the opening parenthesis of `t.Run(...)` through the closing `})` on line 618. The subtests above and below it in the same parent (`skipReasonDuplicate` just before, `errored_unknown_category_id` just after) stay untouched.

5. **Lines 112, 213, 438, 461** — comment-only references (explanatory prose that mentions `ForceAddSet`, `skipReasonForceAddCollision`, or `forceAddSet`). These are not build-critical — Go ignores comments — but update the text so the file's documentation matches the code. A light-touch rewrite: delete any clause inside a comment that names a force-add concept; leave surrounding prose intact. For example, the comment at line 213 currently reads `//   - skipReasonDuplicate and skipReasonForceAddCollision would` — trim to `//   - skipReasonDuplicate would`.

After the edits, re-run the same grep. Expected: **no matches.**

- [ ] **Step 3.6: Build + run all import tests**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go build ./... && go test ./internal/api -run 'TestHandleImport|TestImport|TestProcessImport' -v"
```

Expected: build passes, all remaining import tests pass, zero references to `ForceAdd`/`forceAdd`/`force_add`/`skipReasonForceAddCollision` anywhere in `internal/api/`.

Verify with:

```bash
grep -rn "ForceAdd\|force_add\|skipReasonForceAddCollision\|resolveForceAddSuffix\|forceAddSuffixCap\|errForceAddExhausted" internal/api/
```

Expected: **no matches.**

- [ ] **Step 3.7: Commit**

```bash
git add internal/api/import_handlers.go internal/api/import_handlers_test.go internal/api/import_handlers_property_test.go
git commit -m "refactor(import): remove dead force_add code path"
```

### Task 4: Define `collisionGroup` / `dbMatchPreview` types + `buildCollisionGroups`

**Files:**
- Modify: `internal/api/import_handlers.go` — new types and function, placed right after the `importRow` struct

- [ ] **Step 4.1: Add the types**

Right below the `importRow` struct (after line 44, before `importStore`), add:

```go
// dbMatchPreview carries the displayable fields of a live DB row that
// collides with an import row's content hash. Sent inline inside a
// collisionGroup so the frontend can render "you're about to re-import
// this existing transaction" context without a second round-trip. Populated
// only for groups whose reason is "db_match".
type dbMatchPreview struct {
	ID           int64  `json:"id"`
	Date         string `json:"date"`
	Description  string `json:"description"`
	AmountCents  int64  `json:"amount_cents"`
	CategoryName string `json:"category_name"`
}

// collisionGroup is a set of row_ids within one import session that share
// the same content hash. Reason is "intra_file" when the group is composed
// entirely of preview rows; "db_match" when at least one member row's hash
// also matches a live DB transaction (in which case DBMatch is populated).
// Groups of size 1 are never emitted — a single clean row is not a
// collision.
type collisionGroup struct {
	GroupID      string          `json:"group_id"`
	Reason       string          `json:"reason"`
	MemberRowIDs []int           `json:"member_row_ids"`
	DBMatch      *dbMatchPreview `json:"db_match,omitempty"`
}
```

- [ ] **Step 4.2: Add `buildCollisionGroups` right below the types**

```go
// buildCollisionGroups computes the collision view for a preview session.
// It groups rows by their resolved content hash, then flags each group as
// intra_file (multiple preview rows sharing a hash with no DB match) or
// db_match (at least one preview row whose hash also matches a live DB
// transaction). Rows marked Skip are EXCLUDED from group membership: a
// skipped row can't collide with anyone because it isn't going to be
// inserted, so counting it would make the progress meter lie.
//
// Rows that fail to resolve to a valid hash — unparseable date, empty
// description, zero amount, unresolved category — are silently omitted
// from grouping. They'll still be rejected at confirm time by
// processImportRows; the preview's job here is to flag collisions, not
// to re-implement the full row validator.
//
// Called from two places:
//  1. handleImportUpload, once at upload time, to seed the initial
//     collision_groups field on the preview response.
//  2. handleImportPatchRow, once per PATCH, to recompute groups after any
//     field edit. The PATCH handler passes the session's mutated row slice.
//
// The DB lookup is O(rows) — one GetTransactionByContentHash call per
// hash-resolvable row. This is the same cost as the old predictDuplicateSkips,
// just producing a richer output shape. Callers MUST pass the already-loaded
// category lookups so the hash formula uses the canonical DB category name
// (matching handleImportConfirm exactly).
func buildCollisionGroups(
	ctx context.Context,
	queries *database.Queries,
	rows []importRow,
	categoryMap map[string]int64, // optional: user's resolved name->id map from confirm flow; nil at upload time
	defaultCategoryID int64, // optional: user's chosen default at confirm time; 0 at upload time
	catNameToID map[string]int64,
	catIDToName map[int64]string,
) ([]collisionGroup, error) {
	byHash := make(map[string][]int) // hash -> member row_ids

	// Hashable rows keep their row_id; non-hashable rows are skipped.
	for _, row := range rows {
		if row.Skip {
			continue
		}
		if row.Description == "" || row.Amount == 0 {
			continue
		}
		date, err := parseImportDate(row.Date)
		if err != nil {
			continue
		}
		categoryID := resolveCategoryID(row.Category, categoryMap, catNameToID, defaultCategoryID)
		if categoryID == 0 {
			continue
		}
		canonical, ok := catIDToName[categoryID]
		if !ok {
			continue
		}
		hash := database.ComputeContentHash(
			date,
			dollarsToCents(math.Abs(row.Amount)),
			row.Description,
			canonical,
		)
		byHash[hash] = append(byHash[hash], row.RowID)
	}

	// Emit groups. For each hash with ≥2 members, it's at minimum intra_file.
	// For any hash that also matches a live DB row, we upgrade to db_match
	// and attach the DB row's displayable fields.
	groups := []collisionGroup{}
	for hash, members := range byHash {
		// Look up DB match first (single row by hash). Even a size-1 group
		// graduates to a collision if it matches an existing DB row.
		var dbMatch *dbMatchPreview
		existing, err := queries.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			// A DB fault during hash lookup is a systemic error — return it
			// to the caller so the HTTP handler can 500. Silently dropping
			// the row (the predictDuplicateSkips behavior) was fine when
			// the only consequence was a missing UI hint, but now the
			// collision grouping is load-bearing for blocking /confirm.
			return nil, fmt.Errorf("lookup content hash during grouping: %w", err)
		}
		if err == nil {
			// DB category name is carried forward from catIDToName so the
			// frontend's inline preview matches what the transactions page
			// would show. CategoryID is non-nullable in the live schema
			// (the partial unique index filters content_hash IS NOT NULL,
			// and every hashable live row has a category), so the lookup
			// uses a plain int64 key — no sql.NullInt64 unwrapping.
			dbCatName := ""
			if name, ok := catIDToName[existing.CategoryID]; ok {
				dbCatName = name
			}
			dbMatch = &dbMatchPreview{
				ID:           existing.ID,
				Date:         existing.Date.Format("2006-01-02"),
				Description:  existing.Description,
				AmountCents:  existing.AmountCents,
				CategoryName: dbCatName,
			}
		}

		// A size-1 group is only a collision if it db_matches. Size-≥2 is
		// always a collision.
		if len(members) < 2 && dbMatch == nil {
			continue
		}

		reason := "intra_file"
		if dbMatch != nil {
			reason = "db_match"
		}

		groups = append(groups, collisionGroup{
			GroupID:      "g_" + hash[:8],
			Reason:       reason,
			MemberRowIDs: append([]int(nil), members...), // defensive copy
			DBMatch:      dbMatch,
		})
	}

	// Stable-ish ordering for determinism in tests: sort by the smallest
	// member row_id so the first collision group in the response is always
	// the one whose first row appears earliest in the sheet.
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].MemberRowIDs[0] < groups[j].MemberRowIDs[0]
	})

	return groups, nil
}
```

- [ ] **Step 4.3: Add `"sort"` to the import block if needed**

At the top of `import_handlers.go`, check that `"sort"` is present. If not, add it.

```bash
grep -n '"sort"' internal/api/import_handlers.go
```

- [ ] **Step 4.4: Verify `GetTransactionByContentHash` returns the fields used above**

Read the row type at `internal/database/queries.sql.go:549-556` to confirm the expected shape:

```go
type GetTransactionByContentHashRow struct {
    ID          int64          `json:"id"`
    Date        time.Time      `json:"date"`
    AmountCents int64          `json:"amount_cents"`
    Description string         `json:"description"`
    CategoryID  int64          `json:"category_id"`
    ContentHash sql.NullString `json:"content_hash"`
}
```

Expected: every field referenced in Step 4.2's `dbMatch` assignment (`ID`, `Date`, `Description`, `AmountCents`, `CategoryID`) is present. `CategoryID` is non-nullable (plain `int64`), so the code reads it directly without any `.Valid`/`.Int64` unwrapping. If you discover that the actual generated type differs from the above (e.g. someone rewrote `queries.sql:182-197` between plan authoring and execution), stop and read the `.sql` source — the fix is to re-add the missing column to the `SELECT` list and run `sqlc generate`. If `sqlc` is not available locally (`which sqlc` returns nothing), edit the generated `.go` file directly and leave a `// TODO(plan): re-run sqlc generate` comment on the struct.

- [ ] **Step 4.5: Build-only check (no test yet)**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go build ./..."
```

Expected: build passes. We haven't called `buildCollisionGroups` from anywhere yet, so the function is unused — add a silencing usage in Task 5 before the `go vet -unusedresult` equivalent complains. In practice the function is exported-case (lowercase first letter) but used as a package-private helper, so Go's dead-code check won't trip.

- [ ] **Step 4.6: Commit**

```bash
git add internal/api/import_handlers.go internal/database/queries.sql
git commit -m "feat(import): add collisionGroup type and buildCollisionGroups function"
```

### Task 5: Wire `buildCollisionGroups` into upload preview, remove `predictDuplicateSkips`

**Files:**
- Modify: `internal/api/import_handlers.go`
  - Delete `predictDuplicateSkips` function and `predictedSkip` type
  - Replace `handleImportUpload` response shape: `predicted_skips` → `collision_groups`

- [ ] **Step 5.1: Delete `predictDuplicateSkips` and `predictedSkip`**

Delete `predictedSkip` type (~lines 434–457) and `predictDuplicateSkips` function (~lines 348–432). Verify with:

```bash
grep -n "predictDuplicateSkips\|predictedSkip\|predicted_skips" internal/api/import_handlers.go
```

Expected hits:
1. One or more matches inside `handleImportUpload` where the old code still refers to them — those call sites are fixed in Step 5.2.
2. **One stale comment at `internal/api/import_handlers.go:486`** on the `importSkipReason` type: `// used by predictedSkip on the upload preview path,`. Update this line to read `// used by collisionGroup on the upload preview path,` — the `importSkipReason` enum is still load-bearing for confirm-time outcomes, but its upload-preview consumer has changed from the deleted `predictedSkip` type to the new `collisionGroup` type. Leaving the stale reference would trip future code searches.

After Step 5.2 completes, the same grep should return **no matches** anywhere in `import_handlers.go`.

- [ ] **Step 5.2: Rewrite the upload response composition**

Locate `handleImportUpload` near line 315 where it builds `uniqueCategories`. Replace the block from `// Phase 3.4 upload-time duplicate prediction` (~line 327) through the `writeJSON(w, http.StatusOK, ...)` call (~line 345) with:

```go
	// Phase 3.4b: the upload preview computes collision_groups via
	// buildCollisionGroups. Unlike the old predictDuplicateSkips, this pass
	// detects intra-file collisions (the 20-identical-Starbucks case) in
	// addition to DB matches, and returns them in a shape the editable
	// preview table can consume directly. Categories are loaded here so
	// buildCollisionGroups uses the same canonical name resolution as the
	// confirm path.
	existingCats, err := h.queries.ListAllCategories(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	catIDToName := make(map[int64]string, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
		catIDToName[c.ID] = c.Name
	}

	groups, err := buildCollisionGroups(
		r.Context(),
		h.queries,
		parsedRows,
		nil, // categoryMap not chosen yet at upload time
		0,   // defaultCategoryID not chosen yet either
		catNameToID,
		catIDToName,
	)
	if err != nil {
		log.Printf("import upload: build collision groups: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to compute collision groups")
		return
	}

	// Start background cleanup (idempotent via sync.Once)
	startImportCleanup()

	writeJSON(w, http.StatusOK, map[string]any{
		"import_id":         importID,
		"row_count":         len(parsedRows),
		"rows":              parsedRows,
		"columns":           detectedColumns,
		"unique_categories": uniqueCategories,
		"collision_groups":  groups,
	})
}
```

- [ ] **Step 5.3: Move `importStore.Store` before the collision-group build**

The collision-group DB lookup takes context, so we want the store slot reserved BEFORE the lookup runs — otherwise a slow DB would let a user hammer uploads. Double-check the code order: `importStore.Store` (line 305 in the original) should happen before `buildCollisionGroups`. If the new code in Step 5.2 has them in the wrong order, swap.

- [ ] **Step 5.4: Rewrite the existing `PredictedSkips` tests to assert on `collision_groups`**

Two existing tests assert specifically on the old `predicted_skips` shape and must be updated. Find them:

```bash
grep -n "predicted_skips\|PredictedSkips" internal/api/import_handlers_test.go
```

Expected hits:

1. **`TestHandleImportUpload_PredictedSkips_ReflectsExistingRow` at lines 1290–1367**

   This test seeds two rows via `uploadAndConfirmImport`, uploads the same file a second time, then asserts that `predicted_skips` contains two entries with `{row_index, reason: "duplicate", existing_id}`. The new shape groups by content hash, so the two distinct rows (different descriptions/amounts) produce **two separate size-1 collision groups with reason `"db_match"`** — not one group-of-2. This is because `buildCollisionGroups` emits a group whenever a row's hash matches a live DB row, even if that group has only one preview-side member (see the `len(members) < 2 && dbMatch == nil` clause in Step 4.2 — a size-1 group survives precisely when `dbMatch != nil`).

   Rename the test to `TestHandleImportUpload_CollisionGroups_ReflectsExistingRows` and rewrite the assertion block (the `raw, ok := resp["predicted_skips"]` block at lines 1330–1353) to:

   ```go
   rawGroups, ok := resp["collision_groups"].([]any)
   if !ok {
       t.Fatalf("expected collision_groups in response, got %T", resp["collision_groups"])
   }
   if len(rawGroups) != 2 {
       t.Fatalf("expected 2 collision_groups (one per seeded row), got %d: %v", len(rawGroups), rawGroups)
   }
   for _, rg := range rawGroups {
       g := rg.(map[string]any)
       if g["reason"].(string) != "db_match" {
           t.Errorf("expected reason=db_match, got %v", g["reason"])
       }
       members := g["member_row_ids"].([]any)
       if len(members) != 1 {
           t.Errorf("expected 1 member_row_id (single preview row matching a live DB row), got %d", len(members))
       }
       dbMatch, ok := g["db_match"].(map[string]any)
       if !ok {
           t.Fatalf("expected db_match payload, got %T", g["db_match"])
       }
       if int64(dbMatch["id"].(float64)) == 0 {
           t.Errorf("expected non-zero db_match.id (preserves the existing_id invariant)")
       }
   }
   ```

   The live-row count sanity check at lines 1358–1366 (`SELECT COUNT(*) FROM transactions WHERE deleted_at IS NULL`) stays as-is — it's still asserting a valid invariant.

   Update the test's doc comment at line 1290–1298 to reference `collision_groups` and drop the "force-add checkbox" phrasing (that feature no longer exists): the comment now reads as a contract for DB-match grouping parity.

   Also update the seed call at line 1314 from `uploadAndConfirmImport(t, h, user, xlsxData, nil)` to `uploadAndConfirmImport(t, h, user, xlsxData)` (matching Step 3.2's helper signature change).

2. **`TestHandleImportUpload_PredictedSkips_IgnoresTombstoned` at lines 1369–1422** — **DELETE this entire test.**

   This test's invariant ("a tombstoned DB row with the same hash must not surface as a predicted skip") is now covered by the new `TestHandleImportUpload_HidesTombstonedFromDbMatch` in Step 6.2, which asserts the same soft-delete interaction against the new `collision_groups` shape. Keeping both is pure duplication — the new test seeds the tombstone deterministically via raw UPDATE, while this older test uses `uploadAndConfirmImport` + a follow-up UPDATE. The new one is the more precise formulation.

   Delete the entire function body (lines 1369 through 1422 inclusive) and add a one-line comment where the function used to live: `// TestHandleImportUpload_PredictedSkips_IgnoresTombstoned was deleted in 3.4b — superseded by TestHandleImportUpload_HidesTombstonedFromDbMatch which asserts the same tombstone invariant against collision_groups.`

After editing, run the upload test suite to confirm:

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run TestHandleImportUpload -v"
```

Expected: the rewritten `TestHandleImportUpload_CollisionGroups_ReflectsExistingRows` passes, the deleted `PredictedSkips_IgnoresTombstoned` no longer exists, and `TestHandleImportUpload_ValidFile_ReturnsPreview` still passes (it only asserts on `import_id`, `row_count`, `rows`, `columns`, `unique_categories` — none of which changed). A second grep confirms no stragglers:

```bash
grep -n "predicted_skips\|PredictedSkips" internal/api/import_handlers_test.go
```

Expected: **no matches.**

- [ ] **Step 5.5: Commit**

```bash
git add internal/api/import_handlers.go internal/api/import_handlers_test.go
git commit -m "feat(import): emit collision_groups from upload preview (replaces predicted_skips)"
```

### Task 6: Tests #8 Part A (tombstone upload parity) + #9 (intra-file grouping + baseline)

**Files:**
- Modify: `internal/api/import_handlers_test.go` — add two new test functions

- [ ] **Step 6.1: Write test #9 — `TestHandleImportUpload_IntraFileCollisionGroup`**

Add this function at the bottom of `import_handlers_test.go`:

```go
// TestHandleImportUpload_IntraFileCollisionGroup is spec test #9 (baseline):
// uploading three identical rows produces exactly one collision_group of
// size 3 with reason intra_file. The initial grouping is the foundation
// every PATCH test depends on — if this breaks, nothing else works.
func TestHandleImportUpload_IntraFileCollisionGroup(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "grouping", "member")

	// Ensure the "Food" category exists so the hash can resolve at
	// upload-time grouping (buildCollisionGroups requires a resolvable
	// category name).
	if _, err := q.CreateCategory(context.Background(), database.CreateCategoryParams{
		Name: "Food",
		Type: "expense",
	}); err != nil {
		t.Fatalf("create category: %v", err)
	}

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-07", "Starbucks", "5.00", "Food"},
		{"2026-01-07", "Starbucks", "5.00", "Food"},
		{"2026-01-07", "Starbucks", "5.00", "Food"},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	rawGroups, ok := resp["collision_groups"].([]any)
	if !ok {
		t.Fatalf("collision_groups missing or wrong type: %v", resp["collision_groups"])
	}
	if len(rawGroups) != 1 {
		t.Fatalf("expected 1 collision_group, got %d", len(rawGroups))
	}
	g := rawGroups[0].(map[string]any)
	if g["reason"].(string) != "intra_file" {
		t.Errorf("expected reason intra_file, got %v", g["reason"])
	}
	members := g["member_row_ids"].([]any)
	if len(members) != 3 {
		t.Errorf("expected 3 member_row_ids, got %d", len(members))
	}
	// Rows must carry row_id and it must equal their slice index.
	rows := resp["rows"].([]any)
	for i, r := range rows {
		m := r.(map[string]any)
		if int(m["row_id"].(float64)) != i {
			t.Errorf("row %d has row_id %v, want %d", i, m["row_id"], i)
		}
	}
}
```

- [ ] **Step 6.2: Write test #8 Part A — `TestHandleImportUpload_HidesTombstonedFromDbMatch`**

```go
// TestHandleImportUpload_HidesTombstonedFromDbMatch is spec test #8 Part A:
// a tombstoned DB row with the same content_hash as an uploaded row must
// NOT be flagged as a db_match collision. The partial unique index already
// filters out tombstoned rows (WHERE deleted_at IS NULL), so
// GetTransactionByContentHash returns sql.ErrNoRows and the uploaded row
// stays clean. This test pins that invariant so a future "performance"
// rewrite can't silently drop the filter.
//
// Seeds use amount=999 as a sentinel so a regression is easy to read in
// the failure message — any production row with that exact amount in a
// household DB is vanishingly unlikely.
func TestHandleImportUpload_HidesTombstonedFromDbMatch(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "tombstoned", "member")

	cat, err := q.CreateCategory(context.Background(), database.CreateCategoryParams{
		Name: "Food",
		Type: "expense",
	})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	date := time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)
	hash := database.ComputeContentHash(date, 99900, "Starbucks", "Food")

	// Insert a tombstoned row with this hash. It must not be discoverable
	// by GetTransactionByContentHash (the query filters deleted_at IS NULL)
	// which is what buildCollisionGroups relies on.
	created, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		Date:        date,
		Amount:      999.00,
		AmountCents: 99900,
		Description: "Starbucks",
		CategoryID:  cat.ID,
		ContentHash: sql.NullString{String: hash, Valid: true},
	})
	if err != nil {
		t.Fatalf("create tombstone row: %v", err)
	}
	// Tombstone via raw UPDATE on purpose: we need the deleted_at shape
	// without the audit side-effect of a real soft-delete, so this
	// deliberately bypasses TransactionStore. The test asserts on the
	// query layer (content-hash lookup respecting deleted_at IS NULL),
	// not on the store layer's audit contract.
	if _, err := db.Exec("UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", created.ID); err != nil {
		t.Fatalf("tombstone row: %v", err)
	}

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-07", "Starbucks", "999.00", "Food"},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rec, &resp)
	groups := resp["collision_groups"].([]any)
	if len(groups) != 0 {
		t.Errorf("expected 0 collision_groups (tombstoned row should not match), got %d: %v", len(groups), groups)
	}
}
```

Note: the test imports `database/sql` for `sql.NullString`. Confirm the test file already imports it; if not add to the import block. Same for `time`.

- [ ] **Step 6.3: Run both new tests**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImportUpload_IntraFileCollisionGroup|TestHandleImportUpload_HidesTombstonedFromDbMatch' -v"
```

Expected: both PASS. If the tombstone test fails with a non-empty `collision_groups`, the `GetTransactionByContentHash` query is not filtering `deleted_at IS NULL` — grep `queries.sql:182-197` to confirm the `WHERE` clause.

- [ ] **Step 6.4: Run the full import test suite to catch regressions**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run TestHandleImport -v"
```

Expected: all tests pass. Any failure here is likely an existing test asserting on `predicted_skips` that Step 5.4's grep missed — fix and re-run.

- [ ] **Step 6.5: Commit**

```bash
git add internal/api/import_handlers_test.go
git commit -m "test(import): upload preview emits collision_groups, hides tombstoned matches"
```

---

## Chunk 2: PATCH Endpoint

This chunk adds the PATCH write path that Phase 3.4b's inline edit UX depends on. After Chunk 1, the upload preview emits `collision_groups` but there is no way to change a row once it lands in the session — the only mutation today is confirm (inserts everything) or cancel (drops everything). This chunk introduces a single edit-one-field endpoint, the shared validator that enforces per-field rules, the wiring to re-run `buildCollisionGroups` after each edit, and the backend half of the PATCH tests in the Testing Strategy.

**What this chunk ships:**
- `validateImportField(field, value)` — pure helper returning `(normalized any, errCode string, message string)`. Reuses `parseImportDate`, `parseImportAmount`, and `limits.MaxDescriptionLength`. Centralizes the Validation field rules table from the spec into one place so both PATCH and any future edit entry point share the same contract.
- `handleImportPatchRow` — the new HTTP handler at `PATCH /api/import/{importID}/rows/{rowID}`. Loads the session via the shared `loadImportEntryForUser` helper from Chunk 1, decodes `{field, value}`, calls the validator, mutates the in-session row via a pointer into `entry.Rows`, rebuilds `collision_groups` with `buildCollisionGroups`, and returns the full snapshot `{rows, collision_groups}` (not a sparse diff).
- Route registration in `router.go`.
- Four spec-numbered tests: #1 happy path group-of-3, #2 re-collision + skip stickiness, #3 expired session 404, #4 whitespace/case hash parity.
- Two spec-numbered tests tied to the PATCH path's edge contracts: #8 Part B (tombstone hiding during PATCH re-check) and #9's amount-mode parity case (upload empty amount → 0 silently, PATCH empty → 400).

**What this chunk does NOT ship (deferred to Chunk 3):**
- `GET /api/import/{importID}` resume endpoint
- The `UNRESOLVED_COLLISIONS` 409 response on confirm
- Skip-excluded insert path
- Confirm happy path + skipped-rows-excluded tests (#5, #6, #7)

**Per-chunk verification bar:** at the end of this chunk `go build ./...`, `go vet ./...`, and `go test ./internal/api -run 'TestHandleImport' -v` all pass. The PATCH endpoint is fully exercised by tests #1, #2, #3, #4, #8B, and #9B; confirm still goes through its Chunk-1 behavior (no collision-gate yet) and its own tests from the existing suite stay green.

### Task 7: Add `validateImportField` shared validator

**Files:**
- Modify: `internal/api/import_handlers.go` — add new validator function, placed just below `buildCollisionGroups` from Chunk 1 Task 4

- [ ] **Step 7.1: Add the validator function**

Place this function immediately after `buildCollisionGroups` (i.e. right before the section of the file that currently contains `handleImportConfirm`). It is a pure function with no DB or HTTP dependencies — the PATCH handler in Task 8 is the only caller, but keeping it separate means a future GET-session or undo endpoint can reuse it without refactoring.

```go
// validateImportField normalizes and validates one field of an import row for
// the PATCH endpoint. Returns the parsed canonical value (type varies by
// field: time.Time for date, string for description, float64 dollars for
// amount, bool for skip), plus an error code + user-facing message on
// failure. A non-empty errCode means the caller should write HTTP 400 with
// the error body {code, field, message}. An empty errCode means validation
// passed and normalized is safe to assign.
//
// Per-field rules (matches the spec's Validation field rules table):
//
//   date: non-empty, parseable via parseImportDate (both Excel serial and
//         the text layouts), in [minImportYear, maxImportYear]. Empty or
//         unparseable → INVALID_DATE. Reuses the exact parse path that
//         handleImportUpload uses so the normalize-then-hash step lands on
//         the same canonical date both at upload and PATCH time.
//
//   description: non-empty after TrimSpace, length ≤ MaxDescriptionLength
//         (500, defined in limits.go). Trimmed string is returned as the
//         normalized value — the caller assigns it directly to row.Description
//         so subsequent re-hashes see the canonical form. The trim happens
//         here (not only inside ComputeContentHash) because the stored row
//         value is also what the frontend displays: we want "Starbucks" in
//         the UI after a user types " Starbucks ", not the raw input.
//
//   amount: non-empty, parseable via parseImportAmount (strips currency
//         formatting, rejects NaN/Inf, enforces MaxTransactionAmount
//         magnitude). An empty amount at PATCH time is a HARD error
//         (INVALID_AMOUNT) — this is the edit-mode parity case from test
//         #9: upload silently coerces empty → 0 so the row lands in the
//         preview (skipped from confirm as "negative amount"), but PATCH
//         does not get to silently zero a cell the user is actively
//         editing. Returning 400 forces the frontend to surface an inline
//         error so the user knows the edit did not take effect.
//
//   skip: strict bool. Any non-bool JSON value → INVALID_FIELD. No
//         normalization — the value is passed through untouched.
//
// Unknown field names → INVALID_FIELD with a message naming the field.
// This is the only path that returns INVALID_FIELD; every other failure
// has a field-specific code so the frontend can color-code the originating
// cell without parsing the message.
func validateImportField(field string, value any) (normalized any, errCode string, message string) {
	switch field {
	case "date":
		s, ok := value.(string)
		if !ok {
			return nil, "INVALID_DATE", "date must be a string"
		}
		t, err := parseImportDate(s)
		if err != nil {
			return nil, "INVALID_DATE", "date is not parseable or out of range [1900, 2100]"
		}
		return t, "", ""

	case "description":
		s, ok := value.(string)
		if !ok {
			return nil, "INVALID_DESCRIPTION", "description must be a string"
		}
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return nil, "INVALID_DESCRIPTION", "description cannot be empty"
		}
		if len(trimmed) > MaxDescriptionLength {
			return nil, "INVALID_DESCRIPTION", fmt.Sprintf("description exceeds %d characters", MaxDescriptionLength)
		}
		return trimmed, "", ""

	case "amount":
		s, ok := value.(string)
		if !ok {
			return nil, "INVALID_AMOUNT", "amount must be a string"
		}
		if strings.TrimSpace(s) == "" {
			return nil, "INVALID_AMOUNT", "amount cannot be empty"
		}
		cents, err := parseImportAmount(s)
		if err != nil {
			return nil, "INVALID_AMOUNT", "amount is not a valid number"
		}
		// Return dollars so the caller can assign directly to row.Amount
		// (which is declared as float64 dollars, not int64 cents). The
		// ComputeContentHash caller inside buildCollisionGroups multiplies
		// back to cents via dollarsToCents(math.Abs(row.Amount)), so the
		// round-trip is lossless for the values that parseImportAmount
		// accepts (it already rejects magnitudes above MaxTransactionAmount
		// and NaN/Inf).
		return float64(cents) / 100.0, "", ""

	case "skip":
		b, ok := value.(bool)
		if !ok {
			return nil, "INVALID_FIELD", "skip must be a boolean"
		}
		return b, "", ""
	}
	return nil, "INVALID_FIELD", fmt.Sprintf("unknown field: %q", field)
}
```

- [ ] **Step 7.2: Verify `"fmt"` and `"strings"` are already imported**

Both are used throughout `import_handlers.go`; this function adds no new dependencies. Double-check with:

```bash
grep -n '^\s*"fmt"\|^\s*"strings"' internal/api/import_handlers.go
```

Expected: both present. If either is missing (should never happen at this point — `handleImportUpload` already uses both), add it via Edit to the import block at the top of the file.

- [ ] **Step 7.3: Build-only check**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go build ./..."
```

Expected: build passes. The function is currently unused but Go's dead-code check does not trip on package-private helpers, so the build stays clean until Task 8 wires it up.

- [ ] **Step 7.4: Commit**

```bash
git add internal/api/import_handlers.go
git commit -m "feat(import): add validateImportField shared PATCH validator"
```

### Task 8: Add `handleImportPatchRow` handler + register PATCH route

**Files:**
- Modify: `internal/api/import_handlers.go` — add new handler, placed just below `handleImportCancel` (keeps the four lifecycle handlers adjacent: upload → confirm → cancel → patch)
- Modify: `internal/api/router.go:195-198` — register `PATCH /import/{importID}/rows/{rowID}`

- [ ] **Step 8.1: Add the handler**

Immediately below `handleImportCancel` (rewritten in Chunk 1 Task 2.3), append:

```go
// patchImportRowRequest is the JSON body shape for PATCH /api/import/{importID}/rows/{rowID}.
// Field is one of "date", "description", "amount", "skip" — validated by
// validateImportField. Value is typed as any so the JSON decoder accepts
// both string (for date/description/amount) and bool (for skip) without
// a second layer of per-field request structs.
type patchImportRowRequest struct {
	Field string `json:"field"`
	Value any    `json:"value"`
}

// patchImportRowErrorBody is the 400 response shape. Code is a stable
// machine-readable constant (INVALID_DATE, INVALID_DESCRIPTION,
// INVALID_AMOUNT, INVALID_FIELD) so the frontend can color-code the
// originating cell without parsing the message. Field echoes back the
// request field so the frontend cellErrors map can key on row_id:field
// without a second round-trip.
type patchImportRowErrorBody struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// handleImportPatchRow applies one field edit to one row in a pending
// import session, rebuilds the collision_groups view, and returns a full
// snapshot of {rows, collision_groups}. The endpoint is PATCH (not PUT)
// because it takes exactly one field at a time — the frontend debounces
// multi-field edits into sequential PATCHes via its enqueuePatch promise
// chain, so there is no "edit several fields atomically" need.
//
// Response shape is intentionally NOT a sparse diff: even a single-field
// edit returns the full row list plus the full groups list. Sparse diffs
// would require the frontend to reconcile a partial update into component
// state, which is the class of bug the "styling is always derived from the
// latest server response, never from stale local state" rule is designed
// to prevent. Full snapshots are trivially mergeable via Array.map
// preserving object identity for unchanged rows (see the frontend hook in
// Chunks 4–5).
//
// The re-hash + re-group happens on EVERY edit, even for field="skip",
// because a skip flip changes which rows participate in collision
// grouping (skipped rows are excluded from buildCollisionGroups, so
// toggling skip can collapse or expand a group). Computing a "this field
// does not affect grouping, skip the rebuild" optimization would add a
// branch for one saved DB lookup per skip toggle, which is not worth the
// surface area.
//
// Errors:
//   400 invalid request body       — JSON decode failed or field/value missing
//   400 {code, field, message}     — validateImportField rejected the input
//   400 invalid row_id             — rowID URL param not parseable as an int
//   400 row_id out of range        — rowID outside [0, len(entry.Rows))
//   401/403/404                    — via loadImportEntryForUser (unauthorized,
//                                    wrong user, missing/expired session)
//   500 failed to rebuild groups   — buildCollisionGroups returned a DB fault
func (h *Handler) handleImportPatchRow(w http.ResponseWriter, r *http.Request) {
	importID := chi.URLParam(r, "importID")
	rowIDStr := chi.URLParam(r, "rowID")
	rowID, err := strconv.Atoi(rowIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid row_id")
		return
	}

	entry, ok := loadImportEntryForUser(w, r, importID)
	if !ok {
		return
	}

	if rowID < 0 || rowID >= len(entry.Rows) {
		writeError(w, http.StatusBadRequest, "row_id out of range")
		return
	}

	var req patchImportRowRequest
	if decodeErr := decodeJSON(w, r, &req); decodeErr != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Field == "" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	normalized, errCode, message := validateImportField(req.Field, req.Value)
	if errCode != "" {
		writeJSON(w, http.StatusBadRequest, patchImportRowErrorBody{
			Code:    errCode,
			Field:   req.Field,
			Message: message,
		})
		return
	}

	// Mutate the row in place via a pointer into entry.Rows. We do NOT
	// take a copy, edit the copy, and re-assign by index — that pattern
	// works but reads as "two writes where there is really one", which
	// makes the locking/concurrency story harder to review. The PATCH
	// serialization story (spec § Cross-row PATCH race) is "the frontend
	// promise chain guarantees at-most-one in-flight PATCH per import_id",
	// so we do not need a mutex around entry.Rows inside the handler.
	row := &entry.Rows[rowID]
	switch req.Field {
	case "date":
		// Store the date as the canonical ISO string so downstream
		// re-hashes via parseImportDate + ComputeContentHash produce the
		// same hash they would have at upload time if the user had typed
		// this value originally. Without normalization, "7/1/25" and
		// "2025-07-01" would disagree at the hash step even though they
		// represent the same day.
		t := normalized.(time.Time)
		row.Date = t.Format("2006-01-02")
	case "description":
		row.Description = normalized.(string)
	case "amount":
		row.Amount = normalized.(float64)
	case "skip":
		row.Skip = normalized.(bool)
	}

	// Rebuild the collision view against the just-edited session slice.
	// categoryMap and defaultCategoryID are nil/0 — the upload-time path
	// uses those too (see Chunk 1 Task 5 Step 5.2), so the preview-time
	// grouping contract is identical before and after an edit. The
	// canonical category lookups come from the live DB via
	// ListAllCategories.
	existingCats, listErr := h.queries.ListAllCategories(r.Context())
	if listErr != nil {
		log.Printf("import patch: list categories: %v", listErr)
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	catIDToName := make(map[int64]string, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
		catIDToName[c.ID] = c.Name
	}

	groups, groupErr := buildCollisionGroups(
		r.Context(),
		h.queries,
		entry.Rows,
		nil,
		0,
		catNameToID,
		catIDToName,
	)
	if groupErr != nil {
		log.Printf("import patch: build collision groups: %v", groupErr)
		writeError(w, http.StatusInternalServerError, "failed to rebuild collision groups")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"rows":             entry.Rows,
		"collision_groups": groups,
	})
}
```

- [ ] **Step 8.2: Register the route**

Open `internal/api/router.go`. Find the import block registered inside the authenticated group — after Chunk 1 Task 2 the relevant lines should look like:

```go
r.Post("/import/upload", h.handleImportUpload)
r.Post("/import/confirm", h.handleImportConfirm)
r.Delete("/import/{importID}", h.handleImportCancel)
```

Add a fourth line immediately below the Delete:

```go
r.Patch("/import/{importID}/rows/{rowID}", h.handleImportPatchRow)
```

Verify the final shape with:

```bash
grep -n "r\\.Post(\"/import\\|r\\.Delete(\"/import\\|r\\.Patch(\"/import\\|r\\.Get(\"/import" internal/api/router.go
```

Expected: four hits — Upload, Confirm, Delete, Patch — all inside the same `r.Group(func(r chi.Router) { ... })` block that `handleImportUpload` already lives in. The Get endpoint will be added in Chunk 3; it's not expected here.

- [ ] **Step 8.3: Build + vet the new handler**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go build ./... && go vet ./..."
```

Expected: both commands succeed with zero output. If `go vet` warns on the `any` type for the request Value field, it's a phantom — `encoding/json` routinely decodes into `any` and the surrounding code makes only typed assertions (`.(string)`, `.(bool)`) inside `validateImportField`.

- [ ] **Step 8.4: Sanity-check the existing test suite still passes**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run TestHandleImport -v"
```

Expected: every existing import test passes. The PATCH handler has no tests of its own yet (Task 9/10 add them), but the wiring in this step must not regress upload/confirm/cancel. If a previously-green test fails here, check first whether it collides on the same `row_id` key material now that Task 1 populated `RowID` — if the test marshals a row JSON expecting only the legacy fields and gets a `row_id` it did not expect, the fix is to add `row_id` to the expected map, not to remove it from the struct.

- [ ] **Step 8.5: Commit**

```bash
git add internal/api/import_handlers.go internal/api/router.go
git commit -m "feat(import): add PATCH /import/{importID}/rows/{rowID} endpoint"
```

### Task 9: PATCH tests #1–#4 (happy path, re-collision, 404, hash parity)

**Files:**
- Modify: `internal/api/import_handlers_test.go` — append four new test functions at the bottom of the file

These four tests cover the PATCH-path contract from the Testing Strategy section of the spec. They all follow the same pattern: `clearImportStore` + `setupTestDB` + `seedTestUser` + upload via `handleImportUpload` to get an `import_id`, then call `handleImportPatchRow` directly via `httptest.NewRecorder()` with `withUserAndURLParams` for the `importID`/`rowID` chi params.

- [ ] **Step 9.1: Add a small helper for PATCH requests**

At the top of the test file, just below the existing `postMultipartFile` helper, add:

```go
// patchImportRow builds a PATCH request for the PATCH /api/import/{importID}/rows/{rowID}
// endpoint and wires the chi URL params through withUserAndURLParams. Returns
// an httptest.ResponseRecorder so the caller can assert on status and body.
// Keeping this helper in the test file (not production) avoids coupling
// production code to the chi router's mock helpers.
func patchImportRow(t *testing.T, h *Handler, user database.User, importID string, rowID int, field string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"field": field, "value": value})
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/import/"+importID+"/rows/"+strconv.Itoa(rowID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserAndURLParams(req, user, map[string]string{
		"importID": importID,
		"rowID":    strconv.Itoa(rowID),
	})
	rec := httptest.NewRecorder()
	h.handleImportPatchRow(rec, req)
	return rec
}
```

Verify the `"strconv"` import is already in the test file's import block (it will be needed regardless for the test helpers below):

```bash
grep -n '"strconv"' internal/api/import_handlers_test.go
```

Expected: one match. If none, add `"strconv"` to the import block.

- [ ] **Step 9.2: Test #1 — happy path, group of 3**

Append to the bottom of `internal/api/import_handlers_test.go`:

```go
// TestHandleImportPatchRow_HappyPath_UnbreaksRowFromGroupOfThree verifies the
// core PATCH contract: an edit that changes a field feeding the content hash
// causes the edited row to flip clean and the remaining two rows to stay
// grouped (now as a pair, not a triple). Owns the "stale group_id after
// re-hash" bug class — if the handler fails to rebuild groups from scratch
// and instead mutates-in-place the old group list, the departing row's
// row_id will linger in the old group members slice.
func TestHandleImportPatchRow_HappyPath_UnbreaksRowFromGroupOfThree(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "patcher", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	// Three identical rows — same date, description, amount, category.
	// At upload time, buildCollisionGroups groups them into one intra_file
	// group of 3 members.
	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)
	uploadGroups := upload["collision_groups"].([]any)
	if len(uploadGroups) != 1 {
		t.Fatalf("expected 1 collision group at upload, got %d: %v", len(uploadGroups), uploadGroups)
	}
	uploadMembers := uploadGroups[0].(map[string]any)["member_row_ids"].([]any)
	if len(uploadMembers) != 3 {
		t.Fatalf("expected 3 members in upload group, got %d", len(uploadMembers))
	}

	// PATCH row 0's date to something unique. The edit should flip row 0 to
	// clean and leave rows 1 and 2 still grouped together.
	patchRec := patchImportRow(t, h, user, importID, 0, "date", "2025-08-15")
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}

	// Assert the response shape: rows list still has 3 entries, and the
	// collision_groups list now has exactly one group with 2 members
	// (row_ids 1 and 2).
	rows := resp["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows in response, got %d", len(rows))
	}
	row0 := rows[0].(map[string]any)
	if row0["date"].(string) != "2025-08-15" {
		t.Errorf("expected row 0 date=2025-08-15 after PATCH, got %v", row0["date"])
	}

	groups := resp["collision_groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected 1 remaining collision group after PATCH, got %d: %v", len(groups), groups)
	}
	members := groups[0].(map[string]any)["member_row_ids"].([]any)
	if len(members) != 2 {
		t.Fatalf("expected 2 members in remaining group, got %d: %v", len(members), members)
	}
	seen := map[int]bool{}
	for _, m := range members {
		seen[int(m.(float64))] = true
	}
	if !seen[1] || !seen[2] {
		t.Errorf("expected members to be {1, 2}, got %v", members)
	}
	if seen[0] {
		t.Errorf("row 0 should have left the group after PATCH, but is still listed: %v", members)
	}
}
```

- [ ] **Step 9.3: Test #2 — re-collision + skip stickiness**

Append immediately after test #1:

```go
// TestHandleImportPatchRow_ReCollision_PreservesSkip owns the skip-sticky
// invariant from the spec's Edge Cases table: once a row is marked skip=true,
// no subsequent edit ever clears that flag. Here we mark a colliding row as
// skipped, then PATCH its date so it STOPS colliding — the response must
// still carry skip=true on the row and must NOT include the row in
// collision_groups (because skipped rows are excluded from grouping entirely).
//
// Also owns the stateful-regrouping invariant: the server holds session
// state across PATCHes, so a second PATCH's group view must reflect the
// first PATCH's mutation.
func TestHandleImportPatchRow_ReCollision_PreservesSkip(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "stickyskipper", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)

	// Mark row 0 as skipped. The response should drop the collision group
	// entirely (1 remaining member cannot form a collision with itself).
	skipRec := patchImportRow(t, h, user, importID, 0, "skip", true)
	if skipRec.Code != http.StatusOK {
		t.Fatalf("patch skip: expected 200, got %d: %s", skipRec.Code, skipRec.Body.String())
	}
	var skipResp map[string]any
	if err := json.Unmarshal(skipRec.Body.Bytes(), &skipResp); err != nil {
		t.Fatalf("unmarshal skip response: %v", err)
	}
	if groups := skipResp["collision_groups"].([]any); len(groups) != 0 {
		t.Fatalf("expected 0 collision groups after skipping row 0, got %d: %v", len(groups), groups)
	}
	skipRow0 := skipResp["rows"].([]any)[0].(map[string]any)
	if skipRow0["skip"] != true {
		t.Fatalf("expected row 0 skip=true after skip PATCH, got %v", skipRow0["skip"])
	}

	// Now PATCH the skipped row's date to a unique value. The row is
	// already skipped, so this edit does not change group membership
	// (skipped rows are excluded from grouping). The critical invariant:
	// skip MUST stay true. A handler bug that resets fields-other-than-
	// edited to their zero values would silently un-skip the row.
	datePatch := patchImportRow(t, h, user, importID, 0, "date", "2025-08-15")
	if datePatch.Code != http.StatusOK {
		t.Fatalf("patch date: expected 200, got %d: %s", datePatch.Code, datePatch.Body.String())
	}
	var dateResp map[string]any
	if err := json.Unmarshal(datePatch.Body.Bytes(), &dateResp); err != nil {
		t.Fatalf("unmarshal date response: %v", err)
	}
	dateRow0 := dateResp["rows"].([]any)[0].(map[string]any)
	if dateRow0["skip"] != true {
		t.Errorf("expected skip=true to persist after date PATCH, got %v", dateRow0["skip"])
	}
	if dateRow0["date"].(string) != "2025-08-15" {
		t.Errorf("expected date=2025-08-15 after PATCH, got %v", dateRow0["date"])
	}
	if groups := dateResp["collision_groups"].([]any); len(groups) != 0 {
		t.Fatalf("expected 0 collision groups (row 0 still skipped), got %d: %v", len(groups), groups)
	}
}
```

- [ ] **Step 9.4: Test #3 — 404 on expired session**

Append:

```go
// TestHandleImportPatchRow_ExpiredSession_Returns404 owns the session
// expiry backend half. We upload normally, reach into the importStore to
// rewind CreatedAt past the importTTL (60 minutes after Chunk 1 Task 1
// bumps it), then PATCH. The shared loadImportEntryForUser helper from
// Chunk 1 Task 2 evicts the expired entry and returns 404.
//
// Rewinding CreatedAt is the canonical way to test TTL expiry without
// fake-clock machinery — the helper uses time.Since(entry.CreatedAt), so
// mutating CreatedAt backwards is equivalent to fast-forwarding the wall
// clock. The store lookup still works (we wrote into the sync.Map directly
// by import_id) and the mutation is safe because we're the only goroutine
// reading it in this test.
func TestHandleImportPatchRow_ExpiredSession_Returns404(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "expirysub", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)

	// Rewind the entry's CreatedAt by 2 hours. importTTL is 60 minutes, so
	// time.Since(entry.CreatedAt) now exceeds the limit and the helper
	// returns 404.
	val, ok := importStore.Load(importID)
	if !ok {
		t.Fatalf("importStore.Load returned !ok for import_id=%s", importID)
	}
	entry := val.(*importEntry)
	entry.CreatedAt = time.Now().Add(-2 * time.Hour)

	patchRec := patchImportRow(t, h, user, importID, 0, "description", "Starbucks Reserve")
	if patchRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on expired session, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
}
```

- [ ] **Step 9.5: Test #4 — whitespace/case hash parity**

Append:

```go
// TestHandleImportPatchRow_WhitespaceCasePreservesCollision owns the
// hash-normalization parity bug class: upload-time hash and PATCH-time
// re-hash MUST use the same code path inside ComputeContentHash. A
// whitespace+case variation on description is the canonical canary —
// if either path normalizes differently, the groups diverge and the
// edited row spuriously flips clean.
//
// Setup: two rows with exactly matching description "Starbucks" form an
// intra_file group of 2. PATCH row 0's description to " STARBUCKS " (with
// wrapping whitespace and uppercased). After re-hash, the normalized form
// is still "starbucks" — ComputeContentHash lowercases and trims — so the
// group membership must be preserved.
func TestHandleImportPatchRow_WhitespaceCasePreservesCollision(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "hashparity", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)
	uploadGroups := upload["collision_groups"].([]any)
	if len(uploadGroups) != 1 {
		t.Fatalf("expected 1 collision group at upload, got %d", len(uploadGroups))
	}

	// PATCH row 0's description to a whitespace+case variant. Post-trim
	// the stored value is "STARBUCKS" (trim happens in validateImportField
	// but case is preserved in the row value — only the hash normalizes
	// case). The hash must still equal row 1's hash, so the group
	// stays intact.
	patchRec := patchImportRow(t, h, user, importID, 0, "description", " STARBUCKS ")
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}

	// The displayed row value is "STARBUCKS" (trimmed, case preserved) —
	// this asserts that validateImportField stores the trimmed form, which
	// is what the frontend will render.
	row0 := resp["rows"].([]any)[0].(map[string]any)
	if row0["description"].(string) != "STARBUCKS" {
		t.Errorf("expected row 0 description=STARBUCKS after trim, got %q", row0["description"])
	}

	// The group must still exist with both members. If the hash diverged
	// due to a normalization mismatch, this would collapse to zero groups
	// and the test would catch the regression.
	groups := resp["collision_groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected 1 collision group after whitespace+case PATCH, got %d: %v", len(groups), groups)
	}
	members := groups[0].(map[string]any)["member_row_ids"].([]any)
	if len(members) != 2 {
		t.Fatalf("expected 2 members (hash parity preserved), got %d: %v", len(members), members)
	}
}
```

- [ ] **Step 9.6: Run the four new tests**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImportPatchRow_HappyPath_UnbreaksRowFromGroupOfThree|TestHandleImportPatchRow_ReCollision_PreservesSkip|TestHandleImportPatchRow_ExpiredSession_Returns404|TestHandleImportPatchRow_WhitespaceCasePreservesCollision' -v"
```

Expected: all four PASS. Common failure modes and how to diagnose:

- **#1 reports "expected 2 members, got 3":** the handler didn't rebuild groups — it's still referencing the original upload-time groups. Read the handler body and confirm `buildCollisionGroups` is called AFTER the row mutation and its return value is the one written to the response.
- **#2 reports "expected skip=true after date PATCH, got <nil>":** the mutation step is clobbering the whole row instead of patching one field. The handler must use `row := &entry.Rows[rowID]` and a switch statement, NOT `entry.Rows[rowID] = importRow{Date: newDate}` (which would zero every other field).
- **#3 reports "expected 404, got 200":** the helper's expiry branch is not running. Double-check that Chunk 1 Task 2's `loadImportEntryForUser` has the `time.Since(entry.CreatedAt) > importTTL` check and that this test is actually rewinding the same entry struct the helper reads.
- **#4 reports "expected 1 collision group, got 0":** hash divergence — the PATCH path re-hashes with a normalized-differently description. Read `internal/database/content_hash.go` to confirm it uses `strings.ToLower(strings.TrimSpace(desc))`, and confirm `buildCollisionGroups` does NOT pre-lowercase `row.Description` before passing it to `ComputeContentHash` (the hash function owns normalization).

- [ ] **Step 9.7: Commit**

```bash
git add internal/api/import_handlers_test.go
git commit -m "test(import): PATCH happy path, re-collision, expiry, hash parity"
```

### Task 10: PATCH tests — tombstone hiding + amount-mode parity

**Files:**
- Modify: `internal/api/import_handlers_test.go` — append two more test functions

These two tests finish the PATCH-path coverage: **#8 Part B** (the PATCH half of the tombstone-hiding pair; Part A was added in Chunk 1 Task 6) and **#9 Part B** (the amount-mode parity case: upload empty amount silently → 0, PATCH empty amount → 400 INVALID_AMOUNT). Both own specific bug classes called out in the spec's Testing Strategy and Validation field rules sections.

- [ ] **Step 10.1: Test #8 Part B — tombstone hiding during PATCH re-check**

Append to the bottom of `internal/api/import_handlers_test.go`:

```go
// TestHandleImportPatchRow_HidesTombstonedFromDbMatch owns the second
// half of the soft-delete leak guard (Part A lives in
// TestHandleImportUpload_HidesTombstonedFromDbMatch from Chunk 1 Task 6).
//
// Setup: seed one LIVE row and one TOMBSTONED row that share the same
// content hash. The amount=999 sentinel on the tombstoned row is the
// SpenDrop project convention for "this should not appear in any
// aggregate" — if a reader forgets the deleted_at filter, the test will
// fail loudly with 999 somewhere in the db_match payload.
//
// Upload a single row whose content matches NEITHER seeded row. The upload
// response shows zero collision groups. Then PATCH the uploaded row's
// DATE so that after re-hashing it matches the tombstoned row's content
// hash. A correct handler runs the hash lookup through
// GetTransactionByContentHash at queries.sql:182-197 (which filters
// t.deleted_at IS NULL) and returns zero collision groups. A bug that
// reads hashes without the soft-delete filter would flag the row as a
// db_match against the tombstoned transaction.
//
// Important: this test does NOT assert on any collision group containing
// amount=999. The correct response has no matching group at all; we
// verify absence, not presence. Asserting presence-with-sentinel would
// be a "test of the bug" (only passes if the leak exists), which is
// exactly the anti-pattern the CLAUDE.md soft-delete discipline warns
// against.
func TestHandleImportPatchRow_HidesTombstonedFromDbMatch(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "tombstoner", "member")
	cat := seedTestCategory(t, q, "Coffee", "expense")

	// Seed one live + one tombstoned row that would share a content hash
	// if the import row's date were shifted to 2025-08-15. The live row
	// sits on a DIFFERENT hash (different date), so the import row does
	// not collide with it at upload time. The tombstoned row IS on the
	// hash space the PATCH will move into.
	liveDate := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	tombstoneDate := time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC)

	liveHash := database.ComputeContentHash(liveDate, 500, "Starbucks", "Coffee")
	_, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		CategoryID:  cat.ID,
		Date:        liveDate,
		Amount:      5.00,
		AmountCents: 500,
		Description: "Starbucks",
		ContentHash: sql.NullString{String: liveHash, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed live row: %v", err)
	}

	// Tombstoned row uses amount=999 as the sentinel so a soft-delete
	// leak would be trivially visible in any assertion that touched its
	// amount. Its hash is computed against amount=99900 cents.
	tombstoneHash := database.ComputeContentHash(tombstoneDate, 99900, "Starbucks", "Coffee")
	tombstoned, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		CategoryID:  cat.ID,
		Date:        tombstoneDate,
		Amount:      999.00,
		AmountCents: 99900,
		Description: "Starbucks",
		ContentHash: sql.NullString{String: tombstoneHash, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed tombstoned row: %v", err)
	}
	// Tombstone via raw UPDATE on purpose: we need the deleted_at shape
	// without the audit side-effect of a real soft-delete, so this
	// deliberately bypasses TransactionStore. The test asserts on the
	// query layer (content-hash lookup respecting deleted_at IS NULL),
	// not on the store layer's audit contract. Matches the Chunk 1 Test
	// #8A pattern so both halves of the soft-delete guard use the same
	// tombstoning mechanism.
	if _, err := db.Exec("UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", tombstoned.ID); err != nil {
		t.Fatalf("tombstone row: %v", err)
	}

	// Upload a row whose content is "Starbucks $999.00 Coffee" but on
	// date 2025-07-15 — a third date that collides with NEITHER seeded
	// row's hash space. At upload time, the row is clean.
	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-15", "Starbucks", "999.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)
	if uploadGroups := upload["collision_groups"].([]any); len(uploadGroups) != 0 {
		t.Fatalf("expected 0 collision groups at upload (baseline is clean), got %d: %v", len(uploadGroups), uploadGroups)
	}

	// PATCH the uploaded row's date to 2025-08-15, which re-hashes the row
	// into the tombstoned row's hash space. The correct response still has
	// zero collision groups because the DB match is filtered by the
	// soft-delete predicate inside GetTransactionByContentHash.
	patchRec := patchImportRow(t, h, user, importID, 0, "date", "2025-08-15")
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}
	groups := resp["collision_groups"].([]any)
	if len(groups) != 0 {
		t.Fatalf("expected 0 collision groups after PATCH (tombstoned row must not match), got %d: %v", len(groups), groups)
	}

	// Extra sanity: the tombstoned row really is tombstoned. If this
	// assertion fails the test setup is broken and the primary assertion
	// above is meaningless.
	var tombstonedDeletedAt sql.NullTime
	if err := db.QueryRow("SELECT deleted_at FROM transactions WHERE id = ?", tombstoned.ID).Scan(&tombstonedDeletedAt); err != nil {
		t.Fatalf("re-read tombstoned row: %v", err)
	}
	if !tombstonedDeletedAt.Valid {
		t.Fatalf("seeded tombstoned row still has deleted_at=NULL — test setup is broken")
	}
}
```

- [ ] **Step 10.2: Test #9 Part B — amount-mode parity (empty PATCH → 400)**

Append immediately after test #8 Part B:

```go
// TestHandleImportPatchRow_AmountFieldModeParity owns the edit-mode half of
// the amount-field parity rule in the spec's Validation field rules table:
//
//   Upload mode: empty cell → 0 (existing silent behavior)
//   Edit mode:   empty PATCH → 400 INVALID_AMOUNT
//
// The asymmetry is intentional. An empty cell in a freshly-uploaded file
// is usually a parse failure upstream (blank row, header misalignment)
// and silent-zero has been the behavior since Phase 3.1; changing that
// now would regress every uploader's file. But an empty string sent via
// PATCH is the user actively editing a value — they saw a number, they
// deleted it, they Tab'd away. Silent-zero there would make the Import
// button deceptively enable while the row is effectively broken. Hard
// 400 forces the inline cell error so the user knows the edit did not
// take effect.
//
// Test shape: upload a row with an explicit amount to establish the
// baseline, then PATCH that row's amount to "" and assert the response
// status is 400 and the body carries {code:"INVALID_AMOUNT", field:"amount"}.
// A second assertion checks that the upload-mode silent-zero path is NOT
// regressed by the PATCH-side change: we upload a separate row with
// amount="" and assert that upload responds 200 and includes the row
// (silent coerce to 0.00). This second assertion lives in the same test
// because the invariant — "upload and PATCH handle empty amounts
// differently, and BOTH behaviors must stay stable together" — is a
// single bug owner, not two separate ones.
func TestHandleImportPatchRow_AmountFieldModeParity(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "amountparity", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	// Upload mode: empty amount cell must coerce to 0 and the row must
	// still appear in the preview. This is the existing silent-zero
	// behavior — it is inherited from parseImportAmount returning an
	// error for empty strings but the calling loop in handleImportUpload
	// treating that as zero. The PATCH change in Task 7 adds an extra
	// non-empty check that does NOT apply here (it is in
	// validateImportField, not handleImportUpload's parse loop).
	uploadXlsx := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-02", "Blank Row", "", "Coffee"},
		},
	)
	uploadReq := postMultipartFile(t, "/api/import/upload", uploadXlsx)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200 (silent-zero for empty amount), got %d: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)
	rows := upload["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows in upload response (silent-zero keeps blank-amount row), got %d", len(rows))
	}
	// The row with empty amount parses to 0.0 at upload time.
	row1 := rows[1].(map[string]any)
	if amt, ok := row1["amount"].(float64); !ok || amt != 0 {
		t.Errorf("expected row 1 amount=0 (silent-zero), got %v (%T)", row1["amount"], row1["amount"])
	}

	// Edit mode: PATCH the FIRST row's amount to the empty string. The
	// response must be 400 with the INVALID_AMOUNT error body. We target
	// row 0 (which has a valid 5.00 at upload time) so the test exercises
	// "user is actively clearing a valid amount", not "we already had a
	// zero-amount row and the PATCH no-ops".
	patchRec := patchImportRow(t, h, user, importID, 0, "amount", "")
	if patchRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on PATCH with empty amount, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var patchErr map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patchErr); err != nil {
		t.Fatalf("unmarshal patch error: %v", err)
	}
	if patchErr["code"] != "INVALID_AMOUNT" {
		t.Errorf("expected code=INVALID_AMOUNT, got %v", patchErr["code"])
	}
	if patchErr["field"] != "amount" {
		t.Errorf("expected field=amount, got %v", patchErr["field"])
	}
	if msg, ok := patchErr["message"].(string); !ok || msg == "" {
		t.Errorf("expected non-empty message string, got %v (%T)", patchErr["message"], patchErr["message"])
	}

	// Extra guard: the rejected PATCH must NOT have mutated the session
	// state. Re-read the store directly and confirm row 0 still has
	// amount=5.00. A handler that writes BEFORE validating would leave
	// the row at amount=0 even though the response was 400.
	val, ok := importStore.Load(importID)
	if !ok {
		t.Fatalf("importStore.Load returned !ok for import_id=%s", importID)
	}
	entry := val.(*importEntry)
	if got := entry.Rows[0].Amount; got != 5.0 {
		t.Errorf("rejected PATCH must leave row 0 amount unchanged, got %v", got)
	}
}
```

- [ ] **Step 10.3: Run the two new tests**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImportPatchRow_HidesTombstonedFromDbMatch|TestHandleImportPatchRow_AmountFieldModeParity' -v"
```

Expected: both PASS. Failure diagnostics:

- **#8B reports "expected 0 collision groups, got 1":** `GetTransactionByContentHash` is reading through tombstoned rows. Read `internal/database/queries.sql:182-197` and confirm the `WHERE content_hash = ?` clause is followed by `AND deleted_at IS NULL`. If it is, the leak is inside `buildCollisionGroups` somehow — grep for any direct hash lookup in `import_handlers.go` that bypasses the query wrapper.
- **#9B reports "expected 400, got 200":** `validateImportField`'s amount branch is not rejecting empty strings. Re-read Task 7 Step 7.1 — the `if strings.TrimSpace(s) == ""` check must land BEFORE the `parseImportAmount` call (the helper also errors on empty, but only with a generic "empty amount" message — the explicit pre-check here produces the user-facing message we want).
- **#9B reports "rejected PATCH must leave row 0 amount unchanged, got 0":** the handler writes before validating. Check the ordering in Task 8 Step 8.1: `validateImportField` must run BEFORE the `row := &entry.Rows[rowID]` mutation.

- [ ] **Step 10.4: Run the full PATCH test set once more to catch cross-test interactions**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run TestHandleImportPatchRow -v"
```

Expected: all six PATCH tests PASS — `TestHandleImportPatchRow_HappyPath_UnbreaksRowFromGroupOfThree`, `TestHandleImportPatchRow_ReCollision_PreservesSkip`, `TestHandleImportPatchRow_ExpiredSession_Returns404`, `TestHandleImportPatchRow_WhitespaceCasePreservesCollision`, `TestHandleImportPatchRow_HidesTombstonedFromDbMatch`, `TestHandleImportPatchRow_AmountFieldModeParity`.

- [ ] **Step 10.5: Run the entire import suite to catch regressions**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run TestHandleImport -v"
```

Expected: every existing and new import test passes. If a Chunk 1 test fails here (e.g. upload or confirm), the PATCH addition somehow altered shared state — the most likely cause is the `patchImportRow` test helper not clearing the store between runs (but every test calls `clearImportStore()` at the top, so this is unlikely) or the test-file-level import block is missing a symbol.

- [ ] **Step 10.6: Commit**

```bash
git add internal/api/import_handlers_test.go
git commit -m "test(import): PATCH tombstone hiding + amount empty-mode parity"
```

---

## Chunk 3: GET Session + Confirm Hardening

This chunk adds the F5/tab-refresh resume endpoint and tightens `/confirm` so it refuses to partially import a session that still contains unresolved collisions. It's the final backend chunk — after it lands, the server half of Phase 3.4b is complete and the frontend work in Chunks 4–6 consumes a stable API surface.

**What changes:**
- `handleImportGetSession` — new `GET /api/import/{importID}` handler that returns the full session snapshot using the shared `loadImportEntryForUser` helper from Chunk 1. Recomputes `collision_groups` against current session rows so a refresh after edits shows the post-edit state, not the original upload state.
- `handleImportConfirm` — tightened to (a) re-run `buildCollisionGroups` after category resolution and return **409 UNRESOLVED_COLLISIONS** if any group survives, (b) filter `Skip==true` rows at the handler level before handing off to `processImportRows`, (c) adjust the response totals so user-skipped rows count toward `skipped`.
- Three new backend tests: unresolved-collision 409, happy-path `content_hash` persistence, skip-excluded PATCH-then-confirm.

**What stays untouched:**
- `processImportRows` and its property-test conservation invariant (`len(Rows) == len(Inserted) + len(Skipped) + len(Errored)`). The skip filter lives in the handler so the processor keeps its covered contract.
- Category resolution logic inside `processImportRows`. The confirm-time collision re-check computes collision groups against the final category bindings by passing `req.CategoryMap` + `req.DefaultCategoryID` to `buildCollisionGroups`.
- `importStore`, TTL, ownership enforcement. All inherited from Chunks 1–2.

**Files touched in this chunk:**
- Modify: `internal/api/import_handlers.go` — add `handleImportGetSession` handler, add re-check + skip filter + total adjustment to `handleImportConfirm`, extract `uniqueCategoriesFromRows` helper
- Modify: `internal/api/router.go` — add `r.Get("/import/{importID}", h.handleImportGetSession)` inside the authenticated group
- Modify: `internal/api/import_handlers_test.go` — add 5 new tests (2 for GET, 3 for confirm)

---

### Task 11: Add `handleImportGetSession` resume endpoint

**Files:**
- Modify: `internal/api/import_handlers.go` — new handler placed immediately after `handleImportPatchRow` from Chunk 2; extract `uniqueCategoriesFromRows` helper above `handleImportUpload`
- Modify: `internal/api/router.go` — add one line inside the authenticated import block
- Modify: `internal/api/import_handlers_test.go` — add `TestHandleImportGetSession_HappyPath` and `TestHandleImportGetSession_ExpiredSession_Returns404`

- [ ] **Step 11.1: Write the failing happy-path test**

Open `internal/api/import_handlers_test.go`. Place this new test immediately after `TestHandleImportPatchRow_AmountFieldModeParity` (the last Chunk 2 PATCH test — search for it to find the insertion point):

```go
// TestHandleImportGetSession_HappyPath verifies that after an upload, a
// GET on /api/import/{importID} returns the same shape as the upload
// response (rows, columns, unique_categories, collision_groups,
// import_id, row_count). This is the F5/tab-refresh resume path — the
// frontend mounts with a localStorage import_id and calls GET to
// rehydrate preview state without re-uploading the file.
func TestHandleImportGetSession_HappyPath(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "getter", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00", "Food"},
		{"2025-01-08", "Trader Joe's", "42.10", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Now GET the session.
	getReq := httptest.NewRequest(http.MethodGet, "/api/import/"+importID, nil)
	getReq = withUserAndURLParam(getReq, user, "importID", importID)
	getRec := httptest.NewRecorder()
	h.handleImportGetSession(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d; body: %s", getRec.Code, getRec.Body.String())
	}

	var getResp map[string]any
	decodeResponse(t, getRec, &getResp)

	// Shape parity: every top-level key present in the upload response
	// must also appear in the GET response. This is the F5-refresh
	// contract — frontend code paths that consume upload-shaped JSON
	// keep working when they consume GET-shaped JSON.
	for _, key := range []string{"import_id", "row_count", "rows", "columns", "unique_categories", "collision_groups"} {
		if _, ok := getResp[key]; !ok {
			t.Errorf("GET response missing top-level key %q", key)
		}
	}

	if gotID, _ := getResp["import_id"].(string); gotID != importID {
		t.Errorf("import_id: want %q, got %q", importID, gotID)
	}
	if rc, _ := getResp["row_count"].(float64); int(rc) != 2 {
		t.Errorf("row_count: want 2, got %v", getResp["row_count"])
	}
	rowsResp, ok := getResp["rows"].([]any)
	if !ok || len(rowsResp) != 2 {
		t.Fatalf("rows: want slice of 2, got %T len %d", getResp["rows"], len(rowsResp))
	}

	// Two distinct rows with no DB matches → zero collision groups.
	groups, _ := getResp["collision_groups"].([]any)
	if len(groups) != 0 {
		t.Errorf("collision_groups: want 0 for two distinct rows, got %d", len(groups))
	}
}
```

- [ ] **Step 11.2: Write the expired-session test**

Add this test immediately below the happy-path test:

```go
// TestHandleImportGetSession_ExpiredSession_Returns404 verifies that a
// GET for an import whose CreatedAt is older than importTTL returns 404
// and reaps the entry. Uses the same direct-store mutation pattern as
// TestHandleImportPatchRow_ExpiredSession_Returns404 from Chunk 2:
// upload normally, then rewind CreatedAt to 2h ago via importStore.
func TestHandleImportGetSession_ExpiredSession_Returns404(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "expirer", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00"},
	})
	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Rewind CreatedAt so the entry is expired per importTTL (60m).
	val, ok := importStore.Load(importID)
	if !ok {
		t.Fatalf("store lookup: entry missing immediately after upload")
	}
	entry := val.(*importEntry)
	entry.CreatedAt = time.Now().Add(-2 * time.Hour)

	// GET should now 404 and the helper should reap the entry from
	// the store (same contract as the Chunk 2 PATCH expiry test).
	getReq := httptest.NewRequest(http.MethodGet, "/api/import/"+importID, nil)
	getReq = withUserAndURLParam(getReq, user, "importID", importID)
	getRec := httptest.NewRecorder()
	h.handleImportGetSession(getRec, getReq)

	if getRec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for expired session, got %d; body: %s", getRec.Code, getRec.Body.String())
	}
	if _, still := importStore.Load(importID); still {
		t.Error("expected loadImportEntryForUser to delete the expired entry; it is still in the store")
	}
}
```

- [ ] **Step 11.3: Run the two new tests to verify they fail at the compile step**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImportGetSession' -v"
```

Expected: compile failure `h.handleImportGetSession undefined (type *Handler has no field or method handleImportGetSession)`. This is the TDD failing state that gates Step 11.4.

If the failure is instead about `time` being undefined, the stdlib `time` import was not already present in the test file (it should be — several Chunk 2 tests already use `time.Now()`). If not, add `"time"` to the test file's top-of-file import block.

- [ ] **Step 11.4: Extract `uniqueCategoriesFromRows` helper**

`handleImportGetSession` and `handleImportUpload` both need the same sorted-distinct category list. The upload handler currently inlines this computation around `internal/api/import_handlers.go:~295`. Extract it into a package-private helper to keep the three call sites (upload, GET, a future confirm snapshot) byte-identical. Place the helper right above `handleImportUpload`:

```go
// uniqueCategoriesFromRows returns the sorted-distinct Category values
// from a slice of importRows, skipping empties. Used by
// handleImportUpload and handleImportGetSession so both the initial
// upload response and the F5/resume response seed the category-mapping
// dropdowns from the same source. Case-insensitive dedup keyed on the
// lowercased name, but the returned slice preserves the first-seen
// casing of each category.
func uniqueCategoriesFromRows(rows []importRow) []string {
	seen := make(map[string]string)
	for _, row := range rows {
		cat := strings.TrimSpace(row.Category)
		if cat == "" {
			continue
		}
		key := strings.ToLower(cat)
		if _, ok := seen[key]; !ok {
			seen[key] = cat
		}
	}
	out := make([]string, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
```

Then update `handleImportUpload` to call the helper. Grep for `uniqueCategories` in `internal/api/import_handlers.go`:

```bash
grep -n "uniqueCategories" internal/api/import_handlers.go
```

Expected hits:

1. The inline build-up loop inside `handleImportUpload` — 6-to-10 lines that iterate `parsedRows`, trim each `row.Category`, dedup into a map, append to a slice, sort. Replace that entire block with:

   ```go
   uniqueCategories := uniqueCategoriesFromRows(parsedRows)
   ```

2. The `writeJSON` response map (inside `handleImportUpload`) that references `"unique_categories": uniqueCategories` — leave this untouched.

After the swap, the upload handler is 6-to-10 lines shorter and the two call sites share one formula. Run `go build ./...` to confirm the replacement compiles:

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go build ./..."
```

Expected: clean build (no output). If the helper is missing `sort`, `strings`, or the `importRow` type, the compile error will name the specific symbol — add it to the imports block or rearrange declarations as needed.

- [ ] **Step 11.5: Add the `handleImportGetSession` handler**

Place this handler in `internal/api/import_handlers.go` immediately after `handleImportPatchRow` (the Chunk 2 handler) and immediately before `handleImportCancel`:

```go
// handleImportGetSession returns the full current snapshot of an import
// session — rows, columns, unique_categories, and freshly-computed
// collision_groups — via the shared loadImportEntryForUser gate. This
// is the F5/tab-refresh resume path: the frontend persists import_id in
// localStorage on upload and calls GET on mount to rehydrate preview
// state without re-uploading the file.
//
// Why recompute collision_groups on every GET instead of caching:
// after Chunk 2, every PATCH mutates entry.Rows in place and the
// collision_groups field on the response is always a function of the
// current Rows slice. Caching the groups would require invalidation
// plumbing on every PATCH, and the DB cost is identical to a PATCH
// rebuild (one GetTransactionByContentHash per hashable row).
// Recomputing on read is the cheaper invariant to maintain.
//
// Category resolution:
// At upload time we don't know which category_map / default_category_id
// the user will pick — those are confirm-time arguments. So the GET
// handler, like handleImportUpload and handleImportPatchRow, passes
// nil/0 for the user-choice args. buildCollisionGroups still uses the
// canonical DB category name for the hash formula (via catIDToName),
// so a category rename between upload and GET would correctly mutate
// the preview-time hash and potentially collapse or expand a collision.
//
// Errors:
//   401/403/404                    — via loadImportEntryForUser (unauthorized,
//                                    wrong user, missing/expired session)
//   500 failed to load categories  — ListAllCategories returned a DB fault
//   500 failed to rebuild groups   — buildCollisionGroups returned a DB fault
func (h *Handler) handleImportGetSession(w http.ResponseWriter, r *http.Request) {
	importID := chi.URLParam(r, "importID")

	entry, ok := loadImportEntryForUser(w, r, importID)
	if !ok {
		return
	}

	// Load categories for the canonical hash resolution.
	// buildCollisionGroups needs catNameToID (upload-time name match)
	// and catIDToName (canonical name for the hash formula). Same
	// pattern as the Chunk 1 upload call site and the Chunk 2 PATCH
	// call site — keeping the three sites byte-identical makes future
	// refactors easier to audit.
	existingCats, err := h.queries.ListAllCategories(r.Context())
	if err != nil {
		log.Printf("import get: list categories: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	catIDToName := make(map[int64]string, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
		catIDToName[c.ID] = c.Name
	}

	groups, err := buildCollisionGroups(
		r.Context(),
		h.queries,
		entry.Rows,
		nil, // categoryMap not chosen yet at resume time
		0,   // defaultCategoryID not chosen yet either
		catNameToID,
		catIDToName,
	)
	if err != nil {
		log.Printf("import get: build collision groups: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to rebuild collision groups")
		return
	}

	// unique_categories is the sorted-distinct set of category strings
	// seen in entry.Rows. The frontend uses it to seed the
	// category-mapping dropdowns — same as the upload response.
	// Recompute from the current rows (not a cached field) so edits
	// that rename a category cell are reflected in the resume snapshot.
	uniqueCats := uniqueCategoriesFromRows(entry.Rows)

	writeJSON(w, http.StatusOK, map[string]any{
		"import_id":         importID,
		"row_count":         len(entry.Rows),
		"rows":              entry.Rows,
		"columns":           entry.Columns,
		"unique_categories": uniqueCats,
		"collision_groups":  groups,
	})
}
```

- [ ] **Step 11.6: Register the GET route**

Open `internal/api/router.go`. Find the import block — after Chunks 1 and 2 it should contain four lines:

```go
r.Post("/import/upload", h.handleImportUpload)
r.Post("/import/confirm", h.handleImportConfirm)
r.Delete("/import/{importID}", h.handleImportCancel)
r.Patch("/import/{importID}/rows/{rowID}", h.handleImportPatchRow)
```

Add a fifth line immediately below the `Patch` line:

```go
r.Get("/import/{importID}", h.handleImportGetSession)
```

Verify the final shape with:

```bash
grep -n "r\\.Post(\"/import\\|r\\.Delete(\"/import\\|r\\.Patch(\"/import\\|r\\.Get(\"/import" internal/api/router.go
```

Expected: five matches, one per verb. If the `Get` match hits a DIFFERENT route that also lives under `/import/` (unlikely given the current router), re-read the router to find the right spot — the line must live inside the authenticated group, alongside the other four.

- [ ] **Step 11.7: Run the two tests — expect PASS**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImportGetSession' -v"
```

Expected: both PASS. Failure diagnostics:

- **Happy path reports "expected 200, got 404":** `loadImportEntryForUser` is returning 404 even though the entry is fresh. Two likely causes:
  - The upload response did not actually store into `importStore`. Chunk 1 Task 5 Step 5.3 requires `importStore.Store` to happen BEFORE `buildCollisionGroups` inside `handleImportUpload` — if the store write is missing or is downstream of a 500, the entry is never present. Add `t.Logf("store has entry: %v", func() bool { _, ok := importStore.Load(importID); return ok }())` right after the upload to confirm.
  - The ownership check is firing because `withUserAndURLParam` does not propagate the user context the way `withUser` does. Read `withUserAndURLParam` in `transaction_handlers_test.go:~34` and confirm it sets BOTH the chi route context and the `auth.UserContextKey` — if it only sets one, the helper considers the request unauthenticated and returns 401 (not 404). The test would then fail with 401, not 404, so if you see 401 the bug is here.
- **Happy path reports "import_id: want X, got Y":** the handler is writing the wrong import ID. Check Step 11.5 — the response map uses `"import_id": importID` where `importID` is the URL param, NOT a field on `importEntry` (there is no `importEntry.ID` field). If the value is empty, the chi URL param lookup failed — the helper's `importID := chi.URLParam(r, "importID")` line is running against a request whose chi context does not have `importID` set.
- **Happy path reports "GET response missing top-level key 'unique_categories'":** `uniqueCategoriesFromRows` was called on an empty slice and returned `nil`, which JSON-encodes as `null`. Verify the test xlsx has a Category column with non-empty values — Step 11.1's data uses "Food"/"Food", which should produce `["Food"]` (size 1, not empty). If the test data is fine, check the helper's return statement: `return out` where `out := make([]string, 0, len(seen))` guarantees a non-nil slice even for zero categories (it serializes as `[]`). If you see `null` in the JSON, the handler is returning `nil` somewhere — grep for `nil` returns in `uniqueCategoriesFromRows`.
- **Happy path reports "GET response missing top-level key 'collision_groups'":** `buildCollisionGroups` returned a nil slice which JSON-encoded as `null`. Read Chunk 1 Task 4 Step 4.2 — the initial declaration must be `groups := []collisionGroup{}`, not `var groups []collisionGroup`. If Chunk 1 used `var`, fix it back in Chunk 1's implementation; do NOT paper over the nil in the GET handler by adding `if groups == nil { groups = []collisionGroup{} }`, because the same bug would bite upload and PATCH in their own response encodings.
- **Expired session reports "expected 404 for expired session, got 200":** `loadImportEntryForUser`'s expiry check is not firing. The check is `if time.Since(entry.CreatedAt) > importTTL` (Chunk 1 Step 2.1). With `entry.CreatedAt = time.Now().Add(-2 * time.Hour)` and `importTTL = 60 * time.Minute` (Chunk 1 Step 1.3), `time.Since` returns ~2h which is `> 60m`. If the test sees 200, print `importTTL` in the test to verify it was actually bumped to 60m. If it's still 30m, Chunk 1 Task 1 Step 1.3 was not applied.
- **Expired session reports "expected ... the expired entry; it is still in the store":** the helper is returning 404 without calling `importStore.Delete`. Read Chunk 1 Step 2.1 — the expiry branch must delete the entry before returning false. If it doesn't, fix it in Chunk 1's implementation.

- [ ] **Step 11.8: Commit**

```bash
git add internal/api/import_handlers.go internal/api/router.go internal/api/import_handlers_test.go
git commit -m "feat(import): add GET session resume endpoint"
```

---

### Task 12: Harden `/confirm` with collision re-check + skip filter

This task rewrites `handleImportConfirm` in three small steps:
1. Re-run `buildCollisionGroups` after loading categories, using the confirm-time `CategoryMap` and `DefaultCategoryID` (unlike upload/GET/PATCH which use `nil`/`0`), so the re-check accounts for the user's final category bindings.
2. Return `409 UNRESOLVED_COLLISIONS` with the full `collision_groups` array if any group survives. No partial insert.
3. Filter `Skip==true` rows at the handler level before passing them to `processImportRows`. Adjust the response totals so user-skipped rows land in the `skipped` bucket.

**Files:**
- Modify: `internal/api/import_handlers.go` — three surgical edits inside `handleImportConfirm` (the post-Chunk-1 line numbers are ~640–710; Chunk 1 Task 3 removes the ~15-line force-add block, so the handler is shorter than the current 605-line layout)

- [ ] **Step 12.1: Insert the collision re-check block**

After Chunks 1–2, the body of `handleImportConfirm` reads (roughly; blank lines elided for brevity):

```go
func (h *Handler) handleImportConfirm(w http.ResponseWriter, r *http.Request) {
	var req importConfirmRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	entry, ok := loadImportEntryForUser(w, r, req.ImportID)
	if !ok {
		return
	}

	// Build category name-to-ID and id-to-name lookups from existing
	// categories. ...
	existingCats, err := h.queries.ListAllCategories(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	catIDToName := make(map[int64]string, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
		catIDToName[c.ID] = c.Name
	}

	// Start a database transaction for all inserts
	tx, err := h.db.BeginTx(r.Context(), nil)
	...
```

Insert the collision re-check AFTER the `for _, c := range existingCats` loop closes and BEFORE the `tx, err := h.db.BeginTx(...)` call. This ordering matters: no point opening a DB transaction if we're about to 409. The re-check itself is cheap (one `GetTransactionByContentHash` per hashable row, same as upload/PATCH) and does not need transactional isolation — the collision set can only grow between re-check and confirm, never shrink, and the confirm-time lookup is already a best-effort snapshot of a single instant.

Add this block:

```go
	// Phase 3.4b: re-run buildCollisionGroups against the current
	// session rows with the confirm-time category choices applied.
	// This is the all-or-nothing gate — if ANY non-skipped row is
	// still a member of a collision group (intra_file or db_match)
	// after the user has finished editing, the entire import is
	// rejected with 409 and the full groups array, and the session
	// state is left untouched so the frontend can re-render the same
	// preview with updated hints.
	//
	// Unlike upload and PATCH (which pass nil/0 for categoryMap and
	// defaultCategoryID), confirm passes the user's chosen CategoryMap
	// and DefaultCategoryID. This covers the category-resolution-only
	// collision case: a row whose category cell was empty (and now
	// resolves to the user's default) could produce a hash that
	// matches a live DB row that wouldn't have matched at upload
	// time. Re-checking at confirm with the real resolved categories
	// is how we catch it.
	//
	// buildCollisionGroups already excludes Skip==true rows from
	// grouping (see Chunk 1 Task 4 Step 4.2 — the `if row.Skip
	// { continue }` guard is first in the loop), so `len(groups) > 0`
	// is equivalent to "at least one non-skipped row is still
	// colliding". No separate non-skipped filter is needed here.
	groups, err := buildCollisionGroups(
		r.Context(),
		h.queries,
		entry.Rows,
		req.CategoryMap,
		req.DefaultCategoryID,
		catNameToID,
		catIDToName,
	)
	if err != nil {
		log.Printf("import confirm: build collision groups: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to rebuild collision groups")
		return
	}
	if len(groups) > 0 {
		// 409 body shape mirrors the Chunk 2 PATCH 400 body: {code, ...}
		// with a machine-readable code so the frontend can switch on
		// it. The full collision_groups array is included so the
		// preview can re-render without a second round-trip — the
		// frontend never has to ask "which rows are still colliding?"
		// after a 409.
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":             "UNRESOLVED_COLLISIONS",
			"collision_groups": groups,
		})
		return
	}
```

Note: the local variable `err` above shadows the outer `err` used for `ListAllCategories` and `h.db.BeginTx`. In Go, `groups, err := ...` with `:=` creates a fresh `err` in the block only if `groups` is also new, but both sides of the `:=` need at least one fresh lhs. In this case, `groups` is new and `err` is reused from the enclosing scope — Go's rules say this is a reassignment of the outer `err`, not a shadow. That's fine: the next statement (`tx, err := h.db.BeginTx(...)`) will legitimately re-use the same `err` variable again. If your linter complains about shadow anyway, rename the local to `groupsErr` and update the `if err != nil` block accordingly.

- [ ] **Step 12.2: Filter `Skip==true` rows before `processImportRows`**

Find the `processImportRows(...)` call (post-Chunk-1, this is the only call site in `handleImportConfirm`, right before `tx.Commit()`). The existing struct literal reads:

```go
	result, minImportDate := processImportRows(r.Context(), qtx, importProcessInput{
		UserID:            entry.UserID,
		Rows:              entry.Rows,
		CategoryMap:       req.CategoryMap,
		DefaultCategoryID: req.DefaultCategoryID,
		CatNameToID:       catNameToID,
		CatIDToName:       catIDToName,
	})
```

Replace it with the skip-filtered version:

```go
	// Phase 3.4b: filter user-skipped rows out of the slice passed to
	// processImportRows. The filter lives at the handler level (not
	// inside processImportRows) so the processor's conservation
	// invariant `len(Rows) == len(Inserted) + len(Skipped) + len(Errored)`
	// — which property tests in import_handlers_property_test.go
	// depend on — stays true for the rows that actually reach it.
	// From the handler's POV, user-skipped rows are "never in the
	// batch"; from the processor's POV, the batch simply never
	// contained them.
	//
	// We iterate entry.Rows (not a copy) and accumulate into a fresh
	// slice pre-sized to the upper bound, so there is no reallocation
	// on typical inputs. Over-allocation for a heavily-skipped session
	// is negligible (len(importRow) * number_skipped bytes).
	filteredRows := make([]importRow, 0, len(entry.Rows))
	for _, row := range entry.Rows {
		if row.Skip {
			continue
		}
		filteredRows = append(filteredRows, row)
	}

	result, minImportDate := processImportRows(r.Context(), qtx, importProcessInput{
		UserID:            entry.UserID,
		Rows:              filteredRows,
		CategoryMap:       req.CategoryMap,
		DefaultCategoryID: req.DefaultCategoryID,
		CatNameToID:       catNameToID,
		CatIDToName:       catIDToName,
	})
```

Note on `entry.UserID`: if Chunk 1 Task 2 Step 2.2's replacement of lines 587–623 left a dangling `user.ID` reference further down the handler body, this is the point where you must swap it to `entry.UserID` — the shared helper has already verified ownership, so `entry.UserID == authenticated user's ID` is a hard invariant by the time this line runs. If the compile fails with `undefined: user`, fix it here. Do NOT reintroduce `auth.GetUser(r)` — the helper is the only call site for user auth in this handler after Chunk 1.

- [ ] **Step 12.3: Adjust the response totals**

Find the `writeJSON(w, http.StatusOK, ...)` call at the bottom of `handleImportConfirm`. It currently reads:

```go
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": len(result.Inserted),
		"skipped":  len(result.Skipped) + len(result.Errored),
		"total":    len(entry.Rows),
	})
```

Replace with:

```go
	// Phase 3.4b: the user-visible `skipped` field rolls up three
	// reasons into one bucket, because from the user's perspective a
	// row that "did not land" is a row that did not land, regardless
	// of the category:
	//   1. User-skipped rows (row.Skip==true, filtered above before
	//      processImportRows sees them) — still appear in entry.Rows
	//      so they count toward total but not toward inserted.
	//   2. Processor-skipped rows (content-hash duplicate, zero
	//      amount, negative amount, etc. — see skipReason* in
	//      processImportRows).
	//   3. Errored rows (DB faults, bad category_ids — a tiny bucket
	//      that the user can't distinguish from category 2 without
	//      log access).
	//
	// The arithmetic `len(entry.Rows) - len(result.Inserted)` captures
	// all three without needing to sum the process result's Skipped
	// and Errored slices AND add the user-skipped count separately.
	// It works because:
	//   total        = len(entry.Rows)
	//   processed    = len(filteredRows)            (= total - user_skipped)
	//   inserted     = len(result.Inserted)         (≤ processed)
	//   not_inserted = total - inserted
	//                = user_skipped + (processed - inserted)
	//                = user_skipped + processor_skipped + errored
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": len(result.Inserted),
		"skipped":  len(entry.Rows) - len(result.Inserted),
		"total":    len(entry.Rows),
	})
```

- [ ] **Step 12.4: Run existing confirm tests to verify no regression**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImportConfirm' -v"
```

Expected: every existing `TestHandleImportConfirm_*` test passes. Failure diagnostics:

- **`TestHandleImportConfirm_ValidImport_InsertsTransactions` fails with "expected 2 inserted, got 0":** the collision re-check is rejecting the happy-path fixture. This test uploads two distinct rows (Groceries + Electric bill) — they should not collide intra-file and their content hashes don't match anything in the DB (freshly set up test DB). If `len(groups) > 0` here, read the test data carefully — any accidentally-identical rows would land as an intra_file group. The more likely cause is that `catNameToID` is missing an entry for a row's category, forcing the row to use the default category, and two rows happening to share (date, amount, default-cat). Add `t.Logf("groups = %+v", groups)` inside the handler temporarily to see which hash collided.
- **`TestHandleImportConfirm_SkipsUnparseableDate` fails with unexpected `skipped` count:** the old `skipped` value was `len(result.Skipped) + len(result.Errored)`, the new value is `len(entry.Rows) - len(result.Inserted)`. Both should match for any test that does not use the new Skip field — the unparseable-date row will land in `result.Errored`, and `total - inserted == 1 == errored` so the two formulas agree. If they don't agree, a row is being silently dropped somewhere (most likely the collision re-check is removing a row the old code would have seen as a normal skip). Add `t.Logf("result = %+v", result)` inside the handler and confirm the new formula matches the old for this fixture.
- **Any test fails with compile error "undefined: user":** Chunk 1 Task 2 Step 2.2 removed `user, ok := auth.GetUser(r)` but did not swap the downstream `user.ID` reference to `entry.UserID`. Step 12.2's note already flags this as the right place to fix it — swap the struct literal's `UserID` field and re-run. If the error surfaces somewhere other than the `processImportRows` call, grep for `user\\.` in `handleImportConfirm` and swap every hit to `entry.` (they all refer to the same authenticated user now).
- **`TestHandleImport_DoubleImport_SkipsDuplicates` fails with "expected 409, got 200":** this test uploads the same rows twice, confirming once — the second confirm used to silently skip duplicates via `processImportRows`, but the Chunk 3 re-check now catches them first and returns 409. The existing test expected 200 + a `skipped` count from the processor; the new behavior is 409 + collision_groups. **This is an intentional behavior change — the test must be updated**, not the handler. Update the test body to expect 409 on the second confirm and assert that `collision_groups` contains a db_match group for each row. If the failure message from the test file mentions `expected skipped=2, got 0`, that's the line to rewrite. The test's old name is fine; the new assertion is "double-import is now rejected, not silently deduped".

- [ ] **Step 12.5: Commit**

```bash
git add internal/api/import_handlers.go internal/api/import_handlers_test.go
git commit -m "feat(import): 409 on unresolved collisions + skip filter at confirm"
```

Include `import_handlers_test.go` in the commit ONLY if you updated `TestHandleImport_DoubleImport_SkipsDuplicates` per the last diagnostic above. Otherwise drop it from the `git add` list.

---

### Task 13: Backend tests #5, #6, #7 for hardened `/confirm`

The three tests below own the three load-bearing invariants added by Task 12:
- **Test #5** owns the partial-import rejection (spec §§ 113–123 / Testing Strategy #5): any unresolved non-skipped collision → 409, zero rows inserted.
- **Test #6** owns the `content_hash` persistence invariant (spec §§ 399–411 / Testing Strategy #6): a successful confirm writes a non-null `content_hash` to every inserted row.
- **Test #7** owns the skip ≠ unresolved distinction (spec §§ 363–369 / Testing Strategy #7): a row marked `Skip` via PATCH is excluded from inserts entirely and counts toward the `skipped` field in the response.

**Files:**
- Modify: `internal/api/import_handlers_test.go` — add three new test functions, placed directly below the Task 11 GET tests so all Chunk 3 tests are co-located
- Modify: `internal/api/import_handlers_test.go` — add `countTransactionsForUser` helper near the top of the file (only if it does not already exist)

- [ ] **Step 13.1: Add the `countTransactionsForUser` helper (if missing)**

Grep first:

```bash
grep -n "countTransactionsForUser\|func .*TransactionsForUser" internal/api/import_handlers_test.go
```

If the helper already exists, skip this step and move to Step 13.2. Otherwise, add it near the top of the test file, right after `clearImportStore`.

**Import prerequisite — add `"database/sql"` to the top-of-file import block.** `internal/api/import_handlers_test.go` does NOT currently import `database/sql` (it receives `db` from `setupTestDB` via type inference and never references `sql.*` directly). The helper below uses `*sql.DB` in its signature, and Step 13.3's Test #6 also uses `sql.NullString` when scanning the `content_hash` column. Without the import, Step 13.5's compile step will fail with `undefined: sql`. Open the file and confirm the import block has `"database/sql"` — if it does not, add it alphabetically (goes right before `"encoding/json"`).

Add the helper:

```go
// countTransactionsForUser returns the number of non-tombstoned
// transactions owned by userID. Used by Chunk 3 confirm-flow tests that
// need to assert "DB is empty" or "DB has exactly N rows" after a
// confirm call. Uses a raw SQL count because (a) the existing sqlc
// ListTransactions* queries all filter by date range and do not match
// the test semantics, and (b) a raw count is shorter than adding a new
// query to queries.sql and regenerating sqlc just for tests. Soft-delete
// filtering via `AND deleted_at IS NULL` matches the CLAUDE.md invariant
// — tombstoned rows are invisible to the count.
func countTransactionsForUser(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM transactions WHERE user_id = ? AND deleted_at IS NULL",
		userID,
	).Scan(&n); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	return n
}
```

**If a matching sqlc query already exists and you prefer typed queries**, grep for candidates:

```bash
grep -n "func (q \\*Queries) List.*Transactions\\|func (q \\*Queries) Count.*Transactions" internal/database/queries.sql.go
```

Pick one whose SQL in `internal/database/queries.sql` contains `WHERE t.user_id = ?` and `AND t.deleted_at IS NULL` with **no date range filter**. If you find one, the typed equivalent is:

```go
func countTransactionsForUser(t *testing.T, q *database.Queries, userID int64) int {
	t.Helper()
	txs, err := q.SOME_LIST_QUERY(context.Background(), database.SOME_LIST_QUERY_Params{
		UserID: userID,
		Limit:  10000,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	return len(txs)
}
```

**Use the raw-SQL form above by default.** The typed form is only cheaper if a perfect-fit query already exists. The typed form also avoids needing `"database/sql"` in the imports — but Step 13.3 pulls `sql.NullString` in independently, so the import is needed either way.

- [ ] **Step 13.2: Write Test #5 — unresolved collisions return 409**

Add this test directly below the Task 11 GET tests:

```go
// TestHandleImportConfirm_UnresolvedCollisions_Returns409 verifies that
// confirming a session that still contains a non-skipped collision
// group is rejected with 409 UNRESOLVED_COLLISIONS and zero rows are
// inserted. Uploads two identical rows (same date, description,
// amount, category) which produce a size-2 intra_file collision
// group, then immediately confirms without PATCHing — the backend
// must refuse the import.
//
// This test owns the "no partial insert" invariant: Phase 3.4 without
// this gate would insert the first identical row and silently skip
// the rest with skipReasonDuplicate, losing 19 of 20 rows in the
// 20-Starbucks-receipts case. After Chunk 3, /confirm returns 409
// and the DB is untouched.
func TestHandleImportConfirm_UnresolvedCollisions_Returns409(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "collider", "member")

	// Two identical rows — intra_file collision with no resolution.
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00", "Food"},
		{"2025-01-07", "Starbucks", "5.00", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Sanity: upload already flagged the collision group. If this
	// precondition fails, the bug is in Chunk 1 (upload-time
	// grouping), not Chunk 3 — the confirm re-check can't be tested
	// if upload is missing the group.
	uploadGroups, _ := uploadResp["collision_groups"].([]any)
	if len(uploadGroups) != 1 {
		t.Fatalf("precondition: upload should return 1 collision group, got %d", len(uploadGroups))
	}

	// Build a category_map so confirm can resolve categories.
	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	catMap := make(map[string]float64)
	var defaultID int64
	for _, c := range cats {
		catMap[c.Name] = float64(c.ID)
		if c.Name == "Food" || c.Name == "Groceries" {
			defaultID = c.ID
		}
	}
	if defaultID == 0 {
		defaultID = cats[0].ID
	}

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": defaultID,
		"category_map":        catMap,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)
	if code, _ := confirmResp["code"].(string); code != "UNRESOLVED_COLLISIONS" {
		t.Errorf("code: want UNRESOLVED_COLLISIONS, got %v", confirmResp["code"])
	}
	groupsResp, ok := confirmResp["collision_groups"].([]any)
	if !ok {
		t.Fatalf("collision_groups missing from 409 body, got %T", confirmResp["collision_groups"])
	}
	if len(groupsResp) != 1 {
		t.Errorf("collision_groups: want 1 group, got %d", len(groupsResp))
	}

	// Zero-insert invariant: no transactions exist for this user.
	// The session should also still be present in importStore — 409
	// does not clean up state (only 200 does), so the frontend can
	// re-submit after editing.
	if count := countTransactionsForUser(t, db, user.ID); count != 0 {
		t.Errorf("DB leaked %d rows past the 409 gate — expected 0", count)
	}
	if _, stillPresent := importStore.Load(importID); !stillPresent {
		t.Error("expected session to remain in importStore after 409 — 409 should NOT reap")
	}
}
```

- [ ] **Step 13.3: Write Test #6 — happy path persists `content_hash`**

```go
// TestHandleImportConfirm_PersistsContentHash verifies that a
// successful /confirm writes a non-null content_hash to every inserted
// transaction row. This is the regression guard for the full Phase
// 3.4b invariant: after confirm, the DB rows have content_hash
// populated so a re-import of the same file would trigger the
// collision detection path. Without this, the re-import path silently
// double-inserts.
//
// Uses two distinct rows (no collision) so confirm hits the happy
// path, then SELECTs content_hash directly from the transactions
// table and asserts both values are populated and distinct.
func TestHandleImportConfirm_PersistsContentHash(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "hasher", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00", "Food"},
		{"2025-01-08", "Trader Joe's", "42.10", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	catMap := make(map[string]float64)
	var defaultID int64
	for _, c := range cats {
		catMap[c.Name] = float64(c.ID)
		if c.Name == "Food" || c.Name == "Groceries" {
			defaultID = c.ID
		}
	}
	if defaultID == 0 {
		defaultID = cats[0].ID
	}

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": defaultID,
		"category_map":        catMap,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)
	if imported, _ := confirmResp["imported"].(float64); int(imported) != 2 {
		t.Errorf("imported: want 2, got %v", confirmResp["imported"])
	}

	// Pull every content_hash value for this user and assert both are
	// non-null and distinct. Queries the raw column because the typed
	// Transaction struct exposes content_hash via sql.NullString and
	// asserting on the raw scan is the shortest path to the invariant.
	rowsRS, err := db.Query("SELECT content_hash FROM transactions WHERE user_id = ? AND deleted_at IS NULL ORDER BY date", user.ID)
	if err != nil {
		t.Fatalf("select content_hash: %v", err)
	}
	defer rowsRS.Close()

	var hashes []string
	for rowsRS.Next() {
		var hashCell sql.NullString
		if err := rowsRS.Scan(&hashCell); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !hashCell.Valid || hashCell.String == "" {
			t.Error("row has NULL or empty content_hash — confirm path did not populate it")
		}
		hashes = append(hashes, hashCell.String)
	}
	if err := rowsRS.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(hashes) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(hashes))
	}
	if hashes[0] == hashes[1] {
		t.Errorf("both inserted rows have the same content_hash %q — they should differ for distinct rows", hashes[0])
	}
}
```

- [ ] **Step 13.4: Write Test #7 — skipped rows excluded via PATCH then confirm**

```go
// TestHandleImportConfirm_SkippedRows_ExcludedFromInserts verifies the
// skip ≠ unresolved distinction: a row whose Skip field was flipped
// via a Chunk 2 PATCH is excluded from inserts entirely, does NOT
// count toward the collision re-check, and IS counted toward the
// `skipped` field of the confirm response.
//
// Upload two distinct rows → PATCH row 0 with skip=true → confirm →
// assert imported=1, skipped=1, total=2, and that only row 1 landed
// in the DB. Exercises the full round-trip of PATCH's Skip mutation
// flowing into confirm's handler-level filter.
func TestHandleImportConfirm_SkippedRows_ExcludedFromInserts(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "skipper", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00", "Food"},
		{"2025-01-08", "Trader Joe's", "42.10", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// PATCH row 0 to set skip=true via the Chunk 2 handler.
	patchBody, _ := json.Marshal(map[string]any{
		"field": "skip",
		"value": true,
	})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/import/"+importID+"/rows/0", bytes.NewReader(patchBody))
	patchReq = withUserAndURLParams(patchReq, user, map[string]string{
		"importID": importID,
		"rowID":    "0",
	})
	patchRec := httptest.NewRecorder()
	h.handleImportPatchRow(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d; body: %s", patchRec.Code, patchRec.Body.String())
	}

	// Now confirm. Expect the skipped row to be excluded from inserts.
	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	catMap := make(map[string]float64)
	var defaultID int64
	for _, c := range cats {
		catMap[c.Name] = float64(c.ID)
		if c.Name == "Food" || c.Name == "Groceries" {
			defaultID = c.ID
		}
	}
	if defaultID == 0 {
		defaultID = cats[0].ID
	}

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": defaultID,
		"category_map":        catMap,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)
	if imported, _ := confirmResp["imported"].(float64); int(imported) != 1 {
		t.Errorf("imported: want 1, got %v", confirmResp["imported"])
	}
	if skipped, _ := confirmResp["skipped"].(float64); int(skipped) != 1 {
		t.Errorf("skipped: want 1 (the user-skipped row), got %v", confirmResp["skipped"])
	}
	if total, _ := confirmResp["total"].(float64); int(total) != 2 {
		t.Errorf("total: want 2, got %v", confirmResp["total"])
	}

	// DB verification: only row 1 (Trader Joe's) should exist; the
	// skipped row (Starbucks) must not have been inserted.
	descRows, err := db.Query("SELECT description FROM transactions WHERE user_id = ? AND deleted_at IS NULL", user.ID)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	defer descRows.Close()

	var descriptions []string
	for descRows.Next() {
		var d string
		if err := descRows.Scan(&d); err != nil {
			t.Fatalf("scan: %v", err)
		}
		descriptions = append(descriptions, d)
	}
	if err := descRows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(descriptions) != 1 {
		t.Fatalf("expected exactly 1 row in DB, got %d: %v", len(descriptions), descriptions)
	}
	if descriptions[0] != "Trader Joe's" {
		t.Errorf("expected only Trader Joe's in DB, got %q — the skipped Starbucks row leaked past the filter", descriptions[0])
	}
}
```

- [ ] **Step 13.5: Run the three new tests**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImportConfirm_UnresolvedCollisions_Returns409|TestHandleImportConfirm_PersistsContentHash|TestHandleImportConfirm_SkippedRows_ExcludedFromInserts' -v"
```

Expected: all three PASS. Failure diagnostics:

- **#5 fails "expected 409, got 200":** the confirm handler's re-check block is missing, mis-ordered, or receiving wrong arguments. Read Step 12.1 — the re-check must call `buildCollisionGroups` with `req.CategoryMap, req.DefaultCategoryID` (confirm-time resolution), not `nil, 0` (upload-time defaults). If those arguments are wrong, the hash formula will differ between upload and confirm and the re-check will see a different set of collisions.
- **#5 fails "DB leaked N rows past the 409 gate":** the re-check's `return` after `writeJSON(..., 409, ...)` is missing, so the handler falls through into `BeginTx` and the insert loop. Read Step 12.1 carefully — every `writeError`/`writeJSON` branch in this handler must be followed by `return`.
- **#5 fails "expected session to remain in importStore after 409":** the handler deleted the session on the 409 branch. 409 is a recoverable error — the frontend can re-submit after editing — so the session must remain in the store. Only 200 deletes the session (see the `importStore.Delete(req.ImportID)` line after `tx.Commit`). If a 409 is deleting, the `importStore.Delete` call moved above the `writeJSON` for 409; move it back down.
- **#6 fails "row has NULL or empty content_hash":** `processImportRows`'s insert path is not setting `content_hash`. This is a Phase 3.4 behavior (not new to 3.4b), so if this fails it indicates a pre-existing bug surfaced by a new assertion. Grep `internal/api/import_handlers.go` for `ContentHash:` — there should be a line inside `processImportRows`'s insert branch that populates `sql.NullString{String: hash, Valid: true}`. If the field is missing, add it to the `CreateTransactionParams` literal; this is a real bug, not a test flake.
- **#6 fails "both inserted rows have the same content_hash":** two distinct rows in the xlsx happened to produce identical hashes. Check the test data — Starbucks/$5 and Trader Joe's/$42.10 have different descriptions and amounts so the formula should produce different bytes. If they're identical, the hash implementation is ignoring description or amount — not a 3.4b problem, surface it to Phase 3.4 maintainers by opening a separate bug ticket.
- **#7 fails "imported: want 1, got 2":** the skip filter in Step 12.2 is not engaging. Most likely cause: the PATCH handler is mutating a copy of the row instead of the live slice, so `row.Skip = true` doesn't persist into `entry.Rows[0]`. Re-read Chunk 2 Step 8.1 — the PATCH handler takes a pointer into `entry.Rows[rowID]` via `row := &entry.Rows[rowID]`, specifically to avoid this bug.
- **#7 fails "imported: want 1, got 0":** the skip filter is over-aggressive — both rows are being filtered out. Check Step 12.2: the filter condition is `if row.Skip { continue }`, not `if !row.Skip { continue }`. One character wrong inverts the entire behavior.
- **#7 fails "expected only Trader Joe's in DB, got 'Starbucks'":** the skip filter is filtering row 1 instead of row 0. The loop in Step 12.2 iterates `entry.Rows` (not a reverse-sorted copy), and the filter condition is `row.Skip` (not `row.RowID == 0`). If Trader Joe's is missing and Starbucks is present, the PATCH targeted the wrong row — grep the test for `/rows/0` and confirm it's row 0 that was patched. If row 0 was patched but Starbucks is in the DB, the `importRow` RowID is out of sync with the slice index — check Chunk 1 Task 1 Step 1.2 (`ir.RowID = len(parsedRows)` must run BEFORE the append, so RowID always equals the final slice position).

- [ ] **Step 13.6: Run the full import test set to catch cross-test regressions**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine sh -c "apk add --no-cache build-base >/dev/null && go test ./internal/api -run 'TestHandleImport|TestImport|TestProcessImport' -v"
```

Expected: every existing and new import test passes. If any pre-Chunk-3 test fails, the regression is most likely in Task 12's response-total rewrite — the old formula was `len(result.Skipped) + len(result.Errored)` and the new formula is `len(entry.Rows) - len(result.Inserted)`. These should agree for any test that doesn't use the Skip field, but audit by grepping `result.Errored\\|result.Skipped` in `import_handlers.go` to find any path where a row was previously counted in `result.Skipped` and is now contributing to both buckets. If `TestHandleImport_DoubleImport_SkipsDuplicates` still fails after the Step 12.4 update, re-read its expected body — the new assertion should be `409 + collision_groups`, not the old `imported/skipped` numbers.

- [ ] **Step 13.7: Commit**

```bash
git add internal/api/import_handlers_test.go
git commit -m "test(import): 409 / content_hash persist / skip-excludes"
```

---

## Chunk 4: Frontend Data Layer

Chunk 4 lays the data-layer groundwork for the inline-edit preview UI that Chunk 5 will render. It is purely non-visual: types, endpoint wrappers, a dedicated `useImportSession` hook, and unit tests for both the wrapper module and the hook. No JSX is touched in this chunk — the existing `ImportPreviewStep` table in `Settings.tsx` keeps running unchanged until Chunk 5 replaces it.

**What changes:**
- `web/src/api/types.ts` — add `CollisionGroup`, `CollisionReason`, `DbMatchPreview`, `PatchRowRequest`, `PatchRowResponse`, and an `UnresolvedCollisionsError`-friendly `ImportConfirmError` shape. Extend `ImportRow` with `row_id`, `skip`, and `content_hash`. Extend `ImportPreview` with `collision_groups` and `expires_at` fields.
- `web/src/lib/storage-keys.ts` — add one entry: `importId: 'spendrop-import-id'`.
- `web/src/api/import.ts` — **new module** that encapsulates all five import endpoints (upload, get, patch, confirm, cancel), the `UnresolvedCollisionsError` class, and its 409 unwrap logic. Thin wrappers over `api.upload/get/patch/post/del`.
- `web/src/api/import.test.ts` — **new test file** covering the 409→`UnresolvedCollisionsError` unwrap, the 404→`NotFoundError` unwrap, the 500 fallback-Error case, and the PATCH URL shape. The module never touches `localStorage` — that's the hook's job.
- `web/src/hooks/useImportSession.ts` — **new hook** owning preview state, PATCH queue ref, `pendingPatchCount`, `cellErrors` map, localStorage resume on mount, `handleFileUpload`, `patchRow`, `confirmImport`, `cancelImport`, and derived `canImport` / `unresolvedCount` flags.
- `web/src/hooks/useImportSession.test.ts` — **new test file** covering PATCH queue serialization (stalled-fetch concurrency gate), localStorage resume on mount (200 and 404 paths), 409 state update on confirm **with user-edit preservation**, skipping every member of a collision group → group resolved (the skip-is-sticky invariant), and cellErrors set on 400 + cleared on next 200.

**What stays untouched (Chunk 5 handles these):**
- `Settings.tsx` — the existing `DataSection` import wizard still runs the old preview table. Chunk 5 will cut over to the new hook and rebuild the table.
- Every other UI component, page, and style file.

**Files touched in this chunk:**
- Modify: `web/src/api/types.ts` — extend `ImportRow` + `ImportPreview`, add `CollisionGroup` / `DbMatchPreview` / `PatchRowRequest` / `PatchRowResponse` / `CollisionReason`
- Modify: `web/src/lib/storage-keys.ts` — one new key
- Create: `web/src/api/import.ts`
- Create: `web/src/api/import.test.ts`
- Create: `web/src/hooks/useImportSession.ts`
- Create: `web/src/hooks/useImportSession.test.ts`

**File size sanity check:** After Chunk 4 lands, the new files sizes approximate to: `import.ts` ~160 lines, `import.test.ts` ~210 lines, `useImportSession.ts` ~275 lines, `useImportSession.test.ts` ~430 lines. All well under the "hard to reason about as a whole" threshold. `Settings.tsx` is left unchanged at 1582 lines — Chunk 5 will shrink it once the new hook takes over the state it currently holds inline.

---

### Task 14: Extend API types and storage keys

**Files:**
- Modify: `web/src/api/types.ts` — extend `ImportRow` and `ImportPreview`, add five new exported types
- Modify: `web/src/lib/storage-keys.ts` — add `importId` entry

No runtime test is written for this task — it is pure type-layer work. The verification step is `npm run lint` (which runs `tsc --noEmit`), which must still pass after the edits. Downstream tasks in Chunk 4 will provide the runtime coverage once they compile against these types.

- [ ] **Step 14.1: Extend `ImportRow` with `row_id`, `skip`, and `content_hash`**

Open `web/src/api/types.ts`. Find the existing `ImportRow` interface (around line 135). Replace it with:

```ts
export interface ImportRow {
  /**
   * Stable per-session index assigned by the backend at upload time.
   * The frontend uses this as the React key and as the {rowID} path
   * parameter for PATCH requests. IDs are 0-indexed and dense: row_id
   * always equals the row's position in the initial upload response's
   * rows array, and never changes for the lifetime of the session.
   */
  row_id: number;
  /**
   * True if the user has marked this row as excluded from the import
   * via the Skip checkbox. Skipped rows still render in the preview
   * (with strikethrough styling — Chunk 5) but are NOT sent to the DB
   * on /confirm and do NOT count toward the unresolved-collisions gate.
   * Skip is sticky: once set, only an explicit un-check clears it. No
   * other edit (including editing a collision row into uniqueness)
   * ever mutates skip client-side — the server is the source of truth.
   */
  skip: boolean;
  /**
   * SHA-256 content hash of (date, amount_cents, description, category)
   * computed by the backend. Used by the collision detector. The
   * frontend treats this as opaque — it is shown in the UI only as a
   * debug tooltip (if at all) and is never hashed client-side.
   */
  content_hash: string;
  date: string;
  description: string;
  amount: number;
  original_amount?: number;
  original_currency?: string;
  category: string;
  tags?: string;
  notes?: string;
}
```

**Reasoning for the added fields:**
- `row_id` — without a stable server-assigned id, the React key for each table row would collapse to array index, which breaks mid-edit if the backend ever reorders rows (and it might — Chunk 3 filters skip rows at confirm time, so index ≠ row_id after a skip PATCH). `row_id` is also the PATCH URL path param.
- `skip` — the boolean that drives the Skip checkbox column. Lives on the row (not in a parallel `skippedRowIds: Set<number>`) so the server response is the single source of truth and a row-merge never has to reconcile two data structures.
- `content_hash` — needed for the collision-group membership lookup: `collision_groups[i].member_row_ids` lists the row_ids in a group, and the UI joins them to the full row records via `row_id`. The hash itself is technically only needed for dev-tool debug, but exposing it on the row type lets us skip an optional-chaining dance in Chunk 5's render code.

- [ ] **Step 14.2: Add `CollisionReason`, `DbMatchPreview`, and `CollisionGroup` types**

Immediately below the updated `ImportRow` interface (still in `types.ts`), add:

```ts
/**
 * Why a set of rows share a content hash.
 * - `intra_file`: two or more rows in the current upload session
 *   produce the same hash. The user must edit or skip some of them.
 * - `db_match`: at least one row in the group also matches a live
 *   (non-tombstoned) transaction already in the database. The
 *   `db_match` preview is attached so the UI can show what the
 *   colliding DB row looks like.
 *
 * If a group qualifies as both (two file rows collide AND match a DB
 * row), the backend reports it as `db_match` — the more actionable
 * label (the user can see the DB conflict and decide whether to skip
 * the whole import or edit the rows).
 */
export type CollisionReason = 'intra_file' | 'db_match';

/**
 * A lightweight snapshot of a live DB transaction that collides with
 * an uploaded row. Sent inline on the `CollisionGroup` so the UI can
 * render "this matches: Starbucks, $5.00, Jan 7 2025, Coffee" without
 * a second round-trip.
 */
export interface DbMatchPreview {
  id: number;
  date: string; // ISO date (YYYY-MM-DD)
  description: string;
  amount_cents: number;
  category_name: string;
}

/**
 * A set of rows that share a content hash. The `member_row_ids` array
 * lists the 2+ row_ids in the group (groups of size 1 are not emitted
 * — they are not collisions). The UI renders a header row above each
 * group showing "⚠ N rows collide" and a "Skip all in group" button.
 */
export interface CollisionGroup {
  group_id: string;
  reason: CollisionReason;
  member_row_ids: number[];
  /** Present only when reason === 'db_match'. */
  db_match?: DbMatchPreview;
}
```

- [ ] **Step 14.3: Extend `ImportPreview` with `collision_groups` and `expires_at`**

Find the existing `ImportPreview` interface. Replace it with:

```ts
export interface ImportPreview {
  import_id: string;
  row_count: number;
  rows: ImportRow[];
  columns: string[];
  unique_categories: string[];
  /**
   * Every detected collision group from the current session state.
   * Empty on a clean file. Recomputed by the backend on every PATCH
   * and every GET — never derived on the client.
   */
  collision_groups: CollisionGroup[];
  /**
   * ISO-8601 timestamp (UTC) at which the backend will evict this
   * session from the in-memory importStore. The frontend reads this
   * only to show a countdown in the footer (Chunk 5) — it does NOT
   * attempt to refresh the session or warn before expiry.
   */
  expires_at: string;
}
```

**Important:** `predicted_skips` is NOT on this type. If it ever was (check the current file — see grep below), remove any lingering field. The backend removes `predicted_skips` from the response in Chunk 1, so leaving a dead TypeScript field behind would silently type-match `undefined` and hide the cutover bug.

```bash
grep -n "predicted_skips\|predictedSkips" web/src/api/types.ts
```

Expected after Step 14.3: no matches. If any remain, delete them.

- [ ] **Step 14.4: Add `PatchRowRequest` and `PatchRowResponse`**

Still in `types.ts`, directly below the updated `ImportPreview`:

```ts
/**
 * Body for `PATCH /api/import/{importID}/rows/{rowID}`. The backend's
 * per-field validator splits on `field` — all four variants hit
 * different code paths server-side (date/amount go through the
 * existing parsers; description runs length + trim; skip is a raw
 * boolean with no validation).
 */
export interface PatchRowRequest {
  field: 'date' | 'description' | 'amount' | 'skip';
  value: string | boolean;
}

/**
 * Response shape for a successful PATCH. The backend returns the
 * FULL session snapshot (not just the patched row) so the frontend
 * can re-render collision state without a separate GET round-trip.
 * The shape matches `ImportPreview` exactly — we alias it rather
 * than redeclaring it to keep the contract in one place.
 */
export type PatchRowResponse = ImportPreview;
```

- [ ] **Step 14.5: Add `importId` to `STORAGE_KEYS`**

Open `web/src/lib/storage-keys.ts`. Inside the `STORAGE_KEYS` object, directly after `transactionsPerPage`, add:

```ts
  /**
   * The import_id of the currently-active import preview session, if
   * any. Set on successful upload, read on component mount to resume
   * a session after F5 / tab refresh, cleared on successful confirm
   * or on 404 from the resume GET.
   */
  importId: 'spendrop-import-id',
```

The value string follows the existing `spendrop-*` kebab-case convention (the design doc mentions `spendrop_import_id` but that was a generic placeholder — in-code convention wins).

- [ ] **Step 14.6: Verify types compile**

Run:

```bash
cd web && npm run lint
```

Expected: PASS. If it fails, the error is most likely one of:

- **`TS2339: Property 'collision_groups' does not exist on type 'ImportPreview'` in `Settings.tsx`** — good failure. `Settings.tsx` does not yet use `collision_groups`. No fix needed at this task; the field is added to the type now and used by the hook in Task 17. If tsc flags a usage site that doesn't look like `Settings.tsx` (e.g. a test file), investigate — you may have introduced a type regression.
- **`TS2741: Property 'row_id' is missing in type '{ ... }'`** — means a test fixture or autoMapCategories helper is constructing a bare `ImportRow` literal without the new required fields. Search the codebase:

```bash
grep -rn "ImportRow\|preview.rows\[" web/src/ --include="*.ts" --include="*.tsx"
```

Add the missing fields to any inline literals (use `row_id: i, skip: false, content_hash: ''` as sane defaults for test fixtures — Chunk 5 will replace those anyway). **Do NOT** make `row_id` / `skip` / `content_hash` optional on the type — they are always present on a real backend response, and making them optional would hide the Chunk 5 bug where the UI forgets to render a Skip checkbox for rows with `skip === undefined`.
- **`TS2741: Property 'expires_at' is missing`** — same fix pattern as `row_id`. Use `expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString()` in test fixtures.

**Also scan for type-assertion bypasses** — a test that constructs a row via `{} as ImportRow` would slip through the compile check even though it's missing the new required fields. Run:

```bash
grep -rn "as ImportRow\|as ImportPreview\|as ImportResult" web/src/ --include='*.ts' --include='*.tsx'
```

If any match lands inside a test file that currently predates the new required fields, add the fields explicitly (same defaults as above) rather than leaving the assertion. Production code should not be using these assertions at all — if it is, that is a pre-existing smell to flag separately.

- [ ] **Step 14.7: Commit**

```bash
git add web/src/api/types.ts web/src/lib/storage-keys.ts
git commit -m "feat(import-types): add CollisionGroup, PatchRow* types + row_id/skip fields"
```

---

### Task 15: Create `api/import.ts` module with typed errors

**Files:**
- Create: `web/src/api/import.ts` — all five import endpoint wrappers, two typed error classes (`UnresolvedCollisionsError`, `NotFoundError`), and a shared `apiBaseURL()` helper
- Modify: `web/src/api/client.ts` — **not modified in this task**. We considered adding a generic "preserve error body" branch to `ApiClient.request` but it would change the throw shape for every existing caller across the codebase. Instead, the two endpoints that need typed errors (`getImportSession` for 404 → `NotFoundError`, `confirmImport` for 409 → `UnresolvedCollisionsError`) bypass `api.get`/`api.post` and hit `fetch` directly. The duplication cost is ~25 lines and it contains the blast radius entirely inside `import.ts`.

- [ ] **Step 15.1: Re-read the current `ApiClient.request` error path**

Open `web/src/api/client.ts`. Find the `request` method (around line 12). Look at what happens on `!response.ok`:

```ts
if (!response.ok) {
  const fallback = `HTTP ${response.status}`;
  const error = await response
    .json()
    .catch(() => null);
  throw new Error(
    (error as { error?: string } | null)?.error || fallback,
  );
}
```

**Observation:** the request method parses the error body into `{ error?: string }` and throws a plain `Error` whose message is that string. The full body (which for 409 will include `collision_groups`) is discarded.

This means the import module cannot use `api.post` for confirm and then catch by 409 — the body is already gone by the time the error reaches the caller. Step 15.2 has to either (a) add a new branch to `ApiClient.request` that preserves the full body for specific status codes, or (b) bypass `api.post` for the confirm call and hit `fetch` directly. Pick (b) — it is surgical (only one endpoint needs the full body) and does not change the behavior of any other callsite of `api.post`.

- [ ] **Step 15.2: Create `web/src/api/import.ts`**

Create a new file `web/src/api/import.ts`:

```ts
import { api } from './client';
import type {
  CollisionGroup,
  ImportPreview,
  ImportResult,
  PatchRowRequest,
  PatchRowResponse,
} from './types';

/**
 * Resolves the API base URL from Vite env config, matching `ApiClient`'s
 * logic in `client.ts`. Extracted so the two `fetch`-direct endpoints
 * below (`getImportSession`, `confirmImport`) share exactly one
 * resolver — this prevents dev-vs-prod URL drift between them and the
 * rest of the ApiClient callsites. If `client.ts` ever starts doing
 * additional normalization (trailing-slash handling, CORS fallbacks,
 * version prefixes), update this helper to match rather than bypassing.
 */
function apiBaseURL(): string {
  const base = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? '/api';
  return base.replace(/\/+$/, '');
}

/**
 * Thrown when `POST /api/import/confirm` returns 409 UNRESOLVED_COLLISIONS.
 * Carries the full `collision_groups` payload from the server so the
 * caller (useImportSession hook) can update its local collision state
 * without a second round-trip.
 *
 * We subclass Error rather than returning a `Result<T, E>` because React
 * hook state flow already threads through `try/catch` on every async
 * call — a typed error keeps the happy-path signature clean.
 */
export class UnresolvedCollisionsError extends Error {
  readonly collision_groups: CollisionGroup[];

  constructor(collision_groups: CollisionGroup[]) {
    super('Import has unresolved collisions');
    // Restore prototype chain in ES5-transpiled output (Vite targets
    // ES2020 by default so this is defensive, but costs nothing).
    Object.setPrototypeOf(this, UnresolvedCollisionsError.prototype);
    this.name = 'UnresolvedCollisionsError';
    this.collision_groups = collision_groups;
  }
}

/**
 * Thrown when `GET /api/import/{importID}` returns 404 — i.e. the
 * server-side session has expired (60-minute idle TTL) or never
 * existed. The hook's mount effect uses `err instanceof NotFoundError`
 * to silently drop the stale localStorage key without surfacing an
 * error banner (an expired session is a normal user journey after
 * a coffee break, not a failure state).
 *
 * Typed separately from `UnresolvedCollisionsError` because the two
 * have different semantics and the caller wants to distinguish them
 * with a single `instanceof` check. Using plain `Error` with a magic
 * string match (`err.message.includes('not found')`) is fragile: it
 * couples the UI's silence logic to an exact backend error message,
 * and any backend change (or localization) would quietly turn the
 * expected-404 path into a visible error banner.
 */
export class NotFoundError extends Error {
  readonly importID: string;

  constructor(importID: string) {
    super('Import session not found or expired');
    Object.setPrototypeOf(this, NotFoundError.prototype);
    this.name = 'NotFoundError';
    this.importID = importID;
  }
}

/**
 * Uploads a file and returns the initial preview. Thin wrapper over
 * the existing `api.upload` helper — the only reason this lives in a
 * dedicated module is to keep all five import endpoints co-located
 * with their types and error class.
 */
export function uploadImport(file: File): Promise<ImportPreview> {
  return api.upload<ImportPreview>('import/upload', file);
}

/**
 * Resumes an existing session. Called on component mount if a valid
 * import_id is in localStorage. On 404 (session expired or never
 * existed), throws `NotFoundError` carrying the attempted importID
 * — the hook catches it via `instanceof` and silently drops the
 * localStorage key.
 *
 * Bypasses `api.get` for the same reason `confirmImport` bypasses
 * `api.post`: `ApiClient.request` discards the status code on non-200
 * responses and throws a flat `Error`, so the caller cannot reliably
 * distinguish 404 from 500 without string-matching the error message
 * (fragile). Hitting fetch directly lets us type the 404 branch.
 */
export async function getImportSession(importID: string): Promise<ImportPreview> {
  const response = await fetch(
    `${apiBaseURL()}/import/${encodeURIComponent(importID)}`,
    { credentials: 'include' },
  );

  if (response.status === 404) {
    throw new NotFoundError(importID);
  }

  if (!response.ok) {
    const fallback = `HTTP ${response.status}`;
    const error = (await response.json().catch(() => null)) as
      | { error?: string }
      | null;
    throw new Error(error?.error || fallback);
  }

  return (await response.json()) as ImportPreview;
}

/**
 * Patches a single field on a single row. The backend returns the
 * FULL session snapshot (shape: PatchRowResponse = ImportPreview) so
 * the caller does not need to stitch together partial updates.
 */
export function patchImportRow(
  importID: string,
  rowID: number,
  body: PatchRowRequest,
): Promise<PatchRowResponse> {
  return api.patch<PatchRowResponse>(
    `import/${encodeURIComponent(importID)}/rows/${rowID}`,
    body,
  );
}

/**
 * Confirms the import. On 409 UNRESOLVED_COLLISIONS, throws an
 * `UnresolvedCollisionsError` carrying the full collision_groups array
 * so the hook can re-render collision state without a second GET.
 *
 * Bypasses `api.post` because the shared ApiClient.request method
 * discards the error body on non-200 responses (see Step 15.1). We
 * hit fetch directly for this one endpoint to keep the 409 body
 * intact; all other non-200 status codes (including 401, 403, 500)
 * fall through to the generic `!response.ok` branch, which extracts
 * `error.error` from the body — same contract as `ApiClient.request`.
 */
export async function confirmImport(payload: {
  import_id: string;
  default_category_id?: number;
  category_map: Record<string, number>;
}): Promise<ImportResult> {
  const response = await fetch(`${apiBaseURL()}/import/confirm`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });

  if (response.status === 409) {
    const body = (await response.json().catch(() => null)) as
      | { code?: string; collision_groups?: CollisionGroup[] }
      | null;
    throw new UnresolvedCollisionsError(body?.collision_groups ?? []);
  }

  if (!response.ok) {
    const fallback = `HTTP ${response.status}`;
    const error = (await response.json().catch(() => null)) as
      | { error?: string }
      | null;
    throw new Error(error?.error || fallback);
  }

  return (await response.json()) as ImportResult;
}

/**
 * Cancels an in-flight preview session, freeing the server-side slot.
 * Swallows errors — cancel is best-effort and the UI should drop the
 * client-side state regardless of whether the DELETE landed.
 */
export function cancelImport(importID: string): Promise<void> {
  return api.del(`import/${encodeURIComponent(importID)}`).catch(() => {});
}
```

**Note on `encodeURIComponent`:** `importID` is a UUID from the backend (see `import_handlers.go` — it's generated via `uuid.NewString()` and contains only `[0-9a-f-]`). The encoder is strictly defensive — it costs nothing and prevents a subtle bug if the id shape ever changes.

**Note on dropping the 401 special case:** an earlier draft had a dedicated `if (response.status === 401) throw new Error('Unauthorized')` branch in `confirmImport`. That was dropped because it swallowed the server's descriptive error body (`{"error": "token expired"}` would surface as the generic `"Unauthorized"`). Letting 401 fall through to the generic `!response.ok` branch preserves the backend's error message while still throwing a plain `Error` (not a typed one). If Chunk 5 needs to distinguish 401 from other non-200s for an "please log in again" redirect, add a typed `UnauthorizedError` then — not here.

**Sanity check against `client.ts`:** after writing `import.ts`, open `web/src/api/client.ts` and verify that `ApiClient` resolves its base URL via the same `import.meta.env.VITE_API_BASE_URL` env var with `'/api'` as the fallback. If it does anything more than that (e.g. strips trailing slashes differently, or prepends a version), match the behavior in `apiBaseURL()` above. A subtle dev-vs-prod mismatch between the two base-URL resolvers would break `getImportSession` / `confirmImport` in prod only.

**Note on the `fetch` duplication in `confirmImport`:** this is a deliberate surgical escape. We only pay the duplication cost because `ApiClient.request` eats the 409 body and we need that body. Do NOT refactor `ApiClient.request` to expose the error body — it would change the throw shape for every existing caller and risk masking real errors across the codebase. One-off `fetch` is the right trade-off.

- [ ] **Step 15.3: Verify types compile**

```bash
cd web && npm run lint
```

Expected: PASS. If it fails with `TS2304: Cannot find name 'ImportResult'`, add it to the imports at the top of `import.ts` — it already exists in `types.ts`.

- [ ] **Step 15.4: Commit**

```bash
git add web/src/api/import.ts
git commit -m "feat(import-api): add typed endpoint wrappers + UnresolvedCollisionsError"
```

---

### Task 16: Unit-test the `api/import.ts` module

**Files:**
- Create: `web/src/api/import.test.ts`

Four tests, each owning exactly one bug class:
1. **confirm 409 → `UnresolvedCollisionsError` with `collision_groups`** — guards the 409 unwrap and verifies via `instanceof` (not a name-string match) that the prototype chain survives async transpilation.
2. **confirm 500 → plain `Error` with `error` field** — guards that non-409 errors still surface as plain errors, not the typed class (regression guard: a naive `if (!response.ok) throw new UnresolvedCollisionsError` would trip this test).
3. **patchImportRow URL shape** — guards against future refactors that might break `/rows/{rowID}`.
4. **getImportSession 404 → `NotFoundError`** — guards that the typed error gets thrown on 404 (not a generic `Error`), and that the error carries the attempted `importID`. The hook uses `instanceof NotFoundError` to silently drop stale localStorage; if this test fails, the hook's 404 path regresses to surfacing an error banner.

- [ ] **Step 16.1: Write all four failing tests**

Create `web/src/api/import.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  confirmImport,
  getImportSession,
  patchImportRow,
  NotFoundError,
  UnresolvedCollisionsError,
} from './import';

const originalFetch = globalThis.fetch;

function mockFetch(responses: Array<Partial<Response> & { body?: unknown }>) {
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
```

- [ ] **Step 16.2: Run the tests and verify they PASS**

```bash
cd web && npm test -- src/api/import.test.ts
```

Expected: all four tests PASS (they're testing the module from Step 15.2, which is already implemented). Failure diagnostics:

- **"confirmImport throws UnresolvedCollisionsError on 409" FAILS with `expected confirmImport to reject`:** the 409 branch in `confirmImport` is not triggering. The most likely cause is that the test harness's `mockFetch` helper is overriding `ok` to `true` by default; re-read the mockFetch implementation — `r.ok ?? true` defaults to true, so the test body must pass `ok: false` explicitly. If the test does pass `ok: false` and the check still fails, the 409 branch is gated on `ok` instead of `status`. Grep `confirmImport`: the gate must be `if (response.status === 409)`, NOT `if (!response.ok && response.status === 409)`. A 409 is `!ok`, so the composite is equivalent — but if someone later refactors to `if (response.ok) { ... return ... }` and then a bare `throw` at the bottom, the 409 branch gets swallowed. Keep the explicit `response.status === 409` check in that order.
- **"confirmImport throws plain Error on 500" FAILS because the thrown error IS an UnresolvedCollisionsError:** the 500 branch is matching the 409 check. Verify the order in `confirmImport` — `if (response.status === 409)` MUST come before `if (!response.ok)`. If the order is reversed, a 409 would fall into the generic branch and throw a plain Error, and this test would pass for the wrong reason (and the 409 test would fail instead). The two tests together pin the order.
- **"patchImportRow hits /api/import/{id}/rows/{rowID}" FAILS with URL mismatch:** Check the template literal in `patchImportRow` — `import/${encodeURIComponent(importID)}/rows/${rowID}`. If the test shows `rows/undefined` or `rows/NaN`, the rowID argument is being passed as a string instead of a number, or the function signature is `(importID, body)` with rowID missing. Re-read the signature in `import.ts`.
- **"getImportSession throws NotFoundError on 404" FAILS with `expected caught to be instanceof NotFoundError` but received a plain Error:** `getImportSession` is still going through `api.get` instead of the direct `fetch` branch. Re-read Step 15.2 — the function body must call `fetch(...)` directly and check `response.status === 404` before the generic `!response.ok` branch. If `getImportSession` still starts with `return api.get<ImportPreview>(...)`, replace it with the `async` fetch-direct implementation from Step 15.2.
- **"getImportSession throws NotFoundError on 404" FAILS with `caught.importID is undefined`:** the `NotFoundError` constructor is not recording the id. Re-read the class body in Step 15.2 — the constructor must do `this.importID = importID` after the `Object.setPrototypeOf` call. Assigning before `setPrototypeOf` works in practice but is easy to accidentally drop during a refactor; keep the assignment as the last statement in the constructor.
- **`caught` is an `Error` but NOT a `NotFoundError`:** the prototype chain broke during transpilation. Verify the constructor calls `Object.setPrototypeOf(this, NotFoundError.prototype)` — without it, Vite's ESBuild transform can lose the subclass relationship in some target configs, and `instanceof` returns `false`. This is exactly the same defensive pattern `UnresolvedCollisionsError` uses.

- [ ] **Step 16.3: Commit**

```bash
git add web/src/api/import.test.ts
git commit -m "test(import-api): 409 unwrap, PATCH URL shape, 404 plain-Error"
```

---

### Task 17: Create `useImportSession` hook

**Files:**
- Create: `web/src/hooks/useImportSession.ts`

The hook owns the full import-session lifecycle. It is decoupled from any UI — Chunk 5 will wire it into `Settings.tsx` by calling `const session = useImportSession(); ...`. The existing `DataSection` inline state in `Settings.tsx` is NOT modified in this task; it continues to run the old preview table untouched. Chunk 5 does the cutover.

**Responsibilities:**
1. Upload a file → populate preview state → set localStorage importId
2. Resume on mount if localStorage has an importId (GET the session, handle 404 by clearing localStorage)
3. Maintain a serialized PATCH queue via `useRef<Promise<void>>` so cross-row edits never race
4. Track `pendingPatchCount` for the Chunk 5 Import-button lockout
5. Track per-cell errors in a `cellErrors: Record<string, { field, message }>` map; clear them on the next successful PATCH for the same cell
6. Compute derived state: `unresolvedCount` (collision groups minus all-skipped groups), `canImport` (unresolvedCount === 0 && pendingPatchCount === 0)
7. Provide a `confirmImport(categoryMap, defaultCategoryId)` method that catches `UnresolvedCollisionsError` and updates local collision state without a re-GET
8. Provide a `cancelImport()` method that DELETEs the session, clears localStorage, resets local state
9. On successful confirm, clear localStorage and transition to `importStep: 'done'`

- [ ] **Step 17.1: Create the hook file with the initial scaffold**

Create `web/src/hooks/useImportSession.ts`:

```ts
import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import type {
  CollisionGroup,
  ImportPreview,
  ImportResult,
  ImportRow,
  PatchRowRequest,
} from '../api/types';
import {
  uploadImport,
  getImportSession,
  patchImportRow,
  confirmImport as confirmImportAPI,
  cancelImport as cancelImportAPI,
  NotFoundError,
  UnresolvedCollisionsError,
} from '../api/import';
import { STORAGE_KEYS } from '@/lib/storage-keys';

export type ImportStep = 'upload' | 'preview' | 'done';

export interface CellError {
  field: PatchRowRequest['field'];
  message: string;
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

export function useImportSession(): UseImportSessionResult {
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [importStep, setImportStep] = useState<ImportStep>('upload');
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pendingPatchCount, setPendingPatchCount] = useState(0);
  const [cellErrors, setCellErrors] = useState<Record<string, CellError>>({});

  // Single-lane PATCH queue. Every enqueued PATCH awaits the previous
  // one's settlement before firing, so the backend never sees two
  // concurrent PATCHes for the same import_id. See design doc
  // "Race prevention (cross-row PATCH ordering)".
  const patchQueueRef = useRef<Promise<void>>(Promise.resolve());

  // ---- localStorage resume on mount ----
  useEffect(() => {
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
  }, []);

  // ---- derived state ----
  const unresolvedCount = useMemo(() => {
    if (!preview) return 0;
    return computeUnresolvedCount(preview.collision_groups, preview.rows);
  }, [preview]);

  const canImport = preview !== null && unresolvedCount === 0 && pendingPatchCount === 0;

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
      setCellErrors({});
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
          setCellErrors((prev) => {
            if (!(cellKey in prev)) return prev;
            const next = { ...prev };
            delete next[cellKey];
            return next;
          });
        } catch (err) {
          const message =
            err instanceof Error ? err.message : 'Update failed';
          setCellErrors((prev) => ({
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
    setCellErrors({});
    setPendingPatchCount(0);
  }, [preview]);

  const startOver = useCallback(() => {
    // Called from the "Import another file" button on the done step.
    // No DELETE needed — the confirm already consumed the session.
    setPreview(null);
    setImportStep('upload');
    setResult(null);
    setError(null);
    setCellErrors({});
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
    canImport,
    uploadFile,
    patchRow,
    confirmImport,
    cancelImport,
    startOver,
  };
}
```

**Design notes embedded above:**
- `computeUnresolvedCount` is a pure function so the test suite can cover the all-skipped-group edge case directly without rendering the hook.
- `applyResponse` merges by object identity preservation (`rows.map(r => r.row_id === patched ? updated : r)`) — this is the focus-preservation invariant from the spec line 220. A naive `setPreview(fresh)` would replace every row object and React would unmount/remount every input, dropping focus during Tab-bursts.
- The PATCH queue is a classic single-lane pattern: `patchQueueRef.current = patchQueueRef.current.then(work).catch(noop)`. The `.catch(() => {})` on the queue tail is required — without it, a single 400 would freeze every future PATCH because `.then` would be permanently rejected.
- `pendingPatchCount` uses `Math.max(0, n - 1)` in the decrement to guard against a hypothetical double-decrement (defense-in-depth — the happy path never triggers it).
- `cellErrors` keys on `${rowID}:${field}` — each cell owns its own error. An edit on `3:description` does NOT clear `3:amount`.
- The `uploadFile` localStorage write is wrapped in try/catch because `localStorage.setItem` can throw on quota overflow in private-browsing modes. Resume is a nice-to-have, not a hard requirement.
- `confirmImport` catches `UnresolvedCollisionsError` and ONLY updates `collision_groups` — NOT `rows`. The backend did not change the row data on a 409; only the collision membership might differ (e.g. if another session PATCHed between the last local PATCH and the confirm). Preserving row data means the user's in-flight edits survive.

- [ ] **Step 17.2: Verify types compile**

```bash
cd web && npm run lint
```

Expected: PASS. Failure diagnostics:

- **`TS2322: Type 'Promise<void>' is not assignable to type 'Promise<void>'`** — the promise-chain typing is surprisingly subtle. If this fires, the queue-tail reassignment `patchQueueRef.current = next.catch(...).finally(...)` is returning a different promise type. The fix is an explicit `.then(() => undefined)` at the end to pin the type to `Promise<void>`.
- **`TS2345: Argument of type '...' is not assignable`** in `applyResponse` — most likely the `mergedRows.length !== fresh.rows.length` branch returns `fresh` directly, but `fresh.rows` might have a slightly different row type if `ImportPreview.rows` is `readonly ImportRow[]` in one place and `ImportRow[]` in another. Verify `ImportPreview.rows` is typed as `ImportRow[]` (mutable array) consistently.
- **`Cannot find module '@/lib/storage-keys'`** — the `@/` alias must be configured in `tsconfig.json`. Grep `tsconfig.json` for `"@/*"` — it should exist (other files use it, e.g. `Settings.tsx` imports `@/components/ui/button`). If the alias works for other imports, check for a typo in the import path.

- [ ] **Step 17.3: Commit**

```bash
git add web/src/hooks/useImportSession.ts
git commit -m "feat(import-hook): useImportSession with serialized PATCH queue + resume"
```

---

### Task 18: Unit-test `useImportSession`

**Files:**
- Create: `web/src/hooks/useImportSession.test.ts`

Seven tests, each owning exactly one bug class:
1. **Upload → preview set, localStorage written** — guards the happy path and the resume side effect
2. **Mount with stored importId → GET called, preview rehydrated** — guards the resume path
3. **Mount with stored importId → 404 clears localStorage, no error surfaced** — guards the expected-404 case; uses a realistic backend 404 body (not a magic `HTTP 404` string) to prove the `instanceof NotFoundError` check is what does the silencing
4. **PATCH queue serialization (stalled-fetch concurrency gate)** — proves that PATCH #2 does NOT fire while PATCH #1 is in-flight. The test stalls the first fetch on a manually-resolved promise and asserts `fetchMock.mock.calls.length === 2` (upload + PATCH 1) DURING the stall; only after resolving PATCH 1 does the third call land. A broken concurrent implementation (e.g. `patchQueueRef.current = next` with no chaining) would fail this test because all three fetches would fire immediately.
5. **PATCH 400 sets cellError; next successful PATCH on same cell clears it** — guards the 400 UX
6. **confirmImport catches UnresolvedCollisionsError; updates `collision_groups` WITHOUT clobbering `rows`** — guards the 409 recovery path AND the "preserve in-flight user edits" invariant. The test PATCHes row 0's description to a sentinel value BEFORE confirm, then asserts the sentinel survives the 409 — without this, a regression that does `setPreview({ ...prev, ...err.body })` would silently lose user edits.
7. **Skipping every member of a collision group flips `unresolvedCount` to 0 and `canImport` to true** — guards the `computeUnresolvedCount` skip-resolves rule (spec §§359–369). Without this test, nothing in the hook suite exercises the skip-is-sticky / skip-≠-unresolved invariant; a regression that counted skipped rows as unresolved would silently pass every other test.

- [ ] **Step 18.1: Write all seven failing tests**

Create `web/src/hooks/useImportSession.test.ts`:

```ts
import { renderHook, act, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useImportSession } from './useImportSession';
import { STORAGE_KEYS } from '@/lib/storage-keys';

const originalFetch = globalThis.fetch;

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
  const fetchMock = vi.fn().mockImplementation(async (_url: string) => {
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
    expires_at: '2099-01-01T00:00:00Z',
  };
}

describe('useImportSession', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('uploadFile sets preview and writes importId to localStorage', async () => {
    installFetchQueue([{ body: freshPreviewBody('abc') }]);
    const { result } = renderHook(() => useImportSession());

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

    const { result } = renderHook(() => useImportSession());

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

    const { result } = renderHook(() => useImportSession());

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

    let resolvePatch1: (value: Response) => void;
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

    const { result } = renderHook(() => useImportSession());

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

    const { result } = renderHook(() => useImportSession());

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

    const { result } = renderHook(() => useImportSession());

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

    const { result } = renderHook(() => useImportSession());

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
});
```

- [ ] **Step 18.2: Run the tests**

```bash
cd web && npm test -- src/hooks/useImportSession.test.ts
```

Expected: all seven tests PASS. Failure diagnostics:

- **Test 1 — "uploadFile sets preview and writes importId to localStorage" FAILS with `expected 'abc' but got null`:** the `localStorage.setItem` call in `uploadFile` is inside a try/catch whose catch block swallows the set — check that the set is NOT inside the inner catch. The shape should be `localStorage.setItem(...); /* no catch here */` nested inside the outer `try { uploadImport → setPreview → localStorage.setItem }` block. Also confirm the test environment has `localStorage` — Vitest's default jsdom env has it, but if the test is running under the `node` environment, there is no localStorage. Check `vitest.config.ts` and confirm `environment: 'jsdom'`.

- **Test 2 — "mount with stored importId rehydrates via GET" FAILS with `expected 'stored-id' but got null`:** the `useEffect` mount hook is running BEFORE the test sets up the fetch mock, or the `waitFor` is timing out. Re-read Step 17.1's mount effect — it must use `useEffect(() => { ... }, [])` with an empty dep array so it fires exactly once on mount. If the test still fails, check the `renderHook` result: the hook's state updates are async (the GET fires, then `.then(setPreview)` runs on a microtask), so `waitFor` is the right idiom.

- **Test 3 — "mount ... does NOT surface an error on 404" FAILS with `error` is not null:** the 404 path in the mount effect is not using `instanceof NotFoundError`. Re-read Step 17.1 — the mount catch clause must import `NotFoundError` from `@/api/import` and silence via `if (err instanceof NotFoundError) return;`. If the check is still a string match (`err.message.includes('HTTP 404')` or `err.message.includes('not found')`), this test fails because the mock 404 body is `{ error: 'session not found or expired' }` — that string does NOT contain `'HTTP 404'`, and the test deliberately chose a realistic body to prove string-matching is insufficient. Also verify that `getImportSession` in Step 15.2 actually throws `new NotFoundError(importID)` on status 404 — if it throws a plain `Error`, the `instanceof` check returns false and the silence branch is dead code.

- **Test 3 — "mount ... does NOT surface an error on 404" FAILS with `localStorage.getItem(...)` still equals `'expired-id'`:** the mount effect's catch branch is not calling `localStorage.removeItem` before the `instanceof NotFoundError` early return. The expected shape is `catch (err) { localStorage.removeItem(...); if (err instanceof NotFoundError) return; setError(...) }` — cleanup happens unconditionally for ALL resume failures. If `removeItem` is inside an `if (err instanceof NotFoundError)` branch, the test still passes for 404 but a regression where `removeItem` is inside the `setError` branch would trap the expired id on unknown errors.

- **Test 4 — "patchRow serializes ..." FAILS with `expected 2 calls, got 3` at the mid-stall assertion:** this is the canonical concurrency-bug signature. The PATCH queue is firing both PATCHes in parallel instead of waiting for PATCH #1 to settle. Re-read Step 17.1's `patchRow`: the hook must read the current queue tail, chain the new fetch onto it with `.then(work)`, AND write the chained promise back to `patchQueueRef.current` so the next call chains off the new tail. If the hook reads `patchQueueRef.current` but never writes to it, every call chains off the original (already-resolved) root and all fetches fire at once. Correct pattern:
  ```ts
  const next = patchQueueRef.current.then(() => fetchPatchRow(...));
  patchQueueRef.current = next.catch(noop).finally(decrement);
  return next;
  ```
  The `.catch(noop)` tail is critical — without it, a rejected PATCH poisons the queue for every subsequent PATCH.

- **Test 4 — "patchRow serializes ..." FAILS with `expected 3 calls, got 2` after `resolvePatch1(...)`:** the serialization works but the queue tail is not releasing PATCH #2 after PATCH #1 resolves. Either `.finally(decrement)` is swallowing the chain (use `.catch(noop).finally(decrement)`, not `.then(decrement)` which drops errors), or PATCH #2 was rejected internally before the resolve — add `console.log('tail fired')` inside the `.then(() => fetchPatchRow(...))` lambda to confirm. If `p1` or `p2` reject, Vitest surfaces that as an unhandled rejection inside `act()` — check the test output for warnings.

- **Test 4 — "patchRow serializes ..." FAILS with `pendingPatchCount === 0` at the mid-stall assertion:** the hook's `pendingPatchCount` state is being incremented inside the `.then` callback instead of synchronously at the top of `patchRow`. The counter must increment before the await/chain so mid-flight inspections see the right value. Decrement is correctly placed inside `.finally`.

- **Test 5 — "patchRow 400 cellError" FAILS with `cellErrors['0:date'] is undefined`:** the `setCellErrors` call inside the `.catch` block is not firing. Most likely `patchImportRow` in Step 15.2 is extracting the error message wrong — verify the helper does `await response.json()` then throws `new Error(body.error)`. The test body is `{ error: 'INVALID_DATE' }`, so `body.error === 'INVALID_DATE'`. If the helper throws `new Error('HTTP 400')` instead, the hook's catch reads `err.message === 'HTTP 400'` and the assertion `message === 'INVALID_DATE'` fails. Match the extraction path to the backend envelope.

- **Test 5 — "patchRow 400 cellError" FAILS because the second (success) PATCH does not clear `cellErrors['0:date']`:** the hook's success path never deletes the stale cellError. Re-read Step 17.1 — after a 200 PATCH, the hook must call `setCellErrors(prev => { const next = { ...prev }; delete next[key]; return next; })` for the `(rowID, field)` pair. If the hook only writes errors on failure and never clears them, this test fails on `toBeUndefined`.

- **Test 6 — "confirmImport 409 ..." FAILS with `collision_groups has length 0`:** the hook's `confirmImport` catch branch is not running. Most likely `err instanceof UnresolvedCollisionsError` returns `false` at runtime because of a prototype-chain bug in the error class — Step 15.2's `Object.setPrototypeOf(this, UnresolvedCollisionsError.prototype)` is the defensive fix for exactly this. If it's missing, the transpiled error loses its prototype across async boundaries. Add it back. Also verify `confirmImport` in `api/import.ts` actually throws `new UnresolvedCollisionsError(body.collision_groups ?? [])` on status 409 — if it throws a plain `Error`, the `instanceof` check fails silently and the hook falls through to the generic error branch.

- **Test 6 — "confirmImport 409 ..." FAILS with `rows[0].description === 'Starbucks'` (NOT `'EDITED_SENTINEL'`):** THIS is the regression this test guards. The hook's 409 catch branch is replacing the entire preview with fresh/pristine rows instead of updating only `collision_groups`. Re-read Step 17.1's `confirmImport` — the `UnresolvedCollisionsError` catch handler must call `setPreview(prev => prev ? { ...prev, collision_groups: err.collision_groups } : prev)` and NOTHING ELSE. Any of the following would clobber the sentinel and fail this test: `setPreview({ ...prev, ...err })` (spread-merges nothing useful since err is an Error, not an object with rows); `setPreview(await getImportSession(importID))` (refetches the pristine server state, losing unsynced edits); `setPreview(originalPreview)` (reverts to pre-edit snapshot). The catch handler must only touch `collision_groups`.

- **Test 7 — "skipping every member of a collision group flips unresolvedCount to 0" FAILS with `unresolvedCount === 1` after both skips:** the `computeUnresolvedCount` selector is counting skipped rows as unresolved. Re-read the selector (spec §§359–369) — the correct algorithm is: for each collision group, check if AT LEAST ONE member has `skip === false`; if every member has `skip === true`, the group is resolved even though the content hashes still collide. A regression that does `collision_groups.length` or `collision_groups.filter(g => g.member_row_ids.length > 0).length` ignores the skip state and over-counts. The selector must read `rows[row_id].skip` for each member id in the group.

- **Test 7 — "skipping every member ..." FAILS with `canImport === false` while `unresolvedCount === 0`:** the `canImport` flag is wired to something other than `unresolvedCount === 0`. Re-read Step 17.1 — `canImport` must be `preview !== null && !uploading && unresolvedCount === 0`. If it is ALSO guarded by `preview.collision_groups.length === 0`, it will never flip true in this test because the server's collision group is NOT removed when the user skips all members — it is only "resolved" in-memory by the skip count. Drop the extra guard.

- **Test 7 — "skipping every member ..." FAILS with `preview.rows[0].skip === false` after the first PATCH:** the hook's `patchRow` success branch is not merging the server's updated rows into local state. Re-read Step 17.1 — after a 200 OK response carrying the new preview, the success handler must call `setPreview(prev => prev ? { ...prev, rows: response.rows } : prev)`. If the handler is mutating `prev.rows[rowID]` in place, React will not re-render and the test sees stale state. Use an immutable merge.

- [ ] **Step 18.3: Commit**

```bash
git add web/src/hooks/useImportSession.test.ts
git commit -m "test(import-hook): queue serialization + resume + 400/409 paths"
```

---

## Chunk 5: Frontend UI + Settings Integration

**Goal:** Build the `ImportPreviewTable` component that renders the editable preview with collision highlighting, inline cell editing, a footer with an Import button, and collision-group "Skip all" headers. Wire it into `Settings.tsx`'s `ImportPreviewStep` alongside the existing category-mapping UI. At the end of this chunk the user can upload a spreadsheet, see collisions amber-highlighted, edit cells inline to resolve them, and click Import.

**Architecture:**

- `ImportPreviewTable` is **presentational**. It accepts `preview`, `cellErrors`, `canImport`, `pendingPatchCount`, `unresolvedCount`, and callback props (`onPatchRow`, `onConfirm`). It owns NO fetch logic and NO state besides transient edit-mode UI state (which cell is currently being edited, the draft value). `Settings.tsx` is the only place that holds `useImportSession` and passes its outputs down as props. This split makes the component testable with simple mock props and keeps the fetch-serialization logic confined to the hook (already proven in Chunk 4's tests).
- Visual states derive from `preview.rows[i]` directly on each render. A row is "collision" if its `row_id` appears in any `preview.collision_groups[].member_row_ids`. No local `isCollision` state, no `useEffect` that syncs from props — the importcsv #16 stale-style bug is structurally impossible if we never duplicate the source of truth.
- Per-cell editing uses an uncontrolled `Input` inside a conditionally-rendered edit wrapper. Double-click on a cell enters edit mode; the Input has `autoFocus` and onBlur/onKeyDown handlers. Enter/Tab commits → fires `onPatchRow(rowID, field, draftValue)` → the Input unmounts → next render shows the server's merged value. Escape cancels → the Input unmounts with no PATCH. React reconciliation preserves focus across re-renders because the object-identity-preserving row merge in `useImportSession` (verified in Chunk 4 test #4) keeps row objects stable when their fields haven't changed.
- Cell errors render as a red ring on the Input plus a `<p class="text-xs text-destructive">` sibling directly below. Errors clear on the next successful PATCH response for the same `(row_id, field)` pair — that clearing logic already lives in the hook's `patchRow` success branch (Chunk 4 Step 17.1).
- Collision groups render a non-data header row above their member rows with a "Skip all in group" button. The button's onClick fires N sequential `onPatchRow(rowID, 'skip', true)` calls through the hook's serialized queue — no batch endpoint, no concurrency, just N queued PATCHes handled by the same promise chain that test #4 proved.

**File structure:**
- **Create:** `web/src/components/ImportPreviewTable.tsx` — the editable table + footer + collision group headers, ~350 lines max
- **Create:** `web/src/components/ImportPreviewTable.test.tsx` — 7 test cases covering 5 component-visible bug classes (#10, #11, #12, #14, #15 from the spec testing strategy), ~400 lines max
- **Modify:** `web/src/pages/Settings.tsx` — replace the existing inline `<Table>` in `ImportPreviewStep` with `<ImportPreviewTable>` wired to `useImportSession`

**Why 7 test cases (not 6 as the spec reads):** the spec's testing strategy §413 lists 6 frontend tests (#10–#15) as if they all lived in one file. Now that we have the hook/component split, the underlying bugs are covered at the right layer:
- **Hook-level tests (Chunk 4, 7 tests)** already own: localStorage resume 200/404 (spec test #13), PATCH queue serialization (spec test #15 "serialization" half), cellError 400 path (spec test #14 "400 error" half), confirmImport 409 recovery, skip-resolves invariant.
- **Component-level tests (Chunk 5, 7 test cases)** own: render + visual states (#10 — 1 render test), stale-style regression (#11 — 1 collision highlight + 1 rerender regression test), Import button state (#12 — 1 test covering 4 rerender cases), PATCH wiring through user interaction (#14 — `user-event` double-click/Enter commit test + Escape cancel test), bulk-skip button (#15 "bulk skip" half — 1 test).
- No test is duplicated. Each test in Chunk 5 owns a bug class that only manifests through the rendered component, not through the hook in isolation. Some tasks contain two adjacent test cases covering both the positive and the negative branch of the same bug class.

---

### Task 19: `ImportPreviewTable` skeleton + render test

**Files:**
- Create: `web/src/components/ImportPreviewTable.tsx`
- Create: `web/src/components/ImportPreviewTable.test.tsx`

- [ ] **Step 19.1: Write the failing render test**

Create `web/src/components/ImportPreviewTable.test.tsx`:

```tsx
import { render, screen, within } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { ImportPreviewTable } from './ImportPreviewTable';
import type { ImportPreview } from '@/api/types';

function makePreview(overrides: Partial<ImportPreview> = {}): ImportPreview {
  return {
    import_id: 'preview-abc',
    row_count: 3,
    columns: ['Date', 'Description', 'Amount', 'Category'],
    unique_categories: ['Food'],
    unmatched_categories: [],
    collision_groups: [],
    expires_at: '2099-01-01T00:00:00Z',
    rows: [
      {
        row_id: 0, skip: false, content_hash: 'h0',
        date: '2025-01-07', description: 'Starbucks', amount: 5, category: 'Food',
      },
      {
        row_id: 1, skip: false, content_hash: 'h1',
        date: '2025-01-08', description: "Trader Joe's", amount: 42.1, category: 'Food',
      },
      {
        row_id: 2, skip: false, content_hash: 'h2',
        date: '2025-01-09', description: 'Amazon', amount: 29.99, category: 'Food',
      },
    ],
    ...overrides,
  };
}

const noopProps = {
  cellErrors: {},
  unresolvedCount: 0,
  canImport: true,
  pendingPatchCount: 0,
  onPatchRow: vi.fn(),
  onConfirm: vi.fn(),
};

describe('ImportPreviewTable', () => {
  it('renders one row per preview.rows entry', () => {
    render(<ImportPreviewTable preview={makePreview()} {...noopProps} />);
    // Every row rendered — descriptions are a stable proxy.
    expect(screen.getByText('Starbucks')).toBeInTheDocument();
    expect(screen.getByText("Trader Joe's")).toBeInTheDocument();
    expect(screen.getByText('Amazon')).toBeInTheDocument();
  });
});
```

- [ ] **Step 19.2: Run the test, verify FAIL**

```bash
cd web && npm test -- src/components/ImportPreviewTable.test.tsx
```

Expected: FAIL with `Cannot find module './ImportPreviewTable'`.

- [ ] **Step 19.3: Create the minimal component**

Create `web/src/components/ImportPreviewTable.tsx`:

```tsx
import type { ImportPreview, CollisionGroup, PatchRowRequest } from '@/api/types';
import type { CellError } from '@/hooks/useImportSession';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';

export interface ImportPreviewTableProps {
  preview: ImportPreview;
  cellErrors: Record<string, CellError>;
  unresolvedCount: number;
  canImport: boolean;
  pendingPatchCount: number;
  onPatchRow: (
    rowID: number,
    field: PatchRowRequest['field'],
    value: string | boolean,
  ) => Promise<void>;
  onConfirm: () => void;
}

export function ImportPreviewTable({ preview }: ImportPreviewTableProps) {
  return (
    <div className="flex flex-col gap-3">
      <div className="max-h-[480px] overflow-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Date</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>Category</TableHead>
              <TableHead className="text-right">Amount</TableHead>
              <TableHead className="w-12">Skip</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {preview.rows.map((row) => (
              <TableRow key={row.row_id} data-row-id={row.row_id}>
                <TableCell />
                <TableCell>{row.date}</TableCell>
                <TableCell>{row.description}</TableCell>
                <TableCell className="text-muted-foreground">{row.category}</TableCell>
                <TableCell className="text-right font-mono tabular-nums">
                  {typeof row.amount === 'number' ? row.amount.toFixed(2) : row.amount}
                </TableCell>
                <TableCell />
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
```

**Type note:** The component imports `CellError` (the interface from Chunk 4's hook, already declared `export interface CellError` at `useImportSession.ts`) and `PatchRowRequest` (the request-body type from `@/api/types`, already declared in Chunk 1 at the same place the other import types live). No new type aliases are introduced in the hook file — everything the component needs is already exported by earlier chunks, so Task 19 does not touch `useImportSession.ts` at all.

- [ ] **Step 19.4: Run the test, verify PASS**

```bash
cd web && npm test -- src/components/ImportPreviewTable.test.tsx
```

Expected: PASS (1 test). Failure diagnostics:

- **"Module '@/hooks/useImportSession' has no exported member 'CellError'":** Chunk 4 Step 17.1 declared `export interface CellError { field: PatchRowRequest['field']; message: string; }` around line 3850 of this plan. Re-check that the hook file actually contains that `export` keyword. If not, this is a Chunk 4 implementation bug, not a Chunk 5 bug — fix it in the hook file and re-run.
- **"Module '@/api/types' has no exported member 'PatchRowRequest'":** Chunk 1 Step 14.1 added `PatchRowRequest` to the `@/api/types` module. Check that the interface is exported (with `export interface PatchRowRequest { ... }`). If missing, add the export in `web/src/api/types.ts`.
- **"Starbucks not in document":** the `preview.rows.map` is emitting nothing. Check the `<TableBody>` contents — most likely the map is outside the `<TableBody>` or the JSX fragment is dropping children because of a stray `{}` wrapper.

- [ ] **Step 19.5: Commit**

```bash
git add web/src/components/ImportPreviewTable.tsx web/src/components/ImportPreviewTable.test.tsx
git commit -m "feat(import): add ImportPreviewTable skeleton with render test"
```

---

### Task 20: Collision highlighting + stale-style regression test

Test #11 is the single most important frontend test (importcsv #16 anti-pattern). A row that transitions collision → clean must lose its amber background on the very next render. Deriving classes from `preview.collision_groups` on every render makes this structurally impossible to regress — this task locks that derivation in.

**Files:**
- Modify: `web/src/components/ImportPreviewTable.tsx`
- Modify: `web/src/components/ImportPreviewTable.test.tsx`

- [ ] **Step 20.1: Write the failing collision + stale-style test**

Append to `web/src/components/ImportPreviewTable.test.tsx` inside the `describe` block:

```tsx
it('applies amber collision class to rows in collision_groups and nothing to clean rows', () => {
  const preview = makePreview({
    collision_groups: [
      { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
    ],
  });
  render(<ImportPreviewTable preview={preview} {...noopProps} />);

  const row0 = screen.getByText('Starbucks').closest('tr')!;
  const row1 = screen.getByText("Trader Joe's").closest('tr')!;
  const row2 = screen.getByText('Amazon').closest('tr')!;

  // Collision rows carry the amber marker (asserted via data-collision so
  // we're not coupled to a specific Tailwind utility class).
  expect(row0.getAttribute('data-collision')).toBe('true');
  expect(row1.getAttribute('data-collision')).toBe('true');
  // Clean row is explicitly NOT collision — checking "!== 'true'" rather
  // than null because the attribute could be absent OR set to 'false'.
  expect(row2.getAttribute('data-collision')).not.toBe('true');
});

it('stale-style regression: a row that flips collision → clean loses data-collision on re-render', () => {
  // Start: row 0 is in a collision group.
  const before = makePreview({
    collision_groups: [
      { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
    ],
  });
  const { rerender } = render(<ImportPreviewTable preview={before} {...noopProps} />);

  expect(screen.getByText('Starbucks').closest('tr')!.getAttribute('data-collision')).toBe('true');

  // Simulate the PATCH response: row 0 is now clean. The hook's merge
  // hands us a fresh preview with collision_groups that no longer include
  // row 0. This is the EXACT case importcsv #16 blew — if the component
  // carries collision state in useState/useEffect-derived state instead of
  // deriving from props, row 0 keeps its amber class until the next state
  // update and the user sees a stale "unresolved" marker.
  const after = makePreview({
    collision_groups: [
      { group_id: 'g1', reason: 'intra_file', member_row_ids: [1] }, // row 0 removed
    ],
  });
  rerender(<ImportPreviewTable preview={after} {...noopProps} />);

  expect(screen.getByText('Starbucks').closest('tr')!.getAttribute('data-collision')).not.toBe('true');
  // And row 1 still carries it.
  expect(screen.getByText("Trader Joe's").closest('tr')!.getAttribute('data-collision')).toBe('true');
});
```

- [ ] **Step 20.2: Run the tests, verify the two new ones FAIL**

```bash
cd web && npm test -- src/components/ImportPreviewTable.test.tsx
```

Expected: render test still PASSES; two new tests FAIL with `null is not 'true'` (the `data-collision` attribute does not yet exist).

- [ ] **Step 20.3: Implement collision derivation**

In `ImportPreviewTable.tsx`, add a `useMemo` that builds a `Set<number>` of collision row ids from `preview.collision_groups`, then pass `data-collision` and a Tailwind class into the `<TableRow>`.

Replace the `TableBody` contents (inside the component body, above `return`):

```tsx
import { useMemo } from 'react';
// ... existing imports

export function ImportPreviewTable({ preview, ...}: ImportPreviewTableProps) {
  // Derive collision membership from props on EVERY render. No useState,
  // no useEffect — structural guarantee against importcsv #16.
  const collisionRowIds = useMemo(() => {
    const s = new Set<number>();
    for (const group of preview.collision_groups) {
      for (const rowID of group.member_row_ids) s.add(rowID);
    }
    return s;
  }, [preview.collision_groups]);

  // ... rest
}
```

And in the `preview.rows.map`, replace `<TableRow key={row.row_id} data-row-id={row.row_id}>` with:

```tsx
{preview.rows.map((row) => {
  const isCollision = collisionRowIds.has(row.row_id);
  return (
    <TableRow
      key={row.row_id}
      data-row-id={row.row_id}
      data-collision={isCollision ? 'true' : undefined}
      className={isCollision ? 'bg-amber-500/[0.09] border-l-2 border-l-amber-500' : ''}
    >
      {/* cells unchanged */}
    </TableRow>
  );
})}
```

- [ ] **Step 20.4: Run all three tests, verify PASS**

```bash
cd web && npm test -- src/components/ImportPreviewTable.test.tsx
```

Expected: 3/3 PASS. Failure diagnostics:

- **Collision test FAILS with `null is not 'true'` for row 0 or row 1:** the `useMemo` is not populating the Set. Check that `preview.collision_groups[].member_row_ids` is the field name (not `row_ids` or `members`). The types from Chunk 4 Step 14.1 define it as `member_row_ids`.
- **Stale-style test FAILS with `row 0 still has data-collision 'true'` after rerender:** the component is storing collision membership in local state instead of deriving via useMemo. Grep for `useState` in `ImportPreviewTable.tsx` — if you find one that holds collision ids, delete it. The ONLY state the component should own is the currently-editing cell (added in Task 22). Everything else derives from props.
- **Stale-style test FAILS with `row 1 has data-collision null`:** the `useMemo` deps array is wrong (missing `preview.collision_groups`) — the memo returns a stale Set on rerender. Fix the deps.

- [ ] **Step 20.5: Commit**

```bash
git add web/src/components/ImportPreviewTable.tsx web/src/components/ImportPreviewTable.test.tsx
git commit -m "feat(import): derive collision state from props on every render"
```

---

### Task 21: Footer + Import button state test

**Files:**
- Modify: `web/src/components/ImportPreviewTable.tsx`
- Modify: `web/src/components/ImportPreviewTable.test.tsx`

- [ ] **Step 21.1: Write the failing Import button state test**

Append to `ImportPreviewTable.test.tsx`:

```tsx
it('Import button reflects canImport and pendingPatchCount', async () => {
  const onConfirm = vi.fn();
  const preview = makePreview();

  // Case 1: canImport=false → disabled, label shows unresolved count.
  const { rerender } = render(
    <ImportPreviewTable
      preview={preview}
      cellErrors={{}}
      unresolvedCount={3}
      canImport={false}
      pendingPatchCount={0}
      onPatchRow={vi.fn()}
      onConfirm={onConfirm}
    />,
  );
  const btn = screen.getByRole('button', { name: /import/i });
  expect(btn).toBeDisabled();
  // Status message reflects unresolved count.
  expect(screen.getByText(/3.*collision/i)).toBeInTheDocument();

  // Case 2: canImport=true, pendingPatchCount=0 → enabled.
  rerender(
    <ImportPreviewTable
      preview={preview}
      cellErrors={{}}
      unresolvedCount={0}
      canImport={true}
      pendingPatchCount={0}
      onPatchRow={vi.fn()}
      onConfirm={onConfirm}
    />,
  );
  expect(btn).toBeEnabled();

  // Case 3: pendingPatchCount > 0 → disabled even though canImport is
  // nominally true. This is the PATCH/confirm race guard.
  rerender(
    <ImportPreviewTable
      preview={preview}
      cellErrors={{}}
      unresolvedCount={0}
      canImport={true}
      pendingPatchCount={2}
      onPatchRow={vi.fn()}
      onConfirm={onConfirm}
    />,
  );
  expect(btn).toBeDisabled();

  // Case 4: click back in case-2 state fires onConfirm once.
  rerender(
    <ImportPreviewTable
      preview={preview}
      cellErrors={{}}
      unresolvedCount={0}
      canImport={true}
      pendingPatchCount={0}
      onPatchRow={vi.fn()}
      onConfirm={onConfirm}
    />,
  );
  btn.click();
  expect(onConfirm).toHaveBeenCalledTimes(1);
});
```

- [ ] **Step 21.2: Run the test, verify FAIL**

Expected: FAIL with `Unable to find role 'button' with name /import/i` — no footer rendered yet.

- [ ] **Step 21.3: Implement the footer**

In `ImportPreviewTable.tsx`, add a footer beneath the `<div className="max-h-[480px]...">` block:

```tsx
import { Button } from '@/components/ui/button';

// ... inside the component JSX, after the bordered table div:

<div className="flex items-center justify-between gap-3 pt-2 border-t border-border">
  <div className="text-sm">
    {unresolvedCount > 0 ? (
      <span className="text-amber-500" aria-live="polite">
        {`Fix or skip ${unresolvedCount} ${unresolvedCount === 1 ? 'collision' : 'collisions'} to enable import`}
      </span>
    ) : (
      <span className="text-emerald-500" aria-live="polite">
        {`Ready to import ${preview.rows.filter((r) => !r.skip).length} rows`}
      </span>
    )}
  </div>
  <Button
    type="button"
    disabled={!canImport || pendingPatchCount > 0}
    onClick={onConfirm}
  >
    {`Import ${preview.rows.filter((r) => !r.skip).length}`}
  </Button>
</div>
```

Destructure the props at the top of the component: `const { preview, unresolvedCount, canImport, pendingPatchCount, onConfirm } = props;`

- [ ] **Step 21.4: Run the tests, verify PASS**

Expected: 4/4 PASS. Failure diagnostics:

- **"button is not disabled" in case 1:** the button's `disabled` is wired only to `!canImport` and missing the `|| pendingPatchCount > 0` half, OR the boolean is inverted.
- **Status message "3 collisions" not found:** the pluralization branch is wrong, OR the message text does not include the number. Check that the JSX actually interpolates `unresolvedCount`.
- **"button is not disabled" in case 3:** the `pendingPatchCount > 0` guard is missing. This is the confirm/PATCH race protection — without it, a user can click Import while a PATCH is in-flight and race the backend session state.
- **`onConfirm` called more than once or zero times:** double-check you wired `onClick={onConfirm}` not `onClick={() => onConfirm}` (the latter returns the function instead of calling it — silent bug).

- [ ] **Step 21.5: Commit**

```bash
git add web/src/components/ImportPreviewTable.tsx web/src/components/ImportPreviewTable.test.tsx
git commit -m "feat(import): footer with progress message and Import button lockout"
```

---

### Task 22: Cell editing + PATCH wiring + inline 400 error test

This is the test that owns the integration seam where most real UI bugs live: a user double-clicks a cell, types a new value, presses Enter/Tab, and the component fires `onPatchRow` with the right payload. It also verifies the 400-error UX (red ring + inline message, cleared on next successful response for the same cell).

**Files:**
- Modify: `web/src/components/ImportPreviewTable.tsx`
- Modify: `web/src/components/ImportPreviewTable.test.tsx`

- [ ] **Step 22.1: Write the failing user-event test**

Append to `ImportPreviewTable.test.tsx`:

```tsx
import userEvent from '@testing-library/user-event';

it('double-click cell + type + Enter fires onPatchRow with correct payload, and inline 400 renders when cellErrors is non-empty', async () => {
  const user = userEvent.setup();
  const onPatchRow = vi.fn().mockResolvedValue(undefined);

  const { rerender } = render(
    <ImportPreviewTable
      preview={makePreview()}
      cellErrors={{}}
      unresolvedCount={0}
      canImport={true}
      pendingPatchCount={0}
      onPatchRow={onPatchRow}
      onConfirm={vi.fn()}
    />,
  );

  // Double-click row 0's description cell (Starbucks).
  const cell = screen.getByText('Starbucks');
  await user.dblClick(cell);

  // Edit input must exist, focused, and have the current value.
  const input = await screen.findByDisplayValue('Starbucks');
  expect(input).toHaveFocus();

  // Clear + type new value + Enter.
  await user.clear(input);
  await user.type(input, 'Starbucks NYC{Enter}');

  expect(onPatchRow).toHaveBeenCalledTimes(1);
  expect(onPatchRow).toHaveBeenCalledWith(0, 'description', 'Starbucks NYC');

  // Rerender with the 400 error injected for (row_id=0, field='description').
  rerender(
    <ImportPreviewTable
      preview={makePreview()}
      cellErrors={{ '0:description': { field: 'description', message: 'INVALID_DESCRIPTION' } }}
      unresolvedCount={0}
      canImport={true}
      pendingPatchCount={0}
      onPatchRow={onPatchRow}
      onConfirm={vi.fn()}
    />,
  );

  // Inline error message rendered.
  expect(screen.getByText('INVALID_DESCRIPTION')).toBeInTheDocument();
  // The cell has an error marker (data attribute to avoid coupling to classes).
  const row0 = screen.getByText('Starbucks').closest('tr')!;
  const errorCell = within(row0).getAllByText(/INVALID_DESCRIPTION|Starbucks/i)[0].closest('td')!;
  expect(errorCell.getAttribute('data-cell-error')).toBe('true');
});

it('Escape during edit cancels without firing onPatchRow', async () => {
  const user = userEvent.setup();
  const onPatchRow = vi.fn();

  render(
    <ImportPreviewTable
      preview={makePreview()}
      cellErrors={{}}
      unresolvedCount={0}
      canImport={true}
      pendingPatchCount={0}
      onPatchRow={onPatchRow}
      onConfirm={vi.fn()}
    />,
  );

  await user.dblClick(screen.getByText('Starbucks'));
  const input = await screen.findByDisplayValue('Starbucks');
  await user.clear(input);
  await user.type(input, 'Nope{Escape}');

  expect(onPatchRow).not.toHaveBeenCalled();
  // The cell shows the original value again.
  expect(screen.getByText('Starbucks')).toBeInTheDocument();
});
```

- [ ] **Step 22.2: Run the tests, verify FAIL**

Expected: FAILs with "unable to find input with value 'Starbucks'" — no edit mode implemented yet.

- [ ] **Step 22.3: Implement cell editing**

In `ImportPreviewTable.tsx`, add local state tracking the editing cell, and render an `<Input>` inside the description/date/amount cell when it matches:

```tsx
import { useMemo, useState, useCallback, type KeyboardEvent } from 'react';
import { Input } from '@/components/ui/input';

type EditingCell = { rowID: number; field: 'date' | 'description' | 'amount' } | null;

export function ImportPreviewTable(props: ImportPreviewTableProps) {
  const { preview, cellErrors, unresolvedCount, canImport, pendingPatchCount, onPatchRow, onConfirm } = props;
  const [editing, setEditing] = useState<EditingCell>(null);
  const [draft, setDraft] = useState<string>('');

  const collisionRowIds = useMemo(/* ...same as before */);

  const beginEdit = useCallback((rowID: number, field: 'date' | 'description' | 'amount', current: string) => {
    setEditing({ rowID, field });
    setDraft(current);
  }, []);

  const commitEdit = useCallback(async () => {
    if (!editing) return;
    const target = editing;
    setEditing(null);
    await onPatchRow(target.rowID, target.field, draft);
  }, [editing, draft, onPatchRow]);

  const cancelEdit = useCallback(() => setEditing(null), []);

  const onKeyDown = useCallback((e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault(); // Enter would otherwise submit any ancestor form.
      void commitEdit();
    } else if (e.key === 'Tab') {
      // DO NOT preventDefault — we want the browser to advance focus to the
      // next tab-able element after we commit. `setEditing(null)` inside
      // commitEdit unmounts this Input, and because React's default
      // document.activeElement handling runs after the unmount, the browser
      // will settle focus on the next tab-able element (the Skip checkbox,
      // the next row's date cell, etc.) in normal DOM order. This is the
      // spec §193-194 "Tab commits AND advances" behavior. Shift+Tab is
      // handled by the same browser path.
      void commitEdit();
    } else if (e.key === 'Escape') {
      cancelEdit();
    }
  }, [commitEdit, cancelEdit]);

  // Helper: render a cell that is either text (default) or an Input (editing).
  const renderEditableCell = (
    row: { row_id: number; date: string; description: string; amount: number; category: string; skip: boolean },
    field: 'date' | 'description' | 'amount',
    displayValue: string,
    extraClass = '',
  ) => {
    const isEditing = editing?.rowID === row.row_id && editing.field === field;
    const errKey = `${row.row_id}:${field}`;
    const err = cellErrors[errKey];
    return (
      <TableCell
        className={extraClass}
        data-cell-error={err ? 'true' : undefined}
        onDoubleClick={() => beginEdit(row.row_id, field, String(displayValue))}
      >
        {isEditing ? (
          <Input
            autoFocus
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={commitEdit}
            onKeyDown={onKeyDown}
            className={`h-7 px-2 py-0 text-sm ${err ? 'ring-1 ring-destructive' : ''}`}
          />
        ) : (
          <span>{displayValue}</span>
        )}
        {err && <p className="text-xs text-destructive mt-0.5">{err.message}</p>}
      </TableCell>
    );
  };

  // In the map, replace the three editable cells with calls to renderEditableCell:
  // <TableCell>{row.date}</TableCell>        →  renderEditableCell(row, 'date', row.date)
  // <TableCell>{row.description}</TableCell> →  renderEditableCell(row, 'description', row.description)
  // <TableCell className="text-right ...">{row.amount}</TableCell>
  //                                          →  renderEditableCell(row, 'amount', row.amount.toFixed(2), 'text-right font-mono tabular-nums')
  // The Category cell stays plain <TableCell> — not editable via PATCH.
}
```

- [ ] **Step 22.4: Run the tests, verify PASS**

Expected: all tests PASS. Failure diagnostics:

- **"unable to find input with value 'Starbucks'" after dblClick:** the `onDoubleClick` handler is wired to the wrong element (the inner `<span>` instead of the `<TableCell>`) OR the `beginEdit` is called with the wrong `displayValue` string. Make sure `onDoubleClick` is on `<TableCell>` and the closure captures `row.description`.
- **"onPatchRow not called with 'description'" but called with another field:** the `editing.field` in `commitEdit` is stale — most likely you used a ref instead of the state variable, or the `setEditing(null)` cleared `editing` before the closure ran. The snapshot-and-clear pattern above (`const target = editing; setEditing(null); await onPatchRow(target.rowID, ...)`) avoids this.
- **"onPatchRow called twice" on Enter:** both Enter and the subsequent onBlur are firing commitEdit. The `setEditing(null)` at the top of commitEdit should short-circuit the onBlur path because on the second call `editing` is null and the early return triggers. If this still double-fires, add a `const committed = useRef(false)` guard.
- **"Escape test fails — Starbucks span shows 'Nope'":** the cancelEdit handler is still calling onPatchRow. Grep for `onPatchRow` inside `cancelEdit` — the cancel path must NOT fire a PATCH. Escape should ONLY call `setEditing(null)` and leave the server state untouched.
- **Inline error not rendered:** the `cellErrors` lookup is using the wrong key format. The key must be exactly `${row_id}:${field}` (e.g. `'0:description'`), matching what `useImportSession.patchRow` writes in its 400-catch branch.
- **`data-cell-error='true'` assertion fails:** the `TableCell` prop is set to `err ? 'true' : false` — React removes attributes whose value is `false`, but you want `undefined` (absent). Use `err ? 'true' : undefined`.

- [ ] **Step 22.5: Commit**

```bash
git add web/src/components/ImportPreviewTable.tsx web/src/components/ImportPreviewTable.test.tsx
git commit -m "feat(import): inline cell editing with 400 error display"
```

---

### Task 23: Collision group headers + bulk-skip button test

A collision group renders a non-data header row above its member rows with `⚠ N rows collide` and a "Skip all in group" button. Clicking the button fires N `onPatchRow` calls (one per member with `field='skip', value=true`).

**Files:**
- Modify: `web/src/components/ImportPreviewTable.tsx`
- Modify: `web/src/components/ImportPreviewTable.test.tsx`

- [ ] **Step 23.1: Write the failing bulk-skip test**

Append to `ImportPreviewTable.test.tsx`:

```tsx
it('renders a collision group header with "Skip all" that fires N onPatchRow calls', async () => {
  const user = userEvent.setup();
  const onPatchRow = vi.fn().mockResolvedValue(undefined);

  render(
    <ImportPreviewTable
      preview={makePreview({
        collision_groups: [
          { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1, 2] },
        ],
      })}
      cellErrors={{}}
      unresolvedCount={3}
      canImport={false}
      pendingPatchCount={0}
      onPatchRow={onPatchRow}
      onConfirm={vi.fn()}
    />,
  );

  // Header row present and labelled.
  expect(screen.getByText(/3 rows collide/i)).toBeInTheDocument();

  const skipAll = screen.getByRole('button', { name: /skip all/i });
  await user.click(skipAll);

  // One PATCH per member row, in member-row-id order, each with skip=true.
  expect(onPatchRow).toHaveBeenCalledTimes(3);
  expect(onPatchRow).toHaveBeenNthCalledWith(1, 0, 'skip', true);
  expect(onPatchRow).toHaveBeenNthCalledWith(2, 1, 'skip', true);
  expect(onPatchRow).toHaveBeenNthCalledWith(3, 2, 'skip', true);
});
```

- [ ] **Step 23.2: Run the test, verify FAIL**

Expected: FAIL with `Unable to find text /3 rows collide/i` — no header row yet.

- [ ] **Step 23.3: Implement collision group header rendering**

The member rows of a group must render **after** their header row in the DOM. The easiest way to enforce this without mutating `preview.rows` order is to group rows by collision group during render, emitting each group as `[header row, ...member rows]`, then emitting the remaining clean rows in their original order.

Add a helper near the top of the file:

```tsx
type RenderUnit =
  | { kind: 'group-header'; group: CollisionGroup }
  | { kind: 'row'; row: ImportPreview['rows'][number]; isCollision: boolean };

function buildRenderPlan(preview: ImportPreview): RenderUnit[] {
  const byRowId = new Map<number, ImportPreview['rows'][number]>();
  for (const r of preview.rows) byRowId.set(r.row_id, r);

  const emitted = new Set<number>();
  const units: RenderUnit[] = [];

  // Groups first, in spec order. Each group renders its header immediately
  // followed by its member rows, in member_row_ids order.
  for (const group of preview.collision_groups) {
    units.push({ kind: 'group-header', group });
    for (const rowID of group.member_row_ids) {
      const row = byRowId.get(rowID);
      if (row && !emitted.has(rowID)) {
        units.push({ kind: 'row', row, isCollision: true });
        emitted.add(rowID);
      }
    }
  }

  // Then clean rows in original order.
  for (const row of preview.rows) {
    if (!emitted.has(row.row_id)) {
      units.push({ kind: 'row', row, isCollision: false });
    }
  }

  return units;
}
```

In the component body, replace `collisionRowIds` and the `preview.rows.map` with:

```tsx
const renderPlan = useMemo(() => buildRenderPlan(preview), [preview]);

const skipAllInGroup = useCallback(async (group: CollisionGroup) => {
  for (const rowID of group.member_row_ids) {
    // Sequential await so the hook's queue sees them in order. The queue
    // serializes them under the hood — this await only drives iteration.
    // eslint-disable-next-line no-await-in-loop
    await onPatchRow(rowID, 'skip', true);
  }
}, [onPatchRow]);

// In the TableBody:
{renderPlan.map((unit) => {
  if (unit.kind === 'group-header') {
    return (
      <TableRow
        key={`group-${unit.group.group_id}`}
        className="bg-amber-500/[0.12] border-l-2 border-l-amber-500"
        data-group-header="true"
      >
        <TableCell colSpan={6} className="py-2">
          <div className="flex items-center justify-between gap-3">
            <span className="text-sm font-medium text-amber-500">
              {`\u26a0 ${unit.group.member_row_ids.length} rows collide`}
              {unit.group.reason === 'db_match' && ' (matches existing transaction)'}
            </span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => skipAllInGroup(unit.group)}
            >
              Skip all in group
            </Button>
          </div>
        </TableCell>
      </TableRow>
    );
  }
  const { row, isCollision } = unit;
  return (
    <TableRow
      key={row.row_id}
      data-row-id={row.row_id}
      data-collision={isCollision ? 'true' : undefined}
      className={`${isCollision ? 'bg-amber-500/[0.09] border-l-2 border-l-amber-500' : ''} ${row.skip ? 'text-muted-foreground line-through' : ''}`}
    >
      {/* same cells as before, using renderEditableCell */}
    </TableRow>
  );
})}
```

- [ ] **Step 23.4: Run the tests, verify PASS**

Expected: all tests PASS.

**Important:** the earlier collision + stale-style tests from Task 20 still need to work with the new `buildRenderPlan` machinery. The `data-collision` marker now lives on rows emitted from the `kind: 'row'` branch, where `isCollision` is computed while walking the groups. Re-run the full file to confirm Tasks 20, 21, 22 tests still pass:

```bash
cd web && npm test -- src/components/ImportPreviewTable.test.tsx
```

Failure diagnostics:

- **"3 rows collide" not found:** the header row is not rendering, OR the text is broken across multiple elements and testing-library's `getByText` cannot find it. Wrap the count and the literal "rows collide" in a single `<span>` so the text matches as one node.
- **`onPatchRow` called with wrong member ids (e.g. 1, 2, 0 instead of 0, 1, 2):** the `skipAllInGroup` is iterating `group.member_row_ids` directly — check the group fixture to make sure it's `[0, 1, 2]` and not `[1, 2, 0]` from an earlier shuffle.
- **Stale-style regression (Task 20 test #2) now FAILS:** `buildRenderPlan`'s dependency array is `[preview]` which is correct — React sees a new preview object on every hook state update. If the test fails with "row 0 still has data-collision 'true' after rerender", check that the `after` preview in the test actually provides a new top-level object (it does — `makePreview` constructs one) and that `useMemo` is re-running. If useMemo is not re-running, the deps array is likely using `preview.rows` instead of `preview`.
- **Clean rows rendering before group header:** the loop order in `buildRenderPlan` is wrong. Groups must be emitted FIRST; clean rows come AFTER.

- [ ] **Step 23.5: Commit**

```bash
git add web/src/components/ImportPreviewTable.tsx web/src/components/ImportPreviewTable.test.tsx
git commit -m "feat(import): collision group headers with bulk-skip action"
```

---

### Task 24: Wire `ImportPreviewTable` + `useImportSession` into `Settings.tsx`

The existing `ImportPreviewStep` in `Settings.tsx:1016-1300` uses local state for the preview and renders an inline `<Table>` read-only. Replace its data layer with `useImportSession` and its table with `<ImportPreviewTable>`. Keep the category-mapping UI above the table — that stays as a separate pre-step.

**Files:**
- Modify: `web/src/pages/Settings.tsx`

- [ ] **Step 24.1: Identify the existing integration points**

Read `web/src/pages/Settings.tsx` around lines 1016–1300 (`ImportPreviewStep`) and 1249 (the `handleUpload` that currently sets local preview state). Also note the parent `Settings` component's handler wiring around lines 1399–1460.

The goal: the parent `Settings` component owns the `useImportSession` hook; it passes `hook.preview`, `hook.cellErrors`, `hook.unresolvedCount`, `hook.canImport`, `hook.pendingPatchCount`, `hook.patchRow`, and a `handleConfirm` callback down to `ImportPreviewStep`, which forwards them to `<ImportPreviewTable>`.

The category-mapping UI inside `ImportPreviewStep` stays — its inputs (`uniqueImportCategories`, `matched`, `unmatched`, `rowsWithoutCategory`, `needsDefaultCategory`) are all derivable from `hook.preview` and existing local state (`categoryMap`, `defaultCategoryId`), so the only structural change is replacing the inline `<Table>` with `<ImportPreviewTable>`.

- [ ] **Step 24.2: Replace the three local wizard-state hooks with `useImportSession`**

The existing `Settings` component (`web/src/pages/Settings.tsx:1217-1219`) currently holds the import wizard in three local useStates:

```tsx
const [importStep, setImportStep] = useState<ImportStep>('upload');
const [preview, setPreview] = useState<ImportPreview | null>(null);
const [result, setResult] = useState<ImportResult | null>(null);
```

All three are now owned by `useImportSession` (Chunk 4 Step 17.1 return type: `preview`, `importStep`, `result`). **Delete all three local `useState` lines and replace them with a single hook call:**

```tsx
// web/src/pages/Settings.tsx, around line 1217 (inside the Settings component)

import { useImportSession } from '@/hooks/useImportSession';
import { ImportPreviewTable } from '@/components/ImportPreviewTable';
// ... existing imports

// DELETE these three lines:
// const [importStep, setImportStep] = useState<ImportStep>('upload');
// const [preview, setPreview] = useState<ImportPreview | null>(null);
// const [result, setResult] = useState<ImportResult | null>(null);

// REPLACE with:
const importSession = useImportSession();

// Alias the hook outputs so the rest of the file reads almost identically
// to the old local-state version — no cascade of renames required:
const { preview, importStep, result } = importSession;
```

The `const { preview, importStep, result } = importSession` destructuring is the key trick: **every existing reader** of `preview`, `importStep`, and `result` in the rest of the file continues to work unchanged. Only the **writers** need to be updated (next two bullets).

**Update `handleFileChange` (currently `Settings.tsx:1242-1255`):** it currently does `api.upload(...)` → `setPreview(data)` → `setCategoryMap(...)` → `setImportStep('preview')`. The hook's `uploadFile` already does the upload and the `setPreview`/`setImportStep` transition, so delete that manual plumbing and let the hook drive it:

```tsx
async function handleFileChange(e: ChangeEvent<HTMLInputElement>) {
  const file = e.target.files?.[0];
  if (!file) return;
  setImportError(null);
  try {
    await importSession.uploadFile(file);
    // NOTE: Do NOT call autoMapCategories here. `importSession.preview`
    // in this closure is still the PRE-upload snapshot because we
    // destructured it at the top of the render — the state update
    // from uploadFile has been queued but not yet flushed. The
    // useEffect below watches for the new preview and runs the auto
    // map exactly once per successful upload (see Step 24.2b).
  } catch (err) {
    setImportError(err instanceof Error ? err.message : 'Upload failed');
    clearFileInput();
  }
}
```

**Update `handleConfirmImport` (currently `Settings.tsx:1257-1287`):** replace the entire body with a single fire-and-forget call. The hook's `confirmImport` returns `Promise<void>` — it already handles the success path (`setResult` + `setImportStep('done')`) and the 409 path (updates `collision_groups` in place) internally. The parent only needs to convert the string-ID category map to numeric and call the hook:

```tsx
async function handleConfirmImport() {
  if (!preview) return;
  // Convert string IDs to numbers for the backend (Go expects int64).
  const numericCategoryMap: Record<string, number> = {};
  for (const [name, id] of Object.entries(categoryMap)) {
    if (id) numericCategoryMap[name] = parseInt(id, 10);
  }
  setImportError(null);
  try {
    await importSession.confirmImport(numericCategoryMap, defaultCategoryId);
    // On success the hook has already set importStep='done' and result=res.
    // On 409 the hook has updated preview.collision_groups and importStep
    // is still 'preview' — the preview table re-renders with the new
    // collisions highlighted, Import button disables, user resolves and
    // clicks Import again. Nothing else to do here.
  } catch (err) {
    // confirmImport swallows UnresolvedCollisionsError internally (not a
    // throw). Any error that escapes here is a genuine non-409 failure
    // (500, network error, etc.) — surface it via the existing toast.
    toast.error(err instanceof Error ? err.message : 'Import failed');
  }
}
```

NOTE: the old `handleConfirmImport` called `setConfirmOpen(false)` after both success and failure. That call is **intentionally gone** in the new version because the entire `<Dialog>` confirmation block (and the `confirmOpen`/`setConfirmOpen` state that drives it) is being deleted in Step 24.2c below — see the "delete list" there for the full set of removals. If you see an unused-locals warning about `setConfirmOpen` in the tsc run, you missed a deletion.

**Update `handleCancelImport` and `handleImportAnother` (currently `Settings.tsx:1289-1310`):** both previously did `api.del(...)` + reset local state. Replace with the hook's `cancelImport` (which DELETEs the backend session and clears localStorage) and `startOver` (which just resets the client state without a backend call). Use `cancelImport` for "Cancel" during the preview step so the backend's import_sessions row is freed immediately, and `startOver` for "Import another file" after a successful done step (nothing to DELETE backend-side at that point — the session was already consumed by confirm).

```tsx
async function handleCancelImport() {
  await importSession.cancelImport();
  // cancelImport DELETEs the backend session, clears localStorage,
  // and resets the hook's local state (preview = null, importStep = 'upload').
  // Reset the parent-owned fields too:
  setImportError(null);
  setCategoryMap({});
  setDefaultCategoryId(null);
  clearFileInput();
}

function handleImportAnother() {
  importSession.startOver();
  // startOver resets the hook's local state synchronously (no backend call).
  // The backend session row was already consumed by the successful confirm.
  setImportError(null);
  setCategoryMap({});
  setDefaultCategoryId(null);
  clearFileInput();
}
```

- [ ] **Step 24.2b: Add the `autoMapCategories` effect with a per-upload-id guard**

The previous `handleFileChange` synchronously called `autoMapCategories(data, categories)` right after `setPreview(data)`. With the hook-driven refactor, `importSession.preview` inside `handleFileChange` is stale across the `await` — so the auto-map has to run from a `useEffect` that fires whenever the hook's `preview` changes.

**Naive guard trap:** a gate of `if (Object.keys(categoryMap).length === 0)` silently drops re-upload re-mapping. Example: user uploads file A, manually re-maps one category, cancels, uploads file B — `categoryMap` from file A still has entries, so file B's preview gets no auto-map and the user sees empty selects.

**Correct guard:** track the import-id of the preview we last auto-mapped for. When the hook's preview changes to a new import-id, run the auto-map once; when it changes back to `null` (cancel/startOver), clear the tracker so the next preview re-runs it.

```tsx
// Near the top of the Settings component body, alongside the other refs:
const lastAutoMappedImportIdRef = useRef<string | null>(null);

useEffect(() => {
  if (!importSession.preview) {
    // Upload was cancelled / session reset — arm the ref for the next upload.
    lastAutoMappedImportIdRef.current = null;
    return;
  }
  const currentId = importSession.preview.import_id;
  if (lastAutoMappedImportIdRef.current === currentId) return;
  // First time we see this import_id — run the auto-map.
  setCategoryMap(autoMapCategories(importSession.preview, categories));
  lastAutoMappedImportIdRef.current = currentId;
}, [importSession.preview, categories]);
```

This guard survives the happy path, cancel-then-re-upload, and the localStorage mount-resume path (on remount the ref starts as `null`, sees the resumed preview once, auto-maps once, then silently skips on subsequent renders).

- [ ] **Step 24.2c: Delete the pre-collision-UI "Confirm Import" dialog and its state**

The existing `Settings.tsx` wraps the import flow in a confirmation `<Dialog>` at roughly `Settings.tsx:1410-1442`. The flow was: user clicks Import → `setConfirmOpen(true)` → dialog opens → dialog's "Confirm and Import" button calls `handleConfirmImport`. That dialog predates the collision-resolution UI and is now redundant — the design spec (§§170-305) deliberately does not include a confirmation step because the new inline collision-resolution flow already gives the user an explicit "resolve then Import" commitment gesture (the Import button is disabled until `canImport` is true, and it shows `Import N` with the exact row count baked into the label). Adding a modal on top of that would be double-confirmation friction.

**Delete this dialog and its wiring as part of this step.** The exact set of deletions — every one is required or you will ship either dead UI, an unused-locals warning, or a `tsc` error:

1. **The `confirmOpen` useState at `Settings.tsx:1226`:**

   ```tsx
   // DELETE this line:
   const [confirmOpen, setConfirmOpen] = useState(false);
   ```

2. **The entire `<Dialog open={confirmOpen} ...>` block at `Settings.tsx:1410-1442`** — from the opening `<Dialog open={confirmOpen}` tag through its closing `</Dialog>` tag inclusive. This is the only caller of `confirmOpen` / `setConfirmOpen` in the file, so once the block is gone the state variable has zero references and must also be gone (item 1). Running `tsc --noEmit` with one but not the other will fail.

3. **The `setConfirmOpen(false)` call inside the old `handleConfirmImport`** — already handled by the Step 24.2 rewrite above (the new version has no `setConfirmOpen` reference). Mentioned here for completeness.

4. **The `onConfirm={() => setConfirmOpen(true)}` prop** at the `<ImportPreviewStep>` call site (`Settings.tsx:1406`) — change it to `onConfirm={handleConfirmImport}` so the Import button fires the handler directly. This change is covered in detail by Step 24.3 below; it is listed here only so the full delete list sits in one place.

5. **Do NOT delete the `Dialog`, `DialogContent`, `DialogDescription`, `DialogFooter`, `DialogHeader`, `DialogTitle`, or `DialogTrigger` imports at `Settings.tsx:46-52`.** These components are also used by the Savings Goal dialog (~`Settings.tsx:657-739`) and the Add User dialog (~`Settings.tsx:861-955`), which are unrelated to this feature. Removing them would break two other flows. Only the local `confirmOpen` state and the specific `<Dialog>` block inside the import card come out.

After these deletions, `grep confirmOpen web/src/pages/Settings.tsx` should return zero matches. If it returns anything, you missed a reference — fix it before running `tsc`.

- [ ] **Step 24.3: Replace the `ImportPreviewStep` inline `<Table>` with `<ImportPreviewTable>`**

Inside `ImportPreviewStep`, find the `<Table>` block (currently around `Settings.tsx:1071-1250` — the exact range depends on edit history; grep for `<TableHeader>` inside `ImportPreviewStep`). Replace the entire bordered div + Table block with:

```tsx
<ImportPreviewTable
  preview={preview}
  cellErrors={cellErrors}
  unresolvedCount={unresolvedCount}
  canImport={canImport}
  pendingPatchCount={pendingPatchCount}
  onPatchRow={patchRow}
  onConfirm={onConfirm}
/>
```

Extend `ImportPreviewStepProps` (currently `Settings.tsx:1016-1025`) to include the hook outputs. Import `CellError` from the hook and `PatchRowRequest` from `@/api/types` at the top of `Settings.tsx` — both are already exported (Chunk 4 Step 17.1 and Chunk 1 Step 14.1 respectively):

```tsx
import type { CellError } from '@/hooks/useImportSession';
import type { PatchRowRequest } from '@/api/types';

interface ImportPreviewStepProps {
  preview: ImportPreview;
  cellErrors: Record<string, CellError>;
  unresolvedCount: number;
  canImport: boolean;
  pendingPatchCount: number;
  patchRow: (
    rowID: number,
    field: PatchRowRequest['field'],
    value: string | boolean,
  ) => Promise<void>;
  categories: Category[];
  categoryMap: Record<string, string>;
  setCategoryMap: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  defaultCategoryId: number | null;
  setDefaultCategoryId: (id: number | null) => void;
  onConfirm: () => void;
  onCancel: () => void;
}
```

And at the `<ImportPreviewStep ... />` call site (currently `Settings.tsx:1397-1408`), pass through the hook outputs. The existing render site already uses `{importStep === 'preview' && preview && (...)}` — a truthiness guard on both the step **and** the preview value. Keep that shape; just swap `preview` for `importSession.preview` in both the guard and the prop so TypeScript can narrow the type on its own without a non-null assertion:

```tsx
{importStep === 'preview' && importSession.preview && (
  // Both conditions must be true for TypeScript to narrow
  // `importSession.preview` from `ImportPreview | null` to `ImportPreview`.
  // Do NOT use a non-null assertion (`importSession.preview!`) here — the
  // truthiness check in the guard makes the assertion unnecessary, and
  // unnecessary `!` assertions are banned by the project's no-any / strict
  // type-safety rule (see .claude/CLAUDE.md "strict type safety").
  <ImportPreviewStep
    preview={importSession.preview}
    cellErrors={importSession.cellErrors}
    unresolvedCount={importSession.unresolvedCount}
    canImport={importSession.canImport}
    pendingPatchCount={importSession.pendingPatchCount}
    patchRow={importSession.patchRow}
    categories={categories}
    categoryMap={categoryMap}
    setCategoryMap={setCategoryMap}
    defaultCategoryId={defaultCategoryId}
    setDefaultCategoryId={setDefaultCategoryId}
    onConfirm={handleConfirmImport}
    onCancel={handleCancelImport}
  />
)}
```

Two things to watch for while editing the call site:

1. **Drop the fragment wrapper if the Dialog deletion leaves it empty.** The current code wraps `<ImportPreviewStep>` and the `<Dialog>` in a `<>...</>` fragment (see `Settings.tsx:1398 / 1443`). Once Step 24.2c deletes the Dialog, the fragment has a single child — React allows it but prefers the child to stand alone. Remove the `<>` / `</>` so the branch returns `<ImportPreviewStep ... />` directly.

2. **Rewire `onConfirm` from the old "open dialog" callback to the real handler.** The current prop is `onConfirm={() => setConfirmOpen(true)}` — that pops the dialog we are deleting. Change it to `onConfirm={handleConfirmImport}` so the `<ImportPreviewTable>` Import button fires the hook-driven confirm handler directly, which is the whole point of this refactor. The `handleConfirmImport` and `handleCancelImport` referenced in the prop values are the handlers rewritten in Step 24.2 — no new handlers are introduced here.

- [ ] **Step 24.4: Run type check**

```bash
cd web && npx tsc --noEmit
```

Expected: 0 errors. Failure diagnostics:

- **"Property 'cellErrors' does not exist on type 'UseImportSessionResult'":** the `useImportSession` hook does not return `cellErrors`. Re-check Chunk 4 Step 17.1 — the hook's return interface must include `preview`, `importStep`, `result`, `error`, `pendingPatchCount`, `cellErrors`, `unresolvedCount`, `canImport`, `uploadFile`, `patchRow`, `confirmImport`, `cancelImport`, `startOver`. If any are missing, fix the hook in the Chunk 4 commit, not here.
- **"Type 'ImportPreview | null' is not assignable to type 'ImportPreview'":** the guard in the call site is incomplete. Confirm the outer conditional is exactly `{importStep === 'preview' && importSession.preview && (...)}` (Step 24.3). TypeScript narrows `importSession.preview` to `ImportPreview` only when BOTH the step check and the truthiness check are present. Do NOT "fix" this by adding a non-null assertion — fix the guard.
- **"Cannot find name 'setConfirmOpen'" or "Cannot find name 'confirmOpen'":** you deleted the useState but left a reader behind, or deleted the readers but left the useState. Step 24.2c specifies three required deletions (the useState line, the `<Dialog>` JSX block, and the old `onConfirm={() => setConfirmOpen(true)}` prop — the `setConfirmOpen(false)` call inside `handleConfirmImport` is covered separately by the Step 24.2 rewrite); all three must be removed together or `tsc` will fail here. Run `grep -n confirmOpen web/src/pages/Settings.tsx` — the expected count after the refactor is **zero**.
- **"'setConfirmOpen' is declared but its value is never read":** the useState is still declared at `Settings.tsx:1226` even though all readers are gone. Delete line 1226 (Step 24.2c item 1).
- **"JSX element 'Dialog' has no corresponding closing tag" or "Adjacent JSX elements must be wrapped":** you deleted the `<Dialog>` block but left the enclosing fragment's `<>` / `</>` without noticing the fragment no longer has a second child. Follow Step 24.3 item 1: drop the fragment so the branch returns `<ImportPreviewStep ... />` directly.
- **"Property 'setPreview' does not exist":** left-over writer from the old local state — grep for `setPreview(`, `setResult(`, `setImportStep(` in `Settings.tsx` and delete them. All three writers live inside the hook now; the parent only reads the aliased destructured values.
- **"Cannot find name 'CellError'" or "Cannot find name 'PatchRowRequest'":** the imports at the top of `Settings.tsx` are missing. Add `import type { CellError } from '@/hooks/useImportSession';` and `import type { PatchRowRequest } from '@/api/types';`.
- **"cannot find module '@/hooks/useImportSession'":** path alias wrong. Check `tsconfig.json` paths and Vite alias config.

- [ ] **Step 24.5: Run the test suite**

```bash
cd web && npm test
```

Expected: all tests still pass. The Chunk 4 hook tests and the Chunk 5 component tests are unaffected by the Settings.tsx wiring.

- [ ] **Step 24.6: Commit**

```bash
git add web/src/pages/Settings.tsx
git commit -m "feat(import): wire ImportPreviewTable + useImportSession into Settings"
```

---

### Task 25: Manual smoke test — dev server

**Files:** none (manual acceptance check, not a code change)

This task is a human-driven walkthrough. It catches visual regressions, interaction bugs, and wiring mistakes that unit tests cannot see (e.g. layout drift, focus loss on rerender, aria-live announcements, browser console errors). Do NOT commit anything in this task.

- [ ] **Step 25.1: Prepare a test spreadsheet with known collisions**

Create (or reuse) an `.xlsx` file with the following rows — this triggers both within-file and (optionally) in-database collisions:

| Date       | Description | Amount | Category |
|------------|-------------|--------|----------|
| 2025-01-07 | Starbucks   | 5.00   | Food     |
| 2025-01-07 | Starbucks   | 5.00   | Food     |
| 2025-01-07 | Starbucks   | 5.00   | Food     |
| 2025-01-08 | Uber        | 12.50  | Transport|
| 2025-01-08 | Uber        | 12.50  | Transport|
| 2025-01-09 | Trader Joe's| 42.10  | Food     |
| 2025-01-10 | Amazon      | 29.99  | Shopping |

Expected collision groups after upload: one group of 3 (Starbucks), one group of 2 (Uber), two clean rows (Trader Joe's, Amazon).

- [ ] **Step 25.2: Start the backend**

```bash
go run ./cmd/spendrop
```

Expected: server listens on the configured port (default `:8080`), no errors in the log, migrations applied cleanly.

- [ ] **Step 25.3: Start the frontend dev server**

In a separate terminal:

```bash
cd web && npm run dev
```

Expected: Vite prints a `Local: http://localhost:5173/` URL, no TypeScript errors in the terminal, no red overlay in the browser.

- [ ] **Step 25.4: Log in and navigate to Settings → Import**

Open the dev URL, log in with any seeded household account, then go to the Settings page and click the Import tab.

Expected: the Import tab renders the file picker. No console errors in the browser devtools.

- [ ] **Step 25.5: Upload the test spreadsheet**

Click the file picker and select the `.xlsx` from Step 25.1.

Expected UI behavior:

- The preview table appears with 7 rows.
- The 3 Starbucks rows sit together under a group header reading "3 rows collide — same date + description + amount + category" (or equivalent wording from Task 23).
- The 2 Uber rows sit together under a group header reading "2 rows collide — ...".
- The 2 clean rows (Trader Joe's, Amazon) render below the collision groups without group headers.
- Each collision row has an amber left border / tinted background (the `data-collision="true"` visual).
- The footer shows `Fix or skip 5 collisions to enable import` in amber.
- The `Import 7` button is **disabled** (greyed out).
- An `aria-live="polite"` region is present — turn on a screen reader briefly and confirm the count is announced, or verify the region exists via devtools Elements tab.

- [ ] **Step 25.6: Resolve one collision by editing the description**

Double-click the "Description" cell of the first Starbucks row. An `<Input>` should appear in place of the text, pre-filled with "Starbucks", with a focus ring.

Type ` NYC` (so the value becomes `Starbucks NYC`) and press Enter.

Expected:

- The Input collapses back to a plain cell showing "Starbucks NYC".
- The amber background on that row disappears (the row is no longer in any collision group).
- The footer count drops to `Fix or skip 4 collisions to enable import`.
- The Starbucks group header now says `2 rows collide` (since the original group dropped from 3 members to 2).
- The `Import 7` button is still disabled (4 collisions remain).
- No console errors.

- [ ] **Step 25.7: Resolve another collision with Escape to cancel**

Double-click the Description cell of a Uber row. The Input should appear pre-filled with "Uber".

Type some junk (e.g. `asdf`) but press **Escape** instead of Enter.

Expected:

- The Input collapses back to "Uber" (original value).
- The amber background is **still present** (collision not resolved).
- The footer count is unchanged at `4 collisions`.
- No network request was made (check devtools Network tab — no PATCH to `/api/import/.../rows/...`).

- [ ] **Step 25.8: Bulk-skip a whole group**

Click the "Skip all" button in the Uber group header.

Expected:

- Both Uber rows get a visual skip treatment (strikethrough, dimmed, or checked skip box — whatever Task 23's implementation uses).
- The footer count drops to `Fix or skip 2 collisions` (the 2 remaining Starbucks rows).
- The Uber group header still exists but shows that all members are skipped (optional polish — as long as the rows no longer block import).

- [ ] **Step 25.9: Skip the remaining Starbucks collisions from the row-level skip box**

Click the skip checkbox on each of the 2 remaining Starbucks rows individually.

Expected:

- Footer flips from amber to `Ready to import N rows` in emerald as soon as the last collision is skipped (where N is the count of non-skipped rows: 7 - 2 Uber - 2 Starbucks = 3).
- The `Import N` button flips from disabled to **enabled**.
- No console errors, no stuck spinner.

- [ ] **Step 25.10: Verify localStorage resume on page reload**

Before confirming the import, press **F5** to reload the entire page.

Expected:

- The Import tab reloads with the exact same preview table state — same rows, same skip flags, same collision groups, same footer message.
- The URL / route does not change; Settings → Import is restored.
- If you open devtools → Application → Local Storage, you can see a `spendrop-import-id` entry with the current import ID.

If the preview does NOT restore after reload, the `useImportSession` mount-resume path is broken — re-check Chunk 4 Step 17.3 (test #2) and the effect that reads `localStorage` on mount.

- [ ] **Step 25.11: Verify inline 400 error on invalid edit**

Double-click the Date cell of the Trader Joe's row (a currently-clean row). Type `not-a-date` and press Enter.

Expected:

- The cell reverts to its previous value `2025-01-09`.
- A small inline error indicator appears next to the cell (red ring, tooltip, or `data-cell-error="true"` attribute — whatever Task 22 Step 22.4 implemented).
- The footer does not change.
- No unhandled promise rejection in the console.
- A second attempt with a valid date (`2025-01-11`) updates the cell and clears the error.

- [ ] **Step 25.12: Confirm the import**

Click `Import 3`.

Expected:

- Button briefly shows a loading state (disabled during the in-flight confirm request).
- On success, the UI transitions to the success step showing `3 transactions imported` (or the existing success panel).
- `localStorage.spendrop-import-id` is **cleared**.
- Navigating to Transactions or the Dashboard shows the 3 imported rows (Trader Joe's NYC had no edit, Amazon, and the edited "Starbucks NYC" row).

- [ ] **Step 25.13: Stop the servers**

Kill both the `go run` backend and the `npm run dev` frontend. Do not commit anything — this task is a manual acceptance check only.

If any step above fails, STOP and return to the corresponding Chunk 4 or Chunk 5 task to investigate. Common causes:

- **Preview does not restore after F5:** hook resume effect missing or crashing on 404. Re-check Chunk 4 Step 17.3.
- **Row stays amber after successful edit (stale-style regression):** `ImportPreviewTable` is caching `collisionRowIds` in `useState` instead of deriving from props. Re-check Task 20.
- **Import button stays disabled after all collisions resolved:** `unresolvedCount` or `canImport` logic is wrong. Re-check Chunk 4 Step 17.7 (test #7).
- **Clicking Import fires twice in a row (double-submit):** missing `pendingPatchCount > 0` guard on the button. Re-check Task 21.
- **Inline 400 error never appears:** `cellErrors` not wired from hook to component, or `data-cell-error` attribute logic is inverted. Re-check Task 22.

---

## Chunk 6: Final Verification, Review Pipeline, and Handoff

This chunk wraps the feature: full-suite regression runs across both backends, the two spec-mandated manual acceptance scripts (tests #16 and #17 from the spec's Testing Strategy), the full subagent review pipeline (code review, data-correctness, security, UI/UX, design enforcer), documentation updates, and the final commit + branch handoff (no push — the user creates PRs themselves).

Nothing in this chunk writes application code. It does write: acceptance-script evidence notes (kept in your local scratch, not committed), review-feedback fix commits (only if a reviewer flags a blocker), and the README / DESIGN_GUIDE update commit.

---

### Task 26: Full-suite regression runs

After Chunks 1–5 are all merged locally on the feature branch, run the entire test suite on both backends from a clean state. This catches cross-task breakage that per-task test runs miss (e.g. a Task 7 backend change that only breaks a Task 14 frontend test).

**Files:** none (test execution only; no commits in this task)

- [ ] **Step 26.1: Verify you are on the feature branch and working tree is clean**

```bash
git status
git rev-parse --abbrev-ref HEAD
```

Expected: branch name matches the feature branch name you used throughout (e.g. `feat/import-collision-resolution`). Working tree must be clean — if there are uncommitted changes, commit them to the matching task before continuing. Every change in this feature lives on a task-level commit already (Tasks 1-24 each ended with a commit step); there should be nothing loose.

If `git status` shows files you did not touch (e.g. `web/node_modules` changes), that is typically a `.gitignore` miss — do NOT `git add` them. Clean them up separately if needed.

- [ ] **Step 26.2: Run the full Go test suite in Docker**

The repo runs Go tests inside a `golang:1.26-alpine` container because the host has no C compiler (sqlite3 cgo). Run the entire backend suite — not just `./internal/api`:

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine \
  sh -c "apk add --no-cache build-base >/dev/null && go test ./..."
```

Expected: **all packages PASS, no SKIPs unrelated to this feature, no `FAIL` lines, no `panic` lines.** Typical run time: 60–180 seconds depending on Docker layer caching.

Failure diagnostics:

- **`FAIL internal/api` with `TestHandleImport_ForceAdd_...` undefined:** leftover reference to the deleted force-add tests. Grep for `ForceAdd` in `internal/api/` and delete any dangling callers. These tests were removed in Chunk 1 Task 3 — if any remain, Task 3 Step 3.2 was skipped.
- **`FAIL internal/api` with `TestHandleImportPatchRow_...`:** new PATCH tests from Chunk 2 are broken. Re-run the specific failing test with `-v` inside the same container to get the stack trace, then diagnose. Do NOT "fix" by deleting the test.
- **`FAIL internal/database` unrelated to this feature:** not your problem, but stop and tell the user — this is a pre-existing regression that this feature is about to mask if you proceed.
- **`panic: sync: unlock of unlocked mutex` in `internal/api`:** `importStore` session lock leaked across tests. `clearImportStore` is missing from a test's setup block. Add it.
- **Docker image pull timeout:** pre-pull the image once (`docker pull golang:1.26-alpine`) and re-run. Not a test failure — do NOT retry the test command in a loop waiting for the pull.

- [ ] **Step 26.3: Run `go vet` across all packages in the same container**

```bash
docker run --rm -v "$(pwd)":/src -w /src golang:1.26-alpine \
  sh -c "apk add --no-cache build-base >/dev/null && go vet ./..."
```

Expected: **zero output** (no warnings, no errors). `go vet` catches shadowed variables, printf format mismatches, unreachable code, and misuses of `context.Context` — all things that pass tests but fail review.

Failure diagnostics:

- **`possible misuse of unsafe.Pointer`:** not applicable to this feature; investigate as pre-existing.
- **`printf: Printf format %v has arg err of wrong type error`:** new handler code uses `fmt.Errorf` or `log.Printf` incorrectly. Fix the specific file — do not add `// nolint` comments.
- **`context.Background.WithValue` misuse:** handler is propagating request context wrong. Fix at the source.

- [ ] **Step 26.4: Run the full frontend test suite (Vitest)**

```bash
cd web && npm test -- --run
```

The `-- --run` flag forces Vitest out of watch mode so the command exits with a non-zero code on failure instead of hanging in interactive mode. Expected: **all test files pass**, including both the existing suites and the new Chunk 4 (`useImportSession.test.tsx`) and Chunk 5 (`ImportPreviewTable.test.tsx`) suites.

Failure diagnostics:

- **`happy-dom is not defined` or `jsdom not found`:** Vitest config drift. Re-check `web/vite.config.ts:19-28` for `environment: 'happy-dom'`. Do NOT switch to `jsdom` — the repo pins `happy-dom`.
- **`ReferenceError: localStorage is not defined`:** a test is running in Node context instead of `happy-dom`. Check the top of the failing test file for a `// @vitest-environment node` directive and remove it.
- **`TestingLibraryElementError: Unable to find element`:** a Chunk 5 test's selector diverges from the final `ImportPreviewTable` DOM shape. The `data-collision="true"` and `data-cell-error="true"` attributes are the stable test markers — if the component emits them correctly and the test still fails, the assertion is on the wrong thing. Re-read the test, not the component.
- **`MSW handler was not called`:** an integration test mocked a route the hook never hits. Compare the MSW handler URL against the URL `useImportSession` actually builds.

- [ ] **Step 26.5: Run the full TypeScript type check**

```bash
cd web && npx tsc --noEmit
```

Expected: **0 errors across the whole project**, not just the files this feature touched. `tsc --noEmit` checks the full graph, so a type error in an unrelated page (e.g. `Dashboard.tsx`) will surface here — if it does, it's pre-existing and not caused by this feature, but flag it to the user so it's not silently swept under this feature's PR.

Failure diagnostics: see Task 24 Step 24.4's diagnostics table — those cover every new failure mode introduced by this feature. Any error outside that table is either a pre-existing issue or a genuine integration breakage; investigate the specific file before "fixing" it.

- [ ] **Step 26.6: Record the green results in a scratch note**

```bash
# In your local scratch buffer (do NOT commit):
# - go test ./...            → PASS
# - go vet ./...              → clean
# - npm test -- --run         → PASS
# - npx tsc --noEmit          → 0 errors
```

The acceptance scripts in Task 27 reference this baseline — they are only valid against a green regression run. If any of the above commands failed, STOP and fix the root cause before starting Task 27. Do NOT run the acceptance scripts against a red suite.

---

### Task 27: Spec-mandated manual acceptance scripts (#16 and #17)

The design spec's Testing Strategy at §§424-429 lists two formal manual acceptance scripts — these are distinct from Task 25's generic dev-server smoke test. Task 25 catches wiring bugs against a 7-row file; tests #16 and #17 catch production-grade bugs against realistic 20-row files and the F5 resume path.

**Why both manual:** SpenDrop has no Playwright harness today (see spec §§426-427). Adding one is several hours of config work (preview server, seeded test DB, auth fixtures, CI) and is explicitly Out of Scope for 3.4b. These two checklists stand in for Playwright coverage until the harness lands.

**Files:** none (manual walkthrough, no commits in this task)

- [ ] **Step 27.1: Prepare a 20-row Starbucks fixture**

Create an `.xlsx` file containing exactly 20 rows, all identical:

| Date       | Description | Amount | Category |
|------------|-------------|--------|----------|
| 2025-02-01 | Starbucks   | 5.00   | Food     |
| 2025-02-01 | Starbucks   | 5.00   | Food     |
| ... (18 more identical rows) ||||

Save as `~/scratch/spendrop-test-starbucks-20.xlsx`. Do NOT check into the repo — these fixtures stay on the developer machine.

- [ ] **Step 27.2: Test #16 — Starbucks happy path (20 → 0 collisions via Tab-burst)**

With the backend and frontend dev servers running (from Task 25 steps 25.2–25.3), log in and navigate to Settings → Import. Upload `spendrop-test-starbucks-20.xlsx`.

Expected initial state:

- Preview table shows 20 rows total.
- One collision group of 20 members is displayed with header `⚠ 20 rows collide — same date + description + amount + category` (or equivalent wording from Task 23).
- Every row has the amber left border / tinted background (`data-collision="true"`).
- Footer reads `Fix or skip 20 collisions to enable import`.
- `Import 20` button is disabled.

Now Tab-burst the dates to make them unique:

1. Double-click row 1's Date cell. An `<Input>` appears pre-filled with `2025-02-01`.
2. Type `2025-02-02` and press Tab. Focus advances to row 1's Description cell (or the next tab-able cell — exact target depends on Task 22's tab order). The Date cell commits the new value and the row flips clean (amber background disappears).
3. Click row 2's Date cell, type `2025-02-03`, Tab to commit.
4. Continue for rows 3–20, using incrementing dates (`2025-02-04`, `2025-02-05`, ..., `2025-02-21`).

Expected final state before clicking Import:

- All 20 rows are clean (no amber backgrounds, no warn icons).
- Footer reads `20 rows ready to import` (or the positive confirmation wording from Task 23).
- `Import 20` button is **enabled**.
- The group header row is gone (the collision group is empty).
- No cell shows an inline 400 error.

Click `Import 20`.

Expected post-import state:

- Success panel shows `20 transactions imported, 0 skipped out of 20 total rows`.
- Navigate to the Transactions page and filter to February 2025 — you should see 20 distinct Starbucks rows with the dates you typed.
- **Critical assertion:** no description contains a " (N)" numeric suffix. If you see `Starbucks (1)`, `Starbucks (2)`, etc., the legacy `force_add` suffix path was NOT deleted in Chunk 1 Task 3 — this is a release-blocking regression. Return to Task 3 and verify every deletion listed.
- `localStorage.spendrop-import-id` is cleared (check devtools → Application → Local Storage).

- [ ] **Step 27.3: Test #17 — F5-during-edit resume**

Re-upload the same `spendrop-test-starbucks-20.xlsx` fixture (or a fresh 20-row fixture — does not matter).

Edit dates on rows 1-5 only. Commit each edit with Enter (not Tab). After commit #5, the preview should show:

- Rows 1-5 clean with your edited dates
- Rows 6-20 still amber (group of 15 members)
- Footer: `Fix or skip 15 collisions to enable import`
- `Import 20` button disabled

**Now press F5** (full browser reload, not devtools refresh).

Expected post-reload state:

- The app reloads. You end up back on Settings → Import (the page remembers where you were via `localStorage.spendrop-import-id`).
- The preview table restores with **identical state**: rows 1-5 still show your edited dates and are clean, rows 6-20 still show the original `2025-02-01` date and are still amber.
- Footer still says `Fix or skip 15 collisions to enable import`.
- `Import 20` button is still disabled.
- No console errors, no red overlay, no spinner stuck.
- The backend's `/api/import/{importID}` GET endpoint was hit on mount — you can verify in the browser's devtools Network tab after the reload.

Now finish the edit: Tab-burst rows 6-20 to unique dates (`2025-02-07` through `2025-02-21`), confirm the `Import 20` button enables, click Import.

Expected final state:

- Success panel: `20 transactions imported, 0 skipped out of 20 total rows`.
- Transactions page shows the 20 distinct rows with the full date sequence (5 from before the F5 + 15 from after).
- `localStorage.spendrop-import-id` is cleared.

**If the reload erases your edits:** the `useImportSession` mount-resume path is broken. The hook is either not calling `GET /api/import/{importID}` on mount, not reading `localStorage.spendrop-import-id` at startup, or crashing on 404 without fallback. Re-check Chunk 4 Step 17.3 — this is the single path that owns the F5-resume invariant.

**If the reload shows a blank preview table:** the hook is hitting GET 404 (session expired — unlikely since TTL is 60min and you just uploaded) OR crashing before it can render the resumed state. Check browser console for unhandled errors.

- [ ] **Step 27.4: Record acceptance results in a scratch note**

```bash
# In your local scratch buffer (do NOT commit):
# Test #16 (Starbucks 20-row happy path) → PASS / FAIL + notes
# Test #17 (F5-during-edit resume)       → PASS / FAIL + notes
```

Both tests MUST PASS before proceeding to the review pipeline in Tasks 28-32. If either fails, STOP and return to the flagged Chunk 1-5 task to fix the root cause. Do NOT skip the acceptance scripts to "save time" — they exist to catch the exact bugs unit tests miss (cross-request state, focus loss on rerender, localStorage contract breaks).

---

### Task 28: Dispatch `data-correctness-reviewer` subagent

Per project memory (`reference_data_correctness_reviewer`): this agent must be dispatched on any change touching imports, reports, dashboards, or migrations. This feature touches imports (new PATCH endpoint, new GET session endpoint, `buildCollisionGroups`, `content_hash` re-computation, soft-delete filter discipline in DB match detection) — it is squarely in the agent's scope.

**Files:** none (advisory review only; any fix commits this produces are scoped to the specific file the reviewer flagged)

- [ ] **Step 28.1: Dispatch the reviewer**

Use the `Agent` tool with `subagent_type: "feature-dev:code-reviewer"` if that's the closest available reviewer in this environment, or the `data-correctness-reviewer` agent type if a dedicated one is registered. Pass it the full list of modified backend files:

- `internal/api/import_handlers.go`
- `internal/api/router.go`
- `internal/api/import_handlers_test.go`
- `internal/database/queries.sql` (only if touched — verify with `git diff main`)
- `internal/database/content_hash.go` (only if touched)

Ask the reviewer to focus on:

1. **Soft-delete filter discipline.** Every SQL read in this feature's code path must filter `t.deleted_at IS NULL`. The single canonical reader is `GetTransactionByContentHash` at `internal/database/queries.sql:182-197` — any new raw-SQL SELECT in the handlers is a red flag. Verify `buildCollisionGroups` and the PATCH handler only call `GetTransactionByContentHash`, not fresh SELECTs.
2. **Content hash parity.** Upload-time hash and PATCH-time re-hash MUST use the same `ComputeContentHash` code path. If there are two callers, they must share the same normalization input (trim, lowercase, whitespace collapse). A whitespace divergence means edits silently fail to clear collisions.
3. **Category name canonicalization.** The group builder takes `catNameToID` / `catIDToName` lookups — verify these are computed consistently across upload, confirm, and PATCH. If upload-time and PATCH-time build different maps, the same row hashes differently on each path and collisions leak.
4. **Audit row gap.** The existing `handleImportConfirm` bypasses `TransactionStore` (see memory `project_import_bypasses_audit`) — this feature does NOT fix that gap, and should NOT try to. Verify the reviewer doesn't flag it as new; it's a pre-existing scope-out.
5. **TransactionStore mutation discipline.** Any new backend code path that writes to `transactions` MUST go through `TransactionStore`. PATCH on the import session does NOT write to `transactions` (it only mutates the in-memory `importStore` slice), so it is correctly exempt — verify the reviewer doesn't flag it as a store bypass.

- [ ] **Step 28.2: Triage reviewer findings**

For each finding the reviewer returns:

- **Critical / release-blocking:** fix immediately in a new commit on the same branch. Do NOT batch multiple critical fixes into one commit — one fix per commit so the git history reads cleanly.
- **Important / should-fix:** fix in the same pass. Same one-fix-per-commit rule.
- **Minor / style:** fix if the fix is a one-liner; otherwise write a brief follow-up note in the PR description and defer.

Per CLAUDE.md: **never skip Critical, Important, or Minor review issues.** "Minor" does not mean "skippable" — it means "not release-blocking but still required before merge."

- [ ] **Step 28.3: Re-run Task 26 after every fix**

Every reviewer-driven fix must pass the full regression suite from Task 26 before the next fix or the next task. If the fix touched Go code, re-run `go test ./...` and `go vet ./...` inside the container. If the fix touched TypeScript, re-run `npm test -- --run` and `npx tsc --noEmit`. Do NOT accumulate unreviewed fixes and run the suite once at the end — that makes bisecting regressions much harder.

---

### Task 29: Dispatch `security-auditor` subagent

This feature adds a new authenticated API endpoint (`PATCH /api/import/{importID}/rows/{rowID}`), accepts user-supplied field values that end up in the database, parses and re-validates dates and amounts, and touches the auth/ownership check path via the shared `loadImportEntryForUser` helper introduced in Chunk 1. All of that is squarely in the security-auditor agent's scope per global CLAUDE.md ("security-auditor: before commits touching auth, user input, secrets, or endpoints").

**Files:** none (advisory review only)

- [ ] **Step 29.1: Dispatch the reviewer**

Use the `Agent` tool with `subagent_type: "security-auditor"` if available. Pass the full list of new/modified backend files (see Task 28 Step 28.1) plus `web/src/hooks/useImportSession.ts` and `web/src/components/ImportPreviewTable.tsx`.

Ask the reviewer to focus on:

1. **Ownership check on PATCH.** The `loadImportEntryForUser` helper must reject any request whose `session.UserID` does not match the authenticated user on the context. A missing check means user A can PATCH user B's import session if they guess the `import_id` UUID — low practical exposure but unambiguously a vulnerability.
2. **Ownership check on GET session.** Same rule. GET leaks are often overlooked because "it's just a read"; the session rows contain uncommitted transaction data the user has not yet persisted, and possibly category names the other household shouldn't see.
3. **Input validation on PATCH.** `validateImportField` must reject empty / NaN / Inf amounts, unparseable dates, and descriptions over `limits.MaxDescriptionLength`. A missing check means the frontend can ship a row into `importStore` that later fails at confirm time with a less-helpful error message — not a security issue per se, but the reviewer should verify the validation table in the spec is fully implemented.
4. **Path parameter parsing.** `chi.URLParam(r, "rowID")` returns a string — the handler must parse it with `strconv.Atoi` and reject negatives / non-numeric / out-of-range values before using it as a slice index. An un-parsed rowID used as an index is a crash bug (`panic: index out of range`), not a security issue, but it's an obvious low-effort hardening the reviewer should catch.
5. **XSS surface on the new component.** `ImportPreviewTable` renders user-supplied strings (description, category name) into the DOM. React auto-escapes text nodes, so the surface is zero if the component uses `{value}` consistently. The reviewer should flag any `dangerouslySetInnerHTML` or untrusted HTML insertion — there should be none.
6. **Session TTL change.** The spec bumps `importTTL` from 30min → 60min (Chunk 1 Task 1 Step 1.3). The reviewer should confirm this is still a finite TTL (not `time.Hour * 24` or longer), since unexpired session state is a memory leak and a stale-data-resurrection vector.
7. **CSRF posture on PATCH and DELETE.** SpenDrop uses cookie-based auth; verify chi's existing middleware chain applies to the new routes. The new `PATCH` and `GET` endpoints must go through the same auth middleware as `POST /import/upload` — an accidental route registration outside the auth group would let an unauthenticated attacker PATCH any import session whose UUID leaked.

- [ ] **Step 29.2: Triage reviewer findings (same rule as Task 28 Step 28.2)**

Critical / Important / Minor — all get fixed. See Task 28 Step 28.2 for the rule.

- [ ] **Step 29.3: Re-run Task 26 after every fix (same rule as Task 28 Step 28.3)**

---

### Task 30: Dispatch `code-reviewer` subagent

Per CLAUDE.md project rules: "Always run code quality + logic review before committing — dispatch a code-reviewer subagent." This is distinct from the data-correctness and security reviews above — `code-reviewer` checks general code quality (naming, DRY, YAGNI, dead code, magic numbers, error wrapping, test design), while the earlier two reviews check domain-specific concerns.

**Files:** none (advisory review only)

- [ ] **Step 30.1: Dispatch the reviewer**

Use the `Agent` tool with `subagent_type: "code-reviewer"` or `subagent_type: "superpowers:code-reviewer"` (whichever is registered). Pass it the full diff of the feature branch against `main`:

```bash
git diff main --stat
git diff main -- internal/api/ internal/database/ web/src/
```

Ask the reviewer to focus on:

1. **Dead code.** The `force_add` / suffix path was deleted in Chunk 1 Task 3 — verify no dead references remain. Specifically check for orphaned test helpers, orphaned constants, and `// nolint` comments that referenced the deleted code.
2. **Error wrapping.** Every new `return err` should wrap with `fmt.Errorf("descriptive context: %w", err)` — raw `return err` loses the call stack. Check `buildCollisionGroups`, `validateImportField`, and the PATCH handler specifically.
3. **Magic numbers.** The `importTTL = 60 * time.Minute` constant is fine (named), but any inline `60 * time.Minute` or `30 * time.Minute` in the new code is a miss.
4. **Duplication.** The auth / ownership / expiry check block that Chunk 1 extracts into `loadImportEntryForUser` must actually be used by every handler that previously inlined it. Verify `handleImportConfirm`, `handleImportCancel`, `handleImportPatchRow`, and `handleImportGetSession` all call the helper — not two of them.
5. **Test design.** Every new test in `import_handlers_test.go` and `ImportPreviewTable.test.tsx` should own at least one concrete bug from the spec's Testing Strategy. Tests that verify framework internals (shadcn Input focus, happy-dom DOM behavior) are anti-patterns — see memory `project_test_suite_audit_todo`. The reviewer should flag any such test.
6. **File size.** `Settings.tsx` is already 1500+ lines and this feature added more. The reviewer should flag if the file has grown so large that it needs a split — not in this feature's scope, but as a follow-up note. Memory says to prefer smaller files; this is a "flag for follow-up" not a "fix in this PR" item.
7. **Naming consistency.** `cellErrors`, `unresolvedCount`, `canImport`, `pendingPatchCount` — these are the canonical names from the spec. Any divergence (e.g. `cellErrorMap`, `unresolved`, `canSubmit`) is a rename bug that will confuse future readers.

- [ ] **Step 30.2: Triage reviewer findings (same rule as Task 28 Step 28.2)**
- [ ] **Step 30.3: Re-run Task 26 after every fix (same rule as Task 28 Step 28.3)**

---

### Task 31: Dispatch `ui-ux-reviewer` and `design-enforcer` subagents (parallel)

Per global CLAUDE.md: `ui-ux-reviewer` runs after any user-facing interface change, and `design-enforcer` runs after any UI feature to check visual consistency against the design system. This feature adds `ImportPreviewTable` + modifies the Settings → Import flow — both reviewers apply.

These two reviews are **independent** — dispatch them in parallel (two agent tool calls in the same message) to save wall-clock time.

**Files:** none (advisory review only)

- [ ] **Step 31.1: Dispatch both reviewers in parallel**

In a single message, send two `Agent` tool calls:

1. `subagent_type: "ui-ux-reviewer"` — focus areas:
   - **Keyboard navigation completeness:** Tab advances focus, Shift+Tab moves left, Enter commits + moves down, Escape cancels. A missing direction is a usability bug.
   - **Focus rings on edit cells.** shadcn `<Input>` ships with a visible focus ring; verify the component uses it and doesn't `className="focus:ring-0"` it away.
   - **Screen-reader announcements.** The `aria-live="polite"` region with the collision count must update when the count changes. If it's rendered once at mount and never re-reads, it fails. Verify the count is a reactive derivation of the hook state.
   - **Color contrast.** Amber collision highlight must pass WCAG AA against the page background (4.5:1 for text, 3:1 for non-text indicators). shadcn defaults are usually fine; verify.
   - **Double-click-to-edit discoverability.** No visible affordance tells users the cells are editable until they double-click. Flag if this is a first-time-user bug (the acceptance scripts catch it for experienced users only).
   - **Error cell contrast.** Inline 400 error ring + one-line message must be readable against amber collision background AND against the clean-row background. Verify both states.
   - **Loading states on Import button.** While the confirm PATCH is in flight, the button must visibly indicate progress (spinner or disabled state) — not look clickable.

2. `subagent_type: "design-enforcer"` — focus areas:
   - **Design token usage.** Every color (amber collision, red error, green clean) must come from the design token palette, not ad-hoc hex codes. Grep the new files for `#` followed by hex — there should be none.
   - **Spacing system.** Padding and margins must use Tailwind's spacing scale (`p-2`, `gap-4`) — not arbitrary pixel values (`p-[7px]`).
   - **Typography.** Row text, group headers, and footer message must use the existing text-size / font-weight classes — not custom `text-[13px]` values.
   - **Component pattern alignment.** `ImportPreviewTable` should follow the same table-component pattern as `TransactionEntryRow` (the other spreadsheet-style editor in the app). If the two diverge, pick the established pattern.
   - **Dark-mode parity.** Every new color must have a dark-mode variant. Test with the dark-mode toggle manually (or verify the class lists include `dark:` prefixes).

- [ ] **Step 31.2: Triage each reviewer's findings separately (same rule as Task 28 Step 28.2)**

- [ ] **Step 31.3: Re-run Task 26 after every fix (same rule as Task 28 Step 28.3)**

---

### Task 32: Update README.md and DESIGN_GUIDE.md

Per project memory (`feedback_keep_docs_updated`): README.md and DESIGN_GUIDE.md must be updated with every change/feature. This feature introduces a new user-facing workflow (inline collision resolution during import) — users reading the README to understand how SpenDrop handles duplicates need to see the new flow.

**Files:**
- Modify: `README.md`
- Modify: `DESIGN_GUIDE.md` (only if it covers import UX — if it only covers visual design tokens, skip this file and note in the commit message)

- [ ] **Step 32.1: Locate the Import section in README.md**

```bash
grep -n -i "import" README.md
```

Expected: a section heading like `## Import` or `### Importing transactions`. If there is no such section today, add one — it was an oversight. If the existing section describes the old "silent duplicate skip" behavior, REWRITE that paragraph to describe the new inline collision resolution.

- [ ] **Step 32.2: Write the new Import section (or paragraph)**

Core content the section must cover:

- **What the user can upload:** `.xlsx` files with Date, Description, Amount, Category columns (unchanged from pre-feature).
- **What happens when rows collide (new):** rows with matching date+description+amount+category are grouped in the preview with an amber highlight. The user can edit any field inline (double-click a cell, Enter or Tab to commit, Escape to cancel) to break the collision, or check the Skip box to exclude the row entirely.
- **What happens during DB collision (new):** if an uploaded row matches an existing transaction already in the database, it's flagged in the same UI — the preview shows the existing row's details so the user can decide to edit or skip.
- **F5 resume (new):** if the browser reloads mid-edit, the import session is restored from localStorage + the backend session (60min TTL).
- **What changed:** the old " (N)" suffix behavior (force-add duplicates) was removed — editing or skipping is now the only way to handle duplicates.

Keep it to 5-8 sentences max. Link to the design spec if helpful: `See docs/superpowers/specs/2026-04-15-import-collision-resolution-design.md for the full design.`

- [ ] **Step 32.3: Check DESIGN_GUIDE.md scope**

```bash
grep -n -i "import\|preview\|collision" DESIGN_GUIDE.md
```

- If the file has an Import section or a preview-table component pattern section, update it to reference `ImportPreviewTable` as the canonical spreadsheet-edit pattern (alongside `TransactionEntryRow`).
- If the file is purely visual (color palette, typography scale), SKIP it — this feature doesn't introduce new design tokens.

- [ ] **Step 32.4: Commit the documentation update**

```bash
git add README.md
# Include DESIGN_GUIDE.md only if you actually edited it in 32.3:
# git add DESIGN_GUIDE.md
git commit -m "docs(import): document inline collision resolution flow"
```

The `docs(import):` conventional prefix is deliberate — SpenDrop uses semver auto-bump based on conventional commits (see CLAUDE.md), and `docs:` commits don't trigger a version bump.

---

### Task 33: Final commit and branch handoff (no push)

SpenDrop's workflow rules: **never push to remote without explicit user request** — the user creates PRs themselves. This task ends with the branch in a clean state, ready for the user to review and push.

**Files:** none (git housekeeping only)

- [ ] **Step 33.1: Verify the branch is clean and review-ready**

```bash
git status
git log --oneline main..HEAD
```

Expected:

- `git status` shows working tree clean.
- `git log` shows a linear series of commits, one per task (or one per sub-step for larger tasks) — roughly 20-40 commits total across Chunks 1-6. No "wip" commits, no "fixup!" commits, no merge commits from main.

If there are stray uncommitted changes, STOP — every change should already be committed to a task-specific commit. Uncommitted changes at this stage are usually either (a) files you forgot to `git add` or (b) a reviewer fix you started but didn't finish. Resolve them before proceeding.

- [ ] **Step 33.2: Force-add the plan and spec files if they live under `docs/superpowers/`**

Per CLAUDE.md: `docs/superpowers/` is `.gitignore`d. The spec + plan files must still be committed on the feature branch so reviewers can see them in the PR — use `git add -f`:

```bash
git add -f docs/superpowers/plans/2026-04-15-import-collision-resolution.md
git add -f docs/superpowers/specs/2026-04-15-import-collision-resolution-design.md
```

Check if those files already show modifications from plan/spec edits during the feature work. If they do, commit them:

```bash
git commit -m "docs(plan): finalize import collision resolution plan + spec"
```

If they are already committed on the feature branch, skip this step (no-op).

- [ ] **Step 33.3: Verify branch name and remote state**

```bash
git rev-parse --abbrev-ref HEAD
git remote -v
git branch -vv
```

Expected:

- Current branch is the feature branch (NOT `main`). CLAUDE.md: "Never commit to main — always create a new branch first, check branch before every commit."
- `origin` remote is set.
- The feature branch is either not tracking a remote at all (local-only) OR tracking `origin/feature-branch-name` with no divergence. Do NOT push.

- [ ] **Step 33.4: Summarize the feature for the user**

Write a short summary in the terminal output (NOT a commit):

```
Feature complete: Import Collision Resolution UI

- Commits: N (run `git log --oneline main..HEAD` to review)
- Files changed: backend (import_handlers.go, router.go, tests), frontend (Settings.tsx, ImportPreviewTable.tsx, useImportSession.ts, api/types.ts, api/import.ts), docs (README.md)
- Tests added: 9 backend (Go), 6 frontend (Vitest), 2 manual acceptance scripts
- Reviewers dispatched: data-correctness-reviewer, security-auditor, code-reviewer, ui-ux-reviewer, design-enforcer (Tasks 28-31)
- All reviewer findings resolved.
- Full regression suite green (Task 26).
- Manual acceptance scripts #16 and #17 passed (Task 27).

Ready for your review. The branch is local-only (per workflow rules: no push without explicit ask). When you're ready to open a PR, push with:
  git push -u origin <branch-name>
```

Do NOT run `git push` from this task. Do NOT open a PR via `gh pr create`. The user owns the publish step — SpenDrop's workflow rule is non-negotiable here.

- [ ] **Step 33.5: Mark the plan as complete**

This is the final task. When the human or driver agent sees Step 33.5 checked off, the feature is done. Any remaining work lives in Out of Scope (spec §§450-460) and becomes its own follow-up brainstorming / plan cycle.

No commit for this step. Just check the box.

---
