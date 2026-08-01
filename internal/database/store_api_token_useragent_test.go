package database

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// The User-Agent truncation is stated in CHARACTERS because that is the unit
// its column is declared in: migration 011 constrains
// CHECK(length(actor_user_agent) <= 500), and SQLite's length() counts
// characters on TEXT. It used to cut at 500 BYTES, which was wrong in two
// independent ways at once — stricter than the column for any non-ASCII agent,
// and capable of splitting a rune and writing invalid UTF-8 at rest.
//
// Every fixture below is deliberately multi-byte. An ASCII-only test passes
// identically under both units and proves nothing.

const uaArabic = "م" // ARABIC LETTER MEEM: 1 character, 2 bytes.
const uaEmoji = "\U0001F9FE"

func TestTruncateUserAgent_CountsCharactersNotBytes(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantChars int
		unchanged bool
	}{
		{"ascii under the cap", strings.Repeat("U", 100), 100, true},
		{"ascii exactly at the cap", strings.Repeat("U", maxUserAgentLen), maxUserAgentLen, true},
		{"ascii one over", strings.Repeat("U", maxUserAgentLen+1), maxUserAgentLen, false},
		// The load-bearing case: 400 Arabic characters is 800 bytes. A byte cut
		// would shorten this; a character cut must leave it entirely alone.
		{"arabic inside the cap but over it in bytes",
			strings.Repeat(uaArabic, 400), 400, true},
		{"arabic exactly at the cap",
			strings.Repeat(uaArabic, maxUserAgentLen), maxUserAgentLen, true},
		{"arabic one over", strings.Repeat(uaArabic, maxUserAgentLen+1), maxUserAgentLen, false},
		// 4 bytes per character, so a byte cut lands mid-rune far more often.
		{"astral inside the cap but far over it in bytes",
			strings.Repeat(uaEmoji, 200), 200, true},
		{"astral one over", strings.Repeat(uaEmoji, maxUserAgentLen+1), maxUserAgentLen, false},
		// A rune deliberately straddling byte offset maxUserAgentLen.
		{"multi-byte rune straddling the byte boundary",
			strings.Repeat("U", maxUserAgentLen-1) + uaEmoji + strings.Repeat("U", 10),
			maxUserAgentLen, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateUserAgent(tc.in)

			if n := utf8.RuneCountInString(got); n != tc.wantChars {
				t.Errorf("length = %d characters, want %d (input was %d characters, %d bytes)",
					n, tc.wantChars, utf8.RuneCountInString(tc.in), len(tc.in))
			}
			if tc.unchanged && got != tc.in {
				t.Errorf("value was shortened but fits the cap in characters: "+
					"%d characters, %d bytes — the cut is counting bytes",
					utf8.RuneCountInString(tc.in), len(tc.in))
			}
			if !utf8.ValidString(got) {
				t.Error("result is not valid UTF-8: the cut split a rune, so this " +
					"writes malformed data into a TEXT column")
			}
			if !strings.HasPrefix(tc.in, got) {
				t.Error("result is not a prefix of the input")
			}
		})
	}
}

// TestTruncateUserAgent_ResultSatisfiesTheColumnConstraint closes the loop the
// unit test above cannot: it proves the value the store writes is one the
// COLUMN accepts, by writing it. migration 011 declares
// CHECK(actor_user_agent IS NULL OR length(actor_user_agent) <= 500) and SQLite
// evaluates length() in characters, so a 500-character Arabic agent — 1,000
// bytes — must insert cleanly. Under the old byte-based cut this row could
// never have been produced at all, which is precisely why the truncation was
// silently discarding half of what the schema allowed.
func TestTruncateUserAgent_ResultSatisfiesTheColumnConstraint(t *testing.T) {
	q, db := setupTestDB(t)
	store := NewApiTokenStore(db, q)
	userID := seedUserForStoreTest(t, q, "arabic-ua")

	// 600 characters in, so truncation must fire; 500 characters out, 1,000 bytes.
	actor := ActorContext{
		UserID:      userID,
		IP:          "198.51.100.9",
		UserAgent:   strings.Repeat(uaArabic, 600),
		SessionHash: strings.Repeat("b", 64),
	}

	tok, err := store.Create(context.Background(), actor, CreateAPITokenParams{
		UserID:      userID,
		TokenHash:   hashForTest("plaintext-arabic-ua"),
		TokenPrefix: "spdr_arabicua00",
		Name:        "phone",
	})
	if err != nil {
		t.Fatalf("Create with a 600-character Arabic User-Agent: %v "+
			"(a CHECK violation here means the truncation and the column disagree)", err)
	}

	audits, err := q.ListAPITokenAuditByID(context.Background(), ListAPITokenAuditByIDParams{
		TokenID: tok.ID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("audit rows: want 1, got %d", len(audits))
	}
	stored := audits[0].ActorUserAgent
	if !stored.Valid {
		t.Fatal("actor_user_agent is NULL")
	}
	if n := utf8.RuneCountInString(stored.String); n != maxUserAgentLen {
		t.Errorf("stored %d characters, want %d", n, maxUserAgentLen)
	}
	if !utf8.ValidString(stored.String) {
		t.Error("stored value is not valid UTF-8")
	}
	if len(stored.String) <= maxUserAgentLen {
		t.Errorf("stored value is %d bytes, which is not MORE than the %d-character "+
			"cap — the fixture is not multi-byte and this test is vacuous",
			len(stored.String), maxUserAgentLen)
	}

	// Ask SQLite itself, since its length() is the function the CHECK uses.
	var dbLen int
	if err := db.QueryRow(
		`SELECT length(actor_user_agent) FROM api_token_audit WHERE token_id = ?`,
		tok.ID).Scan(&dbLen); err != nil {
		t.Fatalf("length() query: %v", err)
	}
	if dbLen != maxUserAgentLen {
		t.Errorf("SQLite length() reports %d, want %d: the store's unit and the "+
			"column's unit disagree", dbLen, maxUserAgentLen)
	}
}
