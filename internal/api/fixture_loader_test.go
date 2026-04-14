package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// Phase 3.2 of the data-stewardship plan replaces ad-hoc `now := time.Now();
// seed rows around now` report tests with a declarative fixture format that
// (a) pins a clock instant, (b) seeds users/categories/transactions from a
// single JSON blob, and (c) is diffed against a golden-file response so the
// expected output lives alongside the fixture as plain text. This file is
// the runtime half; the testdata/*.fixture.json files are the data half.
//
// Design notes:
//
//   - JSON, not YAML. The plan's example uses YAML but also forbids adding
//     dependencies beyond pgregory.net/rapid (Phase 3.5). Pulling in a YAML
//     parser just for tests was worse than a small format deviation, so
//     fixtures are JSON and encoded with the stdlib's encoding/json. The
//     readability cost is real but small — fixtures are tens of lines, not
//     hundreds.
//
//   - Users/categories/transactions only. Budgets, savings goals, settings,
//     and audit rows are intentionally out of scope for Phase 3.2. When the
//     first test needs a budget fixture the format will gain a "budgets"
//     section; until then keeping the struct small keeps the loader
//     reviewable.
//
//   - Cross-row references by name, not by ID. The fixture author does not
//     know what category.id SQLite will assign, so transactions reference
//     `category` and `user` by name and the loader resolves them against
//     freshly-inserted rows. This is the same trick the _HidesTombstoned
//     tests already use manually - the fixture loader just hides the
//     bookkeeping.
//
//   - Failure is always t.Fatalf. Tests either get a ready-to-use handler
//     or they stop immediately; partial setup hides the real error behind
//     a later unrelated assertion failure.

// fixtureFile is the top-level shape of a *.fixture.json document. All
// fields are optional so a minimal fixture can omit the sections it does
// not exercise (e.g. a read-only test might skip transactions and just set
// up users).
type fixtureFile struct {
	// Clock is the instant the fixedClock will return from Now(). Must be
	// RFC3339. An empty/zero value is rejected because "unset" almost
	// always indicates a typo - a test that genuinely wants real time
	// should skip the fixture harness entirely and call NewHandler.
	Clock string `json:"clock"`

	Users        []fixtureUser        `json:"users"`
	Categories   []fixtureCategory    `json:"categories"`
	Transactions []fixtureTransaction `json:"transactions"`
}

type fixtureUser struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type fixtureCategory struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	SortOrder int64  `json:"sort_order"`
}

type fixtureTransaction struct {
	// User is the seeded user's username; resolved to user_id at seed time.
	User string `json:"user"`
	// Category is the seeded category's name; resolved to category_id at seed time.
	Category    string  `json:"category"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	Description string  `json:"description"`
	Tags        string  `json:"tags"`
}

// fixtureResult is what loadFixture returns to the test: a handler wired to
// the fixed clock, a map of username->User for building request contexts,
// and the underlying queries/db in case the test wants to seed additional
// out-of-fixture rows before hitting the handler.
type fixtureResult struct {
	Handler    *Handler
	Users      map[string]database.User
	Categories map[string]database.Category
	Queries    *database.Queries
	DB         *sql.DB
}

// loadFixture reads testdata/<name>.fixture.json, applies it to a fresh
// test database, and returns a Handler whose clock is pinned to the
// fixture's Clock field. The path is relative to the _test.go file's
// package (the usual Go testdata convention), so a test at
// internal/api/reports_handlers_test.go reading "income_expenses_basic"
// loads internal/api/testdata/income_expenses_basic.fixture.json.
func loadFixture(t *testing.T, name string) *fixtureResult {
	t.Helper()

	raw, err := os.ReadFile(fmt.Sprintf("testdata/%s.fixture.json", name))
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}

	var fx fixtureFile
	// Reject unknown fields: a typo in a fixture key (e.g. "transacitons")
	// would otherwise silently no-op and the test would fail with a
	// confusing "expected X got Y" instead of the actual cause.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fx); err != nil {
		t.Fatalf("parse fixture %q: %v", name, err)
	}

	if fx.Clock == "" {
		t.Fatalf("fixture %q: clock is required", name)
	}
	clockT, err := time.Parse(time.RFC3339, fx.Clock)
	if err != nil {
		t.Fatalf("fixture %q: parse clock %q: %v", name, fx.Clock, err)
	}

	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: clockT})

	ctx := context.Background()

	// Seed users. Password hash is the same deterministic bcrypt used by
	// seedTestUser so any existing withUser helper keeps working.
	users := make(map[string]database.User, len(fx.Users))
	for _, u := range fx.Users {
		hash, hashErr := auth.HashPassword("testpassword")
		if hashErr != nil {
			t.Fatalf("fixture %q: hash password for %q: %v", name, u.Username, hashErr)
		}
		displayName := u.DisplayName
		if displayName == "" {
			displayName = u.Username
		}
		role := u.Role
		if role == "" {
			role = "member"
		}
		user, seedErr := q.CreateUser(ctx, database.CreateUserParams{
			Username:     u.Username,
			PasswordHash: hash,
			DisplayName:  displayName,
			Role:         role,
		})
		if seedErr != nil {
			t.Fatalf("fixture %q: seed user %q: %v", name, u.Username, seedErr)
		}
		users[u.Username] = user
	}

	// Seed categories, preserving declaration order as sort_order fallback
	// so fixtures that omit sort_order still get stable output.
	categories := make(map[string]database.Category, len(fx.Categories))
	for i, c := range fx.Categories {
		sortOrder := c.SortOrder
		if sortOrder == 0 {
			sortOrder = int64(i)
		}
		cat, seedErr := q.CreateCategory(ctx, database.CreateCategoryParams{
			Name:      c.Name,
			Type:      c.Type,
			SortOrder: sortOrder,
		})
		if seedErr != nil {
			t.Fatalf("fixture %q: seed category %q: %v", name, c.Name, seedErr)
		}
		categories[c.Name] = cat
	}

	// Seed transactions. Every row dual-writes amount_cents (Phase 3.1a
	// invariant); if a fixture asks for a category/user that wasn't
	// declared above, fail loudly instead of inserting an orphan row.
	for i, tx := range fx.Transactions {
		user, ok := users[tx.User]
		if !ok {
			t.Fatalf("fixture %q: transaction[%d] references unknown user %q", name, i, tx.User)
		}
		cat, ok := categories[tx.Category]
		if !ok {
			t.Fatalf("fixture %q: transaction[%d] references unknown category %q", name, i, tx.Category)
		}
		d, parseErr := time.Parse("2006-01-02", tx.Date)
		if parseErr != nil {
			t.Fatalf("fixture %q: transaction[%d] parse date %q: %v", name, i, tx.Date, parseErr)
		}
		params := database.CreateTransactionParams{
			UserID:      user.ID,
			Date:        d,
			Amount:      tx.Amount,
			AmountCents: dollarsToCents(tx.Amount),
			Description: tx.Description,
			CategoryID:  cat.ID,
		}
		if tx.Tags != "" {
			params.Tags = sql.NullString{String: tx.Tags, Valid: true}
		}
		if _, seedErr := q.CreateTransaction(ctx, params); seedErr != nil {
			t.Fatalf("fixture %q: seed transaction[%d]: %v", name, i, seedErr)
		}
	}

	return &fixtureResult{
		Handler:    h,
		Users:      users,
		Categories: categories,
		Queries:    q,
		DB:         db,
	}
}

// assertGoldenJSON compares the given actual bytes against the golden file
// at testdata/<name>.golden.json. Both sides are decoded into generic
// interface{} values and re-encoded with sorted-key indentation so
// whitespace/ordering drift between the handler and the checked-in golden
// does not cause spurious diffs.
//
// Setting UPDATE_GOLDEN=1 overwrites the golden file with the current
// actual bytes — same ergonomics as Go's -update flag convention. Use that
// after an intentional behaviour change, then review the diff the same way
// you would review any other source file edit.
func assertGoldenJSON(t *testing.T, name string, actual []byte) {
	t.Helper()

	goldenPath := fmt.Sprintf("testdata/%s.golden.json", name)

	// Canonicalize actual: decode then re-encode pretty-printed. This
	// ensures the on-disk file is human-readable regardless of how the
	// handler happened to marshal it (Go's encoding/json ordering is map-
	// iteration-random, so a handler that returns a map[string]any cannot
	// produce a stable byte diff without this normalization step).
	var actualDecoded any
	if err := json.Unmarshal(actual, &actualDecoded); err != nil {
		t.Fatalf("golden %q: actual response is not valid JSON: %v\nbody: %s", name, err, string(actual))
	}
	actualCanonical, err := json.MarshalIndent(actualDecoded, "", "  ")
	if err != nil {
		t.Fatalf("golden %q: canonicalize actual: %v", name, err)
	}
	// Trailing newline so the file ends cleanly and `git diff` on it
	// doesn't report "no newline at end of file".
	actualCanonical = append(actualCanonical, '\n')

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, actualCanonical, 0o644); err != nil {
			t.Fatalf("golden %q: write update: %v", name, err)
		}
		t.Logf("golden %q updated", name)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden %q: read (run with UPDATE_GOLDEN=1 to create): %v", name, err)
	}

	if string(want) != string(actualCanonical) {
		t.Fatalf("golden %q: mismatch\nwant:\n%s\ngot:\n%s", name, string(want), string(actualCanonical))
	}
}
