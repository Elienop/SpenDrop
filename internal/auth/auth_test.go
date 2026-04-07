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
