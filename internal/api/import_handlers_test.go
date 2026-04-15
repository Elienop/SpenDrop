package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
	req = withUserAndURLParam(req, user, "id", importID)
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
	req = withUserAndURLParam(req, user2, "id", importID)
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
// confirm with the given force_add indices. Returns the parsed confirm
// response so the caller can assert on imported/skipped counts without
// reconstructing the plumbing each time. Any non-200 along the way is a
// t.Fatal — the Phase 3.4 tests all exercise the happy-path wiring and
// want the assertions on the end-state counts, not on the transport.
func uploadAndConfirmImport(t *testing.T, h *Handler, user database.User, xlsxData []byte, forceAdd []int) map[string]any {
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
	if forceAdd != nil {
		confirmBodyMap["force_add"] = forceAdd
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

	first := uploadAndConfirmImport(t, h, user, xlsxData, nil)
	if int(first["imported"].(float64)) != 2 {
		t.Fatalf("first import: expected imported=2, got %v", first["imported"])
	}
	if int(first["skipped"].(float64)) != 0 {
		t.Errorf("first import: expected skipped=0, got %v", first["skipped"])
	}

	second := uploadAndConfirmImport(t, h, user, xlsxData, nil)
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

// TestHandleImport_ForceAdd_AppendsSuffixAndInserts exercises the
// opt-in override: the user ticks "import anyway" on both rows of the
// second upload, the handler runs resolveForceAddSuffix on each, and
// two new rows land with " (2)" appended to their description. The
// suffixed rows have different content hashes from the originals (and
// from each other), so a THIRD import of the plain file without force-add
// would still skip both originals — force-add does not poison the dedup
// index for future runs.
func TestHandleImport_ForceAdd_AppendsSuffixAndInserts(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "forcer", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food"},
		{"2026-01-16", "Electric bill", "120.00", "Utilities"},
	})

	// First import plays it straight.
	first := uploadAndConfirmImport(t, h, user, xlsxData, nil)
	if int(first["imported"].(float64)) != 2 {
		t.Fatalf("first import: expected imported=2, got %v", first["imported"])
	}

	// Second import marks both row indices for force-add. Both originals
	// already exist in the DB so resolveForceAddSuffix has to bump them.
	second := uploadAndConfirmImport(t, h, user, xlsxData, []int{0, 1})
	if int(second["imported"].(float64)) != 2 {
		t.Errorf("force-add import: expected imported=2, got %v", second["imported"])
	}
	if int(second["skipped"].(float64)) != 0 {
		t.Errorf("force-add import: expected skipped=0, got %v", second["skipped"])
	}

	// Four distinct live rows: the two originals plus the two suffixed copies.
	var live int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE deleted_at IS NULL`,
	).Scan(&live); err != nil {
		t.Fatalf("count live transactions: %v", err)
	}
	if live != 4 {
		t.Fatalf("expected 4 live rows after force-add, got %d", live)
	}

	// Suffixed descriptions must be exactly "<original> (2)". Use a
	// targeted query per description so a failure reports which row is
	// missing its suffix.
	for _, orig := range []string{"Groceries", "Electric bill"} {
		var count int64
		if err := db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM transactions WHERE description = ? AND deleted_at IS NULL`,
			orig+" (2)",
		).Scan(&count); err != nil {
			t.Fatalf("count suffixed %q: %v", orig, err)
		}
		if count != 1 {
			t.Errorf("expected exactly one row for %q, got %d", orig+" (2)", count)
		}
	}

	// A third plain import (no force-add) must still skip the originals and
	// also skip the suffixed copies — both hashes are now in the index.
	third := uploadAndConfirmImport(t, h, user, xlsxData, nil)
	if int(third["imported"].(float64)) != 0 {
		t.Errorf("third plain import: expected imported=0, got %v", third["imported"])
	}
	if int(third["skipped"].(float64)) != 2 {
		t.Errorf("third plain import: expected skipped=2, got %v", third["skipped"])
	}
}

// TestHandleImportUpload_PredictedSkips_ReflectsExistingRow verifies that
// the upload-time preview surfaces duplicate predictions for rows that
// would collide at confirm time, so the UI can pre-grey the row and offer
// the force-add checkbox without a round-trip. We seed the DB via the
// real import flow (rather than a synthetic CreateTransaction with a
// hand-computed hash) so the test exercises the same path an operator
// hits in production, and so a drift between predictDuplicateSkips and
// handleImportConfirm's hash formula shows up here instead of hiding
// behind a test fixture that matches only one of the two.
func TestHandleImportUpload_PredictedSkips_ReflectsExistingRow(t *testing.T) {
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
	// two hashes; a second upload should predict both as duplicates.
	if resp := uploadAndConfirmImport(t, h, user, xlsxData, nil); int(resp["imported"].(float64)) != 2 {
		t.Fatalf("seed import: expected imported=2, got %v", resp["imported"])
	}

	// Second upload: preview only — don't confirm. We want to inspect
	// predicted_skips in the upload response body.
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rec, &resp)

	raw, ok := resp["predicted_skips"].([]any)
	if !ok {
		t.Fatalf("expected predicted_skips in response, got %T", resp["predicted_skips"])
	}
	if len(raw) != 2 {
		t.Fatalf("expected 2 predicted skips, got %d: %v", len(raw), raw)
	}
	seen := make(map[int]bool, 2)
	for _, r := range raw {
		skip := r.(map[string]any)
		idx := int(skip["row_index"].(float64))
		seen[idx] = true
		if skip["reason"] != "duplicate" {
			t.Errorf("row %d: expected reason=duplicate, got %v", idx, skip["reason"])
		}
		if int64(skip["existing_id"].(float64)) == 0 {
			t.Errorf("row %d: expected non-zero existing_id", idx)
		}
	}
	for _, want := range []int{0, 1} {
		if !seen[want] {
			t.Errorf("expected row_index %d in predicted_skips, not found", want)
		}
	}

	// Verify the predicted existing_ids actually point at the seeded live
	// rows (guards against a bug where the handler returns the wrong
	// foreign key from GetTransactionByContentHash).
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

// TestHandleImportUpload_PredictedSkips_IgnoresTombstoned guards the
// interaction between Phase 3.4's hash lookup and Phase 2.1's soft-delete
// discipline. GetTransactionByContentHash filters deleted_at IS NULL, so
// a row that the user trashed and then re-imports must NOT come back as
// a predicted duplicate — otherwise the UI greys a row the user actively
// wants back, and the confirm path would silently skip it. Seed a row,
// tombstone it, then upload a matching file and assert predicted_skips
// is empty.
func TestHandleImportUpload_PredictedSkips_IgnoresTombstoned(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "ghosts", "member")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Groceries", "42.50", "Food"},
	})

	// Seed via the real import path, then tombstone the inserted row.
	// Using the handler path here means the seeded row lands with a real
	// content_hash — if we inserted via CreateTransaction directly we'd
	// have to compute the hash by hand and risk drifting from the actual
	// formula.
	if resp := uploadAndConfirmImport(t, h, user, xlsxData, nil); int(resp["imported"].(float64)) != 1 {
		t.Fatalf("seed import: expected imported=1, got %v", resp["imported"])
	}
	if _, err := db.ExecContext(context.Background(),
		`UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP`,
	); err != nil {
		t.Fatalf("tombstone row: %v", err)
	}

	// Re-upload the same file: the tombstoned row must not appear in the
	// predicted_skips set.
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rec, &resp)

	raw, ok := resp["predicted_skips"].([]any)
	if !ok {
		t.Fatalf("expected predicted_skips array in response, got %T", resp["predicted_skips"])
	}
	if len(raw) != 0 {
		t.Errorf("expected 0 predicted skips against tombstoned row, got %d: %v", len(raw), raw)
	}
}

// TestHandleImport_ReimportAfterTombstone_SucceedsOnConfirm is the
// confirm-path sibling of PredictedSkips_IgnoresTombstoned. The upload
// preview correctly reports no predicted duplicates against a tombstoned
// row — but that is only half the contract. The confirm call also has
// to actually land the re-insert. This test fails under a partial
// unique index that does not filter deleted_at (the INSERT hits UNIQUE
// on the tombstoned row's hash and is counted as skipped), and passes
// once the migration includes AND deleted_at IS NULL in the index WHERE
// clause.
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
	if resp := uploadAndConfirmImport(t, h, user, xlsxData, nil); int(resp["imported"].(float64)) != 1 {
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
	resp := uploadAndConfirmImport(t, h, user, xlsxData, nil)
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
