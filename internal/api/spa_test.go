package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupSPADir creates a temp dir laid out like a Vite build: a top-level
// index.html, a content-hashed asset under /assets/, and a top-level
// static file that is neither of the above. Returns the dir path.
func setupSPADir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>SPA</body></html>"), 0644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "favicon.svg"), []byte("<svg/>"), 0644); err != nil {
		t.Fatalf("write favicon.svg: %v", err)
	}
	assetsDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "index-abc123.js"), []byte("console.log('hello');"), 0644); err != nil {
		t.Fatalf("write js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "style-def456.css"), []byte("body { color: red; }"), 0644); err != nil {
		t.Fatalf("write css: %v", err)
	}
	return dir
}

func serve(t *testing.T, handler http.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestSPAHandler_ServesExistingFile(t *testing.T) {
	dir := setupSPADir(t)
	handler := SPAHandler(dir)
	rec := serve(t, handler, "/favicon.svg")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "<svg/>" {
		t.Errorf("expected svg content, got %q", rec.Body.String())
	}
	// Top-level non-asset, non-shell files get no explicit Cache-Control;
	// http.FileServer handles them with Last-Modified + ETag.
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("expected no Cache-Control on favicon.svg, got %q", cc)
	}
}

func TestSPAHandler_FallsBackToIndexHTML(t *testing.T) {
	dir := setupSPADir(t)
	handler := SPAHandler(dir)
	rec := serve(t, handler, "/dashboard/overview")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "<html><body>SPA</body></html>" {
		t.Errorf("expected index.html content, got %q", body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != shellRevalidateCache {
		t.Errorf("expected Cache-Control %q on SPA fallback, got %q", shellRevalidateCache, cc)
	}
}

func TestSPAHandler_ServesNestedStaticFile(t *testing.T) {
	dir := setupSPADir(t)
	handler := SPAHandler(dir)
	rec := serve(t, handler, "/assets/style-def456.css")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "body { color: red; }" {
		t.Errorf("expected CSS content, got %q", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != assetImmutableCache {
		t.Errorf("expected Cache-Control %q on /assets/ file, got %q", assetImmutableCache, cc)
	}
}

func TestSPAHandler_RootPathServesShell(t *testing.T) {
	dir := setupSPADir(t)
	handler := SPAHandler(dir)
	rec := serve(t, handler, "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "<html><body>SPA</body></html>" {
		t.Errorf("expected index.html content, got %q", body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != shellRevalidateCache {
		t.Errorf("expected Cache-Control %q on /, got %q", shellRevalidateCache, cc)
	}
}

func TestSPAHandler_ExplicitIndexHTMLIsShell(t *testing.T) {
	// An explicit request for /index.html must be treated as the shell
	// (no-cache) even though the file exists on disk — otherwise
	// browsers could pin themselves to a stale shell.
	dir := setupSPADir(t)
	handler := SPAHandler(dir)
	rec := serve(t, handler, "/index.html")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != shellRevalidateCache {
		t.Errorf("expected Cache-Control %q on /index.html, got %q", shellRevalidateCache, cc)
	}
}

func TestSPAHandler_HashedBundleIsImmutable(t *testing.T) {
	dir := setupSPADir(t)
	handler := SPAHandler(dir)
	rec := serve(t, handler, "/assets/index-abc123.js")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != assetImmutableCache {
		t.Errorf("expected Cache-Control %q on hashed bundle, got %q", assetImmutableCache, cc)
	}
}

func TestSPAHandler_AssetsDirReturns404(t *testing.T) {
	// Requesting a subdirectory must NOT serve the shell at a non-root
	// URL and must NOT leak a directory listing. http.FileServer would
	// otherwise list /assets/ or 301 to the trailing slash.
	dir := setupSPADir(t)
	handler := SPAHandler(dir)

	for _, path := range []string{"/assets", "/assets/"} {
		rec := serve(t, handler, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: expected 404, got %d", path, rec.Code)
		}
	}
}

func TestSPAHandler_NestedIndexHTMLIsNotShell(t *testing.T) {
	// Regression guard for the old `path.Base(urlPath) == "index.html"`
	// classifier, which would have served /assets/index.html as the
	// top-level SPA shell with no-cache. After the fix, the exact
	// equality check on `/index.html` leaves this path off the shell
	// branch and lets http.FileServer handle it normally.
	//
	// (Vite never emits /assets/index.html in practice — this test
	// exercises the classifier boundary, not a realistic URL.)
	dir := setupSPADir(t)
	if err := os.WriteFile(filepath.Join(dir, "assets", "index.html"), []byte("<html>nested</html>"), 0644); err != nil {
		t.Fatalf("write nested index.html: %v", err)
	}

	handler := SPAHandler(dir)
	rec := serve(t, handler, "/assets/index.html")

	// Must NOT carry the shell Cache-Control header — that would be
	// the regression.
	if cc := rec.Header().Get("Cache-Control"); cc == shellRevalidateCache {
		t.Errorf("should not classify /assets/index.html as shell; got Cache-Control=%q", cc)
	}
	// Must NOT leak the top-level shell content at this URL.
	if body := rec.Body.String(); strings.Contains(body, "<body>SPA</body>") {
		t.Errorf("/assets/index.html leaked top-level shell body: %q", body)
	}
}
