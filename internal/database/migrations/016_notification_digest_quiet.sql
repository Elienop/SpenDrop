-- 016_notification_digest_quiet.sql
-- Household-wide digest + quiet-hours preferences, additive to the single-row
-- notification_settings table (migration 015, CHECK(id=1)). No backfill: each
-- ALTER carries a NOT NULL default so the existing id=1 row is upgraded in place.
-- digest_mode 'off'|'daily' gates the daily rollup ticker. digest_time is the
-- 'HH:MM' send-time the daily digest is anchored to (default 08:00), OWNED by the
-- digest and decoupled from quiet hours. quiet_start/quiet_end are 'HH:MM' ('' =
-- unset/never quiet); quiet_tz is an IANA zone (default UTC) so the window is
-- evaluated in the household's local time. quiet_allow_over_budget lets state
-- alerts pierce quiet hours by default. last_digest_at (nullable) is the
-- restart-safe cursor for "what changed since the last digest".
ALTER TABLE notification_settings ADD COLUMN digest_mode TEXT NOT NULL DEFAULT 'off';
ALTER TABLE notification_settings ADD COLUMN digest_time TEXT NOT NULL DEFAULT '08:00';
ALTER TABLE notification_settings ADD COLUMN quiet_start TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_settings ADD COLUMN quiet_end   TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_settings ADD COLUMN quiet_tz    TEXT NOT NULL DEFAULT 'UTC';
ALTER TABLE notification_settings ADD COLUMN quiet_allow_over_budget INTEGER NOT NULL DEFAULT 1;
ALTER TABLE notification_settings ADD COLUMN last_digest_at DATETIME;
