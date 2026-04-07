package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSPAHandler_ServesExistingFile(t *testing.T) {
	dir := t.TempDir()
	// Create a JS file in the temp dir
	jsContent := []byte("console.log('hello');")
	if err := os.WriteFile(filepath.Join(dir, "app.js"), jsContent, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	// Create index.html
	indexContent := []byte("<html><body>SPA</body></html>")
	if err := os.WriteFile(filepath.Join(dir, "index.html"), indexContent, 0644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	handler := SPAHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != string(jsContent) {
		t.Errorf("expected JS content, got %q", rec.Body.String())
	}
}

func TestSPAHandler_FallsBackToIndexHTML(t *testing.T) {
	dir := t.TempDir()
	indexContent := []byte("<html><body>SPA</body></html>")
	if err := os.WriteFile(filepath.Join(dir, "index.html"), indexContent, 0644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	handler := SPAHandler(dir)
	// Request a path that doesn't exist as a file
	req := httptest.NewRequest(http.MethodGet, "/dashboard/overview", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// Should serve index.html content
	if body := rec.Body.String(); body != string(indexContent) {
		t.Errorf("expected index.html content, got %q", body)
	}
}

func TestSPAHandler_ServesNestedStaticFile(t *testing.T) {
	dir := t.TempDir()
	assetsDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	cssContent := []byte("body { color: red; }")
	if err := os.WriteFile(filepath.Join(assetsDir, "style.css"), cssContent, 0644); err != nil {
		t.Fatalf("write css: %v", err)
	}
	// Need index.html for fallback
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	handler := SPAHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/assets/style.css", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != string(cssContent) {
		t.Errorf("expected CSS content, got %q", rec.Body.String())
	}
}
