package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword_ReturnsValidBcryptHash(t *testing.T) {
	hash, err := HashPassword("mypassword")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	// The returned hash should be a valid bcrypt hash
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte("mypassword"))
	if err != nil {
		t.Errorf("hash should verify against original password: %v", err)
	}
}

func TestHashPassword_UsesCost12(t *testing.T) {
	hash, err := HashPassword("testcost")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}
	if cost != 12 {
		t.Errorf("expected bcrypt cost 12, got %d", cost)
	}
}

func TestHashPassword_DifferentHashesForSameInput(t *testing.T) {
	hash1, err := HashPassword("samepass")
	if err != nil {
		t.Fatalf("HashPassword (1): %v", err)
	}
	hash2, err := HashPassword("samepass")
	if err != nil {
		t.Fatalf("HashPassword (2): %v", err)
	}
	if hash1 == hash2 {
		t.Error("expected different hashes for same password (bcrypt uses random salt)")
	}
}

func TestCheckPassword_CorrectPassword(t *testing.T) {
	hash, err := HashPassword("correct")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPassword(hash, "correct") {
		t.Error("expected CheckPassword to return true for correct password")
	}
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	hash, err := HashPassword("correct")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if CheckPassword(hash, "wrong") {
		t.Error("expected CheckPassword to return false for wrong password")
	}
}

func TestDummyHash_ReadyBeforeAnyLogin(t *testing.T) {
	// The dummy hash is computed eagerly at package init, so a real bcrypt hash
	// is available BEFORE any DummyCheckPassword call. This proves the first
	// user-miss after process start does not pay an extra GenerateFromPassword.
	dummyMu.RLock()
	hash := dummyHash
	dummyMu.RUnlock()
	if len(hash) == 0 {
		t.Fatal("dummyHash empty at init — eager generation did not run")
	}
	if _, err := bcrypt.Cost(hash); err != nil {
		t.Fatalf("dummyHash is not a valid bcrypt hash at init: %v", err)
	}
}

func TestDummyCheckPassword_UsesConfiguredCost(t *testing.T) {
	// The dummy hash must be generated at the same bcrypt cost the package is
	// configured with, otherwise its compare time diverges from real users and
	// the timing oracle persists at a smaller magnitude. Configure recomputes
	// the dummy hash eagerly at the new cost.
	Configure(6, 32)
	t.Cleanup(func() { Configure(12, 32) }) // restore default for other tests

	authMu.RLock()
	want := bcryptCost
	authMu.RUnlock()
	dummyMu.RLock()
	hash := dummyHash
	dummyMu.RUnlock()
	got, err := bcrypt.Cost(hash)
	if err != nil {
		t.Fatalf("bcrypt.Cost(dummyHash): %v", err)
	}
	if got != want {
		t.Errorf("dummy hash cost = %d, want configured cost %d", got, want)
	}
}

func TestDummyCheckPassword_AlwaysFalseAndNoPanic(t *testing.T) {
	if DummyCheckPassword() {
		t.Error("expected DummyCheckPassword to return false on first call")
	}
	if DummyCheckPassword() {
		t.Error("expected DummyCheckPassword to return false on repeat call")
	}
}

func TestDummyCheckPassword_DoesRealCompareBeforeAnyLogin(t *testing.T) {
	// Even with no Configure/login having happened, DummyCheckPassword runs a
	// real bcrypt CompareHashAndPassword against a valid, cost-bearing hash —
	// never an instant/empty compare that would reopen the timing oracle. We
	// assert this by confirming the hash it compares against is a real bcrypt
	// hash carrying a work factor.
	dummyMu.RLock()
	hash := dummyHash
	dummyMu.RUnlock()
	cost, err := bcrypt.Cost(hash)
	if err != nil {
		t.Fatalf("dummyHash is not a real bcrypt hash: %v", err)
	}
	if cost < bcrypt.MinCost {
		t.Errorf("dummy hash cost %d below bcrypt.MinCost %d — compare would be too cheap", cost, bcrypt.MinCost)
	}
	if DummyCheckPassword() {
		t.Error("expected DummyCheckPassword to return false")
	}
}

func TestRegenerateDummyHash_FallsBackToValidHashOnGenerationFailure(t *testing.T) {
	// Simulate a GenerateFromPassword failure by passing an out-of-range cost
	// (bcrypt rejects cost > MaxCost). regenerateDummyHash must then install the
	// hardcoded fallback — a real bcrypt hash — so DummyCheckPassword keeps
	// doing a real compare instead of degrading to an instant/empty one.
	regenerateDummyHash(bcrypt.MaxCost + 1)
	t.Cleanup(func() { Configure(12, 32) }) // restore a normal dummy hash

	dummyMu.RLock()
	hash := dummyHash
	dummyMu.RUnlock()
	if string(hash) != fallbackDummyHash {
		t.Fatalf("expected fallback hash after generation failure, got %q", string(hash))
	}
	if _, err := bcrypt.Cost(hash); err != nil {
		t.Fatalf("fallback hash is not a valid bcrypt hash: %v", err)
	}
	// And the fallback must never match the probe value (always false).
	if bcrypt.CompareHashAndPassword(hash, []byte("x")) == nil {
		t.Error("fallback dummy hash unexpectedly matched the probe value")
	}
	if DummyCheckPassword() {
		t.Error("expected DummyCheckPassword to return false with the fallback hash")
	}
}

func TestGenerateSessionToken_Returns64CharHex(t *testing.T) {
	token, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	// 32 bytes = 64 hex characters
	if len(token) != 64 {
		t.Errorf("expected 64-char hex token, got %d chars", len(token))
	}
	// Verify it's valid hex
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("expected hex character, got %c", c)
			break
		}
	}
}

func TestGenerateSessionToken_Unique(t *testing.T) {
	token1, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken (1): %v", err)
	}
	token2, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken (2): %v", err)
	}
	if token1 == token2 {
		t.Error("expected unique tokens, got duplicates")
	}
}
