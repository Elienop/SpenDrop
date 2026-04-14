package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Clock is a thin time source abstraction so every reports/dashboard handler
// can be driven by a fixed instant in tests without touching the global wall
// clock. Phase 3.2 of the data-stewardship plan introduces it because the
// reports surface is riddled with `time.Now().Year()` / end-of-month
// arithmetic that otherwise makes every test flaky on the month / year
// rollover boundary.
//
// Scope discipline: this is ONLY for handlers whose output materially depends
// on "what month is it right now". Wall-clock uses that genuinely want
// current time (session TTL, filename timestamps, row CreatedAt, audit row
// timestamps) must continue to call time.Now() directly - freezing those
// against a fixedClock would hide real bugs.
type Clock interface {
	Now() time.Time
}

// realClock is the production implementation; it delegates to time.Now().
// Every handler constructed through NewHandler gets this instance so nothing
// in prod changes behaviour.
type realClock struct{}

// Now returns the current wall-clock time. Defined on value receiver so
// `realClock{}` is cheap to construct at Handler-creation time without
// touching the heap.
func (realClock) Now() time.Time { return time.Now() }

// fixedClock returns the same instant on every call. Tests construct one via
// `NewHandlerWithClock(q, db, fixedClock{t: ...})` and get bit-for-bit
// reproducible report output regardless of what the wall clock says at run
// time.
type fixedClock struct{ t time.Time }

// Now returns the frozen instant. Value receiver to match realClock so both
// implementations satisfy the interface identically.
func (c fixedClock) Now() time.Time { return c.t }

// writeJSON encodes data as JSON and writes it to the response with the given
// status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("write json: %v", err)
	}
}

// writeError writes a JSON error response with the given status and message.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// decodeJSON decodes a JSON request body into v. The body is limited to
// MAX_JSON_BYTES (default 1 MiB, read from the runtime config) to prevent
// oversized payloads from consuming excessive memory.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, getMaxJSONBytes())
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// sanitizeLogValue replaces control characters and truncates to prevent log injection.
func sanitizeLogValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}
