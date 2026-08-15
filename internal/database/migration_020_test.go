package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Migration 020 makes users.role well-formed at the storage layer with two
// BEFORE triggers, because the alternative — a CHECK constraint, which SQLite
// can only add by rebuilding the table — cannot be applied to `users` without
// cascade-deleting the ledger (the migration's header carries the
// measurement). These tests are the only guard that mechanism has.
//
// Every write below is RAW SQL issued straight at the database. That is the
// point: the handler whitelist in internal/api/user_handlers.go is a different
// layer with its own tests, and what B20 asks is whether the column defends
// itself when something that is not that handler writes to it.

const migration020 = "020_users_role_integrity.sql"

// migrate020Fixture stands a database up at the state a real one is in the
// moment before 020 runs: every migration through 019 applied, foreign keys
// ON (production's DSN — Config.SQLiteDSN emits _foreign_keys=on), and the
// pool pinned to one connection so the pragma applies to every query the test
// issues rather than to whichever connection happened to run it.
func migrate020Fixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, dbPath := openTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d (err %v), want 1 to match production", fk, err)
	}
	applyMigrationsThrough(t, db, "019_signed_amounts_booked_rate.sql")
	return db, dbPath
}

// run020ViaRunner records 001..019 as applied and then calls the real
// RunMigrations, so 020 goes through the same per-file transaction and
// pre-flight snapshot production uses. applyMigrationsThrough bypasses the
// runner and writes no tracking rows, hence the backfill of schema_migrations
// first. Copied in shape from migration_019_test.go.
func run020ViaRunner(t *testing.T, db *sql.DB, dbPath string) {
	t.Helper()
	mustExec(t, db, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	for _, name := range embeddedMigrationNames(t) {
		if name < migration020 {
			mustExec(t, db, `INSERT INTO schema_migrations (version) VALUES (?)`, name)
		}
	}
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("apply migration 020 via runner: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, migration020).Scan(&applied); err != nil {
		t.Fatalf("check 020 recorded: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations holds %d rows for %s, want 1", applied, migration020)
	}
}

// TestMigration020_RejectsOutOfSetRole_RawSQL is the core guard: no statement,
// from any layer, can leave a role outside {'admin','member'} in the column.
//
// The rejected cases include the two conflict-resolution clauses that look
// like they might swallow the abort (OR REPLACE, OR IGNORE) and a
// case-variant, because 'Admin' passes no comparison in the codebase —
// internal/auth/middleware.go asks `user.Role != "admin"`, byte for byte.
//
// The ACCEPTED cases are not decoration. Without them a trigger that rejected
// everything — `WHEN 1`, or a typo in the value list — would pass every
// rejection assertion above and lock the household out of creating users at
// all.
func TestMigration020_RejectsOutOfSetRole_RawSQL(t *testing.T) {
	db, dbPath := migrate020Fixture(t)
	ctx := context.Background()

	// Seed BEFORE 020 so the fixture cannot be blamed on the guard, and so the
	// UPDATE cases below have a legitimate row to aim at.
	mustExec(t, db, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('elie', '$2a$10$fake', 'Elie', 'admin')`)
	mustExec(t, db, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('wife', '$2a$10$fake', 'Wife', 'member')`)

	run020ViaRunner(t, db, dbPath)

	const wantMsg = "users.role must be 'admin' or 'member'"

	rejected := []struct {
		name string
		stmt string
	}{
		{
			// The exact B20 shape: a mutated handler writes the empty string.
			name: "insert empty role",
			stmt: `INSERT INTO users (username, password_hash, display_name, role)
			       VALUES ('mutant', 'h', 'Mutant', '')`,
		},
		{
			name: "insert unknown role",
			stmt: `INSERT INTO users (username, password_hash, display_name, role)
			       VALUES ('super', 'h', 'Super', 'superadmin')`,
		},
		{
			name: "insert case-variant admin",
			stmt: `INSERT INTO users (username, password_hash, display_name, role)
			       VALUES ('shouty', 'h', 'Shouty', 'Admin')`,
		},
		{
			name: "insert or replace unknown role",
			stmt: `INSERT OR REPLACE INTO users (id, username, password_hash, display_name, role)
			       VALUES (1, 'elie', 'h', 'Elie', 'owner')`,
		},
		{
			name: "update to empty role",
			stmt: `UPDATE users SET role = '' WHERE username = 'wife'`,
		},
		{
			name: "update to unknown role",
			stmt: `UPDATE users SET role = 'root' WHERE username = 'wife'`,
		},
		{
			// OR IGNORE downgrades constraint violations to skipped rows; it
			// does NOT swallow a RAISE(ABORT), so the statement still fails.
			name: "update or ignore to unknown role",
			stmt: `UPDATE OR IGNORE users SET role = 'root' WHERE username = 'wife'`,
		},
		{
			// The whole-table write a repair script reaches for.
			name: "update every row to an unknown role",
			stmt: `UPDATE users SET role = 'household'`,
		},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.ExecContext(ctx, tc.stmt)
			if err == nil {
				t.Fatalf("statement was ACCEPTED; users.role must reject a value outside {'admin','member'} at the DB layer")
			}
			if !strings.Contains(err.Error(), wantMsg) {
				t.Fatalf("rejected with %q, want a message containing %q", err.Error(), wantMsg)
			}
		})
	}

	accepted := []struct {
		name string
		stmt string
	}{
		{"insert admin", `INSERT INTO users (username, password_hash, display_name, role)
		                  VALUES ('newadmin', 'h', 'New Admin', 'admin')`},
		{"insert member", `INSERT INTO users (username, password_hash, display_name, role)
		                   VALUES ('newmember', 'h', 'New Member', 'member')`},
		{"insert with role omitted (column DEFAULT 'member')",
			`INSERT INTO users (username, password_hash, display_name) VALUES ('defaulted', 'h', 'Defaulted')`},
		{"promote a member", `UPDATE users SET role = 'admin' WHERE username = 'wife'`},
		{"demote them back", `UPDATE users SET role = 'member' WHERE username = 'wife'`},
		{"rewrite the same role", `UPDATE users SET role = 'admin' WHERE username = 'elie'`},
		{"update a column that is not role", `UPDATE users SET display_name = 'Elie A' WHERE username = 'elie'`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, tc.stmt); err != nil {
				t.Fatalf("legitimate statement was REJECTED: %v", err)
			}
		})
	}

	// Nothing above left a malformed value behind, including via a rejected
	// statement that partially applied.
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE role NOT IN ('admin','member')`); n != 0 {
		t.Errorf("%d rows hold an out-of-set role after the probes; the column is not well-formed", n)
	}

	// A NULL role is refused too, by the column's own NOT NULL rather than by
	// the trigger — `NEW.role NOT IN (…)` is NULL for a NULL role, so the WHEN
	// clause does not fire. Pinned here because the migration header claims it.
	_, err := db.ExecContext(ctx, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('nullrole', 'h', 'Null Role', NULL)`)
	if err == nil {
		t.Fatal("a NULL role was accepted")
	}
	if !strings.Contains(err.Error(), "NOT NULL constraint failed: users.role") {
		t.Errorf("NULL role rejected with %q, want the column's NOT NULL constraint", err.Error())
	}
}

// userEdgeCanary describes ONE inbound foreign key into `users`, the row this
// suite seeds on it, and the probe that must still return 1 after migration
// 020 has run.
//
// It is a single list on purpose: the fixture, the pre-migration vacuity
// check, the post-migration assertions and the completeness check below all
// read from it, so adding an edge means adding one entry rather than
// remembering four places. Order is significant — the seeds run top to bottom
// and transaction_audit's row refers to the transaction seeded above it.
type userEdgeCanary struct {
	// table and column identify the edge, and are what the completeness
	// check matches against the live schema.
	table    string
	column   string
	onDelete string
	// why states what a household actually loses if this edge is cascaded,
	// so a failure reads as a consequence rather than as a number.
	why string
	// seed inserts exactly one row on this edge.
	seed string
	// probe must return 1 both before and after the migration. For a CASCADE
	// edge that is the row count. For SET NULL it must interrogate the COLUMN,
	// because the row survives and only the actor is blanked — a row count is
	// blind to exactly the damage that edge does.
	probe string
}

// userEdgeCanaries must name EVERY inbound edge into users.
// TestMigration020_EveryInboundUserEdgeHasACanary is what enforces that
// against the live schema, so this list cannot quietly fall behind a new
// migration that adds a table referencing users.
var userEdgeCanaries = []userEdgeCanary{
	{
		table: "sessions", column: "user_id", onDelete: "CASCADE",
		why: "every household member is logged out",
		seed: `INSERT INTO sessions (token, user_id, expires_at)
		       VALUES ('hash-of-a-cookie', 2, '2030-01-01T00:00:00Z')`,
		probe: `SELECT COUNT(*) FROM sessions`,
	},
	{
		// The ledger row itself — the thing the whole design decision is
		// about. Migration 001 seeds the default categories, so category_id 1
		// resolves under the foreign keys this fixture turns ON.
		table: "transactions", column: "user_id", onDelete: "CASCADE",
		why: "a users rebuild hard-deletes the ledger, with no tombstone, no Trash entry and no restore path",
		seed: `INSERT INTO transactions (id, user_id, date, description, category_id, amount_cents)
		       VALUES (1, 2, '2026-08-01', 'groceries', 1, 4200)`,
		probe: `SELECT COUNT(*) FROM transactions`,
	},
	{
		// SET NULL, not CASCADE: a rebuild KEEPS this row and blanks its
		// actor. Hence the probe on the column. Seeded after transactions
		// because it names that row.
		table: "transaction_audit", column: "actor_user_id", onDelete: "SET NULL",
		why: "the audit trail survives but forgets who did it",
		seed: `INSERT INTO transaction_audit (transaction_id, action, actor_user_id)
		       VALUES (1, 'insert', 2)`,
		probe: `SELECT COUNT(*) FROM transaction_audit WHERE actor_user_id IS NOT NULL`,
	},
	{
		// api_tokens carries two CHECKs that a lazy fixture trips silently —
		// token_hash must be exactly 64 chars and token_prefix exactly 15 —
		// and a rejected insert would leave this canary asserting 0 == 0
		// forever. The pre-migration probe below is what catches that.
		table: "api_tokens", column: "user_id", onDelete: "CASCADE",
		why: "every integration's token is destroyed and cannot be reissued to the same value",
		seed: `INSERT INTO api_tokens (user_id, name, token_hash, token_prefix)
		       VALUES (1, 'cli', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'spdr_abcdefghij')`,
		probe: `SELECT COUNT(*) FROM api_tokens`,
	},
	{
		table: "balance_checkpoints", column: "user_id", onDelete: "CASCADE",
		why: "hand-entered bank-statement anchors have no restore path",
		seed: `INSERT INTO balance_checkpoints (user_id, scope_type, date, expected_amount_cents)
		       VALUES (1, 'total', '2026-08-01', 500000)`,
		probe: `SELECT COUNT(*) FROM balance_checkpoints`,
	},
	{
		table: "push_subscriptions", column: "user_id", onDelete: "CASCADE",
		why: "notifications stop until each device re-subscribes",
		seed: `INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
		       VALUES (2, 'https://push.example/endpoint', 'p256dh-key', 'auth-secret')`,
		probe: `SELECT COUNT(*) FROM push_subscriptions`,
	},
	{
		table: "saved_filters", column: "user_id", onDelete: "CASCADE",
		why: "saved views are lost",
		seed: `INSERT INTO saved_filters (user_id, name, filter_json)
		       VALUES (2, 'Groceries this year', '{}')`,
		probe: `SELECT COUNT(*) FROM saved_filters`,
	},
}

// inboundUserEdges reads every foreign key in the live schema whose parent is
// `users`, keyed "<table>.<column> ON DELETE <action>". It asks the DATABASE,
// not the migration files: 002 and 010 both changed edges that 001 declared,
// so the files are a history and only the built schema is the answer.
func inboundUserEdges(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`
		SELECT m.name, f."from", f.on_delete
		FROM sqlite_master m
		JOIN pragma_foreign_key_list(m.name) f
		WHERE m.type = 'table' AND f."table" = 'users'`)
	if err != nil {
		t.Fatalf("enumerate inbound user edges: %v", err)
	}
	defer rows.Close()
	edges := map[string]bool{}
	for rows.Next() {
		var table, column, onDelete string
		if err := rows.Scan(&table, &column, &onDelete); err != nil {
			t.Fatalf("scan inbound user edge: %v", err)
		}
		edges[table+"."+column+" ON DELETE "+onDelete] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate inbound user edges: %v", err)
	}
	return edges
}

// TestMigration020_EveryInboundUserEdgeHasACanary keeps the canary list honest
// as the schema grows.
//
// The list below it is a hand-written enumeration, and a hand-written
// enumeration of a thing the schema also declares is a duplicate that rots.
// The failure mode is silent and specific: someone adds a table with
// `user_id REFERENCES users(id) ON DELETE CASCADE`, the upgrade test still
// passes on its seven older canaries, and the one table that would have been
// emptied by a future rebuild of `users` is the one nobody is watching.
//
// So the schema is asked directly. The comparison runs BOTH ways: an edge with
// no canary is the case above, and a canary naming an edge the schema no
// longer has means the list is describing a database that does not exist —
// its seed would fail, or worse, silently stop meaning anything.
//
// This is deliberately a sibling of the upgrade test rather than a block
// inside it: it is a claim about the FIXTURE, it needs no migration run of its
// own, and when it fails the thing to fix is the list, not the migration.
func TestMigration020_EveryInboundUserEdgeHasACanary(t *testing.T) {
	db, dbPath := openTestDB(t)
	db.SetMaxOpenConns(1)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	schemaEdges := inboundUserEdges(t, db)
	if len(schemaEdges) == 0 {
		t.Fatal("found no foreign keys pointing at users — the enumeration is broken, and every assertion below would be vacuous")
	}

	canaried := map[string]bool{}
	for _, c := range userEdgeCanaries {
		canaried[c.table+"."+c.column+" ON DELETE "+c.onDelete] = true
	}

	for edge := range schemaEdges {
		if !canaried[edge] {
			t.Errorf("%s has no canary in userEdgeCanaries — a future rebuild of `users` would empty that table and TestMigration020_AppliesOverExistingUsersOfBothRoles would not notice. Add an entry that seeds one row on it and probes it.", edge)
		}
	}
	for edge := range canaried {
		if !schemaEdges[edge] {
			t.Errorf("userEdgeCanaries claims %s but the live schema has no such foreign key — the entry is stale; correct or remove it.", edge)
		}
	}
}

// TestMigration020_AppliesOverExistingUsersOfBothRoles is the upgrade case:
// the migration runs on a database that already holds real accounts, and must
// reject none of them.
//
// It is a users-row fingerprint PLUS one child-table canary per inbound
// foreign key, not a whole-schema fingerprint. The canaries are the point of
// the test, not decoration: this migration exists in trigger form precisely
// BECAUSE the rebuild that a CHECK constraint would require empties every
// table hanging off users (the header carries the measurement). The list is
// userEdgeCanaries above, and TestMigration020_EveryInboundUserEdgeHasACanary
// proves it still covers the whole schema — a regression here is not a thing
// that happens to one table, and partial coverage means a good chance the
// canary in place is not the one that would have caught it.
//
// Both edge behaviours are represented, and each carries its own probe. Six
// are ON DELETE CASCADE, where the row vanishes. transaction_audit is ON
// DELETE SET NULL, where the row SURVIVES and is silently anonymised — so its
// probe interrogates the actor column, which is the only place that damage is
// visible.
//
// It also pins that the guard is live AFTER the upgrade, not only on a
// database created fresh — a migration that applied but installed nothing
// would otherwise pass the "no row was harmed" half of this test.
func TestMigration020_AppliesOverExistingUsersOfBothRoles(t *testing.T) {
	db, dbPath := migrate020Fixture(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO users (id, username, password_hash, display_name, role, created_at, updated_at)
		VALUES (1, 'elie', '$2a$10$fake', 'Elie', 'admin', '2026-01-01T10:00:00Z', '2026-02-02T11:00:00Z')`)
	mustExec(t, db, `INSERT INTO users (id, username, password_hash, display_name, role, created_at, updated_at)
		VALUES (2, 'wife', '$2a$10$fake', 'Wife', 'member', '2026-01-03T10:00:00Z', '2026-02-04T11:00:00Z')`)

	for _, c := range userEdgeCanaries {
		mustExec(t, db, c.seed)
		// Every seed must LAND, or its post-migration assertion is 0 == 0 and
		// passes against the very rebuild this test exists to catch. Checked
		// per row, immediately, so the message names the seed that failed.
		if n := countRows(t, db, c.probe); n != 1 {
			t.Fatalf("fixture: probe for the %s edge returns %d before the migration, want 1 — the seed did not land, and this canary would be vacuous",
				c.table, n)
		}
	}

	before := userFingerprints020(t, db)

	run020ViaRunner(t, db, dbPath)

	after := userFingerprints020(t, db)
	if len(after) != len(before) {
		t.Fatalf("user count changed across the migration: before=%d after=%d", len(before), len(after))
	}
	for id, want := range before {
		got, ok := after[id]
		if !ok {
			t.Errorf("user id=%d vanished", id)
			continue
		}
		if got != want {
			t.Errorf("user id=%d changed across the migration:\n before=%s\n  after=%s", id, want, got)
		}
	}

	for _, c := range userEdgeCanaries {
		if n := countRows(t, db, c.probe); n != 1 {
			t.Errorf("the %s.%s canary is gone after the migration (probe returned %d, want 1) — that edge is ON DELETE %s, so a rebuild of `users` reaches it: %s",
				c.table, c.column, n, c.onDelete, c.why)
		}
	}
	// The SET NULL edge keeps its row and loses only the actor, so its probe
	// above looks at the column. Assert the row itself separately: if it were
	// ever to vanish, both probes would read 0 and the message above would
	// misdescribe what happened.
	if n := countRows(t, db, `SELECT COUNT(*) FROM transaction_audit`); n != 1 {
		t.Errorf("transaction_audit rows = %d after the migration, want 1 — the audit row itself must survive, not merely keep its actor", n)
	}

	// The guard is installed and live on the upgraded database.
	if _, err := db.ExecContext(ctx, `UPDATE users SET role = 'owner' WHERE id = 2`); err == nil {
		t.Error("the upgraded database accepted an out-of-set role — the migration applied but installed no guard")
	}
}

// TestMigration020_NormalisesLegacyOutOfSetRole covers the row this project
// cannot create but a hand-edited or restored database can. The migration
// repairs it to 'member' rather than aborting, and the repair is
// semantics-preserving: every authorization gate reads `role == 'admin'`, so
// such a row was already being treated as a member by the whole app.
//
// The 'admin' row in the fixture is the control. A predicate that normalised
// too much — `WHERE role != 'member'`, say — would silently demote the
// household's only admin and lock everyone out of every admin surface.
func TestMigration020_NormalisesLegacyOutOfSetRole(t *testing.T) {
	db, dbPath := migrate020Fixture(t)

	mustExec(t, db, `INSERT INTO users (id, username, password_hash, display_name, role, updated_at)
		VALUES (1, 'legacy', 'h', 'Legacy', 'owner', '2026-01-01T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO users (id, username, password_hash, display_name, role, updated_at)
		VALUES (2, 'blank', 'h', 'Blank', '', '2026-01-02T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO users (id, username, password_hash, display_name, role, updated_at)
		VALUES (3, 'boss', 'h', 'Boss', 'admin', '2026-01-03T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO users (id, username, password_hash, display_name, role, updated_at)
		VALUES (4, 'plain', 'h', 'Plain', 'member', '2026-01-04T00:00:00Z')`)

	run020ViaRunner(t, db, dbPath)

	want := map[int64]string{1: "member", 2: "member", 3: "admin", 4: "member"}
	for id, wantRole := range want {
		var gotRole, gotUpdated string
		if err := db.QueryRow(`SELECT role, updated_at FROM users WHERE id = ?`, id).Scan(&gotRole, &gotUpdated); err != nil {
			t.Fatalf("read user %d: %v", id, err)
		}
		if gotRole != wantRole {
			t.Errorf("user %d role = %q, want %q", id, gotRole, wantRole)
		}
		// updated_at records when a human last changed the account. A schema
		// repair is not that, and the migration deliberately leaves it alone.
		if wantUpdated := map[int64]string{
			1: "2026-01-01T00:00:00Z",
			2: "2026-01-02T00:00:00Z",
			3: "2026-01-03T00:00:00Z",
			4: "2026-01-04T00:00:00Z",
		}[id]; gotUpdated != wantUpdated {
			t.Errorf("user %d updated_at = %q, want it untouched at %q", id, gotUpdated, wantUpdated)
		}
	}
}

// TestMigration020_ReappliesCleanly pins the idempotency the runner's
// conventions require. The runner never re-applies a recorded migration, so
// the case this covers is a database whose schema carries 020 while its
// schema_migrations row does not (a restore taken between the two, or a
// rebuilt tracking table). Re-running the file must not error — a failure
// there is a boot loop, since the server refuses to start on a migration
// error.
func TestMigration020_ReappliesCleanly(t *testing.T) {
	db, dbPath := migrate020Fixture(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('elie', 'h', 'Elie', 'admin')`)

	run020ViaRunner(t, db, dbPath)

	body := readMigration(t, migration020)
	if _, err := db.ExecContext(ctx, body); err != nil {
		t.Fatalf("re-applying %s failed: %v", migration020, err)
	}

	// Exactly one trigger of each name survives the second run: DROP TRIGGER
	// IF EXISTS + CREATE converges rather than accumulating or colliding.
	for _, name := range []string{"users_role_guard_insert", "users_role_guard_update"} {
		n := countRows(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name = ?`, name)
		if n != 1 {
			t.Errorf("trigger %s present %d times after a re-run, want 1", name, n)
		}
	}
	// And it still guards.
	if _, err := db.ExecContext(ctx, `UPDATE users SET role = 'owner' WHERE username = 'elie'`); err == nil {
		t.Error("the guard stopped working after the migration was re-applied")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM users`); n != 1 {
		t.Errorf("users = %d after the re-run, want 1 — re-applying must not touch data", n)
	}
}

// userFingerprints020 renders every column of a users row as one SQL-quoted
// string, keyed by id. quote() round-trips what SQLite actually stored, so a
// comparison across the migration is a byte-level claim rather than a claim
// about Go's scan conversions. Same idiom as txnFingerprint019.
func userFingerprints020(t *testing.T, db *sql.DB) map[int64]string {
	t.Helper()
	rows, err := db.Query(`
		SELECT id,
		       quote(username) || '|' || quote(password_hash) || '|' || quote(display_name) || '|' ||
		       quote(role) || '|' || quote(created_at) || '|' || quote(updated_at)
		FROM users ORDER BY id`)
	if err != nil {
		t.Fatalf("read user fingerprints: %v", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var (
			id          int64
			fingerprint string
		)
		if err := rows.Scan(&id, &fingerprint); err != nil {
			t.Fatalf("scan user fingerprint: %v", err)
		}
		out[id] = fingerprint
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate user fingerprints: %v", err)
	}
	return out
}
