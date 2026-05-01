# Transactions Bulk-Edit Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Bulk-Edit dialog to the Transactions page that lets a user edit Date, Description, Category, and Tags across N selected rows in one round-trip — with two backend endpoints (`batch-update` for ID lists, `update-by-filter` for atomic all-matching-filter operations) that mirror the existing `batch-delete` / `delete-by-filter` precedent.

**Architecture:** Lidarr-derived `'noChange'` sentinel idiom for "leave alone" defaults; per-row audit for ID-list mode (preserves single-update invariant), summary audit for filter mode (matches existing bulk-delete + bulk-rename precedent); mutually-exclusive SQL paths in the filter handler so the checkpoint reverification hook fires exactly once per request; selection auto-prunes after refetch by intersecting with refetched IDs.

**Tech Stack:** Go (chi router) + SQLite (sqlc) + React/TypeScript + shadcn/ui (Dialog, AlertDialog, Select, Checkbox, RadioGroup) + RHF + zod + sonner toasts.

**Spec:** `docs/superpowers/specs/2026-05-01-transactions-bulk-edit-design.md` — read this end-to-end before starting Task 1. This plan does not duplicate the spec's design rationale; it tells you *what to build* and *how to test it*.

---

## Architecture & File Structure

### Backend (Go)

**Create:**
- `internal/database/store_transaction_test.go` — store-level tests for the new `UpdateTx` method.
- (Errors `ErrTombstoned`, `ErrNotOwned`, `ErrNotFound` go into existing `internal/database/store.go` near `ErrTokenNotFound`.)

**Modify:**
- `internal/api/limits.go` — add `MaxBatchUpdateIDs = 500` next to `MaxBatchDeleteIDs`.
- `internal/api/transaction_handlers.go` — extract per-field validators out of `validateTransactionRequest`; add `summarizePatch` + `buildUpdatePatch` helpers; add `handleBatchUpdateTransactions` and `handleUpdateTransactionsByFilter` handlers.
- `internal/api/transaction_handlers_test.go` — happy-path + skip + error-path tests for both handlers.
- `internal/api/transaction_audit_test.go` — audit invariant tests.
- `internal/api/router.go` — wire the two new routes.
- `internal/database/store.go` — add `UpdateTx` method + sentinel errors.

### Frontend (React/TypeScript)

**Create:**
- `web/src/components/ui/radio-group.tsx` — installed via shadcn CLI, do NOT hand-write.
- `web/src/pages/Transactions/BulkEditDialog.tsx` — the main dialog.
- `web/src/pages/Transactions/BulkEditConfirmDialog.tsx` — the all-matching-scope confirm AlertDialog.
- `web/src/pages/Transactions/computePatch.ts` — pure helper that walks form values and emits the patch object (or empty object).
- `web/src/pages/Transactions/Transactions.bulkEdit.test.tsx` — frontend tests.

**Modify:**
- `web/src/api/types.ts` — add `BulkUpdateRequest`, `BulkUpdateByFilterRequest`, `BulkUpdateResponse`, `Patch` types.
- `web/src/hooks/useTransactions.ts` — add `bulkUpdate`, `bulkUpdateByFilter`; refactor `fetchTransactions` to expose a Promise-returning `fetchTransactionsAsync` companion; export `RefetchAfterMutationError`.
- `web/src/pages/Transactions.tsx` — add the "Edit (N)" button + dialog dispatch + prune-on-refetch + pluralized toast.
- `web/src/api/client.ts` — only if `RefetchAfterMutationError` belongs there (decision deferred to Task 7; see that task's research step).
- `README.md` + `docs/DESIGN_GUIDE.md` — document the new feature + UX patterns.

### Test layout

- Go store tests: `internal/database/store_transaction_test.go` (new file).
- Go handler tests: extend `internal/api/transaction_handlers_test.go`.
- Go audit tests: extend `internal/api/transaction_audit_test.go`.
- Frontend tests: `web/src/pages/Transactions/Transactions.bulkEdit.test.tsx` (new file).

---

## Branching + Commits

The branch `feat/transactions-bulk-edit` already exists (created during spec writing). Each task ends with at least one conventional-commit per CLAUDE.md (`feat(api):` / `feat(ui):` / `test(...):` / `docs(...):` / `chore(deps):`). Never amend; always make a new commit.

### Conventions reminders

- Run Go tests via the pre-baked Docker image:
  ```
  docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
    -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
    go test ./internal/api/... ./internal/database/...
  ```
- Run frontend tests via Docker too:
  ```
  docker run --rm -v D:/claude/SpenDrop/web:/src -w //src node:20-alpine \
    node ./node_modules/vitest/vitest.mjs run <path>
  ```
- Run TypeScript check:
  ```
  docker run --rm -v D:/claude/SpenDrop/web:/src -w //src node:20-alpine \
    sh -c "node ./node_modules/typescript/bin/tsc --noEmit -p tsconfig.json"
  ```
- Use the `data-correctness-reviewer` agent before each commit that touches `internal/api/transaction_handlers.go`, `internal/database/queries.sql`, or `internal/database/store.go` — per CLAUDE.md.

---

## Chunk 1: Backend foundation (Tasks 1–2)

### Task 1: Constants, per-field validators, and `summarizePatch` helper

**Files:**
- Modify: `internal/api/limits.go` (add `MaxBatchUpdateIDs`)
- Modify: `internal/api/transaction_handlers.go` (extract validators, add `summarizePatch` + `buildUpdatePatch`)
- Test: `internal/api/transaction_handlers_test.go` (extend with new test functions)

**Goal:** stand up the small, pure helpers (constants, validators, patch builder, audit summary formatter) before any handler code touches them. This is the most-reused layer; getting it wrong forces rework in every later task.

- [ ] **Step 1.1: Add the `MaxBatchUpdateIDs` constant**

Read `internal/api/limits.go` to find the existing `MaxBatchDeleteIDs`:

```go
MaxBatchDeleteIDs = 500
// MaxBatchRestoreIDs is pinned to MaxBatchDeleteIDs ...
MaxBatchRestoreIDs = MaxBatchDeleteIDs
```

Add `MaxBatchUpdateIDs` immediately after, also pinned to `MaxBatchDeleteIDs` to keep the three caps in lockstep:

```go
// MaxBatchUpdateIDs caps the size of the ID list accepted by
// /api/transactions/batch-update. Pinned to MaxBatchDeleteIDs because
// every bulk mutation should share the same per-request blast radius.
MaxBatchUpdateIDs = MaxBatchDeleteIDs
```

- [ ] **Step 1.2: Run the build to confirm constant compiles**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go build ./internal/api/...
```

Expected: no output (success).

- [ ] **Step 1.3: Write the failing test for `summarizePatch`**

Append to `internal/api/transaction_handlers_test.go`:

```go
func TestSummarizePatch_FormatsAllFieldKindsInFixedOrder(t *testing.T) {
    p := database.UpdatePatch{
        Date:        ptrString("2026-04-30"),
        Description: ptrString(`ATM card #4839 cash`),
        CategoryID:  ptrInt64(5),
        Tags:        ptrString("tax,receipt"),
        TagsMode:    ptrString("add"),
    }
    got := summarizePatch(p)
    want := `date=2026-04-30; description="ATM card #4839 cash"; category_id=5; tags=tax,receipt(add)`
    if got != want {
        t.Errorf("summarizePatch() = %q, want %q", got, want)
    }
}

func TestSummarizePatch_OmitsAbsentFields(t *testing.T) {
    p := database.UpdatePatch{CategoryID: ptrInt64(5)}
    if got := summarizePatch(p); got != "category_id=5" {
        t.Errorf("summarizePatch() = %q, want %q", got, "category_id=5")
    }
}

func TestSummarizePatch_EscapesQuotesInDescription(t *testing.T) {
    p := database.UpdatePatch{Description: ptrString(`he said "hi"`)}
    got := summarizePatch(p)
    want := `description="he said \"hi\""`
    if got != want {
        t.Errorf("summarizePatch() = %q, want %q", got, want)
    }
}

func TestSummarizePatch_GrepBoundaryRegression_EmbeddedQuoteSemicolon(t *testing.T) {
    p := database.UpdatePatch{Description: ptrString(`risky"; DROP TABLE`)}
    got := summarizePatch(p)
    want := `description="risky\"; DROP TABLE"`
    if got != want {
        t.Errorf("summarizePatch() = %q, want %q", got, want)
    }
}

func TestSummarizePatch_TruncatesAt1024Chars(t *testing.T) {
    long := strings.Repeat("x", 1100)
    p := database.UpdatePatch{Description: ptrString(long)}
    got := summarizePatch(p)
    if !strings.HasSuffix(got, "…(truncated)") {
        t.Errorf("expected truncation suffix, got tail %q", got[len(got)-20:])
    }
    if len(got) > 1024 {
        t.Errorf("expected len <= 1024, got %d", len(got))
    }
}

// ptrString and ptrInt64 are tiny helpers — add them to the test file too if not present.
func ptrString(s string) *string { return &s }
func ptrInt64(n int64) *int64    { return &n }
```

- [ ] **Step 1.4: Run the failing test**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test -run TestSummarizePatch ./internal/api/...
```

Expected: FAIL with `undefined: summarizePatch` and `undefined: UpdatePatch`.

- [ ] **Step 1.5: Implement `UpdatePatch` struct + `summarizePatch`**

`UpdatePatch` lives in `internal/database/store.go` (NOT in `internal/api`) — it's a data-shape consumed by the store, and putting it in `internal/api` would force the store to import the api package (cycle). The summary helpers stay in `internal/api`. The api package imports `database.UpdatePatch` and consumes it directly.

Add to `internal/database/store.go` (near `BulkAuditSummary` around line ~281):

```go
// UpdatePatch is the bulk-edit patch shape consumed by TransactionStore.UpdateTx.
// Pointer fields mean "unset = do not touch" (matching JSON-omitempty
// semantics on the wire). Both batch-update and update-by-filter use this
// struct; the api-layer patchRequest is the wire-format shape that
// buildUpdatePatch lifts into UpdatePatch.
type UpdatePatch struct {
    Date        *string
    Description *string
    CategoryID  *int64
    Tags        *string
    TagsMode    *string  // required iff Tags != nil ("add" / "remove" / "replace")
}

// IsEmpty reports whether the patch carries no field changes. Used as the
// final "is the request actually doing anything?" gate before we dispatch.
func (p UpdatePatch) IsEmpty() bool {
    return p.Date == nil && p.Description == nil && p.CategoryID == nil && p.Tags == nil
}
```

Then add to `internal/api/transaction_handlers.go`:

```go

const summarizePatchMaxLen = 1024

// summarizePatch renders the patch as a stable, human-readable string for
// audit-table greppability. Stable format pinned by the spec — see
// docs/superpowers/specs/2026-05-01-transactions-bulk-edit-design.md §5.4.
//
// Field order is fixed: date, description, category_id, tags. Embedded
// quotes in description are escaped \"; semicolons left raw. Output is
// capped at summarizePatchMaxLen so pathological input cannot bloat the
// audit row's `before_json` envelope.
func summarizePatch(p database.UpdatePatch) string {
    var parts []string
    if p.Date != nil {
        parts = append(parts, "date="+*p.Date)
    }
    if p.Description != nil {
        escaped := strings.ReplaceAll(*p.Description, `"`, `\"`)
        parts = append(parts, fmt.Sprintf(`description="%s"`, escaped))
    }
    if p.CategoryID != nil {
        parts = append(parts, fmt.Sprintf("category_id=%d", *p.CategoryID))
    }
    if p.Tags != nil {
        mode := ""
        if p.TagsMode != nil {
            mode = *p.TagsMode
        }
        parts = append(parts, fmt.Sprintf("tags=%s(%s)", *p.Tags, mode))
    }
    out := strings.Join(parts, "; ")
    if len(out) > summarizePatchMaxLen {
        out = out[:summarizePatchMaxLen-len("…(truncated)")] + "…(truncated)"
    }
    return out
}
```

Make sure `strings` and `fmt` are in the imports.

- [ ] **Step 1.6: Run the test to verify it passes**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test -run TestSummarizePatch ./internal/api/...
```

Expected: PASS for all five subtests.

- [ ] **Step 1.7: Write the failing test for the per-field validators**

Append to `transaction_handlers_test.go`:

```go
func TestValidateDate_AcceptsCanonicalForm(t *testing.T) {
    got, err := validateDate("2026-04-30")
    if err != nil { t.Fatalf("unexpected error: %v", err) }
    if got != "2026-04-30" {
        t.Errorf("validateDate(\"2026-04-30\") = %q, want \"2026-04-30\"", got)
    }
}

func TestValidateDate_RejectsBadFormat(t *testing.T) {
    cases := []string{"2026/04/30", "30-04-2026", "April 30 2026", "", "2026-13-40"}
    for _, c := range cases {
        if _, err := validateDate(c); err == nil {
            t.Errorf("validateDate(%q) returned nil, want error", c)
        }
    }
}

func TestValidateDescription_TrimsAndCaps(t *testing.T) {
    got, err := validateDescription("  hello  ")
    if err != nil { t.Fatalf("unexpected error: %v", err) }
    if got != "hello" {
        t.Errorf("validateDescription trim = %q, want \"hello\"", got)
    }
    long := strings.Repeat("x", MaxDescriptionLength+1)
    if _, err := validateDescription(long); err == nil {
        t.Errorf("validateDescription oversized: nil, want error")
    }
}

func TestValidateCategoryID_PositiveOnly(t *testing.T) {
    if _, err := validateCategoryID(5); err != nil {
        t.Errorf("validateCategoryID(5): %v", err)
    }
    for _, n := range []int64{0, -1, -100} {
        if _, err := validateCategoryID(n); err == nil {
            t.Errorf("validateCategoryID(%d): nil, want error", n)
        }
    }
}

func TestValidateTagsField_LengthCheckOnly(t *testing.T) {
    if _, err := validateTagsField("Tax, receipt"); err != nil {
        t.Errorf("validateTagsField: %v", err)
    }
    long := strings.Repeat("a,", MaxTagsLength)  // length > MaxTagsLength
    if _, err := validateTagsField(long); err == nil {
        t.Errorf("validateTagsField oversized: nil, want error")
    }
}
```

- [ ] **Step 1.8: Run tests and watch them fail**

Expected FAILs: `undefined: validateDate`, `undefined: validateDescription`, etc.

- [ ] **Step 1.9: Extract the validators**

Inspect `validateTransactionRequest` at `internal/api/transaction_handlers.go:629`. It currently does all-or-nothing field validation in one function. Decompose by extracting per-field validators that the bulk path can call selectively, but **leave the existing function calling them** so the single-row update path continues to work without behavior change.

Add to `transaction_handlers.go`:

```go
// validateDate parses YYYY-MM-DD and returns the canonical form. Mirrors
// validateTransactionRequest:633-635 but isolated so bulk-edit can call it
// without forcing all-fields validation.
func validateDate(s string) (string, error) {
    t, err := time.Parse("2006-01-02", s)
    if err != nil {
        return "", fmt.Errorf("date must be in YYYY-MM-DD format")
    }
    return t.Format("2006-01-02"), nil
}

// validateDescription trims, then length-checks against MaxDescriptionLength.
// Mirrors validateTransactionRequest:636-641.
func validateDescription(s string) (string, error) {
    s = strings.TrimSpace(s)
    if s == "" {
        return "", fmt.Errorf("description must not be empty")
    }
    if len(s) > MaxDescriptionLength {
        return "", fmt.Errorf("description must be %d characters or less", MaxDescriptionLength)
    }
    return s, nil
}

// validateCategoryID matches the existing single-row laxity in
// validateTransactionRequest:648-649 — id > 0 only. The is_active /
// existence check is deliberately NOT added here; doing so on bulk-edit
// alone would create a divergence with the single-row endpoint. See spec
// §5.5b for the deferred decision.
func validateCategoryID(id int64) (int64, error) {
    if id <= 0 {
        return 0, fmt.Errorf("category_id is required")
    }
    return id, nil
}

// validateTagsField length-checks against MaxTagsLength. Tags storage is
// verbatim — no lowercase, no canonical normalization. See spec §3.3.
func validateTagsField(s string) (string, error) {
    if len(s) > MaxTagsLength {
        return "", fmt.Errorf("tags must be %d characters or less", MaxTagsLength)
    }
    return s, nil
}
```

Refactor `validateTransactionRequest` to call these helpers (without changing its observable behavior on the single-row path):

```go
func validateTransactionRequest(req transactionRequest) error {
    if req.Date == "" {
        return fmt.Errorf("date is required")
    }
    if _, err := validateDate(req.Date); err != nil {
        return err
    }
    if req.Description == "" {
        return fmt.Errorf("description is required")
    }
    if _, err := validateDescription(req.Description); err != nil {
        return err
    }
    if _, err := validateTagsField(req.Tags); err != nil {
        return err
    }
    if len(req.Notes) > MaxNotesLength {
        return fmt.Errorf("notes must be %d characters or less", MaxNotesLength)
    }
    if _, err := validateCategoryID(req.CategoryID); err != nil {
        return err
    }
    if req.OriginalCurrency == "" && req.Amount <= 0 {
        return fmt.Errorf("amount must be positive")
    }
    if math.IsInf(req.Amount, 0) || math.IsNaN(req.Amount) || req.Amount > MaxTransactionAmount {
        return fmt.Errorf("amount exceeds maximum allowed value")
    }
    if req.OriginalAmount != nil && (math.IsInf(*req.OriginalAmount, 0) || math.IsNaN(*req.OriginalAmount) || *req.OriginalAmount > MaxTransactionAmount) {
        return fmt.Errorf("original_amount exceeds maximum allowed value")
    }
    return nil
}
```

- [ ] **Step 1.10: Run all tests including the existing ones**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test ./internal/api/...
```

Expected: ALL pass — both new validator tests and the existing `validateTransactionRequest` callers.

- [ ] **Step 1.11: Write the failing test for `buildUpdatePatch`**

Append to `transaction_handlers_test.go`:

```go
// patchRequest is the wire shape of bulk-edit requests. Tag mode is
// required iff Tags is set; the validator enforces this.
type patchRequest struct {
    Date        *string `json:"date,omitempty"`
    Description *string `json:"description,omitempty"`
    CategoryID  *int64  `json:"category_id,omitempty"`
    Tags        *string `json:"tags,omitempty"`
}

func TestBuildUpdatePatch_HappyPath(t *testing.T) {
    in := patchRequest{
        Date:       ptrString("2026-04-30"),
        CategoryID: ptrInt64(5),
        Tags:       ptrString("tax,receipt"),
    }
    p, err := buildUpdatePatch(in, ptrString("add"))
    if err != nil { t.Fatalf("buildUpdatePatch: %v", err) }
    if p.Date == nil || *p.Date != "2026-04-30" {
        t.Errorf("Date: got %v, want pointer to \"2026-04-30\"", p.Date)
    }
    if p.CategoryID == nil || *p.CategoryID != 5 {
        t.Errorf("CategoryID: got %v, want 5", p.CategoryID)
    }
    if p.Tags == nil || *p.Tags != "tax,receipt" {
        t.Errorf("Tags: got %v, want \"tax,receipt\"", p.Tags)
    }
    if p.TagsMode == nil || *p.TagsMode != "add" {
        t.Errorf("TagsMode: got %v, want \"add\"", p.TagsMode)
    }
}

func TestBuildUpdatePatch_RejectsTagsWithoutMode(t *testing.T) {
    in := patchRequest{Tags: ptrString("tax")}
    _, err := buildUpdatePatch(in, nil)
    if err == nil {
        t.Errorf("expected error when tags set but mode missing")
    }
}

func TestBuildUpdatePatch_RejectsModeWithoutTags(t *testing.T) {
    in := patchRequest{CategoryID: ptrInt64(5)}
    _, err := buildUpdatePatch(in, ptrString("add"))
    if err == nil {
        t.Errorf("expected error when mode set but tags missing")
    }
}

func TestBuildUpdatePatch_RejectsInvalidMode(t *testing.T) {
    in := patchRequest{Tags: ptrString("tax")}
    for _, m := range []string{"set", "merge", "ADD", "", "remove "} {
        _, err := buildUpdatePatch(in, ptrString(m))
        if err == nil {
            t.Errorf("buildUpdatePatch with mode %q: nil, want error", m)
        }
    }
}

func TestBuildUpdatePatch_RejectsEmpty(t *testing.T) {
    in := patchRequest{}
    p, err := buildUpdatePatch(in, nil)
    // buildUpdatePatch itself does NOT reject empty — that's the handler's job
    // to avoid leaking the "no change" semantic into the helper. But IsEmpty()
    // must report true.
    if err != nil { t.Fatalf("buildUpdatePatch on empty: %v", err) }
    if !p.IsEmpty() { t.Errorf("expected IsEmpty()=true on empty input") }
}

func TestBuildUpdatePatch_TrimsAndValidatesEachFieldSelectively(t *testing.T) {
    in := patchRequest{Description: ptrString("  hi  ")}
    p, err := buildUpdatePatch(in, nil)
    if err != nil { t.Fatalf("unexpected error: %v", err) }
    if p.Description == nil || *p.Description != "hi" {
        t.Errorf("Description not trimmed: %v", p.Description)
    }
    // Bad date format propagates as error
    bad := patchRequest{Date: ptrString("nope")}
    if _, err := buildUpdatePatch(bad, nil); err == nil {
        t.Errorf("bad date: nil, want error")
    }
}
```

- [ ] **Step 1.12: Run the failing test**

Expected: FAIL with `undefined: buildUpdatePatch` and `undefined: patchRequest`.

- [ ] **Step 1.13: Implement `buildUpdatePatch`**

Add to `transaction_handlers.go`:

```go
// patchRequest is the wire shape of /api/transactions/batch-update body.patch
// and /api/transactions/update-by-filter body.patch. Pointer fields with
// `omitempty` make absent keys mean "no change" — same semantics as
// UpdatePatch on the server side. Mass-assignment is blocked at the handler
// via dec.DisallowUnknownFields().
type patchRequest struct {
    Date        *string `json:"date,omitempty"`
    Description *string `json:"description,omitempty"`
    CategoryID  *int64  `json:"category_id,omitempty"`
    Tags        *string `json:"tags,omitempty"`
}

var validTagsModes = map[string]struct{}{"add": {}, "remove": {}, "replace": {}}

// buildUpdatePatch validates the wire-format patchRequest and lifts each
// present field into database.UpdatePatch (the server-side struct). Validators
// are the per-field validators from Task 1; no bulk-specific logic here.
//
// Empty patch is NOT rejected here — IsEmpty() exists for that. The handler
// chains buildUpdatePatch + IsEmpty + 400 so the error surface is clean.
func buildUpdatePatch(req patchRequest, tagsMode *string) (database.UpdatePatch, error) {
    var out database.UpdatePatch
    if req.Date != nil {
        d, err := validateDate(*req.Date)
        if err != nil {
            return out, err
        }
        out.Date = &d
    }
    if req.Description != nil {
        d, err := validateDescription(*req.Description)
        if err != nil {
            return out, err
        }
        out.Description = &d
    }
    if req.CategoryID != nil {
        c, err := validateCategoryID(*req.CategoryID)
        if err != nil {
            return out, err
        }
        out.CategoryID = &c
    }
    if req.Tags != nil {
        if tagsMode == nil {
            return out, fmt.Errorf("tagsMode required when tags is set")
        }
        if _, ok := validTagsModes[*tagsMode]; !ok {
            return out, fmt.Errorf("tagsMode must be one of: add, remove, replace")
        }
        t, err := validateTagsField(*req.Tags)
        if err != nil {
            return out, err
        }
        out.Tags = &t
        out.TagsMode = tagsMode
    } else if tagsMode != nil {
        return out, fmt.Errorf("tags required when tagsMode is set")
    }
    return out, nil
}
```

- [ ] **Step 1.14: Run the tests**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test -run "TestBuildUpdatePatch|TestSummarizePatch|TestValidate" ./internal/api/...
```

Expected: ALL pass.

- [ ] **Step 1.15: Run the full backend test suite to check for regressions**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test ./...
```

Expected: ALL pass. The `validateTransactionRequest` refactor must not have broken any existing handler test.

- [ ] **Step 1.16: Commit Task 1**

```
git add internal/api/limits.go internal/api/transaction_handlers.go internal/api/transaction_handlers_test.go
git commit -m "feat(api): add bulk-edit constants, validators, summarizePatch helper"
```

---

### Task 2: TransactionStore.UpdateTx + sentinel errors + store tests

**Files:**
- Modify: `internal/database/store.go` — add `ErrTombstoned`, `ErrNotOwned`, `ErrNotFound`, `UpdateTx`
- Create: `internal/database/store_transaction_test.go` — store-level tests
- Note: `internal/database/queries.sql` and `queries.sql.go` are NOT modified — `UpdateTx` reuses the existing `UpdateTransaction` and `GetTransactionByID` queries.

**Goal:** add the `UpdateTx` method that the handlers will call inside their multi-row transactions. Sentinel errors let the caller distinguish skip-OK conditions from hard errors.

- [ ] **Step 2.1: Examine the existing single-row Update**

Read `internal/database/store.go:100` — the existing `Update(ctx, actorID, params)` method. Note:
- It opens its own `*sql.Tx` via the private `withTx` helper.
- It loads the row before, calls `UpdateTransaction`, loads the row after.
- It calls `writeUpdateAudit(ctx, qtx, actorID, id, before, after)` — note the positional `id` argument.
- `before` and `after` are typed `database.GetTransactionByIDRow` (not `Transaction`), per `store.go:350`.

Also note the existing sentinel error `ErrTokenNotFound` (in `store_api_token.go`) — that's the placement convention for new sentinels.

- [ ] **Step 2.2: Write the failing test for sentinel errors and `UpdateTx` happy path**

Create `internal/database/store_transaction_test.go`:

```go
package database

import (
    "context"
    "database/sql"
    "errors"
    "testing"
)

func TestUpdateTx_HappyPath_AppliesOnlySetFields(t *testing.T) {
    db, store, q := newTestStore(t)
    ctx := context.Background()
    userID := seedTestUser(t, q, "alice")
    catA := seedTestCategory(t, q, "CategoryA")
    catB := seedTestCategory(t, q, "CategoryB")
    txnID := seedTestTransaction(t, q, userID, catA, "2026-04-01", "Original", 10.0, "tag1")

    tx, err := db.BeginTx(ctx, nil)
    if err != nil { t.Fatalf("begin: %v", err) }
    defer tx.Rollback()

    newCat := catB
    patch := UpdatePatch{CategoryID: &newCat}
    before, after, err := store.UpdateTx(ctx, tx, userID, txnID, patch)
    if err != nil { t.Fatalf("UpdateTx: %v", err) }
    if before.CategoryID != catA {
        t.Errorf("before.CategoryID: %d, want %d", before.CategoryID, catA)
    }
    if after.CategoryID != catB {
        t.Errorf("after.CategoryID: %d, want %d", after.CategoryID, catB)
    }
    if after.Description != "Original" {
        t.Errorf("description leaked: %q (should preserve)", after.Description)
    }
    if err := tx.Commit(); err != nil { t.Fatalf("commit: %v", err) }

    // Verify audit row exists
    rows := listAuditRows(t, db)
    found := false
    for _, r := range rows {
        if r["transaction_id"].(int64) == txnID && r["action"].(string) == "update" {
            found = true
            break
        }
    }
    if !found { t.Errorf("expected per-row update audit, got %v", rows) }
}

func TestUpdateTx_TombstonedRow_ReturnsErrTombstoned(t *testing.T) {
    db, store, q := newTestStore(t)
    ctx := context.Background()
    userID := seedTestUser(t, q, "alice")
    catA := seedTestCategory(t, q, "CategoryA")
    txnID := seedTestTransaction(t, q, userID, catA, "2026-04-01", "X", 5.0, "")
    // tombstone it
    if _, err := db.ExecContext(ctx, `UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, txnID); err != nil {
        t.Fatalf("tombstone: %v", err)
    }

    tx, err := db.BeginTx(ctx, nil)
    if err != nil { t.Fatalf("begin: %v", err) }
    defer tx.Rollback()

    newCat := catA
    _, _, err = store.UpdateTx(ctx, tx, userID, txnID, UpdatePatch{CategoryID: &newCat})
    if !errors.Is(err, ErrTombstoned) {
        t.Errorf("expected ErrTombstoned, got %v", err)
    }
}

func TestUpdateTx_NonOwnedRow_ReturnsErrNotOwned(t *testing.T) {
    db, store, q := newTestStore(t)
    ctx := context.Background()
    alice := seedTestUser(t, q, "alice")
    bob := seedTestUser(t, q, "bob")
    cat := seedTestCategory(t, q, "Cat")
    txnID := seedTestTransaction(t, q, alice, cat, "2026-04-01", "X", 5.0, "")

    tx, err := db.BeginTx(ctx, nil)
    if err != nil { t.Fatalf("begin: %v", err) }
    defer tx.Rollback()

    _, _, err = store.UpdateTx(ctx, tx, bob, txnID, UpdatePatch{CategoryID: &cat})
    if !errors.Is(err, ErrNotOwned) {
        t.Errorf("expected ErrNotOwned, got %v", err)
    }
}

func TestUpdateTx_MissingID_ReturnsErrNotFound(t *testing.T) {
    db, store, q := newTestStore(t)
    ctx := context.Background()
    user := seedTestUser(t, q, "alice")

    tx, err := db.BeginTx(ctx, nil)
    if err != nil { t.Fatalf("begin: %v", err) }
    defer tx.Rollback()

    cat := int64(1)
    _, _, err = store.UpdateTx(ctx, tx, user, 99999, UpdatePatch{CategoryID: &cat})
    if !errors.Is(err, ErrNotFound) {
        t.Errorf("expected ErrNotFound, got %v", err)
    }
}

func TestUpdateTx_PreservesUnsetFields(t *testing.T) {
    db, store, q := newTestStore(t)
    ctx := context.Background()
    userID := seedTestUser(t, q, "alice")
    cat := seedTestCategory(t, q, "Cat")
    txnID := seedTestTransaction(t, q, userID, cat, "2026-04-01", "Original Desc", 10.0, "tag1,tag2")

    tx, err := db.BeginTx(ctx, nil)
    if err != nil { t.Fatalf("begin: %v", err) }
    defer tx.Rollback()

    newDate := "2026-05-01"
    _, after, err := store.UpdateTx(ctx, tx, userID, txnID, UpdatePatch{Date: &newDate})
    if err != nil { t.Fatalf("UpdateTx: %v", err) }
    if after.Date != newDate {
        t.Errorf("Date not updated: %s", after.Date)
    }
    if after.Description != "Original Desc" {
        t.Errorf("Description leaked: %q", after.Description)
    }
    if after.CategoryID != cat {
        t.Errorf("Category leaked: %d", after.CategoryID)
    }
    if after.Tags.Valid && after.Tags.String != "tag1,tag2" {
        t.Errorf("Tags leaked: %v", after.Tags)
    }
    _ = tx.Commit()
}

// newTestStore, seedTestUser, seedTestCategory, seedTestTransaction, listAuditRows
// must already exist in the package or be added here. If absent, copy
// from internal/api/transaction_audit_test.go (lines around 30-95).
```

The fixture helpers (`newTestStore`, `seedTestUser`, etc.) need to live in the database package. If they don't exist, lift them from `internal/api/transaction_audit_test.go` adapted for direct DB testing (no http handler).

Concrete fixture helpers to add to `store_transaction_test.go` (top of file, after imports):

```go
func newTestStore(t *testing.T) (*sql.DB, *TransactionStore, *Queries) {
    t.Helper()
    // _foreign_keys=on is REQUIRED — the rollback test in
    // transaction_audit_test.go relies on FK enforcement to trigger a
    // SQL error mid-batch. Without it, an UPDATE that points category_id
    // at a nonexistent category silently succeeds (orphan FK), and the
    // test passes for the wrong reason.
    //
    // Verify against the production DB initialization: if main.go opens
    // SQLite without this pragma, FKs are silently disabled in prod too —
    // raise a separate issue and fix prod alongside, since production
    // FK enforcement is what keeps category_id from going dangling on
    // category-delete.
    db, err := sql.Open("sqlite3", ":memory:?cache=shared&_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
    if err != nil { t.Fatalf("open db: %v", err) }
    t.Cleanup(func() { db.Close() })

    // Belt-and-suspenders: PRAGMA explicitly even if DSN includes it.
    if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
        t.Fatalf("enable FKs: %v", err)
    }
    var fkOn int
    if err := db.QueryRow(`PRAGMA foreign_keys;`).Scan(&fkOn); err != nil || fkOn != 1 {
        t.Fatalf("FK enforcement is OFF (got %d) — rollback test will pass for the wrong reason", fkOn)
    }

    if err := RunMigrations(db); err != nil { t.Fatalf("migrate: %v", err) }
    q := New(db)
    return db, NewTransactionStore(db, q), q
}

func seedTestUser(t *testing.T, q *Queries, name string) int64 {
    t.Helper()
    u, err := q.CreateUser(context.Background(), CreateUserParams{
        Username: name,
        DisplayName: name,
        PasswordHash: "x",
        Role: "member",
    })
    if err != nil { t.Fatalf("seedTestUser: %v", err) }
    return u.ID
}

func seedTestCategory(t *testing.T, q *Queries, name string) int64 {
    t.Helper()
    c, err := q.CreateCategory(context.Background(), CreateCategoryParams{
        Name: name, Type: "expense", Color: "#000000",
    })
    if err != nil { t.Fatalf("seedTestCategory: %v", err) }
    return c.ID
}

func seedTestTransaction(t *testing.T, q *Queries, userID, categoryID int64, date, desc string, amount float64, tags string) int64 {
    t.Helper()
    tt, err := q.CreateTransaction(context.Background(), CreateTransactionParams{
        UserID: userID, CategoryID: categoryID,
        Date: date, Description: desc,
        Amount: amount,
        Tags: sql.NullString{String: tags, Valid: tags != ""},
    })
    if err != nil { t.Fatalf("seedTestTransaction: %v", err) }
    return tt.ID
}

func listAuditRows(t *testing.T, db *sql.DB) []map[string]any {
    t.Helper()
    rows, err := db.Query(`SELECT id, transaction_id, action, actor_user_id, before_json, after_json FROM transaction_audit ORDER BY id`)
    if err != nil { t.Fatalf("listAuditRows: %v", err) }
    defer rows.Close()
    var out []map[string]any
    for rows.Next() {
        var id, txnID int64
        var action string
        var actor sql.NullInt64
        var before, after sql.NullString
        if err := rows.Scan(&id, &txnID, &action, &actor, &before, &after); err != nil {
            t.Fatalf("scan: %v", err)
        }
        out = append(out, map[string]any{
            "id": id, "transaction_id": txnID, "action": action,
            "actor_user_id": actor, "before_json": before, "after_json": after,
        })
    }
    return out
}
```

Adjust column names / params if they don't match the actual schema — the implementer should verify against the current `queries.sql.go` definitions.

- [ ] **Step 2.3: Run the failing test**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test -run TestUpdateTx ./internal/database/...
```

Expected: FAIL — `undefined: ErrTombstoned`, `undefined: ErrNotOwned`, `undefined: ErrNotFound`, `(*TransactionStore).UpdateTx undefined`.

- [ ] **Step 2.4: Add sentinel errors**

`UpdatePatch` is already in `internal/database/store.go` (added in Task 1, Step 1.5). This step only adds the three sentinel errors used by `UpdateTx` to signal skip-OK conditions.

Add to `internal/database/store.go` (near `ErrTokenNotFound` if present, or at the top of the file alongside other top-level vars):

```go
var (
    ErrTombstoned = errors.New("transaction is tombstoned")
    ErrNotOwned   = errors.New("transaction is not owned by the actor")
    ErrNotFound   = errors.New("transaction not found")
)
```

- [ ] **Step 2.5: Implement `UpdateTx`**

**Pre-flight: verify the sqlc-generated param shapes** before writing code. Inspect `internal/database/queries.sql.go` for:
- `UpdateTransactionParams.Date` — likely `time.Time` (not `string`)
- `UpdateTransactionParams.Tags` — `sql.NullString`
- `GetTransactionByIDRow.Date` — `time.Time`
- All other fields the merge will copy through

If `Date` is `time.Time` (which it is — `queries.sql.go:589`), the patch's `*string` Date must be parsed before assigning to `params.Date`. Use `time.Parse("2006-01-02", *patch.Date)` and propagate parse errors. Validation in `validateDate` (Task 1) already enforces the format, so a parse error here is "should never happen" but still must be handled.

Add to `internal/database/store.go` (after the existing `Update` method around line 130):

```go
// UpdateTx applies a partial-field update to a single transaction inside a
// caller-owned *sql.Tx. The handler holds the tx so it can drive a multi-row
// batch under one transactional boundary. Returns sentinel errors for the
// three skip-OK conditions (tombstoned, non-owned, missing) so the caller
// can decide whether to skip-and-continue or roll back.
//
// Per-row audit row is written via writeUpdateAudit inside this same tx —
// the handler does not write audit separately. If UpdateTx returns nil
// (success), the audit row is committed alongside the data update; if it
// returns any error, both are rolled back together.
//
// Admin-bypass is a HANDLER concern. The store enforces strict ownership;
// the handler must short-circuit the ownership check upstream when the
// caller is an admin. (Pattern: handler reads user.Role; if admin, calls
// UpdateTx with a special actorUserID == before.UserID after a separate
// GetTransactionByID lookup, OR keeps a single code path and accepts that
// admin bulk-update enforces "modify only your own rows" — pick at impl
// time, see Step 3.4 admin-bypass research.)
func (s *TransactionStore) UpdateTx(
    ctx context.Context,
    tx *sql.Tx,
    actorUserID int64,
    id int64,
    patch UpdatePatch,
) (before, after GetTransactionByIDRow, err error) {
    qtx := s.q.WithTx(tx)

    before, err = qtx.GetTransactionByID(ctx, id)
    if errors.Is(err, sql.ErrNoRows) {
        return before, after, ErrNotFound
    }
    if err != nil {
        return before, after, fmt.Errorf("load before: %w", err)
    }
    if before.DeletedAt.Valid {
        return before, after, ErrTombstoned
    }
    if before.UserID != actorUserID {
        return before, after, ErrNotOwned
    }

    // Merge patch onto existing row → UpdateTransactionParams (full-replace shape).
    // CRITICAL: Date in UpdateTransactionParams is time.Time (verified at
    // queries.sql.go:589). The patch's *string Date must be parsed.
    params := UpdateTransactionParams{
        ID:               id,
        Date:             before.Date,           // time.Time
        Description:      before.Description,
        CategoryID:       before.CategoryID,
        Tags:             before.Tags,           // sql.NullString
        Notes:            before.Notes,
        Amount:           before.Amount,
        OriginalAmount:   before.OriginalAmount,
        OriginalCurrency: before.OriginalCurrency,
        // ... copy ALL fields from `before` so unset ≠ wipe. Implementer:
        // walk UpdateTransactionParams and copy each field that is NOT in
        // the patch. Use go-staticcheck or zero-init detection to make sure
        // nothing is missed.
    }
    if patch.Date != nil {
        d, perr := time.Parse("2006-01-02", *patch.Date)
        if perr != nil {
            return before, after, fmt.Errorf("invalid date in patch (validateDate should have caught this): %w", perr)
        }
        params.Date = d
    }
    if patch.Description != nil {
        params.Description = *patch.Description
    }
    if patch.CategoryID != nil {
        params.CategoryID = *patch.CategoryID
    }
    if patch.Tags != nil {
        // The handler computes per-row "merged" tags via applyTagsMode
        // (Add/Remove/Replace set arithmetic) BEFORE calling UpdateTx.
        // Here we just assign — the store stays dumb about set arithmetic.
        params.Tags = sql.NullString{String: *patch.Tags, Valid: *patch.Tags != ""}
    }

    if err := qtx.UpdateTransaction(ctx, params); err != nil {
        return before, after, fmt.Errorf("update transaction: %w", err)
    }

    after, err = qtx.GetTransactionByID(ctx, id)
    if err != nil {
        return before, after, fmt.Errorf("load after: %w", err)
    }
    if err := writeUpdateAudit(ctx, qtx, actorUserID, id, before, after); err != nil {
        return before, after, fmt.Errorf("write audit: %w", err)
    }
    return before, after, nil
}
```

Note: the **batch-update handler** is responsible for computing the merged-tags string per row (Add/Remove/Replace set arithmetic) BEFORE calling `UpdateTx`. The store layer just writes whatever it's given. This keeps the store dumb and the tag-merge logic testable in handler-test land.

**Fixture pre-flight:** the `seedTestTransaction` helper in Step 2.2 also passes `Date: date` (string). If `CreateTransactionParams.Date` is `time.Time`, the fixture must `time.Parse` the string first. Verify and adjust before running tests.

- [ ] **Step 2.6: Run the tests**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test -run TestUpdateTx ./internal/database/...
```

Expected: PASS.

- [ ] **Step 2.7: Verify the handler-package conversion didn't break**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test ./...
```

Expected: ALL pass.

- [ ] **Step 2.8: Commit Task 2**

```
git add internal/database/store.go internal/database/store_transaction_test.go internal/api/transaction_handlers.go internal/api/transaction_handlers_test.go
git commit -m "feat(db): add TransactionStore.UpdateTx + sentinel errors"
```

---

## Chunk 2: Backend handlers (Tasks 3–5)

### Task 3: `handleBatchUpdateTransactions`

**Files:**
- Modify: `internal/api/transaction_handlers.go` — add `BatchUpdateRequest`, `BulkUpdateResponse`, `applyTagsMode` helper, `handleBatchUpdateTransactions`
- Modify: `internal/api/transaction_handlers_test.go` — handler-level tests
- Modify: `internal/api/transaction_audit_test.go` — audit invariant tests
- Modify: `internal/api/router.go` — wire `POST /api/transactions/batch-update`

**Goal:** the ID-list path. Iterates per ID inside one tx, calls `UpdateTx` per row, computes per-row merged tags from Add/Remove/Replace mode, emits a summary audit row for skipped count, fires `verifyAffectedCheckpoints` after commit when date is in the patch.

- [ ] **Step 3.1: Write the failing test for the happy path**

Append to `transaction_handlers_test.go`:

```go
func TestBatchUpdate_HappyPath_AllRowsUpdated(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    catA := seedTestCategory(t, fix.q, "A")
    catB := seedTestCategory(t, fix.q, "B")
    id1 := seedTestTransaction(t, fix.q, user, catA, "2026-04-01", "T1", 10.0, "")
    id2 := seedTestTransaction(t, fix.q, user, catA, "2026-04-02", "T2", 20.0, "")
    id3 := seedTestTransaction(t, fix.q, user, catA, "2026-04-03", "T3", 30.0, "")

    body := map[string]any{
        "ids":   []int64{id1, id2, id3},
        "patch": map[string]any{"category_id": catB},
    }
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))

    if rec.Code != 200 {
        t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
    }
    var resp struct{ Updated, Skipped int64 }
    if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil { t.Fatal(err) }
    if resp.Updated != 3 || resp.Skipped != 0 {
        t.Errorf("got updated=%d skipped=%d, want 3 0", resp.Updated, resp.Skipped)
    }
    // verify each row's category got updated
    for _, id := range []int64{id1, id2, id3} {
        var got int64
        if err := db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, id).Scan(&got); err != nil { t.Fatal(err) }
        if got != catB { t.Errorf("id=%d category: %d, want %d", id, got, catB) }
    }
}

// TestBatchUpdate_HidesTombstoned uses the CLAUDE.md canonical pattern:
// seed a tombstoned row with a sentinel amount of 999, then assert that
// 999 never appears in any post-update query result. Per CLAUDE.md soft-
// delete invariant: "Every transactions read must filter `AND t.deleted_at
// IS NULL`."
func TestBatchUpdate_HidesTombstoned(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    catB := seedTestCategory(t, fix.q, "CatB")
    live := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "live", 10.0, "")
    ts := seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "tombstoned-sentinel", 999.0, "")
    db.Exec(`UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, ts)

    // Try to update both — the tombstoned row should be skipped, never touched.
    body := map[string]any{
        "ids":   []int64{live, ts},
        "patch": map[string]any{"category_id": catB},
    }
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 200 { t.Fatal(rec.Body.String()) }

    var resp struct{ Updated, Skipped int64 }
    json.Unmarshal(rec.Body.Bytes(), &resp)
    if resp.Updated != 1 || resp.Skipped != 1 {
        t.Errorf("got updated=%d skipped=%d, want 1 1", resp.Updated, resp.Skipped)
    }

    // Sentinel: tombstoned amount 999.0 must not appear in any "live" view.
    // Verify the tombstoned row is still tombstoned and its category was NOT changed.
    var tsCat int64
    var tsAmount float64
    var tsDeletedAt sql.NullString
    db.QueryRow(`SELECT category_id, amount, deleted_at FROM transactions WHERE id = ?`, ts).
        Scan(&tsCat, &tsAmount, &tsDeletedAt)
    if tsCat != cat {
        t.Errorf("tombstoned row's category got mutated: %d (want %d — must remain untouched)", tsCat, cat)
    }
    if tsAmount != 999.0 {
        t.Errorf("tombstoned row's amount changed: %f (sentinel violated)", tsAmount)
    }
    if !tsDeletedAt.Valid {
        t.Errorf("tombstoned row got resurrected: deleted_at is null")
    }
}

func TestBatchUpdate_PartialSkip_TombstonedAndNonOwnedAreSkipped(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    alice := seedTestUser(t, fix.q, "alice")
    bob := seedTestUser(t, fix.q, "bob")
    cat := seedTestCategory(t, fix.q, "Cat")
    catB := seedTestCategory(t, fix.q, "CatB")

    aliveAlice := seedTestTransaction(t, fix.q, alice, cat, "2026-04-01", "ok", 10.0, "")
    tombstonedAlice := seedTestTransaction(t, fix.q, alice, cat, "2026-04-02", "ts", 10.0, "")
    aliveBob := seedTestTransaction(t, fix.q, bob, cat, "2026-04-03", "bob", 10.0, "")
    db.Exec(`UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, tombstonedAlice)

    body := map[string]any{
        "ids":   []int64{aliveAlice, tombstonedAlice, aliveBob, 99999},
        "patch": map[string]any{"category_id": catB},
    }
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(alice))
    if rec.Code != 200 {
        t.Fatalf("got %d, want 200", rec.Code)
    }
    var resp struct{ Updated, Skipped int64 }
    json.Unmarshal(rec.Body.Bytes(), &resp)
    if resp.Updated != 1 || resp.Skipped != 3 {
        t.Errorf("got updated=%d skipped=%d, want 1 3", resp.Updated, resp.Skipped)
    }
}

func TestBatchUpdate_RejectsEmptyIDList(t *testing.T) {
    h, _, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    body := map[string]any{"ids": []int64{}, "patch": map[string]any{"category_id": int64(1)}}
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 400 { t.Errorf("empty ids: got %d, want 400", rec.Code) }
}

func TestBatchUpdate_RejectsOversizedIDList(t *testing.T) {
    h, _, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    ids := make([]int64, MaxBatchUpdateIDs+1)
    for i := range ids { ids[i] = int64(i + 1) }
    body := map[string]any{"ids": ids, "patch": map[string]any{"category_id": int64(1)}}
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 400 { t.Errorf("oversized ids: got %d, want 400", rec.Code) }
}

func TestBatchUpdate_RejectsEmptyPatch(t *testing.T) {
    h, _, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    txn := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "T", 10.0, "")
    body := map[string]any{"ids": []int64{txn}, "patch": map[string]any{}}
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 400 { t.Errorf("empty patch: got %d, want 400", rec.Code) }
}

func TestBatchUpdate_DisallowUnknownFields_RejectsUserIDInjection(t *testing.T) {
    h, _, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    txn := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "T", 10.0, "")
    // Raw JSON to inject an unknown field
    body := []byte(fmt.Sprintf(`{"ids":[%d],"patch":{"user_id":99,"category_id":%d}}`, txn, cat))
    req := httptest.NewRequest("POST", "/api/transactions/batch-update", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req = withUser(user)(req)
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, req)
    if rec.Code != 400 {
        t.Errorf("expected 400 for unknown field; got %d body=%s", rec.Code, rec.Body.String())
    }
}

func TestBatchUpdate_TagsAdd_AppendsAndDeduplicates(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id1 := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "T1", 10.0, "Tax, receipt")
    id2 := seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "T2", 10.0, "")

    body := map[string]any{
        "ids":      []int64{id1, id2},
        "patch":    map[string]any{"tags": "tax,receipt"},
        "tagsMode": "add",
    }
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 200 { t.Fatalf("got %d", rec.Code) }

    // id1: existing "Tax, receipt" + add "tax,receipt" → "Tax, receipt, tax"
    // (case-sensitive: "Tax" != "tax", "receipt" already present so dedupes)
    var got1 string
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id1).Scan(&got1)
    if got1 != "Tax, receipt, tax" {
        t.Errorf("id1 tags: %q, want %q", got1, "Tax, receipt, tax")
    }
    // id2: empty + add → "tax,receipt"
    var got2 string
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id2).Scan(&got2)
    if got2 != "tax,receipt" {
        t.Errorf("id2 tags: %q, want %q", got2, "tax,receipt")
    }
}

func TestBatchUpdate_TagsRemove_FiltersMatching(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "T", 10.0, "tax,receipt,personal")

    body := map[string]any{
        "ids":      []int64{id},
        "patch":    map[string]any{"tags": "tax,personal"},
        "tagsMode": "remove",
    }
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 200 { t.Fatalf("got %d", rec.Code) }

    var got string
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id).Scan(&got)
    if got != "receipt" {
        t.Errorf("tags: %q, want \"receipt\"", got)
    }
}

func TestBatchUpdate_TagsReplace_OverwritesExisting(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "T", 10.0, "old1,old2,old3")

    body := map[string]any{
        "ids":      []int64{id},
        "patch":    map[string]any{"tags": "fresh"},
        "tagsMode": "replace",
    }
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 200 { t.Fatalf("got %d", rec.Code) }

    var got string
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id).Scan(&got)
    if got != "fresh" { t.Errorf("tags: %q", got) }
}

func TestBatchUpdate_TagsCaseSensitivePinning(t *testing.T) {
    // Spec §3.3 worked example: existing "Tax, receipt" + Add "tax" → "Tax, receipt, tax"
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "T", 10.0, "Tax, receipt")

    body := map[string]any{
        "ids":      []int64{id},
        "patch":    map[string]any{"tags": "tax"},
        "tagsMode": "add",
    }
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 200 { t.Fatalf("got %d", rec.Code) }

    var got string
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id).Scan(&got)
    if got != "Tax, receipt, tax" {
        t.Errorf("tags: %q, want %q", got, "Tax, receipt, tax")
    }
}
```

(The fixtures `setupHandlerTest`, `withUser`, `postJSON` may already exist in the test file or in a sibling — adapt to whatever's there.)

- [ ] **Step 3.2: Run the failing tests**

Expected: FAIL with `404 page not found` or similar (route doesn't exist yet).

- [ ] **Step 3.3: Add the request/response types + `applyTagsMode` helper**

Add to `transaction_handlers.go`:

```go
type batchUpdateRequest struct {
    IDs      []int64       `json:"ids"`
    Patch    patchRequest  `json:"patch"`
    TagsMode *string       `json:"tagsMode,omitempty"`
}

type bulkUpdateResponse struct {
    Updated int64 `json:"updated"`
    Skipped int64 `json:"skipped,omitempty"`
}

// applyTagsMode is the per-row tag set-arithmetic. Existing tags are split
// on commas, trimmed of leading/trailing whitespace per item; new tags
// are split the same way. Set arithmetic is byte-for-byte case-sensitive
// — see spec §3.3 for the worked example. The result is re-serialized
// preserving the existing-then-new order.
//
// Mode = "replace" returns newTags verbatim (no parsing of existing).
func applyTagsMode(existing, newTags, mode string) string {
    if mode == "replace" {
        return newTags
    }
    parse := func(s string) []string {
        if s == "" { return nil }
        parts := strings.Split(s, ",")
        out := make([]string, 0, len(parts))
        for _, p := range parts {
            t := strings.TrimSpace(p)
            if t != "" { out = append(out, t) }
        }
        return out
    }
    cur := parse(existing)
    in := parse(newTags)
    seen := make(map[string]struct{}, len(cur))
    for _, t := range cur { seen[t] = struct{}{} }
    switch mode {
    case "add":
        for _, t := range in {
            if _, ok := seen[t]; !ok {
                cur = append(cur, t)
                seen[t] = struct{}{}
            }
        }
    case "remove":
        rm := make(map[string]struct{}, len(in))
        for _, t := range in { rm[t] = struct{}{} }
        out := cur[:0]
        for _, t := range cur {
            if _, drop := rm[t]; !drop {
                out = append(out, t)
            }
        }
        cur = out
    }
    return strings.Join(cur, ", ")
}
```

The "replace" path returns the input verbatim (per spec §3.3: "as the user typed it (no canonicalization)"). The "add" / "remove" paths are case-sensitive per spec §3.3.

- [ ] **Step 3.4: Implement `handleBatchUpdateTransactions`**

Add to `transaction_handlers.go`:

```go
// handleBatchUpdateTransactions applies a partial-field patch to a list of
// transaction IDs in one tx. Tombstoned / non-owned / missing rows are
// skipped (consistent with handleBatchDeleteTransactions:824-840). Per-row
// audit rows commit alongside the data updates; a summary audit row records
// skipped count if any. Mid-batch SQL/constraint errors roll back the entire
// tx — caller sees no partial progress.
//
// See spec docs/superpowers/specs/2026-05-01-transactions-bulk-edit-design.md
// §5.3 for the design rationale.
func (h *Handler) handleBatchUpdateTransactions(w http.ResponseWriter, r *http.Request) {
    user, ok := auth.GetUser(r)
    if !ok {
        writeError(w, http.StatusUnauthorized, "unauthorized")
        return
    }

    var req batchUpdateRequest
    dec := json.NewDecoder(r.Body)
    dec.DisallowUnknownFields()  // mass-assignment guard — see spec §5.5b
    if err := dec.Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid request body")
        return
    }
    if len(req.IDs) == 0 || len(req.IDs) > MaxBatchUpdateIDs {
        writeError(w, http.StatusBadRequest, "invalid ids")
        return
    }
    patch, err := buildUpdatePatch(req.Patch, req.TagsMode)
    if err != nil {
        writeError(w, http.StatusBadRequest, err.Error())
        return
    }
    if patch.IsEmpty() {
        writeError(w, http.StatusBadRequest, "patch must not be empty")
        return
    }

    tx, err := h.db.BeginTx(r.Context(), nil)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "begin tx")
        return
    }
    defer tx.Rollback()

    var updated, skipped int64
    var minDate time.Time

    for _, id := range req.IDs {
        // Per-row tag merge: load the row, compute the new tags string,
        // pass it to UpdateTx as a fully-resolved patch.Tags pointer.
        rowPatch := patch
        if patch.Tags != nil && *patch.TagsMode != "replace" {
            // Need the row's current tags to do set arithmetic. The store
            // also re-loads inside UpdateTx, but we need it here BEFORE
            // calling UpdateTx so we can pass a resolved Tags string.
            // Side-effect: an extra GetTransactionByID per row. Acceptable
            // at MaxBatchUpdateIDs=500 — the cost vs the alternative
            // (folding tag-merge into the store layer + complicating its
            // API) is much smaller.
            existing, err := h.queries.GetTransactionByID(r.Context(), id)
            if errors.Is(err, sql.ErrNoRows) {
                skipped++
                continue
            }
            if err != nil {
                writeError(w, http.StatusInternalServerError, "load existing tags")
                return
            }
            curTags := ""
            if existing.Tags.Valid { curTags = existing.Tags.String }
            merged := applyTagsMode(curTags, *patch.Tags, *patch.TagsMode)
            rowPatch.Tags = &merged
        }

        before, after, err := h.txnStore.UpdateTx(r.Context(), tx, user.ID, id, rowPatch)
        switch {
        case errors.Is(err, database.ErrTombstoned),
             errors.Is(err, database.ErrNotOwned),
             errors.Is(err, database.ErrNotFound):
            skipped++
        case err != nil:
            writeError(w, http.StatusInternalServerError, "internal server error")
            return
        default:
            updated++
            if patch.Date != nil {
                minDate = earliestDate(minDate, earliestDate(before.Date, after.Date))
            }
        }
    }

    if skipped > 0 {
        if err := h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditUpdate,
            database.BulkAuditSummary{
                Count:  skipped,
                Filter: fmt.Sprintf("skipped_during_batch_update:%d_of_%d", skipped, len(req.IDs)),
            }); err != nil {
            writeError(w, http.StatusInternalServerError, "audit summary")
            return
        }
    }

    if err := tx.Commit(); err != nil {
        writeError(w, http.StatusInternalServerError, "commit")
        return
    }

    if patch.Date != nil && !minDate.IsZero() {
        h.verifyAffectedCheckpoints(r.Context(), minDate)
    }

    writeJSON(w, http.StatusOK, bulkUpdateResponse{Updated: updated, Skipped: skipped})
}
```

Verify the helper signatures it depends on (`earliestDate`, `verifyAffectedCheckpoints`, `database.AuditUpdate`, `database.BulkAuditSummary`) by reading their definitions; spec §5.3 lists them.

**Admin-bypass design (handler-side, NOT store-side).**

The spec §5.2 fixes `UpdateTx` at five args: `(ctx, tx, actorUserID, id, patch)`. The store enforces strict ownership and returns `ErrNotOwned` on mismatch.

**v1 decision: admins call `UpdateTx` exactly like non-admins do.** Cross-user IDs return `ErrNotOwned` and are skipped. This means bulk-edit does not support admin cross-user mutation. Single-row update (`PUT /api/transactions/{id}`) remains the admin path for cross-user edits. Document this in §11 as a known v1 limitation.

Rationale: a hand-rolled admin bypass either (a) needs a 6th `bypassOwnership` arg on `UpdateTx` (contradicts spec §5.2), or (b) needs a parallel admin-only path that double-writes audit rows (one for the row owner, one for the admin actor). Both add complexity for a use case that isn't on the v1 critical path. Defer to a follow-up if/when cross-user bulk admin edits become a real workflow.

Handler call site stays simple — five args, one branch:

```go
before, after, err := h.txnStore.UpdateTx(r.Context(), tx, user.ID, id, rowPatch)
// errors.Is(err, database.ErrNotOwned) → skipped++ (covers both non-admin
// cross-user attempts and admin cross-user attempts; same skip semantics)
```

The `TestBatchUpdate_PartialSkip_TombstonedAndNonOwnedAreSkipped` test (Step 3.1) already covers this.

- [ ] **Step 3.5: Wire the route**

In `internal/api/router.go`, find the existing `batch-delete` route registration (`router.go:117` per spec). Add the new route immediately below:

```go
r.Post("/transactions/batch-update", h.handleBatchUpdateTransactions)
```

Inside the same auth-protected scope as `batch-delete`.

- [ ] **Step 3.6: Run the tests**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test -run "TestBatchUpdate" ./internal/api/...
```

Expected: ALL pass.

- [ ] **Step 3.7: Add the audit invariant tests**

Append to `transaction_audit_test.go`:

```go
func TestAudit_BatchUpdate_WritesUpdateRowPerID_WithBeforeAndAfter(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    catB := seedTestCategory(t, fix.q, "CatB")
    id1 := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10.0, "")
    id2 := seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "Y", 10.0, "")

    postJSON(t, h, "/api/transactions/batch-update", map[string]any{
        "ids": []int64{id1, id2}, "patch": map[string]any{"category_id": catB},
    }, withUser(user))

    audits := listAuditRows(t, db)
    var perRow []map[string]any
    for _, a := range audits {
        if a["action"].(string) == "update" && a["transaction_id"].(int64) > 0 {
            perRow = append(perRow, a)
        }
    }
    if len(perRow) != 2 {
        t.Errorf("expected 2 per-row audit rows, got %d", len(perRow))
    }
    for _, a := range perRow {
        if !a["before_json"].(sql.NullString).Valid {
            t.Errorf("before_json missing on audit %v", a)
        }
        if !a["after_json"].(sql.NullString).Valid {
            t.Errorf("after_json missing on audit %v", a)
        }
    }
}

func TestAudit_BatchUpdate_WithSkips_WritesSummaryRow(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id1 := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10.0, "")

    postJSON(t, h, "/api/transactions/batch-update", map[string]any{
        "ids": []int64{id1, 99999}, "patch": map[string]any{"category_id": cat},
    }, withUser(user))

    audits := listAuditRows(t, db)
    var summary []map[string]any
    for _, a := range audits {
        if a["transaction_id"].(int64) == 0 && a["action"].(string) == "update" {
            summary = append(summary, a)
        }
    }
    if len(summary) != 1 {
        t.Errorf("expected 1 summary row, got %d", len(summary))
    }
    bj := summary[0]["before_json"].(sql.NullString)
    if !bj.Valid || !strings.Contains(bj.String, "skipped_during_batch_update:1_of_2") {
        t.Errorf("summary row body unexpected: %v", bj)
    }
}

// TestAudit_BatchUpdate_OnRollback_LeavesNoOrphanRows arranges a mid-batch
// SQL failure by passing `category_id: 99999` (FK to a category that doesn't
// exist) in the patch. SQLite's FK constraint rejects the UPDATE on the
// first row, the deferred Rollback fires, and both data + audit rolls back
// cleanly. Verifies the tx wrapper holds across both data and audit.
//
// PRECONDITION: PRAGMA foreign_keys = ON. If the handler test fixture's
// DSN omits `_foreign_keys=on`, the UPDATE silently succeeds with an
// orphan FK and this test passes for the wrong reason. Verify the api
// package's test fixture (setupHandlerTest or its sibling) enables FKs
// before running this test — see internal/database/store_transaction_test.go's
// newTestStore helper for the canonical pattern. If the api fixture
// doesn't, propagate the same DSN+PRAGMA-verify pattern there as part
// of this task.
func TestAudit_BatchUpdate_OnRollback_LeavesNoOrphanRows(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id1 := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10.0, "")
    id2 := seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "Y", 10.0, "")

    // Snapshot pre-state.
    auditsBefore := listAuditRows(t, db)
    var c1Before, c2Before int64
    db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, id1).Scan(&c1Before)
    db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, id2).Scan(&c2Before)

    // Poisoned patch: category 99999 doesn't exist → FK violation on UPDATE.
    body := map[string]any{
        "ids": []int64{id1, id2}, "patch": map[string]any{"category_id": 99999},
    }
    rec := postJSON(t, h, "/api/transactions/batch-update", body, withUser(user))
    if rec.Code != 500 {
        t.Errorf("expected 500 on FK violation, got %d", rec.Code)
    }

    // Both rows must be unchanged.
    var c1After, c2After int64
    db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, id1).Scan(&c1After)
    db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, id2).Scan(&c2After)
    if c1After != c1Before { t.Errorf("id1 mutated despite rollback: was %d, got %d", c1Before, c1After) }
    if c2After != c2Before { t.Errorf("id2 mutated despite rollback: was %d, got %d", c2Before, c2After) }

    // No new audit rows: the rolled-back transaction must not have committed
    // any audit either. Compare audit-row count before vs after.
    auditsAfter := listAuditRows(t, db)
    if len(auditsAfter) != len(auditsBefore) {
        t.Errorf("expected zero new audit rows after rollback, got %d new",
            len(auditsAfter)-len(auditsBefore))
    }
}
```

- [ ] **Step 3.8: Run audit tests**

Expected: PASS.

- [ ] **Step 3.9: Run the full backend suite**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 \
  go test ./...
```

Expected: ALL pass.

- [ ] **Step 3.10: Dispatch the data-correctness reviewer**

Per CLAUDE.md: dispatch `data-correctness-reviewer` agent on the changes since last commit (run `git diff` and feed it). The reviewer specifically verifies the audit invariant + soft-delete + checkpoint hook behavior.

If the reviewer finds issues, fix them and re-run tests before committing.

- [ ] **Step 3.11: Commit Task 3**

```
git add internal/api/transaction_handlers.go internal/api/router.go internal/api/transaction_handlers_test.go internal/api/transaction_audit_test.go internal/database/store.go internal/database/store_transaction_test.go
git commit -m "feat(api): add handleBatchUpdateTransactions + applyTagsMode helper"
```

---

## Chunk 3: Filter-mode handler (Tasks 4–5)

### Task 4: `handleUpdateTransactionsByFilter` — no-tags fast path

**Files:**
- Modify: `internal/api/transaction_handlers.go` — add `handleUpdateTransactionsByFilter` + the no-tags branch
- Modify: `internal/api/transaction_handlers_test.go` — handler tests
- Modify: `internal/api/transaction_audit_test.go` — audit invariant tests
- Modify: `internal/api/router.go` — wire the route

**Goal:** the filter-scoped UPDATE that touches every row matching the querystring in one SQL statement. No tags branch — that's Task 5. This task gets the no-tags fast path working end-to-end including audit + checkpoint hook.

The implementer must read spec §5.4 + §5.6 for the canonical SQL shape. The key precedent is `handleDeleteTransactionsByFilter` at `internal/api/transaction_handlers.go:910` — copy its scaffolding (build WHERE clause, append non-admin user_id, append live-filter, JOIN categories) and substitute the UPDATE shape.

- [ ] **Step 4.1: Write the failing happy-path test**

Append to `transaction_handlers_test.go`:

```go
func TestUpdateByFilter_HappyPath(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    catCleaning := seedTestCategory(t, fix.q, "Cleaning")
    catHousehold := seedTestCategory(t, fix.q, "Household")
    seedTestTransaction(t, fix.q, user, catCleaning, "2026-04-01", "vacuum", 10.0, "")
    seedTestTransaction(t, fix.q, user, catCleaning, "2026-04-02", "mop", 5.0, "")
    seedTestTransaction(t, fix.q, user, catCleaning, "2026-04-03", "soap", 3.0, "")
    seedTestTransaction(t, fix.q, user, catHousehold, "2026-04-04", "lightbulb", 4.0, "")  // not in filter

    body := map[string]any{"patch": map[string]any{"category_id": catHousehold}}
    rec := postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", catCleaning), body, withUser(user))
    if rec.Code != 200 {
        t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
    }
    var resp struct{ Updated int64 }
    json.Unmarshal(rec.Body.Bytes(), &resp)
    if resp.Updated != 3 {
        t.Errorf("updated=%d, want 3", resp.Updated)
    }
}

func TestUpdateByFilter_HidesTombstoned(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    catB := seedTestCategory(t, fix.q, "CatB")
    live := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "live", 10.0, "")
    ts := seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "ts", 999.0, "")
    db.Exec(`UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, ts)

    body := map[string]any{"patch": map[string]any{"category_id": catB}}
    rec := postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat), body, withUser(user))
    if rec.Code != 200 { t.Fatal(rec.Body.String()) }

    var liveCat, tsCat int64
    db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, live).Scan(&liveCat)
    db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, ts).Scan(&tsCat)
    if liveCat != catB { t.Errorf("live row not updated: cat=%d", liveCat) }
    if tsCat != cat { t.Errorf("tombstoned row was touched! cat=%d", tsCat) }
}

func TestUpdateByFilter_RespectsOwnershipForNonAdmin(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    alice := seedTestUser(t, fix.q, "alice")
    bob := seedTestUser(t, fix.q, "bob")
    cat := seedTestCategory(t, fix.q, "Cat")
    catB := seedTestCategory(t, fix.q, "CatB")
    aliceTxn := seedTestTransaction(t, fix.q, alice, cat, "2026-04-01", "a", 10, "")
    bobTxn := seedTestTransaction(t, fix.q, bob, cat, "2026-04-02", "b", 10, "")

    body := map[string]any{"patch": map[string]any{"category_id": catB}}
    rec := postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat), body, withUser(alice))
    if rec.Code != 200 { t.Fatal(rec.Body.String()) }

    var aliceCat, bobCat int64
    db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, aliceTxn).Scan(&aliceCat)
    db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, bobTxn).Scan(&bobCat)
    if aliceCat != catB { t.Errorf("alice's not updated") }
    if bobCat != cat { t.Errorf("bob's was wrongly touched") }
}

func TestUpdateByFilter_RejectsEmptyPatch(t *testing.T) {
    h, _, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    body := map[string]any{"patch": map[string]any{}}
    rec := postJSON(t, h, "/api/transactions/update-by-filter", body, withUser(user))
    if rec.Code != 400 { t.Errorf("got %d, want 400", rec.Code) }
}
```

- [ ] **Step 4.2: Run tests, watch fail** — Expected: 404 (route not registered).

- [ ] **Step 4.3: Add the request type + handler skeleton**

In `transaction_handlers.go`:

```go
type updateByFilterRequest struct {
    Patch    patchRequest `json:"patch"`
    TagsMode *string      `json:"tagsMode,omitempty"`
}

// handleUpdateTransactionsByFilter applies a patch to every transaction
// matching the filter querystring. Atomic: one tx wraps the data update
// and the summary audit row.
//
// Two SQL paths, mutually exclusive at the handler level:
//   - patch.Tags == nil → single SQL UPDATE (no-tags fast path)
//   - patch.Tags != nil → enumerate-then-write loop (tags branch, Task 5)
//
// Soft-delete invariant: both layers (inner appendLiveTransactionsFilter +
// outer "AND deleted_at IS NULL") are load-bearing. Removing either is a
// CLAUDE.md violation.
//
// See spec §5.4, §5.6.
func (h *Handler) handleUpdateTransactionsByFilter(w http.ResponseWriter, r *http.Request) {
    user, ok := auth.GetUser(r)
    if !ok { writeError(w, http.StatusUnauthorized, "unauthorized"); return }

    var req updateByFilterRequest
    dec := json.NewDecoder(r.Body)
    dec.DisallowUnknownFields()
    if err := dec.Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid request body")
        return
    }
    patch, err := buildUpdatePatch(req.Patch, req.TagsMode)
    if err != nil {
        writeError(w, http.StatusBadRequest, err.Error())
        return
    }
    if patch.IsEmpty() {
        writeError(w, http.StatusBadRequest, "patch must not be empty")
        return
    }

    tx, err := h.db.BeginTx(r.Context(), nil)
    if err != nil { writeError(w, http.StatusInternalServerError, "begin"); return }
    defer tx.Rollback()

    var updated int64
    var minDate time.Time

    if patch.Tags != nil {
        // Tags read-then-write path — implemented in Task 5.
        writeError(w, http.StatusNotImplemented, "tags filter path: implemented in Task 5")
        return
    }

    // No-tags fast path: single UPDATE.
    updated, minDate, err = h.runUpdateByFilterNoTags(r, tx, user, patch)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "internal server error")
        return
    }

    // Summary audit row — emitted unconditionally (count=0 is valid; spec §5.4).
    // summarizePatch accepts database.UpdatePatch directly — no adapter needed.
    summary := summarizePatch(patch)
    filterDesc := fmt.Sprintf("query=%q patch=%s", r.URL.RawQuery, summary)
    if err := h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditUpdate,
        database.BulkAuditSummary{Count: updated, Filter: filterDesc}); err != nil {
        writeError(w, http.StatusInternalServerError, "audit summary")
        return
    }

    if err := tx.Commit(); err != nil {
        writeError(w, http.StatusInternalServerError, "commit")
        return
    }

    if patch.Date != nil {
        h.verifyAffectedCheckpoints(r.Context(), minDate)  // minDate is zero for no-tags path → "reverify all"
    }

    writeJSON(w, http.StatusOK, map[string]int64{"updated": updated})
}

// runUpdateByFilterNoTags issues a single UPDATE inside the caller's tx.
// The SQL shape mirrors handleDeleteTransactionsByFilter:917-949 — same
// helpers, same JOIN, same user_id append, same live-filter wrap.
//
// Soft-delete invariant: appendLiveTransactionsFilter adds
// "AND t.deleted_at IS NULL" to the inner subquery (load-bearing); the
// "AND deleted_at IS NULL" on the outer UPDATE is defense-in-depth. Both
// are required.
func (h *Handler) runUpdateByFilterNoTags(r *http.Request, tx *sql.Tx, user database.User, patch database.UpdatePatch) (int64, time.Time, error) {
    var setClauses []string
    var args []any
    if patch.Date != nil {
        setClauses = append(setClauses, "date = ?")
        args = append(args, *patch.Date)
    }
    if patch.Description != nil {
        setClauses = append(setClauses, "description = ?")
        args = append(args, *patch.Description)
    }
    if patch.CategoryID != nil {
        setClauses = append(setClauses, "category_id = ?")
        args = append(args, *patch.CategoryID)
    }
    setClauses = append(setClauses, "updated_at = CURRENT_TIMESTAMP")

    whereClause, whereArgs := buildTransactionWhereClause(r.URL.Query())
    if user.Role != RoleAdmin {
        if whereClause == "" {
            whereClause = " WHERE t.user_id = ?"
        } else {
            whereClause += " AND t.user_id = ?"
        }
        whereArgs = append(whereArgs, user.ID)
    }
    liveClause := appendLiveTransactionsFilter(whereClause)

    query := fmt.Sprintf(
        `UPDATE transactions SET %s WHERE deleted_at IS NULL AND id IN (
            SELECT t.id FROM transactions t JOIN categories c ON t.category_id = c.id %s
        )`,
        strings.Join(setClauses, ", "),
        liveClause,
    )

    res, err := tx.ExecContext(r.Context(), query, append(args, whereArgs...)...)
    if err != nil {
        return 0, time.Time{}, fmt.Errorf("exec update: %w", err)
    }
    n, _ := res.RowsAffected()
    return n, time.Time{}, nil  // no per-row date enumeration in fast path
}
```

- [ ] **Step 4.4: Wire the route**

In `router.go`, alongside the new `batch-update`:

```go
r.Post("/transactions/update-by-filter", h.handleUpdateTransactionsByFilter)
```

- [ ] **Step 4.5: Run tests** — Expected: PASS for all four no-tags tests above.

- [ ] **Step 4.6: Add audit invariant tests**

In `transaction_audit_test.go`:

```go
func TestAudit_UpdateByFilter_WritesOneSummaryRow_WithFilterAndPatch(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    catB := seedTestCategory(t, fix.q, "CatB")
    seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10, "")

    postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat),
        map[string]any{"patch": map[string]any{"category_id": catB}}, withUser(user))

    audits := listAuditRows(t, db)
    var summaries []map[string]any
    for _, a := range audits {
        if a["transaction_id"].(int64) == 0 && a["action"].(string) == "update" {
            summaries = append(summaries, a)
        }
    }
    if len(summaries) != 1 {
        t.Errorf("expected 1 summary, got %d", len(summaries))
    }
    bj := summaries[0]["before_json"].(sql.NullString)
    if !bj.Valid || !strings.Contains(bj.String, "category_id=") || !strings.Contains(bj.String, fmt.Sprintf("category=%d", cat)) {
        t.Errorf("summary JSON missing patch or filter: %s", bj.String)
    }
}

func TestAudit_UpdateByFilter_ZeroMatches_StillEmitsSummary(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    catB := seedTestCategory(t, fix.q, "CatB")

    postJSON(t, h, "/api/transactions/update-by-filter?search=nomatch",
        map[string]any{"patch": map[string]any{"category_id": catB}}, withUser(user))

    audits := listAuditRows(t, db)
    var count int
    for _, a := range audits {
        if a["transaction_id"].(int64) == 0 { count++ }
    }
    if count != 1 {
        t.Errorf("zero-match should still emit summary; got %d audit rows", count)
    }
}
```

- [ ] **Step 4.7: Run audit tests + full suite** — Expected: ALL pass.

- [ ] **Step 4.8: Dispatch data-correctness reviewer**

- [ ] **Step 4.9: Commit Task 4**

```
git add internal/api/transaction_handlers.go internal/api/router.go internal/api/transaction_handlers_test.go internal/api/transaction_audit_test.go
git commit -m "feat(api): add update-by-filter no-tags fast path"
```

---

### Task 5: `handleUpdateTransactionsByFilter` — tags read-then-write path

**Files:**
- Modify: `internal/api/transaction_handlers.go` — add `runUpdateByFilterTags`, replace the `NotImplemented` stub
- Modify: `internal/api/transaction_handlers_test.go` — tag-mode tests for the filter path

**Goal:** complete the filter handler's tags branch. Reads each matching row's current tags, computes new tags via `applyTagsMode`, writes them back per-row, all inside one tx. Still emits a single summary audit row.

- [ ] **Step 5.1: Write the failing tests**

```go
func TestUpdateByFilter_TagsAdd_PerRowReadThenWrite(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id1 := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10, "old1")
    id2 := seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "Y", 10, "old2")

    body := map[string]any{
        "patch":    map[string]any{"tags": "fresh"},
        "tagsMode": "add",
    }
    rec := postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat), body, withUser(user))
    if rec.Code != 200 { t.Fatal(rec.Body.String()) }

    var t1, t2 string
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id1).Scan(&t1)
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id2).Scan(&t2)
    if t1 != "old1, fresh" { t.Errorf("id1: %q, want %q", t1, "old1, fresh") }
    if t2 != "old2, fresh" { t.Errorf("id2: %q, want %q", t2, "old2, fresh") }
}

func TestUpdateByFilter_TagsRemove_DropsMatchingItems(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10, "tax,personal,receipt")

    body := map[string]any{"patch": map[string]any{"tags": "personal"}, "tagsMode": "remove"}
    postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat), body, withUser(user))

    var got string
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id).Scan(&got)
    if got != "tax, receipt" {
        t.Errorf("got %q, want %q", got, "tax, receipt")
    }
}

func TestUpdateByFilter_TagsReplace_OverwritesAllRows(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id1 := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10, "old1")
    id2 := seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "Y", 10, "old2,old3")

    body := map[string]any{"patch": map[string]any{"tags": "fresh"}, "tagsMode": "replace"}
    postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat), body, withUser(user))

    for _, id := range []int64{id1, id2} {
        var got string
        db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, id).Scan(&got)
        if got != "fresh" { t.Errorf("id=%d: %q", id, got) }
    }
}

func TestUpdateByFilter_TagsPath_StillWritesOneSummary(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10, "")
    seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "Y", 10, "")
    seedTestTransaction(t, fix.q, user, cat, "2026-04-03", "Z", 10, "")

    postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat),
        map[string]any{"patch": map[string]any{"tags": "fresh"}, "tagsMode": "add"}, withUser(user))

    audits := listAuditRows(t, db)
    var summaryCount int
    for _, a := range audits {
        if a["transaction_id"].(int64) == 0 { summaryCount++ }
    }
    if summaryCount != 1 {
        t.Errorf("expected 1 summary, got %d (per-row leaked into filter mode)", summaryCount)
    }
}

func TestUpdateByFilter_Tags_HidesTombstoned(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    live := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "live", 10, "old")
    ts := seedTestTransaction(t, fix.q, user, cat, "2026-04-02", "ts", 10, "should-not-touch")
    db.Exec(`UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, ts)

    postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat),
        map[string]any{"patch": map[string]any{"tags": "fresh"}, "tagsMode": "add"}, withUser(user))

    var liveTags, tsTags string
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, live).Scan(&liveTags)
    db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, ts).Scan(&tsTags)
    if liveTags != "old, fresh" { t.Errorf("live: %q", liveTags) }
    if tsTags != "should-not-touch" { t.Errorf("tombstoned was touched! got %q", tsTags) }
}
```

- [ ] **Step 5.2: Run failing tests** — Expected: 501 NotImplemented (the stub).

- [ ] **Step 5.3: Implement `runUpdateByFilterTags`**

Replace the `NotImplemented` stub branch in `handleUpdateTransactionsByFilter` with a call to a new helper:

```go
if patch.Tags != nil {
    updated, minDate, err = h.runUpdateByFilterTags(r, tx, user, patch)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "internal server error")
        return
    }
}
```

Add the helper:

```go
// runUpdateByFilterTags enumerates matching rows, computes new tags per row
// via applyTagsMode, and writes them back. All inside the caller's tx so
// rollback covers the whole batch. minDate is computed from before.Date
// across enumerated rows so verifyAffectedCheckpoints can use a precise
// floor when patch.Date is also set.
//
// SQL scaffolding mirrors runUpdateByFilterNoTags (same buildTransactionWhereClause
// + user_id append + appendLiveTransactionsFilter + JOIN categories) but
// the inner statement is a SELECT instead of an UPDATE.
func (h *Handler) runUpdateByFilterTags(r *http.Request, tx *sql.Tx, user database.User, patch database.UpdatePatch) (int64, time.Time, error) {
    whereClause, whereArgs := buildTransactionWhereClause(r.URL.Query())
    if user.Role != RoleAdmin {
        if whereClause == "" {
            whereClause = " WHERE t.user_id = ?"
        } else {
            whereClause += " AND t.user_id = ?"
        }
        whereArgs = append(whereArgs, user.ID)
    }
    liveClause := appendLiveTransactionsFilter(whereClause)

    selectQuery := fmt.Sprintf(
        `SELECT t.id, t.tags, t.date FROM transactions t JOIN categories c ON t.category_id = c.id %s`,
        liveClause,
    )

    rows, err := tx.QueryContext(r.Context(), selectQuery, whereArgs...)
    if err != nil { return 0, time.Time{}, fmt.Errorf("select matching: %w", err) }
    defer rows.Close()

    type row struct {
        id   int64
        tags sql.NullString
        date string
    }
    var matched []row
    for rows.Next() {
        var rr row
        if err := rows.Scan(&rr.id, &rr.tags, &rr.date); err != nil {
            return 0, time.Time{}, fmt.Errorf("scan: %w", err)
        }
        matched = append(matched, rr)
    }
    if err := rows.Err(); err != nil {
        return 0, time.Time{}, fmt.Errorf("rows.Err: %w", err)
    }
    rows.Close()  // explicit so the next Exec doesn't fight for connection

    var minDate time.Time
    for _, m := range matched {
        existing := ""
        if m.tags.Valid { existing = m.tags.String }
        merged := applyTagsMode(existing, *patch.Tags, *patch.TagsMode)

        var setClauses []string
        var setArgs []any
        if patch.Date != nil {
            setClauses = append(setClauses, "date = ?")
            setArgs = append(setArgs, *patch.Date)
        }
        if patch.Description != nil {
            setClauses = append(setClauses, "description = ?")
            setArgs = append(setArgs, *patch.Description)
        }
        if patch.CategoryID != nil {
            setClauses = append(setClauses, "category_id = ?")
            setArgs = append(setArgs, *patch.CategoryID)
        }
        setClauses = append(setClauses, "tags = ?", "updated_at = CURRENT_TIMESTAMP")
        setArgs = append(setArgs, merged)
        setArgs = append(setArgs, m.id)

        upd := fmt.Sprintf("UPDATE transactions SET %s WHERE id = ? AND deleted_at IS NULL", strings.Join(setClauses, ", "))
        if _, err := tx.ExecContext(r.Context(), upd, setArgs...); err != nil {
            return 0, time.Time{}, fmt.Errorf("update id=%d: %w", m.id, err)
        }

        if patch.Date != nil {
            d, _ := time.Parse("2006-01-02", m.date)
            minDate = earliestDate(minDate, d)
            after, _ := time.Parse("2006-01-02", *patch.Date)
            minDate = earliestDate(minDate, after)
        }
    }

    return int64(len(matched)), minDate, nil
}
```

- [ ] **Step 5.4: Run tests** — Expected: PASS for all five tag-filter tests.

- [ ] **Step 5.5: Verify mutual-exclusivity — checkpoint hook fires once**

```go
func TestUpdateByFilter_TagsAndDate_FiresCheckpointHookOnce(t *testing.T) {
    h, db, fix := setupHandlerTest(t)
    user := seedTestUser(t, fix.q, "alice")
    cat := seedTestCategory(t, fix.q, "Cat")
    id := seedTestTransaction(t, fix.q, user, cat, "2026-04-01", "X", 10, "old")

    body := map[string]any{
        "patch":    map[string]any{"tags": "fresh", "date": "2026-05-01"},
        "tagsMode": "add",
    }
    rec := postJSON(t, h, fmt.Sprintf("/api/transactions/update-by-filter?category=%d", cat), body, withUser(user))
    if rec.Code != 200 { t.Fatal(rec.Body.String()) }

    var date, tags string
    db.QueryRow(`SELECT date, tags FROM transactions WHERE id = ?`, id).Scan(&date, &tags)
    if date != "2026-05-01" || tags != "old, fresh" {
        t.Errorf("got date=%q tags=%q", date, tags)
    }
    audits := listAuditRows(t, db)
    var summaries int
    for _, a := range audits {
        if a["transaction_id"].(int64) == 0 { summaries++ }
    }
    if summaries != 1 { t.Errorf("expected 1 summary, got %d", summaries) }
}
```

- [ ] **Step 5.6: Run full backend suite** — Expected: ALL pass.

- [ ] **Step 5.7: Dispatch data-correctness reviewer**

- [ ] **Step 5.8: Commit Task 5**

```
git add internal/api/transaction_handlers.go internal/api/transaction_handlers_test.go internal/api/transaction_audit_test.go
git commit -m "feat(api): add update-by-filter tags read-then-write path"
```

## Chunk 4: Frontend prerequisites + hook (Tasks 6–7)

### Task 6: shadcn radio-group install + types

**Files:**
- Run shadcn CLI to add: `web/src/components/ui/radio-group.tsx`
- Modify: `web/src/api/types.ts` — add bulk-edit request/response types

**Goal:** install missing shadcn primitives + define the wire-format types.

- [ ] **Step 6.1: Install radio-group via shadcn CLI**

```
cd D:/claude/SpenDrop/web
pnpm dlx shadcn@latest add radio-group
```

Verify: `ls web/src/components/ui/radio-group.tsx`. Do NOT hand-write this file.

- [ ] **Step 6.2: Verify checkbox is already installed**

```
ls web/src/components/ui/checkbox.tsx
```

If absent: `pnpm dlx shadcn@latest add checkbox`.

- [ ] **Step 6.3: Run tsc — Expected: clean.**

- [ ] **Step 6.4: Add types to `web/src/api/types.ts`**

```typescript
export interface BulkUpdatePatch {
  date?: string;
  description?: string;
  category_id?: number;
  tags?: string;
}

export type BulkUpdateTagsMode = 'add' | 'remove' | 'replace';

export interface BatchUpdateRequest {
  ids: number[];
  patch: BulkUpdatePatch;
  tagsMode?: BulkUpdateTagsMode;
}

export interface BulkUpdateByFilterRequest {
  patch: BulkUpdatePatch;
  tagsMode?: BulkUpdateTagsMode;
}

export interface BulkUpdateResponse {
  updated: number;
  skipped?: number;  // present on batch-update only
}
```

- [ ] **Step 6.5: Commit Task 6**

```
git add web/src/components/ui/radio-group.tsx web/src/api/types.ts
git commit -m "chore(deps): install shadcn radio-group + add bulk-edit types"
```

(Add `web/package.json` + lockfile to the commit only if shadcn modified them.)

---

### Task 7: useTransactions hook additions + RefetchAfterMutationError

**Files:**
- Modify: `web/src/hooks/useTransactions.ts` — add `bulkUpdate`, `bulkUpdateByFilter`, refactor `fetchTransactions`, export `RefetchAfterMutationError`
- Test: extend or create `web/src/hooks/useTransactions.test.tsx`

- [ ] **Step 7.1: Read existing `useTransactions` hook**

`web/src/hooks/useTransactions.ts` lines ~138-152 (`fetchTransactions`) and ~233-245 (`deleteByFilter`). The new methods follow `deleteByFilter`'s shape but additionally return the post-mutation transactions array.

- [ ] **Step 7.2: Write failing test (mock api)**

In `web/src/hooks/useTransactions.test.tsx` (create if absent):

```tsx
import { renderHook, act, waitFor } from '@testing-library/react';
import { useTransactions } from './useTransactions';
import { api } from '../api/client';
import { vi, test, expect, beforeEach } from 'vitest';

vi.mock('../api/client');

beforeEach(() => { vi.clearAllMocks(); });

test('bulkUpdate returns the visible IDs from the post-PATCH refetch', async () => {
  vi.mocked(api.post).mockResolvedValueOnce({ updated: 2, skipped: 0 });
  vi.mocked(api.get).mockResolvedValueOnce({
    transactions: [{ id: 1 }, { id: 3 }, { id: 5 }],
    total: 3, page: 1, per_page: 25,
  });
  const { result } = renderHook(() => useTransactions());
  await waitFor(() => expect(result.current.initialLoad).toBe(false));

  let response;
  await act(async () => {
    response = await result.current.bulkUpdate({
      ids: [1, 2, 3], patch: { category_id: 5 },
    });
  });
  expect(response).toEqual({ updated: 2, skipped: 0, visibleIds: [1, 3, 5] });
});

test('bulkUpdate wraps refetch failure in RefetchAfterMutationError', async () => {
  vi.mocked(api.post).mockResolvedValueOnce({ updated: 2, skipped: 0 });
  vi.mocked(api.get).mockRejectedValueOnce(new Error('network blip'));
  const { result } = renderHook(() => useTransactions());

  await expect(result.current.bulkUpdate({
    ids: [1, 2], patch: { category_id: 5 },
  })).rejects.toMatchObject({ name: 'RefetchAfterMutationError' });
});
```

- [ ] **Step 7.3: Run failing test — Expected: FAIL with "result.current.bulkUpdate is not a function".**

- [ ] **Step 7.4: Refactor `fetchTransactions` to expose a Promise**

```ts
const fetchTransactionsAsync = useCallback(async (): Promise<TransactionsResponse> => {
  const url = buildQuery(/* same as before */);
  const response = await api.get<TransactionsResponse>(url);
  setTransactions(response.transactions);
  setTotal(response.total);
  // ... same state updates as fetchTransactions does today
  return response;
}, [/* same deps */]);

const fetchTransactions = useCallback(() => {
  fetchTransactionsAsync().catch((err) => setError(err.message));
}, [fetchTransactionsAsync]);
```

- [ ] **Step 7.5: Add `RefetchAfterMutationError`**

```ts
export class RefetchAfterMutationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RefetchAfterMutationError';
  }
}
```

- [ ] **Step 7.6: Add `bulkUpdate` and `bulkUpdateByFilter`**

```ts
const bulkUpdate = useCallback(async (
  args: BatchUpdateRequest,
): Promise<BulkUpdateResponse & { visibleIds: number[] }> => {
  const result = await api.post<BulkUpdateResponse>('transactions/batch-update', args);
  let refreshed: TransactionsResponse;
  try {
    refreshed = await fetchTransactionsAsync();
  } catch (err) {
    throw new RefetchAfterMutationError(err instanceof Error ? err.message : String(err));
  }
  return { ...result, visibleIds: refreshed.transactions.map((t) => t.id) };
}, [fetchTransactionsAsync]);

const bulkUpdateByFilter = useCallback(async (
  args: BulkUpdateByFilterRequest & { filterQuery: string },
): Promise<BulkUpdateResponse> => {
  const { filterQuery, ...body } = args;
  const result = await api.post<BulkUpdateResponse>(
    `transactions/update-by-filter?${filterQuery}`, body,
  );
  try {
    await fetchTransactionsAsync();
  } catch (err) {
    throw new RefetchAfterMutationError(err instanceof Error ? err.message : String(err));
  }
  return result;
}, [fetchTransactionsAsync]);
```

Add both methods to the hook's return value.

- [ ] **Step 7.7: Run frontend tests — Expected: PASS.**

- [ ] **Step 7.8: Run tsc — Expected: clean.**

- [ ] **Step 7.9: Run full vitest — Expected: ALL pass (regression check on existing `fetchTransactions` callsites).**

- [ ] **Step 7.10: Commit Task 7**

```
git add web/src/hooks/useTransactions.ts web/src/hooks/useTransactions.test.tsx
git commit -m "feat(ui): add bulkUpdate / bulkUpdateByFilter hook methods"
```

---

## Chunk 5: Frontend dialog + confirm dialog (Tasks 8–9)

### Task 8: BulkEditDialog

**Files:**
- Create: `web/src/pages/Transactions/BulkEditDialog.tsx`
- Create: `web/src/pages/Transactions/computePatch.ts`
- Test: `web/src/pages/Transactions/Transactions.bulkEdit.test.tsx`

- [ ] **Step 8.1: Write failing test for the dialog skeleton**

Create `web/src/pages/Transactions/Transactions.bulkEdit.test.tsx`:

```tsx
import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { BulkEditDialog } from './BulkEditDialog';

vi.mock('../../hooks/useCategories', () => ({
  useCategories: () => ({
    categories: [
      { id: 1, name: 'Groceries', type: 'expense' },
      { id: 2, name: 'Cleaning', type: 'expense' },
    ],
    loading: false,
  }),
}));

describe('BulkEditDialog', () => {
  test('opens with all fields at noChange / empty', async () => {
    render(<BulkEditDialog open={true} onClose={() => {}} count={12} onSubmit={() => {}} />);
    expect(screen.getByRole('heading', { name: /edit 12/i })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: /set date/i })).not.toBeChecked();
    expect(screen.getByPlaceholderText(/keep same/i)).toBeInTheDocument();
    expect(screen.getByText(/no change/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /apply to 12/i })).toBeDisabled();
  });

  test('Apply enables when category is changed', async () => {
    const user = userEvent.setup();
    render(<BulkEditDialog open={true} onClose={() => {}} count={12} onSubmit={() => {}} />);
    await user.click(screen.getByRole('combobox', { name: /category/i }));
    await user.click(screen.getByRole('option', { name: /groceries/i }));
    expect(screen.getByRole('button', { name: /apply to 12/i })).toBeEnabled();
  });

  test('Tags radio is disabled while tag input is empty', async () => {
    render(<BulkEditDialog open={true} onClose={() => {}} count={12} onSubmit={() => {}} />);
    expect(screen.getByRole('radio', { name: /add/i })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /remove/i })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /replace/i })).toBeDisabled();
  });

  test('Tags radio enables when user types in the tag input', async () => {
    const user = userEvent.setup();
    render(<BulkEditDialog open={true} onClose={() => {}} count={12} onSubmit={() => {}} />);
    await user.type(screen.getByLabelText(/tags/i), 'tax');
    expect(screen.getByRole('radio', { name: /add/i })).toBeEnabled();
  });

  test('Submit calls onSubmit with only dirty fields in the patch', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<BulkEditDialog open={true} onClose={() => {}} count={12} onSubmit={onSubmit} />);
    await user.click(screen.getByRole('combobox', { name: /category/i }));
    await user.click(screen.getByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /apply to 12/i }));
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ patch: { category_id: 1 } })
    );
    expect(onSubmit.mock.calls[0][0].tagsMode).toBeUndefined();
  });
});
```

- [ ] **Step 8.2: Run failing test — Expected: cannot find module `./BulkEditDialog`.**

- [ ] **Step 8.3: Implement `computePatch.ts`**

```ts
import type { BulkUpdatePatch, BulkUpdateTagsMode } from '../../api/types';

export interface BulkEditFormValues {
  setDate: boolean;
  date: string;
  description: string;
  category_id: number | 'noChange';
  tags: string;
  tagsMode: BulkUpdateTagsMode;
}

export interface ComputedPatchResult {
  patch: BulkUpdatePatch;
  tagsMode?: BulkUpdateTagsMode;
}

export function computePatch(values: BulkEditFormValues): ComputedPatchResult {
  const patch: BulkUpdatePatch = {};
  if (values.setDate && values.date) patch.date = values.date;
  const desc = values.description.trim();
  if (desc) patch.description = desc;
  if (values.category_id !== 'noChange') patch.category_id = values.category_id;
  const tags = values.tags.trim();
  if (tags) {
    patch.tags = tags;
    return { patch, tagsMode: values.tagsMode };
  }
  return { patch };
}

export function isPatchEmpty(p: ComputedPatchResult): boolean {
  return Object.keys(p.patch).length === 0;
}
```

- [ ] **Step 8.4: Implement `BulkEditDialog.tsx`**

Skeleton — full implementation follows spec §3.1, §3.2, §4.x:

```tsx
import { useForm, Controller } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { useCategories } from '../../hooks/useCategories';
import { computePatch, isPatchEmpty, type BulkEditFormValues, type ComputedPatchResult } from './computePatch';

const bulkEditSchema = z.object({
  setDate: z.boolean(),
  date: z.string(),
  description: z.string().max(500),
  category_id: z.union([z.literal('noChange'), z.number().int().positive()]),
  tags: z.string(),
  tagsMode: z.enum(['add', 'remove', 'replace']),
});

interface Props {
  open: boolean;
  onClose: () => void;
  count: number;
  onSubmit: (result: ComputedPatchResult) => void;
}

export function BulkEditDialog({ open, onClose, count, onSubmit }: Props) {
  const { categories } = useCategories();
  const form = useForm<BulkEditFormValues>({
    resolver: zodResolver(bulkEditSchema),
    defaultValues: {
      setDate: false, date: '', description: '',
      category_id: 'noChange', tags: '', tagsMode: 'add',
    },
  });
  const values = form.watch();
  const computed = computePatch(values);
  const canSubmit = !isPatchEmpty(computed);
  const tagsEmpty = !values.tags.trim();
  const dateEnabled = values.setDate;

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="md:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Edit {count} transactions</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={form.handleSubmit(() => onSubmit(computed))}
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
              e.preventDefault();
              if (canSubmit) form.handleSubmit(() => onSubmit(computed))();
            }
          }}
          className="grid grid-cols-1 md:grid-cols-[120px_1fr_140px_1fr] gap-4"
        >
          {/* Date column */}
          <div className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
              <Controller
                name="setDate" control={form.control}
                render={({ field }) => (
                  <Checkbox id="bulk-set-date" checked={field.value} onCheckedChange={field.onChange} />
                )}
              />
              <Label htmlFor="bulk-set-date" className="text-xs">Set date</Label>
            </div>
            <Input type="date" {...form.register('date')} disabled={!dateEnabled} aria-label="Date" />
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="bulk-desc">Description</Label>
            <Input id="bulk-desc" placeholder="— Keep same —" {...form.register('description')} />
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="bulk-cat">Category</Label>
            <Controller
              name="category_id" control={form.control}
              render={({ field }) => (
                <Select
                  value={field.value === 'noChange' ? 'noChange' : String(field.value)}
                  onValueChange={(v) => field.onChange(v === 'noChange' ? 'noChange' : Number(v))}
                >
                  <SelectTrigger id="bulk-cat" aria-label="Category"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="noChange">— No change —</SelectItem>
                    {categories.map((c) => (
                      <SelectItem key={c.id} value={String(c.id)}>{c.name}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            />
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="bulk-tags">Tags</Label>
            <Controller
              name="tagsMode" control={form.control}
              render={({ field }) => (
                <RadioGroup
                  value={field.value} onValueChange={field.onChange}
                  className="flex gap-3 text-xs"
                  aria-labelledby="bulk-tags-mode-legend"
                >
                  <span id="bulk-tags-mode-legend" className="sr-only">Tag operation</span>
                  <div className="flex items-center gap-1">
                    <RadioGroupItem value="add" id="m-add" disabled={tagsEmpty} />
                    <Label htmlFor="m-add">Add</Label>
                  </div>
                  <div className="flex items-center gap-1">
                    <RadioGroupItem value="remove" id="m-rm" disabled={tagsEmpty} />
                    <Label htmlFor="m-rm">Remove</Label>
                  </div>
                  <div className="flex items-center gap-1">
                    <RadioGroupItem value="replace" id="m-rep" disabled={tagsEmpty} />
                    <Label htmlFor="m-rep">Replace</Label>
                  </div>
                </RadioGroup>
              )}
            />
            <Input id="bulk-tags" placeholder="comma,separated" {...form.register('tags')} />
          </div>

          <DialogFooter className="md:col-span-4 flex justify-end gap-2 mt-4">
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={!canSubmit}
              aria-label={`Apply changes to ${count} transactions`}>
              Apply to {count}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
```

(Adjust import paths per the existing project conventions. The category-select binding may need iteration — RHF + shadcn Select are notoriously fiddly. Iterate against the test until it passes.)

- [ ] **Step 8.5: Run dialog tests — Expected: PASS for all five tests.**

- [ ] **Step 8.6: Run tsc — Expected: clean.**

- [ ] **Step 8.7: Commit Task 8**

```
git add web/src/pages/Transactions/BulkEditDialog.tsx web/src/pages/Transactions/computePatch.ts web/src/pages/Transactions/Transactions.bulkEdit.test.tsx
git commit -m "feat(ui): add BulkEditDialog with computePatch helper"
```

---

### Task 9: BulkEditConfirmDialog (all-matching scope)

**Files:**
- Create: `web/src/pages/Transactions/BulkEditConfirmDialog.tsx`
- Test: extend `web/src/pages/Transactions/Transactions.bulkEdit.test.tsx`

- [ ] **Step 9.1: Write failing test**

```tsx
import { BulkEditConfirmDialog } from './BulkEditConfirmDialog';

describe('BulkEditConfirmDialog', () => {
  test('summarizes the patch + count', () => {
    render(
      <BulkEditConfirmDialog
        open={true} onCancel={() => {}} onConfirm={() => {}}
        count={1247}
        patch={{ patch: { category_id: 5 }, tagsMode: undefined }}
        categoryName={(id) => id === 5 ? 'Groceries' : 'unknown'}
      />
    );
    expect(screen.getByRole('alertdialog')).toBeInTheDocument();
    expect(screen.getByText(/category/i)).toBeInTheDocument();
    expect(screen.getByText(/groceries/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /apply to 1,?247/i })).toBeInTheDocument();
  });

  test('truncates long descriptions at 80 chars with ellipsis', () => {
    const long = 'a'.repeat(120);
    render(
      <BulkEditConfirmDialog
        open={true} onCancel={() => {}} onConfirm={() => {}}
        count={5}
        patch={{ patch: { description: long } }}
        categoryName={() => ''}
      />
    );
    const node = screen.getByText((content) => content.includes('…'));
    expect(node).toBeInTheDocument();
  });

  test('confirm fires onConfirm', async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(
      <BulkEditConfirmDialog
        open={true} onCancel={() => {}} onConfirm={onConfirm}
        count={3} patch={{ patch: { category_id: 5 } }} categoryName={() => 'Groceries'}
      />
    );
    await user.click(screen.getByRole('button', { name: /apply to 3/i }));
    expect(onConfirm).toHaveBeenCalled();
  });
});
```

- [ ] **Step 9.2: Run failing test — Expected: module not found.**

- [ ] **Step 9.3: Implement BulkEditConfirmDialog**

```tsx
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import type { ComputedPatchResult } from './computePatch';

const TRUNCATE_AT = 80;

function trunc(s: string): string {
  return s.length > TRUNCATE_AT ? s.slice(0, TRUNCATE_AT) + '…' : s;
}

interface Props {
  open: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  count: number;
  patch: ComputedPatchResult;
  categoryName: (id: number) => string;
}

export function BulkEditConfirmDialog({ open, onCancel, onConfirm, count, patch, categoryName }: Props) {
  const lines: string[] = [];
  if (patch.patch.date) lines.push(`Date → ${patch.patch.date}`);
  if (patch.patch.description) lines.push(`Description → "${trunc(patch.patch.description)}"`);
  if (patch.patch.category_id) lines.push(`Category → ${categoryName(patch.patch.category_id)}`);
  if (patch.patch.tags) lines.push(`Tags ${patch.tagsMode}: ${trunc(patch.patch.tags)}`);

  return (
    <AlertDialog open={open} onOpenChange={(o) => { if (!o) onCancel(); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Apply changes to {count} transactions?</AlertDialogTitle>
          <AlertDialogDescription>
            <ul className="text-sm space-y-1 mt-2">
              {lines.map((l, i) => <li key={i}>{l}</li>)}
            </ul>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={onCancel}>Cancel</AlertDialogCancel>
          {/* Default primary palette — bulk-edit is recoverable, not destructive */}
          <AlertDialogAction onClick={onConfirm}>Apply to {count}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
```

- [ ] **Step 9.4: Run tests — Expected: PASS.**

- [ ] **Step 9.5: Commit Task 9**

```
git add web/src/pages/Transactions/BulkEditConfirmDialog.tsx web/src/pages/Transactions/Transactions.bulkEdit.test.tsx
git commit -m "feat(ui): add BulkEditConfirmDialog for all-matching scope"
```

---

## Chunk 6: Page integration + docs (Tasks 10–11)

### Task 10: Transactions.tsx integration

**Files:**
- Modify: `web/src/pages/Transactions.tsx`
- Test: extend `web/src/pages/Transactions.test.tsx`

**Goal:** end-to-end wiring. The "Edit (N)" trigger button slots into the existing selection action bar; clicking opens the dialog; submit dispatches to either `bulkUpdate` or `bulkUpdateByFilter` based on `selectionScope`; success prunes selection + closes dialog + toasts.

- [ ] **Step 10.1: Read existing Transactions.tsx selection action bar**

Around lines 757-812 (per spec §6 references). The bar already has Delete + Clear buttons. The new "Edit ({selectionCount})" button slots in alongside Delete.

- [ ] **Step 10.2: Write failing integration tests**

Append to `Transactions.test.tsx`:

```tsx
test('Edit button only renders when selectionCount > 0', async () => {
  // render Transactions in page mode with no selection
  expect(screen.queryByRole('button', { name: /edit \(/i })).not.toBeInTheDocument();
});

test('Clicking Edit opens BulkEditDialog with N=selectedCount', async () => {
  // select 3 rows
  await user.click(screen.getByRole('button', { name: /edit \(3\)/i }));
  expect(screen.getByRole('heading', { name: /edit 3/i })).toBeInTheDocument();
});

test('Submit in page mode dispatches bulkUpdate, NOT bulkUpdateByFilter', async () => {
  // mocks + select rows + open dialog + change category + submit
  expect(mockedApi.post).toHaveBeenCalledWith('transactions/batch-update', expect.any(Object));
  expect(mockedApi.post).not.toHaveBeenCalledWith(
    expect.stringContaining('update-by-filter'), expect.any(Object),
  );
});

test('Submit in all-matching mode opens confirm dialog first', async () => {
  // select-all-matching
  await user.click(screen.getByRole('button', { name: /edit \(\d+\)/i }));
  await user.click(screen.getByRole('button', { name: /apply to/i }));
  expect(screen.getByRole('alertdialog')).toBeInTheDocument();
  expect(mockedApi.post).not.toHaveBeenCalled();  // not yet fired
  await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: /apply to/i }));
  expect(mockedApi.post).toHaveBeenCalledWith(
    expect.stringContaining('update-by-filter'), expect.any(Object),
  );
});

test('Successful submit prunes selectedIds to refetched IDs', async () => {
  // seed 3 rows, select all 3, change category in patch (kicks them off
  // the current filter so refetch returns 0 rows)
  // After submit, "Edit (3)" disappears, toast says "...selection cleared..."
});
```

- [ ] **Step 10.3: Add the Edit button + dialog state to Transactions.tsx**

Inside the selection action bar (around the existing Delete button):

```tsx
{selectionCount > 0 && (
  <Button size="sm" onClick={() => setBulkEditOpen(true)}>
    Edit ({selectionCount})
  </Button>
)}
```

State:

```tsx
const [bulkEditOpen, setBulkEditOpen] = useState(false);
const [bulkConfirm, setBulkConfirm] = useState<ComputedPatchResult | null>(null);
```

- [ ] **Step 10.4: Wire the submit flow**

```tsx
async function dispatchBulkEdit(p: ComputedPatchResult) {
  const isFilterMode = selectionScope === 'all-matching';
  try {
    if (isFilterMode) {
      const filterQuery = buildFilterQuery(filters);
      const { updated } = await bulkUpdateByFilter({ filterQuery, ...p });
      const noun = (n: number) => `${n} transaction${n === 1 ? '' : 's'}`;
      toast.success(`Updated ${noun(updated)}`);
      handleClearSelection();
    } else {
      const { updated, skipped, visibleIds } = await bulkUpdate({
        ids: [...selectedIds], ...p,
      });
      const visible = new Set(visibleIds);
      const prevSize = selectedIds.size;
      const newSelection = new Set([...selectedIds].filter((id) => visible.has(id)));
      setSelectedIds(newSelection);
      const dropped = prevSize - newSelection.size;
      const noun = (n: number) => `${n} transaction${n === 1 ? '' : 's'}`;
      const head = updated === 0 && skipped && skipped > 0
        ? `No matches updated; skipped ${noun(skipped)}`
        : `Updated ${noun(updated)}${skipped ? `, skipped ${skipped}` : ''}`;
      const tail = dropped > 0
        ? ' (selection cleared — rows no longer match the current filter)'
        : '';
      toast.success(head + tail);
    }
    setBulkEditOpen(false);
    setBulkConfirm(null);
  } catch (err) {
    if (err instanceof RefetchAfterMutationError) {
      toast.error(`Update applied, but refresh failed — please reload to see the latest. (${err.message})`);
    } else {
      toast.error(`Update failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }
}
```

- [ ] **Step 10.5: Wire the dialog's onSubmit**

```tsx
<BulkEditDialog
  open={bulkEditOpen}
  onClose={() => setBulkEditOpen(false)}
  count={selectionCount}
  onSubmit={(p) => {
    if (selectionScope === 'all-matching') {
      setBulkConfirm(p);
    } else {
      void dispatchBulkEdit(p);
    }
  }}
/>

{bulkConfirm && (
  <BulkEditConfirmDialog
    open={true}
    onCancel={() => setBulkConfirm(null)}
    onConfirm={() => { void dispatchBulkEdit(bulkConfirm); }}
    count={selectionCount}
    patch={bulkConfirm}
    categoryName={(id) => categories.find((c) => c.id === id)?.name ?? ''}
  />
)}
```

- [ ] **Step 10.6: Run tests — Expected: PASS.**

- [ ] **Step 10.7: Run tsc + full vitest — Expected: clean + all pass.**

- [ ] **Step 10.8: Manual UX check (browser)**

```
docker compose -f docker-compose.dev.yml up --build -d
```

Open http://localhost:3535/transactions, log in, walk through:
1. Page-mode bulk-edit: search "cleaning", select 3 rows, change category. Toast says "Updated 3 transactions". Selection persists.
2. All-matching mode: click the banner, change category. Confirm dialog appears with changed-fields list. Click Apply. Toast says "Updated N transactions".
3. Tags Add: select 2 rows with different existing tags, Add `["tax"]`, verify each row got tax appended.
4. Empty patch: open dialog, don't change anything, Apply stays disabled.
5. Cmd/Ctrl+Enter submits.
6. Esc closes the dialog.
7. Network failure: kill backend mid-submit. Verify error toast says "Update failed: ..." and dialog stays open.

- [ ] **Step 10.9: Dispatch ui-ux-reviewer**

Per CLAUDE.md: dispatch the `ui-ux-reviewer` agent. Address Critical + Important findings before committing.

- [ ] **Step 10.10: Commit Task 10**

```
git add web/src/pages/Transactions.tsx web/src/pages/Transactions.test.tsx
git commit -m "feat(ui): wire BulkEditDialog into Transactions page"
```

---

### Task 11: Documentation + design enforcement

**Files:**
- Modify: `README.md`
- Modify: `docs/DESIGN_GUIDE.md`
- Modify: `docs/SCHEMA.md` — note no schema changes

- [ ] **Step 11.1: Update README.md**

Find the existing Transactions section. Add a subsection:

```markdown
### Bulk-edit

Select multiple transactions (via checkboxes or the "Select all N matching" banner) and click **Edit (N)** in the selection action bar. The dialog lets you change Date, Description, Category, or Tags across every selected row in one round-trip.

- Each field defaults to "no change". Only fields you explicitly modify are sent to the server.
- Tags support Add / Remove / Replace modes via a radio group above the tags input. Tag matching is byte-for-byte case-sensitive (e.g. `Tax` and `tax` are different).
- Page-mode (visible-page IDs only) fires immediately. All-matching mode opens a confirmation step listing the changes before submitting.
- Selection is pruned after submit: rows that the edit kicks off the current filter naturally drop out of the selection. A toast tells you when this happens.

API endpoints:
- `POST /api/transactions/batch-update` — body `{ ids, patch, tagsMode? }`
- `POST /api/transactions/update-by-filter?<querystring>` — body `{ patch, tagsMode? }`
```

- [ ] **Step 11.2: Update docs/DESIGN_GUIDE.md**

```markdown
## Bulk-edit dialog pattern

The Transactions bulk-edit dialog uses the `'noChange'` sentinel pattern (Lidarr-derived):

- shadcn `Select` first option is `— No change —`.
- Free-form inputs (text, date) default to empty + placeholder.
- Native `<input type="date">` ignores `placeholder`, so a leading "Set date" `<Checkbox>` gates the date picker — unchecked = no change.
- Multi-value fields (tags) get an additional Add / Remove / Replace radio above the input. The radio is disabled while the input is empty to prevent the "I selected Replace but the input is empty" footgun.

Layout: `grid grid-cols-1 md:grid-cols-[120px_1fr_140px_1fr]` collapses to a vertical stack below the `md:` breakpoint.

Confirm dialog (all-matching scope only) uses the **default primary** palette, NOT `destructiveActionClass` — bulk-edit is recoverable from the audit trail.
```

- [ ] **Step 11.3: Note no-schema-change in SCHEMA.md**

Add under the Transactions section:

```markdown
Note: the bulk-edit feature (2026-05-01) introduced no schema changes. Both endpoints (`/api/transactions/batch-update` and `/api/transactions/update-by-filter`) reuse existing tables (`transactions`, `transaction_audit`) and the existing `RecordBulkTx` audit helper.
```

- [ ] **Step 11.4: Run final full test suite**

```
docker run --rm -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build \
  -v D:/claude/SpenDrop:/src -w //src spendrop-go-test:1.26 go test ./...

docker run --rm -v D:/claude/SpenDrop/web:/src -w //src node:20-alpine \
  node ./node_modules/vitest/vitest.mjs run

docker run --rm -v D:/claude/SpenDrop/web:/src -w //src node:20-alpine \
  sh -c "node ./node_modules/typescript/bin/tsc --noEmit -p tsconfig.json"
```

Expected: ALL pass.

- [ ] **Step 11.5: Dispatch the final code-reviewer + security-auditor in parallel**

Per the project's `superpowers:subagent-driven-development` flow: after all tasks complete, dispatch a final code-reviewer for the entire implementation. Plus a security-auditor since the user wanted a TrueNAS safety review.

If either flags Critical issues, address them before commit. Important + Minor findings can be addressed before PR open.

- [ ] **Step 11.6: Commit Task 11**

```
git add README.md docs/DESIGN_GUIDE.md docs/SCHEMA.md
git commit -m "docs(bulk-edit): document feature in README + DESIGN_GUIDE + SCHEMA"
```

- [ ] **Step 11.7: Final branch verification**

```
git log --oneline main..HEAD
```

Expected: ~10-12 commits each beginning with a conventional-commit prefix.

```
git status
```

Expected: clean working tree.

The branch is ready for the user to push + open the PR.

---

## Final acceptance checklist

Before declaring the feature done:

- [ ] All Go tests pass (`go test ./...`).
- [ ] All vitest tests pass.
- [ ] `tsc --noEmit` clean.
- [ ] Both endpoints respond with the documented contract.
- [ ] Audit table contains the expected per-row + summary entries for all paths.
- [ ] Manual browser walkthrough completed (Step 10.8).
- [ ] data-correctness-reviewer agent dispatched after Tasks 3, 4, 5, and approved.
- [ ] ui-ux-reviewer agent dispatched after Task 10 and approved.
- [ ] Final code-reviewer + security-auditor agents dispatched after Task 11 and approved.
- [ ] README, DESIGN_GUIDE, SCHEMA updated.
- [ ] Working tree clean.
- [ ] Branch is `feat/transactions-bulk-edit` and has not been pushed to remote.

The user creates the PR. Do NOT push to origin without explicit user instruction (per CLAUDE.md).
