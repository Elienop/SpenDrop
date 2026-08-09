package api

import "unicode/utf8"

// Numeric business-logic limits that are invariants of the app, not
// operator-tunable knobs. Knobs that an operator might reasonably want
// to adjust at runtime (body caps, rate limits, password bounds, session
// TTLs, etc.) live in internal/config and are read through runtime.go.
//
// These values, by contrast, are part of the product's data-model
// contract — the SQL schema, the API wire format, and the UI all assume
// the same caps, and changing one without the other is a bug. Centralising
// them makes that contract explicit.

// Year bounds. There are two windows because there are two jobs, and
// conflating them is what produced the hole this pair replaces: a single
// [2000, 2100] constant guarded every year PARAM while nothing at all
// guarded a transaction DATE, so the app could store rows in years no
// report would accept.
//
// MinDataYear / MaxDataYear bound the window a transaction date may
// occupy. Everything that reads ledger data by year — reports, dashboard,
// export, and validateDate on the write side — uses this pair, so every
// year the ledger can hold is a year the reports can display.
//
// PlanningMinYear / PlanningMaxYear bound the year params of budgets and
// savings goals. Those rows are configuration the household writes forward,
// not history it imports backward: nothing plans a 1990 budget, and the
// Budgets page's year Select bottoms out at MIN_YEAR (web/src/lib/dates.ts).
// Widening them would add a century of empty years to that Select for no
// gain.
//
// NEVER lower MinDataYear below 1000. Two verified hazards live under four
// digits:
//
//   - generateYearDates and generateMonthDates in
//     web/src/components/reports/heatmapGrid.ts build
//     `new Date(Date.UTC(year, 0, 1))` and `new Date(Date.UTC(year, month, 1))`
//     respectively, and JS maps years 0-99 onto 1900-1999 — a year-84 row
//     would silently render as 1984. The YEAR argument is the hazard in both;
//     they differ only in the month.
//     (Named by FUNCTION, not by line: this used to cite two line numbers in
//     SpendingHeatmap.tsx, which the heatmap rebuild moved into a new file
//     with nothing in either toolchain to notice.)
//   - The report handlers build ranges with fmt.Sprintf("%d-01-01", year),
//     which emits "5-01-01" for year 5 and breaks the lexical string
//     comparison SumByMonthRange does against the stored dates.
//
// 1900 specifically because parseImportDate has accepted [1900, 2100] since
// it was written (minImportYear / maxImportYear in import_handlers.go, now
// aliases of these). One product-wide window, not two that disagree.
const (
	MinDataYear = 1900
	MaxDataYear = 2100

	PlanningMinYear = 2000
	PlanningMaxYear = 2100
)

// Pagination defaults and caps for list endpoints.
const (
	DefaultPerPage = 25
	MaxPerPage     = 100
	// MaxPage is the upper bound on the page number to keep pathological
	// `?page=999999999` requests from producing a huge OFFSET scan.
	MaxPage = 1_000_000
)

// Batch-operation caps. These bound how many items a single request can
// mutate in one database transaction.
const (
	MaxBatchTransactions = 500
	MaxBatchDeleteIDs    = 500
	// MaxBatchRestoreIDs is pinned to MaxBatchDeleteIDs so "undo the
	// last batch delete" is always possible in a single request — an
	// operator who just nuked 500 rows by accident cannot have the
	// restore cap be smaller than the delete cap or the undo becomes
	// a multi-step dance. Declared as a named alias rather than a
	// literal reference at the call site so the symmetry is visible
	// in code review and a future split (if the two ever diverge) is
	// a one-line change here.
	MaxBatchRestoreIDs = MaxBatchDeleteIDs
	// MaxBatchUpdateIDs caps the size of the ID list accepted by
	// /api/transactions/batch-update. Pinned to MaxBatchDeleteIDs because
	// every bulk mutation should share the same per-request blast radius.
	MaxBatchUpdateIDs      = MaxBatchDeleteIDs
	MaxCategoryReorder     = 200
	MaxImportRows          = 10000
	MaxSavedFilters        = 50
	MaxMultiCategoryFilter = 100
)

// Bounds on the SHAPE of an uploaded workbook, as opposed to MaxImportRows
// above which bounds the rows we successfully parse. They exist because
// MAX_UPLOAD_BYTES (10 MiB) bounds the COMPRESSED zip and nothing downstream
// of it bounds what that zip expands into. A workbook declares its own
// geometry — entry sizes in the zip directory, row and cell indices in XML
// attributes — so a few kilobytes of input can direct the parser to do an
// unbounded amount of work. None of this can live in MaxImportRows, which is
// only consulted after the whole sheet has already been materialised.
//
// The caps are ordered by when the bytes are touched, and each one has to hold
// on its own because the earlier ones run before the later ones exist:
//
//	MaxImportUnzippedBytes      at the zip directory, before decompression
//	MaxImportUnzippedPartBytes  per XML part, during decompression
//	MaxImportSheetRows/Cells    while walking the parsed sheet
//
// Every number below was measured against excelize v2.11.0, not estimated.
//
//   - MaxImportUnzippedBytes stops a zip bomb. excelize decompresses the
//     WHOLE archive up front, and its UnzipSizeLimit option defaults to
//     16 GB, so a bomb in any entry — theme, styles, shared strings, an entry
//     we never read at all — inflates before a single row is examined. A
//     2.00 MiB upload drove +10,244 MiB of allocation and took 15.4s inside
//     OpenReader. Summing the zip directory's declared sizes is a sound way
//     to catch this: Go's archive/zip refuses to hand back more bytes than an
//     entry declared (reader.go, checksumReader.Read), so an entry cannot lie
//     small and then inflate.
//
//   - MaxImportUnzippedPartBytes is a staging threshold, NOT a rejection.
//     excelize spills a worksheet or the shared-string table to a temp file
//     once it exceeds this, which keeps peak in-memory well under the archive
//     cap for genuinely large sheets. It is set below MaxImportUnzippedBytes
//     deliberately; raising it to match would keep everything in memory.
//
//   - MaxImportSheetRows stops a row-index spin. Rows.Next() returns true
//     without reading a token whenever the current row index is still ahead of
//     the seek position, so a row claiming r="9223372036854775807" makes the
//     iterator report ~9.2e18 consecutive rows: a 5.9 KB upload that was still
//     burning a core when killed at 20s. Smaller indices are not merely slower
//     but bounded — they also allocate, because GetRows pads the blank run: at
//     r=2147483647 the same shape spins 5.6s AND allocates 48 GiB (a
//     2,147,483,647-length slice at 24 bytes per header), measured. excelize
//     v2.11.0 added a TotalRows guard, but only inside Rows.Next()'s
//     token-scanning loop, which this path skips — Rows.Columns() reads ahead
//     and assigns the huge index itself. The library's guard therefore fires
//     only when the hostile row is FIRST in the sheet; one ordinary row in
//     front of it is enough to bypass it, which is why ours is positional.
//
//   - MaxImportSheetCells stops a column-padding amplification that no
//     excelize version bounds. A value in column XFD makes the parser pad that
//     row to all 16384 entries; 5,000 such rows compress to 31 KB and
//     allocated 5.68 GiB transient / ~1.4 GB retained, roughly 188,000x.
//     Counting total cells rather than per-row width bounds the wide-row and
//     the many-rows axes with one budget.
//
// On the values, and on what "headroom" honestly means for each:
//
//   - 1,048,576 rows is EXACTLY Excel's own maximum row number, chosen so that
//     no file Excel can write is ever refused. This matters more than it
//     sounds: the cap counts row SLOTS, and a <row> element costs a slot even
//     when it holds nothing, so leftover row formatting far below the data
//     trips a lower cap. A 6 KB workbook with a 3-column header, one data row
//     and SetRowHeight(sheet, 150000) scans 150,000 slots — GetRows parses it
//     fine, and a 100,000 cap refused it. Stray formatting below the data is
//     the single most common real-world spreadsheet artifact there is, so the
//     only false-rejection-free ceiling is Excel's own. Scanning a full
//     1,048,576 slots costs about 3 ms and 25 MB of blank-run padding.
//
//   - 4,000,000 cells is 4x a 10,000-row by 100-column sheet, which is already
//     an unusually wide ledger; a realistic one is 10,000 by 30. But headroom
//     is the wrong mental model here, because padding counts: a file with a
//     value in a far-right column on more than ~244 rows (4,000,000 / 16,384)
//     trips this no matter how few values those rows actually hold. That is
//     the intended behaviour — padding is what allocates — and it is why the
//     rejection message talks about padding rather than about content.
//
//   - 128 MiB of decompressed archive is sized against the largest LEGITIMATE
//     workbook the importer can accept, measured rather than reasoned. See the
//     table below for what that workbook actually costs.
//
// THE MULTIPLIERS DEPEND ON THE SCRIPT, and stating them against ASCII was how
// this table went wrong. The field caps are CHARACTER counts (charLen, below)
// and these are BYTE bounds, so the same maximal workbook costs twice as much
// in Arabic and four times as much at UTF-8's worst case. Two workbooks, both
// MaxImportRows rows, measured against excelize v2.11.0. The maximal Arabic row
// is the one TestImportShapeCaps_AdmitTheMaximalLedger rebuilds and re-measures
// on every run; the other three were measured the same way but are recorded
// here rather than asserted, because it is the maximal one that binds:
//
//	                                unzipped   largest part   cell text
//	maximal, ASCII                   31.31        29.18         28.86 MiB
//	maximal, Arabic (2 B/char)       59.72        57.59         57.27 MiB
//	realistic full-size, English      3.19         2.08          1.07 MiB
//	realistic full-size, Arabic       3.59         2.08          1.55 MiB
//
// "Maximal" means every one of 10,000 rows carrying a 500-character
// description, 500 characters of tags AND a 2,000-character note, all distinct
// so nothing dedupes into the shared-string table. "Realistic" is the same row
// count filled with ordinary household entries.
//
// Against the maximal Arabic workbook the caps are 2.14x / 1.11x / 1.12x —
// slim, and nothing like the 4x and 2x this comment used to claim. Against a
// realistic full-size Arabic ledger they are 35.6x / 30.7x / 41.3x. The second
// set is the one that decides false rejection, because the first workbook is
// not a file anyone produces.
//
// THE CAPS ARE DELIBERATELY NOT RAISED to restore a round multiplier on the
// maximal construct, and the reason is measured too. Doubling them
// (MaxImportPartBytes 128 MiB, MaxImportUnzippedBytes 256 MiB,
// MaxImportSheetBytes 128 MiB) admits the same construct written at UTF-8's
// 4-byte worst case: 116.54 MiB unzipped, 114.41 MiB in one part, and an upload
// that peaks at 529 MiB of heap. The container runs with GOMEMLIMIT=768MiB
// under mem_limit: 1g (both compose files), and export is budgeted at 150 MiB
// on top of that (exportPeakBudgetBytes) with no cap on how many members run
// one at once. Two concurrent imports at 529 MiB each exceed the container
// limit outright, and one beside a couple of exports is already past
// GOMEMLIMIT. The headroom would be bought in the only currency this
// deployment cannot spend, to protect a file that does not exist.
//
// At the caps as they stand the same upload peaks at 293 MiB, which the budget
// absorbs.
//
// Where the real edge sits, also measured: the maximal Arabic workbook plus six
// extra 50-character columns is 63.71 MiB in its largest part and still
// admitted; ten such columns, or six of 100 characters, is refused by
// MaxImportPartBytes. So the cliff is roughly 500 extra characters per row past
// a workbook that is already every field at every cap.
//
// If these caps are ever re-derived, derive them in characters times UTF-8's
// worst case, not in the bytes an ASCII fixture happened to produce — and
// re-measure the upload peak in the same change, because that is the constraint
// that actually binds.
//
// A file that trips any of these gets a 400 naming the limit, never a silent
// truncation — dropping rows from a ledger import quietly is worse than
// refusing the file.
//
// GEOMETRY IS NOT BYTES, which is the trap the row and cell caps fell into on
// their own. excelize rebuilds a shared string on every cell that references
// it (xlsxSI.String, cell.go), so one <si> of length L referenced by N cells
// yields N separate L-byte allocations, all retained in the returned rows.
// Measured against this handler's own reader: a 15,774-byte upload — 1.17 MB
// unzipped, 1,000 rows, 3,000 cells, under 0.1% of every geometry cap above —
// retained 3,000 MiB, and the retained figure tracked the summed cell text
// exactly. It also outlives the request, because the parsed rows are held in
// the in-memory import session.
//
// So two further bounds cover the quantity that actually allocates:
//
//   - MaxImportCellBytes bounds one cell. It is a BYTE bound compared against
//     len(), not a character count, and the name says so on purpose: the
//     household writes Arabic alongside English, and a character-flavoured
//     name invites the false claim that a 32,767-character cell always fits.
//     The honest justification is in bytes — the longest field the product
//     accepts is MaxNotesLength (2,000 characters), which is at most 8,000
//     bytes even at UTF-8's 4-byte worst case, so this leaves ~33x headroom
//     for any script.
//
//   - MaxImportSheetBytes bounds the total, because a per-cell cap alone does
//     not: four million cells just under the per-cell limit is still 1,048 GB.
//     64 MiB is 1.12x the cell text of the maximal Arabic workbook in the table
//     above (57.27 MiB) and 41x a realistic full-size Arabic ledger (1.55 MiB).
//
// TOTALS ARE NOT PEAKS, and this is the trap those two fell into. Every cap
// above is evaluated on the slice Rows.Columns() returns, so each can only
// observe an allocation that has already happened. One Columns() call
// materialises an entire row, and cells-per-row is not bounded by excelize at
// all: rowXMLHandler consults CellNameToCoordinates — the thing that enforces
// MaxColumns — only when the cell carries an `r` attribute, and just does
// cellCol++ when it does not. Measured end to end through the upload handler:
// an 8,003-byte upload peaked at 1,258 MiB and a 50,444-byte one at 516 MiB,
// both correctly answered 400 by the totals above, long after the damage.
//
// MaxImportRowBytes is what makes the peak statable, and prescanImportWorkbook
// enforces it BEFORE excelize materialises anything, because no cap checked
// after Columns() can be:
//
//	prescan, largest part admitted (2 x MaxImportPartBytes)   114 MiB measured
//	parse, worst row every cap admits                          35 MiB measured
//	parse, worst sheet every cap admits                       118 MiB measured
//
// Those three are adversarial: the worst input each cap admits. The LEGITIMATE
// worst case is separate and larger, because a real file exercises every cap at
// once rather than one at a time — the maximal Arabic workbook in the table
// above takes 67 MiB through the prescan and 293 MiB through the whole upload
// handler. That 293 MiB, not any of the three above, is what sits against
// GOMEMLIMIT=768MiB, and it is why these caps are not raised.
//
// EVERY FIGURE HERE IS MEASURED END TO END, and the multipliers are why. An
// earlier version of this table derived them instead, stating the prescan term
// as "one text node read whole <= 128 MiB" and concluding a request stayed
// inside MaxImportUnzippedBytes. Both were wrong: encoding/xml grows its buffer
// by DOUBLING and then hands back a CharData copy, so reading a text node of
// size P costs about 2P, and a 100 MiB <si> in a 107,818-byte upload peaked at
// 227 MiB against a stated 128. Likewise the accumulated term, derived as
// MaxImportSheetBytes, measures 118 MiB rather than 64. This is the fourth time
// in this work that a derived number in a comment turned out to be the thing
// justifying a placement, so these are measurements, not arithmetic.
//
// MaxImportPartBytes is what makes the prescan term statable rather than merely
// described: without it the largest text node is bounded only by the whole
// archive, so the doubling cost would be ~256 MiB. Bounding the largest PART at
// 64 MiB puts the measured peak at 114 MiB, inside MaxImportUnzippedBytes.
// Bounding only the archive TOTAL cannot achieve this, because one part may be
// the entire total.
//
// THE BOUND IS THE ROW'S SUM, NOT A PRODUCT OF TWO CAPS, and that choice is
// what keeps real files importable. Capping cells-per-row and bytes-per-cell
// and calling the product the peak was the first design, and it forces both
// caps low: 1,024 columns at 32 KiB per cell. Real spreadsheet applications
// exceed both — Google Sheets allows 18,278 columns and 50,000 characters per
// cell — and this household imports Google Sheets exports regularly, so those
// numbers would have refused a working workflow to buy a bound that summing
// gives for free. The two per-item limits below are therefore set above
// anything the FORMAT can express, and the row sum does the bounding:
//
//   - 100,000 cells in a row is ~6x the 16,384 columns SpreadsheetML allows.
//     No conforming export can reach it — a cell past column XFD cannot even
//     be named — so it can only be tripped by cells written without an `r`
//     attribute, which is the unbounded case it exists for. It bounds
//     blank-run padding, one string header per skipped column, not text.
//
//   - 256 KiB in a cell is ~2x the format's own 32,767-character cell maximum
//     at UTF-8's 4-byte worst case (131,068 bytes), so no conforming cell can
//     trip it either. It catches a single absurd string early and bounds what
//     one cell can carry into the ledger.
//
// Both are stated against the FORMAT's limits rather than any application's,
// because that is the claim that can be checked: Google Sheets' own grid is
// wider and its cells longer than Excel's, but neither survives export.
//
// Both are far looser than the previous values ON PURPOSE. A false rejection
// costs the household a workflow it uses; the residual it buys back requires a
// deliberately-crafted file AND admin credentials.
const (
	MaxImportSheetRows   = 1_048_576
	MaxImportSheetCells  = 4_000_000
	MaxImportCellsPerRow = 100_000

	MaxImportUnzippedBytes     = 128 << 20
	MaxImportPartBytes         = 64 << 20
	MaxImportUnzippedPartBytes = 16 << 20

	MaxImportSheetBytes = 64 << 20
	MaxImportRowBytes   = 32 << 20
	MaxImportCellBytes  = 256 << 10
)

// MaxTransactionAmount is the largest positive value accepted for a
// transaction (or budget). Anything above this is almost certainly a
// currency-entry mistake (e.g. pasting an entire account balance into
// the amount field) and would overflow some downstream chart axes.
const MaxTransactionAmount = 1_000_000_000

// charLen reports the length of s in CHARACTERS (Unicode code points), which
// is the unit every cap in the block below is stated in, and the unit every
// error message about them names.
//
// It exists because the obvious spelling — len(s) — counts BYTES, and every one
// of these caps was compared with len() while telling the user "characters".
// UTF-8 spends one byte on ASCII but two on Arabic, two on an accented Latin
// letter and four on an emoji, so a cap advertised as 500 characters refused an
// Arabic description at about 250. This household writes Arabic alongside
// English, so the field rejected text well inside its stated limit and then
// explained the refusal with a number that was not the one being applied.
//
// Two other counts in this system agree with this one, which is what makes
// characters the correct unit here rather than merely a friendlier one:
//
//   - SQLite's length() counts CHARACTERS on a TEXT value (it counts bytes only
//     on a BLOB), so the CHECK(length(name) BETWEEN 1 AND 100) that migration
//     011 puts on api_tokens.name is already a character constraint. Counting
//     bytes in Go made the handler stricter than the app's own schema for any
//     non-ASCII name — the schema would have taken 100 Arabic characters, the
//     handler stopped at 50.
//   - The frontend counts code points through charCount() in
//     web/src/lib/text.ts. JavaScript's String.length would NOT agree: it
//     counts UTF-16 code units, so an emoji outside the Basic Multilingual
//     Plane is 2 there and 1 here. Arabic and accented Latin happen to agree in
//     UTF-16, which is exactly why a naive .length check on the frontend looks
//     correct in testing and diverges on the first emoji.
//
// Not every cap in this file belongs in this unit, and the ones that do not say
// so where they are enforced: passwords stay byte-bounded because bcrypt
// truncates its input at 72 bytes, MaxFilterJSONLength bounds a serialized
// transport payload, and the import SHAPE caps above bound allocation.
//
// DO NOT MERGE THIS WITH utf16Len / truncateUTF16 in export_handlers.go. They
// have the same shape and look like duplicates, and they are not: this one
// counts CODE POINTS because that is the unit the caps below are stated in and
// the unit SQLite's length() and the frontend's charCount() agree on. Those
// count UTF-16 CODE UNITS because that is the unit the xlsx cell limit is
// defined in, and they match excelize's own countUTF16String so a value this
// package accepts is one excelize will not silently shorten. The two agree on
// Arabic and on accented Latin and diverge on any emoji outside the Basic
// Multilingual Plane, so collapsing them would look correct in testing and
// break one of the two on the first astral character.
func charLen(s string) int { return utf8.RuneCountInString(s) }

// String length caps for user-supplied text fields, in CHARACTERS — compare
// with charLen, never len. These match the SQLite column definitions in
// migrations/001 and the Zod schemas on the frontend (web/src/lib/constants.ts,
// which validates with charCount for the same reason).
//
// MaxFilterJSONLength is the one exception in this block: it bounds a
// serialized JSON payload rather than prose a human typed, so bytes is the
// honest unit and its message says bytes.
const (
	MaxDescriptionLength    = 500
	MaxTagsLength           = 500
	MaxNotesLength          = 2000
	MaxDisplayNameLength    = 64
	MinUsernameLength       = 3
	MaxUsernameLength       = 32
	MaxCategoryNameLength   = 100
	MaxIconNameLength       = 100
	MaxCurrencyNameLength   = 100
	MaxCurrencySymbolLength = 10
	MaxFilterNameLength     = 100
	MaxFilterJSONLength     = 10000
)

// Bounds on the client-supplied idempotency key for POST /api/transactions.
// Clients mint crypto.randomUUID(), which is 36 characters; 64 leaves room for
// a prefixed or otherwise-structured key from the API-token surface without
// letting an unbounded string reach an indexed column.
//
// The 16-character floor exists to keep degenerate values out of the key
// namespace. A key is a claim, and a short one is a claim somebody else's
// script will make too: "0", "1", "undefined" and "null" are what a caller
// emits when its key generator silently failed, and every submission after
// that would replay against the first one and be discarded as a retry. The
// floor makes an accidental key too short to be accepted at all, so the caller
// gets a 400 instead of a ledger that quietly stops recording. It cannot break
// a real client — a UUID is 36 characters and any deliberate scheme clears 16.
//
// The bounds are in bytes, which equals characters here because the charset
// (see clientKeyPattern) is ASCII-only.
const (
	MinClientKeyLength = 16
	MaxClientKeyLength = 64
)

// Query result caps for read endpoints that don't expose pagination.
const (
	DefaultTopMerchantsLimit   = 10
	MaxTopMerchantsLimit       = 50
	DescriptionSuggestionLimit = 500
	TagSuggestionLimit         = 1000
)

// MaxExportRows is the upper bound on rows included in a single spreadsheet
// export.
//
// IT NO LONGER BOUNDS THE SHEET BUFFER. The export handlers assemble every
// sheet through excelize's StreamWriter, which serialises each row and spills
// past a chunk threshold instead of holding a live cell tree until WriteTo, so
// the workbook is not the term that grows with this constant.
//
// What it bounds now is the DRAIN. handleExportTransactions and
// handleExportMonthly consume the cursor fully into a []exportTxnRow before
// writing anything, because production pins the pool to one connection and
// building while the cursor is open would block every other request — see the
// type comment on exportTxnRow. That slice stays resident for the whole build,
// so raising this constant raises the peak linearly in rows. The per-row term
// is NOT capped alongside it: the API bounds description/tags/notes, but the
// importer wrote them unvalidated until recently, so a legacy ledger can hold
// longer values and the drain is O(total text).
//
// TestExportPeakMemory_StaysWithinBudget (export_memory_measure_test.go) is
// what enforces the outcome — it runs a full MaxExportRows export through the
// handler and fails past exportPeakBudgetBytes. Anyone raising this value
// should expect that test to be what objects, and should re-measure rather than
// reason. The figures are deliberately not copied here: they live next to the
// measurement that produced them, which is the only place they can be kept
// honest.
const MaxExportRows = 50_000

// MaxSubscriptionsPerUser caps how many browser push subscriptions one user
// may register. A user with phone + laptop + tablet needs a handful; 20 is
// generous headroom while still bounding a misbehaving client that re-POSTs a
// fresh endpoint on every page load.
const MaxSubscriptionsPerUser = 20

// Month-window caps for trend endpoints. Both income/expense trends and the
// dashboard trend resolve their whole window from ONE SumByMonthRange query
// and then walk it in memory, so the cost is linear in the response size, not
// in queries. Category trends are capped far lower because that query fans out
// per category.
//
// MaxTrendMonths is DERIVED, not chosen. The Savings tab sizes its window from
// the year the user picked (web/src/components/reports/utils.ts,
// MAX_REPORT_MONTHS, which must match this value) and the year picker is
// driven by the ledger, so the widest window the UI can legitimately ask for
// is January of the oldest year the ledger may hold through December of the
// newest — i.e. the whole DATA window. Past MaxDataYear every year-param
// endpoint 400s, so nothing wider is reachable.
//
// There is now ZERO slack: at [1900, 2100] this is exactly the widest
// reachable window, not a value comfortably above it. It used to be sized
// against the narrower planning window and could be described as "far larger
// than any window yearOptions can ask for" — that is no longer true, and any
// future widening of the data window MUST come here (and to the frontend
// literal) in the same change or the oldest years start truncating silently.
//
// Two earlier values were literals sized against a then-hard-coded frontend
// floor of 2024, and each one was already scheduled to start silently
// truncating the oldest selectable years: 120 would have bound from 2034, and
// 600 from 2050. Deriving the constant removes that whole class of expiry —
// the only way to shrink this window now is to narrow the data bounds, which
// is exactly when it should shrink.
const (
	MaxTrendMonths         = (MaxDataYear - MinDataYear + 1) * 12
	MaxCategoryTrendMonths = 60
)
