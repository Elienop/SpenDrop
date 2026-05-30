package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/elienop/spendrop/internal/api"
	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/backup"
	"github.com/elienop/spendrop/internal/config"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Dispatch CLI subcommands (e.g. `spendrop backup <path>`) before any
	// server-side initialization. If a subcommand handles os.Args[1],
	// dispatchSubcommand returns true and we exit with its requested code;
	// otherwise we fall through to the normal HTTP-server path below.
	//
	// LOAD-BEARING ORDER: this dispatch MUST run before any defer below
	// (sqlDB.Close, cleanupCancel, srv.Shutdown...) is registered. os.Exit
	// does NOT run main's deferred calls, so if a resource were acquired
	// first and a subcommand then called os.Exit, that resource would
	// leak. Keeping dispatch as the first statement after config.Load()
	// preserves the invariant; any new code that opens resources must go
	// *after* this block.
	if handled, code := dispatchSubcommand(context.Background(), cfg); handled {
		os.Exit(code)
	}

	// Push password/session tunables into the auth package before any
	// hashing or token generation happens.
	auth.Configure(cfg.Password.BcryptCost, cfg.Session.TokenBytes)

	// Warn operators who have not chosen a cookie-security mode. Auto-detect is
	// safe but we want the decision to be deliberate: in production, set
	// COOKIE_SECURE=true and TRUST_PROXY=true behind a TLS terminator.
	if os.Getenv("COOKIE_SECURE") == "" && os.Getenv("SPENDROP_INSECURE") == "" {
		log.Println("NOTICE: COOKIE_SECURE not set — session cookies will auto-detect from the request scheme. " +
			"For production behind an HTTPS reverse proxy, set COOKIE_SECURE=true and TRUST_PROXY=true. " +
			"For plain-HTTP LAN deployments, set COOKIE_SECURE=false.")
	}

	// Open SQLite with WAL mode via the DSN derived from config. openDB pins
	// the handle to a single connection (SetMaxOpenConns(1)) so every writer
	// — including the boot-time content_hash backfill, the migration runner,
	// and the session-cleanup / daily-integrity goroutines that share this
	// handle — serializes through one OS connection, matching the
	// "No concurrent writers" invariant.
	sqlDB, err := openDB(cfg)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer sqlDB.Close()

	// Run migrations. The snapshot directory is derived from the DB path
	// rather than exposed as its own env var: the data-stewardship spec
	// recommends hardcoding the location, and "next to the DB file" is
	// the only location that makes sense — anything else would need the
	// operator to pre-create a second mount and keep it writable. Docker's
	// DB_PATH=/app/data/spendrop.db therefore produces /app/data/migration-snapshots
	// without any extra wiring, and local dev produces ./migration-snapshots.
	//
	// We resolve the configured DB_PATH to absolute *before* joining the
	// sibling directory so the snapshot dir is a stable, operator-visible
	// location regardless of the process CWD. A systemd unit or
	// `docker exec` that launches from "/" with DB_PATH=spendrop.db would
	// otherwise compute filepath.Dir(".") → "" and try to write snapshots
	// to "/migration-snapshots", which is not writable for a non-root
	// service user and not visible to operators looking in the data
	// directory.
	absDBPath, err := filepath.Abs(cfg.DBPath)
	if err != nil {
		log.Fatalf("resolve DB_PATH to absolute: %v", err)
	}
	migrationSnapshotDir := filepath.Join(filepath.Dir(absDBPath), "migration-snapshots")
	if err := database.RunMigrations(sqlDB, database.MigrationOptions{
		DBPath:      absDBPath,
		SnapshotDir: migrationSnapshotDir,
		BusyTimeout: cfg.SQLite.BusyTimeout,
	}); err != nil {
		log.Fatalf("run migrations: %v", err)
	}
	log.Println("Database migrations applied successfully")

	// Phase 3.4 content_hash backfill. Idempotent and resumable: the
	// query filters `content_hash IS NULL` so once a row is hashed the
	// backfill skips it on every subsequent boot, and a crash mid-sweep
	// picks up where it left off. Placed outside RunMigrations because
	// the migration runner early-returns when no migrations are pending,
	// and the backfill needs to run on every boot until every legacy
	// row has a hash (which could span multiple boots if the first boot
	// crashes mid-sweep). Errors are fatal for the same reason migration
	// failures are: a container booting with some rows hashed and some
	// not will deduplicate inconsistently, and recovering is cheaper
	// than diagnosing a half-completed index.
	//
	// We pass context.Background() intentionally: BackfillContentHashes
	// sizes its own wall-clock deadline from the pre-counted pending row
	// count (see content_hash.go). Wrapping the call in an outer fixed
	// timeout here would re-introduce the boot-loop hazard the proportional
	// budget was written to avoid — a large legacy household DB that takes
	// six minutes to hash cannot be allowed to crash-loop under a naive
	// five-minute outer cap.
	if err := database.BackfillContentHashes(context.Background(), sqlDB); err != nil {
		log.Fatalf("backfill content_hash: %v", err)
	}

	// Full PRAGMA integrity_check runs synchronously at boot, immediately
	// after migrations and before the HTTP server binds a port. Any
	// non-"ok" result — or any query error — is fatal: a corrupt database
	// under a live server compounds the damage with every subsequent
	// write, so the correct operator response is "crash loudly, restore
	// from backup, investigate." We capture the result (not just the
	// pass/fail) so /healthz/data can surface the exact wording without
	// re-running the multi-second scan on every scrape.
	//
	// The five-minute timeout is a safety net for pathological DBs that
	// hang the pragma; at household scale a healthy check finishes in
	// milliseconds. Tune only if real operators ever hit it — no
	// configuration knob until then.
	startupIntegrityCtx, startupIntegrityCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	startupIntegrityResult, err := database.RunIntegrityCheckAtStartup(startupIntegrityCtx, sqlDB)
	startupIntegrityCancel()
	if err != nil {
		log.Fatalf("integrity check at startup: %v", err)
	}
	if startupIntegrityResult != database.IntegrityOK {
		// Log the full multi-line pragma output rather than truncating —
		// the operator's only clue to the nature of the corruption is
		// the exact SQLite error list, and cutting it off here to fit
		// one log line would leave the recovery guess-and-check.
		log.Fatalf("integrity check at startup returned non-ok result:\n%s", startupIntegrityResult)
	}
	startupIntegrityAt := time.Now().UTC()
	log.Println("Database integrity check passed")

	queries := database.New(sqlDB)

	// Clean expired sessions on startup
	if err := queries.DeleteExpiredSessions(context.Background()); err != nil {
		log.Printf("startup session cleanup error: %v", err)
	}

	// Clean expired sessions at the configured cadence, stopping on shutdown.
	//
	// The defer is a safety net: the graceful-shutdown path below calls
	// cleanupCancel() explicitly before srv.Shutdown so the background
	// goroutines stop before the DB handle closes, and this defer then
	// runs as a no-op on a cancelled context. The value of the defer is
	// that any future refactor which grows an error-returning exit path
	// between here and the quit-channel block below (or a panic caught
	// by a middleware-level recover) still releases the context's
	// resources instead of leaking them. log.Fatalf paths bypass defers
	// via os.Exit, so this is not a fix for fatal errors — it is the
	// standard go vet-compliant idiom for context.WithCancel hygiene.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	go func() {
		ticker := time.NewTicker(cfg.Session.CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := queries.DeleteExpiredSessions(cleanupCtx); err != nil {
					log.Printf("session cleanup error: %v", err)
				}
			case <-cleanupCtx.Done():
				return
			}
		}
	}()

	// Scheduled database backups. Shares cleanupCtx with session cleanup so
	// graceful shutdown stops both goroutines at the same time. Run itself
	// does not hold any DB handles that would block close; it opens its own
	// read-only connection per tick, independent of sqlDB.
	scheduler := &backup.Scheduler{
		Enabled:     cfg.Backup.Enabled,
		Dir:         cfg.Backup.Dir,
		Interval:    cfg.Backup.Interval,
		KeepDaily:   cfg.Backup.KeepDaily,
		KeepWeekly:  cfg.Backup.KeepWeekly,
		KeepMonthly: cfg.Backup.KeepMonthly,
		DBPath:      cfg.DBPath,
		BusyTimeout: cfg.SQLite.BusyTimeout,
	}
	go scheduler.RunLoop(cleanupCtx)

	router, h := api.NewRouterWithHandler(queries, sqlDB, cfg)

	// Seed the health endpoint's cached integrity result with the startup
	// check outcome so the first /healthz/data scrape immediately after
	// boot sees the just-ran value rather than the zero "never checked"
	// state. The timestamp was captured right after the synchronous
	// pragma returned above.
	h.SetIntegrityResult(startupIntegrityAt, startupIntegrityResult)

	// Wire Web Push. Constructed ONLY when cfg.Push.Enabled — when push is
	// off the Handler's pushSender stays nil and every push handler treats
	// that as "feature disabled" (public vapid route 404s, subscribe/test
	// report disabled). cfg.Validate has already guaranteed the VAPID keys
	// are present and well-formed whenever Enabled is true, so NewSender
	// cannot be handed half a keypair here. http.DefaultClient is shared
	// across the whole fan-out so the connection pool is reused.
	if cfg.Push.Enabled {
		h.SetPushSender(push.NewSender(
			cfg.Push.VAPIDPublicKey,
			cfg.Push.VAPIDPrivateKey,
			cfg.Push.VAPIDSubject,
			http.DefaultClient,
		))
		log.Println("Web Push enabled")
	}

	// Daily full PRAGMA integrity_check runs in the background alongside
	// session cleanup. This is the long-running scan that cross-checks
	// every index against its table — the per-request quick_check in
	// /healthz/data catches torn page writes but silently misses index
	// rot, so we schedule the expensive scan once per day to close that
	// gap. Results land in the Handler's cached pair via SetIntegrityResult
	// so the next /healthz/data scrape surfaces them.
	//
	// cleanupCtx is shared with the other background goroutines so
	// graceful shutdown aborts an in-flight check. The per-tick context
	// has a ten-minute timeout — at household scale the real scan is
	// sub-second, but a corrupt DB listing 100 errors can take noticeably
	// longer, and we do not want the ticker to leak goroutines on a
	// pathologically slow DB.
	//
	// We deliberately do NOT call SetIntegrityResult if the query itself
	// fails (e.g. context cancelled on shutdown): the cached value from
	// the last successful run is more useful to the operator than a
	// spurious "error: context deadline exceeded" string.
	go func() {
		const integrityInterval = 24 * time.Hour
		const integrityPerRunTimeout = 10 * time.Minute
		ticker := time.NewTicker(integrityInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				integrityCtx, integrityCancel := context.WithTimeout(cleanupCtx, integrityPerRunTimeout)
				result, err := database.IntegrityCheck(integrityCtx, sqlDB)
				integrityCancel()
				if err != nil {
					// Do not overwrite the cached result on error —
					// an operator inspecting /healthz/data on a
					// transient glitch is better served by the last
					// good value than by a context-cancelled string.
					log.Printf("daily integrity check error: %v", err)
					continue
				}
				if result != database.IntegrityOK {
					log.Printf("daily integrity check returned non-ok:\n%s", result)
				}
				h.SetIntegrityResult(time.Now().UTC(), result)
			case <-cleanupCtx.Done():
				return
			}
		}
	}()

	addr := fmt.Sprintf(":%s", cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("SpenDrop starting on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-quit
	log.Println("Shutting down server...")
	cleanupCancel() // stop background goroutines before closing DB

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}

	log.Println("Server stopped")
}
