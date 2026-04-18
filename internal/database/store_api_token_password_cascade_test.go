package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// --- Helpers (test-local; promote to a shared helpers file only when a second
// test file starts needing them) --------------------------------------------

// seedUserWithPassword inserts a users row with a bcrypt-hashed password and
// returns (userID, originalHash). The hash is returned so tests can assert
// it changed (or didn't change, on rollback) after the cascade runs.
func seedUserWithPassword(t *testing.T, db *sql.DB, username, plaintext string) (int64, string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	row := db.QueryRowContext(context.Background(),
		`INSERT INTO users (username, password_hash, display_name, role)
		 VALUES (?, ?, ?, 'member') RETURNING id`,
		username, string(hash), username)
	var id int64
	if err := row.Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	return id, string(hash)
}

// seedTokenForUser inserts a live api_tokens row and returns its id. The
// token's `token_hash` is a deterministic SHA-256 of "name-userID-suffix" so
// collisions across tests can't happen and the hash column looks realistic
// to anyone inspecting the DB mid-test. `token_prefix` is exactly 15 chars
// to satisfy migration 011's `CHECK(length(token_prefix) = 15)` — the
// prefix value itself does not matter for cascade tests.
func seedTokenForUser(t *testing.T, db *sql.DB, userID int64, name, suffix string) int64 {
	t.Helper()
	raw := fmt.Sprintf("%s-%d-%s", name, userID, suffix)
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	const testPrefix = "spdr_testprefix" // 15 chars exactly — migration 011 CHECK
	if len(testPrefix) != 15 {
		t.Fatalf("testPrefix is %d chars, must be 15 — update literal", len(testPrefix))
	}
	row := db.QueryRowContext(context.Background(),
		`INSERT INTO api_tokens (user_id, name, token_hash, token_prefix)
		 VALUES (?, ?, ?, ?) RETURNING id`,
		userID, name, hash, testPrefix)
	var id int64
	if err := row.Scan(&id); err != nil {
		t.Fatalf("seed token %q for user %d: %v", name, userID, err)
	}
	return id
}

// seedSessionForUser inserts one sessions row. Returns the session token so
// tests can assert it's gone (or present) after the cascade.
func seedSessionForUser(t *testing.T, db *sql.DB, userID int64, suffix string) string {
	t.Helper()
	tok := fmt.Sprintf("sess-%d-%s", userID, suffix)
	expiresAt := time.Now().Add(24 * time.Hour)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		tok, userID, expiresAt)
	if err != nil {
		t.Fatalf("seed session for user %d: %v", userID, err)
	}
	return tok
}

// countLiveTokensForUser returns the number of non-revoked api_tokens rows
// for the user. "Live" == revoked_at IS NULL. Tests call this to assert
// cascade effects.
func countLiveTokensForUser(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL`,
		userID).Scan(&n)
	if err != nil {
		t.Fatalf("count live tokens for user %d: %v", userID, err)
	}
	return n
}

// countSessionsForUser returns the number of sessions rows for the user.
func countSessionsForUser(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&n)
	if err != nil {
		t.Fatalf("count sessions for user %d: %v", userID, err)
	}
	return n
}

// currentPasswordHash fetches users.password_hash for the given id. Used on
// both happy-path (expect new hash) and rollback (expect old hash)
// assertions.
func currentPasswordHash(t *testing.T, db *sql.DB, userID int64) string {
	t.Helper()
	var h string
	err := db.QueryRowContext(context.Background(),
		`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&h)
	if err != nil {
		t.Fatalf("fetch password_hash for user %d: %v", userID, err)
	}
	return h
}

// runPasswordCascade is the executable spec — the exact sequence a future
// handleChangePassword MUST run. Tests inject a `corrupt` function that
// fires AFTER step 3 (revoke) and BEFORE step 4 (session delete) so rollback
// paths can be exercised. Pass nil to skip the injection.
//
// The sequence is copy-paste-identical to the TODO block in
// internal/api/auth_handlers.go. If you change one, change the other —
// otherwise the TODO stops being an accurate blueprint.
func runPasswordCascade(
	ctx context.Context,
	db *sql.DB,
	store *ApiTokenStore,
	q *Queries,
	userID int64,
	newHash string,
	actor ActorContext,
	corrupt func(tx *sql.Tx) error,
) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	qtx := q.WithTx(tx)

	if err := qtx.UpdateUserPassword(ctx, UpdateUserPasswordParams{
		PasswordHash: newHash,
		ID:           userID,
	}); err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	if _, err := store.RevokeAllForUserTx(ctx, tx, actor,
		APITokenAuditRevokedByPasswordChange); err != nil {
		return fmt.Errorf("revoke tokens: %w", err)
	}

	if corrupt != nil {
		if err := corrupt(tx); err != nil {
			return fmt.Errorf("corrupt step: %w", err)
		}
	}

	if err := qtx.DeleteSessionsByUserID(ctx, userID); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}

	return tx.Commit()
}

// --- Tests -----------------------------------------------------------------

// TestPasswordCascade_UpdateAndRevokeCommitAtomically — happy path: run the
// full cascade and assert (a) alice's password hash updated, (b) alice's
// two live tokens are revoked, (c) alice's pre-existing already-revoked
// token is unchanged (no double-revoke), (d) alice's sessions are gone,
// (e) bystander bob's password/tokens/sessions are untouched, (f) exactly
// two audit rows were emitted tagged revoked_by_password_change.
func TestPasswordCascade_UpdateAndRevokeCommitAtomically(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)

	aliceID, originalAliceHash := seedUserWithPassword(t, db, "alice", "old-password")
	bobID, originalBobHash := seedUserWithPassword(t, db, "bob", "bob-password")

	// Alice has two live tokens AND one already-revoked token; the revoke
	// step must hit the two live ones and leave the already-revoked one
	// untouched (folds in spec §9.1's DoesNotRevokeAlreadyRevokedTokens).
	aliceTokenA := seedTokenForUser(t, db, aliceID, "homepage-live-a", "A")
	aliceTokenB := seedTokenForUser(t, db, aliceID, "homepage-live-b", "B")
	aliceTokenC := seedTokenForUser(t, db, aliceID, "homepage-already-revoked", "C")
	if _, err := db.ExecContext(context.Background(),
		`UPDATE api_tokens SET revoked_at = CURRENT_TIMESTAMP WHERE id = ?`,
		aliceTokenC); err != nil {
		t.Fatalf("pre-revoke token C: %v", err)
	}
	var preRevokedAt sql.NullTime
	if err := db.QueryRowContext(context.Background(),
		`SELECT revoked_at FROM api_tokens WHERE id = ?`, aliceTokenC,
	).Scan(&preRevokedAt); err != nil {
		t.Fatalf("fetch token C revoked_at pre-cascade: %v", err)
	}

	seedSessionForUser(t, db, aliceID, "device-laptop")
	seedSessionForUser(t, db, aliceID, "device-phone")

	// Bob is the bystander.
	bobToken := seedTokenForUser(t, db, bobID, "bob-homepage", "D")
	bobSession := seedSessionForUser(t, db, bobID, "bob-laptop")

	newAliceHash := "$2a$04$replacement.hash.goes.here.0123456789"
	if originalAliceHash == newAliceHash {
		t.Fatalf("fixture bug: original and new alice hashes are equal")
	}

	actor := ActorContext{
		UserID:    aliceID,
		IP:        "127.0.0.1",
		UserAgent: "test",
	}

	if err := runPasswordCascade(
		context.Background(), db, store, q, aliceID, newAliceHash, actor, nil,
	); err != nil {
		t.Fatalf("runPasswordCascade: %v", err)
	}

	// Alice: password hash updated, zero live tokens, zero sessions.
	if got := currentPasswordHash(t, db, aliceID); got != newAliceHash {
		t.Errorf("alice hash = %q, want %q", got, newAliceHash)
	}
	if got := countLiveTokensForUser(t, db, aliceID); got != 0 {
		t.Errorf("alice live tokens after cascade = %d, want 0", got)
	}
	if got := countSessionsForUser(t, db, aliceID); got != 0 {
		t.Errorf("alice sessions after cascade = %d, want 0", got)
	}

	// Alice's already-revoked token C: revoked_at is UNCHANGED — same
	// timestamp as before the cascade. This is how we prove the cascade
	// didn't "re-revoke" an already-tombstoned row.
	var postRevokedAt sql.NullTime
	if err := db.QueryRowContext(context.Background(),
		`SELECT revoked_at FROM api_tokens WHERE id = ?`, aliceTokenC,
	).Scan(&postRevokedAt); err != nil {
		t.Fatalf("fetch token C revoked_at post-cascade: %v", err)
	}
	if !postRevokedAt.Valid {
		t.Errorf("token C revoked_at went NULL after cascade")
	}
	if preRevokedAt.Time != postRevokedAt.Time {
		t.Errorf("token C revoked_at changed: pre=%v post=%v (cascade double-revoked)",
			preRevokedAt.Time, postRevokedAt.Time)
	}

	// Exactly two audit rows emitted (one per live token revoked), both with
	// action = revoked_by_password_change, pinned to the live token ids.
	// Token C MUST NOT have a new audit row from this cascade.
	var auditCount int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_token_audit
		 WHERE action = ? AND token_id IN (?, ?)`,
		string(APITokenAuditRevokedByPasswordChange), aliceTokenA, aliceTokenB,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows for live tokens: %v", err)
	}
	if auditCount != 2 {
		t.Errorf("revoked_by_password_change audit rows for live tokens = %d, want 2", auditCount)
	}

	var auditCountForC int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_token_audit
		 WHERE action = ? AND token_id = ?`,
		string(APITokenAuditRevokedByPasswordChange), aliceTokenC,
	).Scan(&auditCountForC); err != nil {
		t.Fatalf("count audit rows for token C: %v", err)
	}
	if auditCountForC != 0 {
		t.Errorf("revoked_by_password_change audit rows for already-revoked token C = %d, want 0", auditCountForC)
	}

	// Bob: untouched.
	if got := currentPasswordHash(t, db, bobID); got != originalBobHash {
		t.Errorf("bob hash changed (cascade leaked): was %q, is %q", originalBobHash, got)
	}
	if got := countLiveTokensForUser(t, db, bobID); got != 1 {
		t.Errorf("bob live tokens = %d, want 1", got)
	}
	if got := countSessionsForUser(t, db, bobID); got != 1 {
		t.Errorf("bob sessions = %d, want 1", got)
	}
	_ = bobToken
	_ = bobSession
}

// TestPasswordCascade_RollbackLeavesAllStateUnchanged — inject an error
// between the revoke step and the session delete step. Assert the whole
// cascade rolls back: password still old, tokens still live, sessions still
// present, zero new audit rows. This covers spec §9.1's
// ChangePassword_CascadeHappensInSameTransaction.
func TestPasswordCascade_RollbackLeavesAllStateUnchanged(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)

	aliceID, originalHash := seedUserWithPassword(t, db, "alice", "old-password")

	tokenA := seedTokenForUser(t, db, aliceID, "homepage-a", "A")
	tokenB := seedTokenForUser(t, db, aliceID, "homepage-b", "B")
	seedSessionForUser(t, db, aliceID, "device-1")
	seedSessionForUser(t, db, aliceID, "device-2")

	newHash := "$2a$04$replacement.hash.goes.here.abcdef1234"
	injectedErr := fmt.Errorf("simulated mid-cascade failure")

	actor := ActorContext{
		UserID: aliceID,
		IP:     "127.0.0.1",
	}

	err := runPasswordCascade(
		context.Background(), db, store, q, aliceID, newHash, actor,
		func(tx *sql.Tx) error { return injectedErr },
	)
	if err == nil {
		t.Fatal("runPasswordCascade returned nil; expected injected error to propagate")
	}

	// Password hash unchanged.
	if got := currentPasswordHash(t, db, aliceID); got != originalHash {
		t.Errorf("password hash changed despite rollback: was %q, is %q", originalHash, got)
	}

	// Both tokens still live.
	if got := countLiveTokensForUser(t, db, aliceID); got != 2 {
		t.Errorf("live tokens after rollback = %d, want 2", got)
	}
	for _, id := range []int64{tokenA, tokenB} {
		var revokedAt sql.NullTime
		if err := db.QueryRowContext(context.Background(),
			`SELECT revoked_at FROM api_tokens WHERE id = ?`, id,
		).Scan(&revokedAt); err != nil {
			t.Fatalf("fetch revoked_at for token %d: %v", id, err)
		}
		if revokedAt.Valid {
			t.Errorf("token %d was revoked despite rollback", id)
		}
	}

	// Both sessions present.
	if got := countSessionsForUser(t, db, aliceID); got != 2 {
		t.Errorf("sessions after rollback = %d, want 2", got)
	}

	// Zero audit rows with the password-change action — rollback also reverts
	// audit inserts that happened inside the tx. If we see any, it means the
	// audit write escaped the tx (e.g. a `db.ExecContext` sneak-in instead of
	// `qtx.ExecContext`) and the cascade is not atomic.
	var auditCount int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_token_audit
		 WHERE action = ? AND token_id IN (?, ?)`,
		string(APITokenAuditRevokedByPasswordChange), tokenA, tokenB,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 0 {
		t.Errorf("audit rows after rollback = %d, want 0 (atomicity violation)", auditCount)
	}
}

// TestPasswordCascade_AuditActionIsRevokedByPasswordChange — belt-and-
// suspenders assertion over the enum value that ends up in
// api_token_audit.action. Chunk 2's
// TestApiTokenStore_RevokeAllForUser_EmitsPerRowAuditEntries already covered
// the revoked_by_mass_revoke label. This test guards against a future
// refactor that drops the action parameter plumbing and silently defaults
// every cascade to mass_revoke (would let a password-change event appear in
// logs as an admin-initiated mass revoke — wrong audit trail, wrong
// incident-response story).
func TestPasswordCascade_AuditActionIsRevokedByPasswordChange(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)

	aliceID, _ := seedUserWithPassword(t, db, "alice", "old-password")
	tokenA := seedTokenForUser(t, db, aliceID, "homepage-a", "A")

	if err := runPasswordCascade(
		context.Background(), db, store, q, aliceID,
		"$2a$04$new.hash.placeholder.0123456789abcd",
		ActorContext{UserID: aliceID, IP: "127.0.0.1"},
		nil,
	); err != nil {
		t.Fatalf("runPasswordCascade: %v", err)
	}

	// Read the action column directly — no constant interpolation — to
	// catch a typo in the APITokenAuditAction enum definition or a
	// mis-copied CHECK constraint. If this assertion ever starts failing,
	// inspect migration 011's CHECK and store_api_token.go's enum values:
	// they must both spell "revoked_by_password_change" literally.
	var action string
	if err := db.QueryRowContext(context.Background(),
		`SELECT action FROM api_token_audit WHERE token_id = ?`, tokenA,
	).Scan(&action); err != nil {
		t.Fatalf("read audit action: %v", err)
	}
	if action != "revoked_by_password_change" {
		t.Errorf("audit action in DB = %q, want %q", action, "revoked_by_password_change")
	}

	// Also assert the typed constant matches the literal the CHECK constraint
	// accepts. Compile-time guard against an enum rename that drifts from the
	// migration file.
	if string(APITokenAuditRevokedByPasswordChange) != "revoked_by_password_change" {
		t.Errorf("APITokenAuditRevokedByPasswordChange const = %q, want %q",
			APITokenAuditRevokedByPasswordChange, "revoked_by_password_change")
	}
}

// TestPasswordCascade_SessionsAreClearedInSameTransaction — the session
// cascade is part of the same atomic unit. Chunk 2's tests never touched
// sessions; this test pins the invariant that a future handler cannot
// accidentally drop the DeleteSessionsByUserID step and still pass its
// review.
//
// Scenario: alice has three sessions (three devices) — all must be gone
// after cascade commits; bob's one session must survive (bystander check).
func TestPasswordCascade_SessionsAreClearedInSameTransaction(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)

	aliceID, _ := seedUserWithPassword(t, db, "alice", "old-password")
	bobID, _ := seedUserWithPassword(t, db, "bob", "bob-password")

	seedTokenForUser(t, db, aliceID, "homepage-a", "A")
	seedSessionForUser(t, db, aliceID, "laptop")
	seedSessionForUser(t, db, aliceID, "phone")
	seedSessionForUser(t, db, aliceID, "tablet")

	seedSessionForUser(t, db, bobID, "bobs-laptop")

	if got := countSessionsForUser(t, db, aliceID); got != 3 {
		t.Fatalf("pre-cascade alice sessions = %d, want 3 (fixture bug)", got)
	}

	if err := runPasswordCascade(
		context.Background(), db, store, q, aliceID,
		"$2a$04$new.hash.placeholder.fedcba987654",
		ActorContext{UserID: aliceID, IP: "127.0.0.1"},
		nil,
	); err != nil {
		t.Fatalf("runPasswordCascade: %v", err)
	}

	// Alice: zero sessions — all three devices signed out.
	if got := countSessionsForUser(t, db, aliceID); got != 0 {
		t.Errorf("alice sessions after cascade = %d, want 0", got)
	}

	// Bob: one session still present — cascade did not leak across users.
	if got := countSessionsForUser(t, db, bobID); got != 1 {
		t.Errorf("bob sessions after cascade = %d, want 1 (cascade leaked to bystander)", got)
	}
}
