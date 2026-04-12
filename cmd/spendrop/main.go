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
	"github.com/elienop/spendrop/internal/database"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "spendrop.db"
	}

	// Warn operators who have not chosen a cookie-security mode. Auto-detect is
	// safe but we want the decision to be deliberate: in production, set
	// COOKIE_SECURE=true and TRUST_PROXY=true behind a TLS terminator.
	if os.Getenv("COOKIE_SECURE") == "" && os.Getenv("SPENDROP_INSECURE") == "" {
		log.Println("NOTICE: COOKIE_SECURE not set — session cookies will auto-detect from the request scheme. " +
			"For production behind an HTTPS reverse proxy, set COOKIE_SECURE=true and TRUST_PROXY=true. " +
			"For plain-HTTP LAN deployments, set COOKIE_SECURE=false.")
	}

	// Open SQLite with WAL mode
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on", dbPath)
	sqlDB, err := sql.Open("sqlite3", dsn)
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

	// Clean expired sessions every hour, stopping on shutdown.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
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

	router := api.NewRouter(queries, sqlDB)

	addr := fmt.Sprintf(":%s", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}

	log.Println("Server stopped")
}
