-- 013_push_subscriptions.sql
-- Web Push: one row per browser PushSubscription.
--
-- Purely additive CREATE TABLE — no backfill, no collision machinery (contrast
-- migration 008's content_hash sweep). The table starts empty and is populated
-- only by authenticated POST /api/push/subscriptions calls, so
-- first-boot-after-upgrade has nothing to scan and cannot enter the boot-loop
-- failure mode the Phase 3.4 backfill discipline guards against.
--
-- endpoint is the push service URL the browser hands us; it is globally unique
-- per subscription, so UNIQUE(endpoint) makes the upsert target a single row
-- and lets a re-subscribe from the same browser (which produces an identical
-- endpoint) DO UPDATE the keys + re-home the row to its owner in place rather
-- than accumulating duplicates. p256dh/auth are the ECDH/auth secrets the
-- sender needs to encrypt the payload. user_id REFERENCES users(id)
-- ON DELETE CASCADE so deleting a user transparently drops their subscriptions
-- (consistent with migration 011 api_tokens).
--
-- created_at / last_seen default to datetime('now'); last_seen is bumped on
-- every re-subscribe so a future housekeeper can prune endpoints that have
-- gone quiet for months. Migration numbering: 012 was category_budgets.

CREATE TABLE push_subscriptions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL UNIQUE,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    user_agent TEXT,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    last_seen DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_push_subscriptions_user ON push_subscriptions(user_id);
