package database

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRunMigrations_CreatesSchemaTable(t *testing.T) {
	db := openTestDB(t)

	err := RunMigrations(db)
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// schema_migrations table must exist
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one migration to be recorded")
	}
}

func TestRunMigrations_AppliesInitialSchema(t *testing.T) {
	db := openTestDB(t)

	err := RunMigrations(db)
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Verify tables from 001_initial_schema.sql exist
	tables := []string{"users", "sessions", "categories", "currencies", "transactions", "budgets", "savings_goals", "saved_filters", "app_settings"}
	for _, table := range tables {
		var n int
		err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
		if err != nil {
			t.Errorf("table %s should exist: %v", table, err)
		}
	}
}

func TestRunMigrations_RecordsMigrationVersion(t *testing.T) {
	db := openTestDB(t)

	err := RunMigrations(db)
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var version string
	err = db.QueryRow("SELECT version FROM schema_migrations WHERE version = '001_initial_schema.sql'").Scan(&version)
	if err != nil {
		t.Fatalf("expected 001_initial_schema.sql in schema_migrations: %v", err)
	}
	if version != "001_initial_schema.sql" {
		t.Fatalf("expected version '001_initial_schema.sql', got %q", version)
	}
}

func TestRunMigrations_IsIdempotent(t *testing.T) {
	db := openTestDB(t)

	// Run twice
	if err := RunMigrations(db); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	// Should still have exactly one migration recorded
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 migration record, got %d", count)
	}
}

func TestRunMigrations_SeedsDefaultData(t *testing.T) {
	db := openTestDB(t)

	err := RunMigrations(db)
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Check seeded categories
	var catCount int
	err = db.QueryRow("SELECT COUNT(*) FROM categories").Scan(&catCount)
	if err != nil {
		t.Fatalf("query categories: %v", err)
	}
	if catCount != 19 {
		t.Fatalf("expected 19 seeded categories, got %d", catCount)
	}

	// Check seeded currencies
	var curCount int
	err = db.QueryRow("SELECT COUNT(*) FROM currencies").Scan(&curCount)
	if err != nil {
		t.Fatalf("query currencies: %v", err)
	}
	if curCount != 3 {
		t.Fatalf("expected 3 seeded currencies, got %d", curCount)
	}
}
