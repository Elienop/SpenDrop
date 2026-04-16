package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/elienop/spendrop/internal/database"
)

// clearImportStore removes all entries from the package-level importStore.
// Because importStore is a global sync.Map shared across all tests in the
// process, entries from one test's upload linger into the next test.
// User IDs also collide (each fresh test DB starts auto-increment from 1),
// so a user in test N looks like the same user as the same-index user in
// test M — causing the "too many pending imports" 429 when 3+ uploads
// accumulate for what the store considers one user ID.
// Call this at the top of any test that calls handleImportUpload.
func clearImportStore() {
	importStore.Range(func(key, _ any) bool {
		importStore.Delete(key)
		return true
	})
}

// countTransactionsForUser returns the number of live (non-tombstoned)
// transactions for the given user, bypassing the soft-delete filter at the
// SQL layer. Use this when a test needs to assert inserts/skips at the DB
// level without depending on the list-transactions handler's filters.
func countTransactionsForUser(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM transactions WHERE user_id = ? AND deleted_at IS NULL",
		userID,
	).Scan(&n); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	return n
}

// createTestXLSX builds an in-memory xlsx file with the given sheet name,
// headers, and data rows, returning the bytes.
func createTestXLSX(t *testing.T, sheetName string, headers []string, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()

	// Rename the default sheet
	defaultSheet := f.GetSheetName(0)
	if defaultSheet != sheetName {
		idx, err := f.NewSheet(sheetName)
		if err != nil {
			t.Fatalf("new sheet: %v", err)
		}
		f.SetActiveSheet(idx)
		f.DeleteSheet(defaultSheet)
	}

	// Write headers
	for col, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		f.SetCellValue(sheetName, cell, h)
	}

	// Write data rows
	for rowIdx, row := range rows {
		for col, val := range row {
			cell, _ := excelize.CoordinatesToCellName(col+1, rowIdx+2)
			f.SetCellValue(sheetName, cell, val)
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write xlsx to buffer: %v", err)
	}
	return buf.Bytes()
}

// nativeDateRow describes one row for createTestXLSXWithNativeDateCells.
// Date is stored as a real Excel date cell (numeric serial + date style),
// the remaining string columns are written as plain text.
type nativeDateRow struct {
	Date time.Time
	Rest []string
}

// createTestXLSXWithNativeDateCells builds an xlsx where column A of each
// data row is a native Excel date cell styled with dateNumFmt (e.g.
// "mm-dd-yy" or "d-mmm-yyyy"). This reproduces how real-world budget files
// store dates, so we can exercise the RawCellValue:true serial date path in
// the importer — which used to silently drop rows when the display format
// was anything other than the five hardcoded text layouts.
func createTestXLSXWithNativeDateCells(t *testing.T, sheetName string, headers []string, dateNumFmt string, rows []nativeDateRow) []byte {
	t.Helper()
	f := excelize.NewFile()

	defaultSheet := f.GetSheetName(0)
	if defaultSheet != sheetName {
		idx, err := f.NewSheet(sheetName)
		if err != nil {
			t.Fatalf("new sheet: %v", err)
		}
		f.SetActiveSheet(idx)
		f.DeleteSheet(defaultSheet)
	}

	for col, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		f.SetCellValue(sheetName, cell, h)
	}

	styleID, err := f.NewStyle(&excelize.Style{CustomNumFmt: &dateNumFmt})
	if err != nil {
		t.Fatalf("new style: %v", err)
	}

	for rowIdx, row := range rows {
		dateCell, _ := excelize.CoordinatesToCellName(1, rowIdx+2)
		if err := f.SetCellValue(sheetName, dateCell, row.Date); err != nil {
			t.Fatalf("set date cell: %v", err)
		}
		if err := f.SetCellStyle(sheetName, dateCell, dateCell, styleID); err != nil {
			t.Fatalf("set cell style: %v", err)
		}
		for col, val := range row.Rest {
			cell, _ := excelize.CoordinatesToCellName(col+2, rowIdx+2)
			f.SetCellValue(sheetName, cell, val)
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write xlsx to buffer: %v", err)
	}
	return buf.Bytes()
}

// postMultipartFile creates a multipart request with the given xlsx bytes
// as a file upload under the field name "file".
func postMultipartFile(t *testing.T, url string, xlsxData []byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.xlsx")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(xlsxData)); err != nil {
		t.Fatalf("copy to part: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, url, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// patchImportRow builds a PATCH request for the PATCH /api/import/{importID}/rows/{rowID}
// endpoint and wires the chi URL params through withUserAndURLParams. Returns
// an httptest.ResponseRecorder so the caller can assert on status and body.
// Keeping this helper in the test file (not production) avoids coupling
// production code to the chi router's mock helpers.
func patchImportRow(t *testing.T, h *Handler, user database.User, importID string, rowID int, field string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"field": field, "value": value})
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/import/"+importID+"/rows/"+strconv.Itoa(rowID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserAndURLParams(req, user, map[string]string{
		"importID": importID,
		"rowID":    strconv.Itoa(rowID),
	})
	rec := httptest.NewRecorder()
	h.handleImportPatchRow(rec, req)
	return rec
}

// --- handleImportUpload ---

func TestHandleImportUpload_ValidFile_ReturnsPreview(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "importer", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category", "Tags", "Notes",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food", "weekly", "Whole Foods"},
		{"2026-01-16", "Electric bill", "120.00", "Utilities", "", "January"},
		{"2026-01-17", "Coffee", "5.75", "Food", "coffee", ""},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	if resp["import_id"] == nil || resp["import_id"].(string) == "" {
		t.Fatal("expected non-empty import_id")
	}
	if int(resp["row_count"].(float64)) != 3 {
		t.Errorf("expected row_count 3, got %v", resp["row_count"])
	}
	rows, ok := resp["rows"].([]any)
	if !ok {
		t.Fatal("expected rows to be an array")
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
	columns, ok := resp["columns"].([]any)
	if !ok {
		t.Fatal("expected columns to be an array")
	}
	if len(columns) != 6 {
		t.Errorf("expected 6 columns, got %d", len(columns))
	}
}

func TestHandleImportUpload_AlternateHeaders_Works(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "importer2", "member")

	xlsxData := createTestXLSX(t, "Sheet1", []string{
		"Transaction Date", "Description", "Amount (USD)",
	}, [][]string{
		{"2026-02-01", "Lunch", "12.50"},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if int(resp["row_count"].(float64)) != 1 {
		t.Errorf("expected row_count 1, got %v", resp["row_count"])
	}
}

func TestHandleImportUpload_MissingRequiredColumns_Returns400(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "importer3", "member")

	// Missing Amount column
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description",
	}, [][]string{
		{"2026-01-15", "Groceries"},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleImportUpload_Unauthenticated_Returns401(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50"},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	// No user context
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleImportUpload_PreviewCappedAt10(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "importer4", "member")

	// Create 15 rows
	rows := make([][]string, 15)
	for i := range rows {
		rows[i] = []string{"2026-01-15", "Item", "10.00"}
	}

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, rows)

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	respRows := resp["rows"].([]any)
	if len(respRows) != 15 {
		t.Errorf("expected all 15 rows, got %d", len(respRows))
	}
	if int(resp["row_count"].(float64)) != 15 {
		t.Errorf("expected row_count 15, got %v", resp["row_count"])
	}
}

// --- handleImportConfirm ---

func TestHandleImportConfirm_ValidImport_InsertsTransactions(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "confirmer", "member")

	// Upload first to get import_id
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food"},
		{"2026-01-16", "Electric bill", "120.00", "Utilities"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Get the seeded Food category ID (from migrations)
	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	var foodID int64
	catMap := make(map[string]float64)
	for _, c := range cats {
		if c.Name == "Food" || c.Name == "Groceries" {
			foodID = c.ID
		}
		catMap[c.Name] = float64(c.ID)
	}
	if foodID == 0 {
		// Use first category as default
		foodID = cats[0].ID
	}

	// Confirm the import
	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": foodID,
		"category_map":        catMap,
	})

	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()

	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)

	if int(confirmResp["total"].(float64)) != 2 {
		t.Errorf("expected total 2, got %v", confirmResp["total"])
	}
	if int(confirmResp["imported"].(float64)) != 2 {
		t.Errorf("expected imported 2, got %v", confirmResp["imported"])
	}
	if int(confirmResp["skipped"].(float64)) != 0 {
		t.Errorf("expected skipped 0, got %v", confirmResp["skipped"])
	}
}

func TestHandleImportConfirm_InvalidImportID_Returns404(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "confirmer2", "member")

	// Must be exactly 32 hex chars to pass the length check and reach the
	// store lookup, which then returns 404 because no such entry exists.
	body, _ := json.Marshal(map[string]any{
		"import_id":           "00000000000000000000000000000000",
		"default_category_id": 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportConfirm(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleImportConfirm_WrongUser_Returns403(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user1 := seedTestUser(t, q, "uploader1", "member")
	user2 := seedTestUser(t, q, "attacker1", "member")

	// User1 uploads
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50"},
	})
	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user1)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// User2 tries to confirm
	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": 1,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user2)
	confirmRec := httptest.NewRecorder()

	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}
}

func TestHandleImportConfirm_MultipleDateFormats_Parsed(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "dateimporter", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "ISO format", "10.00"},
		{"01/15/2026", "US format", "20.00"},
		{"1/5/2026", "US short", "30.00"},
		{"15-Jan-2026", "Day-Mon-Year", "40.00"},
		{"2026/01/15", "Slash format", "50.00"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	cats, _ := q.ListAllCategories(context.Background())
	defaultCatID := cats[0].ID

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": defaultCatID,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()

	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, confirmRec, &resp)
	if int(resp["imported"].(float64)) != 5 {
		t.Errorf("expected 5 imported (all date formats), got %v", resp["imported"])
	}
}

func TestHandleImportConfirm_SkipsUnparseableDate(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "skipdate", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Good date", "10.00"},
		{"not-a-date", "Bad date", "20.00"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	cats, _ := q.ListAllCategories(context.Background())

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": cats[0].ID,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()

	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, confirmRec, &resp)
	if int(resp["imported"].(float64)) != 1 {
		t.Errorf("expected 1 imported, got %v", resp["imported"])
	}
	if int(resp["skipped"].(float64)) != 1 {
		t.Errorf("expected 1 skipped, got %v", resp["skipped"])
	}
}

// TestHandleImportConfirm_NativeDateCells_mmddyyFormat is the regression test
// for the silent-drop bug where July/August 2025 budget files imported only
// 24/29 of their 62/99 rows. The files used native Excel date cells with
// number format "mm-dd-yy", which GetRows() rendered as "07-21-25" — a
// string that matched none of the fallback text layouts in parseImportDate.
// With the RawCellValue:true fix, date cells return their underlying serial
// number regardless of display format, and parseImportDate handles them via
// excelize.ExcelDateToTime.
func TestHandleImportConfirm_NativeDateCells_mmddyyFormat(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "mmddyyimporter", "member")

	xlsxData := createTestXLSXWithNativeDateCells(t, "Transactions",
		[]string{"Date", "Description", "Amount"},
		"mm-dd-yy",
		[]nativeDateRow{
			{Date: time.Date(2025, 7, 21, 0, 0, 0, 0, time.UTC), Rest: []string{"transfer to ado", "180.30"}},
			{Date: time.Date(2025, 7, 27, 0, 0, 0, 0, time.UTC), Rest: []string{"supermarket", "45.00"}},
			{Date: time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC), Rest: []string{"fuel", "60.00"}},
		})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	cats, _ := q.ListAllCategories(context.Background())

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": cats[0].ID,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()

	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, confirmRec, &resp)
	if int(resp["imported"].(float64)) != 3 {
		t.Errorf("expected 3 imported (all mm-dd-yy rows), got %v (skipped=%v)",
			resp["imported"], resp["skipped"])
	}
	if int(resp["skipped"].(float64)) != 0 {
		t.Errorf("expected 0 skipped, got %v", resp["skipped"])
	}
}

// TestHandleImportConfirm_NativeDateCells_FormatAgnostic confirms the fix is
// format-agnostic: the same three native date cells styled with different
// display formats all parse to the same underlying dates. This guards
// against regressions that would add format-specific handling back in.
func TestHandleImportConfirm_NativeDateCells_FormatAgnostic(t *testing.T) {
	formats := []string{
		"mm-dd-yy",
		"m/d/yyyy",
		"d-mmm-yyyy",
		"yyyy-mm-dd",
	}
	for _, fmtStr := range formats {
		t.Run(fmtStr, func(t *testing.T) {
			clearImportStore()
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "fmtimporter_"+fmtStr, "member")

			xlsxData := createTestXLSXWithNativeDateCells(t, "Transactions",
				[]string{"Date", "Description", "Amount"},
				fmtStr,
				[]nativeDateRow{
					{Date: time.Date(2025, 7, 21, 0, 0, 0, 0, time.UTC), Rest: []string{"row one", "10.00"}},
					{Date: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), Rest: []string{"row two", "20.00"}},
				})

			uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
			uploadReq = withUser(uploadReq, user)
			uploadRec := httptest.NewRecorder()
			h.handleImportUpload(uploadRec, uploadReq)

			if uploadRec.Code != http.StatusOK {
				t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
			}

			var uploadResp map[string]any
			decodeResponse(t, uploadRec, &uploadResp)
			importID := uploadResp["import_id"].(string)

			cats, _ := q.ListAllCategories(context.Background())

			confirmBody, _ := json.Marshal(map[string]any{
				"import_id":           importID,
				"default_category_id": cats[0].ID,
			})
			confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
			confirmReq = withUser(confirmReq, user)
			confirmRec := httptest.NewRecorder()

			h.handleImportConfirm(confirmRec, confirmReq)

			if confirmRec.Code != http.StatusOK {
				t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
			}

			var resp map[string]any
			decodeResponse(t, confirmRec, &resp)
			if int(resp["imported"].(float64)) != 2 {
				t.Errorf("format %q: expected 2 imported, got %v (skipped=%v)",
					fmtStr, resp["imported"], resp["skipped"])
			}
		})
	}
}

func TestHandleImportConfirm_CategoryMatchByName(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "catmatcher", "member")

	// Seed categories that exist in DB
	cats, _ := q.ListAllCategories(context.Background())
	// Use first category name in the data
	catName := cats[0].Name

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Matched row", "10.00", catName},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Confirm without explicit category_map — should auto-match by name
	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": cats[0].ID,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()

	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, confirmRec, &resp)
	if int(resp["imported"].(float64)) != 1 {
		t.Errorf("expected 1 imported, got %v", resp["imported"])
	}
}

func TestHandleImportConfirm_NegativeAmounts_ConvertedToAbsolute(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "refundimporter", "member")

	// Negative amounts in spreadsheets (refunds/credits) should be converted
	// to absolute values during import, not silently skipped. The system uses
	// category type to distinguish expense vs income, not amount sign.
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food"},
		{"2026-01-16", "Refund from store", "-15.00", "Food"},
		{"2026-01-17", "Credit adjustment", "-5.75", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	if int(uploadResp["row_count"].(float64)) != 3 {
		t.Errorf("expected row_count 3 (including negatives), got %v", uploadResp["row_count"])
	}

	cats, _ := q.ListAllCategories(context.Background())
	defaultCatID := cats[0].ID

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": defaultCatID,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()

	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, confirmRec, &resp)
	// All 3 rows should be imported — negatives are converted to absolute values
	if int(resp["imported"].(float64)) != 3 {
		t.Errorf("expected 3 imported (negatives converted to abs), got %v; skipped=%v", resp["imported"], resp["skipped"])
	}
	if int(resp["skipped"].(float64)) != 0 {
		t.Errorf("expected 0 skipped, got %v", resp["skipped"])
	}
}

func TestStripCurrencyFormat_AccountingNegatives(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"$42.50", "42.50"},
		{"-$15.00", "-15.00"},
		{"($42.50)", "-42.50"},
		{"(€1,234.56)", "-1234.56"},
		{"(£100.00)", "-100.00"},
		{"$ (42.50)", "-42.50"},
		{"1,234.56", "1234.56"},
		{" $42.50 ", "42.50"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := stripCurrencyFormat(tc.input)
			if got != tc.expected {
				t.Errorf("stripCurrencyFormat(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// TestParseImportDate pins the semantics of parseImportDate so future changes
// don't silently alter how the importer interprets Date cells. Covers:
//   - whitespace and empty input
//   - Excel serial date range boundaries [1, 2958465]
//   - household-ledger-year window [1900, 2100]
//   - fractional serials (date + time-of-day)
//   - all five text date formats
//   - obviously-bad inputs
//
// Phase 3.7 note: the FuzzParseImportDate corpus surfaced two serial
// boundary cases — serial 1 (→ 1899-12-31) and serial 2958465 (→ 9999-
// 12-31) — that satisfy the Excel serial range check but fall outside
// any plausible household ledger window. parseImportDate now enforces
// a [1900, 2100] year bound after successful parse via
// validateImportYear; these cases moved from "success" to
// "expected error" below.
//
// The "stray small integer" quirk for mid-range serials is partially
// mitigated but not eliminated. "1234" still parses to 1903-05-18
// (year 1903 is inside [1900, 2100]), so a misaligned integer column
// can still produce a very early Excel date — the user would notice
// it landing in the 1903 bucket. Tightening further would break
// legitimate imports of pre-2000 bank statements; the [1900, 2100]
// window is the compromise.
func TestParseImportDate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantStr string // "2006-01-02" format; ignored if wantErr
	}{
		// Serial date boundaries — pre-Phase-3.7 these returned the
		// boundary dates (1899-12-31 / 9999-12-31); now rejected by
		// the [1900, 2100] household ledger guard.
		{"serial 1 maps to 1899-12-31, below year window", "1", true, ""},
		{"serial 45859 (2025-07-21)", "45859", false, "2025-07-21"},
		{"serial max 2958465 maps to 9999-12-31, above year window", "2958465", true, ""},
		{"serial above max falls through to text and fails", "2958466", true, ""},
		{"serial 0 below range falls through and fails", "0", true, ""},
		{"negative number falls through and fails", "-5", true, ""},
		{"fractional serial with time component", "45859.5", false, "2025-07-21"},

		// Year-window boundaries via ISO text. These pin both ends of
		// the accepted window so a future window adjustment shows up
		// as a failing case here rather than as a silent semantic
		// drift. Using ISO text (not Excel serials) keeps the test
		// independent of excelize's leap-year arithmetic.
		{"iso 1899-12-31 below window", "1899-12-31", true, ""},
		{"iso 1900-01-01 at lower bound", "1900-01-01", false, "1900-01-01"},
		{"iso 2100-12-31 at upper bound", "2100-12-31", false, "2100-12-31"},
		{"iso 2101-01-01 above window", "2101-01-01", true, ""},

		// Text formats
		{"iso yyyy-mm-dd", "2025-07-21", false, "2025-07-21"},
		{"us padded mm/dd/yyyy", "07/21/2025", false, "2025-07-21"},
		{"us short m/d/yyyy", "7/5/2025", false, "2025-07-05"},
		{"day-mon-year", "21-Jul-2025", false, "2025-07-21"},
		{"slash yyyy/mm/dd", "2025/07/21", false, "2025-07-21"},

		// Empty / whitespace / invalid
		{"empty string", "", true, ""},
		{"whitespace only", "   ", true, ""},
		{"whitespace around serial", " 45859 ", false, "2025-07-21"},
		{"whitespace around text date", "  2025-07-21  ", false, "2025-07-21"},
		{"obviously not a date", "not-a-date", true, ""},
		{"mm-dd-yy text format not in allowlist", "07-21-25", true, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseImportDate(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseImportDate(%q) = %v, want error", tc.input, got.Format("2006-01-02"))
				}
				return
			}
			if err != nil {
				t.Errorf("parseImportDate(%q) unexpected error: %v", tc.input, err)
				return
			}
			if gotStr := got.Format("2006-01-02"); gotStr != tc.wantStr {
				t.Errorf("parseImportDate(%q) = %q, want %q", tc.input, gotStr, tc.wantStr)
			}
		})
	}
}

func TestHandleImportUpload_AccountingNegatives_ParsedCorrectly(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "acctformat", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Normal purchase", "$42.50"},
		{"2026-01-16", "Refund (parens)", "($15.00)"},
		{"2026-01-17", "Negative sign", "-$5.75"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	if uploadRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, uploadRec, &resp)

	// All 3 rows should be parsed — the accounting format (parens) must not
	// result in Amount=0 which would still pass the upload skip filter but
	// silently lose the amount value.
	if int(resp["row_count"].(float64)) != 3 {
		t.Errorf("expected row_count 3, got %v", resp["row_count"])
	}

	rows, ok := resp["rows"].([]any)
	if !ok || len(rows) < 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	// Verify the accounting-format row parsed the amount correctly
	row2 := rows[1].(map[string]any)
	if row2["amount"].(float64) != -15.00 {
		t.Errorf("expected accounting-negative amount -15.00, got %v", row2["amount"])
	}

	row3 := rows[2].(map[string]any)
	if row3["amount"].(float64) != -5.75 {
		t.Errorf("expected negative amount -5.75, got %v", row3["amount"])
	}
}

func TestHandleImportConfirm_ZeroAmount_Skipped(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "zeroimporter", "member")

	// Zero-amount rows should be skipped (they have no financial meaning)
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Real purchase", "42.50"},
		{"2026-01-16", "Zero row", "0"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	cats, _ := q.ListAllCategories(context.Background())

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": cats[0].ID,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()

	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, confirmRec, &resp)
	// Only the real purchase should be imported; the zero-amount row skipped
	if int(resp["imported"].(float64)) != 1 {
		t.Errorf("expected 1 imported, got %v", resp["imported"])
	}
	if int(resp["skipped"].(float64)) != 1 {
		t.Errorf("expected 1 skipped (zero amount), got %v", resp["skipped"])
	}
}

// --- handleImportCancel ---

func TestHandleImportCancel_OwnerCanCancel(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "canceler", "member")

	// Upload to get an import_id
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50"},
	})
	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Cancel the import using chi URL param
	req := httptest.NewRequest(http.MethodDelete, "/api/import/"+importID, nil)
	req = withUserAndURLParam(req, user, "importID", importID)
	rec := httptest.NewRecorder()

	h.handleImportCancel(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel: expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify the import is gone
	_, found := importStore.Load(importID)
	if found {
		t.Error("expected import to be deleted after cancel")
	}
}

func TestHandleImportCancel_WrongUser_Returns403(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user1 := seedTestUser(t, q, "owner", "member")
	user2 := seedTestUser(t, q, "attacker", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50"},
	})
	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user1)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)

	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// User2 tries to cancel user1's import
	req := httptest.NewRequest(http.MethodDelete, "/api/import/"+importID, nil)
	req = withUserAndURLParam(req, user2, "importID", importID)
	rec := httptest.NewRecorder()

	h.handleImportCancel(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleImportConfirm_Unauthenticated_Returns401(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	body, _ := json.Marshal(map[string]any{
		"import_id":           "whatever",
		"default_category_id": 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleImportConfirm(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// --- Phase 3.4 idempotent imports ---

// uploadAndConfirmImport runs the full two-step import flow against the
// handler once: upload the xlsx bytes, pull the import_id, then POST the
// confirm. Returns the parsed confirm response so the caller can assert
// on imported/skipped counts without reconstructing the plumbing each
// time. Any non-200 along the way is a t.Fatal — the Phase 3.4 tests all
// exercise the happy-path wiring and want the assertions on the end-state
// counts, not on the transport.
func uploadAndConfirmImport(t *testing.T, h *Handler, user database.User, xlsxData []byte) map[string]any {
	t.Helper()
	ctx := context.Background()

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	cats, err := h.queries.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	catMap := make(map[string]float64, len(cats))
	var defaultID int64
	for _, c := range cats {
		catMap[c.Name] = float64(c.ID)
		if c.Name == "Food" {
			defaultID = c.ID
		}
	}
	if defaultID == 0 {
		defaultID = cats[0].ID
	}

	confirmBodyMap := map[string]any{
		"import_id":           importID,
		"default_category_id": defaultID,
		"category_map":        catMap,
	}
	confirmBody, _ := json.Marshal(confirmBodyMap)

	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}
	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)
	return confirmResp
}

// TestHandleImport_DoubleImport_SkipsDuplicates locks in the Phase 3.4b
// acceptance criterion that importing the same file twice is now
// rejected, not silently deduped. The first confirm inserts two rows
// with content_hash set; the second upload surfaces both rows as
// db_match collision groups in the preview, and the second confirm —
// with no PATCH to resolve the collisions — returns 409
// UNRESOLVED_COLLISIONS with the full groups array. No partial insert.
//
// This supersedes the Phase 3.4 "silent skip" behavior: Phase 3.4
// would have returned 200 with imported=0/skipped=2 and a dedup log
// line in processImportRows. After Chunk 3 the re-check inside
// handleImportConfirm catches the duplicates first and the transaction
// is never even opened.
func TestHandleImport_DoubleImport_SkipsDuplicates(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "dbl", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food"},
		{"2026-01-16", "Electric bill", "120.00", "Utilities"},
	})

	first := uploadAndConfirmImport(t, h, user, xlsxData)
	if int(first["imported"].(float64)) != 2 {
		t.Fatalf("first import: expected imported=2, got %v", first["imported"])
	}
	if int(first["skipped"].(float64)) != 0 {
		t.Errorf("first import: expected skipped=0, got %v", first["skipped"])
	}

	// Second upload: both rows should surface as db_match collision
	// groups in the preview. The upload itself still returns 200 —
	// only the confirm is rejected.
	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("second upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Second confirm: no PATCH was performed to resolve the
	// collisions, so the handler-level re-check must fire a 409.
	cats, err := h.queries.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	catMap := make(map[string]float64, len(cats))
	var defaultID int64
	for _, c := range cats {
		catMap[c.Name] = float64(c.ID)
		if c.Name == "Food" {
			defaultID = c.ID
		}
	}
	if defaultID == 0 {
		defaultID = cats[0].ID
	}

	confirmBodyMap := map[string]any{
		"import_id":           importID,
		"default_category_id": defaultID,
		"category_map":        catMap,
	}
	confirmBody, _ := json.Marshal(confirmBodyMap)
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusConflict {
		t.Fatalf("second confirm: expected 409 UNRESOLVED_COLLISIONS, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}
	var confirmErr map[string]any
	decodeResponse(t, confirmRec, &confirmErr)
	if code, _ := confirmErr["code"].(string); code != "UNRESOLVED_COLLISIONS" {
		t.Errorf("expected code=UNRESOLVED_COLLISIONS, got %v", confirmErr["code"])
	}
	groups, ok := confirmErr["collision_groups"].([]any)
	if !ok || len(groups) != 2 {
		t.Fatalf("expected 2 collision_groups, got %T len %d", confirmErr["collision_groups"], len(groups))
	}
	for i, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			t.Errorf("group %d: not a map, got %T", i, g)
			continue
		}
		if reason, _ := gm["reason"].(string); reason != "db_match" {
			t.Errorf("group %d: expected reason=db_match, got %v", i, gm["reason"])
		}
	}

	// DB sanity: still only two live rows — the rejected second
	// confirm must not have inserted anything partially.
	var live int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE deleted_at IS NULL`,
	).Scan(&live); err != nil {
		t.Fatalf("count live transactions: %v", err)
	}
	if live != 2 {
		t.Errorf("expected 2 live rows after double-import, got %d", live)
	}
}

// TestHandleImportUpload_CollisionGroups_ReflectsExistingRows verifies that
// the upload-time preview surfaces db_match collision groups for rows that
// would collide at confirm time, so the inline editor can pre-flag the row
// without a round-trip. We seed the DB via the real import flow (rather
// than a synthetic CreateTransaction with a hand-computed hash) so the
// test exercises the same path an operator hits in production, and so a
// drift between buildCollisionGroups and handleImportConfirm's hash formula
// shows up here instead of hiding behind a test fixture that matches only
// one of the two.
func TestHandleImportUpload_CollisionGroups_ReflectsExistingRows(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "predictor", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food"},
		{"2026-01-16", "Coffee", "5.75", "Food"},
	})

	// Seed by running a first upload+confirm. After this the DB has the
	// two hashes; a second upload should surface both as db_match groups.
	if resp := uploadAndConfirmImport(t, h, user, xlsxData); int(resp["imported"].(float64)) != 2 {
		t.Fatalf("seed import: expected imported=2, got %v", resp["imported"])
	}

	// Second upload: preview only — don't confirm. We want to inspect
	// collision_groups in the upload response body.
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rec, &resp)

	rawGroups, ok := resp["collision_groups"].([]any)
	if !ok {
		t.Fatalf("expected collision_groups in response, got %T", resp["collision_groups"])
	}
	if len(rawGroups) != 2 {
		t.Fatalf("expected 2 collision_groups (one per seeded row), got %d: %v", len(rawGroups), rawGroups)
	}
	for _, rg := range rawGroups {
		g := rg.(map[string]any)
		if g["reason"].(string) != "db_match" {
			t.Errorf("expected reason=db_match, got %v", g["reason"])
		}
		members := g["member_row_ids"].([]any)
		if len(members) != 1 {
			t.Errorf("expected 1 member_row_id (single preview row matching a live DB row), got %d", len(members))
		}
		dbMatch, ok := g["db_match"].(map[string]any)
		if !ok {
			t.Fatalf("expected db_match payload, got %T", g["db_match"])
		}
		if int64(dbMatch["id"].(float64)) == 0 {
			t.Errorf("expected non-zero db_match.id (preserves the existing_id invariant)")
		}
	}

	// Verify the live DB state is what we expect (guards against a bug
	// where the handler accidentally mutates the seed rows).
	var count int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE deleted_at IS NULL`,
	).Scan(&count); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 live rows, got %d", count)
	}
}

// TestHandleImportUpload_PredictedSkips_IgnoresTombstoned was deleted in 3.4b — superseded by TestHandleImportUpload_HidesTombstonedFromDbMatch which asserts the same tombstone invariant against collision_groups.

// TestHandleImport_ReimportAfterTombstone_SucceedsOnConfirm is the
// confirm-path sibling of HidesTombstonedFromDbMatch (the upload-path
// counterpart, Phase 3.4b). The upload preview correctly reports no
// collision groups against a tombstoned row — but that is only half the
// contract. The confirm call also has to actually land the re-insert.
// This test fails under a partial unique index that does not filter
// deleted_at (the INSERT hits UNIQUE on the tombstoned row's hash and is
// counted as skipped), and passes once the migration includes AND
// deleted_at IS NULL in the index WHERE clause.
//
// Regression for: "user trashes a row, re-imports the spreadsheet, row
// silently does not come back." The Phase 2.1 trash-then-reimport UX
// promise is only kept when the partial unique index and the
// GetTransactionByContentHash filter agree on "tombstoned rows are
// invisible."
func TestHandleImport_ReimportAfterTombstone_SucceedsOnConfirm(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "reimporter", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food"},
	})

	// Seed via the real import path so the row lands with a real hash.
	// Inserting via CreateTransaction directly would couple this test to
	// the exact ComputeContentHash formula and risk drifting from it.
	if resp := uploadAndConfirmImport(t, h, user, xlsxData); int(resp["imported"].(float64)) != 1 {
		t.Fatalf("seed import: expected imported=1, got %v", resp["imported"])
	}

	// Tombstone the seeded row. Raw UPDATE keeps the test decoupled
	// from the trash handler — we only need the row in the tombstoned
	// state, not the audit row.
	if _, err := db.ExecContext(context.Background(),
		`UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP`,
	); err != nil {
		t.Fatalf("tombstone row: %v", err)
	}

	// Re-import the exact same spreadsheet through upload+confirm. The
	// row must come back live. GetTransactionByContentHash filters
	// deleted_at IS NULL so the prediction step returns no duplicates,
	// and the partial unique index must also filter deleted_at IS NULL
	// so the INSERT does not collide with the tombstoned row still
	// carrying its original hash.
	resp := uploadAndConfirmImport(t, h, user, xlsxData)
	if got := int(resp["imported"].(float64)); got != 1 {
		t.Errorf("expected imported=1 after reimport of tombstoned row, got %d (resp=%v)", got, resp)
	}
	if got := int(resp["skipped"].(float64)); got != 0 {
		t.Errorf("expected skipped=0 after reimport of tombstoned row, got %d (resp=%v)", got, resp)
	}

	// Verify DB state: one live row (the re-imported one), one
	// tombstoned row (the original we trashed).
	var live, tombstoned int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE deleted_at IS NULL`,
	).Scan(&live); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE deleted_at IS NOT NULL`,
	).Scan(&tombstoned); err != nil {
		t.Fatalf("count tombstoned: %v", err)
	}
	if live != 1 {
		t.Errorf("expected 1 live row after reimport, got %d", live)
	}
	if tombstoned != 1 {
		t.Errorf("expected 1 tombstoned row after reimport, got %d", tombstoned)
	}
}

// TestHandleImportUpload_IntraFileCollisionGroup is spec test #9 (baseline):
// uploading three identical rows produces exactly one collision_group of
// size 3 with reason intra_file. The initial grouping is the foundation
// every PATCH test depends on — if this breaks, nothing else works.
func TestHandleImportUpload_IntraFileCollisionGroup(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "grouping", "member")

	// Migration 001 already seeds "Food", so buildCollisionGroups can
	// resolve the category name at upload-time grouping without any
	// extra setup here.

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-07", "Starbucks", "5.00", "Food"},
		{"2026-01-07", "Starbucks", "5.00", "Food"},
		{"2026-01-07", "Starbucks", "5.00", "Food"},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	rawGroups, ok := resp["collision_groups"].([]any)
	if !ok {
		t.Fatalf("collision_groups missing or wrong type: %v", resp["collision_groups"])
	}
	if len(rawGroups) != 1 {
		t.Fatalf("expected 1 collision_group, got %d", len(rawGroups))
	}
	g := rawGroups[0].(map[string]any)
	if g["reason"].(string) != "intra_file" {
		t.Errorf("expected reason intra_file, got %v", g["reason"])
	}
	members := g["member_row_ids"].([]any)
	if len(members) != 3 {
		t.Errorf("expected 3 member_row_ids, got %d", len(members))
	}
	// Rows must carry row_id and it must equal their slice index.
	rows := resp["rows"].([]any)
	for i, r := range rows {
		m := r.(map[string]any)
		if int(m["row_id"].(float64)) != i {
			t.Errorf("row %d has row_id %v, want %d", i, m["row_id"], i)
		}
	}
}

// TestHandleImportUpload_HidesTombstonedFromDbMatch is spec test #8 Part A:
// a tombstoned DB row with the same content_hash as an uploaded row must
// NOT be flagged as a db_match collision. The partial unique index already
// filters out tombstoned rows (WHERE deleted_at IS NULL), so
// GetTransactionByContentHash returns sql.ErrNoRows and the uploaded row
// stays clean. This test pins that invariant so a future "performance"
// rewrite can't silently drop the filter.
//
// Seeds use amount=999 as a sentinel so a regression is easy to read in
// the failure message — any production row with that exact amount in a
// household DB is vanishingly unlikely.
func TestHandleImportUpload_HidesTombstonedFromDbMatch(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "tombstoned", "member")

	// Resolve the default "Food" category (seeded by migration 001) so
	// the insert below can reference its ID.
	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	var foodID int64
	for _, c := range cats {
		if c.Name == "Food" {
			foodID = c.ID
			break
		}
	}
	if foodID == 0 {
		t.Fatalf("default Food category not found")
	}

	date := time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)
	hash := database.ComputeContentHash(date, 99900, "Starbucks", "Food")

	// Insert a tombstoned row with this hash. It must not be discoverable
	// by GetTransactionByContentHash (the query filters deleted_at IS NULL)
	// which is what buildCollisionGroups relies on.
	created, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		Date:        date,
		Amount:      999.00,
		AmountCents: 99900,
		Description: "Starbucks",
		CategoryID:  foodID,
		ContentHash: sql.NullString{String: hash, Valid: true},
	})
	if err != nil {
		t.Fatalf("create tombstone row: %v", err)
	}
	// Tombstone via raw UPDATE on purpose: we need the deleted_at shape
	// without the audit side-effect of a real soft-delete, so this
	// deliberately bypasses TransactionStore. The test asserts on the
	// query layer (content-hash lookup respecting deleted_at IS NULL),
	// not on the store layer's audit contract.
	if _, err := db.Exec("UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", created.ID); err != nil {
		t.Fatalf("tombstone row: %v", err)
	}

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-07", "Starbucks", "999.00", "Food"},
	})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rec, &resp)
	groups := resp["collision_groups"].([]any)
	if len(groups) != 0 {
		t.Errorf("expected 0 collision_groups (tombstoned row should not match), got %d: %v", len(groups), groups)
	}
}

// TestHandleImportPatchRow_HappyPath_UnbreaksRowFromGroupOfThree verifies the
// core PATCH contract: an edit that changes a field feeding the content hash
// causes the edited row to flip clean and the remaining two rows to stay
// grouped (now as a pair, not a triple). Owns the "stale group_id after
// re-hash" bug class — if the handler fails to rebuild groups from scratch
// and instead mutates-in-place the old group list, the departing row's
// row_id will linger in the old group members slice.
func TestHandleImportPatchRow_HappyPath_UnbreaksRowFromGroupOfThree(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "patcher", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	// Three identical rows — same date, description, amount, category.
	// At upload time, buildCollisionGroups groups them into one intra_file
	// group of 3 members.
	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)
	uploadGroups := upload["collision_groups"].([]any)
	if len(uploadGroups) != 1 {
		t.Fatalf("expected 1 collision group at upload, got %d: %v", len(uploadGroups), uploadGroups)
	}
	uploadMembers := uploadGroups[0].(map[string]any)["member_row_ids"].([]any)
	if len(uploadMembers) != 3 {
		t.Fatalf("expected 3 members in upload group, got %d", len(uploadMembers))
	}

	// PATCH row 0's date to something unique. The edit should flip row 0 to
	// clean and leave rows 1 and 2 still grouped together.
	patchRec := patchImportRow(t, h, user, importID, 0, "date", "2025-08-15")
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}

	// Assert the response shape: rows list still has 3 entries, and the
	// collision_groups list now has exactly one group with 2 members
	// (row_ids 1 and 2).
	rows := resp["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows in response, got %d", len(rows))
	}
	row0 := rows[0].(map[string]any)
	if row0["date"].(string) != "2025-08-15" {
		t.Errorf("expected row 0 date=2025-08-15 after PATCH, got %v", row0["date"])
	}

	groups := resp["collision_groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected 1 remaining collision group after PATCH, got %d: %v", len(groups), groups)
	}
	members := groups[0].(map[string]any)["member_row_ids"].([]any)
	if len(members) != 2 {
		t.Fatalf("expected 2 members in remaining group, got %d: %v", len(members), members)
	}
	seen := map[int]bool{}
	for _, m := range members {
		seen[int(m.(float64))] = true
	}
	if !seen[1] || !seen[2] {
		t.Errorf("expected members to be {1, 2}, got %v", members)
	}
	if seen[0] {
		t.Errorf("row 0 should have left the group after PATCH, but is still listed: %v", members)
	}
}

// TestHandleImportPatchRow_ResponseMatchesImportPreviewShape is the shape
// regression test for a real bug caught during the Phase 3.4b smoke test:
// the PATCH handler returned only {rows, collision_groups}, but the
// PatchRowResponse TypeScript alias equals ImportPreview, so the hook's
// applyResponse spread set import_id/row_count/columns/unique_categories
// to undefined on local state. That silently broke every follow-on PATCH
// — the next request built `/import/undefined/rows/N` and 404'd, so a
// user could mark a row skipped (first PATCH lands cleanly) but could
// NOT un-check it (second PATCH dies on the undefined import_id URL).
//
// This test owns the full response-shape contract so a future refactor
// can't quietly drop one of those fields again. We assert the presence
// of every field the upload handler emits, with value checks strong
// enough to catch "return zero value for this field" regressions.
func TestHandleImportPatchRow_ResponseMatchesImportPreviewShape(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "shapechecker", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	// Two rows with two different category names so unique_categories is
	// non-empty AND len > 1 — the contract check below needs to see a
	// populated slice. "Food" is already a default seeded category in
	// setupTestDB so it resolves without re-seeding.
	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-02", "Lunch", "12.00", "Food"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)

	// Toggle skip on row 0 — the exact user gesture that surfaced the bug
	// (click the Skip checkbox → first PATCH lands, second PATCH fails).
	patchRec := patchImportRow(t, h, user, importID, 0, "skip", true)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}

	// Contract: every field ImportPreview declares must be present. Check
	// each one with a strong assertion — "field exists and is non-zero" —
	// so returning `nil` or the zero value for any slot still fails.
	if got, _ := resp["import_id"].(string); got != importID {
		t.Errorf("expected import_id=%q, got %v", importID, resp["import_id"])
	}
	if got, _ := resp["row_count"].(float64); int(got) != 2 {
		t.Errorf("expected row_count=2, got %v", resp["row_count"])
	}
	rows, _ := resp["rows"].([]any)
	if len(rows) != 2 {
		t.Errorf("expected rows len=2, got %d", len(rows))
	}
	cols, _ := resp["columns"].([]any)
	if len(cols) == 0 {
		t.Errorf("expected columns to be populated, got %v", resp["columns"])
	}
	uniq, _ := resp["unique_categories"].([]any)
	if len(uniq) == 0 {
		t.Errorf("expected unique_categories to be populated, got %v", resp["unique_categories"])
	}
	if _, ok := resp["collision_groups"]; !ok {
		t.Errorf("expected collision_groups key to be present (even if empty array)")
	}

	// Cross-check the "second PATCH succeeds" invariant — if the first
	// response's shape regresses, the hook's `const importID =
	// preview.import_id` path produces undefined and this second PATCH
	// would 404 against `/import/undefined/rows/0`. We simulate the hook's
	// behavior by reading import_id out of the first response and reusing
	// it, the same way applyResponse flows through state.
	secondImportID := resp["import_id"].(string)
	secondPatch := patchImportRow(t, h, user, secondImportID, 0, "skip", false)
	if secondPatch.Code != http.StatusOK {
		t.Fatalf("second patch (un-skip): expected 200, got %d: %s", secondPatch.Code, secondPatch.Body.String())
	}
	var second map[string]any
	if err := json.Unmarshal(secondPatch.Body.Bytes(), &second); err != nil {
		t.Fatalf("unmarshal second patch response: %v", err)
	}
	secondRows, _ := second["rows"].([]any)
	if len(secondRows) == 0 {
		t.Fatalf("expected rows in second patch response, got %v", second)
	}
	row0 := secondRows[0].(map[string]any)
	if row0["skip"] != false {
		t.Errorf("expected row 0 skip=false after un-skip PATCH, got %v", row0["skip"])
	}
}

// TestHandleImportPatchRow_ReCollision_PreservesSkip owns the skip-sticky
// invariant from the spec's Edge Cases table: once a row is marked skip=true,
// no subsequent edit ever clears that flag. Here we mark a colliding row as
// skipped, then PATCH its date so it STOPS colliding — the response must
// still carry skip=true on the row and must NOT include the row in
// collision_groups (because skipped rows are excluded from grouping entirely).
//
// Also owns the stateful-regrouping invariant: the server holds session
// state across PATCHes, so a second PATCH's group view must reflect the
// first PATCH's mutation.
func TestHandleImportPatchRow_ReCollision_PreservesSkip(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "stickyskipper", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)

	// Mark row 0 as skipped. The response should drop the collision group
	// entirely (1 remaining member cannot form a collision with itself).
	skipRec := patchImportRow(t, h, user, importID, 0, "skip", true)
	if skipRec.Code != http.StatusOK {
		t.Fatalf("patch skip: expected 200, got %d: %s", skipRec.Code, skipRec.Body.String())
	}
	var skipResp map[string]any
	if err := json.Unmarshal(skipRec.Body.Bytes(), &skipResp); err != nil {
		t.Fatalf("unmarshal skip response: %v", err)
	}
	if groups := skipResp["collision_groups"].([]any); len(groups) != 0 {
		t.Fatalf("expected 0 collision groups after skipping row 0, got %d: %v", len(groups), groups)
	}
	skipRow0 := skipResp["rows"].([]any)[0].(map[string]any)
	if skipRow0["skip"] != true {
		t.Fatalf("expected row 0 skip=true after skip PATCH, got %v", skipRow0["skip"])
	}

	// Now PATCH the skipped row's date to a unique value. The row is
	// already skipped, so this edit does not change group membership
	// (skipped rows are excluded from grouping). The critical invariant:
	// skip MUST stay true. A handler bug that resets fields-other-than-
	// edited to their zero values would silently un-skip the row.
	datePatch := patchImportRow(t, h, user, importID, 0, "date", "2025-08-15")
	if datePatch.Code != http.StatusOK {
		t.Fatalf("patch date: expected 200, got %d: %s", datePatch.Code, datePatch.Body.String())
	}
	var dateResp map[string]any
	if err := json.Unmarshal(datePatch.Body.Bytes(), &dateResp); err != nil {
		t.Fatalf("unmarshal date response: %v", err)
	}
	dateRow0 := dateResp["rows"].([]any)[0].(map[string]any)
	if dateRow0["skip"] != true {
		t.Errorf("expected skip=true to persist after date PATCH, got %v", dateRow0["skip"])
	}
	if dateRow0["date"].(string) != "2025-08-15" {
		t.Errorf("expected date=2025-08-15 after PATCH, got %v", dateRow0["date"])
	}
	if groups := dateResp["collision_groups"].([]any); len(groups) != 0 {
		t.Fatalf("expected 0 collision groups (row 0 still skipped), got %d: %v", len(groups), groups)
	}
}

// TestHandleImportPatchRow_ExpiredSession_Returns404 owns the session
// expiry backend half. We upload normally, reach into the importStore to
// rewind CreatedAt past the importTTL (60 minutes after Chunk 1 Task 1
// bumps it), then PATCH. The shared loadImportEntryForUser helper from
// Chunk 1 Task 2 evicts the expired entry and returns 404.
//
// Rewinding CreatedAt is the canonical way to test TTL expiry without
// fake-clock machinery — the helper uses time.Since(entry.CreatedAt), so
// mutating CreatedAt backwards is equivalent to fast-forwarding the wall
// clock. The store lookup still works (we wrote into the sync.Map directly
// by import_id) and the mutation is safe because we're the only goroutine
// reading it in this test.
func TestHandleImportPatchRow_ExpiredSession_Returns404(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "expirysub", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)

	// Rewind the entry's CreatedAt by 2 hours. importTTL is 60 minutes, so
	// time.Since(entry.CreatedAt) now exceeds the limit and the helper
	// returns 404.
	val, ok := importStore.Load(importID)
	if !ok {
		t.Fatalf("importStore.Load returned !ok for import_id=%s", importID)
	}
	entry := val.(*importEntry)
	entry.CreatedAt = time.Now().Add(-2 * time.Hour)

	patchRec := patchImportRow(t, h, user, importID, 0, "description", "Starbucks Reserve")
	if patchRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on expired session, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
}

// TestHandleImportPatchRow_WhitespaceCasePreservesCollision owns the
// hash-normalization parity bug class: upload-time hash and PATCH-time
// re-hash MUST use the same code path inside ComputeContentHash. A
// whitespace+case variation on description is the canonical canary —
// if either path normalizes differently, the groups diverge and the
// edited row spuriously flips clean.
//
// Setup: two rows with exactly matching description "Starbucks" form an
// intra_file group of 2. PATCH row 0's description to " STARBUCKS " (with
// wrapping whitespace and uppercased). After re-hash, the normalized form
// is still "starbucks" — ComputeContentHash lowercases and trims — so the
// group membership must be preserved.
func TestHandleImportPatchRow_WhitespaceCasePreservesCollision(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "hashparity", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)
	uploadGroups := upload["collision_groups"].([]any)
	if len(uploadGroups) != 1 {
		t.Fatalf("expected 1 collision group at upload, got %d", len(uploadGroups))
	}

	// PATCH row 0's description to a whitespace+case variant. Post-trim
	// the stored value is "STARBUCKS" (trim happens in validateImportField
	// but case is preserved in the row value — only the hash normalizes
	// case). The hash must still equal row 1's hash, so the group
	// stays intact.
	patchRec := patchImportRow(t, h, user, importID, 0, "description", " STARBUCKS ")
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}

	// The displayed row value is "STARBUCKS" (trimmed, case preserved) —
	// this asserts that validateImportField stores the trimmed form, which
	// is what the frontend will render.
	row0 := resp["rows"].([]any)[0].(map[string]any)
	if row0["description"].(string) != "STARBUCKS" {
		t.Errorf("expected row 0 description=STARBUCKS after trim, got %q", row0["description"])
	}

	// The group must still exist with both members. If the hash diverged
	// due to a normalization mismatch, this would collapse to zero groups
	// and the test would catch the regression.
	groups := resp["collision_groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected 1 collision group after whitespace+case PATCH, got %d: %v", len(groups), groups)
	}
	members := groups[0].(map[string]any)["member_row_ids"].([]any)
	if len(members) != 2 {
		t.Fatalf("expected 2 members (hash parity preserved), got %d: %v", len(members), members)
	}
}

// TestHandleImportPatchRow_HidesTombstonedFromDbMatch owns the second
// half of the soft-delete leak guard (Part A lives in
// TestHandleImportUpload_HidesTombstonedFromDbMatch from Chunk 1 Task 6).
//
// Setup: seed one LIVE row and one TOMBSTONED row that share the same
// content hash. The amount=999 sentinel on the tombstoned row is the
// SpenDrop project convention for "this should not appear in any
// aggregate" — if a reader forgets the deleted_at filter, the test will
// fail loudly with 999 somewhere in the db_match payload.
//
// Upload a single row whose content matches NEITHER seeded row. The upload
// response shows zero collision groups. Then PATCH the uploaded row's
// DATE so that after re-hashing it matches the tombstoned row's content
// hash. A correct handler runs the hash lookup through
// GetTransactionByContentHash at queries.sql:182-197 (which filters
// t.deleted_at IS NULL) and returns zero collision groups. A bug that
// reads hashes without the soft-delete filter would flag the row as a
// db_match against the tombstoned transaction.
//
// Important: this test does NOT assert on any collision group containing
// amount=999. The correct response has no matching group at all; we
// verify absence, not presence. Asserting presence-with-sentinel would
// be a "test of the bug" (only passes if the leak exists), which is
// exactly the anti-pattern the CLAUDE.md soft-delete discipline warns
// against.
func TestHandleImportPatchRow_HidesTombstonedFromDbMatch(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "tombstoner", "member")
	cat := seedTestCategory(t, q, "Coffee", "expense")

	// Seed one live + one tombstoned row that would share a content hash
	// if the import row's date were shifted to 2025-08-15. The live row
	// sits on a DIFFERENT hash (different date), so the import row does
	// not collide with it at upload time. The tombstoned row IS on the
	// hash space the PATCH will move into.
	liveDate := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	tombstoneDate := time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC)

	liveHash := database.ComputeContentHash(liveDate, 500, "Starbucks", "Coffee")
	_, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		CategoryID:  cat.ID,
		Date:        liveDate,
		Amount:      5.00,
		AmountCents: 500,
		Description: "Starbucks",
		ContentHash: sql.NullString{String: liveHash, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed live row: %v", err)
	}

	// Tombstoned row uses amount=999 as the sentinel so a soft-delete
	// leak would be trivially visible in any assertion that touched its
	// amount. Its hash is computed against amount=99900 cents.
	tombstoneHash := database.ComputeContentHash(tombstoneDate, 99900, "Starbucks", "Coffee")
	tombstoned, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		CategoryID:  cat.ID,
		Date:        tombstoneDate,
		Amount:      999.00,
		AmountCents: 99900,
		Description: "Starbucks",
		ContentHash: sql.NullString{String: tombstoneHash, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed tombstoned row: %v", err)
	}
	// Tombstone via raw UPDATE on purpose: we need the deleted_at shape
	// without the audit side-effect of a real soft-delete, so this
	// deliberately bypasses TransactionStore. The test asserts on the
	// query layer (content-hash lookup respecting deleted_at IS NULL),
	// not on the store layer's audit contract. Matches the Chunk 1 Test
	// #8A pattern so both halves of the soft-delete guard use the same
	// tombstoning mechanism.
	if _, err := db.Exec("UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", tombstoned.ID); err != nil {
		t.Fatalf("tombstone row: %v", err)
	}

	// Upload a row whose content is "Starbucks $999.00 Coffee" but on
	// date 2025-07-15 — a third date that collides with NEITHER seeded
	// row's hash space. At upload time, the row is clean.
	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-15", "Starbucks", "999.00", "Coffee"},
		},
	)
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)
	if uploadGroups := upload["collision_groups"].([]any); len(uploadGroups) != 0 {
		t.Fatalf("expected 0 collision groups at upload (baseline is clean), got %d: %v", len(uploadGroups), uploadGroups)
	}

	// PATCH the uploaded row's date to 2025-08-15, which re-hashes the row
	// into the tombstoned row's hash space. The correct response still has
	// zero collision groups because the DB match is filtered by the
	// soft-delete predicate inside GetTransactionByContentHash.
	patchRec := patchImportRow(t, h, user, importID, 0, "date", "2025-08-15")
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}
	groups := resp["collision_groups"].([]any)
	if len(groups) != 0 {
		t.Fatalf("expected 0 collision groups after PATCH (tombstoned row must not match), got %d: %v", len(groups), groups)
	}

	// Extra sanity: the tombstoned row really is tombstoned. If this
	// assertion fails the test setup is broken and the primary assertion
	// above is meaningless.
	var tombstonedDeletedAt sql.NullTime
	if err := db.QueryRow("SELECT deleted_at FROM transactions WHERE id = ?", tombstoned.ID).Scan(&tombstonedDeletedAt); err != nil {
		t.Fatalf("re-read tombstoned row: %v", err)
	}
	if !tombstonedDeletedAt.Valid {
		t.Fatalf("seeded tombstoned row still has deleted_at=NULL — test setup is broken")
	}
}

// TestHandleImportPatchRow_AmountFieldModeParity owns the edit-mode half of
// the amount-field parity rule in the spec's Validation field rules table:
//
//	Upload mode: empty cell → 0 (existing silent behavior)
//	Edit mode:   empty PATCH → 400 INVALID_AMOUNT
//
// The asymmetry is intentional. An empty cell in a freshly-uploaded file
// is usually a parse failure upstream (blank row, header misalignment)
// and silent-zero has been the behavior since Phase 3.1; changing that
// now would regress every uploader's file. But an empty string sent via
// PATCH is the user actively editing a value — they saw a number, they
// deleted it, they Tab'd away. Silent-zero there would make the Import
// button deceptively enable while the row is effectively broken. Hard
// 400 forces the inline cell error so the user knows the edit did not
// take effect.
//
// Test shape: upload a row with an explicit amount to establish the
// baseline, then PATCH that row's amount to "" and assert the response
// status is 400 and the body carries {code:"INVALID_AMOUNT", field:"amount"}.
// A second assertion checks that the upload-mode silent-zero path is NOT
// regressed by the PATCH-side change: we upload a separate row with
// amount="" and assert that upload responds 200 and includes the row
// (silent coerce to 0.00). This second assertion lives in the same test
// because the invariant — "upload and PATCH handle empty amounts
// differently, and BOTH behaviors must stay stable together" — is a
// single bug owner, not two separate ones.
func TestHandleImportPatchRow_AmountFieldModeParity(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "amountparity", "member")
	seedTestCategory(t, q, "Coffee", "expense")

	// Upload mode: empty amount cell must coerce to 0 and the row must
	// still appear in the preview. This is the existing silent-zero
	// behavior — it is inherited from parseImportAmount returning an
	// error for empty strings but the calling loop in handleImportUpload
	// treating that as zero. The PATCH change in Task 7 adds an extra
	// non-empty check that does NOT apply here (it is in
	// validateImportField, not handleImportUpload's parse loop).
	uploadXlsx := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{
			{"2025-07-01", "Starbucks", "5.00", "Coffee"},
			{"2025-07-02", "Blank Row", "", "Coffee"},
		},
	)
	uploadReq := postMultipartFile(t, "/api/import/upload", uploadXlsx)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200 (silent-zero for empty amount), got %d: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &upload); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := upload["import_id"].(string)
	rows := upload["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows in upload response (silent-zero keeps blank-amount row), got %d", len(rows))
	}
	// The row with empty amount parses to 0.0 at upload time.
	row1 := rows[1].(map[string]any)
	if amt, ok := row1["amount"].(float64); !ok || amt != 0 {
		t.Errorf("expected row 1 amount=0 (silent-zero), got %v (%T)", row1["amount"], row1["amount"])
	}

	// Edit mode: PATCH the FIRST row's amount to the empty string. The
	// response must be 400 with the INVALID_AMOUNT error body. We target
	// row 0 (which has a valid 5.00 at upload time) so the test exercises
	// "user is actively clearing a valid amount", not "we already had a
	// zero-amount row and the PATCH no-ops".
	patchRec := patchImportRow(t, h, user, importID, 0, "amount", "")
	if patchRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on PATCH with empty amount, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var patchErr map[string]any
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patchErr); err != nil {
		t.Fatalf("unmarshal patch error: %v", err)
	}
	if patchErr["code"] != "INVALID_AMOUNT" {
		t.Errorf("expected code=INVALID_AMOUNT, got %v", patchErr["code"])
	}
	if patchErr["field"] != "amount" {
		t.Errorf("expected field=amount, got %v", patchErr["field"])
	}
	if msg, ok := patchErr["message"].(string); !ok || msg == "" {
		t.Errorf("expected non-empty message string, got %v (%T)", patchErr["message"], patchErr["message"])
	}

	// Extra guard: the rejected PATCH must NOT have mutated the session
	// state. Re-read the store directly and confirm row 0 still has
	// amount=5.00. A handler that writes BEFORE validating would leave
	// the row at amount=0 even though the response was 400.
	val, ok := importStore.Load(importID)
	if !ok {
		t.Fatalf("importStore.Load returned !ok for import_id=%s", importID)
	}
	entry := val.(*importEntry)
	if got := entry.Rows[0].Amount; got != 5.0 {
		t.Errorf("rejected PATCH must leave row 0 amount unchanged, got %v", got)
	}
}

// TestHandleImportGetSession_HappyPath verifies that after an upload, a
// GET on /api/import/{importID} returns the same shape as the upload
// response (rows, columns, unique_categories, collision_groups,
// import_id, row_count). This is the F5/tab-refresh resume path — the
// frontend mounts with a localStorage import_id and calls GET to
// rehydrate preview state without re-uploading the file.
func TestHandleImportGetSession_HappyPath(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "getter", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00", "Food"},
		{"2025-01-08", "Trader Joe's", "42.10", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Now GET the session.
	getReq := httptest.NewRequest(http.MethodGet, "/api/import/"+importID, nil)
	getReq = withUserAndURLParam(getReq, user, "importID", importID)
	getRec := httptest.NewRecorder()
	h.handleImportGetSession(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d; body: %s", getRec.Code, getRec.Body.String())
	}

	var getResp map[string]any
	decodeResponse(t, getRec, &getResp)

	// Shape parity: every top-level key present in the upload response
	// must also appear in the GET response. This is the F5-refresh
	// contract — frontend code paths that consume upload-shaped JSON
	// keep working when they consume GET-shaped JSON.
	for _, key := range []string{"import_id", "row_count", "rows", "columns", "unique_categories", "collision_groups"} {
		if _, ok := getResp[key]; !ok {
			t.Errorf("GET response missing top-level key %q", key)
		}
	}

	if gotID, _ := getResp["import_id"].(string); gotID != importID {
		t.Errorf("import_id: want %q, got %q", importID, gotID)
	}
	if rc, _ := getResp["row_count"].(float64); int(rc) != 2 {
		t.Errorf("row_count: want 2, got %v", getResp["row_count"])
	}
	rowsResp, ok := getResp["rows"].([]any)
	if !ok || len(rowsResp) != 2 {
		t.Fatalf("rows: want slice of 2, got %T len %d", getResp["rows"], len(rowsResp))
	}

	// Two distinct rows with no DB matches → zero collision groups.
	groups, _ := getResp["collision_groups"].([]any)
	if len(groups) != 0 {
		t.Errorf("collision_groups: want 0 for two distinct rows, got %d", len(groups))
	}
}

// TestHandleImportGetSession_ExpiredSession_Returns404 verifies that a
// GET for an import whose CreatedAt is older than importTTL returns 404
// and reaps the entry. Uses the same direct-store mutation pattern as
// TestHandleImportPatchRow_ExpiredSession_Returns404 from Chunk 2:
// upload normally, then rewind CreatedAt to 2h ago via importStore.
func TestHandleImportGetSession_ExpiredSession_Returns404(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "expirer", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00"},
	})
	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Rewind CreatedAt so the entry is expired per importTTL (60m).
	val, ok := importStore.Load(importID)
	if !ok {
		t.Fatalf("store lookup: entry missing immediately after upload")
	}
	entry := val.(*importEntry)
	entry.CreatedAt = time.Now().Add(-2 * time.Hour)

	// GET should now 404 and the helper should reap the entry from
	// the store (same contract as the Chunk 2 PATCH expiry test).
	getReq := httptest.NewRequest(http.MethodGet, "/api/import/"+importID, nil)
	getReq = withUserAndURLParam(getReq, user, "importID", importID)
	getRec := httptest.NewRecorder()
	h.handleImportGetSession(getRec, getReq)

	if getRec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for expired session, got %d; body: %s", getRec.Code, getRec.Body.String())
	}
	if _, still := importStore.Load(importID); still {
		t.Error("expected loadImportEntryForUser to delete the expired entry; it is still in the store")
	}
}

// TestHandleImportConfirm_UnresolvedCollisions_Returns409 verifies that
// confirming a session that still contains a non-skipped collision
// group is rejected with 409 UNRESOLVED_COLLISIONS and zero rows are
// inserted. Uploads two identical rows (same date, description,
// amount, category) which produce a size-2 intra_file collision
// group, then immediately confirms without PATCHing — the backend
// must refuse the import.
//
// This test owns the "no partial insert" invariant: Phase 3.4 without
// this gate would insert the first identical row and silently skip
// the rest with skipReasonDuplicate, losing 19 of 20 rows in the
// 20-Starbucks-receipts case. After Chunk 3, /confirm returns 409
// and the DB is untouched.
func TestHandleImportConfirm_UnresolvedCollisions_Returns409(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "collider", "member")

	// Two identical rows — intra_file collision with no resolution.
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00", "Food"},
		{"2025-01-07", "Starbucks", "5.00", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// Sanity: upload already flagged the collision group. If this
	// precondition fails, the bug is in Chunk 1 (upload-time
	// grouping), not Chunk 3 — the confirm re-check can't be tested
	// if upload is missing the group.
	uploadGroups, _ := uploadResp["collision_groups"].([]any)
	if len(uploadGroups) != 1 {
		t.Fatalf("precondition: upload should return 1 collision group, got %d", len(uploadGroups))
	}

	// Build a category_map so confirm can resolve categories.
	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	catMap := make(map[string]float64)
	var defaultID int64
	for _, c := range cats {
		catMap[c.Name] = float64(c.ID)
		if c.Name == "Food" || c.Name == "Groceries" {
			defaultID = c.ID
		}
	}
	if defaultID == 0 {
		defaultID = cats[0].ID
	}

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": defaultID,
		"category_map":        catMap,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)
	if code, _ := confirmResp["code"].(string); code != "UNRESOLVED_COLLISIONS" {
		t.Errorf("code: want UNRESOLVED_COLLISIONS, got %v", confirmResp["code"])
	}
	groupsResp, ok := confirmResp["collision_groups"].([]any)
	if !ok {
		t.Fatalf("collision_groups missing from 409 body, got %T", confirmResp["collision_groups"])
	}
	if len(groupsResp) != 1 {
		t.Errorf("collision_groups: want 1 group, got %d", len(groupsResp))
	}

	// Zero-insert invariant: no transactions exist for this user.
	// The session should also still be present in importStore — 409
	// does not clean up state (only 200 does), so the frontend can
	// re-submit after editing.
	if count := countTransactionsForUser(t, db, user.ID); count != 0 {
		t.Errorf("DB leaked %d rows past the 409 gate — expected 0", count)
	}
	if _, stillPresent := importStore.Load(importID); !stillPresent {
		t.Error("expected session to remain in importStore after 409 — 409 should NOT reap")
	}
}

// TestHandleImportConfirm_PersistsContentHash verifies that a
// successful /confirm writes a non-null content_hash to every inserted
// transaction row. This is the regression guard for the full Phase
// 3.4b invariant: after confirm, the DB rows have content_hash
// populated so a re-import of the same file would trigger the
// collision detection path. Without this, the re-import path silently
// double-inserts.
//
// Uses two distinct rows (no collision) so confirm hits the happy
// path, then SELECTs content_hash directly from the transactions
// table and asserts both values are populated and distinct.
func TestHandleImportConfirm_PersistsContentHash(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "hasher", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00", "Food"},
		{"2025-01-08", "Trader Joe's", "42.10", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	catMap := make(map[string]float64)
	var defaultID int64
	for _, c := range cats {
		catMap[c.Name] = float64(c.ID)
		if c.Name == "Food" || c.Name == "Groceries" {
			defaultID = c.ID
		}
	}
	if defaultID == 0 {
		defaultID = cats[0].ID
	}

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": defaultID,
		"category_map":        catMap,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)
	if imported, _ := confirmResp["imported"].(float64); int(imported) != 2 {
		t.Errorf("imported: want 2, got %v", confirmResp["imported"])
	}

	// Pull every content_hash value for this user and assert both are
	// non-null and distinct. Queries the raw column because the typed
	// Transaction struct exposes content_hash via sql.NullString and
	// asserting on the raw scan is the shortest path to the invariant.
	rowsRS, err := db.Query("SELECT content_hash FROM transactions WHERE user_id = ? AND deleted_at IS NULL ORDER BY date", user.ID)
	if err != nil {
		t.Fatalf("select content_hash: %v", err)
	}
	defer rowsRS.Close()

	var hashes []string
	for rowsRS.Next() {
		var hashCell sql.NullString
		if err := rowsRS.Scan(&hashCell); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !hashCell.Valid || hashCell.String == "" {
			t.Error("row has NULL or empty content_hash — confirm path did not populate it")
		}
		hashes = append(hashes, hashCell.String)
	}
	if err := rowsRS.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(hashes) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(hashes))
	}
	if hashes[0] == hashes[1] {
		t.Errorf("both inserted rows have the same content_hash %q — they should differ for distinct rows", hashes[0])
	}
}

// TestHandleImportConfirm_SkippedRows_ExcludedFromInserts verifies the
// skip ≠ unresolved distinction: a row whose Skip field was flipped
// via a Chunk 2 PATCH is excluded from inserts entirely, does NOT
// count toward the collision re-check, and IS counted toward the
// `skipped` field of the confirm response.
//
// Upload two distinct rows → PATCH row 0 with skip=true → confirm →
// assert imported=1, skipped=1, total=2, and that only row 1 landed
// in the DB. Exercises the full round-trip of PATCH's Skip mutation
// flowing into confirm's handler-level filter.
func TestHandleImportConfirm_SkippedRows_ExcludedFromInserts(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "skipper", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2025-01-07", "Starbucks", "5.00", "Food"},
		{"2025-01-08", "Trader Joe's", "42.10", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// PATCH row 0 to set skip=true via the Chunk 2 handler.
	patchBody, _ := json.Marshal(map[string]any{
		"field": "skip",
		"value": true,
	})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/import/"+importID+"/rows/0", bytes.NewReader(patchBody))
	patchReq = withUserAndURLParams(patchReq, user, map[string]string{
		"importID": importID,
		"rowID":    "0",
	})
	patchRec := httptest.NewRecorder()
	h.handleImportPatchRow(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d; body: %s", patchRec.Code, patchRec.Body.String())
	}

	// Now confirm. Expect the skipped row to be excluded from inserts.
	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	catMap := make(map[string]float64)
	var defaultID int64
	for _, c := range cats {
		catMap[c.Name] = float64(c.ID)
		if c.Name == "Food" || c.Name == "Groceries" {
			defaultID = c.ID
		}
	}
	if defaultID == 0 {
		defaultID = cats[0].ID
	}

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": defaultID,
		"category_map":        catMap,
	})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody))
	confirmReq = withUser(confirmReq, user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}

	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)
	if imported, _ := confirmResp["imported"].(float64); int(imported) != 1 {
		t.Errorf("imported: want 1, got %v", confirmResp["imported"])
	}
	if skipped, _ := confirmResp["skipped"].(float64); int(skipped) != 1 {
		t.Errorf("skipped: want 1 (the user-skipped row), got %v", confirmResp["skipped"])
	}
	if total, _ := confirmResp["total"].(float64); int(total) != 2 {
		t.Errorf("total: want 2, got %v", confirmResp["total"])
	}

	// DB verification: only row 1 (Trader Joe's) should exist; the
	// skipped row (Starbucks) must not have been inserted.
	descRows, err := db.Query("SELECT description FROM transactions WHERE user_id = ? AND deleted_at IS NULL", user.ID)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	defer descRows.Close()

	var descriptions []string
	for descRows.Next() {
		var d string
		if err := descRows.Scan(&d); err != nil {
			t.Fatalf("scan: %v", err)
		}
		descriptions = append(descriptions, d)
	}
	if err := descRows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(descriptions) != 1 {
		t.Fatalf("expected exactly 1 row in DB, got %d: %v", len(descriptions), descriptions)
	}
	if descriptions[0] != "Trader Joe's" {
		t.Errorf("expected only Trader Joe's in DB, got %q — the skipped Starbucks row leaked past the filter", descriptions[0])
	}
}

// TestHandleImportUpload_PreviewCanonicalizesSerialDate pins the fix for
// the Phase 3.4b preview-leaks-serial-date bug: main's PR #19 switched the
// xlsx reader to RawCellValue:true so that date cells arrive as Excel
// serial strings (e.g. "45859"). That fix taught the confirm path to parse
// serials via excelize.ExcelDateToTime, but the upload handler still stored
// the raw cell value into ir.Date, so the 3.4b preview table rendered
// "45689.0" in the Date column (reported via smoke test screenshot on
// 2026-04-15). This test builds an xlsx file with a native date cell in a
// non-text display format, uploads it, and asserts the preview response
// emits ISO (YYYY-MM-DD) strings, not Excel serial numbers. Without the
// fix at ir.Date = val, the assertion on rows[0]["date"] will read a
// numeric string like "45859" and fail — which mirrors exactly what the
// user saw in the UI.
func TestHandleImportUpload_PreviewCanonicalizesSerialDate(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "previewdateimporter", "member")

	xlsxData := createTestXLSXWithNativeDateCells(t, "Transactions",
		[]string{"Date", "Description", "Amount"},
		// mm-dd-yy is deliberately not one of the text formats
		// parseImportDate would ever try directly; the only path by
		// which it can parse this cell is the Excel-serial path (the
		// display format is irrelevant once RawCellValue:true is on).
		"mm-dd-yy",
		[]nativeDateRow{
			{Date: time.Date(2025, 7, 21, 0, 0, 0, 0, time.UTC), Rest: []string{"supermarket", "45.00"}},
			{Date: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), Rest: []string{"year-end treat", "10.00"}},
		})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	rows, ok := resp["rows"].([]any)
	if !ok {
		t.Fatal("expected rows to be an array")
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	want := []string{"2025-07-21", "2025-12-31"}
	for i, w := range want {
		row := rows[i].(map[string]any)
		got, _ := row["date"].(string)
		if got != w {
			t.Errorf("rows[%d].date = %q, want %q — preview is leaking the raw xlsx cell value instead of canonicalizing to ISO. The frontend import preview will render this string as-is.",
				i, got, w)
		}
	}
}

// TestHandleImportPatchRow_WrongUser_Returns403 verifies that PATCHing a
// row on another user's import session is rejected with 403 Forbidden
// before any row-bounds or field-validation work happens. Owns the "one
// user's PATCH cannot mutate another user's session" invariant alongside
// the sibling Confirm/Cancel/GET 403 tests. A regression that forgets to
// route through loadImportEntryForUser (or swaps the ownership check for
// a weaker "session exists" check) will fail here.
func TestHandleImportPatchRow_WrongUser_Returns403(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user1 := seedTestUser(t, q, "patchowner", "member")
	user2 := seedTestUser(t, q, "patchattacker", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50"},
	})
	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user1)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// User2 tries to PATCH row 0 on user1's session.
	rec := patchImportRow(t, h, user2, importID, 0, "description", "hijacked")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Confirm the row was not mutated.
	val, ok := importStore.Load(importID)
	if !ok {
		t.Fatalf("store lookup: entry missing after 403")
	}
	entry := val.(*importEntry)
	if len(entry.Rows) != 1 || entry.Rows[0].Description != "Groceries" {
		t.Errorf("row was mutated despite 403: got description=%q", entry.Rows[0].Description)
	}
}

// TestHandleImportGetSession_WrongUser_Returns403 verifies that GETting
// another user's import session is rejected with 403 Forbidden before
// the groups-rebuild work runs. Paired with the PATCH 403 test above so
// both read and write paths share the invariant: a valid importID from
// another user's upload is indistinguishable from a nonexistent one.
func TestHandleImportGetSession_WrongUser_Returns403(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user1 := seedTestUser(t, q, "getowner", "member")
	user2 := seedTestUser(t, q, "getattacker", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50"},
	})
	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user1)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	// User2 tries to GET user1's session.
	getReq := httptest.NewRequest(http.MethodGet, "/api/import/"+importID, nil)
	getReq = withUserAndURLParam(getReq, user2, "importID", importID)
	getRec := httptest.NewRecorder()
	h.handleImportGetSession(getRec, getReq)

	if getRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", getRec.Code, getRec.Body.String())
	}
}
