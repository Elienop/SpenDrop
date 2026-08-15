package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"github.com/elienop/spendrop/internal/database"
)

// --- Invariant: no transaction may vanish from an export silently ----------
//
// The .xlsx format caps a cell at 32,767 characters and excelize enforces that
// by silently truncating (see the "Over-long cell values" section of
// export_handlers.go). The ledger can hold longer values because the importer
// wrote description/tags/notes unvalidated for most of the product's life. The
// tests below pin that such a value leaves an unmistakable trace in the file
// rather than a cell that merely LOOKS complete.

// overLongChars is comfortably past excelize's 32,767-character cell cap, and
// is a round number so the marker's "full value is N characters" claim is easy
// to assert exactly.
const overLongChars = 40000

// seedOverLongTransaction inserts one transaction whose description, tags and
// notes are all far past the cell cap, plus a distinctive prefix on each so a
// test can prove the surviving text is the START of the real value and not some
// other field. Written through the sqlc layer, which does no length validation
// — exactly how the importer produced these rows before the field-length checks
// existed.
func seedOverLongTransaction(t *testing.T, q *database.Queries, userID, categoryID int64, date string) database.Transaction {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %q: %v", date, err)
	}
	long := func(prefix string) string {
		return prefix + strings.Repeat("x", overLongChars-len(prefix))
	}
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: 77700,
		Description: long("DESC-"),
		CategoryID:  categoryID,
		Tags:        sql.NullString{String: long("TAGS-"), Valid: true},
		Notes:       sql.NullString{String: long("NOTES-"), Valid: true},
	})
	if err != nil {
		t.Fatalf("seed over-long transaction: %v", err)
	}
	return txn
}

// openXLSX parses a recorded response body as a workbook.
func openXLSX(t *testing.T, rec *httptest.ResponseRecorder) *excelize.File {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// assertMarkedCell checks one cell carries the full truncation contract: it is
// shortened, it still starts with the real value, it ends with an INTACT marker
// naming the true original length, and it fits inside the cell cap so excelize
// had no reason to cut it further.
//
// The "ends with" check is the load-bearing one. Appending a marker to a value
// already at the cap would push the cell over, and excelize would silently eat
// the marker — leaving a cell that is truncated, unmarked, and indistinguishable
// from the bug this whole mechanism exists to fix.
func assertMarkedCell(t *testing.T, cell, wantPrefix, column string) {
	t.Helper()
	if !strings.HasPrefix(cell, wantPrefix) {
		t.Errorf("%s cell does not start with the real value %q; got %.40q...", column, wantPrefix, cell)
	}
	if !strings.Contains(cell, "[TRUNCATED BY SPENDROP:") {
		t.Errorf("%s cell carries no truncation marker; a user reconciling this row "+
			"cannot tell the export is abridged. Got %d chars ending %q",
			column, utf16Len(cell), tail(cell, 60))
	}
	if !strings.HasSuffix(cell, "]") {
		t.Errorf("%s cell's truncation marker was itself cut off (ends %q): the marker "+
			"must be counted against the cell cap BEFORE the value is shortened",
			column, tail(cell, 60))
	}
	if !strings.Contains(cell, "40000 characters") {
		t.Errorf("%s marker does not state the true original length of %d; got %q",
			column, overLongChars, tail(cell, 120))
	}
	if n := utf16Len(cell); n > maxExportCellChars {
		t.Errorf("%s cell is %d UTF-16 units, over the %d cap: excelize will silently "+
			"truncate it and take the marker with it", column, n, maxExportCellChars)
	}
	if cell == wantPrefix+strings.Repeat("x", overLongChars-len(wantPrefix)) {
		t.Errorf("%s cell was written in full, which the format cannot represent", column)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// findWarningRow returns the Export Warnings row describing the given column,
// or nil.
func findWarningRow(rows [][]string, sheet, column string) []string {
	for _, r := range rows {
		if len(r) >= 4 && r[0] == sheet && r[2] == column {
			return r
		}
	}
	return nil
}

func TestHandleExportTransactions_MarksOverLongCells(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "over-long", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")
	seedOverLongTransaction(t, h.queries, user.ID, cat, "2026-03-15")

	req := withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user)
	rec := httptest.NewRecorder()
	h.handleExportTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	f := openXLSX(t, rec)

	rows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("read Transactions sheet: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Transactions rows = %d, want 2 (header + 1 data row); an over-long "+
			"value must not cost the row", len(rows))
	}
	data := rows[1]
	if want := len(exportTxnHeaders("USD")); len(data) < want {
		t.Fatalf("data row has %d cells, want %d: %q", len(data), want, data)
	}
	assertMarkedCell(t, data[1], "DESC-", "Description")
	assertMarkedCell(t, data[8], "TAGS-", "Tags")
	assertMarkedCell(t, data[9], "NOTES-", "Notes")

	// The row's other fields must be untouched — the marking is per cell.
	if data[0] != "2026-03-15" {
		t.Errorf("Date = %q, want 2026-03-15", data[0])
	}
	if data[4] != "777" {
		t.Errorf("Amount = %q, want 777", data[4])
	}

	// The warnings sheet names every affected cell.
	warn, err := f.GetRows(exportWarningsSheet)
	if err != nil || len(warn) == 0 {
		t.Fatalf("no %q sheet in an abridged export (err %v): the user has no way to "+
			"discover the truncation without scanning every row", exportWarningsSheet, err)
	}
	for _, column := range []string{"Description", "Tags", "Notes"} {
		row := findWarningRow(warn, "Transactions", column)
		if row == nil {
			t.Errorf("%q sheet does not list the truncated %s cell; rows = %v",
				exportWarningsSheet, column, warn)
			continue
		}
		if row[3] != "40000" {
			t.Errorf("%s warning reports %q characters, want 40000", column, row[3])
		}
	}

	// The workbook opens ON the warning, not on data that looks complete.
	if active := f.GetSheetName(f.GetActiveSheetIndex()); active != exportWarningsSheet {
		t.Errorf("active sheet = %q, want %q: an abridged workbook must open on the "+
			"warning", active, exportWarningsSheet)
	}

	// Consumers that never open a spreadsheet tab still get the signal.
	if got := rec.Header().Get("X-Export-Truncated-Cells"); got != "3" {
		t.Errorf("X-Export-Truncated-Cells = %q, want %q", got, "3")
	}
}

func TestHandleExportMonthly_MarksOverLongCells(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "over-long-monthly", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")
	seedOverLongTransaction(t, h.queries, user.ID, cat, "2026-03-15")

	req := withUserAndURLParams(
		httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/3", nil),
		user, map[string]string{"year": "2026", "month": "3"})
	rec := httptest.NewRecorder()
	h.handleExportMonthly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	f := openXLSX(t, rec)

	rows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("read Transactions sheet: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Transactions rows = %d, want 2 (header + 1 data row)", len(rows))
	}
	assertMarkedCell(t, rows[1][1], "DESC-", "Description")

	if _, err := f.GetRows(exportWarningsSheet); err != nil {
		t.Errorf("no %q sheet on the monthly export: %v", exportWarningsSheet, err)
	}
	if got := rec.Header().Get("X-Export-Truncated-Cells"); got != "3" {
		t.Errorf("X-Export-Truncated-Cells = %q, want %q", got, "3")
	}
}

// TestExports_FaithfulExportCarriesNoWarnings is the negative half. A warnings
// tab on every export would train the user to ignore it, which is how a real
// warning gets missed.
//
// Seeded with values one character SHORT of the cap rather than with short
// ones: a mechanism that marked on `>=` instead of `>`, or that counted bytes
// instead of UTF-16 units, would pass a test seeded with "Lunch".
func TestExports_FaithfulExportCarriesNoWarnings(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "faithful", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")

	atLimit := "AT-" + strings.Repeat("x", maxExportCellChars-3)
	if utf16Len(atLimit) != maxExportCellChars {
		t.Fatalf("fixture is %d UTF-16 units, want exactly %d", utf16Len(atLimit), maxExportCellChars)
	}
	d, _ := time.Parse("2006-01-02", "2026-03-15")
	if _, err := h.queries.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		Date:        d,
		AmountCents: 77700,
		Description: atLimit,
		CategoryID:  cat,
	}); err != nil {
		t.Fatalf("seed at-limit transaction: %v", err)
	}

	req := withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user)
	rec := httptest.NewRecorder()
	h.handleExportTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	f := openXLSX(t, rec)

	rows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("read Transactions sheet: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Transactions rows = %d, want 2", len(rows))
	}
	if rows[1][1] != atLimit {
		t.Errorf("a value at exactly the %d-character cap was altered: got %d chars ending %q",
			maxExportCellChars, utf16Len(rows[1][1]), tail(rows[1][1], 60))
	}
	if idx, _ := f.GetSheetIndex(exportWarningsSheet); idx != -1 {
		t.Errorf("faithful export grew a %q sheet; a warning present on every export "+
			"is a warning nobody reads", exportWarningsSheet)
	}
	if got := rec.Header().Get("X-Export-Truncated-Cells"); got != "" {
		t.Errorf("X-Export-Truncated-Cells = %q on a faithful export, want absent", got)
	}
}

// TestExportTruncations_MarkerIsCountedAgainstTheCap is the unit-level pin on
// the arithmetic assertMarkedCell can only observe indirectly, including the
// multi-byte case: the cap is in UTF-16 code units, so an astral rune costs two
// and a naive len() would let the cell overflow.
func TestExportTruncations_MarkerIsCountedAgainstTheCap(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		wantMark bool
	}{
		{"short", "Lunch", false},
		{"one below the cap", strings.Repeat("a", maxExportCellChars-1), false},
		{"exactly at the cap", strings.Repeat("a", maxExportCellChars), false},
		{"one over the cap", strings.Repeat("a", maxExportCellChars+1), true},
		{"far over the cap", strings.Repeat("a", overLongChars), true},
		// Every rune here is 2 UTF-16 units, so the value is under the cap by
		// byte count and rune count but over it by the unit that matters.
		{"astral runes over the cap by UTF-16 units",
			strings.Repeat("\U0001F600", maxExportCellChars/2+1), true},
		{"astral runes under the cap by UTF-16 units",
			strings.Repeat("\U0001F600", maxExportCellChars/2), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var x exportTruncations
			got := x.text("Transactions", "Description", 2, 7, tc.value)

			if !tc.wantMark {
				if got != tc.value {
					t.Fatalf("value was altered: %d units in, %d out", utf16Len(tc.value), utf16Len(got))
				}
				if x.total != 0 {
					t.Fatalf("recorded %d truncation(s) for a value that fits", x.total)
				}
				return
			}
			if x.total != 1 {
				t.Fatalf("recorded %d truncation(s), want 1", x.total)
			}
			if n := utf16Len(got); n > maxExportCellChars {
				t.Fatalf("marked value is %d UTF-16 units, over the %d cap: excelize "+
					"would silently truncate it and eat the marker", n, maxExportCellChars)
			}
			if !strings.HasSuffix(got, "]") || !strings.Contains(got, "[TRUNCATED BY SPENDROP:") {
				t.Fatalf("marker missing or cut off; value ends %q", tail(got, 60))
			}
			if len(x.notes) != 1 || x.notes[0].cell != "B7" || x.notes[0].column != "Description" {
				t.Fatalf("warning note = %+v, want one note for cell B7 / Description", x.notes)
			}
			if x.notes[0].chars != utf16Len(tc.value) {
				t.Fatalf("note records %d characters, want %d", x.notes[0].chars, utf16Len(tc.value))
			}
			// The result must still be valid UTF-8: truncateUTF16 cuts on a rune
			// boundary, so no astral rune is left half-written.
			if strings.ContainsRune(got, '�') {
				t.Fatal("truncation split a rune: the cell contains U+FFFD")
			}
		})
	}
}

// roundTripCell writes one value through a real StreamWriter, saves the
// workbook and reads the cell back. If excelize shortened the value, the
// returned string differs from the one passed in.
func roundTripCell(t *testing.T, value string) string {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sw, err := f.NewStreamWriter("Sheet1")
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	if err := sw.SetRow("A1", []any{value}); err != nil {
		t.Fatalf("SetRow: %v", err)
	}
	if err := sw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	g, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer g.Close()
	got, err := g.GetCellValue("Sheet1", "A1")
	if err != nil {
		t.Fatalf("GetCellValue: %v", err)
	}
	return got
}

// straddleValue builds a value of exactly totalUnits UTF-16 units whose rune at
// the truncation boundary is astral, so the cut cannot land on it.
//
// The boundary is maxExportCellChars minus the marker's own length, and the
// marker's length depends on the value's length — so the construction keeps the
// total unit count fixed while moving one astral rune to the boundary. That
// keeps the marker (and therefore the boundary) identical between the plain and
// straddling cases.
func straddleValue(t *testing.T, totalUnits int) string {
	t.Helper()
	marker := exportTruncationMarker(totalUnits)
	limit := maxExportCellChars - utf16Len(marker)
	if limit < 2 || totalUnits < limit+2 {
		t.Fatalf("straddleValue: unusable limit %d for %d units", limit, totalUnits)
	}
	// One ASCII rune short of the limit, then an astral rune that needs two
	// units when only one remains, then filler out to totalUnits.
	head := strings.Repeat("a", limit-1)
	tailLen := totalUnits - (limit - 1) - 2
	v := head + "\U0001F600" + strings.Repeat("b", tailLen)
	if got := utf16Len(v); got != totalUnits {
		t.Fatalf("straddleValue built %d units, want %d", got, totalUnits)
	}
	return v
}

// TestExportTruncations_MarkedCellFitsAndExcelizeLeavesItAlone is the boundary
// proof for the marker arithmetic.
//
// The mechanism only works if the marked cell is at or under the format's cap
// when it reaches excelize. One unit over and excelize trims from the END,
// which eats the marker and leaves a cell that is truncated, unmarked, and
// indistinguishable from the original defect — silently. Two things could push
// it over, and both are covered here:
//
//   - The kept prefix ending on an astral rune. The cut is measured in UTF-16
//     units but has to land on a rune boundary, so a two-unit rune at the
//     boundary must be dropped rather than half-written.
//   - The interpolated character count making the marker longer. "40000
//     characters" and "1000000 characters" differ by three units, so a
//     hard-coded reserve that fits one overflows the other.
//
// Each case is checked twice: arithmetically (utf16Len <= cap) and empirically,
// by round-tripping the marked value through a real StreamWriter and asserting
// excelize handed back the exact bytes it was given. The second check is the one
// that cannot be fooled by a mistake shared between the code and the test.
func TestExportTruncations_MarkedCellFitsAndExcelizeLeavesItAlone(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"ascii, one unit over the cap", strings.Repeat("a", maxExportCellChars+1)},
		{"ascii, 5-digit count", strings.Repeat("a", overLongChars)},
		{"ascii, 7-digit count (longer marker)", strings.Repeat("a", 1_000_000)},
		{"all astral, kept prefix is all 2-unit runes",
			strings.Repeat("\U0001F600", maxExportCellChars)},
		{"all astral, 7-digit count", strings.Repeat("\U0001F600", 500_000)},
		{"astral rune straddling the cut", straddleValue(t, overLongChars)},
		{"astral rune straddling the cut, 7-digit count", straddleValue(t, 1_000_000)},
		{"BMP multi-byte (1 unit, 3 bytes)", strings.Repeat("中", maxExportCellChars+1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var x exportTruncations
			got := x.text("Transactions", "Description", 2, 2, tc.value)

			n := utf16Len(got)
			if n > maxExportCellChars {
				t.Fatalf("marked cell is %d UTF-16 units, %d OVER the %d cap: excelize "+
					"will trim from the end and eat the marker",
					n, n-maxExportCellChars, maxExportCellChars)
			}
			// At most one unit of slack: the only legitimate shortfall is a
			// two-unit rune that could not fit in a one-unit gap. More than that
			// means the reserve is being over-estimated, which silently discards
			// ledger text that would have fitted.
			if n < maxExportCellChars-1 {
				t.Errorf("marked cell is %d units, %d short of the %d cap: the marker "+
					"reserve is larger than the marker", n, maxExportCellChars-n, maxExportCellChars)
			}
			if !strings.HasSuffix(got, "]") {
				t.Fatalf("marker cut off; cell ends %q", tail(got, 80))
			}
			if want := fmt.Sprintf("full value is %d characters", utf16Len(tc.value)); !strings.Contains(got, want) {
				t.Errorf("marker does not contain %q; cell ends %q", want, tail(got, 120))
			}
			if !utf8.ValidString(got) {
				t.Error("marked cell is not valid UTF-8: the cut split a rune")
			}

			// The empirical half: excelize must hand back exactly what it was
			// given. Any difference is excelize trimming, which is the failure
			// this whole mechanism exists to avoid.
			back := roundTripCell(t, got)
			if back != got {
				t.Fatalf("excelize altered the marked cell: wrote %d units, read back %d; "+
					"it ends %q, not %q", n, utf16Len(back), tail(back, 80), tail(got, 80))
			}
		})
	}
}

// TestExportTruncations_WarningSheetIsBounded pins that a ledger where every
// row is over-long cannot turn the warnings sheet into a second 50,000-row
// sheet — the memory the stream-writer change exists to save. The overflow is
// still counted in full.
func TestExportTruncations_WarningSheetIsBounded(t *testing.T) {
	var x exportTruncations
	over := strings.Repeat("a", overLongChars)
	for i := 0; i < maxExportTruncationNotes+25; i++ {
		x.text("Transactions", "Description", 2, i+2, over)
	}
	if x.total != maxExportTruncationNotes+25 {
		t.Errorf("total = %d, want %d: the count must not be capped, only the listing",
			x.total, maxExportTruncationNotes+25)
	}
	if len(x.notes) != maxExportTruncationNotes {
		t.Errorf("notes = %d, want %d", len(x.notes), maxExportTruncationNotes)
	}

	f := excelize.NewFile()
	defer f.Close()
	if err := writeExportWarningsSheet(f, &x); err != nil {
		t.Fatalf("writeExportWarningsSheet: %v", err)
	}
	rows, err := f.GetRows(exportWarningsSheet)
	if err != nil {
		t.Fatalf("read warnings sheet: %v", err)
	}
	joined := strings.Join(rows[len(rows)-1], " ")
	if !strings.Contains(joined, "and 25 more") {
		t.Errorf("last warnings row = %q, want it to account for the 25 unlisted cells", joined)
	}
}

// --- Invariant: the largest permitted export must not threaten the container --
//
// TestExports_UseStreamWriter is the cheap structural half of the memory fix;
// TestMeasureExportMemory (env-gated, in export_memory_measure_test.go) is the
// measured half.
//
// It works because the two excelize writing modes leave different artefacts in
// the package. Normal mode (SetCellValue) puts every string into a shared-string
// table at xl/sharedStrings.xml and keeps the whole sheet as a live cell tree
// until WriteTo — that tree is what peaked at 250 MiB for a MaxExportRows
// export. The StreamWriter emits inline strings and no shared-string part at
// all. Asserting the part is absent therefore fails the moment any sheet goes
// back to SetCellValue, which no row-count or cell-value assertion in this
// package would notice.
func TestExports_UseStreamWriter(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "stream-writer", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")
	seedExpenseRow(t, h.queries, user.ID, cat, "2026-03-15", 1234)
	seedExpenseRow(t, h.queries, user.ID, cat, "2026-04-15", 5678)

	cases := []struct {
		name   string
		invoke func(rec *httptest.ResponseRecorder)
	}{
		{"transactions", func(rec *httptest.ResponseRecorder) {
			h.handleExportTransactions(rec,
				withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user))
		}},
		{"monthly", func(rec *httptest.ResponseRecorder) {
			h.handleExportMonthly(rec, withUserAndURLParams(
				httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/3", nil),
				user, map[string]string{"year": "2026", "month": "3"}))
		}},
		{"yearly", func(rec *httptest.ResponseRecorder) {
			h.handleExportYearly(rec, withUserAndURLParams(
				httptest.NewRequest(http.MethodGet, "/api/export/yearly/2026", nil),
				user, map[string]string{"year": "2026"}))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.invoke(rec)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}

			zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
			if err != nil {
				t.Fatalf("open xlsx as zip: %v", err)
			}
			var parts []string
			for _, zf := range zr.File {
				parts = append(parts, zf.Name)
				if zf.Name == "xl/sharedStrings.xml" {
					t.Errorf("%s export contains xl/sharedStrings.xml: a sheet was written "+
						"with SetCellValue instead of the StreamWriter, which rebuilds the "+
						"in-memory cell tree this export was changed to avoid", tc.name)
				}
			}

			// Guard the guard: if excelize ever stopped emitting sharedStrings.xml
			// in normal mode too, the assertion above would go vacuous. A sheet
			// part must still be present, so we know we are reading a real
			// workbook and not an empty one.
			if !containsPrefix(parts, "xl/worksheets/sheet") {
				t.Fatalf("no worksheet part in the produced xlsx; parts = %v", parts)
			}
		})
	}
}

func containsPrefix(ss []string, prefix string) bool {
	for _, s := range ss {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
