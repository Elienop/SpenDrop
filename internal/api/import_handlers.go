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
// until the user confirms the import.
type importEntry struct {
	UserID    int64
	Rows      []importRow
	Columns   []string
	CreatedAt time.Time
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
					ir.Date = val
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

	// Limit pending imports per user (max 3 concurrent)
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

	// Start background cleanup (idempotent via sync.Once)
	startImportCleanup()

	// Collect unique category names from all rows for the mapping UI.
	seen := make(map[string]struct{})
	uniqueCategories := make([]string, 0)
	for _, row := range parsedRows {
		if row.Category != "" {
			if _, ok := seen[row.Category]; !ok {
				seen[row.Category] = struct{}{}
				uniqueCategories = append(uniqueCategories, row.Category)
			}
		}
	}

	// Phase 3.4 upload-time duplicate prediction. Best-effort: we don't
	// have the user's category_map yet (that's on the confirm request),
	// so we predict using only the case-insensitive name match against
	// existing DB categories. Rows whose category doesn't resolve here
	// are omitted from the prediction — they'll either be mapped
	// explicitly at confirm time (and checked there) or fall to the
	// default category (and checked there). A false-negative from the
	// preview is acceptable because handleImportConfirm runs the
	// authoritative check against the live index before every insert.
	predictedSkips := predictDuplicateSkips(r.Context(), h.queries, parsedRows)

	writeJSON(w, http.StatusOK, map[string]any{
		"import_id":         importID,
		"row_count":         len(parsedRows),
		"rows":              parsedRows,
		"columns":           detectedColumns,
		"unique_categories": uniqueCategories,
		"predicted_skips":   predictedSkips,
	})
}

// predictDuplicateSkips walks the parsed preview rows and returns the
// subset that would collide with an existing live transaction's content
// hash if imported as-is. The check mirrors the confirm-time path exactly
// except for category resolution: because the upload endpoint fires
// before the user picks a category_map or default_category_id, this
// function only predicts for rows whose spreadsheet category name has an
// unambiguous case-insensitive match against an existing DB category.
// Rows that don't match are silently skipped in the prediction — the
// authoritative check still runs in handleImportConfirm, so a
// false-negative here just means the UI doesn't grey the row out
// ahead of time.
//
// The hash formula and DB lookup MUST agree with the confirm path. We
// delegate to database.ComputeContentHash and
// queries.GetTransactionByContentHash so there is no chance of drift.
//
// A DB error on any single row is logged and the row is dropped from
// the prediction set — we do not abort the upload. The prediction is a
// UX nicety, not a correctness gate; the worst case of a DB outage
// here is that the user sees a less-informative preview, then hits
// the real check at confirm time.
func predictDuplicateSkips(ctx context.Context, queries *database.Queries, rows []importRow) []predictedSkip {
	// Materialize the category lookups in one query. Upload payloads
	// are capped at MaxImportRows so the memory cost is bounded, and
	// loading categories once keeps the hash-prediction loop DB-free
	// until the actual GetTransactionByContentHash call.
	cats, err := queries.ListAllCategories(ctx)
	if err != nil {
		log.Printf("import preview: list categories: %v", err)
		return []predictedSkip{}
	}
	nameToID := make(map[string]int64, len(cats))
	idToName := make(map[int64]string, len(cats))
	for _, c := range cats {
		nameToID[strings.ToLower(c.Name)] = c.ID
		idToName[c.ID] = c.Name
	}

	skips := []predictedSkip{}
	for i, row := range rows {
		// Short-circuit rows that will never produce a valid hash.
		// These mirror the confirm-time "skip" reasons so the preview
		// stays consistent with the eventual outcome.
		if row.Description == "" || row.Amount == 0 {
			continue
		}
		date, err := parseImportDate(row.Date)
		if err != nil {
			continue
		}
		// Case-insensitive match against the existing DB categories.
		// Rows whose spreadsheet category doesn't land on an existing
		// category (or whose category cell is empty) can't be hashed
		// with the DB name, so we skip them from the prediction.
		name := strings.TrimSpace(row.Category)
		if name == "" {
			continue
		}
		catID, ok := nameToID[strings.ToLower(name)]
		if !ok {
			continue
		}
		canonical := idToName[catID]

		hash := database.ComputeContentHash(
			date,
			dollarsToCents(math.Abs(row.Amount)),
			row.Description,
			canonical,
		)
		existing, err := queries.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true})
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("import preview: lookup hash for row %d: %v", i, err)
			}
			continue
		}
		skips = append(skips, predictedSkip{
			RowIndex:   i,
			Reason:     "duplicate",
			ExistingID: existing.ID,
		})
	}
	return skips
}

// predictedSkip describes one row that the upload-time duplicate check
// believes will be rejected at confirm time because its content hash
// collides with an already-imported live row. The frontend uses this to
// grey out the row in the preview and offer a "force-add" checkbox; the
// authoritative check still happens in handleImportConfirm.
//
// RowIndex is the 0-based position of the row in the parsed preview (the
// same index the frontend uses to render the row list), so the force-add
// opt-in can refer back to the row by index without round-tripping the
// entire row payload.
//
// ExistingID is the id of the live DB row that collides. It is exposed so
// the frontend can deep-link to the duplicate in the transactions list
// ("this row was first imported on 2026-01-17; open it"), and so operators
// reading the JSON response can trace a false-positive back to the data.
//
// Reason is always "duplicate" for this phase — the field exists as a
// discriminator so later phases can add reasons like "archived" or
// "locked_period" without breaking the shape.
type predictedSkip struct {
	RowIndex   int    `json:"row_index"`
	Reason     string `json:"reason"`
	ExistingID int64  `json:"existing_id"`
}

// importConfirmRequest is the JSON body for confirming an import.
//
// ForceAdd lists the RowIndex values of predicted duplicates that the
// user has explicitly ticked "import anyway" on. Rows listed here skip
// the duplicate check and instead append a " (N)" suffix to their
// description until the resulting content hash no longer collides. The
// UI renders this mutation loudly so the user knows what they're
// agreeing to — a forced duplicate is a legitimate-but-distinct row
// (e.g. two identical coffees on the same day) and the suffix is what
// disambiguates them in charts and totals.
type importConfirmRequest struct {
	ImportID          string           `json:"import_id"`
	DefaultCategoryID int64            `json:"default_category_id"`
	CategoryMap       map[string]int64 `json:"category_map"`
	ForceAdd          []int            `json:"force_add"`
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
// used by predictedSkip on the upload preview path, so a frontend that
// renders both preview skips and confirm-time outcomes can match
// strings byte-for-byte without a translation table.
type importSkipReason string

const (
	skipReasonEmptyDescription  importSkipReason = "empty_description"
	skipReasonZeroAmount        importSkipReason = "zero_amount"
	skipReasonUnparseableDate   importSkipReason = "unparseable_date"
	skipReasonMissingCategory   importSkipReason = "missing_category"
	skipReasonDuplicate         importSkipReason = "duplicate"
	skipReasonForceAddCollision importSkipReason = "force_add_collision"
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
	ForceAddSet       map[int]struct{}
	CatNameToID       map[string]int64
	CatIDToName       map[int64]string
}

// errForceAddExhausted is the sentinel returned by resolveForceAddSuffix
// when no free " (N)" suffix exists within [2, forceAddSuffixCap]. The
// processor maps this to skipReasonForceAddCollision, while any other
// error from the suffix loop is treated as a DB fault and flows into
// the Errored bucket. Separating the two reasons prevents a DB blip
// from silently burning a force-add slot.
var errForceAddExhausted = errors.New("force-add suffix exhausted")

// handleImportConfirm inserts all rows from a previously uploaded import
// into the transactions table.
func (h *Handler) handleImportConfirm(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req importConfirmRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.ImportID) != 32 {
		writeError(w, http.StatusBadRequest, "invalid import_id")
		return
	}

	// Look up import entry
	val, found := importStore.Load(req.ImportID)
	if !found {
		writeError(w, http.StatusNotFound, "import not found or expired")
		return
	}
	entry := val.(*importEntry)

	// Verify ownership
	if entry.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Check expiry
	if time.Since(entry.CreatedAt) > importTTL {
		importStore.Delete(req.ImportID)
		writeError(w, http.StatusGone, "import has expired")
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

	// Phase 3.4: materialize force-add row indices into a set for
	// O(1) lookup. The request ships them as a slice for ergonomics;
	// we flip them into a map once rather than doing a linear scan
	// inside the per-row hot path. Empty slice → empty map → every
	// row flows through the ordinary duplicate check.
	forceAddSet := make(map[int]struct{}, len(req.ForceAdd))
	for _, idx := range req.ForceAdd {
		forceAddSet[idx] = struct{}{}
	}

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
	result, minImportDate := processImportRows(r.Context(), qtx, importProcessInput{
		UserID:            user.ID,
		Rows:              entry.Rows,
		CategoryMap:       req.CategoryMap,
		DefaultCategoryID: req.DefaultCategoryID,
		ForceAddSet:       forceAddSet,
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

	// The HTTP response still reports the two-way split (imported vs
	// skipped) for backwards compatibility with the existing frontend
	// and the Phase 3.4 regression tests. Errored rows — DB faults, bad
	// category_ids — are folded into the `skipped` count because from
	// the user's perspective they are indistinguishable ("this row did
	// not land"). The structured importResult is only observed by
	// property tests that call processImportRows directly.
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": len(result.Inserted),
		"skipped":  len(result.Skipped) + len(result.Errored),
		"total":    len(entry.Rows),
	})
}

// handleImportCancel removes a pending import entry from memory so the
// per-user slot is freed immediately (instead of waiting for TTL expiry).
func (h *Handler) handleImportCancel(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	importID := chi.URLParam(r, "id")
	if len(importID) != 32 {
		writeError(w, http.StatusBadRequest, "invalid import_id")
		return
	}

	val, found := importStore.Load(importID)
	if !found {
		// Already gone — treat as success
		w.WriteHeader(http.StatusNoContent)
		return
	}

	entry := val.(*importEntry)
	if entry.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	importStore.Delete(importID)
	w.WriteHeader(http.StatusNoContent)
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
		// identity and check the live index before inserting. Rows
		// listed in ForceAddSet bypass the skip and instead mutate the
		// description until a non-colliding hash is found.
		description := row.Description
		amountCents := dollarsToCents(amount)
		hash := database.ComputeContentHash(date, amountCents, description, canonicalCategoryName)
		_, forceAdd := in.ForceAddSet[i]

		if !forceAdd {
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
		} else {
			// Force-add path: append " (N)" to the description and
			// loop until the resulting hash does not collide. An
			// exhausted suffix cap maps to skipReasonForceAddCollision;
			// any other error is a DB fault and goes to Errored. The
			// split is why resolveForceAddSuffix wraps its exhaustion
			// error with the errForceAddExhausted sentinel.
			suffixed, suffixedHash, suffixErr := resolveForceAddSuffix(
				ctx, qtx, description, date, amountCents, canonicalCategoryName,
			)
			if suffixErr != nil {
				if errors.Is(suffixErr, errForceAddExhausted) {
					result.Skipped = append(result.Skipped, importSkipped{
						RowIndex: i,
						Reason:   skipReasonForceAddCollision,
					})
				} else {
					log.Printf("import: force-add suffix failed (row=%d): %v", i, suffixErr)
					result.Errored = append(result.Errored, importErrored{
						RowIndex: i,
						Reason:   sanitizeLogValue(suffixErr.Error()),
					})
				}
				continue
			}
			description = suffixed
			hash = suffixedHash
		}

		// Build params. Amount is expected to already be in base currency
		// (the Excel "Amount (USD)" column). Original amount/currency are
		// stored as-is for reference; no conversion is applied during import.
		//
		// Phase 3.1a: dual-write amount_cents alongside the legacy REAL
		// amount. The cents value is derived from the same float parsed
		// out of the spreadsheet so a round-trip export->import is
		// lossless for any representable money amount.
		//
		// Phase 3.4: content_hash is populated from the resolved row
		// identity computed above. The partial unique index guarantees
		// the insert fails loudly if the dup check above raced a parallel
		// import of the same row — there is no silent double-insert path.
		params := database.CreateTransactionParams{
			UserID:      in.UserID,
			Date:        date,
			Amount:      amount,
			AmountCents: amountCents,
			Description: description,
			CategoryID:  categoryID,
			Tags:        toNullString(row.Tags),
			Notes:       toNullString(row.Notes),
			ContentHash: sql.NullString{String: hash, Valid: true},
		}
		if row.OriginalAmount != 0 {
			origAmt := math.Abs(row.OriginalAmount)
			params.OriginalAmount = sql.NullFloat64{Float64: origAmt, Valid: true}
			params.OriginalAmountCents = sql.NullInt64{Int64: dollarsToCents(origAmt), Valid: true}
		}
		if row.OriginalCurrency != "" {
			params.OriginalCurrency = sql.NullString{String: row.OriginalCurrency, Valid: true}
		}

		if _, err := qtx.CreateTransaction(ctx, params); err != nil {
			log.Printf("import: failed to insert row (date=%s, desc=%s): %v", sanitizeLogValue(row.Date), sanitizeLogValue(description), err)
			result.Errored = append(result.Errored, importErrored{
				RowIndex: i,
				Reason:   sanitizeLogValue(err.Error()),
			})
			continue
		}

		minDate = earliestDate(minDate, date)
		result.Inserted = append(result.Inserted, importInserted{
			RowIndex:    i,
			Date:        date,
			AmountCents: amountCents,
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
// The [1900, 2100] window is intentionally wider than the year picker's
// [MinYear, MaxYear] = [2000, 2100] from limits.go. Import needs to
// accept historic bank statements going back to the 20th century, while
// the picker only drives budget/savings UI. Tightening import to MinYear
// would break users loading 1990s statements.
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
// parseImportDate accepts. Declared as function-local-ish constants
// (package-level for the fuzz test to reference) rather than inline
// literals so every reader sees the same numbers when investigating a
// "why did my 1990 import row get skipped?" question. See
// parseImportDate's doc comment for the full rationale.
const (
	minImportYear = 1900
	maxImportYear = 2100
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

// forceAddSuffixCap is the ceiling on how many " (N)" suffixes
// resolveForceAddSuffix will try before giving up. Under realistic
// household data the loop terminates at n=2 or n=3; the cap is defensive
// against a pathological input (say, a merchant that is legitimately
// charged 500+ times on the same day for the same amount, which we
// never see, but which would otherwise spin forever).
const forceAddSuffixCap = 1000

// resolveForceAddSuffix finds the smallest " (N)" suffix (starting at
// N=2) such that the content hash of the suffixed description does not
// collide with an existing live row. It returns the suffixed description
// and its hash.
//
// The function runs inside the same sqlc transaction as the surrounding
// import so an earlier row in this batch that landed with "(2)" is
// visible to a later row looking for "(3)". If the cap is exhausted
// without finding a non-colliding suffix, the error is surfaced to the
// caller — the import handler logs it and counts the row as skipped,
// preserving the "no silent doubles" invariant at the cost of the
// legitimate-but-pathological row.
func resolveForceAddSuffix(
	ctx context.Context,
	qtx *database.Queries,
	description string,
	date time.Time,
	amountCents int64,
	categoryName string,
) (string, string, error) {
	for n := 2; n <= forceAddSuffixCap; n++ {
		candidate := fmt.Sprintf("%s (%d)", description, n)
		hash := database.ComputeContentHash(date, amountCents, candidate, categoryName)
		_, err := qtx.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true})
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, hash, nil
		}
		if err != nil {
			return "", "", fmt.Errorf("lookup suffix %q: %w", candidate, err)
		}
		// err == nil → hash hit, try the next suffix.
	}
	return "", "", fmt.Errorf("no free suffix in [2, %d]: %w", forceAddSuffixCap, errForceAddExhausted)
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
