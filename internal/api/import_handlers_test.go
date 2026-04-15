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

// TestHandleImport_DoubleImport_SkipsDuplicates locks in the Phase 3.4
// acceptance criterion that importing the same file twice produces zero
// new rows. The first confirm inserts two rows with content_hash set; the
// second confirm recomputes the same hashes, finds them via
// GetTransactionByContentHash in qtx, and skips each one. A regression
// that reverted the dedup check (or broke hash parity between the import
// path and the backfill) would surface here as imported=2/skipped=0 on
// the second pass.
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

	second := uploadAndConfirmImport(t, h, user, xlsxData)
	if int(second["imported"].(float64)) != 0 {
		t.Errorf("second import: expected imported=0, got %v", second["imported"])
	}
	if int(second["skipped"].(float64)) != 2 {
		t.Errorf("second import: expected skipped=2, got %v", second["skipped"])
	}
	if int(second["total"].(float64)) != 2 {
		t.Errorf("second import: expected total=2, got %v", second["total"])
	}

	// DB sanity: still only two live rows, no silent doubling.
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
