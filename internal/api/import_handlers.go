package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
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
	Skip             bool    `json:"skip"`
}

// dbMatchPreview carries the displayable fields of a live DB row that
// collides with an import row's content hash. Sent inline inside a
// collisionGroup so the frontend can render "you're about to re-import
// this existing transaction" context without a second round-trip. Populated
// only for groups whose reason is "db_match".
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

// buildCollisionGroups computes the collision view for a preview session.
// It groups rows by their resolved content hash, then flags each group as
// intra_file (multiple preview rows sharing a hash with no DB match) or
// db_match (at least one preview row whose hash also matches a live DB
// transaction). Rows marked Skip are EXCLUDED from group membership: a
// skipped row can't collide with anyone because it isn't going to be
// inserted, so counting it would make the progress meter lie.
//
// Rows that fail to resolve to a valid hash — unparseable date, empty
// description, zero amount, unresolved category — are silently omitted
// from grouping. They'll still be rejected at confirm time by
// processImportRows; the preview's job here is to flag collisions, not
// to re-implement the full row validator.
//
// Called from two places:
//  1. handleImportUpload, once at upload time, to seed the initial
//     collision_groups field on the preview response.
//  2. handleImportPatchRow, once per PATCH, to recompute groups after any
//     field edit. The PATCH handler passes the session's mutated row slice.
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
) ([]collisionGroup, error) {
	byHash := make(map[string][]int) // hash -> member row_ids

	// Hashable rows keep their row_id; non-hashable rows are skipped.
	for _, row := range rows {
		if row.Skip {
			continue
		}
		if row.Description == "" || row.Amount == 0 {
			continue
		}
		date, err := parseImportDate(row.Date)
		if err != nil {
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
			dollarsToCents(math.Abs(row.Amount)),
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
//	      preview (skipped from confirm as "negative amount"), but PATCH
//	      does not get to silently zero a cell the user is actively
//	      editing. Returning 400 forces the frontend to surface an inline
//	      error so the user knows the edit did not take effect.
//
//	skip: strict bool. Any non-bool JSON value → INVALID_FIELD. No
//	      normalization — the value is passed through untouched.
//
// Unknown field names → INVALID_FIELD with a message naming the field.
// This is the only path that returns INVALID_FIELD; every other failure
// has a field-specific code so the frontend can color-code the originating
// cell without parsing the message.
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
		if len(trimmed) > MaxDescriptionLength {
			return nil, "INVALID_DESCRIPTION", fmt.Sprintf("description exceeds %d characters", MaxDescriptionLength)
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
			return nil, "INVALID_AMOUNT", "amount is not a valid number"
		}
		// Return dollars so the caller can assign directly to row.Amount
		// (which is declared as float64 dollars, not int64 cents). The
		// ComputeContentHash caller inside buildCollisionGroups multiplies
		// back to cents via dollarsToCents(math.Abs(row.Amount)), so the
		// round-trip is lossless for the values that parseImportAmount
		// accepts (it already rejects magnitudes above MaxTransactionAmount
		// and NaN/Inf).
		return float64(cents) / 100.0, "", ""

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

	// RawCellValue:true tells excelize to skip number-format rendering when
	// returning cell values. Without it, a date cell with number format
	// "mm-dd-yy" renders as "07-21-25", which matches none of the fallback
	// text formats in parseImportDate and silently drops the row. With
	// RawCellValue:true, date cells return their underlying Excel serial
	// number (e.g. "45859"), which parseImportDate converts via
	// excelize.ExcelDateToTime — format-agnostic.
	f, err := excelize.OpenReader(file, excelize.Options{RawCellValue: true})
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

	rows, err := f.GetRows(sheetName)
	if err != nil {
		log.Printf("import: failed to read sheet %q: %v", sheetName, err)
		writeError(w, http.StatusBadRequest, "failed to read spreadsheet data")
		return
	}

	if len(rows) < 2 {
		writeError(w, http.StatusBadRequest, "sheet must have a header row and at least one data row")
		return
	}

	// Scan for the header row: find the first row that contains at least
	// the three required column names (date, description, amount).
	headerIdx := -1
	for i, row := range rows {
		hasDate, hasDesc, hasAmount := false, false, false
		for _, cell := range row {
			normalized := strings.ToLower(strings.TrimSpace(cell))
			if field, found := columnMapping[normalized]; found {
				switch field {
				case "date":
					hasDate = true
				case "description":
					hasDesc = true
				case "amount":
					hasAmount = true
				}
			}
		}
		if hasDate && hasDesc && hasAmount {
			headerIdx = i
			break
		}
	}

	if headerIdx == -1 {
		writeError(w, http.StatusBadRequest, "missing required columns: date, description, amount")
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
	parsedRows := make([]importRow, 0, len(rows)-headerIdx-1)
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
					if val != "" {
						if cents, err := parseImportAmount(val); err == nil {
							ir.OriginalAmount = centsToDollars(cents)
						}
					}
				case "original_currency":
					ir.OriginalCurrency = val
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
	importStore.Store(importID, &importEntry{
		UserID:    user.ID,
		Rows:      parsedRows,
		Columns:   detectedColumns,
		CreatedAt: time.Now(),
	})

	// Collect unique category names from all rows for the mapping UI.
	uniqueCategories := uniqueCategoriesFromRows(parsedRows)

	// Phase 3.4b: the upload preview computes collision_groups via
	// buildCollisionGroups. Unlike the previous upload-time prediction,
	// this pass detects intra-file collisions (the 20-identical-Starbucks
	// case) in addition to DB matches, and returns them in a shape the
	// editable preview table can consume directly. Categories are loaded
	// here so buildCollisionGroups uses the same canonical name resolution
	// as the confirm path.
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

	groups, err := buildCollisionGroups(
		r.Context(),
		h.queries,
		parsedRows,
		nil, // categoryMap not chosen yet at upload time
		0,   // defaultCategoryID not chosen yet either
		catNameToID,
		catIDToName,
	)
	if err != nil {
		log.Printf("import upload: build collision groups: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to compute collision groups")
		return
	}

	// Start background cleanup (idempotent via sync.Once)
	startImportCleanup()

	writeJSON(w, http.StatusOK, map[string]any{
		"import_id":         importID,
		"row_count":         len(parsedRows),
		"rows":              parsedRows,
		"columns":           detectedColumns,
		"unique_categories": uniqueCategories,
		"collision_groups":  groups,
	})
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
	entry.mu.Lock()
	groups, err := buildCollisionGroups(
		r.Context(),
		h.queries,
		entry.Rows,
		req.CategoryMap,
		req.DefaultCategoryID,
		catNameToID,
		catIDToName,
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
	result, minImportDate := processImportRows(r.Context(), qtx, tx, h.txnStore, importProcessInput{
		UserID:            entry.UserID,
		Rows:              filteredRows,
		CategoryMap:       req.CategoryMap,
		DefaultCategoryID: req.DefaultCategoryID,
		CatNameToID:       catNameToID,
		CatIDToName:       catIDToName,
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
	//      amount, negative amount, etc. — see skipReason* in
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
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": len(result.Inserted),
		"skipped":  totalRowCount - len(result.Inserted),
		"total":    totalRowCount,
	})
}

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
// Field is one of "date", "description", "amount", "skip" — validated by
// validateImportField. Value is typed as any so the JSON decoder accepts
// both string (for date/description/amount) and bool (for skip) without
// a second layer of per-field request structs.
type patchImportRowRequest struct {
	Field string `json:"field"`
	Value any    `json:"value"`
}

// patchImportRowErrorBody is the 400 response shape. Code is a stable
// machine-readable constant (INVALID_DATE, INVALID_DESCRIPTION,
// INVALID_AMOUNT, INVALID_FIELD) so the frontend can color-code the
// originating cell without parsing the message. Field echoes back the
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
	case "skip":
		row.Skip = normalized.(bool)
	}

	// Rebuild the collision view against the just-edited session slice.
	// categoryMap and defaultCategoryID are nil/0 — the upload-time path
	// uses those too (see Chunk 1 Task 5 Step 5.2), so the preview-time
	// grouping contract is identical before and after an edit. The
	// canonical category lookups come from the live DB via
	// ListAllCategories.
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

	groups, groupErr := buildCollisionGroups(
		r.Context(),
		h.queries,
		entry.Rows,
		nil,
		0,
		catNameToID,
		catIDToName,
	)
	if groupErr != nil {
		log.Printf("import patch: build collision groups: %v", groupErr)
		writeError(w, http.StatusInternalServerError, "failed to rebuild collision groups")
		return
	}

	// Emit the full ImportPreview shape — same fields as handleImportUpload
	// and handleImportGetSession. The PatchRowResponse TS type aliases
	// ImportPreview, so the frontend's applyResponse spreads the whole
	// object into state; omitting import_id/row_count/columns/
	// unique_categories here caused them to land as `undefined` on the
	// merged preview, which in turn caused the next PATCH to build a
	// URL with `import/undefined/rows/N` and 404, silently dropping
	// every subsequent edit (including un-checking the Skip checkbox).
	// unique_categories is recomputed from the current rows, matching
	// the GET resume handler — if a description edit renames a category
	// cell, that cell's new string is in the snapshot.
	uniqueCats := uniqueCategoriesFromRows(entry.Rows)
	writeJSON(w, http.StatusOK, map[string]any{
		"import_id":         importID,
		"row_count":         len(entry.Rows),
		"rows":              entry.Rows,
		"columns":           entry.Columns,
		"unique_categories": uniqueCats,
		"collision_groups":  groups,
	})
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

	// Load categories for the canonical hash resolution.
	// buildCollisionGroups needs catNameToID (upload-time name match)
	// and catIDToName (canonical name for the hash formula). Same
	// pattern as the Chunk 1 upload call site and the Chunk 2 PATCH
	// call site — keeping the three sites byte-identical makes future
	// refactors easier to audit.
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

	groups, err := buildCollisionGroups(
		r.Context(),
		h.queries,
		entry.Rows,
		nil, // categoryMap not chosen yet at resume time
		0,   // defaultCategoryID not chosen yet either
		catNameToID,
		catIDToName,
	)
	if err != nil {
		log.Printf("import get: build collision groups: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to rebuild collision groups")
		return
	}

	// unique_categories is the sorted-distinct set of category strings
	// seen in entry.Rows. The frontend uses it to seed the
	// category-mapping dropdowns — same as the upload response.
	// Recompute from the current rows (not a cached field) so edits
	// that rename a category cell are reflected in the resume snapshot.
	uniqueCats := uniqueCategoriesFromRows(entry.Rows)

	writeJSON(w, http.StatusOK, map[string]any{
		"import_id":         importID,
		"row_count":         len(entry.Rows),
		"rows":              entry.Rows,
		"columns":           entry.Columns,
		"unique_categories": uniqueCats,
		"collision_groups":  groups,
	})
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
		// Parse date first — every other check below depends on having
		// a valid time.Time to feed into the hash formula, and an
		// unparseable date is the single most common real-world input
		// bug (stray header rows, footer totals, blank rows). Ordering
		// matters for which reason "wins" on a row with multiple
		// defects; the order here matches the pre-refactor behaviour
		// so no existing test changes its outcome label.
		//
		// Phase 3.5 introduced the realistic household ledger window
		// [1900-01-01, 2100-12-31] as a caller-side guard after
		// parseImportDate returned. Phase 3.7 pushed that check into
		// parseImportDate itself (see validateImportYear) so the
		// [1900, 2100] contract is enforced in one place and every
		// future caller — including the fuzz target — inherits it for
		// free. An out-of-window date now returns an error from the
		// parser and routes here as skipReasonUnparseableDate via the
		// dateErr branch. Property test `TestImportProperty_DateSanity`
		// asserts every inserted row lands in this window.
		date, dateErr := parseImportDate(row.Date)
		if dateErr != nil {
			result.Skipped = append(result.Skipped, importSkipped{
				RowIndex: i,
				Reason:   skipReasonUnparseableDate,
			})
			continue
		}

		// Required fields. Description is compared to "" not whitespace —
		// the parsing path already runs strings.TrimSpace before it lands
		// here, so an all-whitespace cell arrives as "". Amount uses
		// math.Abs because negative values in spreadsheets (refunds,
		// credits) are legitimate and get flipped positive at insert
		// time; the policy is "expense/income is determined by category,
		// not amount sign".
		if row.Description == "" {
			result.Skipped = append(result.Skipped, importSkipped{
				RowIndex: i,
				Reason:   skipReasonEmptyDescription,
			})
			continue
		}

		amount := math.Abs(row.Amount)
		if amount == 0 {
			result.Skipped = append(result.Skipped, importSkipped{
				RowIndex: i,
				Reason:   skipReasonZeroAmount,
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
		description := row.Description
		amountCents := dollarsToCents(amount)
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

		// Build params. Amount is expected to already be in base currency
		// (the Excel "Amount (USD)" column). Original amount/currency are
		// stored as-is for reference; no conversion is applied during import.
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
		params := database.CreateTransactionParams{
			UserID:      in.UserID,
			Date:        date,
			AmountCents: amountCents,
			Description: description,
			CategoryID:  categoryID,
			Tags:        toNullString(row.Tags),
			Notes:       toNullString(row.Notes),
			ContentHash: sql.NullString{String: hash, Valid: true},
		}
		if row.OriginalAmount != 0 {
			origAmt := math.Abs(row.OriginalAmount)
			params.OriginalAmountCents = sql.NullInt64{Int64: dollarsToCents(origAmt), Valid: true}
		}
		if row.OriginalCurrency != "" {
			params.OriginalCurrency = sql.NullString{String: row.OriginalCurrency, Valid: true}
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

// stripCurrencyFormat removes currency symbols ($, €, £), commas, and
// whitespace so the string can be parsed as a float. It also converts
// accounting-format negatives like (42.50) to -42.50.
func stripCurrencyFormat(s string) string {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("$", "", "€", "", "£", "", ",", "").Replace(s)
	s = strings.TrimSpace(s)
	// Convert accounting-format negatives: (42.50) → -42.50
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = "-" + s[1:len(s)-1]
	}
	return s
}

// parseImportAmount converts a string from an imported xlsx Amount cell
// (or Original Amount cell) into an int64 cents value. It strips
// currency formatting via stripCurrencyFormat, parses the remainder as
// a float64, and rounds to cents via dollarsToCents.
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
//     ran `stripCurrencyFormat + ParseFloat` inline now route through
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
// Negative values are accepted. Expense spreadsheets frequently encode
// expenses as negatives and the confirm-side insert path flips signs
// based on category type via math.Abs, so rejecting negatives here
// would break that flow.
func parseImportAmount(s string) (int64, error) {
	cleaned := stripCurrencyFormat(s)
	if cleaned == "" {
		return 0, fmt.Errorf("empty amount")
	}
	parsed, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, fmt.Errorf("parse amount %q: %w", s, err)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("non-finite amount: %q", s)
	}
	if math.Abs(parsed) > MaxTransactionAmount {
		return 0, fmt.Errorf("amount out of range: %q", s)
	}
	return dollarsToCents(parsed), nil
}

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
// Priority: explicit category_map > name match against existing categories > default.
func resolveCategoryID(categoryName string, categoryMap map[string]int64, catNameToID map[string]int64, defaultID int64) int64 {
	name := strings.TrimSpace(categoryName)

	// 1. Explicit mapping from the confirm request
	if name != "" && categoryMap != nil {
		if id, found := categoryMap[name]; found {
			return id
		}
	}

	// 2. Case-insensitive match against existing categories
	if name != "" {
		if id, found := catNameToID[strings.ToLower(name)]; found {
			return id
		}
	}

	// 3. Fall back to default
	return defaultID
}
