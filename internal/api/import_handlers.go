package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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

	// Limit upload to 10MB
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing or invalid file upload")
		return
	}
	defer file.Close()

	f, err := excelize.OpenReader(file)
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
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to read sheet %q: %v", sheetName, err))
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
			for colIdx, field := range sec.colIndexToField {
				val := ""
				if colIdx < len(row) {
					val = strings.TrimSpace(row[colIdx])
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
			// Skip completely empty rows
			if ir.Date == "" && ir.Description == "" && ir.Amount == 0 {
				continue
			}
			parsedRows = append(parsedRows, ir)
		}
	}

	if len(parsedRows) == 0 {
		writeError(w, http.StatusBadRequest, "no data rows found")
		return
	}

	const maxImportRows = 10000
	if len(parsedRows) > maxImportRows {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many rows (max %d)", maxImportRows))
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

	// Build preview (first 10 rows)
	previewCount := min(10, len(parsedRows))
	preview := parsedRows[:previewCount]

	// Collect unique category names from ALL rows so the frontend can
	// display the full mapping UI, not just categories from the preview.
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

	writeJSON(w, http.StatusOK, map[string]any{
		"import_id":         importID,
		"row_count":         len(parsedRows),
		"preview":           preview,
		"columns":           detectedColumns,
		"unique_categories": uniqueCategories,
	})
}

// importConfirmRequest is the JSON body for confirming an import.
type importConfirmRequest struct {
	ImportID          string           `json:"import_id"`
	DefaultCategoryID int64            `json:"default_category_id"`
	CategoryMap       map[string]int64 `json:"category_map"`
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
	if err := decodeJSON(r, &req); err != nil {
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

	// Build category name-to-ID lookup from existing categories
	existingCats, err := h.queries.ListAllCategories(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catNameToID := make(map[string]int64, len(existingCats))
	for _, c := range existingCats {
		catNameToID[strings.ToLower(c.Name)] = c.ID
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

	for _, row := range entry.Rows {
		// Parse date
		date, dateErr := parseImportDate(row.Date)
		if dateErr != nil {
			skipped++
			continue
		}

		// Validate required fields
		if row.Description == "" || row.Amount <= 0 {
			skipped++
			continue
		}

		// Resolve category
		categoryID := resolveCategoryID(row.Category, req.CategoryMap, catNameToID, req.DefaultCategoryID)
		if categoryID == 0 {
			skipped++
			continue
		}

		// Build params. Amount is expected to already be in base currency
		// (the Excel "Amount (USD)" column). Original amount/currency are
		// stored as-is for reference; no conversion is applied during import.
		params := database.CreateTransactionParams{
			UserID:      user.ID,
			Date:        date,
			Amount:      row.Amount,
			Description: row.Description,
			CategoryID:  categoryID,
			Tags:        toNullString(row.Tags),
			Notes:       toNullString(row.Notes),
		}

		// Handle original amount/currency if present
		if row.OriginalAmount != 0 {
			params.OriginalAmount = sql.NullFloat64{Float64: row.OriginalAmount, Valid: true}
		}
		if row.OriginalCurrency != "" {
			params.OriginalCurrency = sql.NullString{String: row.OriginalCurrency, Valid: true}
		}

		if _, err := qtx.CreateTransaction(r.Context(), params); err != nil {
			log.Printf("import: failed to insert row (date=%s, desc=%s): %v", sanitizeLogValue(row.Date), sanitizeLogValue(row.Description), err)
			skipped++
			continue
		}

		imported++
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit import")
		return
	}

	// Clean up the import entry
	importStore.Delete(req.ImportID)

	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"skipped":  skipped,
		"total":    len(entry.Rows),
	})
}

// stripCurrencyFormat removes currency symbols ($, €, £), commas, and
// whitespace so the string can be parsed as a float.
func stripCurrencyFormat(s string) string {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("$", "", "€", "", "£", "", ",", "").Replace(s)
	return strings.TrimSpace(s)
}

// parseImportDate tries multiple date formats and returns the first successful
// parse. Returns an error if none match.
func parseImportDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable date: %q", s)
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

