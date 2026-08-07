package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/go-chi/chi/v5"
	"github.com/xuri/excelize/v2"

	"github.com/elienop/spendrop/internal/auth"
)

// buildTransactionWhereClause extracts filter parameters from URL query values
// and builds a SQL WHERE clause string with placeholder args. The returned
// clause includes the leading " WHERE " when conditions exist, or an empty
// string when no filters apply. The conditions reference table aliases "t"
// (transactions) and "c" (categories).
func buildTransactionWhereClause(q url.Values) (string, []any) {
	var conditions []string
	var args []any

	if v := q.Get("date_from"); v != "" {
		if _, err := time.Parse("2006-01-02", v); err == nil {
			conditions = append(conditions, "date(t.date) >= ?")
			args = append(args, v)
		}
	}
	if v := q.Get("date_to"); v != "" {
		if _, err := time.Parse("2006-01-02", v); err == nil {
			conditions = append(conditions, "date(t.date) <= ?")
			args = append(args, v)
		}
	}
	if v := q.Get("category_id"); v != "" && q.Get("category_ids") == "" {
		if catID, err := strconv.ParseInt(v, 10, 64); err == nil {
			conditions = append(conditions, "t.category_id = ?")
			args = append(args, catID)
		}
	}
	if v := q.Get("type"); v != "" {
		if v == CategoryTypeExpense || v == CategoryTypeIncome {
			conditions = append(conditions, "c.type = ?")
			args = append(args, v)
		}
	}
	if v := q.Get("search"); v != "" {
		conditions, args = appendSearchCondition(conditions, args, v)
	}

	// Amount range. Phase 3.1a: user-entered min/max arrive as float
	// strings; parse once, convert to int64 cents immediately, then compare
	// against t.amount_cents (not t.amount) so the filter inherits the
	// exact-integer semantics of the cents column. Comparing against the
	// legacy REAL column could drop edge-case rows where a float roundtrip
	// shifted the stored value by one ULP from the user's input.
	// safeDollarsToCents, not dollarsToCents: ParseFloat happily accepts
	// "1e308", "NaN" and "Inf", all of which convert to int64 minimum. That
	// turned `amount_cents >= ?` into a match-everything predicate and
	// `amount_cents <= ?` into a match-nothing one — the filter silently
	// inverted instead of erroring. An unrepresentable bound is now skipped,
	// exactly as an unparseable one already was.
	if v := q.Get("amount_min"); v != "" {
		if min, err := strconv.ParseFloat(v, 64); err == nil {
			if cents, ok := safeDollarsToCents(min); ok {
				conditions = append(conditions, "t.amount_cents >= ?")
				args = append(args, cents)
			}
		}
	}
	if v := q.Get("amount_max"); v != "" {
		if max, err := strconv.ParseFloat(v, 64); err == nil {
			if cents, ok := safeDollarsToCents(max); ok {
				conditions = append(conditions, "t.amount_cents <= ?")
				args = append(args, cents)
			}
		}
	}

	// Multi-category: comma-separated IDs like "1,3,5", capped at MaxMultiCategoryFilter
	if v := q.Get("category_ids"); v != "" {
		idStrs := strings.Split(v, ",")
		if len(idStrs) > MaxMultiCategoryFilter {
			idStrs = idStrs[:MaxMultiCategoryFilter]
		}
		var placeholders []string
		for _, s := range idStrs {
			if catID, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				placeholders = append(placeholders, "?")
				args = append(args, catID)
			}
		}
		if len(placeholders) > 0 {
			conditions = append(conditions, "t.category_id IN ("+strings.Join(placeholders, ",")+")")
		}
	}

	// Tags filter (search within comma-separated tags string)
	if v := q.Get("tags"); v != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
		conditions = append(conditions, "t.tags LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escaped+"%")
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

// searchAmountPattern recognises a search term that is a bare positive decimal
// number, optionally written with canonical three-digit thousands separators.
// It anchors both ends, so a term with any other character in it — a currency
// symbol, a minus sign, an exponent, a stray letter — is not a number and never
// reaches the foreign-amount arm below.
//
// The alternation is what rejects "1,2,3": a comma-grouped number must be one
// to three leading digits followed by groups of exactly three, while a plain
// number carries no commas at all. Fractions are capped at two digits because
// the column stores cents; "1500000.005" is not a value any row can hold.
var searchAmountPattern = regexp.MustCompile(`^(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]{1,2})?$`)

// searchAmountCents interprets a search term as a foreign-currency amount and
// returns it in cents, reporting false for any term that is not a bare number.
//
// The term is trimmed and stripped of thousands separators for THIS test only —
// the LIKE arms keep matching the term exactly as typed, so widening the search
// cannot change which rows the text arms already matched.
//
// A term above MaxTransactionAmount is rejected rather than clamped, for the
// same reason the amount_min/amount_max bounds are (see safeDollarsToCents): an
// out-of-range float converts to int64 minimum and would turn the equality into
// a match against a nonsense value. No storable row can carry such an amount
// anyway — validateMoneyAmount bounds original_amount on the write path — so a
// term past the cap has nothing to find.
func searchAmountCents(term string) (int64, bool) {
	trimmed := strings.TrimSpace(term)
	if !searchAmountPattern.MatchString(trimmed) {
		return 0, false
	}
	amount, err := strconv.ParseFloat(strings.ReplaceAll(trimmed, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	return safeDollarsToCents(amount)
}

// appendSearchCondition renders the ?search= term as one parenthesised OR group
// and appends it, with its args, to a set of conditions under construction.
//
// Scope: the free-text a user can read on a row — description, notes and the
// category name — plus the foreign amount when the term is a bare number. TAGS
// ARE DELIBERATELY OUT OF SCOPE: they have their own ?tags= filter, and folding
// them in here would make one control silently widen the other with no way to
// ask for description-only from the UI.
//
// Every text arm reuses the SAME escaped pattern, so LIKE-metacharacter
// escaping and SQLite's ASCII-case-insensitive LIKE apply identically across
// all of them — a term containing % or _ is as literal on notes and category
// name as it already was on description.
//
// The OR arms are wrapped in parentheses before joining, because the caller
// composes conditions with AND: unwrapped, the first OR would rebind every
// other filter and a search combined with a date range would return rows
// outside the range.
//
// Widening this predicate widens update-by-filter and delete-by-filter with it,
// which is intended rather than incidental. Their preview count and their write
// scope are both serialised from this one clause, so the number the confirm
// dialog shows is by construction the number of rows the write touches; forking
// a narrower predicate for the list would be what breaks that.
func appendSearchCondition(conditions []string, args []any, term string) ([]string, []any) {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
	pattern := "%" + escaped + "%"

	// t.notes is nullable and needs no COALESCE: `NULL LIKE ?` is NULL, which an
	// OR treats as false, and a '%term%' pattern could never match '' anyway.
	// Wrapping it would be behaviourally identical and unkillable by any test —
	// the same unreachable-guard objection the foreign-amount arm below records.
	arms := []string{
		"t.description LIKE ? ESCAPE '\\'",
		"t.notes LIKE ? ESCAPE '\\'",
		"c.name LIKE ? ESCAPE '\\'",
	}
	args = append(args, pattern, pattern, pattern)

	// Foreign amount, matched EXACTLY on the value in cents rather than as a
	// substring of the digits. A substring match would make "1500" find every
	// 21,500 and 1,500,000 in the ledger, which on delete-by-filter is a
	// selection the user did not ask for.
	//
	// A base-currency row is excluded by the column, not by a guard:
	// original_amount_cents is NULL there, and `NULL = ?` is NULL, which an OR
	// treats as false. An explicit IS NOT NULL test would be unreachable code
	// that no test could kill.
	if cents, ok := searchAmountCents(term); ok {
		arms = append(arms, "t.original_amount_cents = ?")
		args = append(args, cents)
	}

	return append(conditions, "("+strings.Join(arms, " OR ")+")"), args
}

// appendLiveTransactionsFilter tacks the soft-delete predicate onto a WHERE
// clause built by buildTransactionWhereClause. The helper is shared between
// list, export, and delete-by-filter handlers, and the helper itself is
// shared with a future trash view that does NOT want the live-only filter.
// Rather than baking deleted_at into buildTransactionWhereClause, every live
// read routes its output through this helper so the live-vs-trash split
// stays explicit at the call site.
//
// If the input clause is empty (no user filters), we emit a fresh WHERE.
// Otherwise we extend the existing WHERE with an AND. The helper assumes
// the input clause came from buildTransactionWhereClause and therefore
// either starts with " WHERE " or is empty.
func appendLiveTransactionsFilter(whereClause string) string {
	if whereClause == "" {
		return " WHERE t.deleted_at IS NULL"
	}
	return whereClause + " AND t.deleted_at IS NULL"
}

// --- Over-long cell values -------------------------------------------------
//
// The .xlsx format caps a cell at 32,767 characters, and excelize enforces that
// cap by SILENTLY TRUNCATING: setCellString / trimCellValue (cell.go) call
// truncateUTF16Units and return a nil error, so neither SetCellValue nor
// StreamWriter.SetRow reports anything. Checking their error returns for this
// case would be a no-op — verified against v2.11.0 and v2.10.1, and confirmed by
// round-tripping a 40,000-character value through both writers (it reads back at
// exactly 32,767 with no error on either side).
//
// The ledger can hold such values: the spreadsheet importer performed no
// field-length validation until recently, so historic imports could write
// descriptions, tags and notes of any length. Left alone, one of those exports
// as a value that LOOKS complete — no ellipsis, no error, no clue — in a file
// that otherwise reconciles. That is the failure this section exists to prevent.
//
// The behaviour chosen is mark-and-report rather than fail-the-download:
//
//   - Every shortened cell carries an explicit marker in its own text, so a user
//     reconciling that row sees it exactly where they are looking.
//   - The workbook grows an "Export Warnings" sheet listing every affected cell,
//     and that sheet is made the ACTIVE one, so the file opens on the warning
//     instead of hiding it behind 50,000 rows.
//   - The response carries X-Export-Truncated-Cells for API-token consumers,
//     which never see a spreadsheet tab.
//
// Failing the whole download was rejected. Export is the household's only way to
// get its data out of SpenDrop; refusing to produce any file because one historic
// row is over-long converts a fidelity problem into a total loss of access, and
// the user cannot clear the condition in bulk from the UI. A marked cell keeps
// the 49,999 good rows usable while still making the file self-evidently
// abridged. Silent truncation (today) and silent omission both fail the "the
// user can always tell" bar; this does not.

// maxExportCellChars mirrors excelize.TotalCellChars, the spreadsheet format's
// hard cap on one cell, measured in UTF-16 code units (a rune outside the BMP
// costs two). Referenced through excelize rather than re-typed so a future
// upstream change cannot leave this file quietly disagreeing with the writer.
const maxExportCellChars = excelize.TotalCellChars

// maxExportTruncationNotes bounds the Export Warnings sheet. A ledger where
// every row is over-long would otherwise turn the warning sheet into a second
// 50,000-row sheet — and the memory the rest of this file exists to save.
// The count above the listed notes is still reported in full.
const maxExportTruncationNotes = 500

// exportWarningsSheet is the sheet name used for the truncation report.
const exportWarningsSheet = "Export Warnings"

// utf16Len counts s in UTF-16 code units, the unit the xlsx cell limit is
// expressed in. Matches excelize's own countUTF16String so a value this package
// accepts is a value excelize will not shorten.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		// range over a string yields utf8.RuneError for invalid bytes, never a
		// bare surrogate, so RuneLen is >= 1 here; the guard is belt and braces.
		if l := utf16.RuneLen(r); l > 0 {
			n += l
		} else {
			n++
		}
	}
	return n
}

// truncateUTF16 returns the longest prefix of s that fits in limit UTF-16 code
// units, cut on a rune boundary so the result is always valid UTF-8.
func truncateUTF16(s string, limit int) string {
	n := 0
	for i, r := range s {
		l := utf16.RuneLen(r)
		if l <= 0 {
			l = 1
		}
		if n+l > limit {
			return s[:i]
		}
		n += l
	}
	return s
}

// exportTruncationMarker is the suffix appended to a shortened cell. ASCII only:
// it has to survive being read in a spreadsheet, searched for, and pasted into a
// support message.
func exportTruncationMarker(chars int) string {
	return fmt.Sprintf(" [TRUNCATED BY SPENDROP: full value is %d characters, a spreadsheet cell holds at most %d]",
		chars, maxExportCellChars)
}

// exportTruncationNote is one shortened cell, for the Export Warnings sheet.
type exportTruncationNote struct {
	sheet  string
	cell   string
	column string
	chars  int
}

// exportTruncations accumulates the cells an export could not write in full.
// The zero value is ready to use.
type exportTruncations struct {
	notes []exportTruncationNote
	total int
}

// text prepares a ledger string for the cell at (col, row) on sheet. A value
// that fits is returned unchanged. A value that does not is shortened and given
// an explicit marker, and the cell is recorded for the warnings sheet.
//
// The marker is counted against the cell limit before the value is cut, so the
// result always fits and excelize never gets the chance to shorten it further —
// which would eat the marker itself and put us back where we started.
func (x *exportTruncations) text(sheet, column string, col, row int, v string) string {
	chars := utf16Len(v)
	if chars <= maxExportCellChars {
		return v
	}
	marker := exportTruncationMarker(chars)
	kept := truncateUTF16(v, maxExportCellChars-utf16Len(marker))

	x.total++
	if len(x.notes) < maxExportTruncationNotes {
		x.notes = append(x.notes, exportTruncationNote{
			sheet: sheet, cell: cellAt(col, row), column: column, chars: chars,
		})
	}
	return kept + marker
}

// writeExportWarningsSheet appends the truncation report and makes it the active
// sheet, so an abridged workbook opens on the warning rather than on data that
// looks complete. A no-op when nothing was shortened: a warnings tab on every
// export would train the user to ignore it.
func writeExportWarningsSheet(f *excelize.File, x *exportTruncations) error {
	if x.total == 0 {
		return nil
	}
	idx, err := f.NewSheet(exportWarningsSheet)
	if err != nil {
		return fmt.Errorf("create warnings sheet: %w", err)
	}
	sw, err := f.NewStreamWriter(exportWarningsSheet)
	if err != nil {
		return fmt.Errorf("stream warnings sheet: %w", err)
	}

	rows := [][]any{
		{"This export is NOT a faithful copy of your ledger."},
		{fmt.Sprintf("%d cell value(s) were longer than a spreadsheet cell can hold (%d characters) and were shortened.",
			x.total, maxExportCellChars)},
		{"Each shortened cell ends with a [TRUNCATED BY SPENDROP: ...] marker. The full values are unchanged in SpenDrop."},
		{},
		{"Sheet", "Cell", "Column", "Characters in SpenDrop"},
	}
	for _, n := range x.notes {
		rows = append(rows, []any{n.sheet, n.cell, n.column, n.chars})
	}
	if x.total > len(x.notes) {
		rows = append(rows, []any{fmt.Sprintf("... and %d more not listed (this sheet lists the first %d).",
			x.total-len(x.notes), len(x.notes))})
	}

	for i, r := range rows {
		if err := sw.SetRow(cellAt(1, i+1), r); err != nil {
			return fmt.Errorf("write warnings row %d: %w", i+1, err)
		}
	}
	if err := sw.Flush(); err != nil {
		return fmt.Errorf("flush warnings sheet: %w", err)
	}
	f.SetActiveSheet(idx)
	return nil
}

// setExportTruncationHeader advertises the truncation count to consumers that
// never open a spreadsheet tab — the API-token surface and any script driving
// the export endpoints. Header-only clients still get a signal that the payload
// is abridged; the header is absent when the export is faithful.
func setExportTruncationHeader(w http.ResponseWriter, x *exportTruncations) {
	if x.total > 0 {
		w.Header().Set("X-Export-Truncated-Cells", strconv.Itoa(x.total))
	}
}

// handleExportTransactions exports filtered transactions as an Excel (.xlsx)
// file. It accepts the same query parameters as handleListTransactions.
// exportTxnRow is one transactions row drained from the database and held in
// memory until the workbook is assembled.
//
// Production pins the connection pool to a single connection
// (SetMaxOpenConns(1) in cmd/spendrop/db.go — load-bearing, not tuning), so an
// open *sql.Rows holds the process's ONLY connection. Building the workbook
// while the cursor is open therefore blocks every other request for the
// duration, and any query issued during the build can never acquire a
// connection at all: it waits on a cursor that only returns after the handler
// does. Draining first bounds the hold to the read itself.
//
// The slice is bounded in ROW COUNT by MaxExportRows, and at household field
// lengths that is the whole story: 50,000 rows measure 38 MiB. It is NOT bounded
// in per-row text. description/tags/notes are capped at MaxDescriptionLength /
// MaxTagsLength / MaxNotesLength on the API, but the spreadsheet importer wrote
// them unvalidated until recently,
// so a legacy ledger can hold much longer values and the drain is O(total text)
// over the selected rows. Feeding rows straight from the cursor into the stream
// writer would remove that term entirely, at the cost of holding the pool's only
// connection across the whole workbook build (~270 ms at 50,000 rows) instead of
// just the read (~80 ms) — a deliberate trade this file has not taken.
type exportTxnRow struct {
	date         time.Time
	desc         string
	catName      string
	catType      string
	amountCents  int64
	origAmtCents sql.NullInt64
	origCur      sql.NullString
	tags         sql.NullString
	notes        sql.NullString
}

// drainExportTxnRows runs the transactions export query and fully consumes the
// cursor before returning, releasing the connection back to the pool.
func (h *Handler) drainExportTxnRows(ctx context.Context, query string, args ...any) ([]exportTxnRow, error) {
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []exportTxnRow
	for rows.Next() {
		var t exportTxnRow
		if err := rows.Scan(&t.date, &t.desc, &t.catName, &t.catType, &t.amountCents,
			&t.origAmtCents, &t.origCur, &t.tags, &t.notes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// categoryTotalRow is one row of a category-totals query (Summary sheet in the
// monthly export, Category Totals sheet in the yearly one), drained from the
// database before any workbook cell is written.
type categoryTotalRow struct {
	name    string
	catType string
	cents   int64
}

// drainCategoryTotals runs a category-totals query and fully consumes the
// cursor before returning.
//
// Draining is a correctness requirement, not a style choice. Under the
// production pool cap of one connection (see exportTxnRow) an open *sql.Rows
// holds the process's only connection, so ANY query the caller issues next —
// getBaseCurrency, the transactions query, the category query — waits on a
// cursor that is only released when the handler returns.
//
// The inline loops this replaced were not themselves broken: they ran to
// exhaustion, and an exhausted *sql.Rows auto-closes. (The export deadlock that
// did reach production was handleExportTransactions issuing a query with its
// cursor still open, fixed separately.) What draining buys is that the safety
// stops being incidental — under the inline shape one `break` or one early
// `return` added to a loop would have deadlocked the whole server, with nothing
// in the code or the tests to catch it. Returning a slice moves the release into
// a deferred Close that no control flow can skip.
//
// The row count is bounded by the number of categories, so the memory ceiling
// is the same one the categories table already imposes.
func (h *Handler) drainCategoryTotals(ctx context.Context, query string, args ...any) ([]categoryTotalRow, error) {
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query category totals: %w", err)
	}
	defer rows.Close()

	var out []categoryTotalRow
	for rows.Next() {
		var c categoryTotalRow
		if err := rows.Scan(&c.name, &c.catType, &c.cents); err != nil {
			return nil, fmt.Errorf("scan category total: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate category totals: %w", err)
	}
	return out, nil
}

// categoryTotalsHeaders is the Category/Type/Total header row shared by the
// monthly Summary sheet and the yearly Category Totals sheet. A function, not a
// package-level slice: SetRow takes it by reference and a shared mutable header
// row is the kind of global this package does without.
func categoryTotalsHeaders() []any { return []any{"Category", "Type", "Total"} }

// writeCategoryTotals renders drained category totals onto a sheet starting at
// startRow. The column layout matches the Category/Type/Total headers written
// by the monthly and yearly export handlers.
func writeCategoryTotals(sw *excelize.StreamWriter, sheet string, startRow int, totals []categoryTotalRow, x *exportTruncations) error {
	row := startRow
	for _, c := range totals {
		if err := sw.SetRow(cellAt(1, row), []any{
			x.text(sheet, "Category", 1, row, c.name),
			c.catType,
			centsToDollars(c.cents),
		}); err != nil {
			return fmt.Errorf("write category totals row %d: %w", row, err)
		}
		row++
	}
	return nil
}

// exportMonthTotal holds one calendar month's expense and income totals in cents.
type exportMonthTotal struct {
	expensesCents, incomeCents int64
}

// drainMonthlyTotals runs the per-month totals query and fully consumes the
// cursor before returning, for the same single-connection reason as
// drainCategoryTotals. Months absent from the result set keep their zero value,
// which is how the yearly export renders a month with no activity.
func (h *Handler) drainMonthlyTotals(ctx context.Context, query string, args ...any) ([12]exportMonthTotal, error) {
	var months [12]exportMonthTotal

	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return months, fmt.Errorf("query monthly totals: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var monthNum int
		var expensesCents, incomeCents int64
		if err := rows.Scan(&monthNum, &expensesCents, &incomeCents); err != nil {
			return months, fmt.Errorf("scan monthly total: %w", err)
		}
		if monthNum >= 1 && monthNum <= 12 {
			months[monthNum-1] = exportMonthTotal{expensesCents: expensesCents, incomeCents: incomeCents}
		}
	}
	if err := rows.Err(); err != nil {
		return months, fmt.Errorf("iterate monthly totals: %w", err)
	}
	return months, nil
}

// exportTxnHeaders is the Transactions header row, shared by the top-level and
// monthly exports so their column layouts cannot drift apart.
func exportTxnHeaders(baseCurrency string) []any {
	return []any{"Date", "Description", "Category", "Type",
		fmt.Sprintf("Amount (%s)", baseCurrency), "Original Amount",
		"Original Currency", "Tags", "Notes"}
}

// writeExportTxnRows renders drained rows onto a sheet starting at startRow.
// The column layout matches exportTxnHeaders.
//
// One SetRow call per transaction rather than nine SetCellValue calls: the
// stream writer serialises the row straight to its spill buffer, so the
// workbook never holds a cell tree for 50,000 rows (see handleExportTransactions
// for the measured effect). nil entries leave a cell absent, which is how the
// NULL currency/tags/notes columns were already rendered.
func writeExportTxnRows(sw *excelize.StreamWriter, sheet string, startRow int, txns []exportTxnRow, x *exportTruncations) error {
	row := startRow
	// Reused across iterations: SetRow consumes the slice synchronously
	// (stream.go, SetRow writes each value into its own buffer and returns), so
	// no row can retain a reference to it.
	//
	// Sized from the header row and cleared IN FULL each iteration, both
	// deliberately. The previous version made this []any{} nine long by hand and
	// reset only vals[5..8] — the four nullable columns — as an index literal.
	// That was correct and completely unguarded: deleting the reset left the
	// whole internal/api suite green while a bare row silently inherited the
	// previous transaction's currency, tags and notes, with its own date and
	// amount intact so the workbook still reconciled. Adding a tenth header
	// likewise stayed green, with the header row one column wider than the data.
	//
	// Neither drift is possible now: the width follows exportTxnHeaders, and a
	// new nullable column is cleared without anyone remembering to extend a
	// tuple. Keep it that way — an index literal here is a correctness
	// invariant maintained by discipline, and the discipline had no test.
	vals := make([]any, len(exportTxnHeaders("")))
	for _, t := range txns {
		for i := range vals {
			vals[i] = nil
		}
		vals[0] = t.date.Format("2006-01-02")
		vals[1] = x.text(sheet, "Description", 2, row, t.desc)
		vals[2] = x.text(sheet, "Category", 3, row, t.catName)
		vals[3] = t.catType
		vals[4] = centsToDollars(t.amountCents)
		if t.origAmtCents.Valid {
			vals[5] = centsToDollars(t.origAmtCents.Int64)
		}
		if t.origCur.Valid {
			vals[6] = t.origCur.String
		}
		if t.tags.Valid {
			vals[7] = x.text(sheet, "Tags", 8, row, t.tags.String)
		}
		if t.notes.Valid {
			vals[8] = x.text(sheet, "Notes", 9, row, t.notes.String)
		}
		if err := sw.SetRow(cellAt(1, row), vals); err != nil {
			return fmt.Errorf("write transactions row %d: %w", row, err)
		}
		row++
	}
	return nil
}

func (h *Handler) handleExportTransactions(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Transactions are visible to all authenticated household members (by design).

	whereClause, args := buildTransactionWhereClause(r.URL.Query())
	liveClause := appendLiveTransactionsFilter(whereClause)

	// Phase 3.1a: SELECT t.amount_cents / t.original_amount_cents and
	// convert to float dollars at the Excel cell boundary. The xlsx wire
	// format is unchanged (dollars as a spreadsheet number) because that is
	// what every downstream consumer - humans, Excel formulae, pivot
	// tables - already expects; the only move is shifting the conversion
	// step from the SQL side (legacy REAL) to the Go side (int64 ->
	// float64 via centsToDollars).
	query := `SELECT t.date, t.description, c.name AS category_name, c.type AS category_type,
		t.amount_cents, t.original_amount_cents, t.original_currency, t.tags, t.notes
		FROM transactions t
		JOIN categories c ON t.category_id = c.id` + liveClause + ` ORDER BY t.date DESC, t.id DESC LIMIT ?`
	args = append(args, MaxExportRows)

	// Resolve the base currency BEFORE opening the cursor. getBaseCurrency
	// issues its own query, and under the production pool cap of one
	// connection that query can never be served while the cursor below holds
	// the only connection — the handler would block on itself indefinitely
	// and, because it holds the sole connection, take every other request
	// with it. Keep this call above drainExportTxnRows.
	baseCurrency := h.getBaseCurrency(r.Context())

	txns, err := h.drainExportTxnRows(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query transactions")
		return
	}

	// Every database read is done; the workbook is built with no cursor open
	// and no query left to issue.
	//
	// The workbook is assembled through excelize's StreamWriter, not
	// SetCellValue. In normal mode excelize keeps the whole sheet as a live cell
	// tree (an xlsxC per cell, plus a shared-string table) until WriteTo. The
	// stream writer instead serialises each row into a buffer that spills to a
	// temp file past 16 MiB and is copied into the zip without ever being fully
	// resident.
	//
	// Measured end to end through this handler at MaxExportRows rows, as peak
	// HeapAlloc above a pre-run baseline, across GOMAXPROCS 1/2/4/unset:
	//
	//	                       SetCellValue     StreamWriter
	//	one export             235-270 MiB       75-95 MiB
	//	two members at once    448-476 MiB      134-175 MiB
	//
	// Export has no admin gate and no rate limit, so the two-member row is an
	// ordinary Tuesday, not an attack — and against GOMEMLIMIT=768MiB the old
	// figure was around 60% of the soft limit for two ordinary requests.
	//
	// What remains is roughly half the drained []exportTxnRow slice (38 MiB) and
	// half excelize's spill buffer. TestExportPeakMemory_StaysWithinBudget is the
	// guard; TestMeasureExportMemory reports the halves separately so a future
	// change can tell which one moved.
	var truncations exportTruncations

	f := excelize.NewFile()
	defer f.Close()

	sheet := "Transactions"
	f.SetSheetName("Sheet1", sheet)

	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := sw.SetRow(cellAt(1, 1), exportTxnHeaders(baseCurrency)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := writeExportTxnRows(sw, sheet, 2, txns, &truncations); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := sw.Flush(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := writeExportWarningsSheet(f, &truncations); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}

	filename := fmt.Sprintf("spendrop-transactions-%s.xlsx", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	setExportTruncationHeader(w, &truncations)

	if _, err := f.WriteTo(w); err != nil {
		// Headers already sent, can only log
		return
	}
}

// handleExportMonthly exports a monthly report as an Excel file with two
// sheets: Summary (category totals) and Transactions (all entries for that
// month).
func (h *Handler) handleExportMonthly(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	yearStr := chi.URLParam(r, "year")
	monthStr := chi.URLParam(r, "month")

	year, err := strconv.Atoi(yearStr)
	if err != nil || year < MinDataYear || year > MaxDataYear {
		writeError(w, http.StatusBadRequest, "invalid year")
		return
	}
	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		writeError(w, http.StatusBadRequest, "invalid month")
		return
	}

	dateFrom := fmt.Sprintf("%04d-%02d-01", year, month)
	dateTo := time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	ctx := r.Context()

	// Soft-delete filter placement is defensive: `t.deleted_at IS NULL` lives
	// in the LEFT JOIN ON clause so the JOIN shape is preserved. The HAVING
	// clause below still hides zero-total rows from the final output, so in
	// steady state the ON vs WHERE distinction is not observable - but if the
	// HAVING were ever relaxed (e.g. to show empty categories in the export),
	// a WHERE-placed filter would silently collapse the LEFT JOIN to inner-
	// join semantics and drop any category whose only rows were tombstoned.
	// Keeping the predicate in ON means that change stays a one-line tweak
	// instead of a silent correctness regression.
	//
	// Phase 3.1a: sums t.amount_cents (int64) instead of t.amount (float64)
	// and scans total_cents into int64, converting to float at the Excel
	// cell boundary via centsToDollars.
	summaryQuery := `SELECT c.name, c.type, COALESCE(SUM(t.amount_cents), 0) AS total_cents
		FROM categories c
		LEFT JOIN transactions t ON t.category_id = c.id AND t.deleted_at IS NULL AND date(t.date) >= ? AND date(t.date) <= ?
		GROUP BY c.id
		HAVING total_cents > 0
		ORDER BY c.type, total_cents DESC`

	// Phase 3.1a: same cents->float conversion as handleExportTransactions.
	txnQuery := `SELECT t.date, t.description, c.name, c.type, t.amount_cents,
		t.original_amount_cents, t.original_currency, t.tags, t.notes
		FROM transactions t
		JOIN categories c ON t.category_id = c.id
		WHERE t.deleted_at IS NULL AND date(t.date) >= ? AND date(t.date) <= ?
		ORDER BY t.date DESC, t.id DESC LIMIT ?`

	// Every read first, workbook second. Each drain releases the cursor before
	// the next query is issued, which under the production single-connection
	// pool is the difference between a working handler and one that waits
	// forever on a connection only it can release. Building the workbook after
	// the last read means no cell write can ever sit between a cursor and a
	// query.
	summary, err := h.drainCategoryTotals(ctx, summaryQuery, dateFrom, dateTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query category summary")
		return
	}
	baseCurrency := h.getBaseCurrency(ctx)
	txns, err := h.drainExportTxnRows(ctx, txnQuery, dateFrom, dateTo, MaxExportRows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query transactions")
		return
	}

	// StreamWriter rather than SetCellValue, for the memory reason documented in
	// handleExportTransactions: the Transactions sheet here is bounded by the
	// same MaxExportRows.
	var truncations exportTruncations

	f := excelize.NewFile()
	defer f.Close()

	// --- Sheet 1: Summary (category totals) ---
	summarySheet := "Summary"
	f.SetSheetName("Sheet1", summarySheet)

	summarySW, err := f.NewStreamWriter(summarySheet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := summarySW.SetRow(cellAt(1, 1), categoryTotalsHeaders()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := writeCategoryTotals(summarySW, summarySheet, 2, summary, &truncations); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := summarySW.Flush(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}

	// --- Sheet 2: Transactions ---
	txnSheet := "Transactions"
	if _, err := f.NewSheet(txnSheet); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	txnSW, err := f.NewStreamWriter(txnSheet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := txnSW.SetRow(cellAt(1, 1), exportTxnHeaders(baseCurrency)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := writeExportTxnRows(txnSW, txnSheet, 2, txns, &truncations); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := txnSW.Flush(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}

	if err := writeExportWarningsSheet(f, &truncations); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}

	filename := fmt.Sprintf("spendrop-%04d-%02d.xlsx", year, month)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	setExportTruncationHeader(w, &truncations)

	if _, err := f.WriteTo(w); err != nil {
		return
	}
}

// handleExportYearly exports a yearly report as an Excel file with two
// sheets: Monthly Totals (12 months with expenses/income/net) and Category
// Totals.
func (h *Handler) handleExportYearly(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	yearStr := chi.URLParam(r, "year")
	year, err := strconv.Atoi(yearStr)
	if err != nil || year < MinDataYear || year > MaxDataYear {
		writeError(w, http.StatusBadRequest, "invalid year")
		return
	}

	dateFrom := fmt.Sprintf("%04d-01-01", year)
	dateTo := fmt.Sprintf("%04d-12-31", year)
	ctx := r.Context()

	// Phase 3.1a: SUM t.amount_cents (int64) so the per-month totals stay
	// exact end-to-end. Convert to float dollars once per row at the Excel
	// cell boundary below.
	monthlyQuery := `SELECT
		CAST(strftime('%m', t.date) AS INTEGER) AS month_num,
		COALESCE(SUM(CASE WHEN c.type = 'expense' THEN t.amount_cents ELSE 0 END), 0) AS expenses_cents,
		COALESCE(SUM(CASE WHEN c.type = 'income' THEN t.amount_cents ELSE 0 END), 0) AS income_cents
		FROM transactions t
		JOIN categories c ON t.category_id = c.id
		WHERE t.deleted_at IS NULL AND date(t.date) >= ? AND date(t.date) <= ?
		GROUP BY month_num
		ORDER BY month_num`

	// Soft-delete filter placement is defensive: `t.deleted_at IS NULL` lives
	// in the LEFT JOIN ON clause so the JOIN shape is preserved. The HAVING
	// clause below still hides zero-total rows from the final output, so in
	// steady state the ON vs WHERE distinction is not observable — but if the
	// HAVING were ever relaxed (e.g. to show empty categories in the export),
	// a WHERE-placed filter would silently collapse the LEFT JOIN to inner-
	// join semantics and drop any category whose only rows were tombstoned.
	// Keeping the predicate in ON means that change stays a one-line tweak
	// instead of a silent correctness regression.
	// Phase 3.1a: sums t.amount_cents (int64) and exposes total_cents; the
	// Go side scans int64 and converts at the Excel cell boundary.
	catQuery := `SELECT c.name, c.type, COALESCE(SUM(t.amount_cents), 0) AS total_cents
		FROM categories c
		LEFT JOIN transactions t ON t.category_id = c.id AND t.deleted_at IS NULL AND date(t.date) >= ? AND date(t.date) <= ?
		GROUP BY c.id
		HAVING total_cents > 0
		ORDER BY c.type, total_cents DESC`

	// Every read first, workbook second — same ordering rule as the monthly
	// export. All 12 months are pre-filled with zero and then overwritten from
	// the result set. Phase 3.1a: per-month totals stay int64 cents; the
	// conversion to float dollars happens once at the Excel cell boundary.
	months, err := h.drainMonthlyTotals(ctx, monthlyQuery, dateFrom, dateTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query monthly totals")
		return
	}
	catTotals, err := h.drainCategoryTotals(ctx, catQuery, dateFrom, dateTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query category totals")
		return
	}

	var truncations exportTruncations

	f := excelize.NewFile()
	defer f.Close()

	// --- Sheet 1: Monthly Totals ---
	monthlySheet := "Monthly Totals"
	f.SetSheetName("Sheet1", monthlySheet)

	monthlySW, err := f.NewStreamWriter(monthlySheet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := monthlySW.SetRow(cellAt(1, 1), []any{"Month", "Expenses", "Income", "Net"}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	monthNames := []string{"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December"}
	for i, m := range months {
		// Phase 3.1a: compute net in cents (int64) so the subtraction is
		// exact, then convert the three money fields to float dollars once.
		netCents := m.incomeCents - m.expensesCents
		if err := monthlySW.SetRow(cellAt(1, i+2), []any{
			monthNames[i],
			centsToDollars(m.expensesCents),
			centsToDollars(m.incomeCents),
			centsToDollars(netCents),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build export")
			return
		}
	}
	if err := monthlySW.Flush(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}

	// --- Sheet 2: Category Totals ---
	catSheet := "Category Totals"
	if _, err := f.NewSheet(catSheet); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	catSW, err := f.NewStreamWriter(catSheet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := catSW.SetRow(cellAt(1, 1), categoryTotalsHeaders()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := writeCategoryTotals(catSW, catSheet, 2, catTotals, &truncations); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}
	if err := catSW.Flush(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}

	if err := writeExportWarningsSheet(f, &truncations); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build export")
		return
	}

	filename := fmt.Sprintf("spendrop-%04d.xlsx", year)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	setExportTruncationHeader(w, &truncations)

	if _, err := f.WriteTo(w); err != nil {
		return
	}
}

// cellAt converts 1-based column and row indices to an Excel cell reference.
func cellAt(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}
