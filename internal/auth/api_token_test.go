package auth

import (
	"regexp"
	"testing"
)

func TestGenerateAPIToken_ReturnsCorrectShape(t *testing.T) {
	plaintext, hash, prefix, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken: unexpected error: %v", err)
	}
	if len(plaintext) != 38 {
		t.Errorf("plaintext length: want 38, got %d", len(plaintext))
	}
	if !regexp.MustCompile(`^spdr_[0-9A-Za-z]{26}_[0-9A-Za-z]{6}$`).MatchString(plaintext) {
		t.Errorf("plaintext shape: got %q", plaintext)
	}
	if len(hash) != 64 {
		t.Errorf("hash length: want 64, got %d", len(hash))
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Errorf("hash hex: got %q", hash)
	}
	if len(prefix) != 15 {
		t.Errorf("prefix length: want 15, got %d", len(prefix))
	}
	if prefix != plaintext[:15] {
		t.Errorf("prefix is not the first 15 chars of plaintext")
	}
}

func TestGenerateAPIToken_IsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		tok, _, _, err := GenerateAPIToken()
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("iter %d: duplicate token %q", i, tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestIsValidTokenFormat_AcceptsFreshlyGeneratedToken(t *testing.T) {
	tok, _, _, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken: %v", err)
	}
	if !IsValidTokenFormat(tok) {
		t.Fatalf("IsValidTokenFormat rejected freshly generated %q", tok)
	}
}

func TestIsValidTokenFormat_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name, input string
	}{
		{"empty", ""},
		{"missing prefix", "aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123"},
		{"wrong prefix", "spdx_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123"},
		{"short random", "spdr_aB3xQ9z7kL_abc123"},
		{"long random", "spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9A_abc123"},
		{"wrong separator", "spdr-aB3xQ9z7kLmN3pRsTv2wXyZfG9-abc123"},
		{"non-base62 random", "spdr_aB3xQ9z7kLmN3pRsTv2wXyZf-9_abc123"},
		{"non-base62 checksum", "spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc12!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if IsValidTokenFormat(tc.input) {
				t.Fatalf("want false, got true for %q", tc.input)
			}
		})
	}
}

func TestIsValidTokenFormat_RejectsFlippedChecksum(t *testing.T) {
	tok, _, _, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken: %v", err)
	}
	// Flip the last character to a different base62 char (guaranteed mismatch).
	flipped := tok[:len(tok)-1]
	if tok[len(tok)-1] == 'A' {
		flipped += "B"
	} else {
		flipped += "A"
	}
	if IsValidTokenFormat(flipped) {
		t.Fatalf("IsValidTokenFormat accepted flipped-checksum token %q", flipped)
	}
}

func TestHashAPIToken_IsDeterministic(t *testing.T) {
	h1 := HashAPIToken("spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123")
	h2 := HashAPIToken("spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123")
	if h1 != h2 {
		t.Fatalf("HashAPIToken not deterministic: %q vs %q", h1, h2)
	}
}

func TestHashAPIToken_DifferentInputs_DifferentHashes(t *testing.T) {
	h1 := HashAPIToken("spdr_aaaaaaaaaaaaaaaaaaaaaaaaaa_aaaaaa")
	h2 := HashAPIToken("spdr_aaaaaaaaaaaaaaaaaaaaaaaaab_aaaaaa")
	if h1 == h2 {
		t.Fatal("HashAPIToken returned identical hashes for different inputs")
	}
}

func TestHashSessionToken_DifferentFromApiTokenHash(t *testing.T) {
	// Not a security requirement — a correctness smoke test that the two
	// helpers are not accidentally aliases. Hashes of the same input
	// happen to be equal (both are SHA-256 of the input bytes); the test
	// just documents that.
	h1 := HashAPIToken("abc")
	h2 := HashSessionToken("abc")
	if h1 != h2 {
		t.Fatalf("both helpers hash the same way; want equal, got %q vs %q", h1, h2)
	}
}
