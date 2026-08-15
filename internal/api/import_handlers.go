package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/xuri/excelize/v2"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// importEntry holds parsed rows from an uploaded Excel file, kept in memory
// until the user confirms the import. The mu mutex guards Rows against
// concurrent handlers — the realistic race is two browser tabs on the same
// import session, where one tab's PATCH mutation can interleave with
// another tab's PATCH/Confirm/GET read of the same slice. Every handler
// that touches Rows must Lock/defer Unlock before doing so; the mutex is
// held across buildCollisionGroups calls so the returned groups snapshot
// is consistent with the Rows snapshot returned in the same response.
type importEntry struct {
	UserID    int64
	Rows      []importRow
	Columns   []string
	CreatedAt time.Time
	mu        sync.Mutex
}

// importRow represents a single parsed row from the Excel file. RowID is
// a stable 0-based positional index assigned at upload time by the preview
// builder; it never renumbers, and the PATCH endpoint uses it as the merge
// key so the frontend can address a row unambiguously after edits. Skip
// marks a row as excluded from the confirm-time insert loop.
type importRow struct {
	RowID            int     `json:"row_id"`
	Date             string  `json:"date"`
	Description      string  `json:"description"`
	Amount           float64 `json:"amount"`
	Category         string  `json:"category"`
	Tags             string  `json:"tags,omitempty"`
	Notes            string  `json:"notes,omitempty"`
	OriginalAmount   float64 `json:"original_amount,omitempty"`
	OriginalCurrency string  `json:"original_currency,omitempty"`
	// Rate is the sheet's own exchange rate for this row, in the units of
	// currencies.rate_to_base (foreign units per base unit, so LBP 89000 is
	// 89,000 LBP to the dollar). Zero means the sheet quoted no rate.
	Rate float64 `json:"rate,omitempty"`
	// RawRate is the Rate cell exactly as it arrived, kept so the resolver can
	// tell an ABSENT rate from an unparseable one — #5 (rate_missing) and #9
	// (rate_invalid) are different faults with different fixes, and Rate alone
	// is 0 for both. It never leaves the server AS ITSELF: the preview echoes
	// it back under rate_raw only when it is unusable, so the table can show
	// what the sheet held beside a message telling the user to fix it.
	RawRate string `json:"-"`
	// RawAmount is the Amount cell as it arrived — the third and last of these
	// raw twins, and the one whose absence was doing the most damage: an
	// unreadable base amount became a zero and the row left as zero_amount,
	// counted in "N skipped" with no row named and no message anywhere. That
	// is silent data loss on the primary money column.
	RawAmount string `json:"-"`
	// RawOriginalAmount is the Original Amount cell as it arrived, kept for
	// exactly the reason RawRate is. parseImportAmount ZEROES a cell it cannot
	// use — out of range, or unparseable — so OriginalAmount alone cannot tell
	// "the sheet stated no original" from "the sheet stated one the ledger
	// cannot hold", and the two need opposite messages: one says there is
	// nothing to convert, the other says the figure is too big.
	RawOriginalAmount string `json:"-"`
	Skip              bool   `json:"skip"`
}

// dbMatchPreview carries the displayable fields of a live DB row that
// collides with an import row's content hash. Sent inline inside a
// collisionGroup so the frontend can render "you're about to re-import
// this existing transaction" context without a second round-trip. Populated
// only for groups whose reason is "db_match".
//
// AmountCents is the standing, deliberate exception to the dollars-on-the-wire
// rule (see the Money Wire-Edge DTO discipline): it is typed as cents on the
// client and formatted there, it predates this stage, and renaming it now
// would break a shape the frontend already reads. Every OTHER money field on
// an import response is dollars.
type dbMatchPreview struct {
	ID           int64  `json:"id"`
	Date         string `json:"date"`
	Description  string `json:"description"`
	AmountCents  int64  `json:"amount_cents"`
	CategoryName string `json:"category_name"`
}

// collisionGroup is a set of row_ids within one import session that share
// the same content hash. Reason is "intra_file" when the group is composed
// entirely of preview rows; "db_match" when at least one member row's hash
// also matches a live DB transaction (in which case DBMatch is populated).
// Groups of size 1 are never emitted — a single clean row is not a
// collision.
type collisionGroup struct {
	GroupID      string          `json:"group_id"`
	Reason       string          `json:"reason"`
	MemberRowIDs []int           `json:"member_row_ids"`
	DBMatch      *dbMatchPreview `json:"db_match,omitempty"`
}

// importFieldError names one preview row whose value in one field is longer
// than the ledger will store, and carries the explanation with it.
//
// The message travels on the wire rather than being composed by the frontend,
// because this condition is reachable four ways — flagged at upload, re-flagged
// after a PATCH, restored by a GET resume, and refused by the confirm 409 — and
// one of those already returns a server-authored message today (the PATCH 400
// from validateImportField). Wording composed on the client for three of the
// four would mean the same condition reading two different ways depending on
// how the user got there, and drifting apart the first time either side was
// edited. Collisions are modelled the other way round on purpose: they are
// explained per GROUP by one banner, so there is no per-cell string to keep in
// step.
type importFieldError struct {
	RowID   int    `json:"row_id"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Field names as they appear in importFieldError.Field and in the PATCH
// endpoint's request body, so a frontend can route an error straight to the
// control that edits it.
//
// Two families share the union, and the split decides where a flag is SHOWN.
// The length family (description, tags, notes) says a value is longer than the
// ledger stores. The money family (rate, original_currency, amount) says the
// row quotes money that cannot be resolved into a stored value — no rate for a
// foreign original, a currency the household has not set up, or an Amount cell
// that contradicts original ÷ rate. Only description, rate and amount name a
// cell the preview can edit; the rest are fixed in Settings or in the source
// spreadsheet, and every message says which.
const (
	importFieldDescription      = "description"
	importFieldTags             = "tags"
	importFieldNotes            = "notes"
	importFieldRate             = "rate"
	importFieldOriginalCurrency = "original_currency"
	importFieldAmount           = "amount"
)

// importFieldLengthMessage is the single source of the explanation for a field
// being too long. Every surface that reports the condition calls it, including
// validateImportField's PATCH 400, so a description error reads identically
// however the user reached it.
//
// The remedies differ between fields because the preview's capabilities do:
// validateImportField accepts edits to date, description and amount only, so a
// description really can be shortened in place, while tags and notes can only
// be dropped with the row or fixed in the source spreadsheet. Telling someone
// to shorten a note "here" would be an instruction the UI cannot honour.
//
// Each message names the LIMIT and never the overage, and that rule now rests
// on a different reason than it did when it was written. It used to be that the
// caps were compared with len() over bytes while the messages said
// "characters", so any overage count would have been a lie in non-ASCII text.
// The caps are character counts now (charLen, limits.go), so an overage would
// at least be arithmetically true — but it would still be noise the user cannot
// act on without counting, and naming the limit is the shorter true statement.
// Keep it as is.
func importFieldLengthMessage(field string) string {
	switch field {
	case importFieldDescription:
		return fmt.Sprintf(
			"Too long for SpenDrop, which stores %d characters. Shorten it here, or skip this row.",
			MaxDescriptionLength)
	case importFieldTags:
		return fmt.Sprintf(
			"This row's tags are longer than the %d characters SpenDrop stores. Skip this row, or shorten them in your spreadsheet and upload again.",
			MaxTagsLength)
	case importFieldNotes:
		return fmt.Sprintf(
			"This row's note is longer than the %d characters SpenDrop stores. Skip this row, or shorten the note in your spreadsheet and upload again.",
			MaxNotesLength)
	case importFieldOriginalCurrency:
		// "can be", not "SpenDrop stores": the column has no limit, the
		// household's own currency codes are three uppercase letters, and this
		// cap is what a code may be for import to consider it one at all. The
		// remedy is the spreadsheet, like tags and notes — the preview has no
		// currency editor, because an unknown currency is resolved in Settings.
		return fmt.Sprintf(
			"This row's currency code is longer than the %d characters a currency code can be. Skip this row, or fix it in your spreadsheet and upload again.",
			MaxCurrencyCodeLength)
	}
	return ""
}

// checkImportRowLengths reports every non-skipped row whose description, tags
// or notes exceed what the ledger stores.
//
// THE MEASUREMENT MUST MATCH THE WRITE PATH, and that is the whole risk in this
// function. validateTransactionRequest bounds these three fields with charLen —
// CHARACTERS, Unicode code points — and no trimming (transaction_handlers.go,
// plus validateTagsField); the import parser has already trimmed each cell by
// the time a row reaches here, so charLen over the stored value is the same
// quantity the single-row API would apply. Measure anything else — len() over
// bytes, a trimmed copy, a different cap — and this becomes a gate that passes
// rows the write path would refuse, which is worse than no gate, because the
// failure moves from a preview the user can act on to an insert they cannot.
// TestImportFieldLengths_MatchTheWritePath pins the agreement, on values that
// straddle the boundary in both units.
//
// Both sides used to be len() over bytes, which made these caps byte limits
// wearing a character label — 500 was about 250 Arabic characters. They moved
// to characters TOGETHER, in one change, because moving either alone is exactly
// the divergence this function exists to prevent.
//
// Rows the user has skipped are exempt. Skipping IS the remedy the gate offers,
// so a skipped row must not keep blocking the confirm it was skipped to unblock.
func checkImportRowLengths(rows []importRow) []importFieldError {
	fieldErrors := []importFieldError{}
	for _, row := range rows {
		if row.Skip {
			continue
		}
		for _, check := range []struct {
			field string
			value string
			limit int
		}{
			{importFieldDescription, row.Description, MaxDescriptionLength},
			{importFieldTags, row.Tags, MaxTagsLength},
			{importFieldNotes, row.Notes, MaxNotesLength},
			// The currency cell is bounded here rather than only where it is
			// echoed, because it is the one row value with no column limit
			// behind it: an xlsx cell holds 32,767 characters, and until this
			// stage an unknown code was stored verbatim rather than reported.
			// Now it is reported — once per row, on four preview surfaces and
			// in a 409 body — so the cap is what keeps a preview response
			// proportional to a household's ledger. See MaxCurrencyCodeLength.
			{importFieldOriginalCurrency, row.OriginalCurrency, MaxCurrencyCodeLength},
		} {
			if charLen(check.value) > check.limit {
				fieldErrors = append(fieldErrors, importFieldError{
					RowID:   row.RowID,
					Field:   check.field,
					Message: importFieldLengthMessage(check.field),
				})
			}
		}
	}
	return fieldErrors
}

// buildCollisionGroups computes the collision view for a preview session.
// It groups rows by their resolved content hash, then flags each group as
// intra_file (multiple preview rows sharing a hash with no DB match) or
// db_match (at least one preview row whose hash also matches a live DB
// transaction). Rows marked Skip are EXCLUDED from group membership: a
// skipped row can't collide with anyone because it isn't going to be
// inserted, so counting it would make the progress meter lie.
//
// Rows that fail to resolve to a valid hash — unparseable date, empty
// description, no storable money, mismatched money signs, an over-long field,
// an unresolved category — are silently omitted from grouping. They'll still
// be rejected at confirm time by processImportRows; the preview's job here is
// to flag collisions, not to re-implement the full row validator.
//
// The hash is computed over the cents the row WILL STORE, and it gets them
// from preCategorySkipReason — the same call, returning the same importMoney,
// that the insert loop builds its params from. That is what makes the preview
// and the insert one identity rather than two implementations that agree
// today: a rate row's identity is its DERIVED cents, so a hand-typed row at
// 89,000 and a sheet row at 89,000 are the same transaction, while the same
// original quoted at 89,500 is a different booking. Hashing the sheet's own
// Amount cell instead would hash zero for a row whose USD is empty, drop it
// from grouping, and report no collision right up until confirm skipped it as
// a duplicate.
//
// Called from three places:
//  1. buildImportPreview, for the upload / PATCH / GET responses.
//  2. handleImportConfirm's gate, with the user's real category choices.
//  3. nothing else — a fourth caller would be a fourth chance to disagree.
//
// The DB lookup is O(rows) — one GetTransactionByContentHash call per
// hash-resolvable row. Callers MUST pass the already-loaded category
// lookups so the hash formula uses the canonical DB category name
// (matching handleImportConfirm exactly).
func buildCollisionGroups(
	ctx context.Context,
	queries *database.Queries,
	rows []importRow,
	categoryMap map[string]int64, // optional: user's resolved name->id map from confirm flow; nil at upload time
	defaultCategoryID int64, // optional: user's chosen default at confirm time; 0 at upload time
	catNameToID map[string]int64,
	catIDToName map[int64]string,
	cur importCurrencies,
) ([]collisionGroup, error) {
	byHash := make(map[string][]int) // hash -> member row_ids

	// Hashable rows keep their row_id; non-hashable rows are skipped.
	for _, row := range rows {
		if row.Skip {
			continue
		}
		// One predicate, shared with the insert loop, rather than a second
		// copy of the same list. Every reason it blocks on — unparseable date,
		// empty description, over-long field, contradictory signs, unresolvable
		// money, nothing to store — is a reason confirm will reject the row, so
		// grouping it would 409 the whole import over a row that was never
		// going to land. The resolved money comes back with it, which is what
		// makes the hash below the SAME quantity the insert hashes.
		date, money, _, skipped := preCategorySkipReason(row, cur)
		if skipped {
			continue
		}
		categoryID := resolveCategoryID(row.Category, categoryMap, catNameToID, defaultCategoryID)
		if categoryID == 0 {
			continue
		}
		canonical, ok := catIDToName[categoryID]
		if !ok {
			continue
		}
		hash := database.ComputeContentHash(
			date,
			money.AmountCents,
			row.Description,
			canonical,
		)
		byHash[hash] = append(byHash[hash], row.RowID)
	}

	// Emit groups. For each hash with ≥2 members, it's at minimum intra_file.
	// For any hash that also matches a live DB row, we upgrade to db_match
	// and attach the DB row's displayable fields.
	groups := []collisionGroup{}
	for hash, members := range byHash {
		// Look up DB match first (single row by hash). Even a size-1 group
		// graduates to a collision if it matches an existing DB row.
		var dbMatch *dbMatchPreview
		existing, err := queries.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			// A DB fault during hash lookup is a systemic error — return
			// it to the caller so the HTTP handler can 500. Silently
			// dropping the row would be fine if this were only a UI hint,
			// but the collision grouping is load-bearing for blocking
			// /confirm, so we surface the fault.
			return nil, fmt.Errorf("lookup content hash during grouping: %w", err)
		}
		if err == nil {
			// DB category name is carried forward from catIDToName so the
			// frontend's inline preview matches what the transactions page
			// would show. CategoryID is non-nullable in the live schema
			// (the partial unique index filters content_hash IS NOT NULL,
			// and every hashable live row has a category), so the lookup
			// uses a plain int64 key — no sql.NullInt64 unwrapping.
			dbCatName := ""
			if name, ok := catIDToName[existing.CategoryID]; ok {
				dbCatName = name
			}
			dbMatch = &dbMatchPreview{
				ID:           existing.ID,
				Date:         existing.Date.Format("2006-01-02"),
				Description:  existing.Description,
				AmountCents:  existing.AmountCents,
				CategoryName: dbCatName,
			}
		}

		// A size-1 group is only a collision if it db_matches. Size-≥2 is
		// always a collision.
		if len(members) < 2 && dbMatch == nil {
			continue
		}

		reason := "intra_file"
		if dbMatch != nil {
			reason = "db_match"
		}

		// GroupID: 8 random bytes (hex) per session so the value is opaque
		// from the client's POV — deriving it from hash[:8] leaked hash
		// fragments to the frontend and invited "reverse the hash from the
		// group_id" thinking. 4 bytes of entropy is enough for a preview
		// session (max MaxImportRows groups); a collision would only cost
		// a UI glitch, not security. rand.Read failures here are handled
		// as a DB-level fault — the grouping response is load-bearing for
		// blocking /confirm, so propagating the error to a 500 is safer
		// than emitting a group with a blank ID.
		idBytes := make([]byte, 4)
		if _, err := rand.Read(idBytes); err != nil {
			return nil, fmt.Errorf("generate group_id: %w", err)
		}
		groups = append(groups, collisionGroup{
			GroupID:      "g_" + hex.EncodeToString(idBytes),
			Reason:       reason,
			MemberRowIDs: append([]int(nil), members...), // defensive copy
			DBMatch:      dbMatch,
		})
	}

	// Stable-ish ordering for determinism in tests: sort by the smallest
	// member row_id so the first collision group in the response is always
	// the one whose first row appears earliest in the sheet.
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].MemberRowIDs[0] < groups[j].MemberRowIDs[0]
	})

	return groups, nil
}

// validateImportField normalizes and validates one field of an import row for
// the PATCH endpoint. Returns the parsed canonical value (type varies by
// field: time.Time for date, string for description, float64 dollars for
// amount, bool for skip), plus an error code + user-facing message on
// failure. A non-empty errCode means the caller should write HTTP 400 with
// the error body {code, field, message}. An empty errCode means validation
// passed and normalized is safe to assign.
//
// Per-field rules (matches the spec's Validation field rules table):
//
//	date: non-empty, parseable via parseImportDate (both Excel serial and
//	      the text layouts), in [minImportYear, maxImportYear]. Empty or
//	      unparseable → INVALID_DATE. Reuses the exact parse path that
//	      handleImportUpload uses so the normalize-then-hash step lands on
//	      the same canonical date both at upload and PATCH time.
//
//	description: non-empty after TrimSpace, length ≤ MaxDescriptionLength
//	      (500, defined in limits.go). Trimmed string is returned as the
//	      normalized value — the caller assigns it directly to row.Description
//	      so subsequent re-hashes see the canonical form. The trim happens
//	      here (not only inside ComputeContentHash) because the stored row
//	      value is also what the frontend displays: we want "Starbucks" in
//	      the UI after a user types " Starbucks ", not the raw input.
//
//	amount: non-empty, parseable via parseImportAmount (strips currency
//	      formatting, rejects NaN/Inf, enforces MaxTransactionAmount
//	      magnitude). An empty amount at PATCH time is a HARD error
//	      (INVALID_AMOUNT) — this is the edit-mode parity case from test
//	      #9: upload silently coerces empty → 0 so the row lands in the
//	      preview (skipped from confirm as "zero_amount"), but PATCH
//	      does not get to silently zero a cell the user is actively
//	      editing. Returning 400 forces the frontend to surface an inline
//	      error so the user knows the edit did not take effect.
//
//	rate: the sheet's exchange rate for this row, as a string. An EMPTY
//	      string is legal and CLEARS the rate — it is the way back out of a
//	      rate typed by mistake, and the row returns to whatever its other
//	      cells say it is (#2 or #5). Anything else must parse as a finite
//	      POSITIVE number (parseImportRate); zero cannot divide and a
//	      negative would flip a purchase into a refund, so both are refused
//	      with INVALID_RATE rather than applied and flagged afterwards.
//	      The message is the same string the preview's rate flag carries, so
//	      the condition reads identically whichever direction the user met
//	      it from. A rate that parses but cannot APPLY — on the base
//	      currency, or with nothing to convert — is accepted here and judged
//	      by resolveImportMoney on the response, because that is a fact
//	      about the row rather than about the value.
//
//	skip: strict bool. Any non-bool JSON value → INVALID_FIELD. No
//	      normalization — the value is passed through untouched.
//
// Unknown field names → INVALID_FIELD with a message naming the field.
// This is the only path that returns INVALID_FIELD; every other failure
// has a field-specific code so the frontend can color-code the originating
// cell without parsing the message.
// importRateValue is validateImportField's normalized form for the rate field.
// A rate is two things at once on a row — the divisor and the cell it came
// from — and returning them as one value is what stops a caller assigning
// half of it. See importRow.RawRate.
type importRateValue struct {
	Rate float64
	Raw  string
}

func validateImportField(field string, value any) (normalized any, errCode string, message string) {
	switch field {
	case "date":
		s, ok := value.(string)
		if !ok {
			return nil, "INVALID_DATE", "date must be a string"
		}
		t, err := parseImportDate(s)
		if err != nil {
			return nil, "INVALID_DATE", "date is not parseable or out of range [1900, 2100]"
		}
		return t, "", ""

	case "description":
		s, ok := value.(string)
		if !ok {
			return nil, "INVALID_DESCRIPTION", "description must be a string"
		}
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return nil, "INVALID_DESCRIPTION", "description cannot be empty"
		}
		if charLen(trimmed) > MaxDescriptionLength {
			// Same string the preview's field_errors carry, so an over-long
			// description reads identically whether the user met it by
			// uploading, by editing, by resuming, or at confirm.
			return nil, "INVALID_DESCRIPTION", importFieldLengthMessage(importFieldDescription)
		}
		return trimmed, "", ""

	case "amount":
		s, ok := value.(string)
		if !ok {
			return nil, "INVALID_AMOUNT", "amount must be a string"
		}
		if strings.TrimSpace(s) == "" {
			return nil, "INVALID_AMOUNT", "amount cannot be empty"
		}
		cents, err := parseImportAmount(s)
		if err != nil {
			// A figure that parses but is too big gets its own sentence: the
			// preview's "use the computed amount" action PATCHes exactly this
			// value on a row whose derived amount is out of range, and the 400
			// lands in the cell in place of the row's real flag.
			if errors.Is(err, errImportAmountRange) {
				return nil, "INVALID_AMOUNT", importAmountOutOfRangeMessage()
			}
			return nil, "INVALID_AMOUNT", "amount is not a valid number"
		}
		// Return dollars so the caller can assign directly to row.Amount
		// (which is declared as float64 dollars, not int64 cents). The
		// ComputeContentHash caller inside buildCollisionGroups multiplies
		// back to cents via dollarsToCents(row.Amount), so the round-trip is
		// lossless — and sign-preserving in both directions — for the values
		// that parseImportAmount accepts (it already rejects magnitudes above
		// MaxTransactionAmount and NaN/Inf).
		return float64(cents) / 100.0, "", ""

	case importFieldRate:
		str, ok := value.(string)
		if !ok {
			return nil, "INVALID_RATE", "rate must be a string"
		}
		parsed, err := parseImportRate(str)
		// The accept predicate here is the resolver's BLOCK predicate, stated
		// once: a cell with something in it that cannot divide is refused.
		// parseImportRate alone is not enough. A cell holding only symbols or
		// whitespace — "$", "()", "  " — strips to nothing and parses as
		// ABSENCE, so it would be taken as a clear, landing a row whose rate
		// cell now reads empty beside a flag saying the rate is unusable. The
		// user would be looking at an empty cell being told to fix it. (A
		// lone "," is refused by the parser itself now, as a comma outside
		// grouping position; the symbols are what still need this.)
		if err != nil || (strings.TrimSpace(str) != "" && !importRateIsUsable(parsed)) {
			return nil, "INVALID_RATE", importRateInvalidMessage()
		}
		// Both halves travel together so the row cannot end up saying one
		// thing with its parsed rate and another with its raw cell — the pair
		// is what tells "no rate here" apart from "the rate here is wrong".
		// An empty string arrives as (0, ""), which is the cleared state.
		return importRateValue{Rate: parsed, Raw: strings.TrimSpace(str)}, "", ""

	case "skip":
		b, ok := value.(bool)
		if !ok {
			return nil, "INVALID_FIELD", "skip must be a boolean"
		}
		return b, "", ""
	}
	return nil, "INVALID_FIELD", fmt.Sprintf("unknown field: %q", field)
}

// importStore holds pending imports in memory with TTL-based expiry.
var importStore sync.Map

// importTTL is how long an import entry stays valid before expiry. A full
// hour is fixed (not activity-based) to avoid a memory-leak class where an
// idle tab holds a session alive forever.
const importTTL = 60 * time.Minute

// startCleanupOnce ensures the background cleanup goroutine starts only once.
var startCleanupOnce sync.Once

// startImportCleanup launches a background goroutine that removes expired
// import entries every 5 minutes.
func startImportCleanup() {
	startCleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				now := time.Now()
				importStore.Range(func(key, value any) bool {
					entry := value.(*importEntry)
					if now.Sub(entry.CreatedAt) > importTTL {
						importStore.Delete(key)
					}
					return true
				})
			}
		}()
	})
}

// columnMapping maps normalized header names to importRow field names.
var columnMapping = map[string]string{
	"date":              "date",
	"transaction date":  "date",
	"description":       "description",
	"amount":            "amount",
	"amount (usd)":      "amount",
	"category":          "category",
	"tags":              "tags",
	"notes":             "notes",
	"original amount":   "original_amount",
	"original currency": "original_currency",
	// The per-row exchange rate, in currencies.rate_to_base units. "Rate" is
	// what SpenDrop's own export writes, so an export re-imports losslessly;
	// the two aliases are what a hand-kept household sheet tends to call it.
	"rate":          "rate",
	"exchange rate": "rate",
	"fx rate":       "rate",
}

// dateFormats lists the date formats tried when parsing imported dates.
var dateFormats = []string{
	"2006-01-02",
	"01/02/2006",
	"1/2/2006",
	"02-Jan-2006",
	"2006/01/02",
}

// loadImportEntryForUser fetches an import entry from importStore, enforces
// ownership, and checks TTL expiry. On any failure it writes the
// appropriate HTTP error and returns ok=false — the caller must return
// immediately. Used by every handler that touches a specific import
// session (confirm, cancel, GET, PATCH) so the ownership/expiry contract
// lives in exactly one place.
func loadImportEntryForUser(w http.ResponseWriter, r *http.Request, importID string) (*importEntry, bool) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	if len(importID) != 32 {
		writeError(w, http.StatusBadRequest, "invalid import_id")
		return nil, false
	}
	val, found := importStore.Load(importID)
	if !found {
		writeError(w, http.StatusNotFound, "import not found or expired")
		return nil, false
	}
	entry := val.(*importEntry)
	if entry.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return nil, false
	}
	if time.Since(entry.CreatedAt) > importTTL {
		importStore.Delete(importID)
		writeError(w, http.StatusNotFound, "import not found or expired")
		return nil, false
	}
	return entry, true
}

// uniqueCategoriesFromRows returns the sorted-distinct Category values
// from a slice of importRows, skipping empties. Used by
// handleImportUpload and handleImportGetSession so both the initial
// upload response and the F5/resume response seed the category-mapping
// dropdowns from the same source. Case-insensitive dedup keyed on the
// lowercased name, but the returned slice preserves the first-seen
// casing of each category.
func uniqueCategoriesFromRows(rows []importRow) []string {
	seen := make(map[string]string)
	for _, row := range rows {
		cat := strings.TrimSpace(row.Category)
		if cat == "" {
			continue
		}
		key := strings.ToLower(cat)
		if _, ok := seen[key]; !ok {
			seen[key] = cat
		}
	}
	out := make([]string, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// Compile-time assertion that our row cap stays at or below excelize's own
// TotalRows limit. readImportSheetRows reports the library's ErrMaxRows using
// our row-cap message, which is only truthful while a sheet that trips the
// library's limit has necessarily passed ours. Raise MaxImportSheetRows above
// Excel's maximum row and this stops compiling rather than silently making
// that message a lie.
const _ = uint(excelize.TotalRows - MaxImportSheetRows)

// Sentinel errors for the workbook-shape caps. All three are surfaced to the
// caller verbatim as a 400 body, so they are written as advice rather than as
// diagnostics — a user who hits one needs to know what to change about the
// file. None interpolates any part of the upload.
//
// The wording of each has to describe what is actually counted, which is not
// always what a user would count. The row cap counts row SLOTS and the cell
// cap counts cells AFTER padding, so phrasing either in terms of visible
// content would send someone hunting for data that is not there. See the
// limits.go comment for why the counters are right and the naive phrasings
// were wrong.
var (
	errImportSheetTooManyRows = fmt.Errorf(
		"spreadsheet extends past row %d, which is beyond Excel's own maximum, so the file is malformed rather than merely large; re-save it from a spreadsheet application, or split it",
		MaxImportSheetRows)
	errImportSheetTooManyCells = fmt.Errorf(
		"spreadsheet is too wide once each row is padded out to its rightmost value (limit %d cells); a single value in a far-right column costs that row its full width, so clear anything to the right of your data, then split the file if it is still too large",
		MaxImportSheetCells)
	errImportArchiveTooLarge = fmt.Errorf(
		"spreadsheet expands to more than %d MiB when decompressed; split it into smaller files",
		MaxImportUnzippedBytes>>20)
	errImportCellTooLong = fmt.Errorf(
		"a single cell holds more than %d KiB of text, which is beyond what any spreadsheet cell can contain, so the file is malformed rather than merely large; re-save it from a spreadsheet application",
		MaxImportCellBytes>>10)
	errImportRowTooWide = fmt.Errorf(
		"a row declares more than %d cells, which is beyond what any spreadsheet can hold; re-save the file from a spreadsheet application",
		MaxImportCellsPerRow)
	errImportRowTooLarge = fmt.Errorf(
		"a single row holds more than %d MiB of text; split the sheet into smaller files",
		MaxImportRowBytes>>20)
	errImportTooManySharedStrings = fmt.Errorf(
		"spreadsheet declares more than %d distinct text values, which is more than its own cells could reference; re-save it from a spreadsheet application",
		MaxImportSheetCells)
	errImportPartTooLarge = fmt.Errorf(
		"one part of the spreadsheet expands to more than %d MiB on its own; split the sheet into smaller files",
		MaxImportPartBytes>>20)
	errImportNotAZipArchive = errors.New(
		"file is not a readable .xlsx workbook; export it as .xlsx (not .xls, and not password-protected) and try again")
	errImportSheetTooManyBytes = fmt.Errorf(
		"spreadsheet holds more than %d MiB of cell text; split it into smaller files",
		MaxImportSheetBytes>>20)
)

// checkImportArchiveSize rejects a workbook whose zip directory declares more
// decompressed bytes than MaxImportUnzippedBytes, BEFORE any of it is
// decompressed.
//
// This runs ahead of excelize.OpenReader because OpenReader inflates the whole
// archive up front — every entry, including parts the importer never reads —
// so by the time any of our other caps could look at the data, the allocation
// has already happened. Measured: a 2.00 MiB upload drove +10,244 MiB inside
// OpenReader alone.
//
// Reading the zip directory is cheap and does not decompress anything: the
// sizes are metadata.
//
// Acting on a declared size is safe in the direction that matters here.
// OVER-declaring is the dangerous direction, because excelize preallocates a
// buffer from the declared size before reading (lib.go, readFile), so a large
// declaration costs memory whether or not the entry can deliver it — which is
// exactly what this rejects. Under-declaring buys an attacker nothing, because
// Go's archive/zip stops a reader the moment it returns more bytes than its
// entry declared (reader.go, checksumReader.Read).
//
// Every size is compared and summed UNSIGNED, and every entry is bounded
// individually before it is added. archive/zip copies UncompressedSize64 out
// of the zip64 extra field without a range check, and FileInfo().Size()
// converts it to int64 unguarded, so an entry declaring 1<<63 reports a
// NEGATIVE size. A signed comparison then lets it past — and a signed running
// total goes so far negative that no later entry brings it back over any
// limit. A 161-byte archive defeated the signed version of this check
// entirely, and prefixing that entry to 200 MiB of honestly-declared parts
// defeated it too.
//
// Be precise about which half does the work, because the obvious reading is
// wrong. Reading the uint64 FIELD rather than Size() is NOT what holds:
// mutating both reads to uint64(entry.FileInfo().Size()) compiles and every
// test still passes, since uint64(int64(x)) round-trips the same bits. What
// holds is that the comparison operand is unsigned at all, and that a single
// oversized entry is rejected BEFORE the addition — which leaves the total at
// or below the limit on entry to each iteration, so it cannot wrap.
//
// That distinction cost three rounds to find: the mutation meant to prove this
// changed `var total uint64` to int64, which does not compile in a loop adding
// a uint64, and `FAIL [build failed]` greps identically to a real failure. It
// was reported as a passing mutation while proving nothing.
//
// Note that excelize's own UnzipSizeLimit — passed at the call site as a
// second layer — computes the same signed sum internally and has the same
// blind spot, so it does not back this check up for this input. It reaches
// make([]byte, 0, negative) and panics instead. That is another reason this
// check must be correct on its own rather than treated as belt-and-braces.
func checkImportArchiveSize(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// Refuse anything that is not a zip, rather than passing it through
		// for excelize to diagnose.
		//
		// This used to return nil on the reasoning that both sides call
		// archive/zip over the same bytes, so a reader we cannot open is one
		// excelize cannot open either. That is false, and dangerously so:
		// excelize sniffs an 8-byte OLE/CFB header first and, on a match,
		// runs its decryption path BEFORE any zip reader — where extractPart
		// does make([]byte, entry.Size) from an unvalidated CFB directory
		// field. An 8,704-byte upload declaring an 8 GiB stream allocated
		// 8,448 MiB; declaring 64 GiB aborted the process with a runtime
		// out-of-memory, which chi's Recoverer cannot catch. Neither this
		// check nor UnzipSizeLimit sees that path at all.
		//
		// A plain .xlsx is a zip. Encrypted and legacy .xls workbooks are not
		// supported input, so refusing them here costs nothing and keeps the
		// parser off every byte sequence we have not bounded.
		return errImportNotAZipArchive
	}
	// The PER-ENTRY bound is what makes a declared size safe to act on, and it
	// has to come before the addition.
	//
	// archive/zip copies UncompressedSize64 out of the zip64 extra field
	// unchecked, and FileInfo().Size() converts it to int64 without a range
	// check, so an entry declaring 1<<63 reports a NEGATIVE size. Left to
	// accumulate, one such entry drags a signed running total so far down that
	// nothing later recovers it, and two of them sum to exactly 2^64 and wrap
	// an unsigned one to zero. Bounding each entry first removes both: no
	// value large enough to misbehave ever reaches the sum.
	//
	// Note what is NOT load-bearing here, because an earlier version of this
	// comment claimed it was. total's unsignedness is not: uint64(int64(x))
	// round-trips the same bits, so the per-entry check refuses a 1<<63
	// declaration by VALUE whichever type it is read through, and with every
	// addend bounded the sum cannot wrap either way. uint64 is simply the type
	// the field already has.
	//
	// The per-entry bound is NOT redundant with the total, even though an
	// earlier version of it was and was deleted as dead code. That one used
	// the same limit as the total, so the total always caught it first. This
	// one is stricter, and it exists for a reason the total cannot serve: the
	// prescan reads one XML text node whole to measure it, and encoding/xml
	// grows its buffer by doubling before handing back a copy, so a part of
	// size P costs about 2P. Bounding the largest PART is what makes the
	// prescan's own peak statable; bounding only their sum does not, because
	// one part may be the whole sum.
	var total uint64
	for _, entry := range zr.File {
		if entry.UncompressedSize64 > MaxImportPartBytes {
			return errImportPartTooLarge
		}
		total += entry.UncompressedSize64
		if total > MaxImportUnzippedBytes {
			return errImportArchiveTooLarge
		}
	}
	return nil
}

// openImportWorkbook opens an uploaded workbook with the options this
// importer requires.
//
// It is a named function rather than an inline literal at the call site
// specifically so the options can be tested. They are a SECOND layer —
// checkImportArchiveSize rejects an oversized archive before this runs — and a
// backstop is invisible to every test for as long as the layer in front of it
// works. Deleting both options once left all nine packages green while a
// 1,049,793-byte upload peaked at 5,126 MiB and returned 200, so the claim that
// this call site stays bounded on its own needs a test that exercises it on its
// own. TestOpenImportWorkbook_BoundsDecompressionWithoutTheArchiveCheck is it.
//
// RawCellValue:true tells excelize to skip number-format rendering when
// returning cell values. Without it, a date cell with number format "mm-dd-yy"
// renders as "07-21-25", which matches none of the fallback text formats in
// parseImportDate and silently drops the row. With RawCellValue:true, date
// cells return their underlying Excel serial number (e.g. "45859"), which
// parseImportDate converts via excelize.ExcelDateToTime — format-agnostic.
//
// UnzipSizeLimit defaults to 16 GB, which is what made a zip bomb reachable
// here in the first place. UnzipXMLSizeLimit is the temp-file staging
// threshold, pinned to our own constant so a change to excelize's default
// cannot silently alter how much of a worksheet is held in memory.
func openImportWorkbook(data []byte) (*excelize.File, error) {
	return excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		RawCellValue:      true,
		UnzipSizeLimit:    MaxImportUnzippedBytes,
		UnzipXMLSizeLimit: MaxImportUnzippedPartBytes,
	})
}

// prescanImportWorkbook bounds how large a single Rows.Columns() call can get,
// and does it before excelize materialises anything.
//
// This exists because every other cap in this file is evaluated on the slice
// Columns() already returned, and so can only report an allocation rather than
// prevent one. Columns() builds a whole row at once, and the row's cost is the
// sum over its cells of the string each one resolves to. excelize bounds
// neither factor: a cell without an `r` attribute never reaches
// CellNameToCoordinates, so cells-per-row is unbounded, and a shared string is
// rebuilt per referencing cell at whatever length the table declares.
//
// WHY THIS SUMS PER ROW RATHER THAN CAPPING COLUMNS. The obvious cheaper design
// — cap cells-per-row, cap bytes-per-cell, call the product the peak — was
// implemented first and is wrong for this application. It forces the two caps
// low enough that their product is acceptable, and a cells-per-row cap low
// enough to matter (1,024 at a 32 KiB cell) refuses files real spreadsheet
// applications produce: Google Sheets allows 18,278 columns and 50,000
// characters per cell, and this household imports Google Sheets exports every
// month. Summing the row's actual projected bytes bounds exactly the quantity
// that allocates, so the individual limits can each sit far above anything a
// spreadsheet can emit while the peak stays bounded. False rejections here cost
// a working workflow; tightness buys nothing beyond the bound.
//
// Resolving shared-string references is what makes that possible, so this is
// not purely structural: it reads the shared-string table's lengths (not its
// contents) and adds the right one per reference. It still builds no rows,
// keeps no strings, and interprets no values — a cell's TYPE only selects which
// length to add.
//
// Cost is one extra streaming decompression of the shared-string table and each
// worksheet, plus one int per distinct shared string. Measured at 0 MiB of
// allocation rejecting payloads that drove excelize to 1,258 MiB.
func prescanImportWorkbook(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errImportNotAZipArchive
	}

	sharedLens, err := prescanSharedStrings(zr)
	if err != nil {
		return err
	}
	for _, zf := range zr.File {
		if strings.HasPrefix(strings.ToLower(zf.Name), "xl/worksheets/sheet") {
			if err := prescanSheet(zf, sharedLens); err != nil {
				return err
			}
		}
	}
	return nil
}

// prescanSharedStrings returns the text length of every <si> in the workbook's
// shared-string table, indexed as the sheets reference them. An <si> may be
// split across several <r> runs, so the lengths accumulate until the item ends.
//
// It deliberately does NOT reject an over-LONG item here, even though that
// looks like the natural place for it. An entry costs memory only when a cell
// references it, and prescanSheet adds its length at every reference — so an
// item long enough to matter is refused there, and one nothing references
// costs nothing beyond the archive-bounded load that already happened.
//
// It DOES reject an over-MANY table, because that cost is this function's own:
// the returned slice holds one int per item whether or not any cell refers to
// it. The two are not inconsistent, though they look it — one bounds a cost
// paid elsewhere and so not paid here, the other bounds a cost paid right
// here. Both are tested.
func prescanSharedStrings(zr *zip.Reader) ([]int, error) {
	var entry *zip.File
	for _, zf := range zr.File {
		if strings.EqualFold(zf.Name, "xl/sharedStrings.xml") {
			entry = zf
			break
		}
	}
	if entry == nil {
		return nil, nil // inline-strings-only workbook
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, nil // let excelize report it
	}
	defer rc.Close()

	lengths := []int{}
	decoder := xml.NewDecoder(rc)
	current, inItem, inText := 0, false, false
	for {
		token, err := decoder.Token()
		if err != nil {
			// EOF, or malformed XML which is excelize's to report.
			return lengths, nil
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "si":
				current, inItem = 0, true
			case "t":
				inText = true
			}
		case xml.CharData:
			if inItem && inText {
				current += len(element)
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "t":
				inText = false
			case "si":
				inItem = false
				lengths = append(lengths, current)
				if len(lengths) > MaxImportSheetCells {
					return nil, errImportTooManySharedStrings
				}
			}
		}
	}
}

// prescanSheet walks one worksheet and rejects any row whose cells would
// together materialise more than MaxImportRowBytes, or which declares more
// cells than MaxImportCellsPerRow.
//
// The cell count is bounded separately from the bytes because empty cells cost
// nothing in text but still cost a slice slot: excelize pads a row out to the
// column index of its rightmost value, so a run of empty cells followed by one
// value allocates a header per skipped column.
func prescanSheet(zf *zip.File, sharedLens []int) error {
	rc, err := zf.Open()
	if err != nil {
		return nil // let excelize report it
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var (
		rowBytes, cellsThisRow int
		cellIsShared, inValue  bool
		cellBytes              int
	)
	addCellBytes := func(n int) error {
		cellBytes += n
		if cellBytes > MaxImportCellBytes {
			return errImportCellTooLong
		}
		rowBytes += n
		if rowBytes > MaxImportRowBytes {
			return errImportRowTooLarge
		}
		return nil
	}

	for {
		token, err := decoder.Token()
		if err != nil {
			return nil // EOF, or malformed XML for excelize to report
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "row":
				rowBytes, cellsThisRow = 0, 0
			case "c":
				cellBytes = 0
				cellIsShared = false
				for _, attr := range element.Attr {
					if attr.Name.Local == "t" && attr.Value == "s" {
						cellIsShared = true
					}
				}
				cellsThisRow++
				if cellsThisRow > MaxImportCellsPerRow {
					return errImportRowTooWide
				}
			case "v", "t":
				inValue = true
			}
		case xml.CharData:
			if !inValue {
				continue
			}
			if cellIsShared {
				// A shared reference costs the length of the item it points
				// at, once per reference. An unparseable or out-of-range
				// index is excelize's to reject; it cannot cost memory here.
				index, convErr := strconv.Atoi(strings.TrimSpace(string(element)))
				if convErr != nil || index < 0 || index >= len(sharedLens) {
					continue
				}
				if err := addCellBytes(sharedLens[index]); err != nil {
					return err
				}
				continue
			}
			if err := addCellBytes(len(element)); err != nil {
				return err
			}
		case xml.EndElement:
			if element.Name.Local == "v" || element.Name.Local == "t" {
				inValue = false
			}
		}
	}
}

// readImportSheetRows reads a worksheet into the same [][]string shape that
// excelize's File.GetRows returns, but stops at MaxImportSheetRows scanned row
// slots and MaxImportSheetCells materialised cells. It replaces the GetRows
// call this handler used to make, which is unbounded on both axes and lets a
// few KB of upload direct the parser into an indefinite CPU spin or a
// multi-gigabyte allocation. See the cap declarations in limits.go for the
// measurements and for why the upstream v2.11.0 guard does not cover the
// reachable shape.
//
// The body deliberately mirrors GetRows statement for statement — the blank
// run padding, the trailing `results[:maxVal]` trim, and the break-on-error
// are all reproduced — so that swapping it in changes only what the caps
// reject and nothing about how a legitimate file parses. Columns() is called
// with no options for the same reason: it inherits RawCellValue from the
// options passed to OpenReader, exactly as GetRows did.
//
// The budgets below are checked after Columns() returns, because that is the
// only place they can be: Columns() materialises a whole row before yielding
// it. They therefore bound TOTALS, not the peak of any one row — the peak is
// bounded ahead of them by prescanImportWorkbook, and it has to be, because a
// single row costs (cells in the row) x (bytes per cell) and both factors are
// attacker-chosen.
//
// This comment used to price a pathological row at "≈262 KB at Excel's
// 16384-column maximum". That was wrong twice over and is the reason the
// post-hoc placement looked safe: it counted only the 16-byte string headers
// and none of the content those headers point at, and 16,384 is not a ceiling
// on this path at all. Priced properly the same row is 512 MiB, and measured
// at that shape, 514.4 MiB.
func readImportSheetRows(f *excelize.File, sheet string) ([][]string, error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results, cur, maxVal, cells, textBytes := make([][]string, 0, 64), 0, 0, 0, 0
	for rows.Next() {
		cur++
		if cur > MaxImportSheetRows {
			return nil, errImportSheetTooManyRows
		}
		row, err := rows.Columns()
		if err != nil {
			break
		}
		if len(row) > 0 {
			cells += len(row)
			if cells > MaxImportSheetCells {
				return nil, errImportSheetTooManyCells
			}
			// Bound the bytes, not only the geometry. Counting cells says
			// nothing about how much memory they hold, and excelize rebuilds a
			// shared string per referencing cell, so a handful of cells can
			// carry gigabytes. Both bounds are needed: the per-cell one
			// catches a single enormous string, the running total catches many
			// merely-large ones. See the limits.go comment for the
			// measurements.
			// Per-cell and per-row limits are enforced by
			// prescanImportWorkbook, before this row was built — a check here
			// could only report a cell that had already been allocated. What
			// remains here is the SHEET total, which the prescan does not
			// accumulate because it bounds each row independently.
			for _, value := range row {
				textBytes += len(value)
			}
			if textBytes > MaxImportSheetBytes {
				return nil, errImportSheetTooManyBytes
			}
			if emptyRows := cur - maxVal - 1; emptyRows > 0 {
				results = append(results, make([][]string, emptyRows)...)
			}
			results = append(results, row)
			maxVal = cur
		}
	}
	// Read the iterator's deferred error directly instead of via Close().
	// Close() only reports it when the sheet was parsed in memory; once the
	// decompressed XML crosses excelize's UnzipXMLSizeLimit the worksheet is
	// staged in a temp file and Close() returns that file's close error
	// instead, swallowing ErrMaxRows on precisely the large inputs where it
	// is most likely to fire.
	if err := rows.Error(); err != nil {
		// excelize's own row-limit rejection is the same complaint as ours,
		// so it gets the same advice rather than a generic parse failure the
		// user can do nothing with. The two limits are now EQUAL — not ours
		// below theirs — and the message stays truthful either way, because a
		// sheet the library refuses has reached the same row number ours names.
		if errors.Is(err, excelize.ErrMaxRows) {
			return nil, errImportSheetTooManyRows
		}
		return nil, err
	}
	return results[:maxVal], nil
}

// handleImportUpload accepts a multipart xlsx file upload, parses it, stores
// the rows in memory, and returns a preview.
func (h *Handler) handleImportUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Limit upload size via MAX_UPLOAD_BYTES (default 10 MiB).
	r.Body = http.MaxBytesReader(w, r.Body, getMaxUploadBytes())

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing or invalid file upload")
		return
	}
	defer file.Close()

	// Buffer the upload so the archive can be sized before any of it is
	// decompressed. MaxBytesReader above already bounds this read to
	// MAX_UPLOAD_BYTES, and excelize.OpenReader does io.ReadAll internally
	// anyway, so nothing is held that would not have been held regardless.
	data, err := io.ReadAll(file)
	if err != nil {
		// The likely cause is MaxBytesReader tripping, which is the client's
		// problem, not ours.
		writeError(w, http.StatusBadRequest, "failed to read uploaded file")
		return
	}

	// Bound the decompressed size FIRST. Every other cap in this handler runs
	// against already-parsed data, and OpenReader inflates the entire archive
	// before returning, so this is the only place a zip bomb can be caught.
	if err := checkImportArchiveSize(data); err != nil {
		log.Printf("import: rejected archive: %v", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Bound the peak of a single row before excelize can build one. Everything
	// after this point only observes allocations; this is the last place they
	// can still be prevented. See prescanImportWorkbook.
	if err := prescanImportWorkbook(data); err != nil {
		log.Printf("import: rejected workbook shape: %v", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Options, and why each one is required, live on openImportWorkbook.
	f, err := openImportWorkbook(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse xlsx file")
		return
	}
	defer f.Close()

	// Find sheet: prefer "Transactions", fall back to first sheet
	sheetName := "Transactions"
	sheetIdx, err := f.GetSheetIndex(sheetName)
	if err != nil || sheetIdx == -1 {
		sheetName = f.GetSheetName(0)
		if sheetName == "" {
			writeError(w, http.StatusBadRequest, "xlsx file has no sheets")
			return
		}
	}

	rows, err := readImportSheetRows(f, sheetName)
	if err != nil {
		log.Printf("import: failed to read sheet %q: %v", sanitizeLogValue(sheetName), err)
		// A file rejected for its shape gets the specific reason, so the
		// user can act on it. Every other read failure stays generic —
		// excelize's parse errors quote fragments of the uploaded XML.
		if errors.Is(err, errImportSheetTooManyRows) ||
			errors.Is(err, errImportSheetTooManyCells) ||
			errors.Is(err, errImportSheetTooManyBytes) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "failed to read spreadsheet data")
		return
	}

	if len(rows) < 2 {
		writeError(w, http.StatusBadRequest, "sheet must have a header row and at least one data row")
		return
	}

	// Scan for the header row: find the first row that names a date, a
	// description, and MONEY.
	//
	// Money is satisfied by `amount` OR `original amount`, and the OR is the
	// whole reason a back-dated foreign statement can be imported at all. Such
	// a sheet states its money in LBP and quotes the rate it was booked at; it
	// has no USD column, because the USD figure is what SpenDrop is being
	// asked to work out. Demanding `amount` refused that file at the door —
	// before any row reached the resolver that exists to price it — and the
	// user's only way in was to add a column of numbers they would have had to
	// compute themselves, at which point the rate would have been decorative.
	//
	// It stays a REQUIREMENT rather than becoming optional: a file with no
	// money column at all is a file this endpoint cannot do anything with, and
	// saying so once about the sheet beats saying it per row about every row
	// in it. A row with no money inside an otherwise fine sheet is a different
	// thing, and is answered per row by the matrix.
	headerIdx := -1
	for i, row := range rows {
		hasDate, hasDesc, hasMoney := false, false, false
		for _, cell := range row {
			normalized := strings.ToLower(strings.TrimSpace(cell))
			if field, found := columnMapping[normalized]; found {
				switch field {
				case "date":
					hasDate = true
				case "description":
					hasDesc = true
				case "amount", "original_amount":
					hasMoney = true
				}
			}
		}
		if hasDate && hasDesc && hasMoney {
			headerIdx = i
			break
		}
	}

	if headerIdx == -1 {
		// Both acceptable spellings are named. "missing amount" would send the
		// owner of a foreign-only sheet off to add a column they do not need.
		writeError(w, http.StatusBadRequest, "missing required columns: date, description, and either amount or original amount")
		return
	}

	// Parse header row into column sections. When a field name appears
	// again we start a new section (handles side-by-side Expenses/Income).
	type colSection struct {
		colIndexToField map[int]string
	}
	var sections []colSection
	currentSeen := make(map[string]bool)
	currentSection := colSection{colIndexToField: make(map[int]string)}

	headerRow := rows[headerIdx]
	detectedColumns := make([]string, 0, len(headerRow))
	for i, cell := range headerRow {
		normalized := strings.ToLower(strings.TrimSpace(cell))
		field, found := columnMapping[normalized]
		if !found {
			continue
		}
		if currentSeen[field] {
			// Duplicate field — start a new section
			sections = append(sections, currentSection)
			currentSeen = make(map[string]bool)
			currentSection = colSection{colIndexToField: make(map[int]string)}
		}
		currentSeen[field] = true
		currentSection.colIndexToField[i] = field
		detectedColumns = append(detectedColumns, cell)
	}
	sections = append(sections, currentSection)

	// Parse data rows from all sections
	// Size the preallocation off what MaxImportRows can actually admit, not off
	// the sheet's geometry. importRow is 128 bytes, so a bare
	// len(rows)-headerIdx-1 reserves 128 MiB for a sheet whose last row element
	// sits at Excel's maximum — from a 2 KB file, on a path that RETURNS 200
	// and so has no error to push back with. Raising MaxImportSheetRows to
	// Excel's maximum is what made that reachable; before it, the same file was
	// refused at slot 100,001. MaxImportRows is not consulted until after this
	// loop, so the clamp has to happen here.
	//
	// Clamping only affects the initial capacity. A sheet with side-by-side
	// sections can still legitimately produce more rows than it has lines, and
	// append grows to fit; the over-cap case is rejected a few statements later
	// regardless.
	prealloc := len(rows) - headerIdx - 1
	if prealloc > MaxImportRows {
		prealloc = MaxImportRows
	}
	parsedRows := make([]importRow, 0, prealloc)
	for _, row := range rows[headerIdx+1:] {
		for _, sec := range sections {
			ir := importRow{}
			hasAnyValue := false
			for colIdx, field := range sec.colIndexToField {
				val := ""
				if colIdx < len(row) {
					val = strings.TrimSpace(row[colIdx])
				}
				if val != "" {
					hasAnyValue = true
				}
				switch field {
				case "date":
					// Canonicalize to ISO (YYYY-MM-DD) at parse time so
					// the preview response — and every downstream path
					// that reads ir.Date — sees the same string. xlsx
					// date cells arrive as Excel serials (e.g. "45689")
					// under RawCellValue:true; without this step the
					// preview table would leak raw serials to the
					// frontend. Mirrors the PATCH handler's
					// parseImportDate + Format canonicalization so an
					// uploaded "45689" and a later PATCH of "2025-02-27"
					// produce the same stored string (and the same
					// content hash). On parse failure we fall back to
					// the raw value — the downstream confirm loop
					// re-runs parseImportDate and will skip the row
					// with a typed error if it still can't parse.
					if parsed, err := parseImportDate(val); err == nil {
						ir.Date = parsed.Format("2006-01-02")
					} else {
						ir.Date = val
					}
				case "description":
					ir.Description = val
				case "amount":
					// The raw cell is kept whatever the parse does with it, so
					// a value nobody can read stays distinguishable from an
					// empty cell — see importRow.RawAmount.
					ir.RawAmount = val
					if val != "" {
						// parseImportAmount normalizes to cents and
						// rejects NaN/Inf/out-of-range values; on any
						// error ir.Amount stays zero and the row is
						// skipped downstream as zero-amount — same
						// bucket as the pre-extraction silent-fallthrough
						// behavior for unparseable inputs. The round
						// trip through centsToDollars is lossless for
						// any legal value and normalizes 3+ decimal
						// inputs to the cents grid that the DB and
						// downstream content hash both use.
						if cents, err := parseImportAmount(val); err == nil {
							ir.Amount = centsToDollars(cents)
						}
					}
				case "category":
					ir.Category = val
				case "tags":
					ir.Tags = val
				case "notes":
					ir.Notes = val
				case "original_amount":
					// The raw cell is kept whatever happens to the parse, so a
					// figure the ledger cannot hold stays distinguishable from
					// an empty cell — see importRow.RawOriginalAmount.
					ir.RawOriginalAmount = val
					if val != "" {
						if cents, err := parseImportAmount(val); err == nil {
							ir.OriginalAmount = centsToDollars(cents)
						}
					}
				case "original_currency":
					ir.OriginalCurrency = val
				case "rate":
					// Both halves are kept. Rate is the usable divisor (0 when
					// the cell is empty OR unparseable); RawRate is what the
					// cell held, which is the only thing that tells the two
					// apart downstream. Unlike the amount cells, an
					// unparseable rate does NOT vanish into a silent zero —
					// resolveImportMoney flags it, because a rate the user
					// typed and got wrong is a fault worth reporting.
					ir.RawRate = val
					if parsed, err := parseImportRate(val); err == nil {
						ir.Rate = parsed
					}
				}
			}
			// Skip rows where no mapped cell had any value
			if !hasAnyValue {
				continue
			}
			ir.RowID = len(parsedRows)
			parsedRows = append(parsedRows, ir)
		}
	}

	if len(parsedRows) == 0 {
		writeError(w, http.StatusBadRequest, "no data rows found")
		return
	}

	if len(parsedRows) > MaxImportRows {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many rows (max %d)", MaxImportRows))
		return
	}

	// Generate import ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate import ID")
		return
	}
	importID := hex.EncodeToString(idBytes)

	// Limit pending imports per user (max 3 concurrent). The check is
	// advisory, not a security invariant — there is a TOCTOU window
	// between the Range count and the Store below, so two concurrent
	// uploads from the same user at userPending=2 can both pass the gate
	// and end up with 4 pending slots. Tightening the race would require
	// a per-user mutex, which is overkill for a cap whose purpose is
	// memory-pressure back-pressure, not per-user fairness. A user who
	// beats the race by one slot is within 33% of the intended limit
	// and the oldest entries still expire via the TTL reaper.
	userPending := 0
	importStore.Range(func(key, value any) bool {
		entry := value.(*importEntry)
		if entry.UserID == user.ID {
			userPending++
		}
		return userPending < 3
	})
	if userPending >= 3 {
		writeError(w, http.StatusTooManyRequests, "too many pending imports, confirm or wait for existing ones to expire")
		return
	}

	// Store in memory
	entry := &importEntry{
		UserID:    user.ID,
		Rows:      parsedRows,
		Columns:   detectedColumns,
		CreatedAt: time.Now(),
	}
	importStore.Store(importID, entry)

	// Categories are loaded here so the preview's hash formula uses the same
	// canonical name resolution as the confirm path.
	existingCats, err := h.queries.ListAllCategories(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	catIDToName := make(map[int64]string, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
		catIDToName[c.ID] = c.Name
	}

	// The entry is already in the store, so another tab could PATCH it
	// between the Store above and the read below. Held for the same reason
	// the PATCH and GET handlers hold it: the rows and the collision groups
	// in one response have to describe one snapshot.
	entry.mu.Lock()
	preview, err := h.buildImportPreview(r.Context(), importID, entry, catNameToID, catIDToName)
	entry.mu.Unlock()
	if err != nil {
		log.Printf("import upload: build preview: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to build import preview")
		return
	}

	// Start background cleanup (idempotent via sync.Once)
	startImportCleanup()

	writeJSON(w, http.StatusOK, preview)
}

// importConfirmRequest is the JSON body for confirming an import.
type importConfirmRequest struct {
	ImportID          string           `json:"import_id"`
	DefaultCategoryID int64            `json:"default_category_id"`
	CategoryMap       map[string]int64 `json:"category_map"`
}

// importSkipReason enumerates the closed set of reasons a row can be
// rejected at user-data level during handleImportConfirm. The set is
// intentionally small and stable — Phase 3.5 property tests in
// `import_handlers_property_test.go` assert that every row in
// importResult.Skipped carries one of these reasons, so adding a new
// rejection branch to processImportRows without also adding a reason
// constant here will fail the "no silent drops" property and surface
// the regression loudly instead of letting a row vanish from the count.
//
// The reasons are lowercase_snake_case to match the JSON wire format
// used by collisionGroup on the upload preview path, so a frontend that
// renders both preview skips and confirm-time outcomes can match
// strings byte-for-byte without a translation table.
type importSkipReason string

const (
	skipReasonEmptyDescription importSkipReason = "empty_description"
	skipReasonZeroAmount       importSkipReason = "zero_amount"
	skipReasonUnparseableDate  importSkipReason = "unparseable_date"
	skipReasonMissingCategory  importSkipReason = "missing_category"
	skipReasonDuplicate        importSkipReason = "duplicate"
	skipReasonFieldTooLong     importSkipReason = "field_too_long"
	// skipReasonSignMismatch names a row whose base amount and original
	// (foreign-currency) amount point in opposite directions — a $42.50
	// charge against a -150,000 LBP original, or the reverse. B10 made the
	// sign meaningful (a negative amount is a refund), so the pair now
	// describes a contradiction: one side says money left, the other says it
	// came back. Skipped rather than reconciled — the file is the only
	// evidence of what the user meant, and picking a side would silently turn
	// a refund into a purchase.
	skipReasonSignMismatch importSkipReason = "sign_mismatch"

	// The money family. Every one of these is a LAST-DITCH label: each
	// condition blocks on all four preview surfaces and refuses confirm with
	// a 409 MONEY_ERRORS long before processImportRows sees the row, exactly
	// as field_too_long does. They exist so that a session which reaches the
	// insert with a stale flag — a currency deleted between the gate and the
	// commit, a client that never read the preview — skips the row with a
	// name on it instead of storing money the preview could not resolve.
	//
	// They are separate reasons rather than one "money" bucket because they
	// carry different fixes: a missing rate is typed into the preview, an
	// invalid one is corrected there, an unknown currency is added in
	// Settings, and a disagreeing amount is decided in the spreadsheet. A
	// single label would tell an operator reading skipped_reasons that
	// something about money went wrong and nothing about what.
	skipReasonRateMissing         importSkipReason = "rate_missing"
	skipReasonRateInvalid         importSkipReason = "rate_invalid"
	skipReasonRateOnBase          importSkipReason = "rate_on_base"
	skipReasonRateWithoutCurrency importSkipReason = "rate_without_currency"
	skipReasonUnknownCurrency     importSkipReason = "unknown_currency"
	skipReasonAmountDisagrees     importSkipReason = "amount_disagrees"
	skipReasonAmountInvalid       importSkipReason = "amount_invalid"
)

// importInserted records a row that made it into the transactions table.
// RowIndex is the 0-based position in the preview so properties can
// assert one-to-one correspondence between input rows and outcomes.
// Date and AmountCents carry forward the normalized values stored in
// the transactions row so Phase 3.5 properties (`TestImportProperty_DateSanity`,
// `TestImportProperty_AmountSanity`) can assert contract-level
// invariants without re-querying the database.
type importInserted struct {
	RowIndex    int
	Date        time.Time
	AmountCents int64
	// CategoryID carries the resolved category of the inserted row so the
	// confirm handler can derive the (category, month) cell for the
	// post-commit over-budget alert hook (Phase C, Task 17) without
	// re-querying.
	CategoryID int64
}

// importSkipped records a user-data-level rejection (bad date, empty
// description, duplicate content hash, etc.). Reason is drawn from the
// closed importSkipReason set — no free-form strings — so the "every
// skipped row names its reason" property can be a simple set membership
// test.
type importSkipped struct {
	RowIndex int
	Reason   importSkipReason
}

// importErrored records a system-level failure during row processing
// (DB lookup error, insert error, unknown category_id). Unlike
// importSkipped, Reason is free-form because the source is an
// environmental fault rather than a policy decision. Properties only
// assert that every row lands in exactly one of the three buckets;
// they do not test the shape of Reason for errored rows.
//
// Reason is scrubbed via sanitizeLogValue at the point of capture so
// control characters from a pathological underlying error (a corrupt
// DSN, a SQLite error carrying embedded terminal escapes) cannot
// reach a downstream consumer verbatim. Today the field is never
// serialized — the HTTP handler folds the count into `skipped` — but
// a future operator surface that surfaces per-row errors on
// `/healthz/data` or an audit-log row would otherwise inherit a
// log-injection / XSS hazard without any code change at that surface.
// Sanitizing here means future callers can format the string into
// any sink without re-checking its provenance.
type importErrored struct {
	RowIndex int
	Reason   string
}

// importResult is the structured outcome of processImportRows. The
// conservation invariant — `len(Inserted) + len(Skipped) + len(Errored)
// == len(input.Rows)` — is enforced by the loop structure:
// every iteration appends to exactly one of the three slices and then
// continues. The property test for conservation fuzzes this with
// randomized input mixes so a future refactor that accidentally drops a
// row without accounting for it fails loudly.
type importResult struct {
	Inserted []importInserted
	Skipped  []importSkipped
	Errored  []importErrored
}

// importProcessInput bundles the already-resolved inputs that
// processImportRows needs. The HTTP handler builds this once from the
// incoming importConfirmRequest and the categories table, and passes it
// by value because it's a handful of slices/maps and the processor only
// reads from them. The refactored boundary keeps auth, JSON, SQL
// transaction lifecycle, and the category-load query out of the hot
// loop so property tests can observe row outcomes without standing up
// an HTTP round-trip.
type importProcessInput struct {
	UserID            int64
	Rows              []importRow
	CategoryMap       map[string]int64
	DefaultCategoryID int64
	CatNameToID       map[string]int64
	CatIDToName       map[int64]string
	// Currencies is the snapshot every row's money is resolved against. It is
	// taken ONCE, by the handler, inside the insert transaction — so a rate
	// edited midway through a large import cannot split the batch across two
	// views of the table, and the snapshot the rows commit against is the one
	// the transaction can see.
	Currencies importCurrencies
}

// handleImportConfirm inserts all rows from a previously uploaded import
// into the transactions table.
func (h *Handler) handleImportConfirm(w http.ResponseWriter, r *http.Request) {
	var req importConfirmRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	entry, ok := loadImportEntryForUser(w, r, req.ImportID)
	if !ok {
		return
	}

	// Build category name-to-ID and id-to-name lookups from existing
	// categories. The id-to-name map feeds the Phase 3.4 content-hash
	// formula — the hash is computed from the DB category name, not
	// the raw spreadsheet cell, so that the backfill path (which
	// JOINs categories via category_id) and the import path agree on
	// the bytes hashed for any given row.
	existingCats, err := h.queries.ListAllCategories(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	catIDToName := make(map[int64]string, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
		catIDToName[c.ID] = c.Name
	}

	// Phase 3.4b: re-run buildCollisionGroups against the current
	// session rows with the confirm-time category choices applied.
	// This is the all-or-nothing gate — if ANY non-skipped row is
	// still a member of a collision group (intra_file or db_match)
	// after the user has finished editing, the entire import is
	// rejected with 409 and the full groups array, and the session
	// state is left untouched so the frontend can re-render the same
	// preview with updated hints.
	//
	// Unlike upload and PATCH (which pass nil/0 for categoryMap and
	// defaultCategoryID), confirm passes the user's chosen CategoryMap
	// and DefaultCategoryID. This covers the category-resolution-only
	// collision case: a row whose category cell was empty (and now
	// resolves to the user's default) could produce a hash that
	// matches a live DB row that wouldn't have matched at upload
	// time. Re-checking at confirm with the real resolved categories
	// is how we catch it.
	//
	// buildCollisionGroups already excludes Skip==true rows from
	// grouping (see Chunk 1 Task 4 — the `if row.Skip { continue }`
	// guard is first in the loop), so `len(groups) > 0` is equivalent
	// to "at least one non-skipped row is still colliding". No separate
	// non-skipped filter is needed here.
	//
	// Serialize entry.Rows access across concurrent handlers — a PATCH
	// from another tab could mutate entry.Rows mid-read otherwise. Hold
	// the mutex only until we've computed groups and the filtered copy;
	// the SQL transaction that follows runs on the local filteredRows
	// slice, so there is no need to keep entry locked during DB inserts.
	// The gate's view of the currencies table. It is deliberately a DIFFERENT
	// snapshot from the one the insert loop uses: this one answers "is the
	// preview still resolvable?" before any transaction is open, and the
	// insert takes its own inside the tx. If an admin edits or deletes a
	// currency between the two, the gate passes and the affected rows are
	// skipped by name at insert — which is exactly what the last-ditch money
	// reasons exist for. Sharing one snapshot across both would instead let
	// the batch commit against a table state its own transaction never saw.
	gateCurrencies, err := loadImportCurrencies(r.Context(), h.queries)
	if err != nil {
		log.Printf("import confirm: load currencies: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load currencies")
		return
	}

	entry.mu.Lock()

	// Field lengths are re-checked here, ahead of the collision rebuild,
	// because the preview is only a preview: the rows may have been edited by
	// PATCH since upload, and nothing stops a client calling confirm without
	// ever having read the flags. This is the gate; the upload-time list is a
	// courtesy that lets the user fix things before they get here.
	//
	// Ordering against the collision check is deliberate rather than
	// incidental. A row can be both too long and colliding, and a user who
	// resolves the collision by editing a description only to be told the
	// description is too long has been sent round the loop twice. Reporting
	// length first means one round trip surfaces the problem that a content
	// edit cannot accidentally introduce.
	if fieldErrors := checkImportRowLengths(entry.Rows); len(fieldErrors) > 0 {
		entry.mu.Unlock()
		// Same body shape as UNRESOLVED_COLLISIONS below — a machine-readable
		// code plus the full list — so a frontend's existing 409 handling
		// extends to this rather than being replaced.
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":         "FIELD_TOO_LONG",
			"field_errors": fieldErrors,
		})
		return
	}

	// The money gate, and the same argument as the length gate above it: the
	// preview is a courtesy, a client can POST straight here, and a session
	// can have been edited since it was last read. Without this, a row the
	// preview refused to resolve would reach the insert loop and be dropped
	// there as one of the last-ditch money reasons — a row silently missing
	// from a "47 imported, 3 skipped" count instead of a refusal naming the
	// three rows and what is wrong with each.
	//
	// It sits AFTER length and BEFORE the category gates. Length keeps its
	// place for the reason it always has. Money comes next because it is the
	// other family whose remedy is an edit to the row itself, and because
	// nothing further down can change the answer: a category decision cannot
	// make a rate appear, while the collision view below is a FUNCTION of the
	// resolved money — a row whose cents cannot be computed has no identity to
	// collide with.
	//
	// MONEY_ERRORS rather than reusing FIELD_TOO_LONG: the two families share
	// the field_errors array shape but not their remedies, and the frontend
	// counts and renders them apart. FIELD_TOO_LONG's body stays byte
	// identical for the condition it has always named.
	if moneyErrors := importMoneyFieldErrors(entry.Rows, gateCurrencies); len(moneyErrors) > 0 {
		entry.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":         "MONEY_ERRORS",
			"field_errors": moneyErrors,
		})
		return
	}

	// An id the categories table does not have cannot be a choice the user
	// made. Left alone, every row it covers lands in processImportRows'
	// Errored bucket and is folded into `skipped` with nothing saying why —
	// a whole import reported as "0 imported, 500 skipped". Rejected here
	// with the offending ids named instead.
	if unknown := unknownCategoryIDs(req, catIDToName); len(unknown) > 0 {
		entry.mu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":                 "UNKNOWN_CATEGORY",
			"unknown_category_ids": unknown,
			"error":                fmt.Sprintf("no such category: %v", unknown),
		})
		return
	}

	// The category gate. This is the gate, in the same sense the length
	// check above is: the preview lists what needs deciding as a courtesy,
	// but a client can POST straight here, and a preview is only a preview.
	//
	// It sits AFTER length and BEFORE collisions, and neither position is
	// incidental.
	//
	// Length stays first for the reason it always has: it is the only check
	// whose remedy is an edit to a row's content, and being told to shorten
	// a description only after editing it to resolve something else is the
	// round trip that ordering avoids.
	//
	// Collisions come last because the collision view is a FUNCTION of the
	// resolved category — the content hash is computed from the canonical
	// category name, and a row whose category does not resolve is dropped
	// from grouping entirely. Reporting collisions computed against
	// categories the user has not chosen would name rows that stop
	// colliding once the mapping lands, and stay silent about rows that
	// start. The user would resolve a collision that was never real.
	if unresolved := unresolvedImportCategories(entry.Rows, req.CategoryMap, catNameToID, req.DefaultCategoryID, gateCurrencies); len(unresolved) > 0 {
		entry.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":                  "UNRESOLVED_CATEGORIES",
			"unresolved_categories": unresolved,
		})
		return
	}

	groups, err := buildCollisionGroups(
		r.Context(),
		h.queries,
		entry.Rows,
		req.CategoryMap,
		req.DefaultCategoryID,
		catNameToID,
		catIDToName,
		gateCurrencies,
	)
	if err != nil {
		entry.mu.Unlock()
		log.Printf("import confirm: build collision groups: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to rebuild collision groups")
		return
	}
	if len(groups) > 0 {
		entry.mu.Unlock()
		// 409 body shape mirrors the Chunk 2 PATCH 400 body: {code, ...}
		// with a machine-readable code so the frontend can switch on it.
		// The full collision_groups array is included so the preview can
		// re-render without a second round-trip — the frontend never has
		// to ask "which rows are still colliding?" after a 409.
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":             "UNRESOLVED_COLLISIONS",
			"collision_groups": groups,
		})
		return
	}

	// Phase 3.4b: filter user-skipped rows out of the slice passed to
	// processImportRows. The filter lives at the handler level (not
	// inside processImportRows) so the processor's conservation
	// invariant `len(Rows) == len(Inserted) + len(Skipped) + len(Errored)`
	// — which property tests in import_handlers_property_test.go
	// depend on — stays true for the rows that actually reach it.
	// From the handler's POV, user-skipped rows are "never in the
	// batch"; from the processor's POV, the batch simply never
	// contained them.
	//
	// We iterate entry.Rows (not a copy) and accumulate into a fresh
	// slice pre-sized to the upper bound, so there is no reallocation
	// on typical inputs. Over-allocation for a heavily-skipped session
	// is negligible (len(importRow) * number_skipped bytes).
	filteredRows := make([]importRow, 0, len(entry.Rows))
	for _, row := range entry.Rows {
		if row.Skip {
			continue
		}
		filteredRows = append(filteredRows, row)
	}
	totalRowCount := len(entry.Rows)
	userSkippedCount := totalRowCount - len(filteredRows)
	entry.mu.Unlock()

	// Start a database transaction for all inserts
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	qtx := h.queries.WithTx(tx)

	// Phase 3.5: the per-row loop moved into processImportRows so
	// property tests can observe structured outcomes (inserted/
	// skipped/errored) without an HTTP round-trip. The handler still
	// owns auth, JSON, store lookup, category loading, and the SQL
	// transaction lifecycle — processImportRows only runs the policy
	// loop.
	// Loaded on qtx, inside the transaction the rows commit in, so every row
	// in the batch is resolved against one view of the currencies table — the
	// same view the inserts land against.
	//
	// The qtx is not a preference here, it is the only option: the pool is
	// capped at one connection (SetMaxOpenConns(1), cmd/spendrop/db.go, and
	// the test harness mirrors it), so a read issued on h.queries while this
	// transaction holds that connection waits for a connection the
	// transaction will not release until it commits — a deadlock, not a
	// staleness bug. That is also why passing the gate's snapshot down here
	// instead would go unnoticed by every test: it produces the same answer
	// whenever the table has not changed, and the case where it does not is
	// the one no test can open a second connection to create.
	insertCurrencies, err := loadImportCurrencies(r.Context(), qtx)
	if err != nil {
		log.Printf("import confirm: load currencies in tx: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load currencies")
		return
	}

	result, minImportDate := processImportRows(r.Context(), qtx, tx, h.txnStore, importProcessInput{
		UserID:            entry.UserID,
		Rows:              filteredRows,
		CategoryMap:       req.CategoryMap,
		DefaultCategoryID: req.DefaultCategoryID,
		CatNameToID:       catNameToID,
		CatIDToName:       catIDToName,
		Currencies:        insertCurrencies,
	})

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit import")
		return
	}

	// Clean up the import entry
	importStore.Delete(req.ImportID)

	// Phase 3.3: reverify every checkpoint on or after the earliest
	// imported row. Imports are the single largest write path in the
	// system and are the most likely source of "my checkpoint went red
	// overnight" surprises — running the hook once at the end of the
	// batch is cheap and keeps the /healthz/data counts consistent
	// without bloating the hot per-row loop. Skipped-only imports keep
	// minImportDate zero and no-op the hook.
	if !minImportDate.IsZero() {
		h.verifyAffectedCheckpoints(r.Context(), minImportDate)
	}

	// Phase C (Task 17): import is the single largest expense-creating path, so
	// it must fire the over-budget alert just like batch-create. Collect every
	// distinct (category, month) cell the inserted rows landed in and evaluate
	// the deduped set once. Post-commit, best-effort — a push failure never
	// affects the already-committed import.
	if len(result.Inserted) > 0 {
		cellSet := map[budgetCell]struct{}{}
		for _, ins := range result.Inserted {
			cellSet[cellForDate(ins.CategoryID, ins.Date)] = struct{}{}
		}
		cells := make([]budgetCell, 0, len(cellSet))
		for c := range cellSet {
			cells = append(cells, c)
		}
		h.evaluateBudgetAlerts(r.Context(), cells)
	}

	// Live-updates: a CSV import emits ONE signal for the whole batch
	// (mirrors notifyTxnBatch aggregation). Newly-inserted rows make the
	// transactions list, dashboard, reports, and budget cells stale on every
	// open device. Post-commit, best-effort, nil-safe — never affects the
	// already-committed import.
	if len(result.Inserted) > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports", "budgets")
	}

	// Phase 3.4b: the user-visible `skipped` field rolls up three
	// reasons into one bucket, because from the user's perspective a
	// row that "did not land" is a row that did not land, regardless
	// of the category:
	//   1. User-skipped rows (row.Skip==true, filtered above before
	//      processImportRows sees them) — still appear in entry.Rows
	//      so they count toward total but not toward inserted.
	//   2. Processor-skipped rows (content-hash duplicate, zero
	//      amount, mismatched money signs, etc. — see skipReason* in
	//      processImportRows).
	//   3. Errored rows (DB faults, bad category_ids — a tiny bucket
	//      that the user can't distinguish from category 2 without
	//      log access).
	//
	// The arithmetic `len(entry.Rows) - len(result.Inserted)` captures
	// all three without needing to sum the process result's Skipped
	// and Errored slices AND add the user-skipped count separately.
	// It works because:
	//   total        = len(entry.Rows)
	//   processed    = len(filteredRows)            (= total - user_skipped)
	//   inserted     = len(result.Inserted)         (≤ processed)
	//   not_inserted = total - inserted
	//                = user_skipped + (processed - inserted)
	//                = user_skipped + processor_skipped + errored
	//
	// `skipped` alone is a number the user cannot act on. A run that reports
	// "12 imported, 488 skipped" is indistinguishable from a broken import
	// unless the response says what happened to the 488, so the same three
	// buckets are also emitted split by reason. Keys are the importSkipReason
	// values, plus two the processor has no name for because they never reach
	// it: rows the user skipped in the preview, and rows that errored.
	//
	// Only non-zero reasons appear. The map is a description of what
	// happened, and a wall of zeroes describes nothing.
	skippedReasons := make(map[string]int, 4)
	for _, s := range result.Skipped {
		skippedReasons[string(s.Reason)]++
	}
	if len(result.Errored) > 0 {
		skippedReasons[importOutcomeError] = len(result.Errored)
	}
	if userSkippedCount > 0 {
		skippedReasons[importOutcomeUserSkipped] = userSkippedCount
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"imported":        len(result.Inserted),
		"skipped":         totalRowCount - len(result.Inserted),
		"total":           totalRowCount,
		"skipped_reasons": skippedReasons,
	})
}

// Wire-only outcome keys for the confirm response's skipped_reasons rollup.
// They are deliberately NOT importSkipReason values: that type is the closed
// set the property tests assert over result.Skipped, and neither of these
// ever reaches processImportRows — a user-skipped row is filtered out before
// the batch, and an errored row lands in result.Errored.
const (
	importOutcomeUserSkipped = "user_skipped"
	importOutcomeError       = "error"
)

// handleImportCancel removes a pending import entry from memory so the
// per-user slot is freed immediately (instead of waiting for TTL expiry).
func (h *Handler) handleImportCancel(w http.ResponseWriter, r *http.Request) {
	importID := chi.URLParam(r, "importID")
	if _, ok := loadImportEntryForUser(w, r, importID); !ok {
		return
	}
	importStore.Delete(importID)
	w.WriteHeader(http.StatusNoContent)
}

// patchImportRowRequest is the JSON body shape for PATCH /api/import/{importID}/rows/{rowID}.
// Field is one of "date", "description", "amount", "rate", "skip" — validated
// by validateImportField. Value is typed as any so the JSON decoder accepts
// both string (for date/description/amount/rate) and bool (for skip) without
// a second layer of per-field request structs.
//
// original_amount and original_currency are deliberately NOT patchable. A
// row's foreign money is a fact about the spreadsheet, and an unknown currency
// is resolved in Settings — outside this session entirely — which is why the
// rate is the only money cell the preview can edit.
type patchImportRowRequest struct {
	Field string `json:"field"`
	Value any    `json:"value"`
}

// patchImportRowErrorBody is the 400 response shape. Code is a stable
// machine-readable constant (INVALID_DATE, INVALID_DESCRIPTION,
// INVALID_AMOUNT, INVALID_RATE, INVALID_FIELD) so the frontend can color-code
// the originating cell without parsing the message. Field echoes back the
// request field so the frontend cellErrors map can key on row_id:field
// without a second round-trip.
type patchImportRowErrorBody struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// handleImportPatchRow applies one field edit to one row in a pending
// import session, rebuilds the collision_groups view, and returns a full
// snapshot of {rows, collision_groups}. The endpoint is PATCH (not PUT)
// because it takes exactly one field at a time — the frontend debounces
// multi-field edits into sequential PATCHes via its enqueuePatch promise
// chain, so there is no "edit several fields atomically" need.
//
// Response shape is intentionally NOT a sparse diff: even a single-field
// edit returns the full row list plus the full groups list. Sparse diffs
// would require the frontend to reconcile a partial update into component
// state, which is the class of bug the "styling is always derived from the
// latest server response, never from stale local state" rule is designed
// to prevent. Full snapshots are trivially mergeable via Array.map
// preserving object identity for unchanged rows (see the frontend hook in
// Chunks 4–5).
//
// The re-hash + re-group happens on EVERY edit, even for field="skip",
// because a skip flip changes which rows participate in collision
// grouping (skipped rows are excluded from buildCollisionGroups, so
// toggling skip can collapse or expand a group). Computing a "this field
// does not affect grouping, skip the rebuild" optimization would add a
// branch for one saved DB lookup per skip toggle, which is not worth the
// surface area.
//
// Errors:
//
//	400 invalid request body       — JSON decode failed or field/value missing
//	400 {code, field, message}     — validateImportField rejected the input
//	400 invalid row_id             — rowID URL param not parseable as an int
//	400 row_id out of range        — rowID outside [0, len(entry.Rows))
//	401/403/404                    — via loadImportEntryForUser (unauthorized,
//	                                 wrong user, missing/expired session)
//	500 failed to rebuild groups   — buildCollisionGroups returned a DB fault
func (h *Handler) handleImportPatchRow(w http.ResponseWriter, r *http.Request) {
	importID := chi.URLParam(r, "importID")
	rowIDStr := chi.URLParam(r, "rowID")

	// Auth/ownership check first so an unauthenticated caller can't probe
	// rowID validity (or leak the absence-of-session) by sending a malformed
	// rowID ahead of an invalid session. loadImportEntryForUser also asserts
	// the importID is the correct length.
	entry, ok := loadImportEntryForUser(w, r, importID)
	if !ok {
		return
	}

	rowID, err := strconv.Atoi(rowIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid row_id")
		return
	}

	var req patchImportRowRequest
	if decodeErr := decodeJSON(w, r, &req); decodeErr != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Field == "" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	normalized, errCode, message := validateImportField(req.Field, req.Value)
	if errCode != "" {
		writeJSON(w, http.StatusBadRequest, patchImportRowErrorBody{
			Code:    errCode,
			Field:   req.Field,
			Message: message,
		})
		return
	}

	// Serialize entry.Rows access across concurrent handlers for this
	// session. The realistic race is two browser tabs on the same import:
	// one tab's PATCH mutation can interleave with another tab's PATCH,
	// Confirm, or GET read of entry.Rows. We hold the mutex across the
	// buildCollisionGroups DB calls so the returned groups snapshot is
	// consistent with the Rows snapshot emitted in the same response.
	// Cross-entry contention does not exist — each importEntry has its
	// own mutex.
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if rowID < 0 || rowID >= len(entry.Rows) {
		writeError(w, http.StatusBadRequest, "row_id out of range")
		return
	}

	// Mutate the row in place via a pointer into entry.Rows. We do NOT
	// take a copy, edit the copy, and re-assign by index — that pattern
	// reads as "two writes where there is really one" and the mutex above
	// already establishes the serialization story.
	row := &entry.Rows[rowID]
	switch req.Field {
	case "date":
		// Store the date as the canonical ISO string so downstream
		// re-hashes via parseImportDate + ComputeContentHash produce the
		// same hash they would have at upload time if the user had typed
		// this value originally. Without normalization, "7/1/25" and
		// "2025-07-01" would disagree at the hash step even though they
		// represent the same day.
		t := normalized.(time.Time)
		row.Date = t.Format("2006-01-02")
	case "description":
		row.Description = normalized.(string)
	case "amount":
		row.Amount = normalized.(float64)
		// The raw cell travels with the value. validateImportField has already
		// refused anything unparseable, so this always clears an amount_invalid
		// flag rather than carrying a stale one past the edit that fixed it.
		// The comma-ok form because this reads the REQUEST rather than the
		// normalized value its neighbours assert on: the validator has proved
		// it is a string, and a bare assertion here would panic a handler on a
		// body it has already answered for.
		rawAmount, _ := req.Value.(string)
		row.RawAmount = strings.TrimSpace(rawAmount)
	case importFieldRate:
		rate := normalized.(importRateValue)
		row.Rate = rate.Rate
		row.RawRate = rate.Raw
	case "skip":
		row.Skip = normalized.(bool)
	}

	// Rebuild the whole preview against the just-edited session slice. Every
	// derived field is recomputed — collision groups, both families of field
	// errors, the unresolved categories, the currencies — which is what makes
	// an edit resolve a flag with no client-side bookkeeping: skipping the
	// last row carrying an undecided name drops the entry, and shortening a
	// description drops its error.
	//
	// The canonical category lookups come from the live DB via
	// ListAllCategories so the preview-time hash formula matches confirm's.
	existingCats, listErr := h.queries.ListAllCategories(r.Context())
	if listErr != nil {
		log.Printf("import patch: list categories: %v", listErr)
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	catIDToName := make(map[int64]string, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
		catIDToName[c.ID] = c.Name
	}

	preview, previewErr := h.buildImportPreview(r.Context(), importID, entry, catNameToID, catIDToName)
	if previewErr != nil {
		log.Printf("import patch: build preview: %v", previewErr)
		writeError(w, http.StatusInternalServerError, "failed to rebuild collision groups")
		return
	}

	writeJSON(w, http.StatusOK, preview)
}

// handleImportGetSession returns the full current snapshot of an import
// session — rows, columns, unique_categories, and freshly-computed
// collision_groups — via the shared loadImportEntryForUser gate. This
// is the F5/tab-refresh resume path: the frontend persists import_id in
// localStorage on upload and calls GET on mount to rehydrate preview
// state without re-uploading the file.
//
// Why recompute collision_groups on every GET instead of caching:
// after Chunk 2, every PATCH mutates entry.Rows in place and the
// collision_groups field on the response is always a function of the
// current Rows slice. Caching the groups would require invalidation
// plumbing on every PATCH, and the DB cost is identical to a PATCH
// rebuild (one GetTransactionByContentHash per hashable row).
// Recomputing on read is the cheaper invariant to maintain.
//
// Category resolution:
// At upload time we don't know which category_map / default_category_id
// the user will pick — those are confirm-time arguments. So the GET
// handler, like handleImportUpload and handleImportPatchRow, passes
// nil/0 for the user-choice args. buildCollisionGroups still uses the
// canonical DB category name for the hash formula (via catIDToName),
// so a category rename between upload and GET would correctly mutate
// the preview-time hash and potentially collapse or expand a collision.
//
// Errors:
//
//	401/403/404                    — via loadImportEntryForUser (unauthorized,
//	                                 wrong user, missing/expired session)
//	500 failed to load categories  — ListAllCategories returned a DB fault
//	500 failed to rebuild groups   — buildCollisionGroups returned a DB fault
func (h *Handler) handleImportGetSession(w http.ResponseWriter, r *http.Request) {
	importID := chi.URLParam(r, "importID")

	entry, ok := loadImportEntryForUser(w, r, importID)
	if !ok {
		return
	}

	// Serialize entry.Rows access — see handleImportPatchRow for the full
	// rationale. This read path is included so a concurrent PATCH from
	// another tab cannot interleave with the buildCollisionGroups read of
	// entry.Rows here.
	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Load categories for the canonical hash resolution. buildImportPreview
	// needs catNameToID (upload-time name match) and catIDToName (canonical
	// name for the hash formula); every surface loads them the same way so
	// the preview-time hash matches confirm's.
	existingCats, err := h.queries.ListAllCategories(r.Context())
	if err != nil {
		log.Printf("import get: list categories: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	catIDToName := make(map[int64]string, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
		catIDToName[c.ID] = c.Name
	}

	// Everything on the resume response is recomputed here, and the money
	// family is why that matters beyond freshness: a currency added in
	// Settings since the upload clears its rows' flags on this GET, with no
	// re-upload, because the preview is a function of the currencies table as
	// it stands rather than of what it said when the file was parsed.
	preview, err := h.buildImportPreview(r.Context(), importID, entry, catNameToID, catIDToName)
	if err != nil {
		log.Printf("import get: build preview: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to rebuild collision groups")
		return
	}

	writeJSON(w, http.StatusOK, preview)
}

// processImportRows is the pure-processing core of handleImportConfirm.
// Given an already-open sqlc transaction (qtx), a set of preview rows,
// and the resolved category lookups, it walks the rows and returns a
// structured outcome plus the earliest inserted date (for the Phase 3.3
// checkpoint verifier hook).
//
// Phase 3.5 extracted this function out of the HTTP handler so property
// tests can observe row outcomes directly, without an HTTP round-trip
// and without standing up auth/JSON plumbing. The HTTP handler still
// owns auth, decoding, the import store lookup, the category load, and
// the SQL transaction lifecycle — none of which are interesting to a
// fuzzed input mix.
//
// Conservation invariant — `len(input.Rows) == len(result.Inserted) +
// len(result.Skipped) + len(result.Errored)` — is enforced by the loop
// shape: every iteration appends to exactly one slice and then
// continues. The `import_handlers_property_test.go` TestConservation
// property exercises this with randomized input so a refactor that
// forgets to account for a branch fails loudly instead of silently
// dropping a row from the count.
//
// Reason discipline — every row in result.Skipped carries a reason
// drawn from the closed importSkipReason set — is also exercised by a
// property. A branch that increments "skipped" without naming the
// reason fails the "no silent drops with reasons" property within a
// handful of shrinks.
//
// Errors that reach qtx (DB lookup failure, insert failure, unknown
// category_id) flow into result.Errored rather than a Go-level
// `error` return. Callers that want to distinguish data errors from
// systemic errors inspect len(result.Errored); the HTTP handler folds
// both skipped and errored into the `skipped` count of its JSON
// response because from the user's perspective a row that did not
// land is a row that did not land. A future operator surface
// (`/healthz/data`, audit log) can read the bucket split directly if
// we decide per-row error visibility is worth the schema change.
func processImportRows(
	ctx context.Context,
	qtx *database.Queries,
	tx *sql.Tx,
	store *database.TransactionStore,
	in importProcessInput,
) (importResult, time.Time) {
	var result importResult
	var minDate time.Time

	for i, row := range in.Rows {
		// Everything that rejects a row without ever consulting its
		// category, in one place — see preCategorySkipReason for why the
		// order matters and why it is shared rather than inlined here.
		// The parsed date travels out of the check so the hash formula
		// below does not re-parse what was just validated.
		date, money, reason, blocked := preCategorySkipReason(row, in.Currencies)
		if blocked {
			result.Skipped = append(result.Skipped, importSkipped{
				RowIndex: i,
				Reason:   reason,
			})
			continue
		}

		categoryID := resolveCategoryID(row.Category, in.CategoryMap, in.CatNameToID, in.DefaultCategoryID)
		if categoryID == 0 {
			result.Skipped = append(result.Skipped, importSkipped{
				RowIndex: i,
				Reason:   skipReasonMissingCategory,
			})
			continue
		}
		canonicalCategoryName, ok := in.CatIDToName[categoryID]
		if !ok {
			// Caller supplied a category_id that isn't in the DB. This
			// is a client bug (or a racey category deletion); log and
			// route to Errored rather than Skipped — it's a systemic
			// fault, not a user-data rejection, and it's correctable
			// by re-running with a valid default_category_id.
			log.Printf("import: resolved category_id=%d not found in lookup (row desc=%s)", categoryID, sanitizeLogValue(row.Description))
			result.Errored = append(result.Errored, importErrored{
				RowIndex: i,
				Reason:   fmt.Sprintf("unknown category_id=%d", categoryID),
			})
			continue
		}

		// Phase 3.4: compute the content hash from the resolved row
		// identity and check the live index before inserting. A hit
		// skips the row as a duplicate — the Phase 3.4b collision
		// editor surfaces those predictions in the upload preview so
		// the user can resolve them by editing fields instead of
		// appending blunt " (N)" suffixes.
		//
		// B10: the cents value is SIGNED, and the hash is taken over the
		// exact value the row will store — the same input
		// buildCollisionGroups uses, so a preview prediction and this
		// insert can never disagree. A -42.50 refund and a +42.50 purchase
		// on the same day, description and category are now DISTINCT
		// identities; before B10 the second one collapsed into the first
		// as a "duplicate".
		//
		// Accepted consequence, no repair possible: rows imported from
		// negative cells BEFORE B10 were stored flipped-positive and
		// hashed positive. Re-importing that same sheet now hashes the
		// negative, misses the stored anchor, and re-adds those rows.
		// Nothing in stored data distinguishes "was entered as +42.50"
		// from "was flipped from -42.50", so no backfill can tell them
		// apart — this is documented for the owner rather than fixed.
		// The hash is taken over money.AmountCents — the cents that will be
		// STORED — which for a rate row is the derived value, not the sheet's
		// (possibly empty) USD cell. buildCollisionGroups hashes the same
		// quantity from the same resolver, so a hand-typed row at 89,000 and a
		// sheet row at 89,000 are one identity, while the same row quoted at
		// 89,500 is a different booking, which is correct: it is different
		// money.
		description := row.Description
		amountCents := money.AmountCents
		hash := database.ComputeContentHash(date, amountCents, description, canonicalCategoryName)

		// Ordinary path: look up the hash and skip on a hit. The
		// lookup runs on qtx so it observes any rows inserted
		// earlier in this very batch — importing a spreadsheet
		// that contains the same row twice detects the second
		// occurrence as a duplicate of the first within the same
		// commit.
		_, lookupErr := qtx.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true})
		if lookupErr == nil {
			result.Skipped = append(result.Skipped, importSkipped{
				RowIndex: i,
				Reason:   skipReasonDuplicate,
			})
			continue
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			log.Printf("import: content hash lookup failed (row=%d): %v", i, lookupErr)
			result.Errored = append(result.Errored, importErrored{
				RowIndex: i,
				Reason:   sanitizeLogValue(lookupErr.Error()),
			})
			continue
		}

		// Build params. Every money field comes from resolveImportMoney — one
		// function, one matrix, the same one the preview showed the user — so
		// nothing about what this row stores is decided here.
		//
		// Phase 3.1b: the legacy REAL amount column was dropped in migration
		// 010; only amount_cents is written. The cents value is derived from
		// the same float parsed out of the spreadsheet so a round-trip
		// export->import is lossless for any representable money amount.
		//
		// Phase 3.4: content_hash is populated from the resolved row
		// identity computed above. The partial unique index guarantees
		// the insert fails loudly if the dup check above raced a parallel
		// import of the same row — there is no silent double-insert path.
		//
		// idempotency_key stays NULL here, deliberately: an imported row is
		// identified by what it CONTAINS, and content_hash already makes a
		// repeated import of the same spreadsheet idempotent. Keys identify a
		// submission attempt and belong to the single-create endpoint, whose
		// retry-double-post problem content_hash cannot solve. See migration
		// 017 and handleCreateTransaction.
		//
		// booked_rate is NULL for exactly three shapes, and populated for the
		// rest. It stays NULL when the row is plain base money (#1), when the
		// row names the base currency (#7), and when the sheet stated a
		// foreign pair but quoted NO rate (#2) — that pair is a label, and
		// dividing one half by the other would manufacture a rate the user
		// never quoted. It is POPULATED, with the sheet's own Rate cell, for
		// every row whose base amount was derived from it (#3, and #4 once
		// the sheet's own USD is shown to agree). That is the whole point of
		// the column: a back-dated row is worth what it was worth on the day,
		// and a booked rate is one-way (freeze-on-edit), so it must be the
		// rate the row was actually quoted at — never today's.
		//
		// The values are assigned across from importMoney rather than
		// re-derived: the original is already signed in lockstep with
		// amount_cents (a foreign refund is negative on both sides, which
		// keeps the implied rate positive), and the currency is already the
		// household's canonical code rather than the sheet's spelling.
		params := database.CreateTransactionParams{
			UserID:              in.UserID,
			Date:                date,
			AmountCents:         amountCents,
			OriginalAmountCents: money.OriginalAmountCents,
			OriginalCurrency:    money.OriginalCurrency,
			BookedRate:          money.BookedRate,
			Description:         description,
			CategoryID:          categoryID,
			Tags:                toNullString(row.Tags),
			Notes:               toNullString(row.Notes),
			ContentHash:         sql.NullString{String: hash, Valid: true},
		}

		// Route the insert through the TransactionStore on the caller's
		// tx so each imported row emits a paired transaction_audit row.
		// CreateTx internally does s.q.WithTx(tx), so the audited insert
		// shares the exact transaction as the dup-check qtx above — N data
		// rows + N audit rows commit (or roll back) together at the
		// handler's tx.Commit. in.UserID is the actor, the same value
		// already written as params.UserID.
		created, err := store.CreateTx(ctx, tx, in.UserID, params)
		if err != nil {
			log.Printf("import: failed to insert row (date=%s, desc=%s): %v", sanitizeLogValue(row.Date), sanitizeLogValue(description), err)
			result.Errored = append(result.Errored, importErrored{
				RowIndex: i,
				Reason:   sanitizeLogValue(err.Error()),
			})
			continue
		}

		minDate = earliestDate(minDate, created.Date)
		result.Inserted = append(result.Inserted, importInserted{
			RowIndex:    i,
			Date:        created.Date,
			AmountCents: created.AmountCents,
			CategoryID:  created.CategoryID,
		})
	}

	return result, minDate
}

// preCategorySkipReason reports whether a row is rejected before its category
// is consulted at all, and returns the parsed date so the caller does not
// re-parse it.
//
// Date first: every later check depends on having a valid time.Time for the
// hash formula, and an unparseable date is the single most common real-world
// input bug (stray header rows, footer totals, blank rows). The order decides
// which reason "wins" on a row with several defects, and it is the order
// processImportRows has always used — no existing outcome label changes.
//
// Description is compared to "" not whitespace: the parsing path already runs
// strings.TrimSpace before a row lands here, so an all-whitespace cell arrives
// as "". The zero-amount test is against the RESOLVED cents rather than the
// sheet's USD cell, which is what lets a row that carries only a foreign
// original and a rate through — it has money, the sheet just stated it in
// another currency. The sign survives untouched to the ledger either way; what
// is rejected is a row worth nothing at all.
//
// The policy, since B10: sign carries meaning WITHIN a category. A category is
// still expense or income, but a negative amount on an expense row is a refund
// and nets against that category's spend, so a spreadsheet's "-15.00" is data,
// not a formatting quirk to be normalized away. What a signed ledger permits
// is zero SUMS, never zero ROWS — hence the zero gate below stays.
//
// The sign gate is the corollary: when a row carries both a base amount and a
// foreign original, the two must agree in direction (a foreign refund is
// negative on both sides). Disagreement is a contradiction, not something to
// reconcile — see skipReasonSignMismatch.
//
// The length check is a floor that should never fire from the HTTP path:
// handleImportConfirm refuses the whole batch with 409 FIELD_TOO_LONG first.
// It exists because this is the last thing between a preview row and the
// ledger, and because the property tests require every skipped row to name a
// declared reason — a silent drop here would be a row that vanished with
// nothing recording why. It is also why the confirm-time gate cannot be
// quietly deleted: doing so would not let over-long rows through, it would
// move the rejection from a preview the user can act on to a count they
// cannot explain.
//
// Shared with unresolvedImportCategories rather than inlined, because the
// category gate must flag exactly the rows whose ONLY obstacle is the
// category. Two copies of this list would drift, and the drift would show up
// as a file the preview refuses to import for a decision that could not
// change its outcome.
//
// The resolved money travels out alongside the date, for the same reason the
// date does: every caller needs it (the hash is taken over the cents that will
// be stored, and the insert assigns all four money fields from it), and
// resolving it twice would be two chances to resolve it differently.
//
// Money resolution is LAST, and every step of that order is load-bearing:
//
//   - The sign gate stays ahead of it. A row whose base and foreign halves
//     point opposite ways is a contradiction about the row, and reporting the
//     amount instead would name the consequence rather than the cause.
//   - zero_amount is now read off the RESOLVED money rather than off the
//     sheet's USD cell. That is the design's "no usd AND no (orig+rate)" rule
//     stated once: a row with an original and a rate resolves to real cents
//     and is not zero, while a row with nothing at all still is. It cannot be
//     asked before the resolver, because before the resolver there is no
//     amount to test.
//   - The money flags are last-ditch. Every one of them has already blocked
//     the preview and refused confirm with a 409; this floor exists so a
//     session that reaches the insert with a stale flag — a currency deleted
//     between gate and commit — skips the row with a name instead of storing
//     the wrong money. Same role the field_too_long floor plays above it.
func preCategorySkipReason(row importRow, cur importCurrencies) (time.Time, importMoney, importSkipReason, bool) {
	date, reason, blocked := preMoneySkipReason(row)
	if blocked {
		return time.Time{}, importMoney{}, reason, true
	}
	if len(checkImportRowLengths([]importRow{row})) > 0 {
		return time.Time{}, importMoney{}, skipReasonFieldTooLong, true
	}
	if moneySignsDisagree(row.Amount, row.OriginalAmount) {
		return time.Time{}, importMoney{}, skipReasonSignMismatch, true
	}
	money, _, moneyReason := resolveImportMoney(row, cur)
	if moneyReason != "" {
		return time.Time{}, importMoney{}, moneyReason, true
	}
	if money.AmountCents == 0 {
		return time.Time{}, importMoney{}, skipReasonZeroAmount, true
	}
	return date, money, "", false
}

// preMoneySkipReason reports the two rejections that are decided before a row's
// money is looked at, and that EXEMPT it from the money flags.
//
// It exists to be shared, not to shorten preCategorySkipReason. A row with no
// parseable date or no description is going to be skipped whatever its
// currency cell says — the archetype is the trailing "TOTAL 5,000,000 LBP"
// line on a bank statement — so flagging its money would demand a Skip tick,
// per footer line, to unblock an import those rows were never going to join.
// unresolvedImportCategories has always taken exactly this position for the
// category gate; importRowMoney takes it for the money one.
//
// It is deliberately NARROWER than "everything decided before money". A row
// that is merely too long, or whose two money halves disagree in sign, keeps
// its money flag: both are fixable in the preview (shorten the description,
// edit the amount), so the user can see and fix both problems in one pass
// instead of resolving one and being sent round again for the other.
//
// The date travels out because the caller needs it and re-parsing is the
// classic way two copies of a check drift.
func preMoneySkipReason(row importRow) (time.Time, importSkipReason, bool) {
	date, err := parseImportDate(row.Date)
	if err != nil {
		return time.Time{}, skipReasonUnparseableDate, true
	}
	if row.Description == "" {
		return time.Time{}, skipReasonEmptyDescription, true
	}
	return date, "", false
}

// stripCurrencySymbols removes currency symbols ($, €, £) and surrounding
// whitespace, and converts accounting-format negatives like (42.50) to -42.50.
// It leaves digits, commas and the decimal point alone, so a caller can still
// see where the commas were.
func stripCurrencySymbols(s string) string {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("$", "", "€", "", "£", "").Replace(s)
	s = strings.TrimSpace(s)
	// Convert accounting-format negatives: (42.50) → -42.50
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = "-" + s[1:len(s)-1]
	}
	return s
}

// importGroupedInteger matches an integer part whose commas are all thousands
// separators: one to three digits, then groups of exactly three.
var importGroupedInteger = regexp.MustCompile(`^\d{1,3}(,\d{3})+$`)

// cleanImportNumber prepares an imported money cell for strconv, and refuses
// any comma that is not a thousands separator.
//
// This is the one reading a comma may take, and the rule is positional
// because no other rule is available. Half the world writes 0,92 for what the
// other half writes 0.92; a bare spreadsheet cell carries no locale, and
// "1,500" is one thousand five hundred to one reader and one and a half to
// another. Deleting every comma — which is what these parsers used to do —
// resolves that ambiguity by silently picking
// the reading that is a HUNDREDFOLD error when it is wrong: "0,92" became 92.
// On a rate that is worse than wrong, because a booked rate is frozen onto
// the row: 100 EUR at a "0,92" rate stored $1.09 and a booked rate of 92,
// with nothing flagged, on precisely the foreign-only sheet the Rate column
// exists to import.
//
// So: commas in grouping position are stripped (89,000 and 1,500,000.50 are
// ordinary household figures), and anything else — a decimal comma, a
// mis-grouped one, one after the decimal point — is an error the caller
// reports rather than a number it guesses at.
func cleanImportNumber(s string) (string, error) {
	cleaned := stripCurrencySymbols(s)
	if !strings.Contains(cleaned, ",") {
		return cleaned, nil
	}

	body := strings.TrimPrefix(strings.TrimPrefix(cleaned, "-"), "+")
	intPart, frac := body, ""
	if dot := strings.IndexByte(body, '.'); dot >= 0 {
		intPart, frac = body[:dot], body[dot:]
	}
	// A comma after the decimal point is never a separator, and neither is
	// one in a group that is not exactly three digits long.
	if strings.Contains(frac, ",") || !importGroupedInteger.MatchString(intPart) {
		return "", fmt.Errorf("a comma is only a thousands separator: %q", s)
	}
	return strings.ReplaceAll(cleaned, ",", ""), nil
}

// parseImportAmount converts a string from an imported xlsx Amount cell
// (or Original Amount cell) into an int64 cents value. It strips
// currency formatting via cleanImportNumber — which also refuses any comma
// that is not a thousands separator — parses the remainder as a float64, and
// rounds to cents via dollarsToCents.
//
// Extracted for two reasons:
//
//  1. It is the fuzz target for FuzzParseImportAmount
//     (import_handlers_fuzz_test.go). Having a single named function
//     for the parse-and-round step lets the fuzzer hit the exact code
//     that handles untrusted string input without needing to drive the
//     whole xlsx→row plumbing.
//
//  2. Phase 3.1 cents-normalization hygiene. Callers that previously
//     ran a strip-and-`ParseFloat` inline now route through
//     one place that also validates NaN/Inf and magnitude. Previously
//     an xlsx cell containing "NaN" or "1e20" would parse to a garbage
//     float and flow silently into dollarsToCents, producing an
//     unpredictable int64 bit pattern in the DB; we now reject those
//     up front and the caller counts the row as zero-amount (same
//     bucket as the existing "unparseable" silent-skip behavior).
//
// Returns an error (and zero cents) on:
//   - empty string (after stripping)
//   - unparseable float after stripping
//   - NaN or infinite values
//   - magnitude above MaxTransactionAmount — anything larger is either
//     a currency-entry mistake or would overflow int64 cents after the
//     dollarsToCents multiplication
//
// Negative values are accepted, and since B10 they are also PRESERVED: the
// returned cents keep their sign all the way to amount_cents, where a negative
// on an expense row is a refund. (Before B10 the confirm-side insert flipped
// every sign unconditionally with math.Abs — this doc used to describe that
// flip as conditional on category type, which was never true.)
//
// The bound below is magnitude-symmetric on purpose: ±MaxTransactionAmount,
// not "> Max" on a stripped value. FuzzParseImportAmount pins both ends.
func parseImportAmount(s string) (int64, error) {
	cleaned, err := cleanImportNumber(s)
	if err != nil {
		return 0, err
	}
	if cleaned == "" {
		return 0, fmt.Errorf("empty amount")
	}
	parsed, parseErr := strconv.ParseFloat(cleaned, 64)
	if parseErr != nil {
		return 0, fmt.Errorf("parse amount %q: %w", s, parseErr)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("non-finite amount: %q", s)
	}
	if math.Abs(parsed) > MaxTransactionAmount {
		return 0, fmt.Errorf("amount out of range: %q: %w", s, errImportAmountRange)
	}
	return dollarsToCents(parsed), nil
}

// errImportAmountRange marks the one parseImportAmount failure that is not a
// parse failure: a number the caller wrote correctly and SpenDrop will not
// store. It is a sentinel because the two need different sentences — "this is
// not a number" is a false statement about 2,000,000,000, and the PATCH 400 it
// produces is rendered into the cell the user just edited, where it replaces
// an accurate flag about the row's real problem.
var errImportAmountRange = errors.New("amount out of range")

// parseImportDate converts a string from an imported xlsx Date cell into a
// time.Time. It tries two strategies in order:
//
//  1. Excel serial date number. When the upload path opens the file with
//     RawCellValue:true, any date-typed cell returns its underlying serial
//     number (e.g. "45859" = 2025-07-21). excelize.ExcelDateToTime handles
//     the conversion and works regardless of the cell's number format — so
//     "mm-dd-yy", "yyyy-mm-dd", "d-mmm-yyyy" etc. all land correctly.
//  2. Text date formats. Covers files where the Date column was typed as
//     plain text rather than a date-formatted cell.
//
// A successful parse must then fall inside the household ledger window
// [minImportYear, maxImportYear]. That bound exists because Phase 3.7's
// fuzz target (FuzzParseImportDate) caught a class of edge-case inputs
// that parse cleanly but produce dates no household spreadsheet would
// actually contain: Excel serial 1 → 1899-12-31, 2958465 → 9999-12-31,
// stray misaligned integers like "1234" → 1903-05-18. Before the check
// these would flow silently into the DB and be noticed only when a user
// spotted a row from 1899. The check rejects them up front and the
// caller counts the row as skipped — same bucket as any other
// unparseable date.
//
// An unparseable date returns an error; the caller counts it as skipped.
//
// Note: the serial-date path assumes the 1900 date system (the default in
// modern Excel on every platform). Legacy Mac Excel files that set the 1904
// date system flag are not detected here — their dates will be off by ~4
// years. In practice this is a non-issue for SpenDrop because modern Excel
// and Google Sheets both write 1900-based workbooks.
//
// This window used to be documented as a DELIBERATE divergence: import
// accepted [1900, 2100] while the year picker's [MinYear, MaxYear] was
// [2000, 2100], on the reasoning that import needed historic bank statements
// while the picker only drove budget/savings UI. That reasoning was wrong —
// the picker also drives reports, so an imported 1995 row was stored,
// aggregated, and unreportable. The two are now ONE window:
// minImportYear/maxImportYear are aliases of MinDataYear/MaxDataYear
// (limits.go), which every read endpoint and validateDate share.
//
// 1900 now serves two independent reasons, and both must hold before it can
// move: Excel's serial 1 is 1899-12-31, so a floor of 1900 is what rejects
// misaligned integers as dates; and 1900 is the product's declared oldest
// supported ledger year.
func parseImportDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	// Strategy 1: Excel serial date. Excel's epoch is 1900-01-01 (serial 1),
	// so any valid date lands in [1, 2958465] (2958465 = 9999-12-31). Clamp
	// to that range to avoid mistaking a random stray number (e.g. an amount
	// in the date column) for a date.
	if serial, err := strconv.ParseFloat(s, 64); err == nil {
		if serial >= 1 && serial <= 2958465 {
			if t, err := excelize.ExcelDateToTime(serial, false); err == nil {
				return validateImportYear(t, s)
			}
		}
	}
	// Strategy 2: text formats.
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return validateImportYear(t, s)
		}
	}
	return time.Time{}, fmt.Errorf("unparseable date: %q", s)
}

// minImportYear / maxImportYear bound the household ledger window that
// parseImportDate accepts. They are ALIASES of the data window in limits.go,
// not independent numbers: import and the reports must agree, or an imported
// row lands in a year no report will display.
//
// Kept as named constants rather than folded into the call site for two
// reasons: FuzzParseImportDate references them by name, and the names read
// better than the generic ones inside a "why did my 1990 import row get
// skipped?" investigation. See parseImportDate's doc comment.
const (
	minImportYear = MinDataYear
	maxImportYear = MaxDataYear
)

// validateImportYear enforces the household ledger window on a
// freshly-parsed import date. Split out so both the Excel serial path
// and the text-format path share exactly one range check — there is
// only one place to update if the window ever changes.
func validateImportYear(t time.Time, raw string) (time.Time, error) {
	if y := t.Year(); y < minImportYear || y > maxImportYear {
		return time.Time{}, fmt.Errorf("date out of range: %q parsed to year %d, expected [%d, %d]",
			raw, y, minImportYear, maxImportYear)
	}
	return t, nil
}

// resolveCategoryID determines the category ID for an imported row.
// Priority: explicit category_map > name match against existing categories,
// and the default applies ONLY to a row whose Category cell is empty.
//
// A non-empty name that matches neither the user's map nor an existing
// category resolves to 0 — never to the default. Falling back would file the
// row under a category the user never chose for it, and would do so with
// nothing to notice: the row lands in the ledger, the confirm response counts
// it as imported, and only the ledger itself records that "Grocries" became
// whatever the default happened to be. The name is a decision, and
// handleImportConfirm refuses the batch with 409 UNRESOLVED_CATEGORIES until
// the user makes it.
//
// The default IS honoured for an empty cell, because there is no name to
// decide about — choosing "Default Category" in the preview is itself the
// decision for those rows, and the control says so.
//
// Returning 0 needs no new handling in either caller: buildCollisionGroups
// already drops a 0 row from grouping and processImportRows already records
// it as skipReasonMissingCategory. So even with the 409 gate removed, an
// unmatched name costs a counted skip rather than a silent re-home.
//
// Map keys are matched EXACTLY (after trimming), not case-insensitively.
// The preview's unresolved_categories list is keyed the same way, so every
// distinct spelling the sheet contains gets its own control and its own key
// — the client never has to guess which casing the server will look up.
func resolveCategoryID(categoryName string, categoryMap map[string]int64, catNameToID map[string]int64, defaultID int64) int64 {
	name := strings.TrimSpace(categoryName)

	// 1. No name to decide about — the user's default is the decision.
	if name == "" {
		return defaultID
	}

	// 2. Explicit mapping from the confirm request
	if categoryMap != nil {
		if id, found := categoryMap[name]; found {
			return id
		}
	}

	// 3. Case-insensitive match against existing categories
	if id, found := catNameToID[strings.ToLower(name)]; found {
		return id
	}

	// 4. Undecided. NOT the default — see the doc comment.
	return 0
}

// Reasons an import row's category is unresolved, as they appear on the wire.
//
//	unmapped — the cell names a category that matches nothing the household
//	           has and nothing the user mapped. Remedy: map that name.
//	missing  — the cell is empty. Remedy: choose a default category.
//
// The two are named separately because their remedies are different controls
// in the preview, and a single "unresolved" label would point the user at
// the wrong one.
const (
	unresolvedCategoryUnmapped = "unmapped"
	unresolvedCategoryMissing  = "missing"
)

// unresolvedCategory names one distinct spreadsheet category value that no
// decision covers yet, with the preview rows carrying it. Name is "" for the
// missing-cell case; RowIDs lets the frontend say how much of the file each
// undecided name accounts for, which is the difference between "one typo"
// and "half my ledger".
type unresolvedCategory struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
	RowIDs []int  `json:"row_ids"`
}

// unresolvedImportCategories lists every category value among `rows` that
// resolveCategoryID cannot turn into an id under the given choices. It is
// the shared definition behind three surfaces:
//
//  1. the preview responses (upload / PATCH / GET), called with a nil map
//     and a zero default — i.e. "what would need deciding if you decided
//     nothing", which is exactly the list the mapping UI renders;
//  2. handleImportConfirm's 409 gate, called with the user's actual choices;
//  3. nothing else — the frontend's own gate consumes (1) and marks entries
//     resolved against local state, so the two can disagree only about
//     choices the user has made since the last preview response.
//
// Rows the user marked Skip are excluded, mirroring the collision gate: a
// row that is not going to be inserted needs no category.
//
// Rows that would be rejected BEFORE their category is ever consulted are
// excluded too, via preCategorySkipReason. This is what keeps the gate from
// being an obstacle for real files: a trailing "TOTAL 5,000" footer row has
// no date and no category, and demanding a category decision for a row that
// is going to be dropped as unparseable either way would block a file that
// has nothing wrong with it.
//
// Grouping is by exact trimmed name, and entries come back in
// first-appearance order — no sort, because the sheet's own order is what
// the user is looking at.
func unresolvedImportCategories(
	rows []importRow,
	categoryMap map[string]int64,
	catNameToID map[string]int64,
	defaultCategoryID int64,
	cur importCurrencies,
) []unresolvedCategory {
	byName := make(map[string]*unresolvedCategory)
	order := make([]*unresolvedCategory, 0, 4)

	for _, row := range rows {
		if row.Skip {
			continue
		}
		if _, _, _, blocked := preCategorySkipReason(row, cur); blocked {
			continue
		}
		if resolveCategoryID(row.Category, categoryMap, catNameToID, defaultCategoryID) != 0 {
			continue
		}

		name := strings.TrimSpace(row.Category)
		entry, seen := byName[name]
		if !seen {
			reason := unresolvedCategoryUnmapped
			if name == "" {
				reason = unresolvedCategoryMissing
			}
			entry = &unresolvedCategory{Name: name, Reason: reason, RowIDs: []int{}}
			byName[name] = entry
			order = append(order, entry)
		}
		entry.RowIDs = append(entry.RowIDs, row.RowID)
	}

	out := make([]unresolvedCategory, 0, len(order))
	for _, entry := range order {
		out = append(out, *entry)
	}
	return out
}

// unknownCategoryIDs returns the ids in the confirm request that name no
// live category, sorted. A stale id is a client bug or a category deleted
// between preview and confirm; either way the rows it covers would land in
// processImportRows' Errored bucket and be folded into an unexplained
// `skipped` count. Naming the ids up front turns that into a message.
func unknownCategoryIDs(req importConfirmRequest, catIDToName map[int64]string) []int64 {
	seen := map[int64]struct{}{}
	var unknown []int64
	check := func(id int64) {
		if id == 0 {
			return
		}
		if _, ok := catIDToName[id]; ok {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		unknown = append(unknown, id)
	}
	check(req.DefaultCategoryID)
	for _, id := range req.CategoryMap {
		check(id)
	}
	sort.Slice(unknown, func(i, j int) bool { return unknown[i] < unknown[j] })
	return unknown
}
