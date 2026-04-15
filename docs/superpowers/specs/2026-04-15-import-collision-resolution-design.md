# Import Collision Resolution UI — Design Spec

## Overview

Phase 3.4b extends the Settings → Import flow so users can resolve SHA-256 content-hash collisions by editing rows inline in the preview table. Replaces the current force-add-with-" (N)"-suffix behavior, which breaks the user's clean-description convention on the transactions page.

**User scenario that motivates the feature:**
- User batch-logs 20 identical transactions (e.g., 20 × `Starbucks $5.00` on `2025-01-07`) onto monthly bucket dates in their Excel tracker.
- SpenDrop's content-hash dedupe correctly flags all 20 as an intra-file collision group.
- Current behavior: "Force add" suffixes descriptions to `Starbucks (2)`, `Starbucks (3)`, etc. — breaks the user's "replace-all" normalization workflow for spending monitoring.
- New behavior: the preview table is editable. User edits the **date** on each row (or any other field), server re-hashes, collision clears, import enables.

**Design axiom:** the preview is the single cleanup surface. No post-import fixup, no " (N)" suffixes, no force-add escape hatch for inline-resolved rows.

**Scope is strictly Phase 3.4b:** the edit-in-preview loop and its backend. Import insert logic, content-hash definition, upload parser, and session store infrastructure are all pre-existing and mostly untouched.

---

## Data Flow

```
[1] User uploads .xlsx
       ↓
[2] Backend parses rows → builds preview response with:
     - rows[] (each with row_id, fields, content_hash, client_error?)
     - collision_groups[] (each with group_id, member row_ids, reason)
     - import_id (32-char hex, keys in-memory session)
       ↓
[3] Frontend caches import_id in localStorage, renders flat preview table
       ↓
[4] User double-clicks a cell (date/description/category/amount)
       ↓
[5] Frontend fires PATCH /api/import/:import_id/rows/:row_id
       { field, value, patch_id }
       ↓
[6] Backend re-runs single-row validator → re-computes content_hash →
     re-groups across the whole in-memory session → returns full response:
     { rows, collision_groups, patch_id }
       ↓
[7] Frontend merges response keyed by row_id, respects patch_id ordering
     (drops stale responses), re-renders amber/clean styling
       ↓
[8] When collision_groups == [] or all remaining collisions are `skip=true`:
     "Import N" button enables
       ↓
[9] User clicks Import → POST /api/import/:import_id/confirm
       ↓
[10] Backend re-checks session state; if any unresolved collisions remain,
      rejects with 409 + full collision_groups payload (all-or-nothing);
      otherwise inserts all non-skipped rows via CreateTransaction and
      clears the session
```

**Critical invariant:** the preview's display state and the server's session state are two views on the same data, synced only through PATCH responses. Frontend never mutates row state locally; it always waits for the server's re-hash and re-grouping.

---

## Backend API Design

### New endpoint: `PATCH /api/import/:import_id/rows/:row_id`

**Request:**
```json
{
  "field": "date" | "description" | "category" | "amount" | "skip",
  "value": "2025-01-08",
  "patch_id": 42
}
```

**Response (200 OK):**
```json
{
  "rows": [/* full current row list, keyed by row_id */],
  "collision_groups": [
    {
      "group_id": "g_abc123",
      "reason": "intra_file" | "db_match",
      "member_row_ids": [3, 7, 12],
      "db_match_id": 8421  // only when reason == "db_match"
    }
  ],
  "patch_id": 42
}
```

**Error responses:**
- `400` — validation error (bad date format, NaN amount, unknown category, empty description). Body: `{ code, field, message }`. Session state unchanged.
- `403` — import_id owned by another user. Reuses existing ownership helper.
- `404` — session expired or import_id not found.

**Response shape is always the full current snapshot**, not a sparse diff. Simpler frontend merge, deterministic behavior under races, cheap for the session sizes we target (50–500 rows).

### Modified endpoint: `POST /api/import/:import_id/confirm`

Existing endpoint, now with stricter enforcement:

- On entry, re-checks `collision_groups` in the session (recomputed, not cached).
- If any non-skipped row is still a member of any collision group → **409 Conflict** with the full `collision_groups` array in the body. **No partial insert.**
- Otherwise iterates non-skipped rows, inserts via `CreateTransaction` (existing path), writes content_hash, clears session.
- Existing `force_add []int` field is honored only for rows flagged at upload time — inline-resolved rows (those touched by PATCH) are tracked in a separate `edited_row_ids` set and are **ineligible for force_add** (the whole point was to avoid the " (N)" suffix).

### New endpoint: `GET /api/import/:import_id`

Resume endpoint for F5/tab-refresh. Returns the full preview response (`rows`, `collision_groups`, `import_id`) from in-memory session state. 404 if expired.

Frontend calls this on mount if `localStorage.importId` exists.

### Session storage

**Reuse existing `importStore sync.Map` at `internal/api/import_handlers.go:47-74`:**
- Bump TTL from 30min → **60min fixed** (not activity-based — avoids memory leak from idle tabs)
- Existing per-user slot cap and ownership check already guard against abuse
- Existing cleanup goroutine already reaps stale sessions
- **No new persistence layer.** Lost on server restart = re-upload. Correct tradeoff for a hobby tool.

### Validation + hashing reuse (critical)

**Single source of truth:** `internal/database/content_hash.go` normalization is the ONLY hashing path. Both upload and PATCH go through it. No separate "edit-time hash" helper.

**Single validator function:** a shared per-field validator is used by both the upload preview builder and the PATCH handler. The difference is the error-wrapping mode:
- **Upload mode:** silent-zero on empty amount (existing behavior), row still surfaces in preview with warning
- **Edit mode:** hard 400 on any validation failure, session state unchanged

**Parser reuse:** `parseImportDate` (`import_handlers.go:1078`), `parseImportAmount` (`import_handlers.go:991`), category resolution — all reused identically. No divergent edit-time parsers.

### Unicode normalization fix

**One-line change to `internal/database/content_hash.go`:**

```go
import "golang.org/x/text/unicode/norm"

// In the normalization step:
normalized := norm.NFC.String(strings.ToLower(strings.TrimSpace(s)))
```

Catches the Mac-NFD vs Windows-NFC mismatch when a user retypes `café` in the preview after Excel uploaded it in NFC. Turkish dotless-i and similar locale-sensitive cases are NOT fixed by this — documented as known limitation.

### Request sequence number

`patch_id` is a client-provided monotonic counter per import session. Backend echoes it in the response. Frontend uses it to drop stale out-of-order responses (see Frontend § Race Prevention).

Backend has no logic on it — just JSON echo. Zero server-side complexity.

---

## Frontend Design

### Layout: flat editable table (Option A)

Single table, collision rows sorted to the top with amber highlight. No separate "triage zone". All rows share the same shape — edit any cell inline, collision and clean rows behave identically.

**Columns:** `[warn-icon?] [Date] [Description] [Category] [Amount] [Skip]`

**Sort policy for MVP:** collision rows frozen at top, ordering stable after edits (a row that resolves stays in place, does NOT re-sort mid-flow). Avoids row-jumping during bulk Tab-burst editing. Re-sort happens only on new upload or explicit refresh.

### Cell editing

- **Double-click** to enter edit mode (matches spreadsheet muscle memory, matches Handsontable/Flatfile/OneSchema convention)
- Single-click only selects the row (preserved for keyboard navigation)
- **Escape** cancels edit, restores original value, no PATCH fired
- **Enter** commits edit, fires PATCH
- **Tab** commits edit, fires PATCH, moves focus to next cell right
- **Shift+Tab** same but moves left

### Component choices

All shadcn, dense classNames for table density:

| Field | Component | Dense classes |
|---|---|---|
| Date | `Input type="text"` | `h-8 px-2 py-1 text-sm focus-visible:ring-1 focus-visible:ring-offset-0 border-0 rounded-none` |
| Description | `Input` | same |
| Category | `Select` | `h-8 text-sm` — existing pattern from `TransactionRow.tsx:122-133` |
| Amount | `Input inputMode="decimal"` | same Input classes + `text-right font-mono tabular-nums` |
| Skip | `Checkbox` | default |

Date format: free-text input, parsed server-side via existing `parseImportDate`. No date-picker popover — breaks keyboard flow.

**Do NOT reuse `TransactionRow.tsx` edit pattern** — it's an "edit-mode toggle entire row" design, wrong for spreadsheet-feel cell-level editing.

### Visual states

- **Clean row:** standard row background, no warning icon
- **Collision row:** amber background (`bg-amber-500/9`), left border accent, warning icon in first column
- **Skipped row:** strikethrough text + muted gray (`text-muted-foreground line-through`)
- **Editing cell:** focused input with ring, cursor visible
- **After PATCH response:** styling is re-derived from the fresh server response, NEVER stale-local-state

### Footer

- **Left:** status message — "⚠ Fix or skip N collisions to enable import" | "✓ Ready to import N rows"
- **Right:** `Import N` button — disabled when `unresolved_collisions > 0`, disabled while `pending_patch_count > 0`, enabled when both are zero (including the all-skipped case)

### localStorage resume

**On file upload success:** `localStorage.setItem('spendrop_import_id', import_id)`

**On component mount:**
1. Read `localStorage.spendrop_import_id`
2. If present → `GET /api/import/:import_id`
3. On 200 → rehydrate rows, collision_groups, render preview table
4. On 404 → show "Your previous import session expired" message, clear localStorage, return to file-drop state

**On successful `/confirm`:** clear localStorage.

Survives tab refresh (the real vulnerability); does NOT survive server restart (acceptable tradeoff).

### Race prevention (cross-row PATCH ordering)

Frontend tracks `const latestPatchIdRef = useRef(0)`. Before each PATCH:

```ts
const patchId = ++latestPatchIdRef.current;
const response = await patchRow({ ..., patch_id: patchId });
if (response.patch_id < latestPatchIdRef.current) return; // stale, drop
applyResponse(response);
```

This handles the "user Tabs from row 1 → row 2 faster than round-trip, response 1 arrives after response 2" case. Response 1's stale `collision_groups` would otherwise clobber response 2's merged state.

**Why not AbortController:** abort-per-row doesn't protect cross-row races. Sequence number is simpler and correct.

### Confirm button lockout during pending PATCH

`pendingPatchCount` state increments before fetch, decrements in finally. Import button disables while `pendingPatchCount > 0`. Prevents the "/confirm runs while PATCH in flight, sees stale session" race at the frontend layer — no backend locking required.

### API contract additions

`web/src/api/types.ts` — extend `ImportPreview`:

```ts
interface ImportPreview {
  import_id: string;
  rows: ImportRow[];
  collision_groups: CollisionGroup[];  // NEW
  // ... existing fields
}

interface CollisionGroup {  // NEW
  group_id: string;
  reason: 'intra_file' | 'db_match';
  member_row_ids: number[];
  db_match_id?: number;
}

interface PatchRowRequest {  // NEW
  field: 'date' | 'description' | 'category' | 'amount' | 'skip';
  value: string | boolean;
  patch_id: number;
}
```

---

## Edge Cases & Correctness Invariants

### Hash normalization parity (highest-risk trap)

**Rule:** upload-time hash and PATCH-time re-hash MUST use the same code path in `content_hash.go`. Any divergence means edits fail to clear collisions despite visually identical values.

**Guard:** one dedicated test that takes a row, hashes it, mutates via PATCH, re-hashes, asserts equality under whitespace/case/NFC-NFD variations.

### Tombstoned rows in DB-match detection

**Rule:** The re-check during PATCH must filter `t.deleted_at IS NULL`. Reuse the existing `GetTransactionByContentHash` query at `internal/database/queries.sql:182-187` — do NOT write a fresh SELECT in the handler.

**Guard:** `*_HidesTombstoned` test seeds a live row + a tombstoned row (`amount=999` sentinel) with the same content hash as an uploaded row, asserts the import row is NOT flagged as a DB collision.

### Force-add is disabled for inline-resolved rows

**Rule:** Rows touched by PATCH are tracked in an `edited_row_ids` set in the session. The `force_add []int` field in the `/confirm` request is silently filtered against this set — inline-resolved rows cannot be force-added, because force-add would re-introduce the " (N)" suffix and defeat the whole feature.

### Validation field rules

| Field | Rule | Error code |
|---|---|---|
| `date` | Non-empty, parseable via `parseImportDate`, in `[1900, 2100]` | `INVALID_DATE` |
| `description` | Non-empty, ≤ 500 chars after trim | `INVALID_DESCRIPTION` |
| `category` | Resolves to existing category via name lookup; on miss, response includes `category_candidates: []` (top 5 by edit distance) | `UNKNOWN_CATEGORY` |
| `amount` | Parseable via `parseImportAmount`, not NaN/Inf. **Upload mode: empty → 0. Edit mode: empty → 400.** | `INVALID_AMOUNT` |
| `skip` | Boolean, no validation | — |

**Error precedence ranking** (when multiple errors could apply): field-presence > field-shape > cross-row uniqueness. The first failing rule wins; downstream rules are not evaluated. Deterministic user experience — the same edit always surfaces the same error message.

### Collision state transitions

| Before | Edit | After |
|---|---|---|
| Pair collision (2 rows) | Edit row A to unique value | Both rows flip to clean; group disappears |
| Triple collision (3 rows) | Edit row A to unique value | Row A clean; remaining 2 stay grouped |
| Clean row | Edit amount/date into existing group's space | Row joins the existing group; group enlarges |
| Clean row | Edit into a value that matches another clean row | New 2-member group forms |
| Collision row | Edit into a value that matches a DIFFERENT group | Row moves from group A → group B |
| Skipped collision row | Edit into unique value | Skip flag cleared, row becomes clean |
| Clean row | Mark skip = true | Row is excluded from import but still displays |

### Skipped ≠ unresolved

- `skip=true` means the row is excluded from `/confirm` inserts entirely
- Skipped rows do NOT count toward the "unresolved collisions" count
- Skipped rows display with strikethrough but remain visible (user can un-skip)
- "Import N" button enables when `unresolved_non_skipped_collisions == 0`

### Session expiry mid-edit

- Backend returns 404 on PATCH to expired session
- Frontend catches 404 → shows toast "Import session expired — please re-upload" → clears localStorage → returns to file-drop
- User loses in-progress edits. Acceptable: 60min TTL is generous, and the resume endpoint covers the F5 case

### Stale error-styling bug (importcsv #16 anti-pattern)

**Rule:** styling is always derived from the latest server response, never from stale local state. A row that transitions collision → clean loses amber background on the very next render. A cell that had an error loses the error ring on the next successful PATCH response.

**Implementation:** no "sticky error" state in component. Each render reads from `rows[row_id]` in the canonical server-shaped state.

### Cross-row PATCH race

See Frontend § Race Prevention. `patch_id` sequence number guards against out-of-order responses.

### Confirm + pending PATCH race

See Frontend § Confirm button lockout. Import button disabled while any PATCH in flight. No backend locking.

---

## Testing Strategy

**Target: 17 tests total.** Core bar (from Agent 1's simplification review): every bug owned by exactly one test, every test owns at least one bug. Nothing else earns its keep.

### Backend — Go (9 tests)

All in `internal/api/import_handlers_test.go`, following existing pattern (direct handler invocation via `httptest.NewRecorder()`, see `import_handlers_test.go:167-171`).

1. **PATCH happy path (group of 3):** upload 3 identical rows, PATCH row 1's date, assert row 1 clean + remaining 2 still grouped together. Owns: core regrouping + stale group_id in untouched rows.
2. **PATCH re-collision:** PATCH moves row from group A → group B; if row had `skip=true`, skip flag cleared on un-collide. Owns: stateful regrouping, skip-persistence rule.
3. **PATCH 404 on expired session.** Owns: session expiry backend half.
4. **PATCH content-hash parity (table-driven, 4 cases):**
   - Whitespace: `" Starbucks "` → `"Starbucks"` → same hash
   - Case: `"STARBUCKS"` → `"Starbucks"` → same hash
   - Unicode NFC/NFD: `"café"` composed vs decomposed → same hash (after NFC fix)
   - Baseline different strings → different hashes
   Owns: whitespace + Unicode hash-mismatch bug class.
5. **Confirm 409 with full `collision_groups`** when any unresolved non-skipped collision remains. Owns: partial-import rejection invariant.
6. **Confirm happy path:** all resolved → `CreateTransaction` called per row, `content_hash` persisted. Owns: end-to-end wiring.
7. **Confirm skipped rows excluded** from inserts. Owns: skip ≠ unresolved distinction.
8. **`*_HidesTombstoned`:** tombstoned DB row with `amount=999` sentinel NOT flagged as DB collision for matching upload row. Owns: soft-delete leak. Satisfies CLAUDE.md tombstone convention.
9. **Upload preview flags intra-file collision groups** correctly (baseline — the initial grouping must work before any PATCH can be tested).

### Frontend — Vitest (6 tests)

All in `web/src/components/ImportPreviewTable.test.tsx` or a new `EditableCell.test.tsx`, using Vitest + happy-dom + `@testing-library/react` + `user-event`. Config at `web/vite.config.ts:19-28`, setup at `web/src/test/setup.ts`. Pattern reference: `web/src/components/TransactionEntryRow.test.tsx`.

10. **ImportPreviewTable renders:** collision rows get amber + warning icon, clean rows don't. Owns: initial render.
11. **Stale-style regression:** row flips collision → clean on PATCH response, amber background removed on next render. Owns: importcsv #16 bug class — single most important frontend test.
12. **Import button state:** disabled `unresolved > 0`, enabled at 0 (including all-skipped case), **disabled while `pendingPatchCount > 0`**. Owns: enable/disable logic + /confirm + PATCH race.
13. **localStorage resume:** GET 200 → rehydrates rows/groups; GET 404 → shows "session expired" message + clears storage + returns to file-drop (NOT silent blank grid). Owns: session expiry frontend half + resume path.
14. **PATCH wiring:** edit cell → correct `{row_id, field, value, patch_id}` payload → response merged into table state. Owns: the integration seam where most real bugs live.
15. **Stale PATCH response race:** MSW + two `deferred()` promises, fire PATCH A (patch_id=1) then PATCH B (patch_id=2), resolve B first then A, assert A's stale response is dropped and B's state wins. Owns: cross-row response ordering bug.

### E2E — Playwright (2 tests)

Playwright setup is net-new for this feature (SpenDrop has no existing E2E suite). Minimum viable: 2 smoke paths in a new `web/e2e/` directory.

16. **Starbucks happy path:** upload 20-row `.xlsx` with identical `Starbucks $5.00` rows, verify 1 collision group of 20, edit row 1 date, assert row 1 flips clean and group shrinks to 19, Tab through remaining rows incrementing dates, verify "Import 20" enables, click, assert 20 distinct transactions in DB with clean descriptions (no " (N)" suffixes).
17. **F5-during-edit resume:** upload 20-row file, edit dates on rows 1-5, refresh browser, assert rows 1-5 still show edited dates, continue editing rows 6-20, import, verify final DB state. Owns: resume path integration (crosses frontend state + localStorage + GET session + revalidation).

### Manual acceptance

Before merge: run the user's actual "20 Starbucks on Jan 7" Excel file end-to-end. Verify transactions page shows 20 rows with clean descriptions, no suffixes. This is the whole reason we're building the feature — if manual flow feels wrong, the tests lied.

### Explicitly deferred (with reasoning)

| Deferred | Why |
|---|---|
| Large-grid perf (500+ row single-row-rerender) | Household import size is 50-200 rows; revisit when we see 1000+ |
| Optimistic rollback tests | We're not doing optimistic updates — wait for response before merging |
| Paste-into-cell, undo/redo | Not shipping these |
| Turkish locale hash handling | `strings.ToLower` locale quirk, documented as known limitation |
| EditableCell primitive unit tests (double-click, Esc, Enter, Tab) | shadcn Input + native focus/blur is library behavior, not our logic |
| `groupCollisions` extracted function unit test | Duplicates PATCH handler coverage; extracted only if refactoring genuinely earns it |
| Session 60min TTL simulated-clock test | Flaky, caught by code review — one-line constant change |
| `validateImportCell` extracted function unit test | Covered by handler tests; no extraction needed |
| Storybook / visual regression / multi-browser matrix | We ship one browser, no Storybook setup |

---

## Out of Scope

Things that are reasonable follow-ups but explicitly NOT part of 3.4b:

- **Two-zone layout** (collisions in triage panel above clean rows) — kept as Phase 2 alternative if flat-table layout feels cluttered after real use
- **Category fuzzy match with auto-fix** — PATCH returns `category_candidates[]`, but we don't auto-apply; user must accept
- **Bulk date shift** (e.g., "move all 20 rows forward 1 day") — users can Tab-burst this manually
- **Import audit rows** — noted in memory as a pre-existing gap (`handleImportConfirm` bypasses `TransactionStore` audit); fixing is a separate chore
- **Undo/redo** — no session history stack
- **Paste from clipboard into multiple cells** — single-cell editing only
- **Persistent sessions across server restart** — in-memory is the explicit tradeoff
- **Server-side locking during /confirm** — frontend button lockout is sufficient guard
- **Multi-tab conflict resolution** (two tabs editing the same import_id) — per-user slot cap already prevents this at upload; PATCH ownership check catches the edge case

## File Impact Summary

**Modified:**
- `internal/api/import_handlers.go` — add PATCH handler, GET handler, extend `predictDuplicateSkips` to emit `collision_groups`, tighten `/confirm` to reject unresolved collisions, bump TTL to 60min
- `internal/database/content_hash.go` — add `golang.org/x/text/unicode/norm` NFC normalization
- `internal/api/routes.go` — register new PATCH and GET routes
- `web/src/api/types.ts` — add `CollisionGroup`, `PatchRowRequest`, extend `ImportPreview`
- `web/src/pages/Settings.tsx` — `ImportPreviewStep` rewritten to use new editable table + localStorage resume
- `web/src/api/import.ts` — add `patchImportRow`, `getImportSession` clients

**New:**
- `web/src/components/ImportPreviewTable.tsx` — editable table component with cell-level editing
- `web/src/components/EditableCell.tsx` — cell wrapper handling double-click/Escape/Enter/Tab and PATCH wiring (no unit tests — tested via table integration)
- `web/src/hooks/useImportSession.ts` — localStorage resume + PATCH race prevention hook
- `internal/api/import_handlers_test.go` — new test cases (see Testing Strategy)
- `web/src/components/ImportPreviewTable.test.tsx` — new test file
- `web/e2e/import-collision.spec.ts` — Playwright tests (requires new `@playwright/test` dev dep + config)

**No changes:**
- `internal/database/queries.sql` — reuses existing `GetTransactionByContentHash`, `CreateTransaction`, soft-delete-aware reads
- `internal/stores/transaction_store.go` — unchanged
