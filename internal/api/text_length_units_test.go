package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// The unit tests in this file all turn on one thing: the caps in limits.go are
// CHARACTER counts, and every check that enforces one must count characters
// too. They used to be compared with len(), which counts bytes, while the error
// messages said "characters" — so a description advertised as 500 characters
// was refused at about 250 in Arabic, and the refusal named a number the server
// was not applying.
//
// EVERY FIXTURE HERE IS MULTI-BYTE ON PURPOSE. An ASCII test passes identically
// under both units and proves nothing at all; the whole class of defect is
// invisible until the input costs more than one byte per character. The
// household these limits are for writes Arabic alongside English.
//
// The load-bearing assertion in each case is the AT-THE-LIMIT one. A value one
// character over the cap is refused under either unit, so that direction alone
// cannot tell a character check from a byte check — it only pins that a check
// exists. A value exactly at the cap is accepted under characters and refused
// under bytes, which is what makes these tests go red if the enforcement slips
// back to len().
const (
	// arabicLetter is 2 bytes in UTF-8 and 1 character. It is also 1 UTF-16
	// code unit, which is why the frontend's String.length agrees here and
	// diverges on the emoji below — see web/src/lib/text.ts.
	arabicLetter = "ب"
	// astralEmoji is 4 bytes in UTF-8 and 1 character (2 UTF-16 code units).
	// It is the worst case for a byte bound and the case a naive frontend
	// .length check gets wrong.
	astralEmoji = "🧾"
)

// TestTextLengthFixtures_AreActuallyMultiByte guards every other test in this
// file. If someone replaced these constants with ASCII — or a tool normalised
// the file — the rest of the suite would keep passing while asserting nothing,
// because a byte check and a character check agree on ASCII.
func TestTextLengthFixtures_AreActuallyMultiByte(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		bytes int
	}{
		{"arabicLetter", arabicLetter, 2},
		{"astralEmoji", astralEmoji, 4},
	} {
		if got := len(tc.value); got != tc.bytes {
			t.Errorf("%s is %d bytes, want %d — the fixture is no longer multi-byte and every test using it has gone vacuous",
				tc.name, got, tc.bytes)
		}
		if got := charLen(tc.value); got != 1 {
			t.Errorf("%s is %d characters, want 1", tc.name, got)
		}
	}
}

// TestCharLen_CountsCharactersNotBytes pins the helper itself, so a
// reimplementation that reaches for len() is caught here rather than diffusely
// across a dozen handlers.
func TestCharLen_CountsCharactersNotBytes(t *testing.T) {
	value := strings.Repeat(arabicLetter, 10) + strings.Repeat(astralEmoji, 5)
	if got, want := charLen(value), 15; got != want {
		t.Errorf("charLen = %d, want %d", got, want)
	}
	if got, want := len(value), 40; got != want {
		t.Fatalf("fixture is %d bytes, want %d — the test is not exercising the divergence", got, want)
	}
}

// repeatTo returns n copies of a single-character unit.
func repeatTo(unit string, n int) string { return strings.Repeat(unit, n) }

// TestValidateTransactionRequest_TextLimitsAreCharacters covers the single-row
// write path — the one every manual entry, every edit and every quick-add goes
// through.
func TestValidateTransactionRequest_TextLimitsAreCharacters(t *testing.T) {
	base := func() transactionRequest {
		return transactionRequest{
			Date:        "2026-01-15",
			Description: "ok",
			Amount:      1,
			CategoryID:  1,
		}
	}

	for _, unit := range []struct {
		name string
		char string
	}{
		{"arabic", arabicLetter},
		{"emoji", astralEmoji},
	} {
		t.Run(unit.name, func(t *testing.T) {
			for _, field := range []struct {
				name  string
				limit int
				set   func(*transactionRequest, string)
			}{
				{"description", MaxDescriptionLength, func(r *transactionRequest, v string) { r.Description = v }},
				{"tags", MaxTagsLength, func(r *transactionRequest, v string) { r.Tags = &v }},
				{"notes", MaxNotesLength, func(r *transactionRequest, v string) { r.Notes = &v }},
			} {
				t.Run(field.name, func(t *testing.T) {
					atLimit := base()
					field.set(&atLimit, repeatTo(unit.char, field.limit))
					if err := validateTransactionRequest(atLimit, noStoredDate); err != nil {
						t.Errorf("%d characters was refused by a limit of %d: %v — the check is counting bytes, so the field rejects text well inside the limit it advertises",
							field.limit, field.limit, err)
					}

					overLimit := base()
					field.set(&overLimit, repeatTo(unit.char, field.limit+1))
					if err := validateTransactionRequest(overLimit, noStoredDate); err == nil {
						t.Errorf("%d characters was accepted by a limit of %d — the cap is not being applied at all",
							field.limit+1, field.limit)
					}
				})
			}
		})
	}
}

// TestValidateDescription_LimitIsCharacters covers the bulk-edit helper, which
// is a separate implementation from the single-row check above (it trims first)
// and so needs its own boundary assertion.
func TestValidateDescription_LimitIsCharacters(t *testing.T) {
	atLimit := repeatTo(arabicLetter, MaxDescriptionLength)
	if _, err := validateDescription(atLimit); err != nil {
		t.Errorf("%d characters refused by a %d-character limit: %v",
			MaxDescriptionLength, MaxDescriptionLength, err)
	}
	if _, err := validateDescription(repeatTo(arabicLetter, MaxDescriptionLength+1)); err == nil {
		t.Errorf("%d characters accepted by a %d-character limit", MaxDescriptionLength+1, MaxDescriptionLength)
	}
}

// TestValidateTagsField_LimitIsCharacters — same, for the bulk-edit tags helper.
func TestValidateTagsField_LimitIsCharacters(t *testing.T) {
	if _, err := validateTagsField(repeatTo(arabicLetter, MaxTagsLength)); err != nil {
		t.Errorf("%d characters refused by a %d-character limit: %v", MaxTagsLength, MaxTagsLength, err)
	}
	if _, err := validateTagsField(repeatTo(arabicLetter, MaxTagsLength+1)); err == nil {
		t.Errorf("%d characters accepted by a %d-character limit", MaxTagsLength+1, MaxTagsLength)
	}
}

// TestValidateCheckpointRequest_NoteLimitIsCharacters — the checkpoint note is
// bounded by MaxNotesLength through its own check.
func TestValidateCheckpointRequest_NoteLimitIsCharacters(t *testing.T) {
	base := func(note string) checkpointRequest {
		return checkpointRequest{
			ScopeType:      "total",
			Date:           "2026-01-15",
			ExpectedAmount: 10,
			Note:           note,
		}
	}
	if err := validateCheckpointRequest(base(repeatTo(arabicLetter, MaxNotesLength))); err != nil {
		t.Errorf("%d characters refused by a %d-character limit: %v", MaxNotesLength, MaxNotesLength, err)
	}
	if err := validateCheckpointRequest(base(repeatTo(arabicLetter, MaxNotesLength+1))); err == nil {
		t.Errorf("%d characters accepted by a %d-character limit", MaxNotesLength+1, MaxNotesLength)
	}
}

// TestCheckImportRowLengths_LimitsAreCharacters covers the import preview gate.
// TestImportFieldLengths_MatchTheWritePath already pins that this gate agrees
// with the ledger; this pins WHICH quantity they agree on, which that test
// cannot — two byte checks agree with each other perfectly.
func TestCheckImportRowLengths_LimitsAreCharacters(t *testing.T) {
	for _, tc := range []struct {
		field string
		limit int
		set   func(*importRow, string)
	}{
		{"description", MaxDescriptionLength, func(r *importRow, v string) { r.Description = v }},
		{"tags", MaxTagsLength, func(r *importRow, v string) { r.Tags = v }},
		{"notes", MaxNotesLength, func(r *importRow, v string) { r.Notes = v }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			atLimit := importRow{RowID: 1, Description: "ok", Amount: 1}
			tc.set(&atLimit, repeatTo(arabicLetter, tc.limit))
			if got := checkImportRowLengths([]importRow{atLimit}); len(got) != 0 {
				t.Errorf("%d characters flagged against a %d-character limit: %+v — the preview blocks a row the ledger would take",
					tc.limit, tc.limit, got)
			}

			overLimit := importRow{RowID: 1, Description: "ok", Amount: 1}
			tc.set(&overLimit, repeatTo(arabicLetter, tc.limit+1))
			if got := checkImportRowLengths([]importRow{overLimit}); len(got) == 0 {
				t.Errorf("%d characters not flagged against a %d-character limit", tc.limit+1, tc.limit)
			}
		})
	}
}

// TestValidateImportField_DescriptionLimitIsCharacters covers the preview's
// PATCH route, which re-validates an edited cell through a separate check.
func TestValidateImportField_DescriptionLimitIsCharacters(t *testing.T) {
	_, code, msg := validateImportField("description", repeatTo(arabicLetter, MaxDescriptionLength))
	if code != "" {
		t.Errorf("%d characters refused by a %d-character limit: %s / %s",
			MaxDescriptionLength, MaxDescriptionLength, code, msg)
	}
	if _, code, _ := validateImportField("description", repeatTo(arabicLetter, MaxDescriptionLength+1)); code == "" {
		t.Errorf("%d characters accepted by a %d-character limit", MaxDescriptionLength+1, MaxDescriptionLength)
	}
}

// jsonRequest builds an authenticated JSON request for the handler-level tests
// below. Handlers read auth.GetUser from the context, not from a cookie.
func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
}

// TestHandleCreateCategory_NameLimitIsCharacters — category names are the
// field most likely to be written in Arabic, since they are the household's own
// vocabulary rather than a merchant's.
func TestHandleCreateCategory_NameLimitIsCharacters(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "cat-charlimit-admin", "admin")

	atLimit := repeatTo(arabicLetter, MaxCategoryNameLength)
	rec := httptest.NewRecorder()
	h.handleCreateCategory(rec, withUser(jsonRequest(t, http.MethodPost, "/api/categories",
		map[string]any{"name": atLimit, "type": "expense"}), admin))
	if rec.Code != http.StatusCreated {
		t.Errorf("a %d-character name got %d, want 201: %s",
			MaxCategoryNameLength, rec.Code, rec.Body.String())
	}

	over := repeatTo(arabicLetter, MaxCategoryNameLength+1)
	rec = httptest.NewRecorder()
	h.handleCreateCategory(rec, withUser(jsonRequest(t, http.MethodPost, "/api/categories",
		map[string]any{"name": over, "type": "expense"}), admin))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-character name got %d, want 400", MaxCategoryNameLength+1, rec.Code)
	}
}

// TestHandleUpdateCategory_IconLimitIsCharacters — the icon field carries its
// own cap and its own check, so it needs its own boundary.
func TestHandleUpdateCategory_IconLimitIsCharacters(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "icon-charlimit-admin", "admin")
	cat := seedTestCategory(t, q, "IconLimit-"+t.Name(), "expense")

	over := repeatTo(arabicLetter, MaxIconNameLength+1)
	rec := httptest.NewRecorder()
	h.handleUpdateCategory(rec, withUserAndURLParam(
		jsonRequest(t, http.MethodPut, "/api/categories/1",
			map[string]any{"name": "ok", "icon": over}),
		admin, "id", itoa(cat.ID)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a %d-character icon got %d, want 400", MaxIconNameLength+1, rec.Code)
	}

	atLimit := repeatTo(arabicLetter, MaxIconNameLength)
	rec = httptest.NewRecorder()
	h.handleUpdateCategory(rec, withUserAndURLParam(
		jsonRequest(t, http.MethodPut, "/api/categories/1",
			map[string]any{"name": "ok", "icon": atLimit}),
		admin, "id", itoa(cat.ID)))
	if rec.Code != http.StatusOK {
		t.Errorf("a %d-character icon got %d, want 200: %s",
			MaxIconNameLength, rec.Code, rec.Body.String())
	}
}

// TestHandleCreateCurrency_NameAndSymbolLimitsAreCharacters. The symbol matters
// concretely here: the Lebanese pound is written "ل.ل.", four characters and
// eight bytes, and this household keeps its ledger in LBP and USD.
func TestHandleCreateCurrency_NameAndSymbolLimitsAreCharacters(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "cur-charlimit-admin", "admin")

	post := func(code, name, symbol string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.handleCreateCurrency(rec, withUser(jsonRequest(t, http.MethodPost, "/api/currencies",
			map[string]any{"code": code, "name": name, "symbol": symbol, "rate_to_base": 1.5}), admin))
		return rec
	}

	rec := post("AAA", repeatTo(arabicLetter, MaxCurrencyNameLength), "$")
	if rec.Code != http.StatusCreated {
		t.Errorf("a %d-character currency name got %d, want 201: %s",
			MaxCurrencyNameLength, rec.Code, rec.Body.String())
	}
	rec = post("BBB", repeatTo(arabicLetter, MaxCurrencyNameLength+1), "$")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-character currency name got %d, want 400", MaxCurrencyNameLength+1, rec.Code)
	}

	rec = post("CCC", "ok", repeatTo(arabicLetter, MaxCurrencySymbolLength))
	if rec.Code != http.StatusCreated {
		t.Errorf("a %d-character symbol got %d, want 201: %s",
			MaxCurrencySymbolLength, rec.Code, rec.Body.String())
	}
	rec = post("DDD", "ok", repeatTo(arabicLetter, MaxCurrencySymbolLength+1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-character symbol got %d, want 400", MaxCurrencySymbolLength+1, rec.Code)
	}
}

// TestHandleCreateSavedFilter_NameIsCharactersButJSONStaysBytes asserts the
// split unit in one place, because the two live four lines apart and the whole
// risk is that someone "fixes" the second one for consistency.
//
// The name is prose a human typed, so it is bounded in characters. filter_json
// is a serialized payload the client assembled, so it is bounded in bytes —
// what needs bounding there is transport and storage cost, not a count anyone
// reads.
func TestHandleCreateSavedFilter_NameIsCharactersButJSONStaysBytes(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "filter-charlimit", "member")

	post := func(name, filterJSON string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.handleCreateSavedFilter(rec, withUser(jsonRequest(t, http.MethodPost, "/api/filters",
			map[string]any{"name": name, "filter_json": filterJSON}), user))
		return rec
	}

	rec := post(repeatTo(arabicLetter, MaxFilterNameLength), `{"a":1}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("a %d-character filter name got %d, want 201: %s",
			MaxFilterNameLength, rec.Code, rec.Body.String())
	}
	rec = post(repeatTo(arabicLetter, MaxFilterNameLength+1), `{"a":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-character filter name got %d, want 400", MaxFilterNameLength+1, rec.Code)
	}

	// A payload whose CHARACTER count is inside the cap but whose BYTE count is
	// over must still be refused. That is the only shape the two units disagree
	// on, so it is the assertion that keeps filter_json in bytes: a character
	// bound accepts this, and a byte bound refuses it.
	//
	// It is also valid JSON, so a 400 cannot be coming from json.Valid instead.
	wideButShort := `{"q":"` + repeatTo(arabicLetter, MaxFilterJSONLength/2+100) + `"}`
	if charLen(wideButShort) > MaxFilterJSONLength {
		t.Fatalf("fixture is %d characters against a %d cap; it must be INSIDE the cap in characters or it proves nothing about the unit",
			charLen(wideButShort), MaxFilterJSONLength)
	}
	if len(wideButShort) <= MaxFilterJSONLength {
		t.Fatalf("fixture is %d bytes against a %d cap; it must EXCEED the cap in bytes",
			len(wideButShort), MaxFilterJSONLength)
	}
	if !json.Valid([]byte(wideButShort)) {
		t.Fatal("fixture is not valid JSON; a 400 would prove nothing about the length unit")
	}
	rec = post("ok", wideButShort)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("filter_json of %d bytes (%d characters) got %d, want 400 — the JSON cap has been converted to characters, and it should not be: it bounds a serialized payload, not prose",
			len(wideButShort), charLen(wideButShort), rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "bytes") {
		t.Errorf("filter_json rejection says %q; it must name BYTES, because that is the unit being applied", body)
	}
}

// TestHandleCreateUser_DisplayNameLimitIsCharacters. A display name is the one
// piece of the account a household member picks for themselves, so it is the
// likeliest place for a non-Latin script on the auth surface.
func TestHandleCreateUser_DisplayNameLimitIsCharacters(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "dn-charlimit-admin", "admin")

	create := func(username, displayName string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.handleCreateUser(rec, withUser(jsonRequest(t, http.MethodPost, "/api/users", map[string]any{
			"username":     username,
			"password":     "correct-horse-battery-staple",
			"display_name": displayName,
			"role":         "member",
		}), admin))
		return rec
	}

	rec := create("dn-at-limit", repeatTo(arabicLetter, MaxDisplayNameLength))
	if rec.Code != http.StatusCreated {
		t.Errorf("a %d-character display name got %d, want 201: %s",
			MaxDisplayNameLength, rec.Code, rec.Body.String())
	}
	rec = create("dn-over-limit", repeatTo(arabicLetter, MaxDisplayNameLength+1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-character display name got %d, want 400", MaxDisplayNameLength+1, rec.Code)
	}
}

// TestCreateAPIToken_NameLimitIsCharacters_AndTheColumnAgrees is the one case
// where the byte bound made the handler stricter than the app's own schema:
// migration 011 puts CHECK(length(name) BETWEEN 1 AND 100) on api_tokens.name,
// and SQLite's length() counts CHARACTERS on a TEXT value. The 201 below is
// therefore proof of both halves at once — the handler admitted 100 characters
// AND the row survived the column's own constraint.
func TestCreateAPIToken_NameLimitIsCharacters_AndTheColumnAgrees(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "token-charlimit")

	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": repeatTo(arabicLetter, apiTokenNameMax),
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Errorf("a %d-character token name got %d, want 201: %s — the handler is stricter than the column it writes to",
			apiTokenNameMax, rec.Code, rec.Body.String())
	}

	rec = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": repeatTo(arabicLetter, apiTokenNameMax+1),
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-character token name got %d, want 400", apiTokenNameMax+1, rec.Code)
	}
}

// TestHandleBulkRename_NewDescriptionLimitIsCharacters. Bulk rename writes a
// description to every matching row through raw SQL rather than through
// validateTransactionRequest, so it carries its own copy of the cap and needs
// its own boundary assertion.
func TestHandleBulkRename_NewDescriptionLimitIsCharacters(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "rename-charlimit", "member")
	cat := seedTestCategory(t, h.queries, "Rename-"+t.Name(), "expense")
	seedTestTransaction(t, h.queries, user.ID, cat.ID, "2026-04-01", 10, "old name")

	rename := func(newDescription string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.handleBulkRename(rec, withUser(jsonRequest(t, http.MethodPut, "/api/transactions/bulk-rename",
			map[string]any{"search": "old name", "new_description": newDescription}), user))
		return rec
	}

	rec := rename(repeatTo(arabicLetter, MaxDescriptionLength))
	if rec.Code != http.StatusOK {
		t.Errorf("a %d-character new_description got %d, want 200: %s",
			MaxDescriptionLength, rec.Code, rec.Body.String())
	}
	rec = rename(repeatTo(arabicLetter, MaxDescriptionLength+1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-character new_description got %d, want 400", MaxDescriptionLength+1, rec.Code)
	}
}

// TestHandleDismissRecurring_DescriptionLimitIsCharacters. The dismissed-
// recurring list stores the description it was given, so it applies the same
// cap through a check of its own.
func TestHandleDismissRecurring_DescriptionLimitIsCharacters(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "dismiss-charlimit", "member")

	dismiss := func(description string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.handleDismissRecurring(rec, withUser(jsonRequest(t, http.MethodPost, "/api/reports/dismiss-recurring",
			map[string]any{"year": 2026, "description": description}), user))
		return rec
	}

	rec := dismiss(repeatTo(arabicLetter, MaxDescriptionLength))
	if rec.Code != http.StatusOK {
		t.Errorf("a %d-character description got %d, want 200: %s",
			MaxDescriptionLength, rec.Code, rec.Body.String())
	}
	rec = dismiss(repeatTo(arabicLetter, MaxDescriptionLength+1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a %d-character description got %d, want 400", MaxDescriptionLength+1, rec.Code)
	}
}

// TestHandleRegister_DisplayNameLimitIsCharacters. Registration carries its own
// display-name check, separate from handleCreateUser's, so the two can drift.
func TestHandleRegister_DisplayNameLimitIsCharacters(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	register := func(username, displayName string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := jsonRequest(t, http.MethodPost, "/api/auth/register", map[string]any{
			"username":     username,
			"password":     "correct-horse-battery-staple",
			"display_name": displayName,
		})
		req.RemoteAddr = "203.0.113.7:4321"
		h.handleRegister(rec, req)
		return rec
	}

	// Registration is open while no user exists, so the first call is the one
	// that can reach 201; the over-limit call is rejected before that matters.
	rec := register("reg-over-limit", repeatTo(arabicLetter, MaxDisplayNameLength+1))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a %d-character display name got %d, want 400: %s",
			MaxDisplayNameLength+1, rec.Code, rec.Body.String())
	}

	rec = register("reg-at-limit", repeatTo(arabicLetter, MaxDisplayNameLength))
	if rec.Code != http.StatusCreated {
		t.Errorf("a %d-character display name got %d, want 201: %s",
			MaxDisplayNameLength, rec.Code, rec.Body.String())
	}
}

// TestPasswordBounds_StayByteBounded is a CARVE-OUT guard, not a limit test.
// It exists to fail if someone sweeps the password bounds into charLen along
// with everything else in this change.
//
// bcrypt hashes at most the first 72 bytes of its input and silently discards
// the rest. A character bound would accept a password whose tail never reaches
// the hasher, and two distinct passwords agreeing on their first 72 bytes would
// then authenticate the same account. config.Validate caps
// PASSWORD_MAX_LENGTH at 72 for the same reason.
//
// The probe is a password whose CHARACTER count is comfortably inside the cap
// but whose BYTE count is over it: accepted under characters, refused under
// bytes, which is exactly the discrimination this guard needs.
func TestPasswordBounds_StayByteBounded(t *testing.T) {
	_, maxLen := getPasswordBounds()
	probe := repeatTo(arabicLetter, maxLen/2+1)
	if charLen(probe) > maxLen {
		t.Fatalf("probe is %d characters against a %d cap; it must be INSIDE the cap in characters for this to discriminate", charLen(probe), maxLen)
	}
	if len(probe) <= maxLen {
		t.Fatalf("probe is %d bytes against a %d cap; it must EXCEED the cap in bytes", len(probe), maxLen)
	}

	if msg := validateNewPassword(probe); msg == "" {
		t.Errorf("a %d-byte password was accepted against a %d-byte cap — the password bound has been converted to characters, and bcrypt will silently drop everything past byte 72",
			len(probe), maxLen)
	}

	// And the message names the unit actually being applied. Saying
	// "characters" here would name a limit the server is not enforcing.
	msg := validateNewPassword(probe)
	if !strings.Contains(msg, "bytes") {
		t.Errorf("password rejection reads %q; it must say bytes, because that is what bcrypt consumes", msg)
	}
}

// TestUsernameCharsetGate_KeepsBytesEqualToCharacters pins the premise the
// username byte bound rests on. MinUsernameLength / MaxUsernameLength are
// compared with len(), which is only equivalent to a character count because
// isValidUsername admits nothing but one-byte ASCII. Widen that charset to
// Unicode letters and the bound silently becomes a byte bound wearing a
// character label again — this test is what says so.
func TestUsernameCharsetGate_KeepsBytesEqualToCharacters(t *testing.T) {
	for _, name := range []string{
		repeatTo(arabicLetter, 5),
		"user" + arabicLetter,
		"user" + astralEmoji,
		"café",
	} {
		if isValidUsername(name) {
			t.Errorf("isValidUsername(%q) = true — the username charset now admits multi-byte characters, so the len() bounds in handleRegister and handleCreateUser are no longer character counts and must move to charLen",
				name)
		}
	}
	if !isValidUsername("ok_user-1") {
		t.Error("isValidUsername rejected a plain ASCII username; the fixtures above are no longer testing what they claim")
	}
}

// TestSanitizeLogValue_TruncatesOnARuneBoundary. The cap here is a byte budget
// on one log line and stays one, but the cut has to land between characters:
// Arabic is two bytes per character, so a bare s[:100] slices mid-character
// half the time and writes a broken UTF-8 sequence into the log.
func TestSanitizeLogValue_TruncatesOnARuneBoundary(t *testing.T) {
	// An odd-length ASCII prefix forces every following character boundary onto
	// an odd offset, so the 100-byte cut falls INSIDE a character.
	value := "x" + strings.Repeat(arabicLetter, 200)
	got := sanitizeLogValue(value)

	if !utf8.ValidString(got) {
		t.Errorf("sanitizeLogValue returned invalid UTF-8 (%q); the truncation sliced a character in half", got)
	}
	if len(got) > sanitizeLogValueMaxBytes {
		t.Errorf("sanitizeLogValue returned %d bytes, want at most %d", len(got), sanitizeLogValueMaxBytes)
	}
	// It still truncates: a guard that returned the input untouched would pass
	// the UTF-8 check trivially.
	if got == value {
		t.Error("sanitizeLogValue did not truncate at all")
	}
}
