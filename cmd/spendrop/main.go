package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/elienop/spendrop/internal/api"
	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/config"
	"github.com/elienop/spendrop/internal/database"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
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

	// Open SQLite with WAL mode via the DSN derived from config.
	sqlDB, err := sql.Open("sqlite3", cfg.SQLiteDSN())
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	// Run migrations
	if err := database.RunMigrations(sqlDB); err != nil {
		log.Fatalf("run migrations: %v", err)
	}
	log.Println("Database migrations applied successfully")

	queries := database.New(sqlDB)

	// Clean expired sessions on startup
	if err := queries.DeleteExpiredSessions(context.Background()); err != nil {
		log.Printf("startup session cleanup error: %v", err)
	}

	// Clean expired sessions at the configured cadence, stopping on shutdown.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
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

	router := api.NewRouter(queries, sqlDB, cfg)

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
