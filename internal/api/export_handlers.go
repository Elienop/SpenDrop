package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
		conditions = append(conditions, "t.description LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escaped+"%")
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
// The slice is bounded by MaxExportRows, so the memory ceiling is fixed.
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
// cursor that is only released when the handler returns. Iterating to
// exhaustion happens to auto-close the cursor, which is what kept the previous
// inline loops working, but that made the safety incidental: one `break` or one
// early `return` added to the loop deadlocked the whole server. Returning a
// slice moves the release into a deferred Close that no control flow can skip.
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

// writeCategoryTotals renders drained category totals onto a sheet starting at
// startRow. The column layout matches the Category/Type/Total headers written
// by the monthly and yearly export handlers.
func writeCategoryTotals(f *excelize.File, sheet string, startRow int, totals []categoryTotalRow) {
	row := startRow
	for _, c := range totals {
		f.SetCellValue(sheet, cellAt(1, row), c.name)
		f.SetCellValue(sheet, cellAt(2, row), c.catType)
		f.SetCellValue(sheet, cellAt(3, row), centsToDollars(c.cents))
		row++
	}
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

// writeExportTxnRows renders drained rows onto a sheet starting at startRow.
// The column layout matches the headers written by each export handler.
func writeExportTxnRows(f *excelize.File, sheet string, startRow int, txns []exportTxnRow) {
	row := startRow
	for _, t := range txns {
		f.SetCellValue(sheet, cellAt(1, row), t.date.Format("2006-01-02"))
		f.SetCellValue(sheet, cellAt(2, row), t.desc)
		f.SetCellValue(sheet, cellAt(3, row), t.catName)
		f.SetCellValue(sheet, cellAt(4, row), t.catType)
		f.SetCellValue(sheet, cellAt(5, row), centsToDollars(t.amountCents))
		if t.origAmtCents.Valid {
			f.SetCellValue(sheet, cellAt(6, row), centsToDollars(t.origAmtCents.Int64))
		}
		if t.origCur.Valid {
			f.SetCellValue(sheet, cellAt(7, row), t.origCur.String)
		}
		if t.tags.Valid {
			f.SetCellValue(sheet, cellAt(8, row), t.tags.String)
		}
		if t.notes.Valid {
			f.SetCellValue(sheet, cellAt(9, row), t.notes.String)
		}
		row++
	}
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

	f := excelize.NewFile()
	defer f.Close()

	sheet := "Transactions"
	f.SetSheetName("Sheet1", sheet)

	headers := []string{"Date", "Description", "Category", "Type", fmt.Sprintf("Amount (%s)", baseCurrency), "Original Amount", "Original Currency", "Tags", "Notes"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}

	writeExportTxnRows(f, sheet, 2, txns)

	filename := fmt.Sprintf("spendrop-transactions-%s.xlsx", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

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
	if err != nil || year < MinYear || year > MaxYear {
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

	f := excelize.NewFile()
	defer f.Close()

	// --- Sheet 1: Summary (category totals) ---
	summarySheet := "Summary"
	f.SetSheetName("Sheet1", summarySheet)

	summaryHeaders := []string{"Category", "Type", "Total"}
	for i, h := range summaryHeaders {
		f.SetCellValue(summarySheet, cellAt(i+1, 1), h)
	}

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

	// Drain before touching the workbook, and before getBaseCurrency below:
	// under the production single-connection pool a still-open summary cursor
	// makes the next query wait on the handler that issued it.
	summary, err := h.drainCategoryTotals(ctx, summaryQuery, dateFrom, dateTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query category summary")
		return
	}
	writeCategoryTotals(f, summarySheet, 2, summary)

	// --- Sheet 2: Transactions ---
	txnSheet := "Transactions"
	f.NewSheet(txnSheet)

	baseCurrency := h.getBaseCurrency(ctx)
	txnHeaders := []string{"Date", "Description", "Category", "Type", fmt.Sprintf("Amount (%s)", baseCurrency), "Original Amount", "Original Currency", "Tags", "Notes"}
	for i, h := range txnHeaders {
		f.SetCellValue(txnSheet, cellAt(i+1, 1), h)
	}

	// Phase 3.1a: same cents->float conversion as handleExportTransactions.
	txnQuery := `SELECT t.date, t.description, c.name, c.type, t.amount_cents,
		t.original_amount_cents, t.original_currency, t.tags, t.notes
		FROM transactions t
		JOIN categories c ON t.category_id = c.id
		WHERE t.deleted_at IS NULL AND date(t.date) >= ? AND date(t.date) <= ?
		ORDER BY t.date DESC, t.id DESC LIMIT ?`

	txns, err := h.drainExportTxnRows(ctx, txnQuery, dateFrom, dateTo, MaxExportRows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query transactions")
		return
	}
	writeExportTxnRows(f, txnSheet, 2, txns)

	filename := fmt.Sprintf("spendrop-%04d-%02d.xlsx", year, month)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

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
	if err != nil || year < MinYear || year > MaxYear {
		writeError(w, http.StatusBadRequest, "invalid year")
		return
	}

	dateFrom := fmt.Sprintf("%04d-01-01", year)
	dateTo := fmt.Sprintf("%04d-12-31", year)
	ctx := r.Context()

	f := excelize.NewFile()
	defer f.Close()

	// --- Sheet 1: Monthly Totals ---
	monthlySheet := "Monthly Totals"
	f.SetSheetName("Sheet1", monthlySheet)

	monthlyHeaders := []string{"Month", "Expenses", "Income", "Net"}
	for i, h := range monthlyHeaders {
		f.SetCellValue(monthlySheet, cellAt(i+1, 1), h)
	}

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

	// Drain before writing any cell and before the category query below: an
	// open cursor holds the pool's only connection in production, so the
	// second query would wait on the handler that issued the first.
	// All 12 months are pre-filled with zero, then overwritten from the result
	// set. Phase 3.1a: per-month totals stay int64 cents; the conversion to
	// float dollars happens once at the Excel cell boundary.
	months, err := h.drainMonthlyTotals(ctx, monthlyQuery, dateFrom, dateTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query monthly totals")
		return
	}

	monthNames := []string{"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December"}
	for i, m := range months {
		row := i + 2
		// Phase 3.1a: compute net in cents (int64) so the subtraction is
		// exact, then convert the three money fields to float dollars once.
		netCents := m.incomeCents - m.expensesCents
		f.SetCellValue(monthlySheet, cellAt(1, row), monthNames[i])
		f.SetCellValue(monthlySheet, cellAt(2, row), centsToDollars(m.expensesCents))
		f.SetCellValue(monthlySheet, cellAt(3, row), centsToDollars(m.incomeCents))
		f.SetCellValue(monthlySheet, cellAt(4, row), centsToDollars(netCents))
	}

	// --- Sheet 2: Category Totals ---
	catSheet := "Category Totals"
	f.NewSheet(catSheet)

	catHeaders := []string{"Category", "Type", "Total"}
	for i, h := range catHeaders {
		f.SetCellValue(catSheet, cellAt(i+1, 1), h)
	}

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

	catTotals, err := h.drainCategoryTotals(ctx, catQuery, dateFrom, dateTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query category totals")
		return
	}
	writeCategoryTotals(f, catSheet, 2, catTotals)

	filename := fmt.Sprintf("spendrop-%04d.xlsx", year)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	if _, err := f.WriteTo(w); err != nil {
		return
	}
}

// cellAt converts 1-based column and row indices to an Excel cell reference.
func cellAt(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}
