package api

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// assetImmutableCache is the `Cache-Control` value applied to Vite's
// content-hashed `/assets/<name>-<hash>.<ext>` bundles. The hash
// changes on every content change, so once a filename is published
// its contents are immutable — browsers can cache it for a year and
// skip revalidation entirely.
const assetImmutableCache = "public, max-age=31536000, immutable"

// shellRevalidateCache is the `Cache-Control` value applied to the
// SPA shell (`index.html`, `/`, and any SPA route that falls back to
// it). `no-cache` does not mean "never cache" — it means "always
// revalidate with the server before using the cached copy". Browsers
// will still get a cheap 304 on unchanged responses, but they can no
// longer pin themselves to a stale shell that references an old
// content-hashed bundle.
const shellRevalidateCache = "no-cache"

// SPAHandler serves static files from distPath. If the requested file
// does not exist, it serves index.html so that the React SPA handles
// client-side routing.
//
// Cache-Control strategy:
//   - `/assets/*` (Vite content-hashed bundles) → immutable, 1 year.
//   - `index.html`, `/`, SPA fallbacks → no-cache (always revalidate).
//   - Everything else (e.g. /favicon.svg) → no explicit directive;
//     `http.FileServer` keeps its Last-Modified + ETag behavior.
//
// Without this, `http.FileServer` sends `Last-Modified` but no
// `Cache-Control` on `index.html`, and browsers apply heuristic
// caching — a freshly-deployed bundle hash never reaches the user
// until their cached shell expires.
func SPAHandler(distPath string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(distPath))
	indexPath := filepath.Join(distPath, "index.html")
	return func(w http.ResponseWriter, r *http.Request) {
		urlPath := r.URL.Path
		fsPath := filepath.Join(distPath, filepath.Clean(urlPath))

		stat, err := os.Stat(fsPath)

		// SPA shell is served when:
		//   - no file exists at the requested path (client-side route),
		//   - the root path `/` is requested (resolves to dist dir),
		//   - or `/index.html` is explicitly requested (exact match).
		// All three get `no-cache` so cached shells cannot pin users to
		// stale content-hashed bundle references.
		//
		// NOTE: directory checks are restricted to urlPath=="/" so a
		// subdirectory like `/assets/` cannot serve the shell at an
		// asset URL, and the index-html check uses exact equality
		// (not path.Base) so a hypothetical `/assets/index.html` stays
		// classified as an asset.
		isShell := os.IsNotExist(err) ||
			(err == nil && stat.IsDir() && urlPath == "/") ||
			urlPath == "/index.html"
		if isShell {
			serveShell(w, r, indexPath)
			return
		}
		// os.Stat errored for a reason other than "not exist" — e.g.
		// permission denied or an I/O failure on the dist volume.
		// These are operator problems; log server-side and return 500.
		if err != nil {
			log.Printf("spa: stat %q: %v", fsPath, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Non-root directories must 404 rather than fall through to
		// http.FileServer, which would otherwise emit a directory
		// listing (information disclosure on `/assets/`) or a 301 to
		// the trailing-slash variant.
		if stat.IsDir() {
			http.NotFound(w, r)
			return
		}

		// Content-hashed assets can be cached forever.
		if strings.HasPrefix(urlPath, "/assets/") {
			w.Header().Set("Cache-Control", assetImmutableCache)
		}

		fs.ServeHTTP(w, r)
	}
}

// serveShell writes index.html with no-cache headers. It uses
// `http.ServeContent` rather than `http.ServeFile` because ServeFile
// auto-redirects any URL ending in `/index.html` to `.../` — a
// canonicalization we don't want here, since the SPA shell is the
// content we want to serve regardless of which URL the client asked
// for. ServeContent writes inline with no URL rewriting and still
// honors conditional GETs (Last-Modified, ETag, 304).
//
// Caveat: if ServeContent rejects the request with a 4xx error (e.g.
// a malformed Range header → 416), Go 1.23+ strips our Cache-Control
// header from the error response. That's acceptable here: the 4xx
// body is tiny and nothing downstream references a hashed bundle.
func serveShell(w http.ResponseWriter, r *http.Request, indexPath string) {
	f, err := os.Open(indexPath)
	if err != nil {
		log.Printf("spa: open %q: %v", indexPath, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		log.Printf("spa: stat %q: %v", indexPath, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", shellRevalidateCache)
	http.ServeContent(w, r, "index.html", stat.ModTime(), f)
}
