# Date Boundary Comparison Fix — Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development to execute. Steps use `- [ ]` for tracking.

**Goal:** Wrap every `t.date >= ?` / `t.date <= ?` comparison in `internal/api/export_handlers.go` with `date(t.date)` so upper-bound date filters stop silently dropping rows whose time component is non-zero on the boundary day.

**Architecture:** All 6 unsafe call sites live in one file. Fix is mechanical: add `date(...)` around the `t.date` side of the comparison at each site. Lower-bound (`>=`) comparisons are accidentally safe today against RFC3339-encoded `time.Time` values, but we wrap both sides for consistency and to match the canonical pattern already used in `internal/database/queries.sql` (see `:358,394,413,474-475,561,624` and the header comment at `:545-559` / `:607-614`).

**Tech Stack:** Go 1.26, mattn/go-sqlite3, existing `buildTransactionWhereClause` helper, `encoding/csv`, `xuri/excelize/v2`.

---

## Context for the implementer

**Why this bug exists:** `transactions.date` is declared `DATE NOT NULL` but SQLite has no type enforcement. `time.Time` values get written via mattn/go-sqlite3's default encoding as RFC3339 text (`2026-03-31T14:22:00Z`). When a user-facing date-only filter like `date_to=2026-03-31` is compared lexically via `t.date <= '2026-03-31'`, any row with a non-zero time component on that day (`2026-03-31T14:22:00Z`) sorts LATER than the bound and is silently dropped. Wrapping with `date(t.date)` extracts the YYYY-MM-DD prefix before comparing. The `>=` direction is accidentally safe today because `'T'` (0x54) sorts after the digit characters in the bound string, but we wrap both sides for symmetry and to defend against future changes to how the date argument is constructed.

**Safe pattern (already in use):** See `internal/database/queries.sql:358,394` — `WHERE date(t.date) >= date(?)` / `WHERE date(t.date) <= date(?)`. On the bound side we can use plain `?` since the input is already YYYY-MM-DD text (not a full RFC3339 string).

**Safe scope (confirmed by survey):** Only `export_handlers.go` has hand-rolled SQL touching `t.date` inequality. Everywhere else either uses `date(t.date)` already, compares integer (year, month), or is string-vs-string and not date-boundary sensitive.

---

## File Structure

**Modified:**
- `internal/api/export_handlers.go` — 6 SQL fragments (lines 29, 35, 279, 324, 425, 495) get `date(...)` wrapper on both sides.
- `internal/api/export_handlers_test.go` — existing table-driven test for `buildTransactionWhereClause` (the 15-case test I just shipped on `chore/test-suite-cleanup`) — **NOTE: that branch is unmerged; on this `fix/date-boundary-trap` branch which is off main, the test file still has the original 15 separate `TestBuildTransactionWhereClause_*` functions**. Update the expected-SQL strings in whichever form we have on this branch.

**Created (new tests):**
- `internal/api/export_handlers_boundary_test.go` — new end-to-end test file that seeds a transaction with a non-zero time-of-day on a boundary date and asserts it appears in each of the 5 export paths + 1 filter path. One test per path, so a failure pinpoints which SQL fragment regressed.

---

## Task 1: Boundary-behavior end-to-end test (RED)

**Files:**
- Create: `internal/api/export_handlers_boundary_test.go`

- [ ] **Step 1: Write the failing tests**

Seed a single transaction with `date = time.Date(2026, 3, 31, 14, 22, 0, 0, time.UTC)` (non-zero time on the last day of March). Then fire each boundary-constrained export / filter and assert the row appears in the output.

Use the existing test helpers in `internal/api/` for HTTP plumbing (`setupTestServer` or equivalent — look at how `export_handlers_test.go` currently exercises the handlers; follow that pattern).

One test function per export path:

```go
func TestExports_IncludeRowAtEndOfBoundaryDay_CSVExport(t *testing.T)
func TestExports_IncludeRowAtEndOfBoundaryDay_ListFilter(t *testing.T)
func TestExports_IncludeRowAtEndOfBoundaryDay_MonthlySummarySheet(t *testing.T)
func TestExports_IncludeRowAtEndOfBoundaryDay_MonthlyTransactionsSheet(t *testing.T)
func TestExports_IncludeRowAtEndOfBoundaryDay_YearlyMonthlyTotalsSheet(t *testing.T)
func TestExports_IncludeRowAtEndOfBoundaryDay_YearlyCategoryTotalsSheet(t *testing.T)
```

Each test:
1. Sets up an isolated test DB + handler.
2. Creates a category and a user.
3. Inserts a transaction on `2026-03-31T14:22:00Z` with a distinctive amount (e.g. `777` cents → `$7.77`) so you can grep for it.
4. Fires the matching request:
   - CSV: `GET /api/export/csv?date_from=2026-03-01&date_to=2026-03-31`
   - List filter: `GET /api/transactions?date_from=2026-03-01&date_to=2026-03-31`
   - Monthly summary: `GET /api/export/monthly/2026/3`
   - Monthly transactions sheet: same XLSX, different sheet — parse XLSX and locate the row in the "Transactions" sheet.
   - Yearly monthly totals: `GET /api/export/yearly/2026` — parse XLSX and locate in the "Monthly Totals" sheet.
   - Yearly category totals: same XLSX, "Category Totals" sheet — assert category's yearly total is non-zero.
5. Asserts the row / contribution is present. Use `t.Fatalf` with a message like `"boundary-day row dropped from CSV export — the t.date <= ? comparison trap is back"` so a future regression is self-explaining.

For XLSX parsing use `excelize.OpenReader(bytes.NewReader(body))`. For CSV parse with `encoding/csv.NewReader`.

- [ ] **Step 2: Run the tests, confirm ALL 6 fail**

Run: `MSYS_NO_PATHCONV=1 docker run --rm -v /d/claude/SpenDrop:/app -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build -w /app spendrop-go-test:1.26 sh -c "go test -race -count=1 -run TestExports_IncludeRowAtEndOfBoundaryDay ./internal/api/..."`

Expected: 6/6 FAIL with "boundary-day row dropped from …" — this is the whole point, don't proceed until they fail for the right reason.

- [ ] **Step 3: Commit the RED tests alone**

```bash
git add internal/api/export_handlers_boundary_test.go
git commit -m "test(api): pin end-of-boundary-day rows in exports and list filter"
```

RED commit first makes the bisect story clean — a future regression fix can `git revert` the GREEN commit and immediately see the tests fail.

---

## Task 2: Apply `date()` wrapper to all 6 unsafe sites (GREEN)

**Files:**
- Modify: `internal/api/export_handlers.go` at lines 29, 35, 279, 324, 425, 495.
- Modify: `internal/api/export_handlers_test.go` — update expected-SQL strings in the existing `TestBuildTransactionWhereClause_*` tests (there are 15 on this branch).

- [ ] **Step 1: Rewrite each SQL fragment**

For each of the 6 sites, change the WHERE/ON fragment from `t.date >= ?` / `t.date <= ?` to `date(t.date) >= ?` / `date(t.date) <= ?`. Keep arguments untouched — they're already YYYY-MM-DD strings, so no `date(?)` wrapper is needed on the bound side.

Exact sites (verified by survey — use `git grep -n "t.date >= ?" internal/api/export_handlers.go` to confirm before and after):

| Line | Today | After |
|---|---|---|
| 29 | `" AND t.date >= ?"` | `" AND date(t.date) >= ?"` |
| 35 | `" AND t.date <= ?"` | `" AND date(t.date) <= ?"` |
| 279 | `"... AND t.date >= ? AND t.date <= ?"` | `"... AND date(t.date) >= ? AND date(t.date) <= ?"` |
| 324 | same shape | same fix |
| 425 | same shape | same fix |
| 495 | same shape | same fix |

- [ ] **Step 2: Update `export_handlers_test.go` expected strings**

The existing `TestBuildTransactionWhereClause_DateFrom`, `_DateTo`, `_DateRange`, `_Multiple`, etc. assert literal clauses like `" AND t.date >= ?"`. Update each to `" AND date(t.date) >= ?"` (and `<=`). Leave all non-date assertions (`type`, `category_id`, etc.) untouched.

- [ ] **Step 3: Run the boundary tests — expect GREEN**

Run: `MSYS_NO_PATHCONV=1 docker run --rm -v /d/claude/SpenDrop:/app -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build -w /app spendrop-go-test:1.26 sh -c "go test -race -count=1 -run TestExports_IncludeRowAtEndOfBoundaryDay ./internal/api/..."`

Expected: 6/6 PASS.

- [ ] **Step 4: Run the full API package — no regressions**

Run: `MSYS_NO_PATHCONV=1 docker run --rm -v /d/claude/SpenDrop:/app -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build -w /app spendrop-go-test:1.26 sh -c "go test -race -count=1 ./internal/api/..."`

Expected: all PASS.

- [ ] **Step 5: Run the whole repo — no collateral damage**

Run: `MSYS_NO_PATHCONV=1 docker run --rm -v /d/claude/SpenDrop:/app -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build -w /app spendrop-go-test:1.26 sh -c "go test -race -count=1 ./..."`

Expected: all PASS.

- [ ] **Step 6: Commit the GREEN fix**

```bash
git add internal/api/export_handlers.go internal/api/export_handlers_test.go
git commit -m "fix(api): wrap t.date comparisons with date() to include end-of-day boundary rows"
```

---

## Invariants to preserve

- No change to argument types, nil-handling, order of predicates, or slice ordering — only the SQL text changes.
- Soft-delete filter `AND t.deleted_at IS NULL` must remain present where it is today.
- LEFT JOIN `ON` placement (for category-totals queries) — do NOT move predicates to WHERE; only wrap `t.date` inside the existing clause location.
- Keep `buildTransactionWhereClause` return shape (SQL string + `[]any`) identical.

## Out of scope

- DSN change (adding `parseTime=true` / `_loc`) — flagged by audit as Minor, but mixing DSN behavior change with a SQL-text fix risks regressing other call sites. Revisit in a separate branch if ever.
- Migrating `export_handlers.go` SQL to sqlc — also out of scope; this is a surgical fix.
- Adding `date(?)` on the bound side — unnecessary because the args are already YYYY-MM-DD text, not full RFC3339.
