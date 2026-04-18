package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"regexp"
)

// API tokens use a distinctive prefix so log scrapers and secret scanners
// can identify them at a glance (matches ghp_, glsa_, tskey-api- convention).
const (
	apiTokenPrefix       = "spdr_"
	apiTokenRandomLen    = 26 // base62 chars
	apiTokenChecksumLen  = 6  // base62 chars encoding a CRC32 checksum
	apiTokenPrefixLength = 15 // `spdr_` + first 10 random chars; stored for UI
)

// apiTokenAlphabet is the base62 alphabet used for the random and checksum
// segments. Ordering (digits, upper, lower) is the conventional base62 order
// and is irrelevant to security — it just has to be stable.
const apiTokenAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// apiTokenFormatRegex validates the exact shape of an API token. Used by
// the middleware as a pre-DB filter (spec §3.8 guardrail 1). Character
// class matches apiTokenAlphabet.
var apiTokenFormatRegex = regexp.MustCompile(
	`^spdr_[0-9A-Za-z]{26}_[0-9A-Za-z]{6}$`,
)

// GenerateAPIToken returns (plaintext, hash, prefix). The caller stores
// `hash` and `prefix` in the database and returns `plaintext` to the user
// exactly once. plaintext is 38 chars; hash is 64 hex chars; prefix is 15
// chars.
//
// Uses crypto/rand for the 26-char random segment. A crypto/rand failure
// returns a wrapped error — the caller should treat this as a 500, since
// the system entropy pool being unavailable is not a user-facing error.
func GenerateAPIToken() (plaintext, hash, prefix string, err error) {
	random := make([]byte, apiTokenRandomLen)
	for i := range random {
		n, readErr := randInt(len(apiTokenAlphabet))
		if readErr != nil {
			return "", "", "", fmt.Errorf("generate api token: %w", readErr)
		}
		random[i] = apiTokenAlphabet[n]
	}
	checksum := base62Checksum(random)
	plaintext = apiTokenPrefix + string(random) + "_" + checksum
	hash = HashAPIToken(plaintext)
	prefix = plaintext[:apiTokenPrefixLength]
	return plaintext, hash, prefix, nil
}

// HashAPIToken returns the lowercase hex SHA-256 of the plaintext token.
// Used both at creation time (to store the hash) and at middleware time
// (to look up the stored hash). SHA-256 is chosen over bcrypt because a
// 154-bit random token is not brute-forceable, bcrypt would be a DoS
// vector on every Homepage poll, and a salted hash cannot be uniquely
// indexed for O(log n) lookup. See spec §3.2.
func HashAPIToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// HashSessionToken returns the lowercase hex SHA-256 of a session cookie
// value. Used by the api-token audit trail to pin which session performed
// a token mutation without storing the raw cookie. Separate function from
// HashAPIToken so a future change to one doesn't silently change the
// other — audit rows are long-lived and rehashing is painful.
func HashSessionToken(plaintext string) string {
	// Implementation is intentionally identical to HashAPIToken. Do not DRY.
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// IsValidTokenFormat checks shape + CRC32 without touching the database.
// Used by the Bearer middleware as a pre-filter to kill enumeration timing
// attacks and load from pasted-wrong-token requests (spec §3.8 guardrail 1
// + §6.1).
func IsValidTokenFormat(token string) bool {
	if !apiTokenFormatRegex.MatchString(token) {
		return false
	}
	// Layout: "spdr_" [26 random] "_" [6 checksum]
	// Slice the random and checksum parts and verify.
	random := token[len(apiTokenPrefix) : len(apiTokenPrefix)+apiTokenRandomLen]
	checksum := token[len(apiTokenPrefix)+apiTokenRandomLen+1:]
	return base62Checksum([]byte(random)) == checksum
}

// base62Checksum computes CRC32 over `random` and encodes the 32-bit
// result as exactly 6 base62 characters. 62^6 = 56_800_235_584 which
// comfortably holds 2^32 = 4_294_967_296 — the encoding never overflows.
func base62Checksum(random []byte) string {
	sum := crc32.ChecksumIEEE(random)
	out := make([]byte, apiTokenChecksumLen)
	for i := apiTokenChecksumLen - 1; i >= 0; i-- {
		out[i] = apiTokenAlphabet[sum%uint32(len(apiTokenAlphabet))]
		sum /= uint32(len(apiTokenAlphabet))
	}
	return string(out)
}

// randInt returns a uniformly distributed integer in [0, n) using
// crypto/rand. Rejection sampling avoids the modulo bias that would skew
// the alphabet toward early characters if n is not a power of two (it
// isn't — 62).
func randInt(n int) (int, error) {
	if n <= 0 || n > 256 {
		return 0, fmt.Errorf("randInt: n out of range: %d", n)
	}
	max := 256 - (256 % n) // largest multiple of n ≤ 256
	buf := make([]byte, 1)
	for {
		if _, err := rand.Read(buf); err != nil {
			return 0, err
		}
		if int(buf[0]) < max {
			return int(buf[0]) % n, nil
		}
	}
}
