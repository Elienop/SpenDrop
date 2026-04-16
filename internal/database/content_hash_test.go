package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestComputeContentHash_Deterministic asserts the pure-function contract
// that the same inputs always hash to the same bytes. Any non-determinism
// here would silently defeat idempotent imports: the startup backfill and
// the import path would produce different hashes for the same legacy row
// and the partial unique index would never get a hit.
func TestComputeContentHash_Deterministic(t *testing.T) {
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	h1 := ComputeContentHash(date, 4250, "Groceries", "Food")
	h2 := ComputeContentHash(date, 4250, "Groceries", "Food")
	if h1 != h2 {
		t.Errorf("expected identical hashes, got %q and %q", h1, h2)
	}
	// SHA-256 hex is 64 characters.
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex digest, got %d chars", len(h1))
	}
}

// TestComputeContentHash_NormalizesDescriptionAndCategory asserts the
// trim+lower normalization of description and category name. A household
// user typing "  Starbucks " on one import and "Starbucks" on the next
// should get a duplicate hit, not two rows. Same for Excel's auto-capitalize
// turning "food" into "Food" silently.
func TestComputeContentHash_NormalizesDescriptionAndCategory(t *testing.T) {
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	base := ComputeContentHash(date, 4250, "Groceries", "Food")
	variants := []struct {
		desc, cat string
	}{
		{"  Groceries", "Food"},
		{"Groceries  ", "Food"},
		{"GROCERIES", "Food"},
		{"groceries", "food"},
		{" groceries ", " FOOD "},
	}
	for _, v := range variants {
		got := ComputeContentHash(date, 4250, v.desc, v.cat)
		if got != base {
			t.Errorf("variant (%q, %q) produced %q, want %q", v.desc, v.cat, got, base)
		}
	}
}

// TestComputeContentHash_DistinguishesFields asserts that changing any of
// the four hash inputs flips the output. A weak formula that ignored one
// of the fields would collapse legitimately distinct rows (e.g. a $10
// Starbucks and a $100 Starbucks on the same day) into one hash and the
// second import would be a false-positive skip.
func TestComputeContentHash_DistinguishesFields(t *testing.T) {
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	base := ComputeContentHash(date, 4250, "Groceries", "Food")

	otherDate := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		label string
		hash  string
	}{
		{"date", ComputeContentHash(otherDate, 4250, "Groceries", "Food")},
		{"amount", ComputeContentHash(date, 4251, "Groceries", "Food")},
		{"description", ComputeContentHash(date, 4250, "Coffee", "Food")},
		{"category", ComputeContentHash(date, 4250, "Groceries", "Utilities")},
	}
	for _, c := range cases {
		if c.hash == base {
			t.Errorf("changing %s should change the hash, got match", c.label)
		}
	}
}

// TestBackfillContentHashes_AllRowsMatchRecomputed seeds legacy rows whose
// content_hash starts NULL (the state of every pre-migration-008 row on
// first boot), runs BackfillContentHashes, and checks every row ends with
// a hash byte-for-byte equal to a fresh ComputeContentHash over the same
// inputs. This is the guarantee the partial unique index relies on: the
// import path and the backfill path must agree, or a reimported
// spreadsheet will slip past deduplication because the two paths disagree
// on what bytes to hash.
func TestBackfillContentHashes_AllRowsMatchRecomputed(t *testing.T) {
	q, db := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "backfillee",
		PasswordHash: "$2a$10$fake",
		DisplayName:  "Backfillee",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	var foodID, foodName = int64(0), ""
	for _, c := range cats {
		if c.Name == "Food" {
			foodID = c.ID
			foodName = c.Name
			break
		}
	}
	if foodID == 0 {
		t.Fatal("seed category 'Food' missing")
	}

	// Seed 10 rows with distinct descriptions so each hash is unique under
	// the partial unique index. Every row gets content_hash=NULL via the
	// zero-value sql.NullString, which is the exact state a pre-migration-008
	// row occupies on first boot after the migration lands.
	type seeded struct {
		id          int64
		date        time.Time
		amountCents int64
		description string
		categoryID  int64
	}
	seeds := make([]seeded, 0, 10)
	for i := 0; i < 10; i++ {
		d := time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC)
		amount := 10.0 + float64(i)
		cents := dollarsToCents(amount)
		desc := "Legacy row " + time.Month(1+i).String()
		txn, err := q.CreateTransaction(ctx, CreateTransactionParams{
			UserID:      user.ID,
			Date:        d,
			Amount:      amount,
			AmountCents: cents,
			Description: desc,
			CategoryID:  foodID,
			ContentHash: sql.NullString{}, // deliberately NULL
		})
		if err != nil {
			t.Fatalf("CreateTransaction %d: %v", i, err)
		}
		// Sanity: fresh legacy row must have NULL content_hash.
		if txn.ContentHash.Valid {
			t.Fatalf("expected NULL content_hash on fresh row, got %q", txn.ContentHash.String)
		}
		seeds = append(seeds, seeded{
			id:          txn.ID,
			date:        d,
			amountCents: cents,
			description: desc,
			categoryID:  foodID,
		})
	}

	// Quick pre-check: there must be 10 pending rows. If the partial index
	// accidentally blocked the NULL inserts, CreateTransaction would have
	// failed above and we wouldn't get here — but assert anyway so a future
	// refactor that adds a "NOT NULL" shim blows up loudly instead of
	// silently producing a no-op backfill.
	var pending int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions WHERE content_hash IS NULL`,
	).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 10 {
		t.Fatalf("expected 10 rows with NULL hash pre-backfill, got %d", pending)
	}

	if err := BackfillContentHashes(ctx, db); err != nil {
		t.Fatalf("BackfillContentHashes: %v", err)
	}

	// Every seeded row must now carry a hash equal to ComputeContentHash
	// recomputed from its columns — same formula the import path uses.
	// Running the assertion per-row rather than a bulk SELECT makes the
	// failure mode "row X has hash Y, want Z" instead of "some row
	// mismatches."
	for _, s := range seeds {
		var got sql.NullString
		if err := db.QueryRowContext(ctx,
			`SELECT content_hash FROM transactions WHERE id = ?`, s.id,
		).Scan(&got); err != nil {
			t.Fatalf("select content_hash for id=%d: %v", s.id, err)
		}
		if !got.Valid {
			t.Errorf("row id=%d: content_hash still NULL after backfill", s.id)
			continue
		}
		want := ComputeContentHash(s.date, s.amountCents, s.description, foodName)
		if got.String != want {
			t.Errorf("row id=%d: content_hash mismatch\n got: %s\nwant: %s", s.id, got.String, want)
		}
	}

	// And nothing should remain pending — a resumable pass with the same
	// input must converge on zero, otherwise a fresh container would
	// re-scan the whole table on every boot.
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions WHERE content_hash IS NULL`,
	).Scan(&pending); err != nil {
		t.Fatalf("count pending post-backfill: %v", err)
	}
	if pending != 0 {
		t.Errorf("expected 0 pending rows after backfill, got %d", pending)
	}
}

// TestBackfillContentHashes_Idempotent reruns the backfill on an
// already-hashed table and asserts every row's hash is unchanged. A naive
// implementation that re-hashed every row on every boot would thrash the
// index for no reason; the query filter `content_hash IS NULL` guarantees
// the second call is a no-op. Lock this in so a future refactor that
// changes the filter notices immediately.
func TestBackfillContentHashes_Idempotent(t *testing.T) {
	q, db := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "idem",
		PasswordHash: "$2a$10$fake",
		DisplayName:  "Idem",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	foodID := cats[0].ID
	_, err = q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Amount:      10,
		AmountCents: 1000,
		Description: "Idem row",
		CategoryID:  foodID,
		ContentHash: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	if err := BackfillContentHashes(ctx, db); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	var first sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT content_hash FROM transactions ORDER BY id LIMIT 1`,
	).Scan(&first); err != nil {
		t.Fatalf("select post first backfill: %v", err)
	}
	if !first.Valid {
		t.Fatal("first backfill did not populate hash")
	}

	if err := BackfillContentHashes(ctx, db); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	var second sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT content_hash FROM transactions ORDER BY id LIMIT 1`,
	).Scan(&second); err != nil {
		t.Fatalf("select post second backfill: %v", err)
	}
	if second.String != first.String {
		t.Errorf("hash drifted across reruns: %s → %s", first.String, second.String)
	}
}

// TestGetTransactionByContentHash_HidesTombstoned enforces the SpenDrop
// soft-delete invariant for the new read path: a tombstoned row must not
// leak out of the hash lookup, or a user who trashed a row and then
// reimported the spreadsheet would see the row silently dropped with
// reason "duplicate" — worse UX than the rare legitimate second entry.
// The filter lives in queries.sql under GetTransactionByContentHash.
func TestGetTransactionByContentHash_HidesTombstoned(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "tombstone",
		PasswordHash: "$2a$10$fake",
		DisplayName:  "Tombstone",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	foodID := cats[0].ID
	foodName := cats[0].Name

	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	hash := ComputeContentHash(date, 99900, "Ghost row", foodName)
	txn, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        date,
		Amount:      999,
		AmountCents: 99900,
		Description: "Ghost row",
		CategoryID:  foodID,
		ContentHash: sql.NullString{String: hash, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	// While alive, the lookup must find the row.
	got, err := q.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true})
	if err != nil {
		t.Fatalf("GetTransactionByContentHash (live): %v", err)
	}
	if got.ID != txn.ID {
		t.Errorf("expected live lookup to return id=%d, got %d", txn.ID, got.ID)
	}

	// Tombstone it and expect the lookup to behave as if the row doesn't
	// exist — sql.ErrNoRows, not a 500 or a surfaced deleted row.
	if err := q.SoftDeleteTransaction(ctx, txn.ID); err != nil {
		t.Fatalf("SoftDeleteTransaction: %v", err)
	}
	_, err = q.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after soft-delete, got %v", err)
	}
}

// TestBackfillContentHashes_ToleratesLegitimateCollisions is the regression
// test for the TrueNAS boot-loop incident. Users with legitimately-identical
// legacy rows — same date, same amount_cents, same normalized description,
// same category — must not crash-loop the backfill on first boot after
// migration 008. The earliest row (lowest id) claims the hash as the
// canonical anchor for future imports; later colliding rows stay NULL and
// remain in the ledger untouched. The whole sweep must still complete so
// the rest of the pending rows (non-colliding) get hashed, and the function
// must return nil so main.go does not abort startup.
//
// The legacy assumption in migration 008's original comment — that the
// operator would pre-deduplicate by hand — is wrong for real household
// spreadsheets, which contain legitimate same-date/same-amount/same-
// description rows (e.g. two coffees on the same day). Locking this in
// prevents a future refactor from reintroducing the abort-on-collision
// behaviour.
func TestBackfillContentHashes_ToleratesLegitimateCollisions(t *testing.T) {
	q, db := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "collider",
		PasswordHash: "$2a$10$fake",
		DisplayName:  "Collider",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	var foodID, foodName = int64(0), ""
	for _, c := range cats {
		if c.Name == "Food" {
			foodID = c.ID
			foodName = c.Name
			break
		}
	}
	if foodID == 0 {
		t.Fatal("seed category 'Food' missing")
	}

	// Seed three rows:
	//   - first colliding pair (two "Coffee" rows on the same day)
	//   - a non-colliding third row
	// The non-colliding row proves the sweep continues after a collision
	// instead of aborting and leaving later rows unhashed.
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	first, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        date,
		Amount:      5,
		AmountCents: 500,
		Description: "Coffee",
		CategoryID:  foodID,
		ContentHash: sql.NullString{}, // legacy NULL
	})
	if err != nil {
		t.Fatalf("CreateTransaction first: %v", err)
	}
	second, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        date,
		Amount:      5,
		AmountCents: 500,
		Description: "Coffee",
		CategoryID:  foodID,
		ContentHash: sql.NullString{}, // legacy NULL — will collide with first
	})
	if err != nil {
		t.Fatalf("CreateTransaction second: %v", err)
	}
	third, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        date,
		Amount:      10,
		AmountCents: 1000,
		Description: "Lunch",
		CategoryID:  foodID,
		ContentHash: sql.NullString{}, // legacy NULL — no collision
	})
	if err != nil {
		t.Fatalf("CreateTransaction third: %v", err)
	}

	// The backfill MUST return nil — a UNIQUE violation on one row
	// cannot fail the whole sweep.
	if err := BackfillContentHashes(ctx, db); err != nil {
		t.Fatalf("BackfillContentHashes returned error on legitimate collision: %v", err)
	}

	// first (lowest id) is the canonical anchor — it gets the hash.
	var firstHash sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT content_hash FROM transactions WHERE id = ?`, first.ID,
	).Scan(&firstHash); err != nil {
		t.Fatalf("select first: %v", err)
	}
	if !firstHash.Valid {
		t.Errorf("earliest colliding row id=%d should have been hashed", first.ID)
	}
	want := ComputeContentHash(date, 500, "Coffee", foodName)
	if firstHash.String != want {
		t.Errorf("first row hash mismatch\n got: %s\nwant: %s", firstHash.String, want)
	}

	// second (higher id, same identity) stays NULL: the partial unique
	// index rejected its UPDATE, and our tolerance left it untouched.
	var secondHash sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT content_hash FROM transactions WHERE id = ?`, second.ID,
	).Scan(&secondHash); err != nil {
		t.Fatalf("select second: %v", err)
	}
	if secondHash.Valid {
		t.Errorf("later colliding row id=%d should still have NULL content_hash, got %q", second.ID, secondHash.String)
	}

	// third (non-colliding) must be hashed — the sweep continued after the
	// collision instead of bailing out at the first UNIQUE error.
	var thirdHash sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT content_hash FROM transactions WHERE id = ?`, third.ID,
	).Scan(&thirdHash); err != nil {
		t.Fatalf("select third: %v", err)
	}
	if !thirdHash.Valid {
		t.Errorf("non-colliding row id=%d should have been hashed; sweep aborted early", third.ID)
	}
	wantThird := ComputeContentHash(date, 1000, "Lunch", foodName)
	if thirdHash.String != wantThird {
		t.Errorf("third row hash mismatch\n got: %s\nwant: %s", thirdHash.String, wantThird)
	}
}

// TestBackfillContentHashes_CollisionSkipped_IsStableAcrossReboots asserts
// that a row left NULL by a previous collision does not re-trigger a crash
// on the next boot. This is the "container restart" case: whatever the
// implementation does to advance past a NULL-and-skipped row, it must do so
// again on the next invocation with the same final state.
//
// Without this guarantee, the skipped row could re-enter the pending set on
// every boot, re-collide with the canonical row, and keep the
// startup-noise loud forever. We want the second run to be a clean no-op
// on that row.
func TestBackfillContentHashes_CollisionSkipped_IsStableAcrossReboots(t *testing.T) {
	q, db := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "reboot",
		PasswordHash: "$2a$10$fake",
		DisplayName:  "Reboot",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	foodID := cats[0].ID

	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if _, err := q.CreateTransaction(ctx, CreateTransactionParams{
			UserID:      user.ID,
			Date:        date,
			Amount:      5,
			AmountCents: 500,
			Description: "Coffee",
			CategoryID:  foodID,
			ContentHash: sql.NullString{},
		}); err != nil {
			t.Fatalf("CreateTransaction %d: %v", i, err)
		}
	}

	if err := BackfillContentHashes(ctx, db); err != nil {
		t.Fatalf("first BackfillContentHashes: %v", err)
	}
	// Second invocation simulates a restart: the skipped NULL row must
	// be tolerated again and must not crash. The canonical row is
	// already hashed, so the pending set is {skipped row} and the only
	// attempted UPDATE will again hit the UNIQUE index and be skipped.
	if err := BackfillContentHashes(ctx, db); err != nil {
		t.Fatalf("second BackfillContentHashes (simulated restart): %v", err)
	}

	// State must match what we had after the first run: exactly one
	// row hashed, exactly one NULL.
	var nullCount, validCount int64
	if err := db.QueryRowContext(ctx,
		`SELECT
			COUNT(*) FILTER (WHERE content_hash IS NULL),
			COUNT(*) FILTER (WHERE content_hash IS NOT NULL)
		 FROM transactions`,
	).Scan(&nullCount, &validCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if nullCount != 1 || validCount != 1 {
		t.Errorf("expected 1 NULL + 1 hashed after reboot, got %d NULL + %d hashed", nullCount, validCount)
	}
}
