package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"
)

// APITokenAuditAction is a typed audit action. The underlying string MUST
// match the CHECK constraint in migrations/011_api_tokens.sql. Using a typed
// enum (vs. the TransactionStore's bare-string constants) means a typo is a
// compile error instead of a runtime CHECK violation.
type APITokenAuditAction string

const (
	APITokenAuditCreated                 APITokenAuditAction = "created"
	APITokenAuditRevokedByUser           APITokenAuditAction = "revoked_by_user"
	APITokenAuditRevokedByPasswordChange APITokenAuditAction = "revoked_by_password_change"
	APITokenAuditRevokedByMassRevoke     APITokenAuditAction = "revoked_by_mass_revoke"
)

// maxUserAgentLen caps the actor_user_agent column at the length enforced by
// the CHECK constraint in migration 011. Browsers routinely emit long UA
// strings; truncating in Go yields a clean insert instead of a CHECK-violation
// error the handler would have to translate.
//
// The unit is CHARACTERS, because that is the unit the constraint is written
// in: SQLite's length() counts characters on a TEXT value. See
// TruncateUserAgent for why counting bytes here was wrong twice over.
const maxUserAgentLen = 500

// ActorContext describes who performed an API-token mutation. All four
// fields correspond to audit columns mandated by spec §3.6:
//
//	UserID      — the authenticated user owning the mutation. Zero is
//	              illegal for handler-originated writes; the store does
//	              not defend against zero because Go's zero-value for int64
//	              would point at a non-existent user and the FK on
//	              api_token_audit.user_id would surface that as an INSERT
//	              error, which is the correct loud failure.
//	IP          — remote client IP ("" maps to NULL). Handlers extract this
//	              via the existing clientIP helper used for login audits.
//	UserAgent   — User-Agent header raw ("" maps to NULL). Truncated to
//	              maxUserAgentLen characters before INSERT.
//	SessionHash — lowercase hex SHA-256 of the session cookie value ("" maps
//	              to NULL). Handlers construct this via auth.HashSessionToken
//	              from Chunk 1; the store itself does no hashing to avoid an
//	              internal/auth import cycle.
type ActorContext struct {
	UserID      int64
	IP          string
	UserAgent   string
	SessionHash string
}

// Sentinel errors exposed to callers. Handlers use errors.Is to distinguish
// these from wrapped DB errors and map to specific HTTP statuses:
//
//	ErrTokenNotFound     -> 404 (also returned when the user doesn't own the id)
//	ErrCreateRateLimit   -> 429
var (
	ErrTokenNotFound   = errors.New("api token not found or not owned by user")
	ErrCreateRateLimit = errors.New("api token creation rate limit exceeded")
)

// ApiTokenStore is the only writable surface for the api_tokens table. Every
// mutation is paired with one or more api_token_audit rows inside the same
// SQL transaction, so audit rows exist iff the mutation committed.
//
// Single-row methods (Create, Revoke, RevokeAllForUser) open their own
// short-lived tx via withTx. RevokeAllForUserTx accepts a caller-owned
// *sql.Tx so a future password-change handler can cascade revoke inside
// its own tx that also updates users.password_hash — atomic all the way
// or nothing.
type ApiTokenStore struct {
	db *sql.DB
	q  *Queries
}

// NewApiTokenStore constructs the store bound to the given DB + Queries.
// Callers pass the same handles they use for everything else.
func NewApiTokenStore(db *sql.DB, q *Queries) *ApiTokenStore {
	return &ApiTokenStore{db: db, q: q}
}

// Create inserts a token row and writes its "created" audit row atomically.
// The caller has already hashed the plaintext via auth.HashAPIToken and
// computed the 15-char prefix via auth.GenerateAPIToken's third return.
// params.Name has been trimmed and length-validated by the handler.
//
// Returns the created row (so the handler can echo id, created_at, etc. in
// the response) or a wrapped error.
func (s *ApiTokenStore) Create(
	ctx context.Context,
	actor ActorContext,
	params CreateAPITokenParams,
) (ApiToken, error) {
	var created ApiToken
	err := s.withTx(ctx, func(qtx *Queries) error {
		t, err := qtx.CreateAPIToken(ctx, params)
		if err != nil {
			return fmt.Errorf("create api token: %w", err)
		}
		created = t
		return writeAPITokenAudit(ctx, qtx, t.ID, actor, APITokenAuditCreated)
	})
	return created, err
}

// Revoke marks the token as revoked and writes a `revoked_by_user` audit
// row — atomic in one tx. Idempotent: if the token is already revoked
// (racing caller, retry after a dropped response), Revoke returns nil
// without writing a duplicate audit row. A token id that doesn't exist
// or that belongs to a different user returns ErrTokenNotFound.
//
// The ownership check lives in the SQL query (`WHERE user_id = ?`). A
// handler bug that lets user A pass user B's token id here still hits
// the ErrTokenNotFound path because the UPDATE affects zero rows AND
// the GetAPITokenByID precheck also enforces user_id.
//
// The action is hardcoded to revoked_by_user because Revoke is the
// user-initiated single-revoke path. Password-change cascade uses
// RevokeAllForUserTx with an explicit action; revoke-all-from-settings
// uses RevokeAllForUser.
func (s *ApiTokenStore) Revoke(
	ctx context.Context,
	actor ActorContext,
	tokenID int64,
) error {
	return s.withTx(ctx, func(qtx *Queries) error {
		before, err := qtx.GetAPITokenByID(ctx, GetAPITokenByIDParams{
			ID:     tokenID,
			UserID: actor.UserID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTokenNotFound
		}
		if err != nil {
			return fmt.Errorf("load token: %w", err)
		}
		if before.RevokedAt.Valid {
			// Idempotent — already revoked.
			return nil
		}
		n, err := qtx.RevokeAPIToken(ctx, RevokeAPITokenParams{
			ID:     tokenID,
			UserID: actor.UserID,
		})
		if err != nil {
			return fmt.Errorf("revoke api token: %w", err)
		}
		if n == 0 {
			// Race: another caller revoked this token between our GET and UPDATE.
			// Idempotent — no audit row.
			return nil
		}
		return writeAPITokenAudit(ctx, qtx, tokenID, actor, APITokenAuditRevokedByUser)
	})
}

// RevokeAllForUser bulk-revokes every live token for the user and writes
// one `revoked_by_mass_revoke` audit row per revoked token. Returns the
// number of rows revoked.
//
// Implementation note: spec §4's CASCADE FK forbids a sentinel bulk-audit
// id. We first LIST the live token IDs, then UPDATE + INSERT-AUDIT for
// each inside the same tx. A user with zero live tokens yields N=0 and
// zero audit rows — that is the correct outcome; spec §6 records the
// fact that the user *attempted* the operation via the HTTP request log,
// not the audit trail, because the audit row requires a real token_id.
func (s *ApiTokenStore) RevokeAllForUser(
	ctx context.Context,
	actor ActorContext,
) (int64, error) {
	var count int64
	err := s.withTx(ctx, func(qtx *Queries) error {
		n, err := revokeAllTokensForUserInTx(ctx, qtx, actor, APITokenAuditRevokedByMassRevoke)
		if err != nil {
			return err
		}
		count = n
		return nil
	})
	return count, err
}

// RevokeAllForUserTx bulk-revokes every live token for actor.UserID inside
// a caller-owned *sql.Tx and writes one audit row per revoked token in the
// same tx. The action parameter lets the caller distinguish
// revoked_by_mass_revoke (settings UI, also reachable via RevokeAllForUser)
// from revoked_by_password_change (future password-change handler).
//
// TODO(password-change-handler): this is the call site a future password-
// change handler must invoke to cascade revoke on a successful password
// update (spec §3.5). The password-change handler does not yet exist in
// the codebase — see the plan's "Correction A" for the audit trail. When
// that handler lands, it will:
//  1. Open its own *sql.Tx that also updates users.password_hash.
//  2. Call store.RevokeAllForUserTx(ctx, tx, ActorContext{…},
//     APITokenAuditRevokedByPasswordChange) inside that tx.
//  3. Commit — both the password update and the cascade revoke land
//     together, or both roll back.
func (s *ApiTokenStore) RevokeAllForUserTx(
	ctx context.Context,
	tx *sql.Tx,
	actor ActorContext,
	action APITokenAuditAction,
) (int64, error) {
	qtx := s.q.WithTx(tx)
	return revokeAllTokensForUserInTx(ctx, qtx, actor, action)
}

// revokeAllTokensForUserInTx is the shared loop used by both RevokeAllForUser
// (owns its tx) and RevokeAllForUserTx (borrows a caller-owned tx). It loads
// the live ID set, then updates + audits each row. The RevokeSingleLiveTokenByID
// query tolerates concurrent revokes (already-revoked rows affect zero rows
// and are skipped) so a parallel Revoke from the same user does not cause a
// duplicate audit row.
func revokeAllTokensForUserInTx(
	ctx context.Context,
	qtx *Queries,
	actor ActorContext,
	action APITokenAuditAction,
) (int64, error) {
	ids, err := qtx.ListLiveAPITokenIDsForUser(ctx, actor.UserID)
	if err != nil {
		return 0, fmt.Errorf("list live token ids: %w", err)
	}
	var revoked int64
	for _, id := range ids {
		n, err := qtx.RevokeSingleLiveTokenByID(ctx, id)
		if err != nil {
			return revoked, fmt.Errorf("revoke token id=%d: %w", id, err)
		}
		if n == 0 {
			// Lost race — another tx revoked this id between the LIST and
			// this UPDATE. Skip the audit row, the other tx wrote one.
			continue
		}
		if err := writeAPITokenAudit(ctx, qtx, id, actor, action); err != nil {
			return revoked, fmt.Errorf("audit token id=%d: %w", id, err)
		}
		revoked++
	}
	return revoked, nil
}

// writeAPITokenAudit writes one audit row for the given token + action +
// actor. The column set matches migration 011 verbatim: token_id, user_id,
// action, actor_ip, actor_user_agent (truncated to 500 bytes), actor_session_hash.
func writeAPITokenAudit(
	ctx context.Context,
	qtx *Queries,
	tokenID int64,
	actor ActorContext,
	action APITokenAuditAction,
) error {
	return qtx.InsertAPITokenAudit(ctx, InsertAPITokenAuditParams{
		TokenID:          tokenID,
		UserID:           actor.UserID,
		Action:           string(action),
		ActorIp:          nullStringOrEmpty(actor.IP),
		ActorUserAgent:   nullStringOrEmpty(TruncateUserAgent(actor.UserAgent)),
		ActorSessionHash: nullStringOrEmpty(actor.SessionHash),
	})
}

// TruncateUserAgent clips a User-Agent string to maxUserAgentLen CHARACTERS,
// which is the unit the column is declared in: migration 011 constrains
// CHECK(length(actor_user_agent) <= 500), and SQLite's length() counts
// characters on a TEXT value (it counts bytes only on a BLOB).
//
// This used to cut at 500 BYTES, with a comment arguing that was safe because
// UA strings are ASCII-ish and a mid-rune cut costs only a replacement
// character downstream. Both halves were wrong. A byte cut is stricter than the
// column for any non-ASCII UA — Arabic reaches 500 bytes at 250 characters, so
// the store discarded half of what the schema would have accepted, the same
// handler-stricter-than-its-own-column defect that the api_token name check
// carried. And a byte cut can land mid-rune, writing invalid UTF-8 into a TEXT
// column; that is not a display artifact, it is malformed data at rest, and
// length() over it is not meaningful.
//
// Counting characters cannot overflow the constraint: 500 characters is at most
// 2,000 bytes, and the CHECK bounds characters, not bytes.
func TruncateUserAgent(ua string) string {
	if utf8.RuneCountInString(ua) <= maxUserAgentLen {
		return ua
	}
	n := 0
	for i := range ua {
		if n == maxUserAgentLen {
			return ua[:i]
		}
		n++
	}
	return ua
}

// nullStringOrEmpty maps "" to NULL. The three optional columns
// (actor_ip, actor_user_agent, actor_session_hash) all use this.
func nullStringOrEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// withTx is the single place where single-row methods manage tx lifecycle.
// Same pattern as TransactionStore.withTx.
func (s *ApiTokenStore) withTx(ctx context.Context, fn func(*Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}
