package api

import (
	"net/http"

	"github.com/elienop/spendrop/internal/version"
)

// versionResponse is the payload returned by GET /api/version.
type versionResponse struct {
	// Version is the release tag the running binary was linked with,
	// carrying its leading "v" (e.g. "v0.35.0"), or "dev" for a build that
	// was not stamped. Never empty — the UI renders it verbatim.
	Version string `json:"version"`
}

// handleVersion serves GET /api/version so the UI can show which release
// it is talking to.
//
// Authenticated for consistency with the rest of /api — everything else
// under that prefix requires a session, and a lone public exception is a
// surprise for anyone reading the router. /api/health is the deliberate
// exception, and only because the Docker HEALTHCHECK scrapes it.
//
// This is NOT a fingerprinting control, and must not be described as one.
// The same release string is stamped into the frontend bundle at build
// time, and the SPA serves that bundle unauthenticated so the login page
// can render — so anyone who can reach the app reads the version from
// GET / followed by GET /assets/index-*.js, without ever touching this
// endpoint. Measured on a real image: both requests return 200 and the
// tag appears in the JS. Treat the version as public information and do
// not add complexity here in the belief that it is hidden.
//
// Nothing in the response is per-user — every household member sees the
// same string.
func (h *Handler) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{Version: version.Version()})
}
