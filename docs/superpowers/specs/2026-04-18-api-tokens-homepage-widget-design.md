# API Tokens + Homepage Widget — Design Spec

**Date:** 2026-04-18
**Branch:** `feat/api-tokens-homepage-widget`
**Status:** Design — awaiting implementation plan

---

## 1. Context

The user runs SpenDrop at `https://spendrop.nop.homes` and a Homepage (gethomepage.dev) dashboard alongside it. Homepage's built-in `customapi` widget can poll any JSON endpoint and render fields from the response, but SpenDrop's current auth is session-cookie-based — Homepage cannot authenticate that way.

This spec designs a minimal API token system that:

1. Lets a user mint long-lived bearer tokens from SpenDrop's Settings page.
2. Authorizes a new read-only `/api/homepage/summary` endpoint that Homepage's widget consumes.
3. Lays the same foundation for future bearer-auth integrations (curl, iOS Shortcuts, n8n) without redesign.

The feature is informed by research into ten self-hosted and developer tools (Home Assistant, Immich, Paperless-ngx, Linkding, Vaultwarden, GitHub fine-grained PATs, Grafana, Sonarr/Radarr, Gitea, Tailscale) plus a deep dive into Plex's `X-Plex-Token` model and Homepage's `customapi` widget source.

## 2. Goals / Non-goals

### Goals

- User can mint a long-lived bearer token with a friendly name (e.g. "Homepage dashboard").
- User can list, revoke individually, and revoke-all their own tokens.
- Tokens are revoked automatically when the user changes their password.
- Tokens paste directly into Homepage's `services.yaml` via `Authorization: Bearer {{HOMEPAGE_VAR_SPENDROP_TOKEN}}` and work end-to-end.
- `/api/homepage/summary` returns a small, fast JSON payload suitable for 30-second polling.
- Every mutation (create / revoke) leaves an audit row with actor IP, user-agent, and session attribution, so a compromised-account investigation has breadcrumbs.

### Non-goals (v1 — deferred until a concrete use case lands)

- **Scoped tokens.** Tokens grant full user-level API access. No `scopes` column. Agreed explicitly with the user: "no ghosts in the backend."
- Token rotation endpoint (revoke + create is two clicks).
- PIN / OAuth-style grant flow (Plex pattern — overkill for a single-household tool).
- Admin force-revoke of another user's tokens (user-scoped only; each user owns their own tokens).
- Per-token IP allowlist.
- Per-token usage metrics / call count.
- `X-Client-Identifier` header requirement.
- `X-Api-Key` alternate header name.
- Query-param token transport (leaks into access logs + Referer).

## 3. Key Decisions

### 3.1 Token format — `spdr_<26 base62>_<6 base62 CRC32>`

38 chars total, ~154 bits of entropy from the random segment.

- **`spdr_` prefix**: distinctive for secret scanners and log triage. Matches the `ghp_` / `glsa_` / `tskey-api-` convention.
- **Base62 (not hex)**: same entropy in ~60% the characters; terminal-safe (no `+/=` padding).
- **6-char CRC32 checksum** (GitHub's pattern): the middleware validates the checksum **before** hitting the database. This blocks typo-driven enumeration timing attacks and kills load from pasted-wrong-token requests.

Reject regex: `^spdr_[0-9A-Za-z]{26}_[0-9A-Za-z]{6}$` plus CRC32 verification.

### 3.2 Storage — SHA-256 hex, unique-indexed

Stored as 64-char lowercase hex in `token_hash`. `UNIQUE INDEX` on that column gives O(log n) lookup.

**Not bcrypt/argon2id**, despite those being correct for password storage. A 154-bit random token is not brute-forceable, so KDF hardening adds no real security; running bcrypt on every Homepage poll (default 10s, floor 1s) is a DoS vector on the request pool, and salted hashes can't be uniquely indexed for direct lookup. This matches Immich, Gitea, GitHub, and Grafana's current design; diverges from Paperless-ngx / Linkding only because those inherit DRF's legacy plaintext default.

The first **15 chars** of the full token string (`spdr_` + 10 random base62 chars, e.g. `spdr_aB3xQ9z7kL`) are also stored as `token_prefix` so the UI can distinguish tokens in the list without re-revealing the secret (GitHub's pattern). 15 chars exposes ~60 bits of the 154-bit entropy — enumerable at scale but structurally too costly to brute-force the remaining 94 bits even if the prefix leaks. Schema enforces `CHECK(length(token_prefix) = 15)` to catch insertion bugs early.

### 3.3 Transport — `Authorization: Bearer <token>` only

Convergent modern standard (Home Assistant, GitHub, Grafana, Tailscale). Homepage supports it natively via `widget.headers`. No `X-Api-Key`, no query-param. Plex uses `?X-Plex-Token=` only because HLS/DLNA URLs can't carry headers — a constraint SpenDrop does not share.

### 3.4 Scope — user-full, no scopes column

Tokens grant the full API surface the owning user has. Explicit decision with the user to not build a scope system without a concrete second consumer. If/when a real read-only use case appears (e.g. a public dashboard share), a `scopes TEXT NULL` column becomes an additive migration.

### 3.5 Lifecycle

- **Expiry**: optional. Default **Never** (Plex-aligned; common in homelab tooling). Dropdown offers 7 days / 30 days / 90 days / 1 year / Never at creation.
- **Revocation**: soft-delete via `revoked_at`. Consistent with SpenDrop's transaction-tombstone discipline. Immediate effect at the next middleware check.
- **Mass revoke**: `DELETE /api/api-tokens` (no ID) — user-triggered "sign out all my tokens." Plex pattern.
- **Password-change cascade (atomicity is load-bearing)**: the existing password-change handler opens a single `*sql.Tx`, executes both the `UPDATE users SET password_hash = …` and a `ApiTokenStore.RevokeAllForUserTx(tx, userID, "revoked_by_password_change", actorCtx)` call on that same transaction, and commits once. `ApiTokenStore` exposes `*Tx` variants that accept a `database.DBTX` (the interface already satisfied by `*sql.DB` and `*sql.Tx` — matches the existing `TransactionStore` pattern). A failure anywhere rolls back both the password update and the cascade; "all or nothing" is structurally guaranteed by the single transaction. Nested transactions are never attempted — SQLite does not support them.
- **`last_used_at` + `last_used_ip` debouncing is done at SQL, not in Go.** The middleware fires the UPDATE unconditionally from a goroutine, and the query itself gates the write: `UPDATE api_tokens SET last_used_at = CURRENT_TIMESTAMP, last_used_ip = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < datetime('now','-60 seconds'))`. When N concurrent requests arrive with a stale cached `last_used_at`, SQLite's single-writer lock serializes them and only the first one wins the WHERE predicate — the other N-1 UPDATEs affect zero rows and cost one B-tree lookup each. The Go-side staleness check is kept as a cheap fast-path skip but is not the authoritative gate. SQLite is single-writer; Homepage polling at 10s × N tokens would otherwise inflate WAL and fight other writers.
- **Graceful shutdown of the debounced touch**: middleware-spawned goroutines use `context.WithTimeout(srv.BaseContext(), 2*time.Second)`, so `SIGTERM`-triggered `srv.Shutdown()` cancels in-flight touches before the DB closes. No `context.Background()`.

### 3.6 Audit table

Every mutation wraps both the `api_tokens` change and an `api_token_audit` insert in one SQL transaction, via an `ApiTokenStore` (same pattern as `TransactionStore`). Captures actor IP, user-agent, and a hash of the session token that performed the action. This is the data a future compromise investigation needs — `created_at` and `revoked_at` alone don't say *from where* or *by which session*.

Actions enum (SQLite `CHECK` constraint):

- `created`
- `revoked_by_user` — user clicked "Revoke"
- `revoked_by_password_change` — cascade from password update
- `revoked_by_mass_revoke` — user clicked "Revoke all"

Capture rules:

- **`actor_session_hash`**: `hex(SHA-256(session_token_value))`, computed inside the handler from the active session cookie. No salt — session tokens are already high-entropy bearer secrets and a pinned hash-to-hash correlation across audit rows is the explicit goal.
- **`actor_user_agent`**: truncated to 500 chars at insert time so pathological UA strings can't bloat the audit table.
- **Idempotency**: revoking an already-revoked token is a no-op — the store returns early and emits **no** second audit row. Matches `SoftDeleteTransaction`'s contract.
- **Failed mutations emit no audit row.** A `Create` that fails password reconfirmation or rate-limit returns 401/429 *before* the store is called, so the audit table is only ever appended to after the `api_tokens` mutation committed.

### 3.7 Rate limiting

- **Token creation**: 5 successful creates per user per rolling hour, enforced in `handleCreateAPIToken`. The 6th create returns 429 with a `Retry-After` header. Prevents a compromised session from minting an army of persistence tokens silently. **Only successful password reconfirmations consume this bucket**; failed reconfirmations feed the *existing* login-failure bucket for that user (so a typo-prone legitimate user doesn't burn their create quota on wrong-password attempts, and an attacker with session but not password can't slow-probe passwords without hitting the login limiter).
- **Token auth failures (per-IP)**: rolling 10-minute window, 30 failures → 429 for the remainder of the window. Applied inside `RequireAPIToken` **only** when the token format is valid-shaped (passes regex + CRC32) but the hash lookup fails. Malformed gibberish never consumes the bucket, so a buggy client that sends `Authorization: Bearer undefined` once per poll doesn't lock out legitimate traffic from the same NAT egress. Response is 429, body `{"error":"rate limit"}`, `Retry-After: <seconds>`.
- **Implementation**: the codebase audit found no existing rate-limiter abstraction. This feature introduces `internal/ratelimit/bucket.go` with a minimal `TokenBucket` (`sync.Mutex`-guarded `map[string]*bucketState`, rolling window per key), a `time.Ticker`-driven cleanup goroutine that drops buckets idle for > 2× window, and constructors `NewUserBucket(limit, window)` / `NewIPBucket(limit, window)`. A `Clock` interface (same one used by the cache) supports deterministic tests. The existing login handler is migrated onto the same package so the "failed password reconfirmation → login bucket" fallback path above has a single source of truth. If plan-time exploration turns up an existing abstraction, reuse it instead.

### 3.8 Security guardrails

1. CRC32 pre-check before DB lookup.
2. **Hash comparison happens via a direct B-tree lookup on the UNIQUE index on `token_hash`**, not an iteration — so `crypto/subtle.ConstantTimeCompare` is structurally unnecessary here. The timing-sensitive comparison inside SQLite's binary comparator is not constant-time, but given a uniform 256-bit hash space and a CRC32 pre-filter in front of it, a timing oracle is not meaningfully exploitable.
3. Bearer-auth path never creates a session cookie. Separate chi subrouter; no handler under that subtree calls `auth.CreateSession` or writes a `Set-Cookie` header.
4. CSRF middleware explicitly skips requests with `Authorization: Bearer …`.
5. Token creation requires **password reconfirmation** inside the already-authenticated session — defense in depth with the rate limiter. Failed reconfirms feed the login-failure bucket (see §3.7).
6. Logs only capture `token_prefix` + action on create / revoke; never the full token, never on successful use.
7. Expiry + revocation enforced inside the SQL `WHERE`, not in Go, to make bypass-by-bug impossible. `GetAPITokenByHash` hard-codes `AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)` into the query body.
8. Timestamps are UTC `CURRENT_TIMESTAMP` everywhere; never mix Go `time.Now()` against DB-stored strings.
9. Admin does **not** see or revoke other users' tokens. User-scoped strictly.
10. A server-side 15-second response cache on `/api/homepage/summary` keyed by `token_hash` so a misconfigured `refreshInterval: 1000` can't DOS the endpoint. Implemented in `internal/api/homepage_cache.go` as a thread-safe `sync.Map` storing `{jsonBytes []byte; expiresAt time.Time}` per token hash. **Cache lookup runs *after* `RequireAPIToken`** — a revoked/expired token fails middleware first and never reaches the cache lookup, so invalidation-on-revoke is structurally unneeded (TTL is the only expiration path). Clock is injected via a small `Clock interface { Now() time.Time }` so tests drive it deterministically; production uses `realClock{}`.
11. All middleware error paths (missing header, malformed header, invalid shape, unknown hash, user-deleted, DB error) emit the **same** 401 body `{"error":"invalid or missing token"}`. No text distinguishes "this token was valid yesterday" from "this token never existed."

## 4. Data Model (migration 011)

Migration 010 is reserved for Phase 3.1b REAL-column drop (see header comment in `migrations/006_amount_cents_add.sql`). This feature ships as **011**.

```sql
-- migrations/011_api_tokens.sql

CREATE TABLE api_tokens (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
    token_hash      TEXT    NOT NULL UNIQUE CHECK(length(token_hash) = 64),
    token_prefix    TEXT    NOT NULL CHECK(length(token_prefix) = 15),
    expires_at      DATETIME NULL,
    last_used_at    DATETIME NULL,
    last_used_ip    TEXT    NULL,
    revoked_at      DATETIME NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Partial index for ListAPITokensForUser (live tokens only).
-- GetAPITokenByHash hits the UNIQUE INDEX on token_hash directly — no separate partial
-- needed because revoked-but-not-yet-deleted rows are rare and the WHERE clause on
-- revoked_at IS NULL / expires_at filters them post-lookup without a second index.
CREATE INDEX idx_api_tokens_user_live
    ON api_tokens(user_id)
    WHERE revoked_at IS NULL;

CREATE TABLE api_token_audit (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    token_id           INTEGER NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    user_id            INTEGER NOT NULL,
    action             TEXT    NOT NULL CHECK(action IN (
        'created',
        'revoked_by_user',
        'revoked_by_password_change',
        'revoked_by_mass_revoke'
    )),
    actor_ip           TEXT    NULL,
    actor_user_agent   TEXT    NULL CHECK(actor_user_agent IS NULL OR length(actor_user_agent) <= 500),
    actor_session_hash TEXT    NULL CHECK(actor_session_hash IS NULL OR length(actor_session_hash) = 64),
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_api_token_audit_token ON api_token_audit(token_id);
CREATE INDEX idx_api_token_audit_user  ON api_token_audit(user_id, created_at DESC);
```

Notes:

- `ON DELETE CASCADE` on `user_id`: deleting a user wipes their tokens and audit trail — correct for a single-household app.
- No `household_id`: SpenDrop's data model has no household column (confirmed in codebase audit — "household" is README copy; actual model is a shared DB with `users.role`).
- `actor_session_hash`: `hex(SHA-256(session_token_value))` (64 lowercase hex chars, CHECK-enforced); see §3.6 capture rules.
- CHECK constraints are enforced on every INSERT/UPDATE and fail fast — an off-by-one in prefix slicing or an unbounded-name bug surfaces at write time rather than corrupting the table.

## 5. API Surface

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/api-tokens` | session + password reconfirm | Create token. Returns plaintext **once**. |
| `GET` | `/api/api-tokens` | session | List caller's live tokens (no hash, no plaintext). |
| `DELETE` | `/api/api-tokens/{id}` | session | Revoke one token (soft-delete, idempotent). |
| `DELETE` | `/api/api-tokens` | session | Revoke all caller's live tokens. |
| `GET` | `/api/homepage/summary` | Bearer | Homepage widget payload. Cached 15s per token. |

### 5.1 POST /api/api-tokens

Request:

```json
{
  "name": "Homepage dashboard",
  "expires_at": null,
  "password": "<current password>"
}
```

`expires_at` is a nullable ISO-8601 UTC timestamp. Frontend submits either `null` or a pre-computed UTC timestamp based on the dropdown choice.

Server-side validation on `POST`:

- `name`: non-empty, ≤ 100 chars after trim. Rejects whitespace-only.
- `expires_at`: if non-null, must be strictly in the future (> `CURRENT_TIMESTAMP`) AND ≤ 10 years ahead of now. Past timestamps and suspiciously far-future ones (e.g. year 9999 to create a permanent-looking "1-year" token) return `400 invalid expires_at`.
- `password`: non-empty string; checked with the existing `bcrypt.CompareHashAndPassword` helper.

Response `201 Created`:

```json
{
  "id": 7,
  "name": "Homepage dashboard",
  "token_prefix": "spdr_aB3xQ9z7kL",
  "expires_at": null,
  "created_at": "2026-04-18T14:23:00Z",
  "token": "spdr_aB3xQ9z7kLmN3pRsTv2wXyZf_abc123"
}
```

The `token` field is present only on this response. Every subsequent GET omits it.

Errors: `400` malformed body · `401` password mismatch · `429` rate limit exceeded.

### 5.2 GET /api/api-tokens

Response:

```json
{
  "tokens": [
    {
      "id": 7,
      "name": "Homepage dashboard",
      "token_prefix": "spdr_aB3xQ9z7kL",
      "created_at": "2026-04-18T14:23:00Z",
      "last_used_at": "2026-04-18T18:45:00Z",
      "last_used_ip": "192.168.1.50",
      "expires_at": null
    }
  ]
}
```

Filters `revoked_at IS NULL` by default. A future `?include=revoked` query param would support a "Show revoked" toggle but is not in v1.

### 5.3 GET /api/homepage/summary

Response:

```json
{
  "month_spent": 1234.56,
  "month_budget": 2000.00,
  "month_remaining": 765.44,
  "txn_count": 42,
  "over_budget_categories": 2,
  "currency": "EUR",
  "as_of": "2026-04-18T14:23:00Z"
}
```

Shape notes (informed by Homepage widget research):

- **Flat, one level deep** — Homepage's `shvl.get` supports dot-paths but flat is simpler to eyeball and eliminates YAML `field:` typos.
- **Numeric values unformatted** — Homepage has no `currency` format; the widget renders `format: float` + `prefix: "$"`. Returning raw numbers keeps locale concerns on the display side.
- **Snake_case** — matches SpenDrop's existing handler DTO convention (confirmed in `internal/api/category_handlers.go`).
- **Monetary values are derived from `amount_cents`** (integer) and divided by 100 at the JSON edge — same dual-write contract as the rest of SpenDrop per migration 006.
- **All queries filter `deleted_at IS NULL`** per the soft-delete discipline in `.claude/CLAUDE.md`.

Field semantics (explicit, to prevent ambiguity in the summary handler):

- **`month_spent`**: sum of `amount_cents` for the owning user's live transactions in the **current month** where `amount_cents > 0` (expense sign by SpenDrop's existing convention), divided by 100.
- **`month_budget`**: sum of every active budget's `monthly_amount_cents` for the owning user in the current month, divided by 100.
- **`month_remaining`**: `month_budget - month_spent`, signed. Positive = under budget, negative = over. Homepage's `additionalField.color: adaptive` renders positive green, negative red automatically.
- **`txn_count`**: count of **month-to-date** live transactions for the owning user. **Not lifetime.** Month-to-date keeps the field consistent with `month_spent` so a dashboard row labeled "Transactions" reads as "this month."
- **`over_budget_categories`**: count of categories that have a non-null monthly budget **AND** whose month-to-date spend strictly exceeds that budget. Categories without a budget set are not counted.
- **`currency`**: ISO-4217 code read from `user_settings` (same lookup the dashboard uses).
- **`as_of`**: RFC-3339 UTC timestamp of when the aggregation actually ran — **not** when the cache served the response. Cached hits return the original run time so consumers can detect cache staleness.
- **Month boundary**: the current calendar month in the user's configured timezone, computed by the same helper `dashboard_handlers.go` already uses (plan-time step: locate the helper, reuse verbatim — do not reimplement).

## 6. Middleware & Routing

### 6.1 `RequireAPIToken` middleware

Lives in `internal/auth/api_token_middleware.go`. Pseudocode:

```go
// Every 401 path emits EXACTLY this body. Prevents valid-vs-invalid enumeration via
// error text or timing side channels.
const opaqueAuthFailure = "invalid or missing token"

func RequireAPIToken(
    queries *database.Queries,
    authFailLimiter *ratelimit.Bucket,    // per-IP; see §3.7
    baseCtx context.Context,              // srv.BaseContext() — for graceful shutdown
) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := extractIP(r)

            authz := r.Header.Get("Authorization")
            if !strings.HasPrefix(authz, "Bearer ") {
                writeError(w, http.StatusUnauthorized, opaqueAuthFailure)
                return
            }
            plaintext := strings.TrimPrefix(authz, "Bearer ")

            if !isValidTokenFormat(plaintext) {   // regex + CRC32, pure-Go, no DB hit
                writeError(w, http.StatusUnauthorized, opaqueAuthFailure)
                return
            }

            // Rate-limit check before DB — valid-shape misses below will consume;
            // malformed gibberish above does not.
            if authFailLimiter.Exhausted(ip) {
                w.Header().Set("Retry-After", authFailLimiter.RetryAfter(ip))
                writeError(w, http.StatusTooManyRequests, "rate limit")
                return
            }

            // GetAPITokenByHash hard-codes the expiry + revocation filter in its
            // WHERE clause — Go-side bypass impossible (§3.8 guardrail 7).
            hash := sha256Hex(plaintext)
            tok, err := queries.GetAPITokenByHash(r.Context(), hash)
            if err != nil {                        // sql.ErrNoRows OR revoked OR expired OR DB error
                authFailLimiter.Consume(ip)
                writeError(w, http.StatusUnauthorized, opaqueAuthFailure)
                return
            }

            user, err := queries.GetUserByID(r.Context(), tok.UserID)
            if err != nil {                        // user deleted, DB error — one message, no text leak
                writeError(w, http.StatusUnauthorized, opaqueAuthFailure)
                return
            }

            // Debounced last-used update. The SQL WHERE gates concurrent writers (§3.5);
            // the Go-side staleness check here is a cheap fast-path skip, not the gate.
            if tok.LastUsedAt == nil ||
               time.Since(*tok.LastUsedAt) > 60*time.Second {
                go func() {
                    ctx, cancel := context.WithTimeout(baseCtx, 2*time.Second)
                    defer cancel()
                    _ = queries.TouchAPITokenLastUsed(ctx, TouchParams{
                        ID:         tok.ID,
                        LastUsedIP: ip,
                    })
                }()
            }

            // NO session cookie is issued anywhere in this path.
            ctx := context.WithValue(r.Context(), auth.UserContextKey, &user)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

Key invariants:

- **Single error message on every 401 path** — missing header, malformed prefix, invalid shape, unknown/revoked/expired hash, missing user, DB error all emit the same body with status 401. Enumeration by error text impossible.
- **SQL WHERE is the source of truth for expiry + revocation** — `GetAPITokenByHash` hard-codes `AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`. Go never re-checks.
- **No `crypto/subtle.ConstantTimeCompare`** — a UNIQUE-index B-tree lookup is not iteration, and CRC32 pre-filter plus 256-bit hash space eliminates the oracle. See §3.8 item 2.
- **`*database.User` placed on context** with the same `UserContextKey` the cookie-auth middleware uses, so downstream handlers see an authenticated user uniformly and don't need to know which auth scheme got them there.
- **No `Set-Cookie` header** is written anywhere in this path. Invariant test `BearerRoute_ResponseHasNoSetCookie` enforces this on every bearer-subrouter handler (§9.1).
- **Goroutine context is bounded** (`srv.BaseContext` + 2s timeout). A graceful shutdown cancels in-flight touches before the DB closes; no `context.Background()`.

### 6.2 Router wiring

`internal/api/router.go` gets a new subtree for Bearer-auth routes:

```go
r.Route("/api/homepage", func(r chi.Router) {
    r.Use(auth.RequireAPIToken(queries))
    r.Get("/summary", h.handleHomepageSummary)
})
```

Token-management endpoints stay under the existing session-auth subtree:

```go
r.Route("/api/api-tokens", func(r chi.Router) {
    r.Post("/", h.handleCreateAPIToken)          // + password reconfirm in handler
    r.Get("/", h.handleListAPITokens)
    r.Delete("/{id}", h.handleRevokeAPIToken)
    r.Delete("/", h.handleRevokeAllAPITokens)
})
```

### 6.3 CSRF exemption

Extend the existing CSRF middleware (wherever `requireJSONContentType` lives) to skip requests where `Authorization` starts with `Bearer `. Bearer auth is not a cookie, not sent cross-site by browsers, so CSRF protection is structurally unnecessary on that path.

Footgun note: the exemption is safe *only* if the bearer path never issues or honors a session cookie on the same response. Guardrail 3 in §3.8 enforces this in code; the `BearerRoute_ResponseHasNoSetCookie` invariant test in §9.1 enforces it at test time. If a future handler accidentally mixes bearer auth with `auth.CreateSession`, the test catches it before a cross-site scripting attacker can.

Precedence when both auth schemes are present on one request:

- If `Authorization: Bearer …` is present on a bearer-subrouter route, the bearer scheme wins and CSRF is skipped.
- Session cookie and bearer header on the same request is a pathological case (browsers don't add `Authorization` cross-origin, but curl can). Tested by `BearerAndCookieBothPresent_BearerWins_NoCSRF`.

## 7. Frontend

### 7.1 Settings tab

`web/src/pages/Settings.tsx` gains `'api-tokens'` in `VALID_TABS`, a new `<TabsTrigger value="api-tokens">API tokens</TabsTrigger>`, and an inline `ApiTokensSection` component (matching the `UsersSection` pattern already in the file). No role gate — every user manages their own tokens.

### 7.2 Components used

All shadcn primitives already installed: `Card`, `Button`, `Input`, `Label`, `Dialog`, `Table`, `Badge`, `Select`, `DropdownMenu`, `Separator`, `sonner` (toast). **No `AlertDialog`** is installed; destructive confirms reuse the `Dialog` + destructive `Button` pattern from `web/src/pages/Trash.tsx` (`ConfirmPurgeDialog`).

### 7.3 List view

`Table` columns:

| Column | Content |
|---|---|
| Name | User-provided label |
| Token | `spdr_aB3xQ9z7kL…` monospaced (the `token_prefix`) |
| Last used | `2 minutes ago · 192.168.1.50`, or "Never used" |
| Expires | `Never`, absolute date with relative tooltip, or `Expired` badge |
| Created | Relative with absolute tooltip |
| | `Revoke` button (destructive variant) |

Header row: `+ New token` (primary button) · `Revoke all` (destructive, only rendered if ≥1 live token; opens confirm dialog).

### 7.4 Create dialog

Fields:

- **Name** — `Input`, required, placeholder "Homepage dashboard".
- **Expires** — `Select` with options `Never` (default) · `7 days` · `30 days` · `90 days` · `1 year`.
- **Confirm password** — `Input type="password"`, required.

Submit → `POST /api/api-tokens` → on `201` the dialog swaps to the show-once reveal (same dialog, `DialogContent` children replaced so the user never returns to the form).

### 7.5 Show-once reveal (same dialog)

- Banner: "This is the only time you'll see this token. Store it somewhere safe."
- Full token in a read-only monospaced `<Input>` with a trailing `Copy` button. Copy uses `navigator.clipboard.writeText` + `toast.success('Copied')`.
- Collapsible "Use with Homepage" block pre-filled with YAML that embeds the just-issued token:

  ```yaml
  widget:
    type: customapi
    url: https://<your-spendrop>/api/homepage/summary
    refreshInterval: 30000
    method: GET
    display: list
    headers:
      Authorization: "Bearer spdr_aB3xQ9z7kLmN3pRsTv2wXyZf_abc123"
    mappings:
      - { field: month_spent, label: This month, format: float, prefix: "$" }
      - { field: txn_count, label: Transactions, format: number }
      - field: month_remaining
        label: Remaining
        format: float
        prefix: "$"
        additionalField: { field: month_remaining, format: float, color: adaptive }
      - { field: over_budget_categories, label: Over budget, format: number }
  ```

- `Done` closes the dialog; `onClose` clears the plaintext from React state.

### 7.6 Revoke confirm

Reuse `ConfirmPurgeDialog` pattern from `Trash.tsx`. Body text: *"Revoke '{name}'? Any integration using this token will immediately stop working."* Mass-revoke variant: *"Revoke all your API tokens? All integrations will stop working until you create new ones."*

## 8. User-Facing Homepage YAML

Copy-pasteable example, destined for a new "Homepage integration" section in `README.md`:

```yaml
- Household:
    - SpenDrop:
        icon: si-googlesheets
        href: https://spendrop.example
        description: Household expenses
        widget:
          type: customapi
          url: https://spendrop.example/api/homepage/summary
          refreshInterval: 30000
          method: GET
          display: list
          headers:
            Authorization: "Bearer {{HOMEPAGE_VAR_SPENDROP_TOKEN}}"
            Accept: application/json
          mappings:
            - field: month_spent
              label: This month
              format: float
              prefix: "$"
            - field: txn_count
              label: Transactions
              format: number
            - field: month_remaining
              label: Remaining
              format: float
              prefix: "$"
              additionalField:
                field: month_remaining
                format: float
                color: adaptive
            - field: over_budget_categories
              label: Over budget
              format: number
```

Setup steps (also in README):

1. SpenDrop → Settings → API tokens → `+ New token` → name it "Homepage", copy the revealed token.
2. In Homepage's `docker-compose.yml`, add `HOMEPAGE_VAR_SPENDROP_TOKEN=spdr_…` under `environment:`.
3. Restart the Homepage container so the env var is picked up (required — Homepage reads env vars at startup only, per https://github.com/gethomepage/homepage/discussions/3422).
4. Paste the YAML above into `services.yaml`.

## 9. Test Plan

### 9.1 Backend

**`internal/auth/api_token_middleware_test.go`** — unit tests for every middleware branch:

- `MissingAuthorizationHeader_401`
- `MalformedBearerPrefix_401`
- `InvalidTokenFormat_401` (bad regex + bad CRC32)
- `UnknownHash_401`
- `RevokedToken_401`
- `ExpiredToken_401`
- `ExpiredToken_TouchIsNotUpdated` — expired tokens never reach the touch fast-path because middleware 401s first; asserts `last_used_at` is unchanged.
- `ValidToken_AttachesUserToContext`
- `ValidToken_TouchesLastUsedWithin60sWindow`
- `ValidToken_SkipsTouchIfRecentlyUpdated`
- `ValidToken_TouchConcurrentWritersOnlyOneWins` — fires N goroutines with stale `last_used_at`; asserts exactly one UPDATE affected rows (SQL WHERE gate).
- `AllAuthFailures_SameErrorBody` — exercises every 401 path, asserts body equals `"invalid or missing token"` verbatim.
- `AuthFailureRateLimit_429AfterThreshold` — 30 failed valid-shape lookups from one IP within 10 min triggers 429 on the 31st.
- `AuthFailureRateLimit_MalformedGibberishDoesNotConsume` — `Bearer undefined` 100× from one IP does not trigger the limiter.
- `BearerAndCookieBothPresent_BearerWins_NoCSRF` — request carries both `Authorization: Bearer …` and a session cookie; asserts bearer auth is used and CSRF check is skipped.
- `BearerRoute_ResponseHasNoSetCookie` — for every handler registered on the bearer subrouter, hit it with a valid token and assert `response.Header["Set-Cookie"]` is empty. Catches accidental `auth.CreateSession` inside a bearer handler.

**`internal/api/api_token_handlers_test.go`**:

- `Create_WrongPassword_401`
- `Create_WrongPassword_DoesNotEmitAuditRow` — audit is only appended after the `api_tokens` mutation commits.
- `Create_WrongPassword_ConsumesLoginFailureBucketNotCreationBucket` — typo-prone users don't burn their create quota.
- `Create_ReturnsPlaintextOnceInResponse`
- `Create_EmitsAuditRowWithCreatedAction`
- `Create_RateLimitExceeded_429` (6th successful create within an hour)
- `Create_ExpiresAtInPast_400`
- `Create_ExpiresAtBeyond10Years_400`
- `Create_EmptyName_400`
- `Create_NameOver100Chars_400`
- `List_ExcludesHashAndPlaintext`
- `List_OnlyReturnsOwnTokens` (seed two users; isolation check)
- `List_HidesRevokedByDefault`
- `RevokeOne_SoftDeletesAndEmitsAuditWithRevokedByUser`
- `RevokeOne_AlreadyRevoked_Idempotent_NoSecondAudit` — matches `SoftDeleteTransaction`'s contract.
- `RevokeOne_OtherUsersToken_404`
- `RevokeAll_SoftDeletesAllLiveTokensForUser_EmitsAuditPerToken`
- `Revoke_EmitsAuditWithCorrectActorSessionHash` — asserts `actor_session_hash = hex(SHA-256(session_cookie_value))`.

**`internal/api/homepage_handlers_test.go`**:

- `Summary_RequiresBearer_401_WithSessionCookie`
- `Summary_AggregatesMonthlySpendCorrectly`
- `Summary_HidesTombstoned` — mandatory per `.claude/CLAUDE.md` soft-delete discipline. Seed one live row and one tombstoned row with sentinel amount `999`; assert tombstoned never leaks into any aggregate.
- `Summary_ScopedToTokenOwnerUser` (seed another user with overlapping transactions; assert isolation)
- `Summary_CurrencyFromSettings`
- `Summary_MonthRemainingIsSignedDifference` (over-budget case returns negative)
- `Summary_TxnCountIsMonthToDateNotLifetime` — seed transactions in previous months; assert count excludes them.
- `Summary_OverBudgetCategories_ExcludesCategoriesWithoutBudget` — a category with spend > 0 but no budget row is not counted.
- `Summary_OverBudgetCategories_StrictlyGreaterThan` — spend == budget is not over-budget.
- `Summary_MonthBoundaryUsesUserTimezone` — seed a transaction at 2026-04-01T00:30:00 in UTC while user timezone is `America/Los_Angeles`; assert it belongs to March (local), not April (UTC).
- `Summary_CachedWithin15SecondsPerToken` — mock clock; second call within window doesn't re-query and returns identical bytes.
- `Summary_CacheKeyIsTokenHash_NotUserID` — two tokens for the same user must get isolated cache slots (so a revoked-then-recreated token doesn't inherit stale data).
- `Summary_AsOfReflectsOriginalRunTimeNotCacheHit` — cached hit returns the `as_of` timestamp from the original aggregation, not the current time.
- `Summary_RevokedTokenReturns401_DoesNotServeStaleCache` — revoke a token whose summary is in the cache; next call with that token fails middleware first (401), never reaching cache lookup.

**Password-change cascade** — add to `internal/api/auth_handlers_test.go`:

- `UpdatePassword_RevokesAllUsersTokensAtomicallyInSameTx` — pass in a DB wrapper that counts `BeginTx` calls; assert exactly one transaction spans the password UPDATE and the cascade.
- `UpdatePassword_EmitsAuditRowsWithRevokedByPasswordChange` — one audit row per revoked token, all with `action = 'revoked_by_password_change'`.
- `UpdatePassword_Failure_DoesNotRevokeTokens` — force the password UPDATE to fail (e.g. DB mock returns error); assert no tokens were revoked AND no audit rows were inserted. Proves atomicity.
- `UpdatePassword_RevokesOnlyOwnerTokens` — seed two users' tokens; only the password-changer's are revoked.

**CSRF exemption** — add to the CSRF middleware test file:

- `BearerRequest_BypassesCSRFCheck`
- `CookieRequest_StillRequiresCSRF`

**Rate limiter** — `internal/ratelimit/bucket_test.go`:

- `UserBucket_ConsumeUpToLimit_Then429`
- `UserBucket_RollingWindow_RefillsAfterWindow`
- `IPBucket_SeparateKeysDoNotInterfere`
- `Cleanup_DropsIdleBucketsAfter2xWindow`
- `Clock_InjectedForDeterministicTests`

### 9.2 Frontend

**`web/src/pages/Settings.apiTokens.test.tsx`** (Vitest + happy-dom):

- Renders empty state when no tokens exist.
- Clicking `+ New token` opens create dialog.
- Submitting create dialog with valid password shows show-once reveal.
- `Copy` button writes to `navigator.clipboard` and fires success toast.
- Closing the reveal clears plaintext from state (cannot be re-opened).
- List shows `token_prefix` but never full token.
- `Revoke` button opens confirm dialog; confirming calls `DELETE /api/api-tokens/{id}`.
- `Revoke all` is only rendered when ≥1 live token exists.

## 10. Files Touched

### New

- `internal/database/migrations/011_api_tokens.sql`
- `internal/database/store_api_token.go` — `ApiTokenStore`, wraps `Queries` plus audit row emission in one SQL transaction. Interface:
  - `Create(ctx, params CreateParams) (ApiToken, error)` — opens its own `*sql.Tx`; inserts `api_tokens` row and `api_token_audit` row with `action='created'`; commits atomically. Returns 429-ready error if the user-creation-rate-limit bucket is exhausted.
  - `RevokeOne(ctx, id, userID int64, actor ActorContext) error` — opens own tx. Idempotent on already-revoked rows (returns nil, emits no second audit row). 404-style error if `id` is not owned by `userID`.
  - `RevokeAllForUser(ctx, userID int64, action string, actor ActorContext) (n int, err error)` — opens own tx. One audit row emitted per revoked token. `action` must be one of the enum values — validated at call site.
  - `RevokeAllForUserTx(ctx, tx database.DBTX, userID int64, action string, actor ActorContext) (n int, err error)` — same as above but reuses a caller-provided `*sql.Tx`. **This is the one the password-change cascade calls.** The non-`Tx` variant is a thin wrapper `{tx, err := db.BeginTx(ctx); defer commit/rollback; return RevokeAllForUserTx(ctx, tx, ...)}`, matching `TransactionStore`'s pattern.
  - `ActorContext` is a small struct `{IP string; UserAgent string; SessionHash string}` the handler layer fills in once and passes through, so the store doesn't need `*http.Request`.
- `internal/auth/api_token.go` — `GenerateAPIToken() (plaintext, hash, prefix string)`, `HashAPIToken(s string) string`, `IsValidTokenFormat(s string) bool`, `HashSessionToken(s string) string` (the `SHA-256` used for `actor_session_hash`).
- `internal/auth/api_token_middleware.go`
- `internal/auth/api_token_middleware_test.go`
- `internal/api/api_token_handlers.go` — `handleCreateAPIToken`, `handleListAPITokens`, `handleRevokeAPIToken`, `handleRevokeAllAPITokens`.
- `internal/api/api_token_handlers_test.go`
- `internal/api/homepage_handlers.go` — `handleHomepageSummary` plus the 15s per-token response cache (see `homepage_cache.go`).
- `internal/api/homepage_cache.go` — `summaryCache` backed by `sync.Map[string]cacheEntry`, `Clock` interface, TTL-only invalidation.
- `internal/api/homepage_handlers_test.go`
- `internal/ratelimit/bucket.go` — `TokenBucket` with `Consume`/`Exhausted`/`RetryAfter` plus a `time.Ticker` cleanup goroutine.
- `internal/ratelimit/bucket_test.go`
- `web/src/api/apiTokens.ts` — typed client wrappers.

### Edited

- `internal/database/queries.sql` — new `-- API Tokens` section with `CreateAPIToken`, `GetAPITokenByHash`, `ListAPITokensForUser`, `RevokeAPIToken`, `RevokeAllTokensForUser`, `TouchAPITokenLastUsed`, plus the summary aggregation queries (`GetHomepageSummaryForUser` or a pair of month-sum + category-over-budget counts).
- `internal/database/queries.sql.go` — regenerated via `sqlc generate`.
- `internal/api/router.go` — register `/api/api-tokens` under the session-auth subtree and the new `/api/homepage` subtree with `RequireAPIToken` middleware.
- `internal/api/auth_handlers.go` (or wherever password change lives) — cascade-revoke tokens + emit audit rows in the same SQL transaction as the password update.
- The CSRF middleware (wherever `requireJSONContentType` is defined) — skip when `Authorization` begins with `Bearer `.
- `web/src/pages/Settings.tsx` — new tab + `ApiTokensSection` component.
- `README.md` — new "Homepage integration" section with the YAML above and setup steps.
- `docs/DESIGN_GUIDE.md` — short subsection documenting the API tokens settings layout so design stays consistent.

### Regenerated (not hand-edited)

- `docs/SCHEMA.md` — run `make docs` after migration lands.

## 11. Plan-time code-reading tasks (not design ambiguity)

These three items require opening specific files during plan writing to pin exact names/locations. None are design decisions — the design is settled above.

1. **sqlc regeneration command** — confirmed `sqlc.yaml` at `internal/database/sqlc.yaml`; plan must document the exact `sqlc generate` invocation (may be bare `sqlc generate` in that dir, may require a Makefile target add).
2. **CSRF middleware location** — locate the file containing `requireJSONContentType` and add the `Authorization: Bearer …` skip there.
3. **Password-change handler location + existing bcrypt call site** — locate the current password-update handler, identify where the `UPDATE users SET password_hash` executes, and replace the bare `queries.UpdateUserPassword` call with a `BeginTx`-wrapped pair `(UpdateUserPassword, ApiTokenStore.RevokeAllForUserTx)`.

## 12. Minor notes / future iterations

- **Clipboard on non-HTTPS origins**: `navigator.clipboard.writeText` requires a secure context. The user's deployment is HTTPS so this is fine; the reveal dialog includes a fallback "Select all" helper + toast if `navigator.clipboard` is undefined, so self-hosters on plain HTTP LAN aren't blocked.
- **`last_used_ip` privacy**: the list response returns the user's own last-used IP back to their browser. Fine in the single-household threat model; a future iteration could truncate to `/24` if tokens are used from VPN exits that look sensitive.
- **YAML duplication**: the show-once reveal dialog's embedded YAML (§7.5) and the README's copy-paste block (§8) both carry the same structure. Plan should extract to a single source (README is canonical; the dialog's helper text links to it with a shortened inline example).

---

**Next step:** spec review loop, then implementation plan.
