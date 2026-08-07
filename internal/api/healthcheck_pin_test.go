package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestDockerfileHealthcheckScrapesDataEndpoint pins the image's HEALTHCHECK
// probe against the route this package registers. Nothing executes the probe
// in any test — it only runs inside a built container — so like the version
// stamp (internal/version.TestDockerfileStampsTheLinkedVersionVar) it is a
// plain-text seam a refactor can break without anything going red.
//
// Three regressions this catches, each of which ships a green build:
//
//   - Repointing the probe at /api/health. That endpoint runs no query, so
//     the container reports healthy while the database is corrupt — the exact
//     bug B7 closed (measured 2026-08-07: same boot, corrupt DB, /api/health
//     200, /healthz/data 503).
//   - Restoring --spider as a "simplification". chi registers /healthz/data
//     for GET only and answers HEAD with 405, and --spider's documented
//     contract — check that the URL exists, download nothing — never promises
//     GET. GNU wget's implementation probes with HEAD; busybox's happens to
//     send GET today, so the probe would work by implementation accident
//     only, and a build that switched to HEAD would pin every container
//     permanently unhealthy.
//   - Tightening --interval below the endpoint's own floor. The route's
//     operational note (router.go) requires >=10s so the per-request
//     quick_check cannot become a hot path that starves WAL writers.
//
// The dynamic halves are covered elsewhere: TestNewRouter_HealthzData_
// Public_Returns200 proves the sessionless probe is not walled off behind
// auth, and the degraded 503 path has its own handler tests.
func TestDockerfileHealthcheckScrapesDataEndpoint(t *testing.T) {
	healthcheck := dockerfileHealthcheck(t)

	if !strings.Contains(healthcheck, "http://127.0.0.1:8080/healthz/data") {
		t.Errorf("HEALTHCHECK does not scrape /healthz/data — the container "+
			"would report healthy on a corrupt database. Got: %q", healthcheck)
	}
	if strings.Contains(healthcheck, "/api/health") {
		t.Errorf("HEALTHCHECK scrapes /api/health, which runs no query and "+
			"answers 200 on a corrupt database. Probe /healthz/data instead. "+
			"Got: %q", healthcheck)
	}
	if strings.Contains(healthcheck, "--spider") {
		t.Errorf("HEALTHCHECK uses wget --spider, whose documented contract "+
			"never promises a GET — GNU wget probes with HEAD, and "+
			"/healthz/data answers HEAD with 405, so the probe works only by "+
			"busybox's implementation accident of sending GET. Use an "+
			"explicit GET (-O /dev/null). Got: %q", healthcheck)
	}

	interval := regexp.MustCompile(`--interval=(\d+)s`).FindStringSubmatch(healthcheck)
	if interval == nil {
		t.Fatalf("HEALTHCHECK has no --interval=<n>s flag; without one Docker "+
			"polls every 30s by default, which is fine — but the pin below "+
			"cannot vouch for an interval it cannot read. Got: %q", healthcheck)
	}
	if secs, err := strconv.Atoi(interval[1]); err != nil || secs < 10 {
		t.Errorf("HEALTHCHECK interval %ss is below the 10s floor the "+
			"/healthz/data route comment requires — per-request quick_check "+
			"polled hotter than that starves WAL writers on large databases",
			interval[1])
	}
}

// dockerfileHealthcheck returns the Dockerfile's HEALTHCHECK instruction with
// comment lines removed and backslash continuations joined, so the assertions
// above match the live instruction and never a commented-out example (the
// same trap dockerfileInstructions in internal/version guards against).
func dockerfileHealthcheck(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	joined := strings.ReplaceAll(strings.Join(kept, "\n"), "\\\n", " ")

	for _, line := range strings.Split(joined, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "HEALTHCHECK") {
			return line
		}
	}
	t.Fatal("Dockerfile has no HEALTHCHECK instruction — the container would " +
		"report no health at all and every container-health monitor goes blind")
	return "" // unreachable
}
