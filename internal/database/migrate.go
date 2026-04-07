package database

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func RunMigrations(db *sql.DB) error {
	// Create migrations tracking table
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// Read all migration files
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	// Sort by filename
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Check if already applied
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", entry.Name()).Scan(&count)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", entry.Name(), err)
		}
		if count > 0 {
			continue
		}

		// Read and execute
		content, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		log.Printf("Applying migration: %s", entry.Name())
		_, err = db.Exec(string(content))
		if err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}

		// Record migration
		_, err = db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", entry.Name())
		if err != nil {
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
	}

	return nil
}
