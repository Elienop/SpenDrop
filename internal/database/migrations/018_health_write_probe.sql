-- 018_health_write_probe.sql
-- A one-row scratch table whose only purpose is to be WRITTEN by /healthz/data.
--
-- Every other sub-check on that endpoint is a read, and `PRAGMA quick_check`
-- passes happily on a database the process cannot write to. That gap is not
-- hypothetical: a restore that leaves the file owned by root while the server
-- runs as PUID:PGID produces exactly "attempt to write a readonly database",
-- and until this table existed the first thing in the whole system to notice
-- was a failed scheduled backup — up to BACKUP_INTERVAL (default 24h) later.
-- Backlog B7 piece 2. The handler side is probeWritable in
-- internal/api/health_handlers.go.
--
-- Shape notes, each of which is load-bearing:
--
--  * CHECK (id = 1) is a STORAGE BOUND enforced by the schema rather than by
--    the discipline of the one caller. The probe runs on every scrape — the
--    container health check alone is 2/min, and an external monitor may add
--    ~6/min indefinitely — so an append-only design would grow the database
--    forever at a rate nobody is watching. With this constraint the table
--    cannot hold a second row no matter what a later caller writes, and the
--    upsert in probeWritable rewrites the same row in place, so the table
--    never grows. The -wal file recycles frames up to SQLite's 1000-page
--    autocheckpoint (~4 MiB measured) — EXCEPT while a concurrent reader
--    pins a WAL snapshot, which is what VACUUM INTO holds during a backup;
--    see the operational note at the /healthz/data registration in
--    internal/api/router.go for the measured numbers.
--
--  * No foreign key and no reference to any user-facing table. The row is not
--    data, it is evidence that a write committed. It must never appear in the
--    transaction counts, in last_write_at, in the backup verifier's row-count
--    parity check (which counts `transactions` only), or in any UI surface.
--
--  * DATETIME rather than a bare counter, so an operator opening the file with
--    sqlite3 can see when the last successful probe ran. The handler binds its
--    own clock rather than datetime('now') so tests can freeze it.
--
-- Purely additive CREATE TABLE: no backfill, and no UNIQUE index over legacy
-- data, so the migration-backfill collision-tolerance discipline does not
-- apply here. The table starts EMPTY on purpose — probeWritable's statement is
-- an upsert, so the first scrape on a fresh database seeds row 1 and reports
-- ok without this migration having to invent a timestamp for a probe that
-- never ran.

CREATE TABLE health_write_probe (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    probed_at DATETIME NOT NULL
);
