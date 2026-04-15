# Import Collision Resolution UI — Design Spec

## Overview

Phase 3.4b extends the Settings → Import flow so users can resolve SHA-256 content-hash collisions by editing rows inline in the preview table. Gives users an explicit resolution path instead of today's silent-drop behavior.

**What happens today (verified):**
- User uploads an Excel file with 20 identical `Starbucks $5.00` rows on `2025-01-07`.
- Upload preview shows all 20 rows; category mapping runs if any categories are unmapped.
- On confirm, backend iterates rows and calls `GetTransactionByContentHash`. The lookup runs on the *in-flight transaction* (`qtx`), so the **first** row inserts, but rows 2–20 are silently skipped with `skipReasonDuplicate` because they collide with the row just inserted in the same batch.
- End result: **19 of 20 rows are silently lost.** User sees "1 inserted, 19 skipped (duplicate)" in the import result and no easy way to recover.

**Dead-code path being retired:** The backend also has a `force_add []int` field on `importConfirmRequest` plus a `resolveForceAddSuffix` helper that appends " (N)" to descriptions until a free hash is found. This path is **never reached from the current frontend** — `force_add`/`forceAdd` does not appear anywhere under `web/src/`. Phase 3.4b removes this code path entirely because (a) it's unreachable today, (b) the " (N)" suffix would break the user's "replace-all" description-normalization workflow on the transactions page, and (c) the new inline-edit flow supersedes the need for a non-interactive escape hatch.

**New behavior:** the preview table is editable. User edits the **date** on each row (or the description/amount), server re-hashes, collision clears, import enables. Rows the user wants to drop are marked `skip`. All-or-nothing commit: any unresolved non-skipped collision blocks `/confirm`.

**Design axiom:** the preview is the single cleanup surface. No post-import fixup, no " (N)" suffixes anywhere, no force-add escape hatch.

**Scope:** the edit-in-preview loop and its backend. Import insert logic, content-hash definition, upload parser, session store infrastructure, and the existing category mapping UI are pre-existing and mostly untouched.

---

## Data Flow

```
[1] User uploads .xlsx
       ↓
[2] Backend parses rows → assigns stable row_id to each → builds preview:
     - rows[] (each: row_id, fields, content_hash?, client_error?)
     - collision_groups[] (group_id, member row_ids, reason)
     - unmatched_categories[] (from existing category mapping flow)
     - import_id (32-char hex, keys in-memory session)
       ↓
[3] User maps any unmatched spreadsheet categories to DB categories
     (pre-existing UI — unchanged from current implementation)
       ↓
[4] Frontend caches import_id in localStorage, renders editable table
       ↓
[5] User double-clicks an editable cell (date / description / amount / skip)
       ↓
[6] Frontend fires PATCH /api/import/:import_id/rows/:row_id
       { field, value, patch_id }
       ↓
[7] Backend re-runs single-row validator → re-computes content_hash via
     the single path in content_hash.go → re-groups across the session →
     returns full response: { rows, collision_groups, patch_id }
       ↓
[8] Frontend drops stale responses (patch_id < latestPatchIdRef), merges
     fresh response into table state keyed by row_id, re-renders
       ↓
[9] When unresolved_non_skipped_collisions == 0 AND pendingPatchCount == 0:
     "Import N" button enables
       ↓
[10] User clicks Import → POST /api/import/:import_id/confirm
       ↓
[11] Backend re-checks collision_groups from session state; if any
      unresolved non-skipped collisions → 409 + full collision_groups;
      otherwise inserts all non-skipped rows via CreateTransaction,
      clears the session
```

**Critical invariant:** the preview's display state and the server's session state are two views on the same data, synced only through PATCH responses. Frontend never mutates row state locally; it always waits for the server's re-hash and re-grouping.

---

## Backend API Design

### Row identifier (`row_id`)

**Definition:** stable 0-based positional index into the upload row slice, assigned once at upload time by the preview builder. Does NOT renumber across PATCH calls. Survives re-hash and re-grouping passes. Unique within a single `import_id` session.

**Where it lives:** added as a new field on `importRow` struct at `internal/api/import_handlers.go:35`. The existing `predictedSkip.RowIndex` uses the same positional-index semantics, so `row_id` is named differently only to make the PATCH URL path explicit.

**Frontend key:** used as the React list key for the table, as the PATCH URL path parameter, and as the merge key when applying PATCH responses.

### New endpoint: `PATCH /api/import/:import_id/rows/:row_id`

**Request:**
```json
{
  "field": "date" | "description" | "amount" | "skip",
  "value": "2025-01-08",
  "patch_id": 42
}
```

**Note on editable fields:** `category` is **NOT** a PATCH-editable field. Categories are resolved once up-front via the pre-existing category-mapping UI (`web/src/pages/Settings.tsx` — `categoryMap` / `setCategoryMap`, auto-mapped on upload via `autoMapCategories`, manual override via UI). All rows in the session share the same category map, so a row's category is a deterministic function of its raw Excel category string and the user-approved mapping. Keeping category out of PATCH avoids ambiguity between "the Excel cell says `Groceries`" and "the user has resolved it to DB category `#5 Food`".

**Response (200 OK):**
```json
{
  "rows": [/* full current row list, keyed by row_id */],
  "collision_groups": [
    {
      "group_id": "g_abc123",
      "reason": "intra_file" | "db_match",
      "member_row_ids": [3, 7, 12],
      "db_match_id": 8421
    }
  ],
  "patch_id": 42
}
```

- `db_match_id` only present when `reason == "db_match"`.
- Response shape is always the full current snapshot, not a sparse diff. Simpler frontend merge, deterministic behavior under races, cheap for the session sizes we target (50–500 rows).

**Protocol contract on `patch_id`:** the backend MUST echo the exact `patch_id` from the request body in the response, unchanged. No transformation, no regeneration. Any violation is treated as a protocol bug; handler has a single assertion that the response `patch_id` matches the request `patch_id` before writing JSON.

**Error responses:**
- `400` — validation error (bad date format, NaN amount, empty description, empty amount). Body: `{ code, field, message }`. Session state unchanged.
- `403` — import_id owned by another user. Reuses existing ownership helper at `import_handlers.go:47-74`.
- `404` — session expired or import_id not found.

### Modified endpoint: `POST /api/import/:import_id/confirm`

Existing endpoint, now with stricter enforcement:

- On entry, re-checks `collision_groups` in the session (recomputed from current session rows, not cached).
- If any non-skipped row is still a member of any collision group → **409 Conflict** with the full `collision_groups` array in the body. **No partial insert.**
- Otherwise iterates non-skipped rows, inserts via `CreateTransaction` (existing path), writes content_hash, clears session.

**Removed:** the existing `force_add []int` field on `importConfirmRequest`. The `resolveForceAddSuffix` helper and its test `TestHandleImport_ForceAdd_AppendsSuffixAndInserts` are deleted as part of this feature. See **Removal of `force_add` path** in Edge Cases for details.

**Preserved:** the existing `category_map` / `default_category_id` fields on `importConfirmRequest` — category mapping runs unchanged.

### New endpoint: `GET /api/import/:import_id`

Resume endpoint for F5/tab-refresh. Returns the full preview response (`rows`, `collision_groups`, `import_id`, `unmatched_categories`) from in-memory session state. 404 if expired.

Frontend calls this on mount if `localStorage.spendrop_import_id` is set.

### Session storage

**Reuse existing `importStore sync.Map` at `internal/api/import_handlers.go:47-74`:**
- Bump TTL from 30min → **60min fixed** (not activity-based — avoids memory leak from idle tabs)
- Existing per-user slot cap and ownership check already guard against abuse
- Existing cleanup goroutine already reaps stale sessions
- **No new persistence layer.** Lost on server restart = re-upload. Correct tradeoff for a hobby tool.

**Concurrency shape:** `importStore` entries are accessed only by the HTTP handler goroutine currently processing a request for that import_id. The sync.Map guards lookup-by-key; concurrent access to a single entry's contents is avoided because (a) the frontend is a single tab per user session, (b) upload creates the entry, PATCH and confirm mutate it, and (c) the frontend serializes its own requests (sequence number prevents out-of-order responses but does not fire PATCHes concurrently — they queue on the React state lifecycle). A second tab hitting the same entry would race at the Go level; the per-user slot cap on upload plus the ownership check at PATCH/confirm time mean this can only happen if the user explicitly deep-links an import_id from one tab to another, which is not a supported flow.

### Validation + hashing reuse (critical)

**Single source of truth:** `internal/database/content_hash.go` `ComputeContentHash` is the ONLY hashing path. Both upload and PATCH go through it. No separate "edit-time hash" helper.

**Single validator function:** a shared per-field validator is used by both the upload preview builder and the PATCH handler. The difference is the error-wrapping mode:
- **Upload mode:** silent-zero on empty amount (existing behavior, see `parseImportAmount` at `import_handlers.go:991`), row still surfaces in preview
- **Edit mode:** hard 400 on any validation failure, session state unchanged

**Parser reuse:** `parseImportDate` (`import_handlers.go:1078`), `parseImportAmount` (`import_handlers.go:991`) — reused identically. No divergent edit-time parsers.

### Collision grouping (net-new computation)

The existing `predictDuplicateSkips` at `import_handlers.go:369-432` only detects DB matches (one-by-one `GetTransactionByContentHash` lookup). It does **not** currently detect intra-file collisions — that computation is implicit in the confirm-time `qtx`-scoped lookup, which fires after inserts begin.

**New function: `buildCollisionGroups(rows []importRow, dbMatches map[int]int64) []collisionGroup`** — in `import_handlers.go`, called by both the upload preview builder and the PATCH handler. Signature:
- Input: the current row list (with resolved category_id from the mapping step) + a map from row_id to matching DB transaction id (built via one-shot `GetTransactionByContentHash` loop).
- Output: a list of `collisionGroup` entries where each group is a set of row_ids sharing the same content hash. Groups of size 1 (unique rows) are not emitted.
- Reason: `intra_file` if the group has ≥2 rows from the session; `db_match` if any row in the group also matches a live DB transaction; both flags possible (reason becomes `db_match` to surface the more actionable issue).

Pure function of its inputs — no DB access inside (DB lookups happen in the caller via the existing `GetTransactionByContentHash` which already filters tombstones). Testable in isolation via the PATCH handler tests; no standalone unit test needed (see Testing Strategy).

### Upload response shape change

The existing preview response carries `predicted_skips []predictedSkip`. In 3.4b, **`predicted_skips` is removed** and replaced with `collision_groups`. The two represent the same underlying data (which rows collide), but `collision_groups` is the richer shape the frontend now consumes.

Existing tests that assert on `predicted_skips` shape (`TestHandleImport_…` in `internal/api/import_handlers_test.go`) are updated to assert on `collision_groups` instead. No dual-shape / backwards-compat layer — the frontend is updated in the same commit.

### Request sequence number

`patch_id` is a client-provided monotonic counter per import session. Backend echoes it unchanged in the response (see contract above). Frontend uses it to drop stale out-of-order responses (see Frontend § Race Prevention).

Backend has no logic on it beyond echo + assert-echo-matches-request. Zero server-side complexity.

---

## Frontend Design

### Layout: flat editable table (Option A)

Single table, collision rows sorted to the top with amber highlight. No separate "triage zone". All rows share the same shape — edit any editable cell inline, collision and clean rows behave identically.

**Columns:** `[warn-icon?] [Date] [Description] [Category] [Amount] [Skip]`

`Category` column is display-only (shows the resolved DB category from the category-mapping step). To re-map categories, users go back to the category-mapping section above the table. Keeping category read-only here avoids the "is the user editing the Excel string or the mapped DB category" ambiguity.

**Sort policy for MVP:** collision rows frozen at top, ordering stable after edits (a row that resolves stays in place, does NOT re-sort mid-flow). Avoids row-jumping during bulk Tab-burst editing. Re-sort happens only on new upload or explicit refresh.

### Cell editing

- **Double-click** to enter edit mode (matches spreadsheet muscle memory, matches Handsontable/Flatfile/OneSchema convention)
- Single-click only selects the row (preserved for keyboard navigation)
- **Escape** cancels edit, restores original value, no PATCH fired
- **Enter** commits edit, fires PATCH
- **Tab** commits edit, fires PATCH, moves focus to next editable cell right
- **Shift+Tab** same but moves left

### Component choices

All shadcn, dense classNames for table density:

| Field | Component | Dense classes |
|---|---|---|
| Date | `Input type="text"` | `h-8 px-2 py-1 text-sm focus-visible:ring-1 focus-visible:ring-offset-0 border-0 rounded-none` |
| Description | `Input` | same |
| Category | (plain text span) | read-only, not editable via PATCH |
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
- **Right:** `Import N` button — disabled when `unresolved_non_skipped_collisions > 0`, disabled while `pendingPatchCount > 0`, enabled when both are zero (including the all-skipped case)

### localStorage resume

**On file upload success:** `localStorage.setItem('spendrop_import_id', import_id)`

**On component mount:**
1. Read `localStorage.spendrop_import_id`
2. If present → `GET /api/import/:import_id`
3. On 200 → rehydrate rows, collision_groups, category mapping, render preview table
4. On 404 → show "Your previous import session expired — please re-upload" message, clear localStorage, return to file-drop state

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

Handles the "user Tabs from row 1 → row 2 faster than round-trip, response 1 arrives after response 2" case. Response 1's stale `collision_groups` would otherwise clobber response 2's merged state. Strict `<` is correct: the most-recently-issued patch has `patch_id == ref.current` at response time and must be applied.

**Why not AbortController:** abort-per-row doesn't protect cross-row races. Sequence number is simpler and correct.

### Confirm button lockout during pending PATCH

`pendingPatchCount` state increments before fetch, decrements in `finally`. Import button disables while `pendingPatchCount > 0`. Prevents the "/confirm runs while PATCH in flight, sees stale session" race at the frontend layer — no backend locking required.

**Soft-edge UX caveat:** if a PATCH returns 400 (validation error), `pendingPatchCount` still decrements and the button re-enables. The row's state is whatever the last successful PATCH left it at. If the user then clicks Import expecting their failed edit to have taken effect, `/confirm` sees the stale (pre-edit) session state and may succeed or 409 depending on that state. This is acceptable: the 400 response shows an inline error on the failed cell, so a user who clicks Import past that error is doing so deliberately. Tests explicitly assert the error-stays-visible behavior (see Testing Strategy #11).

### API contract additions

`web/src/api/types.ts` — extend `ImportPreview`:

```ts
interface ImportPreview {
  import_id: string;
  rows: ImportRow[];
  collision_groups: CollisionGroup[];  // NEW — replaces predicted_skips
  unmatched_categories: string[];       // pre-existing, unchanged
  // ... existing fields
}

interface CollisionGroup {  // NEW
  group_id: string;
  reason: 'intra_file' | 'db_match';
  member_row_ids: number[];
  db_match_id?: number;
}

interface PatchRowRequest {  // NEW
  field: 'date' | 'description' | 'amount' | 'skip';
  value: string | boolean;
  patch_id: number;
}
```

---

## Edge Cases & Correctness Invariants

### Hash normalization parity (highest-risk trap)

**Rule:** upload-time hash and PATCH-time re-hash MUST use the same code path in `content_hash.go` `ComputeContentHash`. Any divergence means edits fail to clear collisions despite visually identical values.

**Guard:** one dedicated test that takes a row, hashes it, applies whitespace/case variations to description and category (via PATCH for description, via full upload re-parse for category), re-hashes, asserts equality.

**Note on Unicode normalization:** earlier drafts of this spec included a `norm.NFC.String` addition to `ComputeContentHash` to catch Mac-NFD vs Windows-NFC mismatches. **This is deferred** — adding NFC to the hash silently invalidates every content_hash already stored in the DB (any pre-existing row whose description contains non-NFC codepoints would re-hash differently after the change, and the duplicate detection would miss real duplicates). Fixing it cleanly requires a one-shot backfill migration to re-hash all live transactions. The user base (single household, primarily English descriptions, Windows source) has negligible NFC/NFD exposure, so the practical impact is zero and the feature work is deferred. See **Out of Scope**.

### Tombstoned rows in DB-match detection

**Rule:** Both the initial upload preview AND the PATCH re-check must filter `t.deleted_at IS NULL`. Reuse the existing `GetTransactionByContentHash` query at `internal/database/queries.sql:182-187` — do NOT write a fresh SELECT in the handler.

**Guards (two tests):**
- Upload preview: `*_HidesTombstoned` seeds a live row + a tombstoned row (`amount=999` sentinel) with the same content hash as an uploaded row, asserts the import row is NOT flagged as a DB collision at upload time (test #8).
- PATCH re-check: same seed pattern, but the import row starts clean and the user PATCHes its date to a value that would match the tombstoned row — assert PATCH response does NOT flag it (test #8 extension).

### Removal of `force_add` path

**Change:** delete `force_add` from `importConfirmRequest`, delete `resolveForceAddSuffix` and its helpers, delete `TestHandleImport_ForceAdd_AppendsSuffixAndInserts` and related force-add test cases.

**Why safe:**
1. `force_add` / `forceAdd` is not referenced anywhere under `web/src/` (verified via grep). The current frontend never sends this field.
2. The code path is reachable only by hand-crafting a JSON confirm body — not a user-facing surface.
3. The " (N)" suffix behavior it implements actively breaks the user's description-normalization workflow (the whole reason for this feature).
4. The new inline-edit flow supersedes the need for a non-interactive escape hatch: users who want to keep "duplicate" rows simply edit a field to make them unique, or mark them `skip` to drop them.

**Test update:** `import_handlers_test.go` tests that assert on force-add behavior are removed. Existing tests that use `force_add: nil` or `force_add: []` continue to work (the field is gone, not repurposed).

### Validation field rules

| Field | Rule | Error code |
|---|---|---|
| `date` | Non-empty, parseable via `parseImportDate`, in `[1900, 2100]` | `INVALID_DATE` |
| `description` | Non-empty after trim, length ≤ `limits.MaxDescriptionLength` (500, defined at `internal/api/limits.go:61`) | `INVALID_DESCRIPTION` |
| `amount` | Parseable via `parseImportAmount`, not NaN/Inf. **Upload mode: empty → 0 (existing silent behavior). Edit mode: empty → 400 `INVALID_AMOUNT`.** | `INVALID_AMOUNT` |
| `skip` | Boolean, no validation | — |

**Error precedence ranking** (when multiple errors could apply): field-presence > field-shape. The first failing rule wins; downstream rules are not evaluated. Deterministic user experience.

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

See Frontend § Race Prevention. `patch_id` sequence number guards against out-of-order responses. Backend contract: echo `patch_id` unchanged.

### Confirm + pending PATCH race

See Frontend § Confirm button lockout. Import button disabled while any PATCH in flight. No backend locking.

---

## Testing Strategy

**Target: 15 automated tests + 2 manual scripts.** Core bar: every bug owned by exactly one test, every test owns at least one bug. Nothing else earns its keep.

### Backend — Go (9 tests)

All in `internal/api/import_handlers_test.go`, following existing pattern (direct handler invocation via `httptest.NewRecorder()`, see `import_handlers_test.go:167-171`).

1. **PATCH happy path (group of 3):** upload 3 identical rows, PATCH row 1's date, assert row 1 clean + remaining 2 still grouped together. Owns: core regrouping + stale group_id in untouched rows.
2. **PATCH re-collision:** PATCH moves row from group A → group B; if row had `skip=true`, skip flag cleared on un-collide. Owns: stateful regrouping, skip-persistence rule.
3. **PATCH 404 on expired session.** Owns: session expiry backend half.
4. **PATCH content-hash parity (table-driven, 3 cases):**
   - Whitespace: `" Starbucks "` → `"Starbucks"` → same hash via PATCH re-hash
   - Case: `"STARBUCKS"` → `"Starbucks"` → same hash via PATCH re-hash
   - Baseline: different strings → different hashes
   - **Each case covers both description and category normalization paths.**
   Owns: whitespace/case hash-mismatch bug class.
5. **Confirm 409 with full `collision_groups`** when any unresolved non-skipped collision remains. Owns: partial-import rejection invariant.
6. **Confirm happy path:** all resolved → `CreateTransaction` called per row, `content_hash` persisted. Owns: end-to-end wiring.
7. **Confirm skipped rows excluded** from inserts. Owns: skip ≠ unresolved distinction.
8. **`*_HidesTombstoned` at both upload and PATCH re-check paths:** seed live row + tombstoned row (`amount=999` sentinel) with same content hash. Part A — upload preview: assert matching import row is NOT flagged as DB collision. Part B — PATCH re-check: user edits a clean row's date into the tombstoned row's hash space; assert PATCH response still reports clean. Owns: soft-delete leak at both read paths.
9. **Upload preview flags intra-file collision groups** via the new `buildCollisionGroups` pass (baseline — the initial grouping must work before any PATCH can be tested). Also includes an **amount-mode parity case**: upload with empty amount cell parses to 0 (existing behavior), PATCH clearing the amount returns 400 `INVALID_AMOUNT`.

### Frontend — Vitest (6 tests)

All in `web/src/components/ImportPreviewTable.test.tsx`, using Vitest + happy-dom + `@testing-library/react` + `user-event`. Config at `web/vite.config.ts:19-28`, setup at `web/src/test/setup.ts`. Pattern reference: `web/src/components/TransactionEntryRow.test.tsx`.

10. **ImportPreviewTable renders:** collision rows get amber + warning icon, clean rows don't. Owns: initial render.
11. **Stale-style regression:** row flips collision → clean on PATCH response, amber background removed on next render. Owns: importcsv #16 bug class — single most important frontend test.
12. **Import button state:** disabled `unresolved > 0`, enabled at 0 (including all-skipped case), **disabled while `pendingPatchCount > 0`**. Owns: enable/disable logic + /confirm + PATCH race.
13. **localStorage resume:** GET 200 → rehydrates rows/groups; GET 404 → shows "session expired" message + clears storage + returns to file-drop (NOT silent blank grid). Owns: session expiry frontend half + resume path.
14. **PATCH wiring:** edit cell → correct `{row_id, field, value, patch_id}` payload → response merged into table state. Owns: the integration seam where most real bugs live.
15. **Stale PATCH response race:** MSW + two `deferred()` promises, fire PATCH A (patch_id=1) then PATCH B (patch_id=2), resolve B first then A, assert A's stale response is dropped and B's state wins. Owns: cross-row response ordering bug.

### Manual acceptance scripts (2)

SpenDrop has no Playwright harness today, and setting one up (vite preview server, test DB, auth seeding, CI integration) is several hours of config work — out of scope for 3.4b. These two scripts are **manual checklists** run before merge:

16. **Starbucks happy path (manual):** upload a 20-row `.xlsx` with identical `Starbucks $5.00` rows, verify 1 collision group of 20 is displayed, edit row 1 date, verify row 1 flips clean and group shrinks to 19, Tab through the remaining rows incrementing dates, verify "Import 20" enables, click, verify 20 distinct transactions appear on the transactions page with clean descriptions (no " (N)" suffixes anywhere).
17. **F5-during-edit resume (manual):** upload 20-row file, edit dates on rows 1-5, refresh browser (F5), verify rows 1-5 still show edited dates + collision state is correct, continue editing rows 6-20, import, verify final DB state.

Automated Playwright coverage for these scenarios is listed in **Out of Scope** as a future chore.

### Explicitly deferred (with reasoning)

| Deferred | Why |
|---|---|
| Large-grid perf (500+ row single-row-rerender) | Household import size is 50-200 rows; revisit when we see 1000+ |
| Optimistic rollback tests | We're not doing optimistic updates — wait for response before merging |
| Paste-into-cell, undo/redo | Not shipping these |
| Turkish locale hash handling | `strings.ToLower` locale quirk, documented as known limitation |
| Unicode NFC normalization in hash | Deferred — invalidates existing DB hashes, requires backfill migration, low user exposure |
| EditableCell primitive unit tests (double-click, Esc, Enter, Tab) | shadcn Input + native focus/blur is library behavior, not our logic |
| `buildCollisionGroups` / `validateImportCell` extracted function unit tests | Duplicate PATCH handler coverage |
| Session 60min TTL simulated-clock test | Flaky, caught by code review — one-line constant change |
| Playwright E2E harness + tests 16/17 automated | Several hours of harness setup, out of scope for 3.4b |
| Storybook / visual regression / multi-browser matrix | We ship one browser, no Storybook setup |

---

## Out of Scope

Things that are reasonable follow-ups but explicitly NOT part of 3.4b:

- **Two-zone layout** (collisions in triage panel above clean rows) — kept as Phase 2 alternative if flat-table layout feels cluttered after real use
- **Inline category editing** — category mapping stays as a separate pre-step above the editable table; users re-map there, not inline
- **Category fuzzy match with auto-fix** — out of scope; the existing `autoMapCategories` helper handles the happy case, manual mapping handles the rest
- **Unicode NFC normalization in `content_hash.go`** — deferred because adding it silently invalidates existing DB hashes; requires a one-shot backfill migration. Revisit if users report NFC/NFD collision misses.
- **Bulk date shift** (e.g., "move all 20 rows forward 1 day") — users can Tab-burst this manually
- **Import audit rows** — noted in memory as a pre-existing gap (`handleImportConfirm` bypasses `TransactionStore` audit); fixing is a separate chore
- **Undo/redo** — no session history stack
- **Paste from clipboard into multiple cells** — single-cell editing only
- **Persistent sessions across server restart** — in-memory is the explicit tradeoff
- **Server-side locking during /confirm** — frontend button lockout is sufficient guard
- **Multi-tab conflict resolution** (two tabs editing the same import_id) — per-user slot cap already prevents this at upload; PATCH ownership check catches the edge case
- **Playwright E2E harness + automated versions of tests 16/17** — separate chore; 3.4b uses manual acceptance scripts
- **`force_add` / `resolveForceAddSuffix` / " (N)" suffix code path** — **removed** as part of this feature (unreachable from current frontend, breaks user workflow)

---

## File Impact Summary

**Modified:**
- `internal/api/import_handlers.go` — add PATCH handler + GET handler, assign `row_id` to each row at upload time, add new `buildCollisionGroups` function, wire it into upload preview + PATCH re-check paths, tighten `/confirm` to reject unresolved collisions, bump TTL to 60min, **remove `resolveForceAddSuffix` and all force-add logic**
- `internal/api/router.go` — register new PATCH (`/api/import/{importID}/rows/{rowID}`) and GET (`/api/import/{importID}`) routes
- `internal/api/import_handlers_test.go` — remove force-add tests, update `predicted_skips` assertions to `collision_groups`, add new test cases per Testing Strategy
- `web/src/api/types.ts` — add `CollisionGroup`, `PatchRowRequest`, extend `ImportPreview` with `collision_groups`, remove `predicted_skips`
- `web/src/pages/Settings.tsx` — `ImportPreviewStep` rewritten: existing category-mapping UI preserved above, new editable `ImportPreviewTable` below, localStorage resume on mount
- `web/src/api/import.ts` — add `patchImportRow`, `getImportSession` clients

**New:**
- `web/src/components/ImportPreviewTable.tsx` — editable table component with cell-level editing
- `web/src/components/EditableCell.tsx` — cell wrapper handling double-click/Escape/Enter/Tab and PATCH wiring (no unit tests — tested via table integration)
- `web/src/hooks/useImportSession.ts` — localStorage resume + PATCH race prevention hook
- `web/src/components/ImportPreviewTable.test.tsx` — new test file

**No changes:**
- `internal/database/content_hash.go` — reused unchanged (Unicode NFC deferred)
- `internal/database/queries.sql` — reuses existing `GetTransactionByContentHash`, `CreateTransaction`, soft-delete-aware reads
- `internal/stores/transaction_store.go` — unchanged
