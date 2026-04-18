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

The first 12 chars of the full token string are also stored as `token_prefix` so the UI can distinguish tokens in the list without re-revealing the secret (GitHub's pattern).

### 3.3 Transport — `Authorization: Bearer <token>` only

Convergent modern standard (Home Assistant, GitHub, Grafana, Tailscale). Homepage supports it natively via `widget.headers`. No `X-Api-Key`, no query-param. Plex uses `?X-Plex-Token=` only because HLS/DLNA URLs can't carry headers — a constraint SpenDrop does not share.

### 3.4 Scope — user-full, no scopes column

Tokens grant the full API surface the owning user has. Explicit decision with the user to not build a scope system without a concrete second consumer. If/when a real read-only use case appears (e.g. a public dashboard share), a `scopes TEXT NULL` column becomes an additive migration.

### 3.5 Lifecycle

- **Expiry**: optional. Default **Never** (Plex-aligned; common in homelab tooling). Dropdown offers 7 days / 30 days / 90 days / 1 year / Never at creation.
- **Revocation**: soft-delete via `revoked_at`. Consistent with SpenDrop's transaction-tombstone discipline. Immediate effect at the next middleware check.
- **Mass revoke**: `DELETE /api/api-tokens` (no ID) — user-triggered "sign out all my tokens." Plex pattern.
- **Password-change cascade**: existing password-change handler revokes every live token for the user atomically in the same SQL transaction as the `UPDATE users SET password_hash = …`. Matches user expectation of "if my password leaked, nothing's still valid."
- **`last_used_at` + `last_used_ip`**: updated inside the middleware, **throttled to one write per token per 60 seconds**. SQLite is single-writer; Homepage polling at 10s × N tokens otherwise inflates WAL and fights other writers.

### 3.6 Audit table

Every mutation wraps both the `api_tokens` change and an `api_token_audit` insert in one SQL transaction, via an `ApiTokenStore` (same pattern as `TransactionStore`). Captures actor IP, user-agent, and a hash of the session token that performed the action. This is the data a future compromise investigation needs — `created_at` and `revoked_at` alone don't say *from where* or *by which session*.

Actions enum (SQLite `CHECK` constraint):

- `created`
- `revoked_by_user` — user clicked "Revoke"
- `revoked_by_password_change` — cascade from password update
- `revoked_by_mass_revoke` — user clicked "Revoke all"

### 3.7 Rate limiting

- **Token creation**: 5 per user per hour. Prevents a compromised session from minting an army of persistence tokens silently.
- **Token auth failures**: per-IP bucket, separate from the existing login rate-limiter. Won't slow brute-force (154 bits makes that impossible anyway), but throttles log-flooding and makes leaked-token probing noisy.

### 3.8 Security guardrails

1. CRC32 pre-check before DB lookup.
2. `crypto/subtle.ConstantTimeCompare` on hash comparison.
3. Bearer-auth path never creates a session cookie. Separate chi subrouter.
4. CSRF middleware explicitly skips requests with `Authorization: Bearer …`.
5. Token creation requires **password reconfirmation** inside the already-authenticated session — defense in depth with the rate limiter.
6. Logs only capture `token_prefix` + action on create / revoke; never the full token, never on successful use.
7. Expiry + revocation enforced inside the SQL `WHERE`, not in Go, to make bypass-by-bug impossible.
8. Timestamps are UTC `CURRENT_TIMESTAMP` everywhere; never mix Go `time.Now()` against DB-stored strings.
9. Admin does **not** see or revoke other users' tokens. User-scoped strictly.
10. A server-side 15-second response cache on `/api/homepage/summary` keyed by token, so a misconfigured `refreshInterval: 1000` in Homepage can't DOS the endpoint.

## 4. Data Model (migration 011)

Migration 010 is reserved for Phase 3.1b REAL-column drop (see header comment in `migrations/006_amount_cents_add.sql`). This feature ships as **011**.

```sql
-- migrations/011_api_tokens.sql

CREATE TABLE api_tokens (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    token_hash      TEXT    NOT NULL UNIQUE,
    token_prefix    TEXT    NOT NULL,
    expires_at      DATETIME NULL,
    last_used_at    DATETIME NULL,
    last_used_ip    TEXT    NULL,
    revoked_at      DATETIME NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

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
    actor_user_agent   TEXT    NULL,
    actor_session_hash TEXT    NULL,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_api_token_audit_token ON api_token_audit(token_id);
CREATE INDEX idx_api_token_audit_user  ON api_token_audit(user_id, created_at DESC);
```

Notes:

- `ON DELETE CASCADE` on `user_id`: deleting a user wipes their tokens and audit trail — correct for a single-household app.
- No `household_id`: SpenDrop's data model has no household column (confirmed in codebase audit — "household" is README copy; actual model is a shared DB with `users.role`).
- `actor_session_hash`: a hash of the session token that performed the mutation, so multiple actions from the same session can be correlated without storing plaintext session identifiers.

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

`expires_at` is a nullable ISO-8601 timestamp. Frontend submits either `null` or a pre-computed UTC timestamp based on the dropdown choice.

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
- **`month_remaining` is the signed difference** (`month_budget - month_spent`) — positive when under budget, negative when over. Homepage's `additionalField.color: adaptive` colors positive green and negative red automatically, so the "color red if over budget" behavior drops out for free.
- **Monetary values are derived from `amount_cents`** (integer) and divided by 100 at the JSON edge — same dual-write contract as the rest of SpenDrop per migration 006.
- **All queries filter `deleted_at IS NULL`** per the soft-delete discipline in `.claude/CLAUDE.md`.

## 6. Middleware & Routing

### 6.1 `RequireAPIToken` middleware

Lives in `internal/auth/api_token_middleware.go`. Pseudocode:

```go
func RequireAPIToken(queries *database.Queries) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            authz := r.Header.Get("Authorization")
            if !strings.HasPrefix(authz, "Bearer ") {
                writeError(w, http.StatusUnauthorized, "missing bearer token")
                return
            }
            plaintext := strings.TrimPrefix(authz, "Bearer ")

            if !isValidTokenFormat(plaintext) {      // regex + CRC32
                writeError(w, http.StatusUnauthorized, "invalid token")
                return
            }

            hash := sha256Hex(plaintext)
            tok, err := queries.GetAPITokenByHash(r.Context(), hash)
            if err != nil {                          // sql.ErrNoRows or DB error
                writeError(w, http.StatusUnauthorized, "invalid or expired token")
                return
            }

            user, err := queries.GetUserByID(r.Context(), tok.UserID)
            if err != nil {
                writeError(w, http.StatusUnauthorized, "user not found")
                return
            }

            // Debounced last-used update — never blocks the request.
            if tok.LastUsedAt == nil ||
               time.Since(*tok.LastUsedAt) > 60*time.Second {
                go queries.TouchAPITokenLastUsed(context.Background(), TouchParams{
                    ID:         tok.ID,
                    LastUsedIP: extractIP(r),
                })
            }

            ctx := context.WithValue(r.Context(), auth.UserContextKey, &user)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

Key invariants:

- The `GetAPITokenByHash` query enforces `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)` in SQL — bypass-by-bug impossible in Go.
- `*database.User` placed on context with the same `UserContextKey` the cookie-auth middleware uses, so every downstream handler sees an authenticated user uniformly.
- No session cookie is issued anywhere in this path.

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
- `ValidToken_AttachesUserToContext`
- `ValidToken_TouchesLastUsedWithin60sWindow`
- `ValidToken_SkipsTouchIfRecentlyUpdated`

**`internal/api/api_token_handlers_test.go`**:

- `Create_WrongPassword_401`
- `Create_ReturnsPlaintextOnceInResponse`
- `Create_EmitsAuditRowWithCreatedAction`
- `Create_RateLimitExceeded_429` (6th create within an hour)
- `List_ExcludesHashAndPlaintext`
- `List_OnlyReturnsOwnTokens` (seed two users; isolation check)
- `List_HidesRevokedByDefault`
- `RevokeOne_SoftDeletesAndEmitsAuditWithRevokedByUser`
- `RevokeOne_OtherUsersToken_404`
- `RevokeAll_SoftDeletesAllLiveTokensForUser_EmitsAuditPerToken`

**`internal/api/homepage_handlers_test.go`**:

- `Summary_RequiresBearer_401_WithSessionCookie`
- `Summary_AggregatesMonthlySpendCorrectly`
- `Summary_HidesTombstoned` — mandatory per `.claude/CLAUDE.md` soft-delete discipline. Seed one live row and one tombstoned row with sentinel amount `999`; assert tombstoned never leaks.
- `Summary_ScopedToTokenOwnerUser` (seed another user with overlapping transactions; assert isolation)
- `Summary_CurrencyFromSettings`
- `Summary_MonthRemainingIsSignedDifference` (over-budget case returns negative)
- `Summary_CachedWithin15SecondsPerToken` (mock clock; second call doesn't re-query)

**Password-change cascade** — add to `internal/api/auth_handlers_test.go`:

- `UpdatePassword_RevokesAllUsersTokensAtomically`
- `UpdatePassword_EmitsAuditRowsWithRevokedByPasswordChange`
- `UpdatePassword_Failure_DoesNotRevokeTokens` (atomicity)

**CSRF exemption** — add to the CSRF middleware test file:

- `BearerRequest_BypassesCSRFCheck`
- `CookieRequest_StillRequiresCSRF`

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
- `internal/database/store_api_token.go` — `ApiTokenStore`, wraps `Queries` plus audit row emission in one SQL transaction for `Create`, `RevokeOne`, `RevokeAllForUser`, `RevokeAllForUserAction` (used by the password-change cascade).
- `internal/auth/api_token.go` — `GenerateAPIToken() (plaintext, hash, prefix string)`, `HashAPIToken(s string) string`, `IsValidTokenFormat(s string) bool`.
- `internal/auth/api_token_middleware.go`
- `internal/auth/api_token_middleware_test.go`
- `internal/api/api_token_handlers.go` — `handleCreateAPIToken`, `handleListAPITokens`, `handleRevokeAPIToken`, `handleRevokeAllAPITokens`.
- `internal/api/api_token_handlers_test.go`
- `internal/api/homepage_handlers.go` — `handleHomepageSummary` plus the 15s per-token response cache.
- `internal/api/homepage_handlers_test.go`
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

## 11. Risks and Open Questions

1. **sqlc regeneration command** — the codebase audit confirmed `sqlc.yaml` lives at `internal/database/sqlc.yaml` but no Makefile target exists. The plan must document the exact `sqlc generate` invocation so it's reproducible.
2. **CSRF middleware location** — the audit didn't locate the exact file; plan-time exploration needed to confirm whether the skip-on-Bearer change goes in an existing middleware or in a new one.
3. **Password-change handler location** — same; the plan must pinpoint the handler that currently performs the bcrypt update before wiring the cascade.
4. **Rate limiter pattern** — unclear whether SpenDrop already has an in-memory rate limiter abstraction or whether this is the first one. Plan should either reuse the existing helper or introduce a minimal `map[userID]*tokenBucket` with a periodic cleanup goroutine.
5. **Clipboard permission on HTTPS-only origins** — `navigator.clipboard.writeText` requires a secure context. The user's domain is HTTPS so this is fine; worth a note in README for self-hosters on plain HTTP LAN.

---

**Next step:** spec review loop, then implementation plan.
