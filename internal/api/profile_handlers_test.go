package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// profileRouter wires PATCH /api/auth/me exactly as production router.go does:
// RequireAuthOrAPIToken + requireJSONContentType, mounted under /api/auth.
//
// The tests below go through the router rather than calling the handler with a
// user injected into the context, because the property under test is partly the
// ROUTE. A handler-level call cannot see whether the path carries an id a caller
// could aim at someone else, and a 401 asserted against a bare handler proves
// only that the handler checks the context — not that the route is authed.
func profileRouter(h *Handler) chi.Router {
	limiter := ratelimit.NewBucket(30, 10*time.Minute, h.clock)
	r := chi.NewRouter()
	r.Route("/api/auth", func(r chi.Router) {
		r.With(auth.RequireAuthOrAPIToken(h.queries, limiter)).
			With(requireJSONContentType).
			Patch("/me", h.handleUpdateMe)
	})
	return r
}

// seedProfileUser creates a user whose display name is DISJOINT from its
// username and from every name these tests rename to, and gives it one live
// session cookie.
//
// The disjointness is deliberate. This package has a recorded case of a rename
// assertion passing against the stale value because the old and new names shared
// a prefix; names with no substring in common make "did the write land" and "did
// the write land on the right row" both decidable.
func seedProfileUser(t *testing.T, h *Handler, username, displayName, role string) (database.User, *http.Cookie) {
	t.Helper()
	auth.SetBcryptCostForTesting()
	hash, err := auth.HashPassword("profile-password-" + username)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := h.queries.CreateUser(context.Background(), database.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		DisplayName:  displayName,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	token, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate session token: %v", err)
	}
	// Sessions are stored HASHED; persist the hash, hand the browser the
	// plaintext. Seeding the plaintext would make every "the session survived"
	// assertion below run against a row production could never have resolved.
	if err := h.queries.CreateSession(context.Background(), database.CreateSessionParams{
		Token:     auth.HashSessionToken(token),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session for %s: %v", username, err)
	}
	return user, &http.Cookie{Name: "session", Value: token}
}

// reloadUser re-reads a user row so an assertion is made against storage rather
// than against the response body.
func reloadUser(t *testing.T, h *Handler, id int64) database.User {
	t.Helper()
	u, err := h.queries.GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatalf("reload user %d: %v", id, err)
	}
	return u
}

// patchMe sends one PATCH /api/auth/me through the production middleware chain.
func patchMe(t *testing.T, h *Handler, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return doJSONRequest(t, profileRouter(h), http.MethodPatch, "/api/auth/me", body, cookie)
}

// --- happy path ---

func TestUpdateMe_RenamesTheCallerAndReturnsTheNewProfile(t *testing.T) {
	h := setupHandler(t)
	user, cookie := seedProfileUser(t, h, "profile-member", "Original Ledger Label", RoleMember)

	rec := patchMe(t, h, `{"display_name":"Zenobia Karam"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// The body is decoded into a map, not a typed struct: a typed decode
	// zero-fills a missing or renamed key and would report a blank display name
	// as an empty string rather than as an absent field.
	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["display_name"] != "Zenobia Karam" {
		t.Errorf("response display_name = %v, want %q", resp["display_name"], "Zenobia Karam")
	}
	if resp["username"] != "profile-member" {
		t.Errorf("response username = %v, want %q", resp["username"], "profile-member")
	}
	if resp["role"] != RoleMember {
		t.Errorf("response role = %v, want %q", resp["role"], RoleMember)
	}
	if _, leaked := resp["password_hash"]; leaked {
		t.Error("response leaked password_hash")
	}

	// The response could be assembled from the request. Storage is the claim.
	stored := reloadUser(t, h, user.ID)
	if stored.DisplayName != "Zenobia Karam" {
		t.Errorf("stored display_name = %q, want %q — the rename did not persist",
			stored.DisplayName, "Zenobia Karam")
	}
	if stored.Username != user.Username {
		t.Errorf("stored username = %q, want it unchanged at %q", stored.Username, user.Username)
	}
	if stored.Role != user.Role {
		t.Errorf("stored role = %q, want it unchanged at %q", stored.Role, user.Role)
	}
	if stored.PasswordHash != user.PasswordHash {
		t.Error("stored password_hash changed during a rename")
	}
}

// TestUpdateMe_AdminCanRenameThemselves pins that the route is NOT admin-gated
// in either direction. The whole point of the endpoint is that a member can
// reach it, and the obvious way to get that wrong on a second pass is to move
// it under a RequireAdmin group by symmetry with /api/users — but an admin
// shortening their own name (now that it labels every transaction they enter) is
// the case this endpoint was asked for, so it has to work for both roles.
func TestUpdateMe_AdminCanRenameThemselves(t *testing.T) {
	h := setupHandler(t)
	admin, cookie := seedProfileUser(t, h, "profile-admin", "Household Administrator", RoleAdmin)

	rec := patchMe(t, h, `{"display_name":"Elie"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("an admin renaming themselves got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	stored := reloadUser(t, h, admin.ID)
	if stored.DisplayName != "Elie" {
		t.Errorf("stored display_name = %q, want %q", stored.DisplayName, "Elie")
	}
	if stored.Role != RoleAdmin {
		t.Errorf("stored role = %q, want it unchanged at %q — a rename must not move a privilege",
			stored.Role, RoleAdmin)
	}
}

// --- the blast radius ---

// TestUpdateMe_LeavesEveryOtherUserUntouched is the privilege-escalation guard.
// The body names the other user in every way a caller might reasonably try, and
// the assertion is the OTHER ROW's state, not the status code: this package has
// a recorded incident where an authorization test passed on the status while the
// mutation went through underneath it.
//
// The caller's own rename must land as well, which is what keeps the assertion
// from going vacuous — "the other row is untouched" is free on a request that
// did nothing at all.
func TestUpdateMe_LeavesEveryOtherUserUntouched(t *testing.T) {
	h := setupHandler(t)
	_, cookie := seedProfileUser(t, h, "profile-attacker", "Attacker Own Label", RoleMember)
	victim, _ := seedProfileUser(t, h, "profile-victim", "Victim Ledger Label", RoleAdmin)

	body := `{"display_name":"Renamed By The Caller",` +
		`"id":` + strconv.FormatInt(victim.ID, 10) + `,` +
		`"user_id":` + strconv.FormatInt(victim.ID, 10) + `,` +
		`"username":"profile-victim"}`

	rec := patchMe(t, h, body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// The request reached the write path — so the victim assertions below are
	// about targeting, not about a request that no-op'd.
	if got := reloadUser(t, h, mustGetUser(t, h.queries, "profile-attacker").ID).DisplayName; got != "Renamed By The Caller" {
		t.Fatalf("the caller's own display_name = %q, want %q — the write did not happen, so the victim assertions below would prove nothing",
			got, "Renamed By The Caller")
	}

	stillVictim := reloadUser(t, h, victim.ID)
	if stillVictim.DisplayName != "Victim Ledger Label" {
		t.Errorf("the OTHER user's display_name = %q, want it unchanged at %q — a caller named a target in the body and the handler honoured it",
			stillVictim.DisplayName, "Victim Ledger Label")
	}
	if stillVictim.Username != victim.Username {
		t.Errorf("the OTHER user's username = %q, want it unchanged at %q", stillVictim.Username, victim.Username)
	}
	if stillVictim.Role != RoleAdmin {
		t.Errorf("the OTHER user's role = %q, want it unchanged at %q", stillVictim.Role, RoleAdmin)
	}
	if stillVictim.PasswordHash != victim.PasswordHash {
		t.Error("the OTHER user's password_hash changed")
	}
}

// TestUpdateMe_IgnoresOtherFields is the field-set guard on the caller's OWN
// row, which is where a widened request struct would do its damage: a member who
// can post "role":"admin" to an endpoint they are entitled to call has escalated
// themselves. It fails the moment anyone adds a second field to the handler's
// request struct, which is the review this endpoint most needs.
func TestUpdateMe_IgnoresOtherFields(t *testing.T) {
	h := setupHandler(t)
	user, cookie := seedProfileUser(t, h, "profile-escalator", "Escalator Ledger Label", RoleMember)

	rec := patchMe(t, h, `{"display_name":"Just A Rename","role":"admin","username":"root","password":"hunter2","password_hash":"$2a$04$forged"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	stored := reloadUser(t, h, user.ID)
	if stored.DisplayName != "Just A Rename" {
		t.Fatalf("display_name = %q, want %q — the rename did not land, so the assertions below would prove nothing",
			stored.DisplayName, "Just A Rename")
	}
	if stored.Role != RoleMember {
		t.Errorf("role = %q, want it unchanged at %q — a member escalated themselves through the profile endpoint",
			stored.Role, RoleMember)
	}
	if stored.Username != "profile-escalator" {
		t.Errorf("username = %q, want it unchanged at %q", stored.Username, "profile-escalator")
	}
	if stored.PasswordHash != user.PasswordHash {
		t.Error("password_hash changed — the profile endpoint wrote a credential")
	}
}

// --- validation ---

func TestUpdateMe_EmptyOrWhitespaceName_Returns400AndKeepsTheStoredName(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty string", `{"display_name":""}`},
		{"spaces", `{"display_name":"   "}`},
		{"tabs and newlines", "{\"display_name\":\"\\t\\n \"}"},
		{"key absent", `{}`},
		// Non-breaking space and an ideographic space: both are Unicode
		// whitespace, so strings.TrimSpace removes them and this is the same
		// input as "". A trim that reached for a hand-rolled ASCII-only check
		// would store a name that renders as blank in every transaction row.
		{"unicode whitespace", "{\"display_name\":\"\\u00a0\\u3000\"}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setupHandler(t)
			user, cookie := seedProfileUser(t, h, "profile-blank", "Kept Ledger Label", RoleMember)

			rec := patchMe(t, h, tc.body, cookie)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
			}
			if stored := reloadUser(t, h, user.ID); stored.DisplayName != "Kept Ledger Label" {
				t.Errorf("stored display_name = %q, want it unchanged at %q — a refused request still wrote to the column",
					stored.DisplayName, "Kept Ledger Label")
			}
		})
	}
}

func TestUpdateMe_InvalidJSON_Returns400(t *testing.T) {
	h := setupHandler(t)
	user, cookie := seedProfileUser(t, h, "profile-badjson", "Kept Ledger Label", RoleMember)

	rec := patchMe(t, h, `not json`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if stored := reloadUser(t, h, user.ID); stored.DisplayName != "Kept Ledger Label" {
		t.Errorf("stored display_name = %q, want it unchanged", stored.DisplayName)
	}
}

// --- authentication ---

// TestUpdateMe_Unauthenticated_Returns401 runs against the PRODUCTION router,
// because what it is really asking is whether the route is public. /api/auth is
// the one group in this app whose siblings (register, login, logout) are
// deliberately unauthenticated, so "is this route authed" is a per-route fact
// there, not something the group settles.
//
// The row assertion is the load-bearing half: a 401 alone would also be produced
// by a route that wrote first and refused afterwards.
func TestUpdateMe_Unauthenticated_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)
	cookie := registerViaRouter(t, router, "anonvictim")
	if cookie == nil {
		t.Fatal("no session cookie from register")
	}
	victim := mustGetUser(t, q, "anonvictim")

	req := httptest.NewRequest(http.MethodPatch, "/api/auth/me",
		strings.NewReader(`{"display_name":"Written Without A Session"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an anonymous PATCH /api/auth/me got %d, want 401; body: %s", rec.Code, rec.Body.String())
	}
	if got := mustGetUser(t, q, "anonvictim").DisplayName; got != victim.DisplayName {
		t.Errorf("display_name = %q, want it unchanged at %q — an unauthenticated request wrote to the users table",
			got, victim.DisplayName)
	}
}

// TestUpdateMe_HandlerRefusesAMissingUser_Returns401 pins the handler's OWN
// guard, which the route middleware makes unreachable in production. That is
// exactly why it needs its own test: a defence layer that never fires while the
// layer above it works can be deleted without a single test noticing, and this
// one is what would answer if the route were ever moved to a group whose
// middleware does not populate the context.
func TestUpdateMe_HandlerRefusesAMissingUser_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodPatch, "/api/auth/me",
		strings.NewReader(`{"display_name":"No Session Here"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleUpdateMe(rec, req) // no user in the context

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestUpdateMe_RequiresJSONContentType_Returns415 pins the CSRF guard on the
// production route. requireJSONContentType is what stops a cross-site HTML form
// from driving this endpoint, and it is attached per-route here rather than
// inherited — the /api/auth group applies it to /password the same way.
//
// It needs a REAL SESSION to mean anything: an anonymous probe is refused by the
// auth middleware first and would return 401 whether or not the content-type
// gate is attached at all.
func TestUpdateMe_RequiresJSONContentType_Returns415(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)
	cookie := registerViaRouter(t, router, "csrfprobe")
	before := mustGetUser(t, q, "csrfprobe")

	req := httptest.NewRequest(http.MethodPatch, "/api/auth/me",
		strings.NewReader(`{"display_name":"Cross Site Rename"}`))
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a non-JSON PATCH got %d, want 415; body: %s", rec.Code, rec.Body.String())
	}
	if got := mustGetUser(t, q, "csrfprobe").DisplayName; got != before.DisplayName {
		t.Errorf("display_name = %q, want it unchanged at %q — a form-encodable request renamed the user",
			got, before.DisplayName)
	}
}

// TestUpdateMe_DeletedAccount_Returns404 exercises the RETURNING-based
// zero-rows arm: the account was removed between authenticating and the write.
// It is driven at the handler rather than through the router because sessions
// cascade on user delete, so the router would answer 401 and never reach the
// statement this test is about.
func TestUpdateMe_DeletedAccount_Returns404(t *testing.T) {
	h := setupHandler(t)
	ghost := database.User{ID: 999_999, Username: "ghost", DisplayName: "Ghost", Role: RoleMember}

	req := httptest.NewRequest(http.MethodPatch, "/api/auth/me",
		strings.NewReader(`{"display_name":"Renamed After Deletion"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleUpdateMe(rec, withUser(req, ghost))

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s — an UPDATE that matched no row reported success",
			rec.Code, rec.Body.String())
	}
}

// --- the cascade that must NOT run ---

// TestUpdateMe_DoesNotRunThePasswordCascade is the reason this endpoint is not
// built on any part of the credential paths. Changing a password deletes every
// session, revokes every API token and drops every push subscription
// (runPasswordResetCascade); handleUpdateUser separately wipes the target's
// sessions whenever the effective role differs from the stored one. A rename is
// not a credential change, so a member who fixes a typo in their own name must
// not be signed out of their phone, lose their homepage-widget token, or stop
// receiving household notifications.
//
// Every count is seeded at TWO so a partial revoke is distinguishable from none,
// and the rename is asserted to have landed first — otherwise this test would
// pass on a handler that did nothing at all.
func TestUpdateMe_DoesNotRunThePasswordCascade(t *testing.T) {
	h := setupHandler(t)
	user, cookie := seedProfileUser(t, h, "profile-cascade", "Cascade Ledger Label", RoleMember)

	// A second device's session, alongside the one seedProfileUser created.
	seedSessionForUser(t, h.queries, user.ID)
	seedLiveToken(t, h, user.ID, "homepage-widget")
	seedLiveToken(t, h, user.ID, "backup-script")
	for _, endpoint := range []string{
		"https://fcm.googleapis.com/fcm/send/profile-phone",
		"https://fcm.googleapis.com/fcm/send/profile-tablet",
	} {
		if err := h.queries.UpsertPushSubscription(context.Background(), database.UpsertPushSubscriptionParams{
			UserID:   user.ID,
			Endpoint: endpoint,
			P256dh:   "key",
			Auth:     "auth",
		}); err != nil {
			t.Fatalf("seed push subscription %s: %v", endpoint, err)
		}
	}

	if got := countSessions(t, h, user.ID); got != 2 {
		t.Fatalf("fixture: %d sessions, want 2", got)
	}
	if got := countLiveTokens(t, h, user.ID); got != 2 {
		t.Fatalf("fixture: %d live tokens, want 2", got)
	}
	subsBefore, err := h.queries.CountPushSubscriptionsByUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("count push subscriptions: %v", err)
	}
	if subsBefore != 2 {
		t.Fatalf("fixture: %d push subscriptions, want 2", subsBefore)
	}

	rec := patchMe(t, h, `{"display_name":"Renamed Without Consequences"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// The rename must have LANDED, or "nothing was revoked" is only saying that
	// a request which did nothing also destroyed nothing.
	stored := reloadUser(t, h, user.ID)
	if stored.DisplayName != "Renamed Without Consequences" {
		t.Fatalf("stored display_name = %q, want %q — the rename did not land, so every assertion below would prove nothing",
			stored.DisplayName, "Renamed Without Consequences")
	}

	if got := countSessions(t, h, user.ID); got != 2 {
		t.Errorf("sessions after a rename = %d, want 2 — a rename signed the user out of a device", got)
	}
	if got := countLiveTokens(t, h, user.ID); got != 2 {
		t.Errorf("live API tokens after a rename = %d, want 2 — a rename revoked a credential", got)
	}
	subsAfter, err := h.queries.CountPushSubscriptionsByUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("count push subscriptions: %v", err)
	}
	if subsAfter != 2 {
		t.Errorf("push subscriptions after a rename = %d, want 2 — a rename unsubscribed a device", subsAfter)
	}
	if stored.PasswordHash != user.PasswordHash {
		t.Error("password_hash changed during a rename")
	}

	// The caller's own cookie still authenticates. The counts above are storage;
	// this is the outcome the user experiences, and it is what a session wipe
	// welded to the wrong condition would break.
	again := patchMe(t, h, `{"display_name":"Still Signed In"}`, cookie)
	if again.Code != http.StatusOK {
		t.Errorf("the caller's session stopped working after their own rename: got %d, want 200; body: %s",
			again.Code, again.Body.String())
	}
}

// --- the admin route is still the only way to rename somebody else ---

// TestNewRouter_SelfRenameIsOpenToMembers_ButRenamingOthersStaysAdminOnly runs
// against the PRODUCTION router, so it pins the wiring as well as the policy:
// that PATCH /api/auth/me is registered and reachable by a member, and that
// PUT /api/users/{id} — the only other write path for display_name — still
// refuses one. The 403 is asserted alongside the target row, because a status
// code cannot tell a refused request from one that was refused after writing.
func TestNewRouter_SelfRenameIsOpenToMembers_ButRenamingOthersStaysAdminOnly(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

	// First registration is the admin.
	adminCookie := registerViaRouter(t, router, "routeradmin")
	admin := mustGetUser(t, q, "routeradmin")
	if admin.Role != RoleAdmin {
		t.Fatalf("first registered user has role %q, want %q", admin.Role, RoleAdmin)
	}
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   "registration_enabled",
		Value: "true",
	}); err != nil {
		t.Fatalf("open registration: %v", err)
	}
	memberCookie := registerViaRouter(t, router, "routermember")
	member := mustGetUser(t, q, "routermember")
	if member.Role != RoleMember {
		t.Fatalf("second registered user has role %q, want %q", member.Role, RoleMember)
	}

	send := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// A member renames THEMSELVES: allowed.
	rec := send(http.MethodPatch, "/api/auth/me", `{"display_name":"Member Chosen Label"}`, memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("a member renaming themselves got %d, want 200; body: %s — the route is not registered or not reachable by members",
			rec.Code, rec.Body.String())
	}
	if got := mustGetUser(t, q, "routermember").DisplayName; got != "Member Chosen Label" {
		t.Fatalf("member display_name = %q, want %q", got, "Member Chosen Label")
	}

	// The same member renames the ADMIN through the admin route: refused, and
	// the admin's row is untouched.
	rec = send(http.MethodPut, "/api/users/"+strconv.FormatInt(admin.ID, 10),
		`{"display_name":"Owned By A Member"}`, memberCookie)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a member renaming the admin got %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if got := mustGetUser(t, q, "routeradmin").DisplayName; got == "Owned By A Member" {
		t.Error("a member renamed the admin through PUT /api/users/{id} — the admin gate did not hold")
	}

	// And the admin route still works for an admin, so the 403 above is the
	// gate refusing a member rather than the route being broken for everyone.
	rec = send(http.MethodPut, "/api/users/"+strconv.FormatInt(member.ID, 10),
		`{"display_name":"Renamed By The Admin"}`, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("an admin renaming a member got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if got := mustGetUser(t, q, "routermember").DisplayName; got != "Renamed By The Admin" {
		t.Errorf("member display_name = %q, want %q", got, "Renamed By The Admin")
	}
}
