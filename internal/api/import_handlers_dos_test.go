package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// A worksheet declares its own geometry in XML attributes, so the size of an
// upload says nothing about how much work parsing it costs. The tests below
// drive the handler with files a few KB wide that used to cost an unbounded
// amount of CPU and multiple gigabytes of RAM respectively. See the
// MaxImportSheetRows / MaxImportSheetCells comment in limits.go for the
// measurements taken against the real attack payloads.
//
// Neither payload can be produced with excelize's writer API — it emits only
// well-formed geometry — so both are built by rewriting sheet1.xml inside an
// otherwise valid workbook.

// hostileRowIndex is the row number the row-axis tests claim.
//
// MaxImportSheetRows is now Excel's own maximum row, so any index exceeding
// our cap also exceeds excelize's TotalRows. That does NOT mean the library
// would catch these payloads — its guard is position-dependent, which is the
// original finding and the raise did not change it. `r=1` followed by
// `r=1048577` is accepted by upstream and refused only by us; `r=1` followed
// by an index near max-int64 still spins upstream without bound. Our cap is
// load-bearing at every position below and deleting it reopens the CPU
// denial of service.
//
// What the equality does change is that FIRST position is no longer
// distinguishable: there the library's guard fires first, so a subtest
// asserting our behaviour would pass with our cap deleted. That is why the
// table omits it, and why first position is covered on its own terms by
// TestHandleImportUpload_ExcelizeMaxRows_IsAClientError.
//
// The value is kept just over the cap rather than near max-int64 so that
// removing our cap makes these tests fail in milliseconds rather than hang.
// The genuinely unbounded payload spins effectively forever; that belongs in
// the incident notes, not in a test a verification pass has to run.
const hostileRowIndex = 2_000_000

// buildXLSXWithRawSheet returns a valid xlsx whose first worksheet's
// <sheetData> has been replaced with the supplied XML fragment. Every other
// part of the package (content types, rels, workbook, shared strings) is
// whatever excelize itself wrote, so the file is only malformed in the one
// dimension under test and reaches the same parser a real upload would.
func buildXLSXWithRawSheet(t *testing.T, sheetData string) []byte {
	t.Helper()

	f := excelize.NewFile()
	if err := f.SetCellValue(f.GetSheetName(0), "A1", "seed"); err != nil {
		t.Fatalf("seed cell: %v", err)
	}
	var base bytes.Buffer
	if err := f.Write(&base); err != nil {
		t.Fatalf("write base workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close base workbook: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(base.Bytes()), int64(base.Len()))
	if err != nil {
		t.Fatalf("open base workbook as zip: %v", err)
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, zf := range zr.File {
		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", zf.Name, err)
		}
		if zf.Name == "xl/worksheets/sheet1.xml" {
			doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
				`<sheetData>` + sheetData + `</sheetData></worksheet>`
			if _, err := io.WriteString(w, doc); err != nil {
				t.Fatalf("write malicious sheet: %v", err)
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", zf.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatalf("copy zip entry %q: %v", zf.Name, err)
		}
		rc.Close()
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("finish zip: %v", err)
	}
	return out.Bytes()
}

// inlineCell renders one inline-string cell. Inline strings keep the payload
// self-contained — no shared-strings table entry has to exist for the value.
func inlineCell(ref, val string) string {
	return `<c r="` + ref + `" t="inlineStr"><is><t>` + val + `</t></is></c>`
}

// headerRow is an ordinary, entirely valid row. Its presence in front of a
// hostile row is what defeats excelize's own guard, so the cases below use it
// as padding rather than as data.
func headerRow(n int) string {
	return fmt.Sprintf(`<row r="%d">`, n) +
		inlineCell(fmt.Sprintf("A%d", n), "Date") +
		inlineCell(fmt.Sprintf("B%d", n), "Description") +
		inlineCell(fmt.Sprintf("C%d", n), "Amount") +
		`</row>`
}

// uploadWithin runs handleImportUpload on its own goroutine and fails the test
// if it has not returned within the budget. The failure mode under test is a
// hang, not a wrong answer, so a plain call would wedge the whole test binary
// instead of reporting anything. The goroutine is deliberately left running on
// timeout — it cannot be cancelled, which is itself the finding.
func uploadWithin(t *testing.T, h *Handler, req *http.Request, budget time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.handleImportUpload(rec, req)
	}()
	select {
	case <-done:
		return rec
	case <-time.After(budget):
		t.Fatalf("handleImportUpload did not return within %s — the worksheet-shape cap is not holding", budget)
		return nil
	}
}

// TestHandleImportUpload_HugeDeclaredRowIndex_BoundedAtAnyPosition pins the
// CPU axis, and pins it at every position a hostile row can occupy.
//
// Position is the whole point. excelize v2.11.0's ErrMaxRows guard lives in
// Rows.Next()'s token-scanning loop, which only runs while the seek position
// has caught up to the current row; Rows.Columns() reads ahead and assigns the
// claimed index itself. So the library's guard is position-dependent — it
// covers a hostile first row and nothing else — which is why it is not a bound
// and why ours has to be.
//
// This is the row axis in isolation: every payload here has a handful of
// cells, orders of magnitude below MaxImportSheetCells, so only the row cap
// can be what rejects them. Asserting the specific message is what makes that
// a check rather than an assumption.
func TestHandleImportUpload_HugeDeclaredRowIndex_BoundedAtAnyPosition(t *testing.T) {
	// Guard the constant at runtime, not only in its comment. If a future
	// edit drops hostileRowIndex under the cap these payloads become
	// legitimate files and every subtest below silently stops testing
	// anything. Its two sibling tests carry the same shape of guard.
	if hostileRowIndex <= MaxImportSheetRows {
		t.Fatalf("hostileRowIndex (%d) must exceed MaxImportSheetRows (%d) or these payloads are legitimate",
			hostileRowIndex, MaxImportSheetRows)
	}

	hostile := fmt.Sprintf(`<row r="%d">`, hostileRowIndex) +
		inlineCell("A2", "x") + `</row>`

	var thousandRows strings.Builder
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&thousandRows, `<row r="%d">%s</row>`,
			i, inlineCell(fmt.Sprintf("A%d", i), "x"))
	}

	cases := []struct {
		name  string
		sheet string
	}{
		// One ordinary row in front is all it takes to defeat the library.
		{"hostile row behind a header", headerRow(1) + hostile},
		// And it stays defeated however far back the hostile row sits.
		{"hostile row behind a thousand rows", thousandRows.String() + hostile},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearImportStore()
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "dos-rows", "admin")

			payload := buildXLSXWithRawSheet(t, tc.sheet)
			if len(payload) > 128*1024 {
				t.Fatalf("payload grew to %d bytes; it must stay small for the amplification to be the point", len(payload))
			}

			req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
			rec := uploadWithin(t, h, req, 20*time.Second)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), fmt.Sprintf("extends past row %d", MaxImportSheetRows)) {
				t.Errorf("body=%s, want the row-cap reason naming row %d", rec.Body.String(), MaxImportSheetRows)
			}
		})
	}
}

// TestHandleImportUpload_WideRowPadding_RejectedNotExhausted pins the memory
// axis. A cell at column XFD makes excelize pad that row out to Excel's full
// 16384-column width, which no version of the library bounds. 5,000 such rows
// compress to 31 KB and allocated 5.7 GB before the cap existed.
//
// This is the cell axis in isolation: the row count here is three orders of
// magnitude below MaxImportSheetRows, so the row cap cannot be what fires. The
// count is derived from the cell budget rather than hard-coded, so raising or
// lowering that constant keeps this test meaningful instead of quietly turning
// it into a test of something else.
func TestHandleImportUpload_WideRowPadding_RejectedNotExhausted(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "dos-cols", "admin")

	// Sized off the constant, not a literal: enough far-right rows to clear
	// the cell budget with one to spare.
	wideRows := MaxImportSheetCells/excelize.MaxColumns + 2
	if wideRows >= MaxImportSheetRows {
		t.Fatalf("payload has %d rows, which would also trip the row cap (%d) and stop this from testing the cell axis alone",
			wideRows, MaxImportSheetRows)
	}

	var sheet strings.Builder
	for i := 1; i <= wideRows; i++ {
		fmt.Fprintf(&sheet, `<row r="%d">%s</row>`, i, inlineCell(fmt.Sprintf("XFD%d", i), "x"))
	}
	payload := buildXLSXWithRawSheet(t, sheet.String())

	if len(payload) > 64*1024 {
		t.Fatalf("payload grew to %d bytes; it must stay small for the amplification to be the point", len(payload))
	}

	req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
	rec := uploadWithin(t, h, req, 20*time.Second)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprintf("limit %d cells", MaxImportSheetCells)) {
		t.Errorf("body=%s, want the cell-cap reason naming %d cells", rec.Body.String(), MaxImportSheetCells)
	}
}

// TestHandleImportUpload_ExcelizeMaxRows_IsAClientError covers the one
// rejection path we do not own: a row index past excelize's TotalRows makes
// the library return ErrMaxRows from its own guard, ahead of our counter. That
// has to land as a 400 naming what to fix, like every other shape rejection —
// not a 500, and not the generic "failed to read spreadsheet data" that tells
// the user nothing actionable. Any file tripping the library's limit
// necessarily extends past ours too, so the row-cap advice is true for both.
func TestHandleImportUpload_ExcelizeMaxRows_IsAClientError(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "dos-maxrows", "admin")

	const beyondLibraryLimit = 2_000_000

	if beyondLibraryLimit <= excelize.TotalRows {
		t.Fatalf("row index %d must exceed excelize.TotalRows (%d) for the library guard to fire",
			beyondLibraryLimit, excelize.TotalRows)
	}
	payload := buildXLSXWithRawSheet(t,
		fmt.Sprintf(`<row r="%d">%s</row>`, beyondLibraryLimit, inlineCell("A1", "x")))

	req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
	rec := uploadWithin(t, h, req, 20*time.Second)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprintf("extends past row %d", MaxImportSheetRows)) {
		t.Errorf("body=%s, want the row-cap advice rather than a generic parse failure", rec.Body.String())
	}
}

// TestReadImportSheetRows_MatchesGetRows is the other half of the change:
// readImportSheetRows replaces a library call, so it has to agree with that
// library call on every shape a real file can take. The awkward ones are the
// blank-run padding (a gap in the middle of the data must keep later rows at
// their original index) and the trailing trim (blank rows at the bottom are
// dropped, so the returned length is the last populated row).
//
// Any divergence here is a silent import bug — rows landing under the wrong
// header, or a row count that disagrees with the sheet — which is why this is
// asserted against excelize's own output rather than against a hand-written
// expectation that could encode the same mistake twice.
func TestReadImportSheetRows_MatchesGetRows(t *testing.T) {
	cases := []struct {
		name  string
		cells map[string]string
	}{
		{
			name: "contiguous rows from A1",
			cells: map[string]string{
				"A1": "Date", "B1": "Description", "C1": "Amount",
				"A2": "2026-01-15", "B2": "Groceries", "C2": "42.50",
				"A3": "2026-01-16", "B3": "Coffee", "C3": "5.75",
			},
		},
		{
			name: "blank run in the middle",
			cells: map[string]string{
				"A1": "Date", "B1": "Description", "C1": "Amount",
				"A2": "2026-01-15", "B2": "Groceries", "C2": "42.50",
				"A7": "2026-01-16", "B7": "Coffee", "C7": "5.75",
			},
		},
		{
			name: "preamble above the header",
			cells: map[string]string{
				"A1": "Household ledger",
				"A4": "Date", "B4": "Description", "C4": "Amount",
				"A5": "2026-01-15", "B5": "Groceries", "C5": "42.50",
			},
		},
		{
			name: "ragged row lengths",
			cells: map[string]string{
				"A1": "Date", "B1": "Description", "C1": "Amount", "D1": "Notes",
				"A2": "2026-01-15", "B2": "Groceries", "C2": "42.50",
				"A3": "2026-01-16", "D3": "orphan note",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := excelize.NewFile()
			sheet := f.GetSheetName(0)
			for ref, val := range tc.cells {
				if err := f.SetCellValue(sheet, ref, val); err != nil {
					t.Fatalf("set %s: %v", ref, err)
				}
			}
			buf, err := f.WriteToBuffer()
			if err != nil {
				t.Fatalf("write workbook: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close workbook: %v", err)
			}

			// Two independent Files so neither read can observe iterator
			// state left behind by the other.
			open := func() *excelize.File {
				g, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()), excelize.Options{RawCellValue: true})
				if err != nil {
					t.Fatalf("open workbook: %v", err)
				}
				return g
			}

			viaLibrary := open()
			defer viaLibrary.Close()
			want, err := viaLibrary.GetRows(sheet)
			if err != nil {
				t.Fatalf("GetRows: %v", err)
			}

			viaOurs := open()
			defer viaOurs.Close()
			got, err := readImportSheetRows(viaOurs, sheet)
			if err != nil {
				t.Fatalf("readImportSheetRows: %v", err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("readImportSheetRows diverged from GetRows\n got: %#v\nwant: %#v", got, want)
			}
		})
	}
}

// buildXLSXWithPaddingEntry returns a valid workbook carrying one extra,
// highly compressible entry of the requested inflated size. The entry is a
// part the importer never reads, which is the point: the archive is fully
// decompressed before anything decides which parts matter.
func buildXLSXWithPaddingEntry(t *testing.T, inflatedBytes int) []byte {
	t.Helper()

	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for ref, val := range map[string]string{
		"A1": "Date", "B1": "Description", "C1": "Amount",
		"A2": "2026-01-15", "B2": "Groceries", "C2": "42.50",
	} {
		if err := f.SetCellValue(sheet, ref, val); err != nil {
			t.Fatalf("set %s: %v", ref, err)
		}
	}
	var base bytes.Buffer
	if err := f.Write(&base); err != nil {
		t.Fatalf("write base workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close base workbook: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(base.Bytes()), int64(base.Len()))
	if err != nil {
		t.Fatalf("open base workbook as zip: %v", err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, zf := range zr.File {
		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", zf.Name, err)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", zf.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatalf("copy zip entry %q: %v", zf.Name, err)
		}
		rc.Close()
	}
	w, err := zw.Create("xl/theme/theme1.xml")
	if err != nil {
		t.Fatalf("create padding entry: %v", err)
	}
	chunk := bytes.Repeat([]byte("A"), 1<<20)
	for written := 0; written < inflatedBytes; written += len(chunk) {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write padding: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("finish zip: %v", err)
	}
	return out.Bytes()
}

// TestHandleImportUpload_ZipBomb_RejectedBeforeDecompression pins the earliest
// cap, and it is the one that matters most: excelize decompresses the entire
// archive inside OpenReader, so every other bound in this handler runs against
// data that has already been allocated. Measured before the cap existed, a
// 2.00 MiB upload drove +10,244 MiB and 15.4s inside OpenReader alone.
//
// The padding lives in a part the importer never opens, which is why bounding
// the worksheet alone is not enough.
//
// Asserting our own message rather than just the 400 is deliberate. There are
// two layers here — checkImportArchiveSize and the UnzipSizeLimit passed to
// OpenReader — and a bare status assertion would stay green with either one
// deleted, since the survivor also produces a 400. The messages differ, so
// this fails if the first layer goes.
func TestHandleImportUpload_ZipBomb_RejectedBeforeDecompression(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "dos-bomb", "admin")

	payload := buildXLSXWithPaddingEntry(t, MaxImportUnzippedBytes+(8<<20))
	if len(payload) > 1<<20 {
		t.Fatalf("payload is %d bytes; a zip bomb that is not tiny is not a zip bomb", len(payload))
	}

	req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
	rec := uploadWithin(t, h, req, 30*time.Second)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprintf("more than %d MiB", MaxImportUnzippedBytes>>20)) {
		t.Errorf("body=%s, want the archive-size reason naming %d MiB",
			rec.Body.String(), MaxImportUnzippedBytes>>20)
	}
}

// TestCheckImportArchiveSize_AcceptsRealisticLedger is the accept side of the
// archive cap. Without it, cutting MaxImportUnzippedBytes to something that
// breaks real imports would ship green.
func TestCheckImportArchiveSize_AcceptsRealisticLedger(t *testing.T) {
	// A full-size ledger: MaxImportRows rows across the recognised headers.
	// Measured at ~3.8 MiB unzipped, comfortably inside the cap.
	rows := make([][]string, 0, MaxImportRows)
	for i := 0; i < MaxImportRows; i++ {
		rows = append(rows, []string{
			fmt.Sprintf("2026-%02d-%02d", i%12+1, i%28+1),
			fmt.Sprintf("Merchant number %d, downtown branch", i),
			fmt.Sprintf("%d.%02d", i%5000, i%100),
			"Groceries",
			"weekly,household",
			fmt.Sprintf("Receipt %d filed for the quarter", i),
		})
	}
	payload := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Tags", "Notes"}, rows)

	if err := checkImportArchiveSize(payload); err != nil {
		t.Fatalf("a %d-row ledger must import: %v", MaxImportRows, err)
	}
	// The prescan runs on every upload, so a false rejection here would refuse
	// every real ledger. This is the accept side of MaxImportCellsPerRow and
	// of the prescan's per-cell measurement.
	if err := prescanImportWorkbook(payload); err != nil {
		t.Fatalf("the prescan must accept a %d-row ledger: %v", MaxImportRows, err)
	}
}

// Requirements the caps must clear, expressed WITHOUT reference to the caps
// themselves. Sizing an accept-side test off the constant it is meant to
// protect makes it scale with any cut and detect nothing — a cell budget of
// 100 kept the suite green until these were written this way.
const (
	// A legitimate workbook may carry a row element anywhere up to Excel's own
	// maximum row, because stray formatting sits below the data, not with it.
	rowsRealFilesNeed = excelize.TotalRows
	// The widest ledger the product claims to accept: MaxImportRows rows at
	// 100 columns. A realistic one is nearer 30 columns.
	cellsRealFilesNeed = MaxImportRows * 100
	// The largest legitimate workbook measured: MaxImportRows rows carrying
	// the maximum permitted description, tags and notes, all distinct, is
	// 31.31 MiB unzipped, of which 28.8 MiB is cell text.
	unzippedBytesRealFilesNeed = 32 << 20
	sheetBytesRealFilesNeed    = 32 << 20
	// The longest field the product accepts is MaxNotesLength CHARACTERS, and
	// the per-cell bound is in BYTES, so the requirement has to allow for
	// UTF-8's worst case. The household writes Arabic as well as English.
	cellBytesRealFilesNeed = MaxNotesLength * 4
	// The widest ledger the product claims to accept.
	cellsPerRowRealFilesNeed = 100
	// A real row is a couple of kilobytes; this leaves three orders of
	// magnitude, so the row bound can never be what refuses a ledger.
	rowBytesRealFilesNeed = 1 << 20
)

// TestImportShapeCaps_ClearRealWorldRequirements states the floors directly.
// The end-to-end tests below exercise them through the handler; this one makes
// a cap cut fail with a message that says which requirement it broke, rather
// than as a puzzling 400 somewhere downstream.
func TestImportShapeCaps_ClearRealWorldRequirements(t *testing.T) {
	if MaxImportSheetRows < rowsRealFilesNeed {
		t.Errorf("MaxImportSheetRows=%d is below Excel's own maximum row (%d); files Excel can write would be refused",
			MaxImportSheetRows, rowsRealFilesNeed)
	}
	if MaxImportSheetCells < cellsRealFilesNeed {
		t.Errorf("MaxImportSheetCells=%d is below a %d-row by 100-column ledger (%d cells)",
			MaxImportSheetCells, MaxImportRows, cellsRealFilesNeed)
	}
	if MaxImportUnzippedBytes < unzippedBytesRealFilesNeed {
		t.Errorf("MaxImportUnzippedBytes=%d MiB is below the largest legitimate workbook measured (%d MiB)",
			MaxImportUnzippedBytes>>20, unzippedBytesRealFilesNeed>>20)
	}
	if MaxImportSheetBytes < sheetBytesRealFilesNeed {
		t.Errorf("MaxImportSheetBytes=%d MiB is below the cell text a full-size ledger carries (%d MiB)",
			MaxImportSheetBytes>>20, sheetBytesRealFilesNeed>>20)
	}
	if MaxImportCellBytes < cellBytesRealFilesNeed {
		t.Errorf("MaxImportCellBytes=%d is below MaxNotesLength (%d chars) at UTF-8's worst case (%d bytes); non-ASCII ledgers would be refused",
			MaxImportCellBytes, MaxNotesLength, cellBytesRealFilesNeed)
	}
	if MaxImportCellsPerRow < cellsPerRowRealFilesNeed {
		t.Errorf("MaxImportCellsPerRow=%d is below the widest ledger the product claims to accept (%d columns)",
			MaxImportCellsPerRow, cellsPerRowRealFilesNeed)
	}

	if MaxImportRowBytes < rowBytesRealFilesNeed {
		t.Errorf("MaxImportRowBytes=%d MiB is below the headroom a real ledger row needs (%d MiB)",
			MaxImportRowBytes>>20, rowBytesRealFilesNeed>>20)
	}

	// The peak a single Rows.Columns() call can reach is the row's text plus
	// one string header per padded column. State it so that raising either
	// term cannot quietly restore a multi-hundred-megabyte peak.
	const (
		peakBudgetMiB  = 64
		stringHeaderSz = 16
	)
	peak := (MaxImportRowBytes + MaxImportCellsPerRow*stringHeaderSz) >> 20
	if peak > peakBudgetMiB {
		t.Errorf("worst-case single row is %d MiB (MaxImportRowBytes=%d MiB + %d padded headers), want <= %d MiB",
			peak, MaxImportRowBytes>>20, MaxImportCellsPerRow, peakBudgetMiB)
	}
}

// TestHandleImportUpload_AtCapBoundaries_StillAccepted is the accept side of
// the row and cell caps, and it exists because both were previously pinned
// only on the reject side: cutting MaxImportSheetCells to 100 and
// MaxImportSheetRows to 12 left the suite green, so a tightening that broke
// real imports could ship unnoticed.
//
// Both payloads are sized off what real files need, NOT off the caps. That
// distinction is the whole point — the first version of this test sized its
// padding off MaxImportSheetCells and so shrank in step with a cut, staying
// green against a budget of 100.
func TestHandleImportUpload_AtCapBoundaries_StillAccepted(t *testing.T) {
	header := headerRow(1)
	dataRow := `<row r="2">` +
		inlineCell("A2", "2026-01-15") +
		inlineCell("B2", "Groceries") +
		inlineCell("C2", "42.50") + `</row>`

	cases := []struct {
		name  string
		sheet string
	}{
		{
			// A row element at Excel's own maximum row. This is the
			// stray-formatting shape: Excel writes a bare <row> for a height
			// or visibility change far below the data, and refusing it would
			// refuse a file Excel itself produced.
			name: "row element at Excel's maximum row",
			sheet: header + dataRow +
				fmt.Sprintf(`<row r="%d">%s</row>`,
					rowsRealFilesNeed, inlineCell(fmt.Sprintf("A%d", rowsRealFilesNeed), "x")),
		},
		{
			// Enough padded rows to cover the widest ledger the product
			// claims to accept.
			name:  "cells covering the widest claimed ledger",
			sheet: header + dataRow + widePaddingRows(cellsRealFilesNeed/excelize.MaxColumns+1),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearImportStore()
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "cap-accept", "admin")

			payload := buildXLSXWithRawSheet(t, tc.sheet)
			req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
			rec := uploadWithin(t, h, req, 60*time.Second)

			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d, want 200 — a file inside both caps must import; body=%s",
					rec.Code, rec.Body.String())
			}
		})
	}
}

// widePaddingRows renders n rows whose only value sits in the far-right
// column, so each costs a full padded row width.
func widePaddingRows(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, `<row r="%d">%s</row>`, i+3, inlineCell(fmt.Sprintf("XFD%d", i+3), "x"))
	}
	return sb.String()
}

// TestReadImportSheetRows_SurfacesErrorWhenWorksheetIsStaged pins the choice
// to read rows.Error() rather than rows.Close().
//
// The two agree only while the worksheet is parsed in memory. Once the
// decompressed XML crosses UnzipXMLSizeLimit, excelize stages the sheet to a
// temp file and Close() returns that file's close error — nil — swallowing the
// iterator's ErrMaxRows entirely. That is not a hypothetical branch: this
// handler now sets UnzipXMLSizeLimit explicitly, so which side of it a real
// upload falls on is a configured property of the system.
//
// Without this test, swapping Error() back to Close() leaves the package green.
func TestReadImportSheetRows_SurfacesErrorWhenWorksheetIsStaged(t *testing.T) {
	// Beyond excelize's TotalRows and in first position, so the iterator sets
	// ErrMaxRows and nothing else can account for the failure.
	payload := buildXLSXWithRawSheet(t,
		fmt.Sprintf(`<row r="%d">%s</row>`, excelize.TotalRows+1, inlineCell("A1", "x")))

	for _, tc := range []struct {
		name           string
		xmlSizeLimit   int64
		wantTempStaged bool
	}{
		{"worksheet held in memory", MaxImportUnzippedPartBytes, false},
		{"worksheet staged to a temp file", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := excelize.OpenReader(bytes.NewReader(payload), excelize.Options{
				RawCellValue:      true,
				UnzipSizeLimit:    MaxImportUnzippedBytes,
				UnzipXMLSizeLimit: tc.xmlSizeLimit,
			})
			if err != nil {
				t.Fatalf("open workbook: %v", err)
			}
			defer f.Close()

			sheet := f.GetSheetName(0)

			// Establish that this configuration really does diverge, so the
			// staged case is not passing for an unrelated reason. Close() is
			// consulted on a separate iterator to avoid disturbing the one
			// under test.
			probe, err := f.Rows(sheet)
			if err != nil {
				t.Fatalf("open probe iterator: %v", err)
			}
			for probe.Next() { //nolint:revive // drain to reach the guard
			}
			probeErr, probeCloseErr := probe.Error(), probe.Close()
			if probeErr == nil {
				t.Fatalf("expected the iterator to record ErrMaxRows, got nil")
			}
			if tc.wantTempStaged && probeCloseErr != nil {
				t.Fatalf("expected Close() to swallow the iterator error when staged, got %v", probeCloseErr)
			}

			if _, err := readImportSheetRows(f, sheet); err == nil {
				t.Fatal("readImportSheetRows returned nil error; the iterator's ErrMaxRows was swallowed")
			} else if !errors.Is(err, errImportSheetTooManyRows) {
				t.Errorf("err=%v, want it mapped to the row-cap advice", err)
			}
		})
	}
}

// --- round 2: bounds on bytes and on declared metadata ---

// deflateBytes returns the raw deflate stream archive/zip would produce for b,
// so a FileHeader can be written with CreateRaw and a size of our choosing.
func deflateBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("x")
	if err != nil {
		t.Fatalf("create temp entry: %v", err)
	}
	if _, err := w.Write(b); err != nil {
		t.Fatalf("write temp entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close temp writer: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reopen temp archive: %v", err)
	}
	rc, err := zr.File[0].OpenRaw()
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	out := new(bytes.Buffer)
	if _, err := out.ReadFrom(rc); err != nil {
		t.Fatalf("read raw: %v", err)
	}
	return out.Bytes()
}

// TestCheckImportArchiveSize_RejectsOverflowingDeclaredSize pins the archive
// check against a declared size that cannot be represented as a positive
// int64.
//
// archive/zip copies UncompressedSize64 out of the zip64 extra field without a
// range check, and FileInfo().Size() converts it to int64 unguarded, so an
// entry declaring 1<<63 reports a NEGATIVE size. Summing those as int64 sends
// the running total so far negative that no later entry can bring it back over
// any limit — the check fails OPEN, and excelize's own UnzipSizeLimit computes
// the same signed sum, so it does not catch it either (it reaches
// make([]byte, 0, negative) and panics).
//
// Note this cannot be built with zw.Create, which only writes honest sizes.
// CreateRaw is what allows the declared size to diverge from reality, and no
// other test in this file exercises that divergence.
func TestCheckImportArchiveSize_RejectsOverflowingDeclaredSize(t *testing.T) {
	compressed := deflateBytes(t, []byte("hello"))
	overflowing := &zip.FileHeader{
		Name:               "xl/decoy.xml",
		Method:             zip.Deflate,
		CompressedSize64:   uint64(len(compressed)),
		UncompressedSize64: 1 << 63,
	}

	t.Run("alone", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.CreateRaw(overflowing)
		if err != nil {
			t.Fatalf("create raw entry: %v", err)
		}
		if _, err := w.Write(compressed); err != nil {
			t.Fatalf("write raw entry: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("close archive: %v", err)
		}

		// Establish that the payload really does carry a negative signed size,
		// so this test cannot quietly stop exercising the overflow.
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("reopen archive: %v", err)
		}
		if got := zr.File[0].FileInfo().Size(); got >= 0 {
			t.Fatalf("FileInfo().Size()=%d, want negative — the payload no longer overflows", got)
		}

		if err := checkImportArchiveSize(buf.Bytes()); !errors.Is(err, errImportArchiveTooLarge) {
			t.Errorf("err=%v, want errImportArchiveTooLarge from a %d-byte archive", err, buf.Len())
		}
	})

	t.Run("as a decoy in front of honestly declared bulk", func(t *testing.T) {
		// The dangerous shape: the overflowing entry poisons the running total
		// so that genuinely large entries after it are never counted.
		payload := bytes.Repeat([]byte("A"), 1<<20)
		compressedBulk := deflateBytes(t, payload)

		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.CreateRaw(overflowing)
		if err != nil {
			t.Fatalf("create raw entry: %v", err)
		}
		if _, err := w.Write(compressed); err != nil {
			t.Fatalf("write raw entry: %v", err)
		}
		bulkEntries := (MaxImportUnzippedBytes / len(payload)) + 8
		for i := 0; i < bulkEntries; i++ {
			bw, err := zw.CreateRaw(&zip.FileHeader{
				Name:               fmt.Sprintf("xl/bulk%d.xml", i),
				Method:             zip.Deflate,
				CompressedSize64:   uint64(len(compressedBulk)),
				UncompressedSize64: uint64(len(payload)),
			})
			if err != nil {
				t.Fatalf("create bulk entry: %v", err)
			}
			if _, err := bw.Write(compressedBulk); err != nil {
				t.Fatalf("write bulk entry: %v", err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("close archive: %v", err)
		}

		if err := checkImportArchiveSize(buf.Bytes()); !errors.Is(err, errImportArchiveTooLarge) {
			t.Errorf("err=%v, want errImportArchiveTooLarge — the decoy suppressed %d MiB of declared bulk",
				err, (bulkEntries*len(payload))>>20)
		}
	})
}

// buildXLSXWithSharedString returns a workbook whose shared-string table holds
// a single item of siLen bytes, referenced refsPerRow times on each of rows
// rows. Every reference is one <c t="s"><v>0</v></c> — a few bytes of input
// that excelize expands into its own copy of the string.
func buildXLSXWithSharedString(t *testing.T, siLen, rows, refsPerRow int) []byte {
	t.Helper()

	f := excelize.NewFile()
	if err := f.SetCellValue(f.GetSheetName(0), "A1", "seed"); err != nil {
		t.Fatalf("seed cell: %v", err)
	}
	var base bytes.Buffer
	if err := f.Write(&base); err != nil {
		t.Fatalf("write base workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close base workbook: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(base.Bytes()), int64(base.Len()))
	if err != nil {
		t.Fatalf("open base workbook as zip: %v", err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, zf := range zr.File {
		if zf.Name == "xl/worksheets/sheet1.xml" || zf.Name == "xl/sharedStrings.xml" {
			continue
		}
		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", zf.Name, err)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", zf.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatalf("copy zip entry %q: %v", zf.Name, err)
		}
		rc.Close()
	}

	sst, err := zw.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatalf("create shared strings: %v", err)
	}
	if _, err := io.WriteString(sst, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="1" uniqueCount="1">`+
		`<si><t>`+strings.Repeat("A", siLen)+`</t></si></sst>`); err != nil {
		t.Fatalf("write shared strings: %v", err)
	}

	sheet, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatalf("create sheet: %v", err)
	}
	if _, err := io.WriteString(sheet, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`); err != nil {
		t.Fatalf("write sheet head: %v", err)
	}
	for r := 1; r <= rows; r++ {
		fmt.Fprintf(sheet, `<row r="%d">`, r)
		for c := 0; c < refsPerRow; c++ {
			name, err := excelize.CoordinatesToCellName(c+1, r)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			fmt.Fprintf(sheet, `<c r="%s" t="s"><v>0</v></c>`, name)
		}
		io.WriteString(sheet, `</row>`)
	}
	if _, err := io.WriteString(sheet, `</sheetData></worksheet>`); err != nil {
		t.Fatalf("write sheet tail: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("finish zip: %v", err)
	}
	return out.Bytes()
}

// TestHandleImportUpload_SharedStringAmplification_Bounded pins the byte
// bounds, which exist because none of the geometry caps is a proxy for memory.
//
// excelize rebuilds a shared string on every cell that references it, so one
// <si> of length L referenced N times yields N separate L-byte allocations,
// all retained in the parsed rows — and those outlive the request, because the
// handler stores them in the in-memory import session. Measured before these
// bounds existed: a 15,774-byte upload with 1,000 rows and 3,000 cells — under
// 0.1% of the unzipped, row and cell caps alike — retained 3,000 MiB.
//
// The two subtests separate the two bounds. A single oversized cell is caught
// by the per-cell limit; many merely-large cells, each individually legal, are
// caught only by the running total.
func TestHandleImportUpload_SharedStringAmplification_Bounded(t *testing.T) {
	cases := []struct {
		name       string
		siLen      int
		rows       int
		refsPerRow int
		wantErr    error
	}{
		{
			name:  "one cell longer than a spreadsheet cell can be",
			siLen: MaxImportCellBytes + 1, rows: 1, refsPerRow: 1,
			wantErr: errImportCellTooLong,
		},
		{
			// Every cell is individually legal; only the sum is not.
			name:  "many individually legal cells summing past the byte budget",
			siLen: MaxImportCellBytes, rows: MaxImportSheetBytes/MaxImportCellBytes + 8, refsPerRow: 1,
			wantErr: errImportSheetTooManyBytes,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearImportStore()
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "dos-bytes", "admin")

			payload := buildXLSXWithSharedString(t, tc.siLen, tc.rows, tc.refsPerRow)

			// The geometry caps must NOT be what rejects this, or the byte
			// bounds are untested. Assert that directly.
			if tc.rows >= MaxImportSheetRows {
				t.Fatalf("payload has %d rows, which trips the row cap (%d)", tc.rows, MaxImportSheetRows)
			}
			if cells := tc.rows * tc.refsPerRow; cells >= MaxImportSheetCells {
				t.Fatalf("payload has %d cells, which trips the cell cap (%d)", cells, MaxImportSheetCells)
			}
			if err := checkImportArchiveSize(payload); err != nil {
				t.Fatalf("payload trips the archive cap, so the byte bound is untested: %v", err)
			}

			req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
			rec := uploadWithin(t, h, req, 60*time.Second)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantErr.Error()) {
				t.Errorf("body=%s\nwant it to name: %s", rec.Body.String(), tc.wantErr)
			}
		})
	}
}

// TestHandleImportUpload_TallSheet_DoesNotPreallocateOffGeometry pins the
// preallocation against the row cap rather than the sheet's geometry.
//
// importRow is 128 bytes, so sizing the parsed-row slice off
// len(rows)-headerIdx-1 reserves 128 MiB for a sheet whose last row element
// sits at Excel's maximum — from a file of about two kilobytes. This is a
// SUCCESS path, so unlike the reject paths there is no error to push back
// with, and it only became reachable when the row cap was raised to Excel's
// maximum: the same file used to be refused at slot 100,001.
//
// The assertion is on live heap after the request rather than on the response,
// because the response is a correct 200 either way.
func TestHandleImportUpload_TallSheet_DoesNotPreallocateOffGeometry(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "prealloc", "admin")

	payload := buildXLSXWithRawSheet(t, headerRow(1)+
		fmt.Sprintf(`<row r="%d">%s%s%s</row>`, MaxImportSheetRows,
			inlineCell(fmt.Sprintf("A%d", MaxImportSheetRows), "2026-01-15"),
			inlineCell(fmt.Sprintf("B%d", MaxImportSheetRows), "Groceries"),
			inlineCell(fmt.Sprintf("C%d", MaxImportSheetRows), "42.50")))

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
	rec := uploadWithin(t, h, req, 60*time.Second)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 — a row at Excel's maximum is legitimate; body=%s",
			rec.Code, rec.Body.String())
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// The blank-run padding is inherent to matching GetRows' shape and costs
	// about 24 MiB at this row count; the preallocation defect added ~128 MiB
	// on top. A 64 MiB ceiling separates the two without being brittle.
	const budgetMiB = 64
	grewMiB := int64(after.HeapAlloc-before.HeapAlloc) / (1 << 20)
	if grewMiB > budgetMiB {
		t.Errorf("live heap grew %d MiB from a %d-byte upload, want <= %d MiB — the parsed-row slice is being sized off sheet geometry rather than MaxImportRows",
			grewMiB, len(payload), budgetMiB)
	}
}

// --- round 4: peaks, not totals ---

// buildXLSXWithWideRow returns a workbook whose single row carries cellsInRow
// cells, each referencing one shared string of siLen bytes. When omitR is set
// the cells carry no `r` attribute, which is what makes cells-per-row
// unbounded in excelize: rowXMLHandler only consults CellNameToCoordinates —
// the thing that enforces MaxColumns — when `r` is present.
func buildXLSXWithWideRow(t *testing.T, siLen, cellsInRow int, omitR bool) []byte {
	t.Helper()

	f := excelize.NewFile()
	if err := f.SetCellValue(f.GetSheetName(0), "A1", "seed"); err != nil {
		t.Fatalf("seed cell: %v", err)
	}
	var base bytes.Buffer
	if err := f.Write(&base); err != nil {
		t.Fatalf("write base workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close base workbook: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(base.Bytes()), int64(base.Len()))
	if err != nil {
		t.Fatalf("open base workbook as zip: %v", err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, zf := range zr.File {
		if zf.Name == "xl/worksheets/sheet1.xml" || zf.Name == "xl/sharedStrings.xml" {
			continue
		}
		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", zf.Name, err)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", zf.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatalf("copy zip entry %q: %v", zf.Name, err)
		}
		rc.Close()
	}
	sst, err := zw.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatalf("create shared strings: %v", err)
	}
	if _, err := io.WriteString(sst, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="1" uniqueCount="1">`+
		`<si><t>`+strings.Repeat("A", siLen)+`</t></si></sst>`); err != nil {
		t.Fatalf("write shared strings: %v", err)
	}
	sheet, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatalf("create sheet: %v", err)
	}
	if _, err := io.WriteString(sheet, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`+
		`<sheetData><row r="1">`); err != nil {
		t.Fatalf("write sheet head: %v", err)
	}
	for i := 0; i < cellsInRow; i++ {
		if omitR {
			io.WriteString(sheet, `<c t="s"><v>0</v></c>`)
			continue
		}
		name, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			t.Fatalf("cell name: %v", err)
		}
		fmt.Fprintf(sheet, `<c r="%s" t="s"><v>0</v></c>`, name)
	}
	if _, err := io.WriteString(sheet, `</row></sheetData></worksheet>`); err != nil {
		t.Fatalf("write sheet tail: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("finish zip: %v", err)
	}
	return out.Bytes()
}

// peakHeapDuringMiB runs fn while sampling live heap, and returns the largest
// value seen. Peak LIVE heap is the quantity the bounds are stated against —
// not total allocation, which also counts short-lived garbage the collector
// reclaims as it goes. The two differ by more than an order of magnitude for a
// streaming scan: parsing 100,000 XML tokens churns tens of MiB through the
// allocator while never holding more than a few hundred KiB at once, and it is
// the holding that kills a container.
func peakHeapDuringMiB(fn func()) uint64 {
	runtime.GC()
	var peak uint64
	done := make(chan struct{})
	sampled := make(chan uint64, 1)
	go func() {
		ticker := time.NewTicker(500 * time.Microsecond)
		defer ticker.Stop()
		var m runtime.MemStats
		for {
			select {
			case <-done:
				sampled <- peak
				return
			case <-ticker.C:
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
			}
		}
	}()
	fn()
	close(done)
	return <-sampled / (1 << 20)
}

// TestPrescanImportWorkbook_BoundsPeakBeforeMaterialising is the round-4
// regression, and the distinction it pins is placement rather than value.
//
// The row, cell and byte budgets are all evaluated on the slice Columns()
// returns, so each can only observe an allocation that has already happened.
// Columns() builds an entire row at once, and its size is (cells in the row) x
// (bytes each cell resolves to) — neither of which excelize bounds. Measured
// through the upload handler before this prescan existed, and answered 400 by
// those budgets every time, far too late:
//
//	 8,003-byte upload, cells with no `r`  -> peak 1,258 MiB
//	50,444-byte upload, cells with `r`     -> peak   516 MiB
//
// The assertion is on ALLOCATION during the prescan, not on the status code.
// A status assertion cannot tell "rejected before building the row" from
// "rejected after building it", which is the entire finding.
func TestPrescanImportWorkbook_BoundsPeakBeforeMaterialising(t *testing.T) {
	cases := []struct {
		name       string
		siLen      int
		cellsInRow int
		omitR      bool
		wantErr    error
	}{
		// Text-heavy: few enough cells to clear the width bound, so only the
		// row's byte sum can refuse it.
		{"a row whose cells sum past the row budget", MaxImportCellBytes,
			(MaxImportRowBytes / MaxImportCellBytes) + 8, false, errImportRowTooLarge},
		// Cell-heavy: the cells carry no text at all, so the byte sum stays at
		// zero and only the width bound can refuse it. This is the shape that
		// costs a string header per padded column.
		{"a row declaring more cells than any sheet can hold", 0,
			MaxImportCellsPerRow + 8, true, errImportRowTooWide},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := buildXLSXWithWideRow(t, tc.siLen, tc.cellsInRow, tc.omitR)

			// None of the earlier caps may be what rejects this, or the
			// prescan is untested.
			if err := checkImportArchiveSize(payload); err != nil {
				t.Fatalf("payload trips the archive cap, so the prescan is untested: %v", err)
			}

			var err error
			peakMiB := peakHeapDuringMiB(func() { err = prescanImportWorkbook(payload) })

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}

			// The prescan streams and holds one int per shared string. The
			// rows it refuses here would have materialised at 512 MiB and
			// 1,258 MiB respectively.
			const budgetMiB = 32
			if peakMiB > budgetMiB {
				t.Errorf("prescan peaked at %d MiB rejecting a %d-byte upload, want <= %d MiB — it is materialising rather than scanning",
					peakMiB, len(payload), budgetMiB)
			}
		})
	}
}

// TestHandleImportUpload_WideRow_RejectedWithBoundedPeak drives the same shape
// through the real handler and watches live heap across the request, so the
// bound is pinned end to end rather than only at the function that enforces it.
func TestHandleImportUpload_WideRow_RejectedWithBoundedPeak(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "dos-widerow", "admin")

	payload := buildXLSXWithWideRow(t, 0, MaxImportCellsPerRow+8, true)

	req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
	var rec *httptest.ResponseRecorder
	peakMiB := peakHeapDuringMiB(func() { rec = uploadWithin(t, h, req, 60*time.Second) })

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprintf("more than %d cells", MaxImportCellsPerRow)) {
		t.Errorf("body=%s, want the row-width reason naming %d cells", rec.Body.String(), MaxImportCellsPerRow)
	}
	// Peak live heap: the defect this pins held the whole row at once, so it
	// shows up here even though it is transient across the request.
	const budgetMiB = 64
	if peakMiB > budgetMiB {
		t.Errorf("request peaked at %d MiB from a %d-byte upload, want <= %d MiB",
			peakMiB, len(payload), budgetMiB)
	}
}

// TestCheckImportArchiveSize_RejectsNonZipInput pins that a workbook which is
// not a zip is refused here rather than passed on.
//
// The OLE/CFB header matters specifically: excelize sniffs those eight bytes
// and runs its decryption path BEFORE any zip reader, where extractPart does
// make([]byte, entry.Size) from an unvalidated directory field. An 8,704-byte
// upload declaring an 8 GiB stream allocated 8,448 MiB, and declaring 64 GiB
// aborted the process outright with a runtime out-of-memory — which chi's
// Recoverer cannot catch, so the whole server dies. Neither the archive size
// check nor UnzipSizeLimit is consulted on that path.
//
// This check used to return nil for non-zip input on the reasoning that
// excelize would fail the same way. It does not.
func TestCheckImportArchiveSize_RejectsNonZipInput(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{
			// The magic excelize routes to its decryption path.
			name: "OLE/CFB compound file header",
			data: append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1},
				bytes.Repeat([]byte{0}, 8696)...),
		},
		{"empty", nil},
		{"arbitrary bytes", []byte("this is not a spreadsheet at all")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkImportArchiveSize(tc.data); !errors.Is(err, errImportNotAZipArchive) {
				t.Errorf("err=%v, want errImportNotAZipArchive", err)
			}
		})
	}
}

// TestCheckImportArchiveSize_RejectsWraparoundPair pins the per-entry bound
// specifically, which the single-decoy test above does not.
//
// With the per-entry check removed but the unsigned total kept, one entry
// declaring 1<<63 still exceeds the limit on its own and is caught. TWO such
// entries sum to exactly 2^64, wrap to zero, and sail through. So the
// per-entry bound is not redundant with the total: it is what makes the total
// incapable of wrapping in the first place.
func TestCheckImportArchiveSize_RejectsWraparoundPair(t *testing.T) {
	compressed := deflateBytes(t, []byte("hello"))

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < 2; i++ {
		w, err := zw.CreateRaw(&zip.FileHeader{
			Name:               fmt.Sprintf("xl/half%d.xml", i),
			Method:             zip.Deflate,
			CompressedSize64:   uint64(len(compressed)),
			UncompressedSize64: 1 << 63,
		})
		if err != nil {
			t.Fatalf("create raw entry: %v", err)
		}
		if _, err := w.Write(compressed); err != nil {
			t.Fatalf("write raw entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	// Establish the wraparound really is exact, so this cannot quietly stop
	// exercising it.
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reopen archive: %v", err)
	}
	var wrapped uint64
	for _, e := range zr.File {
		wrapped += e.UncompressedSize64
	}
	if wrapped != 0 {
		t.Fatalf("declared sizes sum to %d, want an exact wrap to 0", wrapped)
	}

	if err := checkImportArchiveSize(buf.Bytes()); !errors.Is(err, errImportArchiveTooLarge) {
		t.Errorf("err=%v, want errImportArchiveTooLarge from a %d-byte archive whose declared sizes wrap to zero",
			err, buf.Len())
	}
}

// TestPrescanImportWorkbook_BoundsCellTextBeforeMaterialising pins the
// prescan's per-cell measurement, which its sibling above cannot: that one
// trips the cells-per-row bound first, and the post-hoc per-cell check in
// readImportSheetRows produces the same 400 for the same file, so a
// handler-level assertion cannot tell the two apart.
//
// The distinction is peak memory. Here the row holds far fewer cells than the
// width bound allows, so only the per-cell measurement stands between a
// handful of references and (references x shared-string length) materialised
// at once.
func TestPrescanImportWorkbook_BoundsCellTextBeforeMaterialising(t *testing.T) {
	const (
		siBytes = 4 << 20
		cells   = 200
	)
	if cells > MaxImportCellsPerRow {
		t.Fatalf("cells=%d must stay under MaxImportCellsPerRow (%d) so the width bound is not what fires",
			cells, MaxImportCellsPerRow)
	}
	payload := buildXLSXWithWideRow(t, siBytes, cells, false)
	if err := checkImportArchiveSize(payload); err != nil {
		t.Fatalf("payload trips the archive cap, so the prescan is untested: %v", err)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := prescanImportWorkbook(payload)
	runtime.ReadMemStats(&after)

	if !errors.Is(err, errImportCellTooLong) {
		t.Fatalf("err=%v, want errImportCellTooLong", err)
	}
	// Without this bound the row would materialise at cells x siBytes = 800
	// MiB. The prescan's own worst case is one shared string, because
	// encoding/xml hands back a text node in a single CharData.
	const budgetMiB = 32
	grewMiB := (after.TotalAlloc - before.TotalAlloc) / (1 << 20)
	if grewMiB > budgetMiB {
		t.Errorf("prescan allocated %d MiB, want <= %d MiB — it is materialising the row rather than measuring it",
			grewMiB, budgetMiB)
	}
}

// TestPrescanImportWorkbook_AcceptsSheetWithManyInlineStrings pins the reset of
// the running text length at each <c>.
//
// Without it the length accumulates across every inline string in the sheet
// rather than being measured per cell, so an ordinary file crosses
// MaxImportCellBytes on total volume and is refused — a false rejection that
// grows more likely the more real data the file holds. The sheet below carries
// many times MaxImportCellBytes in total while no single cell approaches it,
// which is exactly the shape of a real ledger.
func TestPrescanImportWorkbook_AcceptsSheetWithManyInlineStrings(t *testing.T) {
	const perCell = 500
	rows := (MaxImportCellBytes/perCell)*4 + 10

	var sheet strings.Builder
	sheet.WriteString(headerRow(1))
	value := strings.Repeat("d", perCell)
	for r := 2; r <= rows+1; r++ {
		fmt.Fprintf(&sheet, `<row r="%d">%s%s%s</row>`, r,
			inlineCell(fmt.Sprintf("A%d", r), "2026-01-15"),
			inlineCell(fmt.Sprintf("B%d", r), value),
			inlineCell(fmt.Sprintf("C%d", r), "42.50"))
	}
	payload := buildXLSXWithRawSheet(t, sheet.String())

	totalInline := rows * perCell
	if totalInline <= MaxImportCellBytes {
		t.Fatalf("sheet carries %d bytes of inline text, which must exceed MaxImportCellBytes (%d) for this to test anything",
			totalInline, MaxImportCellBytes)
	}
	if perCell >= MaxImportCellBytes {
		t.Fatalf("per-cell text (%d) must stay under MaxImportCellBytes (%d)", perCell, MaxImportCellBytes)
	}

	if err := prescanImportWorkbook(payload); err != nil {
		t.Errorf("prescan refused an ordinary sheet carrying %d bytes of inline text across %d cells: %v",
			totalInline, rows, err)
	}
}

// TestPrescanImportWorkbook_MeasuresLiteralValues pins that the prescan
// measures a cell whose value is a bare literal, not only shared strings and
// inline text.
//
// This started life as a test for a post-hoc per-cell check, back when the
// prescan measured only text nodes and a long <v> could reach the parsed rows
// unmeasured. Once the prescan resolves every value source excelize reads —
// shared reference, inline string, literal — that post-hoc check had no input
// left to catch and was removed rather than pinned, since a check that cannot
// fire is worse than no check: it looks like protection.
//
// What remains worth pinning is that the literal path is measured at all, and
// measured BEFORE materialisation.
func TestPrescanImportWorkbook_MeasuresLiteralValues(t *testing.T) {
	// A bare <v> with no t="s" and no inline string: not a text node.
	huge := strings.Repeat("1", MaxImportCellBytes+1)
	payload := buildXLSXWithRawSheet(t, headerRow(1)+
		`<row r="2"><c r="A2"><v>`+huge+`</v></c></row>`)

	if err := prescanImportWorkbook(payload); !errors.Is(err, errImportCellTooLong) {
		t.Errorf("err=%v, want errImportCellTooLong — a literal value is unmeasured", err)
	}
}

// --- round 5: the file the household actually imports ---

// buildGoogleSheetsStyleXLSX writes a workbook shaped the way Google Sheets
// exports one, which is NOT how excelize's writer or Excel does it. The
// differences are the ones that matter to a shape bound:
//
//   - every value goes through the shared-string table, including short
//     repeated ones like a category name;
//   - the sheet declares a <dimension> and <cols>, and emits a cell for every
//     column of the used range on every row — including empty ones carrying
//     only a style, which excelize's writer omits entirely;
//   - the used range is the sheet's grid, not the data, so a small ledger
//     still exports the full default column width.
//
// A bound tuned only against excelize-written fixtures can pass its own tests
// and still refuse this, which is the regression this guards.
func buildGoogleSheetsStyleXLSX(t *testing.T, dataRows, gridColumns int) []byte {
	t.Helper()
	if gridColumns < 6 {
		t.Fatalf("gridColumns=%d must cover the six ledger headers", gridColumns)
	}

	headers := []string{"Date", "Description", "Amount", "Category", "Tags", "Notes"}
	// Shared-string table: headers, then per-row values, with the category
	// deduped exactly as Sheets would.
	var shared []string
	index := map[string]int{}
	intern := func(v string) int {
		if i, ok := index[v]; ok {
			return i
		}
		index[v] = len(shared)
		shared = append(shared, v)
		return index[v]
	}
	for _, h := range headers {
		intern(h)
	}

	type cell struct {
		ref    string
		shared int // -1 for a numeric literal
		number string
	}
	rows := make([][]cell, 0, dataRows+1)

	headerCells := make([]cell, 0, gridColumns)
	for c := 0; c < gridColumns; c++ {
		ref, err := excelize.CoordinatesToCellName(c+1, 1)
		if err != nil {
			t.Fatalf("cell name: %v", err)
		}
		if c < len(headers) {
			headerCells = append(headerCells, cell{ref: ref, shared: intern(headers[c])})
			continue
		}
		headerCells = append(headerCells, cell{ref: ref, shared: -2}) // styled but empty
	}
	rows = append(rows, headerCells)

	for r := 0; r < dataRows; r++ {
		rowCells := make([]cell, 0, gridColumns)
		values := []string{
			fmt.Sprintf("2026-%02d-%02d", r%12+1, r%28+1),
			fmt.Sprintf("Merchant %d", r),
			"", // amount is numeric, written as a literal
			"Groceries",
			"weekly,household",
			fmt.Sprintf("Receipt %d", r),
		}
		for c := 0; c < gridColumns; c++ {
			ref, err := excelize.CoordinatesToCellName(c+1, r+2)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			switch {
			case c == 2:
				rowCells = append(rowCells, cell{ref: ref, shared: -1,
					number: fmt.Sprintf("%d.%02d", r%500, r%100)})
			case c < len(values):
				rowCells = append(rowCells, cell{ref: ref, shared: intern(values[c])})
			default:
				rowCells = append(rowCells, cell{ref: ref, shared: -2})
			}
		}
		rows = append(rows, rowCells)
	}

	lastCol, err := excelize.ColumnNumberToName(gridColumns)
	if err != nil {
		t.Fatalf("column name: %v", err)
	}

	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	fmt.Fprintf(&sheet, `<dimension ref="A1:%s%d"/>`, lastCol, len(rows))
	fmt.Fprintf(&sheet, `<cols><col min="1" max="%d" width="14.5" customWidth="1"/></cols>`, gridColumns)
	sheet.WriteString(`<sheetData>`)
	for i, rowCells := range rows {
		fmt.Fprintf(&sheet, `<row r="%d" spans="1:%d">`, i+1, gridColumns)
		for _, c := range rowCells {
			switch c.shared {
			case -2:
				fmt.Fprintf(&sheet, `<c r="%s" s="1"/>`, c.ref)
			case -1:
				fmt.Fprintf(&sheet, `<c r="%s" s="2"><v>%s</v></c>`, c.ref, c.number)
			default:
				fmt.Fprintf(&sheet, `<c r="%s" s="1" t="s"><v>%d</v></c>`, c.ref, c.shared)
			}
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)

	var sst strings.Builder
	fmt.Fprintf(&sst, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`,
		len(shared), len(shared))
	for _, v := range shared {
		sst.WriteString(`<si><t xml:space="preserve">` + v + `</t></si>`)
	}
	sst.WriteString(`</sst>`)

	// Reuse an excelize-written package for the parts that carry no data, then
	// replace the sheet and the shared-string table with the Sheets-shaped ones.
	base := excelize.NewFile()
	if err := base.SetCellValue(base.GetSheetName(0), "A1", "seed"); err != nil {
		t.Fatalf("seed cell: %v", err)
	}
	var baseBuf bytes.Buffer
	if err := base.Write(&baseBuf); err != nil {
		t.Fatalf("write base workbook: %v", err)
	}
	if err := base.Close(); err != nil {
		t.Fatalf("close base workbook: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(baseBuf.Bytes()), int64(baseBuf.Len()))
	if err != nil {
		t.Fatalf("open base workbook: %v", err)
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, zf := range zr.File {
		if zf.Name == "xl/worksheets/sheet1.xml" || zf.Name == "xl/sharedStrings.xml" {
			continue
		}
		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", zf.Name, err)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", zf.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatalf("copy zip entry %q: %v", zf.Name, err)
		}
		rc.Close()
	}
	for name, body := range map[string]string{
		"xl/worksheets/sheet1.xml": sheet.String(),
		"xl/sharedStrings.xml":     sst.String(),
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("finish zip: %v", err)
	}
	return out.Bytes()
}

// TestHandleImportUpload_GoogleSheetsExport_StillImports is the accept-side
// test that matters most in this file: the household imports Google Sheets
// exports, so a shape bound that refuses one is a worse outcome than the
// residual it removes.
//
// The wide case uses the FORMAT's maximum column, which is the widest a real
// export can be: Google Sheets' own grid goes wider than Excel's, but an .xlsx
// cannot — SpreadsheetML caps a sheet at 16,384 columns, and
// CoordinatesToCellName refuses to name one beyond it. So this is the widest
// row any spreadsheet application can hand us with cell references intact.
func TestHandleImportUpload_GoogleSheetsExport_StillImports(t *testing.T) {
	cases := []struct {
		name        string
		dataRows    int
		gridColumns int
	}{
		{"default Sheets grid width", 500, 26},
		{"widest sheet the format allows", 50, excelize.MaxColumns},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearImportStore()
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "sheets", "admin")

			payload := buildGoogleSheetsStyleXLSX(t, tc.dataRows, tc.gridColumns)

			// Each bound in turn, so a failure names the one that refused a
			// real file rather than surfacing as an opaque 400.
			if err := checkImportArchiveSize(payload); err != nil {
				t.Fatalf("archive bound refused a Google Sheets export: %v", err)
			}
			if err := prescanImportWorkbook(payload); err != nil {
				t.Fatalf("prescan refused a Google Sheets export: %v", err)
			}

			req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
			rec := uploadWithin(t, h, req, 120*time.Second)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode preview: %v", err)
			}
			if n, _ := resp["row_count"].(float64); int(n) != tc.dataRows {
				t.Errorf("row_count=%v, want %d — the export did not parse as the ledger it is",
					resp["row_count"], tc.dataRows)
			}
		})
	}
}
