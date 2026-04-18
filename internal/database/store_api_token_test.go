package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// hashForTest produces a token_hash that matches the production format
// (lowercase hex SHA-256). The store package cannot import internal/auth,
// so the algorithm is duplicated here in a single-line helper rather than
// threading a real auth.HashAPIToken call through.
func hashForTest(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// seedUserForStoreTest creates a user row via the existing CreateUser query
// used by the rest of the test suite, returning the user's ID. DisplayName
// and Role are populated to match the convention used by the rest of the
// test suite (see queries_test.go:46-51, content_hash_test.go:92+).
func seedUserForStoreTest(t *testing.T, q *Queries, username string) int64 {
	t.Helper()
	u, err := q.CreateUser(context.Background(), CreateUserParams{
		Username:     username,
		PasswordHash: "$2a$10$fake.bcrypt.hash.for.test",
		DisplayName:  username,
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("seed user %q: %v", username, err)
	}
	return u.ID
}

// newActor returns an ActorContext populated with the same user + realistic
// IP / UA / session hash the production handlers would build. Tests use it
// as a default; individual tests override fields they care about.
func newActor(userID int64) ActorContext {
	return ActorContext{
		UserID:      userID,
		IP:          "203.0.113.10",
		UserAgent:   "Mozilla/5.0 (test)",
		SessionHash: strings.Repeat("a", 64),
	}
}

func TestApiTokenStore_Create_EmitsAuditRow(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)
	userID := seedUserForStoreTest(t, q, "alice")

	tok, err := store.Create(context.Background(), newActor(userID),
		CreateAPITokenParams{
			UserID:      userID,
			TokenHash:   hashForTest("plaintext-1"),
			TokenPrefix: "spdr_abc1234567",
			Name:        "homepage",
		})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	audits, err := q.ListAPITokenAuditByID(context.Background(), ListAPITokenAuditByIDParams{
		TokenID: tok.ID,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("want 1 audit row, got %d", len(audits))
	}
	if audits[0].Action != string(APITokenAuditCreated) {
		t.Errorf("action: want created, got %q", audits[0].Action)
	}
	if audits[0].UserID != userID {
		t.Errorf("user_id: want %d, got %d", userID, audits[0].UserID)
	}
	if audits[0].TokenID != tok.ID {
		t.Errorf("token_id: want %d, got %d", tok.ID, audits[0].TokenID)
	}
}

func TestApiTokenStore_Create_AuditCapturesActorMetadata(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)
	userID := seedUserForStoreTest(t, q, "alice")

	// 600-char UA to prove the 500-char truncation fires before INSERT.
	longUA := strings.Repeat("U", 600)
	actor := ActorContext{
		UserID:      userID,
		IP:          "198.51.100.5",
		UserAgent:   longUA,
		SessionHash: strings.Repeat("b", 64),
	}

	tok, err := store.Create(context.Background(), actor,
		CreateAPITokenParams{
			UserID:      userID,
			TokenHash:   hashForTest("plaintext-meta"),
			TokenPrefix: "spdr_metadata00",
			Name:        "homepage",
		})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	audits, _ := q.ListAPITokenAuditByID(context.Background(), ListAPITokenAuditByIDParams{
		TokenID: tok.ID, Limit: 10,
	})
	if len(audits) != 1 {
		t.Fatalf("audit rows: want 1, got %d", len(audits))
	}
	a := audits[0]
	if !a.ActorIp.Valid || a.ActorIp.String != "198.51.100.5" {
		t.Errorf("actor_ip: want 198.51.100.5, got %+v", a.ActorIp)
	}
	if !a.ActorUserAgent.Valid || len(a.ActorUserAgent.String) != maxUserAgentLen {
		t.Errorf("actor_user_agent length: want %d, got %d (truncation failed)",
			maxUserAgentLen, len(a.ActorUserAgent.String))
	}
	if !a.ActorSessionHash.Valid || a.ActorSessionHash.String != actor.SessionHash {
		t.Errorf("actor_session_hash: want %s, got %+v", actor.SessionHash, a.ActorSessionHash)
	}
}

func TestApiTokenStore_Revoke_IsIdempotentOnAlreadyRevoked(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)
	userID := seedUserForStoreTest(t, q, "alice")

	tok, err := store.Create(context.Background(), newActor(userID),
		CreateAPITokenParams{
			UserID:      userID,
			TokenHash:   hashForTest("t"),
			TokenPrefix: "spdr_aaaaaaaaaa",
			Name:        "n",
		})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Revoke(context.Background(), newActor(userID), tok.ID); err != nil {
		t.Fatalf("first Revoke: %v", err)
	}
	if err := store.Revoke(context.Background(), newActor(userID), tok.ID); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}

	audits, _ := q.ListAPITokenAuditByID(context.Background(), ListAPITokenAuditByIDParams{
		TokenID: tok.ID, Limit: 10,
	})
	// Expect 2: one "created", one "revoked_by_user". NOT three — the
	// second revoke must not write another row.
	if len(audits) != 2 {
		t.Fatalf("audit rows: want 2, got %d", len(audits))
	}
	var revCount int
	for _, a := range audits {
		if a.Action == string(APITokenAuditRevokedByUser) {
			revCount++
		}
	}
	if revCount != 1 {
		t.Errorf("revoked_by_user rows: want 1, got %d", revCount)
	}
}

func TestApiTokenStore_Revoke_RejectsCrossUserTokenID(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)
	aliceID := seedUserForStoreTest(t, q, "alice")
	bobID := seedUserForStoreTest(t, q, "bob")

	tok, err := store.Create(context.Background(), newActor(aliceID),
		CreateAPITokenParams{
			UserID:      aliceID,
			TokenHash:   hashForTest("alice-token"),
			TokenPrefix: "spdr_alicexxxxx",
			Name:        "alice",
		})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Bob tries to revoke Alice's token by ID — must fail with
	// ErrTokenNotFound, never succeed.
	err = store.Revoke(context.Background(), newActor(bobID), tok.ID)
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("want ErrTokenNotFound, got %v", err)
	}
	// And Alice's token must still be live.
	refreshed, err := q.GetAPITokenByID(context.Background(), GetAPITokenByIDParams{
		ID: tok.ID, UserID: aliceID,
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.RevokedAt.Valid {
		t.Error("Alice's token was incorrectly revoked by Bob")
	}
}

func TestApiTokenStore_RevokeAllForUser_EmitsPerRowAuditEntries(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)
	userID := seedUserForStoreTest(t, q, "alice")

	const N = 3
	tokenIDs := make([]int64, 0, N)
	for i := 0; i < N; i++ {
		tok, err := store.Create(context.Background(), newActor(userID),
			CreateAPITokenParams{
				UserID:      userID,
				TokenHash:   hashForTest(fmt.Sprintf("t-%d", i)),
				TokenPrefix: fmt.Sprintf("spdr_t%09d", i),
				Name:        "n",
			})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		tokenIDs = append(tokenIDs, tok.ID)
	}

	n, err := store.RevokeAllForUser(context.Background(), newActor(userID))
	if err != nil {
		t.Fatalf("RevokeAllForUser: %v", err)
	}
	if n != N {
		t.Errorf("revoked count: want %d, got %d", N, n)
	}

	// Each token must have exactly one revoked_by_mass_revoke audit row on
	// its real id (spec §4's CASCADE FK forbids a sentinel id).
	var totalMassRevoke int
	for _, id := range tokenIDs {
		audits, err := q.ListAPITokenAuditByID(context.Background(), ListAPITokenAuditByIDParams{
			TokenID: id, Limit: 10,
		})
		if err != nil {
			t.Fatalf("list audits id=%d: %v", id, err)
		}
		var thisOne int
		for _, a := range audits {
			if a.Action == string(APITokenAuditRevokedByMassRevoke) {
				thisOne++
				totalMassRevoke++
			}
		}
		if thisOne != 1 {
			t.Errorf("token id=%d: want 1 revoked_by_mass_revoke, got %d", id, thisOne)
		}
	}
	if totalMassRevoke != N {
		t.Errorf("total revoked_by_mass_revoke rows: want %d, got %d", N, totalMassRevoke)
	}
}

func TestApiTokenStore_RevokeAllForUser_ZeroLiveTokens_ReturnsZero(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)
	userID := seedUserForStoreTest(t, q, "alice")

	n, err := store.RevokeAllForUser(context.Background(), newActor(userID))
	if err != nil {
		t.Fatalf("RevokeAllForUser: %v", err)
	}
	if n != 0 {
		t.Errorf("count: want 0, got %d", n)
	}
	// No tokens -> no audit rows (spec §4 CASCADE FK forbids sentinel).
	rows, err := q.ListAPITokenAuditByUser(context.Background(), ListAPITokenAuditByUserParams{
		UserID: userID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list audits by user: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("audit rows: want 0 (no tokens to pin to), got %d", len(rows))
	}
}

func TestApiTokenStore_RevokeAllForUserTx_SharesCallerTxAtomicity(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)
	userID := seedUserForStoreTest(t, q, "alice")

	tok, err := store.Create(context.Background(), newActor(userID),
		CreateAPITokenParams{
			UserID:      userID,
			TokenHash:   hashForTest("atomic"),
			TokenPrefix: "spdr_atomic0000",
			Name:        "n",
		})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := store.RevokeAllForUserTx(context.Background(), tx,
		newActor(userID), APITokenAuditRevokedByPasswordChange); err != nil {
		tx.Rollback()
		t.Fatalf("RevokeAllForUserTx: %v", err)
	}
	// Caller rolls back instead of committing — neither the UPDATE nor any
	// per-row audit row must persist.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	refreshed, err := q.GetAPITokenByID(context.Background(), GetAPITokenByIDParams{
		ID: tok.ID, UserID: userID,
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.RevokedAt.Valid {
		t.Error("token was revoked despite rollback — tx did not roll back UPDATE")
	}
	audits, _ := q.ListAPITokenAuditByID(context.Background(), ListAPITokenAuditByIDParams{
		TokenID: tok.ID, Limit: 10,
	})
	// Only the original "created" audit must survive; the
	// revoked_by_password_change row rolled back with the UPDATE.
	var pwChange int
	for _, a := range audits {
		if a.Action == string(APITokenAuditRevokedByPasswordChange) {
			pwChange++
		}
	}
	if pwChange != 0 {
		t.Errorf("revoked_by_password_change audit row committed despite rollback: %d", pwChange)
	}
}
