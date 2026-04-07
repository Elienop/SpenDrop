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

	"github.com/xuri/excelize/v2"
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
	preview, ok := resp["preview"].([]any)
	if !ok {
		t.Fatal("expected preview to be an array")
	}
	if len(preview) != 3 {
		t.Errorf("expected 3 preview rows, got %d", len(preview))
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
	preview := resp["preview"].([]any)
	if len(preview) != 10 {
		t.Errorf("expected preview capped at 10, got %d", len(preview))
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
