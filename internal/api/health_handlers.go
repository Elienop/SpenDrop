package api

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/elienop/spendrop/internal/backup"
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
	//
	// DELIBERATELY UNAUTHENTICATED, reviewed 2026-08-01 and kept. A
	// security pass flagged it as a deployment fingerprint, which it is —
	// the question is whether hiding it would remove one, and it would
	// not. Three facts, each verified rather than assumed:
	//
	//  1. A strictly SHARPER fingerprint is already public at the same
	//     network position. The release tag is stamped into the frontend
	//     bundle, which is served unauthenticated so the login page can
	//     render; README documents it as public information. Anyone who
	//     can reach this endpoint can already read the exact release,
	//     which pins the build far more precisely than "the newest
	//     migration is 017_transactions_idempotency_key.sql".
	//  2. The migration FILENAMES are not secret either. migrate.go
	//     embeds migrations/*.sql with go:embed, so every name ships
	//     verbatim inside the binary, and the published container image
	//     is anonymously pullable. Gating the endpoint would not make the
	//     string unknowable; it would only make it unknowable to a
	//     monitoring scraper.
	//  3. Gating it would cost real signal. The value's whole job is to
	//     let an operator alert on "the DB is on an older schema than the
	//     binary expects" — a check that has to run from outside the app,
	//     from a scraper that has no session. Since B7 the Docker
	//     HEALTHCHECK is itself one of those sessionless callers: it
	//     scrapes /healthz/data every 30s (see the Dockerfile), so
	//     authenticating this endpoint would not merely cost external
	//     monitoring its signal — it would peg every container
	//     permanently unhealthy.
	//
	// So the honest reason to leave it open is that authenticating it
	// would be secrecy theatre — the same false rationale the version
	// stamp work already had to remove from four places once. If this app
	// ever ships a multi-tenant build, or the image stops being public,
	// revisit fact 2 and this whole calculus changes.
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

	// Backups reports the scheduled-backup subsystem. A POINTER with
	// omitempty, unlike Checkpoints: an absent block means this binary
	// never installed a status source, which is a truthful "we do not
	// know" rather than the lie that `enabled: false` would tell. main.go
	// wires the source unconditionally — including when backups are
	// switched off, in which case the block is present with
	// enabled=false — so a production scrape always sees it and an alert
	// rule can safely key off the nested fields.
	Backups *healthzBackupStatus `json:"backups,omitempty"`
}

// healthzBackupStatus is the nested shape inside healthzDataResponse for the
// scheduled-backup subsystem. Derived from backup.Status; see backupHealth for
// the mapping and for which of these signals flip the endpoint to 503.
//
// Every field here is served UNAUTHENTICATED, so the block carries only
// counts, timestamps, a duration and a fixed enum. Deliberately absent:
// BACKUP_DIR, DB_PATH, any filename, and the verify error text — the last
// because it embeds the backup's path and its byte size.
type healthzBackupStatus struct {
	// Enabled mirrors BACKUP_ENABLED. When false, none of the fields below
	// carry a health signal: the operator turned the subsystem off and an
	// empty backup directory is the expected result, not a fault.
	Enabled bool `json:"enabled"`

	// LastRunAt is when the most recent tick finished, successful or not.
	// Absent means no tick has completed — the state during the first
	// seconds after boot, and permanently when backups are disabled.
	LastRunAt *time.Time `json:"last_run_at,omitempty"`

	// LastOutcome is "success", "verify_failed", "error", or "" for
	// never-ran. verify_failed and error are kept apart because they call
	// for different responses: a snapshot being written and rejected is a
	// different incident from no snapshot being written at all.
	LastOutcome string `json:"last_outcome"`

	// TrustedCount is the number of restore points on disk: backups whose
	// filename parses AND which have a .sha256 sidecar. Zero while enabled
	// is the loudest state this subsystem has and flips the endpoint to 503.
	//
	// A POINTER, and ABSENT until the process has successfully read
	// BACKUP_DIR at least once. "Never observed" and "observed and empty"
	// used to share the encoding zero, so a directory the process could not
	// read reported the same thing as an empty one — a 503 saying there is
	// nothing to restore, beside last_outcome: "success", over a volume full
	// of backups. An unknown must not be spelled the same way as a fact that
	// raises an alarm. See DirUnreadable for what is emitted instead.
	TrustedCount *int `json:"trusted_count,omitempty"`

	// QuarantinedCount is the number of surviving *.corrupt files.
	// Informational — see backupHealth. Absent under the same rule as
	// TrustedCount: it comes from the same directory scan.
	QuarantinedCount *int `json:"quarantined_count,omitempty"`

	// PruneFailedCount is the number of files the last retention sweep
	// could not remove. Non-zero means retention is not being enforced and
	// the volume will grow. Informational — see backupHealth. Absent under
	// the same rule as TrustedCount: the sweep that would have produced it
	// is the call that failed.
	PruneFailedCount *int `json:"prune_failed_count,omitempty"`

	// DirUnreadable reports that the most recent tick could not read
	// BACKUP_DIR. Emitted only when true, so its absence means "the
	// directory was read".
	//
	// It is the signal that distinguishes "there is no restore point" from
	// "I cannot see whether there is a restore point", which are the same
	// alarm to a scraper watching only the HTTP code but completely
	// different repairs. It is also a fault in its own right: Prune walks
	// the directory with the same call, so retention is not running and the
	// quarantine is unbounded on a volume shared with the live database.
	DirUnreadable bool `json:"dir_unreadable,omitempty"`

	// NewestBackupAt is the timestamp of the newest trusted backup, taken
	// from its filename rather than its mtime so it survives an NTP step.
	// Absent when there is no trusted backup at all.
	NewestBackupAt *time.Time `json:"newest_backup_at,omitempty"`

	// NewestBackupAgeSeconds is that timestamp's age measured against the
	// SERVER's clock. It duplicates information in NewestBackupAt on
	// purpose: an alert rule that subtracts the timestamp from the
	// scraper's own clock silently mismeasures whenever the two hosts
	// disagree, and "the backup is three days stale" is exactly the alert
	// nobody wants firing spuriously.
	//
	// A POINTER, not a plain int64 with omitempty: an age of zero is a
	// backup taken this second, which must not be encoded the same way as
	// no backup at all.
	NewestBackupAgeSeconds *int64 `json:"newest_backup_age_seconds,omitempty"`

	// IntervalSeconds is the configured BACKUP_INTERVAL. Emitted so a
	// consumer can express staleness as a multiple of the cadence the
	// operator actually chose; the endpoint deliberately does not pick a
	// staleness threshold of its own (see backupHealth).
	IntervalSeconds int64 `json:"interval_seconds"`
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

	// Scheduled-backup subsystem. Read from the scheduler's own in-memory
	// snapshot, which it refreshes once per tick — the handler does NOT
	// stat the backup directory, so this block costs a mutex acquisition
	// and stays safe to poll. Nothing here re-derives retention; the
	// counts come from the same directory walk previousBackupSize uses
	// and from Prune's own return values.
	if h.backupStatus != nil {
		block, backupDegraded := backupHealth(h.backupStatus(), h.clock.Now())
		resp.Backups = &block
		if backupDegraded {
			degraded = true
		}
	}

	status := http.StatusOK
	if degraded {
		resp.Status = "degraded"
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

// backupHealth maps a backup.Status onto the wire block and decides whether it
// should degrade the endpoint. Pure, so the unhealthy/informational split is
// testable without a filesystem.
//
// UNHEALTHY (503):
//
//   - No trusted backup on disk while backups are enabled. A restore is
//     impossible right now. This is the single loudest thing the subsystem
//     can say and it is reachable in practice: BACKUP_KEEP_CORRUPT=0 plus a
//     sustained verify failure sweeps the directory empty.
//   - The last tick did not end in success. Either the snapshot is failing
//     (nothing new is being captured) or it is being written and rejected
//     (what is being captured is not trustworthy). Both mean the newest
//     restore point is frozen at whatever survives, and both are actionable
//     immediately.
//
// INFORMATIONAL (reported, does not degrade):
//
//   - Quarantined *.corrupt files. They are forensic evidence and they
//     OUTLIVE the failure that produced them by design — KeepCorrupt retains
//     the newest samples deliberately — so degrading on them would hold the
//     endpoint at 503 long after backups recovered, which trains an operator
//     to ignore it. The failure itself already surfaces via LastOutcome.
//   - Prune removal failures. Retention not being enforced means the volume
//     grows without bound, which is a real problem, but every existing backup
//     is intact and a restore still works. It is a capacity warning, not a
//     data-protection outage.
//   - An unreadable BACKUP_DIR that FOLLOWS a good scan. The counts from that
//     scan are still the best description of the volume, and BACKUP_INTERVAL
//     defaults to 24h — degrading on one transient EIO would pin the endpoint
//     at 503 for a day over a directory whose backups are all still present.
//     dir_unreadable reports it, and a persistent fault keeps surfacing
//     through newest_backup_age_seconds as the last good backup ages out.
//   - A stale-but-present backup. There is no correct universal threshold:
//     BACKUP_INTERVAL is operator-configured and the README recommends tuning
//     it, so any constant baked in here is wrong for someone in both
//     directions. The block emits the age and the configured interval and
//     lets the alert rule choose the multiple.
//
// NEVER DEGRADES: a disabled scheduler (the operator opted out, and
// BACKUP_ENABLED=false is documented as a supported kill-switch), a scheduler
// that has not completed its first tick (the HTTP server binds before the
// startup fire finishes, so degrading here would flap 503 on every container
// restart), and an unobserved directory with no read failure behind it — an
// unknown is not a zero, and alarming on one is how an endpoint teaches its
// operator to ignore it.
func backupHealth(st backup.Status, now time.Time) (healthzBackupStatus, bool) {
	block := healthzBackupStatus{
		Enabled:         st.Enabled,
		LastOutcome:     string(st.LastOutcome),
		DirUnreadable:   st.DirUnreadable,
		IntervalSeconds: int64(st.Interval / time.Second),
	}
	// The three directory counts are emitted ONLY once a scan has actually
	// happened. Copied into locals because they are addressed, and taking the
	// address of a struct field of the by-value st would tie the payload's
	// lifetime to the snapshot copy.
	if st.DirObserved {
		trusted, quarantined, pruneFailed := st.TrustedCount, st.QuarantinedCount, st.PruneFailedCount
		block.TrustedCount = &trusted
		block.QuarantinedCount = &quarantined
		block.PruneFailedCount = &pruneFailed
	}
	if !st.LastRunAt.IsZero() {
		t := st.LastRunAt.UTC()
		block.LastRunAt = &t
	}
	if !st.NewestBackupAt.IsZero() {
		t := st.NewestBackupAt.UTC()
		block.NewestBackupAt = &t
		// Clamped at zero. A negative age can only come from a clock moving
		// backwards, and most alert expressions ("age > N") would read it as
		// healthy anyway while a human reads it as nonsense; the timestamp
		// beside it preserves the raw fact for anyone diagnosing skew.
		age := int64(now.Sub(st.NewestBackupAt) / time.Second)
		if age < 0 {
			age = 0
		}
		block.NewestBackupAgeSeconds = &age
	}

	if !st.Enabled || st.LastRunAt.IsZero() {
		return block, false
	}

	degraded := st.LastOutcome != backup.OutcomeSuccess
	switch {
	case st.DirObserved:
		// The count describes something we actually looked at, so an empty
		// directory is a fact: a restore right now is impossible. A read
		// failure on TOP of a good scan only reports (see INFORMATIONAL).
		degraded = degraded || st.TrustedCount == 0
	case st.DirUnreadable:
		// Nothing has ever been read, and we know why. This is not a
		// zero-count alarm — it is "retention is not running and no one can
		// confirm a restore is possible", which is a permission or mount
		// fault that does not self-heal. Reporting it at 200 would leave the
		// documented "alert on the HTTP code" contract silent on a subsystem
		// that has stopped working, which is the failure Status exists to
		// prevent. dir_unreadable is what tells the operator which repair to
		// reach for.
		degraded = true
	}
	// Remaining case — never observed, no read failure — stays on
	// LastOutcome alone. An unknown of unknown cause is not evidence.
	return block, degraded
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
