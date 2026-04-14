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
