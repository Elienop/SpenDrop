package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// Two message decisions that a later reader will be tempted to "tidy", and that
// no other test pins. Both are about the UNIT a limit is stated in, which is
// the whole subject of text_length_units_test.go — these are the two places
// where the apt wording is not simply "characters everywhere".

// errorMessage pulls the message out of a writeError body.
func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body["error"]
}

// TestUsernameValidation_NonASCIIIsAnsweredByTheCharsetGate pins the ORDER of
// the two username checks, which is load-bearing rather than cosmetic.
//
// MinUsernameLength / MaxUsernameLength are compared with len(), which counts
// bytes, and that is only the same number as characters because isValidUsername
// admits nothing but one-byte ASCII. While the length check ran FIRST, that
// equivalence held only by reading ahead — and a non-ASCII username (20 Arabic
// letters is 40 bytes) was refused with "must be between 3 and 32 characters",
// a count it had not violated, instead of the charset message that describes
// what is actually wrong with it.
//
// The probe is deliberately INSIDE the length bound in characters and OUTSIDE
// it in bytes. A 40-character Arabic name would be refused by either check and
// could not tell the two orderings apart.
func TestUsernameValidation_NonASCIIIsAnsweredByTheCharsetGate(t *testing.T) {
	probe := repeatTo(arabicLetter, 20) // 20 characters, 40 bytes
	if charLen(probe) > MaxUsernameLength {
		t.Fatalf("probe is %d characters against a %d cap; it must be INSIDE the cap in characters or this test cannot discriminate",
			charLen(probe), MaxUsernameLength)
	}
	if len(probe) <= MaxUsernameLength {
		t.Fatalf("probe is %d bytes against a %d cap; it must EXCEED the cap in bytes", len(probe), MaxUsernameLength)
	}

	body := func() string {
		return `{"username":"` + probe + `","password":"longpassword12","display_name":"x","role":"member"}`
	}

	t.Run("register", func(t *testing.T) {
		q, db := setupTestDB(t)
		h := NewHandler(q, db)
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(body()))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.handleRegister(rec, req)
		assertCharsetMessage(t, rec)
	})

	t.Run("admin creates user", func(t *testing.T) {
		q, db := setupTestDB(t)
		h := NewHandler(q, db)
		admin := seedTestUser(t, q, "unitadmin", RoleAdmin)
		req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body()))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.handleCreateUser(rec, withUser(req, admin))
		assertCharsetMessage(t, rec)
	})
}

func assertCharsetMessage(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	msg := errorMessage(t, rec)
	if !strings.Contains(msg, "may only contain") {
		t.Errorf("a non-ASCII username was refused with %q; the charset gate must answer it, not the length bound — otherwise the user is told they broke a character count they did not break",
			msg)
	}
}

// TestUsernameValidation_EmptyStillSaysRequired guards the other end of the
// reorder. "username is required" runs before both checks; moving the charset
// gate up must not let an empty username fall through to it, which would answer
// a missing field with a message about permitted characters.
func TestUsernameValidation_EmptyStillSaysRequired(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Handler, *httptest.ResponseRecorder, *http.Request, database.User)
	}{
		{"register", func(h *Handler, rec *httptest.ResponseRecorder, r *http.Request, _ database.User) {
			h.handleRegister(rec, r)
		}},
		{"admin creates user", func(h *Handler, rec *httptest.ResponseRecorder, r *http.Request, admin database.User) {
			h.handleCreateUser(rec, withUser(r, admin))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			admin := seedTestUser(t, q, "emptyadmin", RoleAdmin)
			// Whitespace only: it survives JSON decoding and is emptied by the
			// TrimSpace both handlers apply, so it reaches the required check
			// the same way a "" would.
			req := httptest.NewRequest(http.MethodPost, "/api/users",
				strings.NewReader(`{"username":"   ","password":"longpassword12","role":"member"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			tc.call(h, rec, req, admin)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if msg := errorMessage(t, rec); msg != "username is required" {
				t.Errorf("an empty username was refused with %q, want \"username is required\"", msg)
			}
		})
	}
}

// TestPasswordMessages_MinSaysCharactersMaxSaysBytes pins a deliberate
// asymmetry that reads as an inconsistency, which is exactly why it needs a
// test: a reader tidying the two messages to match would be undoing a decision,
// and nothing else in the suite would notice.
//
// The maximum must say bytes. bcrypt hashes at most the first 72 bytes and
// discards the rest, so this is the bound that can refuse text a character
// count would have admitted — a 40-character Arabic password is 80 bytes, and
// "72 characters or less" would name a limit the server is not applying.
// TestPasswordBounds_StayByteBounded pins the enforcement; this pins the word.
//
// The minimum says characters because bytes there buys no accuracy. For any
// ASCII password the two counts are identical, and for non-ASCII input a byte
// minimum is LOOSER than a character minimum — so the mismatch can only err
// toward accepting a password the message implied was too short, never toward
// refusing one it implied was long enough.
func TestPasswordMessages_MinSaysCharactersMaxSaysBytes(t *testing.T) {
	minLen, maxLen := getPasswordBounds()

	short := validateNewPassword(strings.Repeat("a", minLen-1))
	if short == "" {
		t.Fatalf("a %d-character password was accepted against a %d minimum", minLen-1, minLen)
	}
	if !strings.Contains(short, "characters") {
		t.Errorf("the password MINIMUM reads %q; it must say characters. Bytes there is jargon that buys no accuracy — the two counts are identical for every ASCII password, and a byte minimum is the looser of the two for anything else.",
			short)
	}

	long := validateNewPassword(strings.Repeat("a", maxLen+1))
	if long == "" {
		t.Fatalf("a %d-byte password was accepted against a %d maximum", maxLen+1, maxLen)
	}
	if !strings.Contains(long, "bytes") {
		t.Errorf("the password MAXIMUM reads %q; it must say bytes, because bcrypt truncates at 72 BYTES and this bound can refuse text a character count would have admitted",
			long)
	}
}
