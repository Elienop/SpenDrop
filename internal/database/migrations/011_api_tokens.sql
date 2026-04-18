-- 011_api_tokens.sql
-- API tokens for the Homepage widget integration (spec
-- docs/superpowers/specs/2026-04-18-api-tokens-homepage-widget-design.md §4).
-- Two tables:
--   * api_tokens        — live metadata, one row per token, hashed never plaintext
--   * api_token_audit   — append-only mutation log, paired with every api_tokens
--                         write by the ApiTokenStore chokepoint in
--                         internal/database/store_api_token.go
--
-- Migration numbering: 010 is reserved for the REAL column drop (Phase 3.1b).
-- This feature is 011.
--
-- CHECK constraints are enforced on every INSERT/UPDATE and fail fast — an
-- off-by-one in prefix slicing or an unbounded-name bug surfaces at write
-- time rather than corrupting the table.

CREATE TABLE IF NOT EXISTS api_tokens (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Handler trims whitespace and validates 1..100 chars first; CHECK is
    -- a second layer that catches any future bypass. Trimming is NOT done
    -- in the CHECK so whitespace-only submissions fail at the handler, not
    -- the DB (the two layers have deliberately different strictness per
    -- spec §5.1).
    name            TEXT    NOT NULL CHECK(length(name) BETWEEN 1 AND 100),

    -- token_hash is lowercase hex SHA-256 of the plaintext token. UNIQUE
    -- because there is no salt (a salted hash cannot be uniquely indexed
    -- for O(log n) lookup, and a 154-bit random token is not brute-forceable).
    -- CHECK pins the length to 64 hex chars so a caller bug that truncates
    -- the hash fails here, not silently.
    token_hash      TEXT    NOT NULL UNIQUE CHECK(length(token_hash) = 64),

    -- First 15 chars of the plaintext ("spdr_" + first 10 random base62).
    -- Displayed in the Settings list so users can distinguish tokens without
    -- re-revealing the secret. CHECK pins the length.
    token_prefix    TEXT    NOT NULL CHECK(length(token_prefix) = 15),

    -- expires_at NULL means "never expires". V1 defaults to long-lived
    -- tokens (spec §3.2); this column is reserved for a future TTL feature.
    expires_at      DATETIME NULL,

    -- last_used_at + last_used_ip are updated by TouchAPITokenLastUsed, which
    -- is debounced at the SQL level so back-to-back polls don't write-amplify
    -- this row. NULL means "never used". last_used_ip folds empty-string to
    -- NULL at write time via NULLIF so reverse proxies that strip the header
    -- don't leave "" noise.
    last_used_at    DATETIME NULL,
    last_used_ip    TEXT    NULL,

    -- revoked_at NULL = live. Populated = dead. No revoke_reason column —
    -- the reason is encoded in the paired audit row's action
    -- (revoked_by_user / revoked_by_password_change / revoked_by_mass_revoke).
    revoked_at      DATETIME NULL,

    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Partial index supports ListAPITokensForUser's common path (live tokens
-- only). Revoked rows are filtered by handler code; a partial index keeps
-- the hot set small even if the table accumulates revoked rows over time.
-- GetAPITokenByHash hits the UNIQUE INDEX on token_hash directly — no
-- separate partial needed because revoked-but-not-yet-deleted rows are
-- rare and the WHERE clause on revoked_at IS NULL filters them post-lookup.
CREATE INDEX idx_api_tokens_user_live
    ON api_tokens(user_id)
    WHERE revoked_at IS NULL;

-- api_token_audit captures enough to answer "who revoked this token, from
-- where, in which session" for a future compromise investigation. The
-- table is APPEND-ONLY by application code — never UPDATEd or DELETEd.
--
-- FK on token_id with ON DELETE CASCADE: when a user is deleted (via
-- api_tokens.user_id CASCADE), their tokens vanish and the audit trail
-- goes with them. Single-household app invariant (spec §4 notes).
CREATE TABLE IF NOT EXISTS api_token_audit (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    token_id           INTEGER NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,

    -- user_id is the owner of the token (denormalised from api_tokens for
    -- fast per-user audit queries without a join). Enforced NOT NULL so the
    -- forensic reader can always filter "all audit rows for user X" without
    -- a fallback path.
    user_id            INTEGER NOT NULL,

    action             TEXT    NOT NULL CHECK(action IN (
        'created',
        'revoked_by_user',
        'revoked_by_password_change',
        'revoked_by_mass_revoke'
    )),

    -- actor_ip is the remote address as seen by the handler (trimmed of
    -- port, X-Forwarded-For honoured by the middleware before it reaches
    -- the store). NULL is legitimate for system-originated writes.
    actor_ip           TEXT    NULL,

    -- actor_user_agent is truncated to 500 chars at insert time by the
    -- store so pathological UA strings can't bloat the table. The CHECK
    -- is a belt-and-braces backstop in case a future caller bypasses the
    -- store truncation.
    actor_user_agent   TEXT    NULL CHECK(actor_user_agent IS NULL OR length(actor_user_agent) <= 500),

    -- actor_session_hash is lowercase hex SHA-256 of the session cookie
    -- value. Pinned to 64 chars by CHECK. Stored as a hash so the audit
    -- table never holds a credential that could replay a session.
    actor_session_hash TEXT    NULL CHECK(actor_session_hash IS NULL OR length(actor_session_hash) = 64),

    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_api_token_audit_token ON api_token_audit(token_id);
CREATE INDEX idx_api_token_audit_user  ON api_token_audit(user_id, created_at DESC);
