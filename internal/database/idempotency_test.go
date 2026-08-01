package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestIdempotencyAndContentHashDetectorsDoNotSwallowEachOther pins the one
// property handleCreateTransaction's two-stage recovery rests on: a single
// INSERT can violate EITHER partial unique index, and each detector must fire
// for its own index and stay silent for the other.
//
// Getting this wrong is not a loud failure. If the content-hash detector
// matched a key violation, a replayed submission would be "recovered" by
// retrying with a NULL hash — which inserts a SECOND row, the exact duplicate
// the key exists to prevent. If the key detector matched a hash violation, a
// legitimate second identical entry from the other household member would be
// answered with somebody else's row instead of being stored.
//
// The errors are produced by real INSERTs against real indexes rather than
// hand-written strings: a test asserting strings.Contains over a literal it
// also wrote would pass even if SQLite's message never looked like that.
func TestIdempotencyAndContentHashDetectorsDoNotSwallowEachOther(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "keyowner",
		PasswordHash: "$2a$10$fake",
		DisplayName:  "Key Owner",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	catID, catName := cats[0].ID, cats[0].Name

	date := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	hash := ComputeContentHash(date, 2500, "Anchor", catName)
	seed := CreateTransactionParams{
		UserID:         user.ID,
		Date:           date,
		AmountCents:    2500,
		Description:    "Anchor",
		CategoryID:     catID,
		ContentHash:    sql.NullString{String: hash, Valid: true},
		IdempotencyKey: sql.NullString{String: "key-anchor-00000001", Valid: true},
	}
	if _, err := q.CreateTransaction(ctx, seed); err != nil {
		t.Fatalf("seed anchor row: %v", err)
	}

	// A key collision alone: different content (so no hash collision), same key.
	keyOnly := seed
	keyOnly.Description = "Different content"
	keyOnly.ContentHash = sql.NullString{String: ComputeContentHash(date, 2500, "Different content", catName), Valid: true}
	_, keyErr := q.CreateTransaction(ctx, keyOnly)
	if keyErr == nil {
		t.Fatal("expected an idempotency_key UNIQUE violation, got nil")
	}
	if !IsIdempotencyKeyUniqueViolation(keyErr) {
		t.Errorf("key violation not detected: %v", keyErr)
	}
	if IsContentHashUniqueViolation(keyErr) {
		t.Errorf("key violation was mistaken for a content_hash violation; the "+
			"handler would retry with a NULL hash and insert a duplicate row: %v", keyErr)
	}

	// A hash collision alone: same content, different key.
	hashOnly := seed
	hashOnly.IdempotencyKey = sql.NullString{String: "key-other-000000001", Valid: true}
	_, hashErr := q.CreateTransaction(ctx, hashOnly)
	if hashErr == nil {
		t.Fatal("expected a content_hash UNIQUE violation, got nil")
	}
	if !IsContentHashUniqueViolation(hashErr) {
		t.Errorf("hash violation not detected: %v", hashErr)
	}
	if IsIdempotencyKeyUniqueViolation(hashErr) {
		t.Errorf("hash violation was mistaken for a replay; the other household "+
			"member's identical entry would be answered with this row instead of "+
			"being stored: %v", hashErr)
	}

	// The %w wrap TransactionStore.Create applies must not defeat the substring
	// match — the handler only ever sees the wrapped form.
	if !IsIdempotencyKeyUniqueViolation(fmt.Errorf("create transaction: %w", keyErr)) {
		t.Errorf("wrapped key violation not detected: %v", keyErr)
	}

	// Narrow scope: an unrelated UNIQUE violation must surface as a real bug.
	_, dupUserErr := q.CreateUser(ctx, CreateUserParams{
		Username:     "keyowner",
		PasswordHash: "$2a$10$fake",
		DisplayName:  "Dup",
		Role:         "member",
	})
	if dupUserErr == nil {
		t.Fatal("expected a users.username UNIQUE violation, got nil")
	}
	if IsIdempotencyKeyUniqueViolation(dupUserErr) {
		t.Errorf("users.username violation matched the key detector: %v", dupUserErr)
	}

	if IsIdempotencyKeyUniqueViolation(nil) {
		t.Error("nil error matched the key detector")
	}
}

// TestIdempotencyKeyNamespaceIsPerUser pins the composite index and the scoped
// lookup together. The key namespace belongs to the user who minted the key: two
// members may hold the same key value, each anchoring their own row, and a
// lookup must return the caller's row rather than whichever one landed first.
//
// A global namespace made this an authorization boundary by accident. The second
// user's INSERT collided, the handler answered with the FIRST user's row, and the
// second user's transaction was discarded with a 201 saying it worked — no row,
// no audit trail.
func TestIdempotencyKeyNamespaceIsPerUser(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	alice, err := q.CreateUser(ctx, CreateUserParams{
		Username: "alice", PasswordHash: "$2a$10$fake", DisplayName: "Alice", Role: "member",
	})
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	bob, err := q.CreateUser(ctx, CreateUserParams{
		Username: "bob", PasswordHash: "$2a$10$fake", DisplayName: "Bob", Role: "member",
	})
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}
	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}

	shared := sql.NullString{String: "collision-key-000001", Valid: true}
	date := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)

	aliceRow, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID: alice.ID, Date: date, AmountCents: 100, Description: "Alice's",
		CategoryID: cats[0].ID, IdempotencyKey: shared,
	})
	if err != nil {
		t.Fatalf("alice insert: %v", err)
	}
	bobRow, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID: bob.ID, Date: date, AmountCents: 200, Description: "Bob's",
		CategoryID: cats[0].ID, IdempotencyKey: shared,
	})
	if err != nil {
		t.Fatalf("bob's insert under the same key was rejected — the key namespace "+
			"is shared across users, so bob's write is lost: %v", err)
	}

	for _, tc := range []struct {
		name   string
		userID int64
		wantID int64
	}{
		{"alice", alice.ID, aliceRow.ID},
		{"bob", bob.ID, bobRow.ID},
	} {
		got, err := q.GetTransactionByIdempotencyKey(ctx, GetTransactionByIdempotencyKeyParams{
			UserID: tc.userID, IdempotencyKey: shared,
		})
		if err != nil {
			t.Fatalf("%s lookup: %v", tc.name, err)
		}
		if got.ID != tc.wantID {
			t.Errorf("%s: lookup returned id %d, want %d — a replay would hand this "+
				"user the other member's row", tc.name, got.ID, tc.wantID)
		}
	}

	// The same user reusing their own key still collides — scoping must not have
	// disarmed the guarantee the index exists for.
	_, err = q.CreateTransaction(ctx, CreateTransactionParams{
		UserID: alice.ID, Date: date, AmountCents: 100, Description: "Alice's",
		CategoryID: cats[0].ID, IdempotencyKey: shared,
	})
	if !IsIdempotencyKeyUniqueViolation(err) {
		t.Errorf("alice reusing her own key = %v, want a UNIQUE violation", err)
	}
}

// TestGetTransactionByIdempotencyKey_FindsTombstonedRow pins the deliberate
// divergence from GetTransactionByContentHash: this lookup must see soft-deleted
// rows. If it filtered them out, a retry arriving after the user trashed the
// original would find nothing, fall through to an INSERT, and resurrect deleted
// content under a new id.
func TestGetTransactionByIdempotencyKey_FindsTombstonedRow(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "tombowner",
		PasswordHash: "$2a$10$fake",
		DisplayName:  "Tomb Owner",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}

	key := sql.NullString{String: "key-tombstone-00001", Valid: true}
	created, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:         user.ID,
		Date:           time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
		AmountCents:    900,
		Description:    "Trashed later",
		CategoryID:     cats[0].ID,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	if err := q.SoftDeleteTransaction(ctx, created.ID); err != nil {
		t.Fatalf("SoftDeleteTransaction: %v", err)
	}

	found, err := q.GetTransactionByIdempotencyKey(ctx, GetTransactionByIdempotencyKeyParams{
		UserID: user.ID, IdempotencyKey: key,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatal("tombstoned row not found by key — a replay would fall " +
				"through to an INSERT and resurrect deleted content")
		}
		t.Fatalf("GetTransactionByIdempotencyKey: %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("found id = %d, want %d", found.ID, created.ID)
	}
	if !found.DeletedAt.Valid {
		t.Error("expected the returned row to still carry its tombstone")
	}

	// And the key stays claimed: a second insert under it must still collide,
	// so the tombstone cannot be worked around by retrying.
	_, err = q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:         user.ID,
		Date:           time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
		AmountCents:    900,
		Description:    "Trashed later",
		CategoryID:     cats[0].ID,
		IdempotencyKey: key,
	})
	if !IsIdempotencyKeyUniqueViolation(err) {
		t.Errorf("insert under a tombstoned row's key = %v, want a UNIQUE "+
			"violation — the index must not carry a deleted_at predicate", err)
	}
}
