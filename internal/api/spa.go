package api

import (
	"net/http"
	"os"
	"path/filepath"
)

// SPAHandler serves static files from distPath. If the requested file does not
// exist, it serves index.html so that the React SPA handles client-side routing.
func SPAHandler(distPath string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(distPath))
	return func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(distPath, filepath.Clean(r.URL.Path))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			http.ServeFile(w, r, filepath.Join(distPath, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	}
}
