package api

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// checkpointStaleWindow is the age beyond which a checkpoint row is
// considered stale enough to re-verify during a /healthz/data scrape. 24h
// matches the cadence of the daily full integrity check, so the two
// background maintenance passes share a single freshness envelope. The
// same window is re-used by CountStaleMismatchCheckpoints as the 503-flip
// threshold: after a successful sweep every affected row has its
// last_verified_at bumped to now, so CountStaleMismatchCheckpoints returns
// non-zero only when the verifier itself failed on a still-mismatching
// row — which IS a real degradation signal worth paging on.
const checkpointStaleWindow = 24 * time.Hour

// healthzDataResponse is the payload returned by GET /healthz/data.
//
// The contract is pinned in docs/superpowers/plans/data-stewardship-plan.md
// Tier 2 → Phase 2.2. Field names are snake_case so a monitoring scraper
// written in any language can key off them without having to translate
// camelCase conventions from Go. Every field has a deterministic shape —
// no nullable strings, no `any`-typed payloads — because the response is
// consumed by dashboards and alert rules, not humans.
type healthzDataResponse struct {
	// Status is "ok" when every sub-check succeeded. Any failing
	// sub-check (quick_check != "ok", or the cached integrity result
	// is non-ok) flips the whole document to "degraded" and the HTTP
	// status to 503. Monitoring treats the HTTP code as the primary
	// signal and this string as the human-readable explanation.
	Status string `json:"status"`

	// SchemaVersion is the filename of the most recently applied
	// migration, taken from the `schema_migrations` tracking table. It
	// is useful for pinning an alert when the DB is running an older
	// schema than the binary expects, and for "which release is
	// deployed" dashboards. Empty string if migrations have never run
	// (fresh container).
	SchemaVersion string `json:"schema_version"`

	// TransactionsLive is the count of rows with deleted_at IS NULL.
	// Deliberately exposed at a public endpoint because it carries
	// zero PII (just a count) and is a useful liveness signal: a
	// household app where this number drops overnight has probably
	// been broken by a bad bulk delete.
	TransactionsLive int64 `json:"transactions_live"`

	// TransactionsDeleted is the count of soft-deleted rows still
	// present in the trash. Combined with TransactionsLive, an operator
	// can spot "trash is growing unboundedly" or "the live count
	// collapsed but the trash count spiked" within seconds of the
	// incident.
	TransactionsDeleted int64 `json:"transactions_deleted"`

	// LastWriteAt is MAX(updated_at) across every transaction,
	// including tombstoned rows — a proxy for "is the application
	// still writing?". The format is RFC 3339 UTC so shell one-liners
	// and Grafana alike can parse it. Zero value (omitted from the
	// JSON) means there are no transactions at all, which is the
	// legitimate fresh-install state.
	LastWriteAt *time.Time `json:"last_write_at,omitempty"`

	// QuickCheck is the verbatim result of `PRAGMA quick_check` run
	// synchronously for this request. At household scale the pragma
	// is sub-millisecond. A value of "ok" is the only healthy result;
	// anything else is rendered in full so the operator can paste it
	// into a bug report. Multi-line results are joined with \n, so
	// JSON consumers will see a single string with embedded newlines
	// rather than an array.
	QuickCheck string `json:"quick_check"`

	// LastIntegrityCheckAt is the instant the daily ticker (or the
	// startup check) finished the most recent full `PRAGMA
	// integrity_check`. If this field is missing or stale by more
	// than ~26h an operator should investigate — the ticker goroutine
	// may have panicked.
	LastIntegrityCheckAt *time.Time `json:"last_integrity_check_at,omitempty"`

	// LastIntegrityCheckResult is the verbatim last-known result of
	// the full integrity check. Same encoding rules as QuickCheck.
	LastIntegrityCheckResult string `json:"last_integrity_check_result"`

	// Checkpoints is the per-status count of balance checkpoints.
	// Always emitted (even as all-zero) so monitoring rules can alert
	// on "the checkpoint count dropped to zero overnight" without
	// special-casing an absent field. A non-zero Mismatch is a
	// user-visible data-quality signal, not a system-health signal —
	// the status flip to 503 only happens when the verifier itself
	// cannot make progress (CountStaleMismatchCheckpoints > 0 after
	// the re-verify sweep), not when a user has merely asserted a
	// number that no longer matches the live SUM.
	Checkpoints healthzCheckpointCounts `json:"checkpoints"`
}

// healthzCheckpointCounts is the nested shape inside healthzDataResponse
// for per-status counts. Matches the row returned by
// queries.CountCheckpointsByStatus field-for-field.
type healthzCheckpointCounts struct {
	OK       int64 `json:"ok"`
	Mismatch int64 `json:"mismatch"`
	Pending  int64 `json:"pending"`
}

// handleHealthzData serves GET /healthz/data — the DB-aware liveness
// endpoint consumed by monitoring scrapers on the Docker network. It is
// intentionally unauthenticated: every field is a count, a timestamp,
// or a version string, all of which are acceptable to expose on a
// self-hosted LAN deployment.
//
// Ordering of sub-checks is deliberate:
//
//  1. schema_version — near-free query, always reported even if later
//     steps fail so operators can tell which migration is applied.
//  2. transaction counts — one row via CountAllTransactions.
//  3. last_write_at — one row fetched via ORDER BY updated_at DESC
//     LIMIT 1. There is no index on updated_at (see the helper
//     comment on latestTransactionWriteAt), so this is a full table
//     scan — at household scale (≤10k rows) still sub-millisecond, but
//     do not promote this endpoint to a hot path without adding one.
//  4. quick_check — the synchronous per-request PRAGMA. Anything other
//     than "ok" flags the status as degraded.
//  5. cached full integrity result — read from the Handler struct under
//     its RWMutex; degraded if last result was non-ok.
//
// Failures in steps 1-3 set the status to degraded and return 503; we
// do not short-circuit because operators benefit from seeing the
// partial document ("quick_check is ok but count query failed" is a
// different incident from "the whole DB is gone").
//
// No logging on the happy path: this endpoint is hit on every scrape
// cycle (once a minute by Prometheus, up to once a second by
// uptime-kuma), and logging every hit would drown the real incidents.
// We do log unexpected errors so a one-off DB hiccup is still
// captured in the container logs.
func (h *Handler) handleHealthzData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resp := healthzDataResponse{Status: "ok"}
	degraded := false

	// schema_version: most recently applied migration. The helper maps
	// the "no rows" case to (empty, nil) so the empty-table fresh-install
	// state stays "ok" without the caller having to special-case
	// sql.ErrNoRows. Any other error flips the response to degraded so
	// operators notice a broken tracking table (e.g. someone ran the
	// server against a non-SpenDrop SQLite file by mistake).
	if sv, err := latestSchemaVersion(ctx, h.db); err == nil {
		resp.SchemaVersion = sv
	} else {
		degraded = true
	}

	// transaction counts: one SELECT, two scalars. Errors flip the
	// response to degraded but do not abort — the remaining sub-checks
	// are independent and still provide signal.
	if counts, err := h.queries.CountAllTransactions(ctx); err == nil {
		resp.TransactionsLive = counts.Live
		resp.TransactionsDeleted = counts.Deleted
	} else {
		degraded = true
	}

	// last_write_at: MAX(updated_at) as a nullable timestamp. Zero
	// rows → NULL → nil pointer, which the JSON encoder omits thanks
	// to the omitempty tag. An error flips degraded but still emits
	// nil, consistent with "we couldn't measure it".
	if ts, err := latestTransactionWriteAt(ctx, h.db); err == nil {
		if !ts.IsZero() {
			t := ts.UTC()
			resp.LastWriteAt = &t
		}
	} else {
		degraded = true
	}

	// quick_check: the cheap pragma, run per request. Non-"ok" result
	// is a hard degradation signal — this is the whole point of the
	// endpoint.
	if qc, err := database.QuickCheck(ctx, h.db); err == nil {
		resp.QuickCheck = qc
		if qc != database.IntegrityOK {
			degraded = true
		}
	} else {
		resp.QuickCheck = "error: " + err.Error()
		degraded = true
	}

	// Cached full integrity result populated by main.go (startup +
	// daily ticker). The zero time is the "never ran" state and does
	// not by itself flip the status; a non-empty result that is not
	// "ok" does.
	at, result := h.getIntegrityResult()
	if !at.IsZero() {
		t := at.UTC()
		resp.LastIntegrityCheckAt = &t
	}
	resp.LastIntegrityCheckResult = result
	if result != "" && result != database.IntegrityOK {
		degraded = true
	}

	// Phase 3.3 checkpoint freshness sweep. Re-verify every row whose
	// last_verified_at is older than the stale window (or NULL),
	// report the resulting per-status counts, and flip to 503 only
	// when a mismatch has evaded the verifier for longer than the
	// window. At household scale (tens of checkpoints) the entire
	// block is sub-millisecond. DO NOT promote /healthz/data to a
	// hot path on a deployment with thousands of checkpoints without
	// first adding an index on balance_checkpoints.last_verified_at
	// — ListStaleCheckpoints currently full-scans the table.
	//
	// h.clock.Now() (not time.Now()) is used so the fixture test
	// harness (Phase 3.2) can drive the sweep under a frozen clock
	// without flaking on wall-clock jitter.
	staleThreshold := h.clock.Now().UTC().Add(-checkpointStaleWindow).Format(time.RFC3339)
	if stale, err := h.queries.ListStaleCheckpoints(ctx, staleThreshold); err == nil {
		for _, cp := range stale {
			if _, _, verr := h.verifyCheckpointOnce(ctx, cp); verr != nil {
				// Log and continue: the sweep is best-effort, and
				// leaving this row's last_verified_at unchanged
				// means the next CountStaleMismatchCheckpoints call
				// will surface the failure as a 503 if the row is
				// in mismatch state.
				log.Printf("/healthz/data: reverify stale checkpoint id=%d: %v", cp.ID, verr)
			}
		}
	} else {
		log.Printf("/healthz/data: list stale checkpoints: %v", err)
		degraded = true
	}

	if counts, err := h.queries.CountCheckpointsByStatus(ctx); err == nil {
		resp.Checkpoints = healthzCheckpointCounts{
			OK:       counts.Ok,
			Mismatch: counts.Mismatch,
			Pending:  counts.Pending,
		}
	} else {
		log.Printf("/healthz/data: count checkpoints by status: %v", err)
		degraded = true
	}

	if staleMismatches, err := h.queries.CountStaleMismatchCheckpoints(ctx, staleThreshold); err == nil {
		if staleMismatches > 0 {
			degraded = true
		}
	} else {
		log.Printf("/healthz/data: count stale mismatches: %v", err)
		degraded = true
	}

	status := http.StatusOK
	if degraded {
		resp.Status = "degraded"
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

// latestSchemaVersion returns the most recently applied migration's
// version string from the `schema_migrations` tracking table. Schema
// versions in SpenDrop are filenames (e.g. `009_transaction_audit.sql`)
// and they sort lexicographically into apply order, so the query just
// ORDER BYs version DESC. Returns ("", nil) when the table exists but
// contains no rows — that is the legitimate fresh-install state and
// the caller should surface it as an empty string without flipping
// status to degraded. Mirroring the latestTransactionWriteAt contract
// keeps the health handler free of sql.ErrNoRows special-casing.
func latestSchemaVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx,
		`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`,
	).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// latestTransactionWriteAt returns the maximum updated_at across the
// entire transactions table (tombstoned rows included). Returns a
// zero time with no error when the table is empty — the caller
// encodes that as a missing field in the health payload.
//
// Implementation note: we use `ORDER BY updated_at DESC LIMIT 1`
// rather than the more obvious `MAX(updated_at)` so that the
// mattn/go-sqlite3 driver can parse the returned value into a
// time.Time. Aggregates like MAX() lose the underlying column's
// type affinity in SQLite and arrive at the driver as raw strings,
// which then fail to scan into sql.NullTime (the stdlib's
// convertAssign does not know how to turn a datetime string into a
// time.Time). Reading the column directly preserves the affinity
// and lets the driver's datetime parser do its job. The sql.ErrNoRows
// case is the legitimate empty-table signal and is mapped to a
// zero time with nil error so the caller does not need to distinguish
// "empty DB" from "query succeeded".
func latestTransactionWriteAt(ctx context.Context, db *sql.DB) (time.Time, error) {
	var ts time.Time
	err := db.QueryRowContext(ctx,
		`SELECT updated_at FROM transactions ORDER BY updated_at DESC LIMIT 1`,
	).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return ts, nil
}
