package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Package-level tunables. These are set via Configure at startup by main;
// tests that don't call Configure get the production defaults below.
//
// The mutex guards against a data race in the unlikely case Configure is
// called concurrently with a request (e.g. a test that reloads config).
var (
	authMu     sync.RWMutex
	bcryptCost = 12 // bcrypt work factor (4-31)
	tokenBytes = 32 // random bytes per session token → 64 hex chars
)

// Configure sets the bcrypt cost and session-token byte length from the
// application config. Safe to call at any time; callers after startup will
// apply to subsequently hashed passwords and generated tokens only.
func Configure(cost, sessionTokenBytes int) {
	authMu.Lock()
	defer authMu.Unlock()
	bcryptCost = cost
	tokenBytes = sessionTokenBytes
}

// SetBcryptCostForTesting lowers the bcrypt cost to speed up tests. Kept as a
// backwards-compatible wrapper over Configure so existing test init() funcs
// do not need to change.
func SetBcryptCostForTesting() {
	Configure(4, 32)
}

func HashPassword(password string) (string, error) {
	authMu.RLock()
	cost := bcryptCost
	authMu.RUnlock()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GenerateSessionToken() (string, error) {
	authMu.RLock()
	n := tokenBytes
	authMu.RUnlock()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
