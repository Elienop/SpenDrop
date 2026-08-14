package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// The tests in this file turn on one thing: a display name may not carry
// characters that let it forge STRUCTURE in a context that renders it.
//
// The sink is the activity push body. notifications.go interpolates the actor's
// name as the first field of
// fmt.Sprintf("%s added $%.2f in %s — %s", actor, …), and the service worker
// rolls several activities into one notification with lines.join('\n')
// (web/src/lib/sw-notifications.ts). So a newline in a name adds a visual line
// to ANOTHER household member's notification, and that line can be written to
// read as a genuine, separately-attributed activity.
//
// EVERY REJECTED FIXTURE PUTS THE CHARACTER IN THE INTERIOR, and that is
// load-bearing rather than tidy. All four callers run strings.TrimSpace first,
// and unicode.IsSpace is true for LF, CR, TAB, U+0085, U+2028 and U+2029 — so a
// fixture like "\nAli" is trimmed to "Ali" and stored happily. A test written
// that way asserts nothing about the guard; it asserts that TrimSpace exists.
//
// The ACCEPTED fixtures are the more important half. This household writes
// Arabic and French alongside English, and a guard that refused a class for
// being unfamiliar rather than dangerous would lock a member out of their own
// name. Each accepted case below is one a plausible-looking guard gets wrong.

const (
	// forgedName is the payload the four handler tests all submit: a name whose
	// interior newline manufactures a second, separately-attributed line in the
	// rolled-up push body. It is spelled the way the sink formats a real one.
	forgedName = "Zenobia\nHousehold added $250.00 in Rent"

	// cleanName is the positive control that runs beside every rejection. A
	// guard that refuses everything would pass a rejection-only test.
	cleanName = "Marwa Chidiac"
)

// TestValidateDisplayName_PerCodepointClass is the unit table. It is separate
// from the handler tests below because the classes are the substance of the
// rule and the handlers only carry it: a class added or dropped shows up here
// as one row, not as a fifth handler test somebody forgets to write.
func TestValidateDisplayName_PerCodepointClass(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  string
		reject bool
	}{
		// --- REFUSED: Cc, the control characters ---
		{"LF forges a line in the rolled-up push body", "Ali\nBot", true},
		{"CR", "Ali\rBot", true},
		{"TAB forges column structure", "Ali\tBot", true},
		{"NUL truncates in any C-string consumer", "Ali\x00Bot", true},
		{"ESC opens an ANSI sequence", "Ali\x1b[31mBot", true},
		{"DEL U+007F", "Ali\u007FBot", true},
		{"C1 NEL U+0085", "Ali\u0085Bot", true},
		{"C1 APC U+009F", "Ali\u009FBot", true},

		// --- REFUSED: Zl/Zp, the gap unicode.IsControl leaves open ---
		{"U+2028 LINE SEPARATOR", "Ali\u2028Bot", true},
		{"U+2029 PARAGRAPH SEPARATOR", "Ali\u2029Bot", true},

		// --- REFUSED: bidi embeddings and overrides, U+202A-U+202E ---
		{"U+202A LRE", "Ali\u202ABot", true},
		{"U+202B RLE", "Ali\u202BBot", true},
		{"U+202C PDF", "Ali\u202CBot", true},
		{"U+202D LRO", "Ali\u202DBot", true},
		{"U+202E RLO reverses the rest of the body", "Ali\u202EBot", true},

		// --- REFUSED: bidi isolates, U+2066-U+2069 ---
		{"U+2066 LRI", "Ali\u2066Bot", true},
		{"U+2067 RLI", "Ali\u2067Bot", true},
		{"U+2068 FSI", "Ali\u2068Bot", true},
		{"U+2069 PDI", "Ali\u2069Bot", true},

		// --- ACCEPTED: the household's own scripts ---
		{"Arabic name", "علي عبد الله", false},
		{"French accented name", "Éloïse Béchara", false},
		{"mixed Arabic and Latin", "علي Karam", false},

		// --- ACCEPTED: bidi MARKS, which open no scope ---
		// A guard reaching for unicode.Bidi_Control sweeps these up with the
		// overrides above and makes ordinary bidi-aware paste unusable.
		{"U+200E LRM", "Ali\u200EKaram", false},
		{"U+200F RLM", "علي\u200F Karam", false},
		{"U+061C ARABIC LETTER MARK", "علي\u061CKaram", false},

		// --- ACCEPTED: zero-width joiners, which are load-bearing ---
		{"ZWJ emoji sequence (woman technologist)", "Zenobia \U0001f469\u200D\U0001f4bb", false},
		{"ZWJ family emoji", "Karam \U0001f468\u200D\U0001f469\u200D\U0001f467", false},
		{"ZWNJ, required by Persian orthography", "می\u200Cرود", false},

		// --- ACCEPTED: merely unusual, not structure-forging ---
		{"ZWSP from a bad paste", "Ali\u200BKaram", false},
		{"BOM from a bad paste", "Ali\uFEFFKaram", false},
		{"NBSP, which French typography uses", "Jean\u00A0Pierre", false},
		{"soft hyphen", "Jean\u00ADPierre", false},
		{"astral emoji", "Elie " + astralEmoji, false},
		{"apostrophe", "O'Brien", false},
		{"angle brackets — there is no HTML sink", "<script>alert(1)</script>", false},

		// --- ACCEPTED: absent ---
		// "" means "not supplied" on three of the four callers; the fourth
		// refuses it with its own message before reaching this function.
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDisplayName(tc.value)
			if tc.reject && err == nil {
				t.Fatalf("validateDisplayName(%q) = nil, want a refusal — this class can forge structure in the push body", tc.value)
			}
			if !tc.reject && err != nil {
				t.Fatalf("validateDisplayName(%q) = %v, want nil — refusing this locks a household member out of their own name", tc.value, err)
			}
			if !tc.reject {
				return
			}
			// The refusal must be the CONTENT one. Without this a mutant that
			// makes the LENGTH branch fire on everything would pass every row
			// above, and the table would be pinning "some error happened".
			if got := err.Error(); !strings.Contains(got, "may not contain line breaks") {
				t.Errorf("validateDisplayName(%q) refused with %q; want the content message, not the length one", tc.value, got)
			}
		})
	}
}

// TestValidateDisplayName_NamesTheOffendingCodepoint. Every character this
// guard refuses is invisible, so a bare "that name is not allowed" leaves the
// user with nothing to remove. The message carries the codepoint.
func TestValidateDisplayName_NamesTheOffendingCodepoint(t *testing.T) {
	err := validateDisplayName("Ali\u202EBot")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := err.Error(); !strings.Contains(got, "U+202E") {
		t.Errorf("refusal is %q; it must name the offending codepoint, because the character is invisible", got)
	}
	// The FIRST offender is the one named, not the last — otherwise the user
	// fixes one character at a time from the wrong end.
	err = validateDisplayName("Ali\u2028Bot\u202EEnd")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := err.Error(); !strings.Contains(got, "U+2028") {
		t.Errorf("refusal is %q; want the FIRST offending codepoint, U+2028", got)
	}
}

// TestValidateDisplayName_LengthBoundaryStillBehaves. The content rule was
// folded into the same validator as the length rule, so the 64/65 boundary and
// its exact message have to be re-pinned here — a content check placed before
// the length check would change the message an over-long name gets, and
// web/src/pages/Settings.test.tsx asserts on that string.
func TestValidateDisplayName_LengthBoundaryStillBehaves(t *testing.T) {
	for _, unit := range []struct {
		name  string
		value string
	}{
		{"arabic", arabicLetter},
		{"astral emoji", astralEmoji},
	} {
		t.Run(unit.name, func(t *testing.T) {
			if err := validateDisplayName(repeatTo(unit.value, MaxDisplayNameLength)); err != nil {
				t.Errorf("a %d-character name was refused: %v", MaxDisplayNameLength, err)
			}
			err := validateDisplayName(repeatTo(unit.value, MaxDisplayNameLength+1))
			if err == nil {
				t.Fatalf("a %d-character name was accepted", MaxDisplayNameLength+1)
			}
			want := "display name must be 64 characters or less"
			if got := err.Error(); got != want {
				t.Errorf("over-length refusal is %q, want %q — the frontend asserts on this exact string", got, want)
			}
		})
	}

	// An over-long name that ALSO contains a newline keeps the length message.
	// Length is checked first precisely so this case did not change wording.
	err := validateDisplayName(repeatTo(arabicLetter, MaxDisplayNameLength) + "\n" + arabicLetter)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := err.Error(); !strings.Contains(got, "characters or less") {
		t.Errorf("an over-long name containing a newline refused with %q; want the length message", got)
	}
}

// --- the four write paths, asserted on STORAGE ---
//
// Each of the four tests below asserts the SIDE EFFECT, not the status code.
// This package has a recorded incident where an authorization test passed on
// the status while the mutation went through underneath it, and a validation
// test is the same shape: a 400 written after the write would look identical.
//
// Each also submits cleanName through the same handler in the same test. A
// guard that refused every name would satisfy a rejection-only test.

// TestUpdateMe_RefusesAForgedNameAndLeavesTheStoredNameUntouched covers the one
// path a member can reach unaided, which is where a crafted name would arrive.
func TestUpdateMe_RefusesAForgedNameAndLeavesTheStoredNameUntouched(t *testing.T) {
	h := setupHandler(t)
	user, cookie := seedProfileUser(t, h, "dn-forge-self", "Original Ledger Label", RoleMember)

	rec := patchMe(t, h, jsonBody(map[string]any{"display_name": forgedName}), cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a name with an interior newline got %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "may not contain line breaks") {
		t.Errorf("refusal body is %s; want the content message", body)
	}
	if stored := reloadUser(t, h, user.ID); stored.DisplayName != "Original Ledger Label" {
		t.Errorf("stored display_name = %q, want it unchanged at %q — the 400 was written after the write",
			stored.DisplayName, "Original Ledger Label")
	}

	// Positive control: the same handler, the same session, a clean name.
	rec = patchMe(t, h, jsonBody(map[string]any{"display_name": cleanName}), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("a clean name got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if stored := reloadUser(t, h, user.ID); stored.DisplayName != cleanName {
		t.Errorf("stored display_name = %q, want %q", stored.DisplayName, cleanName)
	}
}

// TestUpdateMe_StoresArabicFrenchAndZWJEmojiVerBatim is the accept half at the
// handler, not just in the validator. It matters at this level because a name
// that survives validation can still be mangled on the way to the column.
func TestUpdateMe_StoresArabicFrenchAndZWJEmojiVerbatim(t *testing.T) {
	h := setupHandler(t)
	user, cookie := seedProfileUser(t, h, "dn-accept-self", "Original Ledger Label", RoleMember)

	for _, name := range []string{
		"علي عبد الله",
		"Éloïse Béchara",
		"Zenobia \U0001f469\u200D\U0001f4bb",
	} {
		rec := patchMe(t, h, jsonBody(map[string]any{"display_name": name}), cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("renaming to %q got %d, want 200; body: %s", name, rec.Code, rec.Body.String())
		}
		if stored := reloadUser(t, h, user.ID); stored.DisplayName != name {
			t.Errorf("stored display_name = %q, want %q byte-for-byte", stored.DisplayName, name)
		}
	}
}

// TestHandleUpdateUser_RefusesAForgedNameAndLeavesTheStoredNameUntouched.
// The admin path merges an unspecified field from the stored row, so the
// rejection has to happen before that merge writes anything.
func TestHandleUpdateUser_RefusesAForgedNameAndLeavesTheStoredNameUntouched(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "dn-forge-admin", RoleAdmin)
	target := seedTestUser(t, q, "dn-forge-target", RoleMember)

	update := func(displayName string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := jsonRequest(t, http.MethodPut, "/api/users/"+strconv.FormatInt(target.ID, 10),
			map[string]any{"display_name": displayName})
		h.handleUpdateUser(rec, withUserAndURLParam(req, admin, "id", strconv.FormatInt(target.ID, 10)))
		return rec
	}

	rec := update(forgedName)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a name with an interior newline got %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	stored := reloadUser(t, h, target.ID)
	if stored.DisplayName != target.DisplayName {
		t.Errorf("stored display_name = %q, want it unchanged at %q", stored.DisplayName, target.DisplayName)
	}

	rec = update(cleanName)
	if rec.Code != http.StatusOK {
		t.Fatalf("a clean name got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if stored := reloadUser(t, h, target.ID); stored.DisplayName != cleanName {
		t.Errorf("stored display_name = %q, want %q", stored.DisplayName, cleanName)
	}
}

// TestHandleUpdateUser_RoleOnlyEditSurvivesALegacyStoredName is the
// grandfathering case. There is no migration behind this rule, so a name stored
// before it exists keeps whatever it has — and validating the MERGED value
// rather than the request field would leave an admin unable to change that
// user's role at all.
func TestHandleUpdateUser_RoleOnlyEditSurvivesALegacyStoredName(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "dn-legacy-admin", RoleAdmin)

	// Seeded through the store, not the handler: this is a row that predates
	// the guard, which is the only way such a value can exist now.
	legacy, err := q.CreateUser(context.Background(), database.CreateUserParams{
		Username:     "dn-legacy-target",
		PasswordHash: "x",
		DisplayName:  "Legacy\nName",
		Role:         RoleMember,
	})
	if err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}

	rec := httptest.NewRecorder()
	req := jsonRequest(t, http.MethodPut, "/api/users/"+strconv.FormatInt(legacy.ID, 10),
		map[string]any{"role": RoleAdmin})
	h.handleUpdateUser(rec, withUserAndURLParam(req, admin, "id", strconv.FormatInt(legacy.ID, 10)))
	if rec.Code != http.StatusOK {
		t.Fatalf("a role-only edit of a legacy row got %d, want 200; body: %s — the guard is being applied to the merged value, which locks the row",
			rec.Code, rec.Body.String())
	}
	stored := reloadUser(t, h, legacy.ID)
	if stored.Role != RoleAdmin {
		t.Errorf("stored role = %q, want %q", stored.Role, RoleAdmin)
	}
	if stored.DisplayName != "Legacy\nName" {
		t.Errorf("stored display_name = %q, want the legacy value preserved", stored.DisplayName)
	}
}

// TestHandleCreateUser_RefusesAForgedNameAndCreatesNoRow. The side effect here
// is a whole account, so the assertion is the user list.
func TestHandleCreateUser_RefusesAForgedNameAndCreatesNoRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "dn-create-admin", RoleAdmin)

	create := func(username, displayName string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.handleCreateUser(rec, withUser(jsonRequest(t, http.MethodPost, "/api/users", map[string]any{
			"username":     username,
			"password":     "correct-horse-battery-staple",
			"display_name": displayName,
			"role":         RoleMember,
		}), admin))
		return rec
	}

	rec := create("dn-create-forged", forgedName)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a name with an interior newline got %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if _, err := q.GetUserByUsername(context.Background(), "dn-create-forged"); err == nil {
		t.Error("the refused request created a user row anyway")
	}

	rec = create("dn-create-clean", cleanName)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a clean name got %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	created, err := q.GetUserByUsername(context.Background(), "dn-create-clean")
	if err != nil {
		t.Fatalf("the accepted request created no user: %v", err)
	}
	if created.DisplayName != cleanName {
		t.Errorf("stored display_name = %q, want %q", created.DisplayName, cleanName)
	}
}

// TestHandleRegister_RefusesAForgedNameAndCreatesNoRow. Registration is the
// only one of the four an UNAUTHENTICATED caller can reach, and it carries its
// own copy of every other check — so it is the likeliest to drift.
func TestHandleRegister_RefusesAForgedNameAndCreatesNoRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	register := func(username, displayName string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := jsonRequest(t, http.MethodPost, "/api/auth/register", map[string]any{
			"username":     username,
			"password":     "correct-horse-battery-staple",
			"display_name": displayName,
		})
		req.RemoteAddr = "203.0.113.9:4321"
		h.handleRegister(rec, req)
		return rec
	}

	// Registration is open while the household has no users, so this call
	// reaches the display-name gate rather than the closed-registration 403.
	rec := register("dn-register-forged", forgedName)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a name with an interior newline got %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	users, err := q.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("the refused registration created %d user(s); want 0", len(users))
	}

	rec = register("dn-register-clean", cleanName)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a clean name got %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	created, err := q.GetUserByUsername(context.Background(), "dn-register-clean")
	if err != nil {
		t.Fatalf("the accepted registration created no user: %v", err)
	}
	if created.DisplayName != cleanName {
		t.Errorf("stored display_name = %q, want %q", created.DisplayName, cleanName)
	}
}

// TestDisplayNameFixtures_AreWhatTheyClaim guards every rejection test above.
// forgedName's newline must be INTERIOR: strings.TrimSpace runs on all four
// paths before the guard, so a leading or trailing newline is removed and the
// name is stored clean. A fixture that drifted to an edge would leave every
// test in this file asserting that TrimSpace exists.
func TestDisplayNameFixtures_AreWhatTheyClaim(t *testing.T) {
	if strings.TrimSpace(forgedName) != forgedName {
		t.Fatalf("forgedName %q loses characters to TrimSpace; the offending character must be INTERIOR or the handler tests prove nothing",
			forgedName)
	}
	if !strings.Contains(forgedName, "\n") {
		t.Fatal("forgedName no longer contains a newline")
	}
	if charLen(forgedName) > MaxDisplayNameLength {
		t.Fatalf("forgedName is %d characters against a %d cap; it would be refused for LENGTH and prove nothing about content",
			charLen(forgedName), MaxDisplayNameLength)
	}
	if validateDisplayName(cleanName) != nil {
		t.Fatal("cleanName is not clean; the positive controls are inert")
	}
}
