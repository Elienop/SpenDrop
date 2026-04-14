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

// importRow represents a single parsed row from the Excel file.
type importRow struct {
	Date             string  `json:"date"`
	Description      string  `json:"description"`
	Amount           float64 `json:"amount"`
	Category         string  `json:"category"`
	Tags             string  `json:"tags,omitempty"`
	Notes            string  `json:"notes,omitempty"`
	OriginalAmount   float64 `json:"original_amount,omitempty"`
	OriginalCurrency string  `json:"original_currency,omitempty"`
}

// importStore holds pending imports in memory with TTL-based expiry.
var importStore sync.Map

// importTTL is how long an import entry stays valid before expiry.
const importTTL = 30 * time.Minute

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
						cleaned := stripCurrencyFormat(val)
						if parsed, err := strconv.ParseFloat(cleaned, 64); err == nil {
							ir.Amount = parsed
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
						cleaned := stripCurrencyFormat(val)
						if parsed, err := strconv.ParseFloat(cleaned, 64); err == nil {
							ir.OriginalAmount = parsed
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

	imported := 0
	skipped := 0
	// Phase 3.3: track the earliest date across successfully imported
	// rows so the post-commit checkpoint verifier can bound its sweep.
	// A zero value means every row was skipped and the hook is a no-op.
	var minImportDate time.Time

	for i, row := range entry.Rows {
		// Parse date
		date, dateErr := parseImportDate(row.Date)
		if dateErr != nil {
			skipped++
			continue
		}

		// Validate required fields. Use absolute value for amount because the
		// system stores amounts as positive numbers (category type determines
		// expense vs income). Negative values in spreadsheets (refunds/credits)
		// are converted to positive so they are not silently dropped.
		amount := math.Abs(row.Amount)
		if row.Description == "" || amount == 0 {
			skipped++
			continue
		}

		// Resolve category
		categoryID := resolveCategoryID(row.Category, req.CategoryMap, catNameToID, req.DefaultCategoryID)
		if categoryID == 0 {
			skipped++
			continue
		}
		canonicalCategoryName, ok := catIDToName[categoryID]
		if !ok {
			// Caller supplied a category_id that isn't in the DB. This
			// is a client bug (or a racey category deletion); log and
			// skip rather than 500ing mid-batch.
			log.Printf("import: resolved category_id=%d not found in lookup (row desc=%s)", categoryID, sanitizeLogValue(row.Description))
			skipped++
			continue
		}

		// Phase 3.4: compute the content hash from the resolved row
		// identity and check the live index before inserting. Rows
		// listed in ForceAdd bypass the skip and instead mutate the
		// description until a non-colliding hash is found.
		description := row.Description
		amountCents := dollarsToCents(amount)
		hash := database.ComputeContentHash(date, amountCents, description, canonicalCategoryName)
		_, forceAdd := forceAddSet[i]
		if !forceAdd {
			// Ordinary path: look up the hash and skip on a hit. The
			// lookup runs on qtx so it observes any rows inserted
			// earlier in this very batch — importing a spreadsheet
			// that contains the same row twice detects the second
			// occurrence as a duplicate of the first within the same
			// commit, which is the correct answer for a household
			// shared ledger.
			_, lookupErr := qtx.GetTransactionByContentHash(r.Context(), sql.NullString{String: hash, Valid: true})
			if lookupErr == nil {
				skipped++
				continue
			}
			if !errors.Is(lookupErr, sql.ErrNoRows) {
				log.Printf("import: content hash lookup failed (row=%d): %v", i, lookupErr)
				skipped++
				continue
			}
		} else {
			// Force-add path: append " (N)" to the description and
			// loop until the resulting hash does not collide in the
			// DB. Start at 2 because "Coffee" and "Coffee (2)" read
			// naturally as a first and second occurrence to the user.
			// The cap of 1000 is defensive: under pathological input
			// (a merchant with 999 legitimate same-day same-amount
			// transactions) we'd rather fail loudly than spin
			// forever. In practice the loop almost always terminates
			// at n=2.
			suffixed, suffixedHash, suffixErr := resolveForceAddSuffix(
				r.Context(), qtx, description, date, amountCents, canonicalCategoryName,
			)
			if suffixErr != nil {
				log.Printf("import: force-add suffix failed (row=%d): %v", i, suffixErr)
				skipped++
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
		// amount. The cents value is derived from the same float parsed out
		// of the spreadsheet so a round-trip export->import is lossless for
		// any representable money amount.
		//
		// Phase 3.4: content_hash is populated from the resolved row
		// identity computed above. The partial unique index guarantees
		// the insert fails loudly if the dup check above raced a parallel
		// import of the same row — there is no silent double-insert path.
		params := database.CreateTransactionParams{
			UserID:      user.ID,
			Date:        date,
			Amount:      amount,
			AmountCents: amountCents,
			Description: description,
			CategoryID:  categoryID,
			Tags:        toNullString(row.Tags),
			Notes:       toNullString(row.Notes),
			ContentHash: sql.NullString{String: hash, Valid: true},
		}

		// Handle original amount/currency if present
		if row.OriginalAmount != 0 {
			origAmt := math.Abs(row.OriginalAmount)
			params.OriginalAmount = sql.NullFloat64{Float64: origAmt, Valid: true}
			params.OriginalAmountCents = sql.NullInt64{Int64: dollarsToCents(origAmt), Valid: true}
		}
		if row.OriginalCurrency != "" {
			params.OriginalCurrency = sql.NullString{String: row.OriginalCurrency, Valid: true}
		}

		if _, err := qtx.CreateTransaction(r.Context(), params); err != nil {
			log.Printf("import: failed to insert row (date=%s, desc=%s): %v", sanitizeLogValue(row.Date), sanitizeLogValue(description), err)
			skipped++
			continue
		}

		minImportDate = earliestDate(minImportDate, date)
		imported++
	}

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

	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"skipped":  skipped,
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
// An unparseable date returns an error; the caller counts it as skipped.
//
// Note: the serial-date path assumes the 1900 date system (the default in
// modern Excel on every platform). Legacy Mac Excel files that set the 1904
// date system flag are not detected here — their dates will be off by ~4
// years. In practice this is a non-issue for SpenDrop because modern Excel
// and Google Sheets both write 1900-based workbooks.
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
				return t, nil
			}
		}
	}
	// Strategy 2: text formats.
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable date: %q", s)
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
	return "", "", fmt.Errorf("no free suffix in [2, %d]", forceAddSuffixCap)
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
