package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/version"
)

// TestHandleVersion covers both halves of the endpoint's contract: an
// authenticated caller gets the running release, and an anonymous one gets
// nothing.
//
// The authed case is the one that proves the route is wired. An anonymous
// request cannot: /api/version sits inside the authed chi group, whose
// middleware rejects with 401 before routing resolves, so a 401 would come
// back for a path that was never registered at all. Only the 200 with a
// populated `version` key distinguishes "wired" from "typo in the path".
func TestHandleVersion(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

	t.Run("without a session returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
		}
		// The rejection body must carry the error and nothing else — a
		// handler that wrote its payload before checking auth would still
		// 401 while serving the response. This is response hygiene, NOT a
		// secrecy control: the release string is readable from the
		// unauthenticated frontend bundle either way (see handleVersion).
		if strings.Contains(rec.Body.String(), version.Version()) {
			t.Errorf("401 body contains the version payload: %s", rec.Body.String())
		}
	})

	t.Run("with a session returns the running release", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		req.AddCookie(versionTestSession(t, router))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
		}

		// Decoded as a map, not into versionResponse: a typed decode
		// zero-fills a renamed or missing field and would pass against a
		// payload the frontend cannot read.
		var body map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		got, ok := body["version"]
		if !ok {
			t.Fatalf("response has no `version` key; got %v", body)
		}
		// Presence alone is not enough: a map decode still passes when the
		// handler grows extra fields, and this payload is documented as
		// public information — a future build_path/db_path addition must
		// fail here, not ship.
		if len(body) != 1 {
			t.Errorf("response carries %d keys, want exactly 1 (`version`); got %v", len(body), body)
		}
		if got != version.Version() {
			t.Errorf("version = %v, want %q", got, version.Version())
		}
		// The test binary is never link-stamped, so this is also the
		// assertion that an unstamped build reports "dev" all the way out
		// to the wire rather than an empty string.
		if got != version.Dev {
			t.Errorf("an unstamped test binary reported %v, want %q", got, version.Dev)
		}
	})
}

// TestHandleVersionReadsTheLinkedValue is a source-level pin, for the same
// reason the money-write guard is one: the regression it catches cannot be
// caught by a request-level test.
//
// A handler that returned the literal "dev" instead of version.Version()
// would satisfy every behavioural assertion above — the test binary is
// unstamped, so both produce "dev" — and would ship an image whose UI
// reports "dev" for every release while the Dockerfile stamp works
// perfectly. Asserting the call directly is what makes that mutation fail.
func TestHandleVersionReadsTheLinkedValue(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "version_handlers.go", nil, 0)
	if err != nil {
		t.Fatalf("parse version_handlers.go: %v", err)
	}

	var handler *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "handleVersion" {
			handler = fn
			break
		}
	}
	if handler == nil {
		t.Fatal("handleVersion not found in version_handlers.go")
	}

	var callsVersion bool
	ast.Inspect(handler.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Version" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "version" {
			callsVersion = true
		}
		return true
	})

	if !callsVersion {
		t.Error("handleVersion does not call version.Version() — a hardcoded string here " +
			`reports "dev" for every release while every other test still passes`)
	}
}

// versionTestSession registers a user and returns their session cookie.
// Registering (rather than inserting a session row directly) sidesteps the
// hashed-at-rest trap: sessions.token stores the SHA-256 of the cookie
// value, so a hand-seeded row has to be hashed to be accepted.
func versionTestSession(t *testing.T, router http.Handler) *http.Cookie {
	t.Helper()

	body := strings.NewReader(`{"username":"versionuser","password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d; body: %s", rec.Code, rec.Body.String())
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	t.Fatal("no session cookie from register")
	return nil
}
