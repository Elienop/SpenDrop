package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// --- Export memory measurement harness -------------------------------------
//
// TestExportPeakMemory_StaysWithinBudget runs in the DEFAULT suite. It is the
// only thing enforcing the memory invariant on every change, so it must not be
// behind an env gate — a budget nobody executes is a comment that happens to
// compile.
//
// It measures the real ceiling (MaxExportRows rows) rather than a scaled-down
// proxy, because it turns out to be affordable: seeding 50,000 rows through one
// prepared statement in one transaction plus two exports costs ~1.2 s against a
// package that already takes ~11 s. A smaller N would have worked too, but the
// separation from the un-streamed writer narrows as N falls (1.7x at 10,000
// rows versus 3.2x at 50,000), and a proxy would be testing a proxy.
//
// The two TestMeasureExport* tests below stay gated behind SPENDROP_EXPORT_MEM=1.
// They are reporting tools, not guards: a per-phase breakdown and a two-member
// concurrency case, both of which print numbers a human reads. They stay
// compiled so `go build` / `go vet` keep them honest.
//
//	SPENDROP_EXPORT_MEM=1 go test ./internal/api -run TestMeasureExport -v -count=1
//	SPENDROP_EXPORT_MEM=1 SPENDROP_EXPORT_MEM_ROWS=10000 go test ./internal/api -run TestMeasureExportMemory -v
//
// Peak heap is sampled rather than derived: excelize's cost is dominated by
// live objects that exist only while the workbook is being assembled, so a
// before/after MemStats pair sees nothing. The sampler polls HeapAlloc while
// the handler runs and keeps the maximum.

// discardResponseWriter throws the response body away. Production streams the
// xlsx to a socket; httptest.ResponseRecorder would keep a second full copy of
// it in the heap and pollute the peak figure with a cost the server does not
// actually pay.
type discardResponseWriter struct {
	header http.Header
	code   int
	n      int64
}

func (d *discardResponseWriter) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}

func (d *discardResponseWriter) Write(p []byte) (int, error) {
	d.n += int64(len(p))
	return len(p), nil
}

func (d *discardResponseWriter) WriteHeader(code int) { d.code = code }

// seedBulkTransactions inserts n transactions in a single SQL transaction,
// bypassing the sqlc layer for speed. Descriptions, tags and notes are sized
// to plausible household values so the string cost is realistic rather than
// degenerate.
func seedBulkTransactions(t *testing.T, db *sql.DB, userID, categoryID int64, n int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin bulk seed: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT INTO transactions
		(user_id, category_id, date, amount_cents, description, tags, notes,
		 original_amount_cents, original_currency)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare bulk seed: %v", err)
	}
	defer stmt.Close()

	base := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		date := base.Add(time.Duration(i) * time.Hour)
		desc := "Grocery run at the corner shop #" + strconv.Itoa(i)
		tags := "groceries,household,weekly"
		notes := "Paid by card; receipt filed under " + strconv.Itoa(i%500)
		if _, err := stmt.Exec(userID, categoryID, date, int64(1000+i%90000),
			desc, tags, notes, int64(1500000+i), "LBP"); err != nil {
			t.Fatalf("bulk seed row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit bulk seed: %v", err)
	}
}

// heapSampler polls the heap while a workload runs and records the maximum.
type heapSampler struct {
	stop     chan struct{}
	done     chan struct{}
	peak     atomic.Uint64
	peakSys  atomic.Uint64
	baseline uint64
}

func startHeapSampler() *heapSampler {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	s := &heapSampler{
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		baseline: ms.HeapAlloc,
	}
	go func() {
		defer close(s.done)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		var m runtime.MemStats
		for {
			select {
			case <-s.stop:
				return
			case <-tick.C:
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > s.peak.Load() {
					s.peak.Store(m.HeapAlloc)
				}
				if inuse := m.HeapInuse + m.StackInuse; inuse > s.peakSys.Load() {
					s.peakSys.Store(inuse)
				}
			}
		}
	}()
	return s
}

func (s *heapSampler) Stop() (peakHeapAlloc, peakInuse, baseline uint64) {
	close(s.stop)
	<-s.done
	return s.peak.Load(), s.peakSys.Load(), s.baseline
}

func mib(b uint64) string { return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20)) }

// exportPeakBudgetBytes is the ceiling enforced on a single MaxExportRows
// export, measured as peak HeapAlloc above the pre-run baseline.
//
// SIZED FROM MEASUREMENT, not chosen. Peak HeapAlloc is GC-timing sensitive, so
// it was measured three times each at GOMAXPROCS 1, 2, 4 and unset — the shapes
// a CI runner might have — with the streaming writer in place and with it
// reverted to SetCellValue:
//
//	streamed:      75.4  75.4  75.7  76.1  76.5  81.5  86.9  88.6  89.4  94.2  94.6 MiB
//	un-streamed:   234.9 (GOMAXPROCS=2)  242.8 (unset)  269.9 (GOMAXPROCS=1) MiB
//
// 150 MiB sits in the middle of that gap: 1.6x above the worst streamed run, and
// 0.64x of the best un-streamed one. Both margins matter. Too tight and the test
// flakes on a slow runner where the collector lags; too loose and reverting the
// writer stops failing, which is the whole point. Anyone raising this must
// re-measure the un-streamed column, because the budget is only meaningful
// relative to it.
const exportPeakBudgetBytes = 150 << 20

// measureExport runs one export handler end to end and reports peak heap.
// It returns the peak HeapAlloc above the baseline so callers can budget it.
func measureExport(t *testing.T, label string, run func(w http.ResponseWriter)) uint64 {
	t.Helper()

	// Warm up once so lazily-initialised package state (excelize's style
	// tables, sqlite statement caches) is not billed to the measured run.
	warm := &discardResponseWriter{}
	run(warm)
	if warm.code != 0 && warm.code != http.StatusOK {
		t.Fatalf("%s warm-up status = %d", label, warm.code)
	}

	sampler := startHeapSampler()
	w := &discardResponseWriter{}
	start := time.Now()
	run(w)
	elapsed := time.Since(start)
	peak, peakInuse, baseline := sampler.Stop()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	t.Logf("%s: peak HeapAlloc=%s (baseline %s, delta %s), peak HeapInuse+Stack=%s, "+
		"cumulative TotalAlloc=%s, bytes written=%s, elapsed=%s",
		label, mib(peak), mib(baseline), mib(peak-baseline), mib(peakInuse),
		mib(ms.TotalAlloc), mib(uint64(w.n)), elapsed.Round(time.Millisecond))

	if peak < baseline {
		return 0
	}
	return peak - baseline
}

// TestExportPeakMemory_StaysWithinBudget is the memory invariant's guard, and
// the only part of it that runs on every change.
//
// It exercises the exact request a household member can issue with no admin
// gate and no rate limit — GET /api/export/transactions over a ledger at
// MaxExportRows — and fails if it costs more heap than exportPeakBudgetBytes.
//
// It is a real guard, not decoration: reverting the Transactions sheet from the
// StreamWriter to a SetCellValue loop takes the same export to 235-270 MiB and
// this test fails by ~1.7x. TestExports_UseStreamWriter catches the same revert
// structurally and much faster; this one is what proves the structural pin is
// pinning something that matters.
func TestExportPeakMemory_StaysWithinBudget(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "budget-exporter", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")
	seedBulkTransactions(t, h.db, user.ID, cat, MaxExportRows)

	delta := measureExport(t, fmt.Sprintf("export/transactions (%d rows)", MaxExportRows),
		func(w http.ResponseWriter) {
			req := withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user)
			h.handleExportTransactions(w, req)
		})

	if delta > exportPeakBudgetBytes {
		t.Errorf("a %d-row export peaked at %s above baseline, over the %s budget.\n"+
			"The largest export any member can issue must stay well clear of the "+
			"container's 1 GiB mem_limit / 768 MiB GOMEMLIMIT — and two members "+
			"exporting at once costs about 1.8x this figure.\n"+
			"If a sheet was reverted from excelize's StreamWriter to SetCellValue, "+
			"that is the cause: normal mode holds the whole sheet as a live cell "+
			"tree until WriteTo.",
			MaxExportRows, mib(delta), mib(exportPeakBudgetBytes))
	}
}

func TestMeasureExportMemory(t *testing.T) {
	if os.Getenv("SPENDROP_EXPORT_MEM") != "1" {
		t.Skip("set SPENDROP_EXPORT_MEM=1 to run the export memory measurement")
	}
	rows := MaxExportRows
	if v := os.Getenv("SPENDROP_EXPORT_MEM_ROWS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("SPENDROP_EXPORT_MEM_ROWS: %v", err)
		}
		rows = n
	}

	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "mem-exporter", "admin")
	cat := seedExpenseCategory(t, h.queries, "Groceries")
	t.Logf("seeding %d transactions...", rows)
	seedBulkTransactions(t, h.db, user.ID, cat, rows)

	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	t.Logf("seeded %d transactions", n)

	// Isolate the two halves of the handler so the report attributes the peak
	// correctly: the drained []exportTxnRow slice versus the workbook build.
	measureExport(t, "drain only", func(w http.ResponseWriter) {
		got, err := h.drainExportTxnRows(context.Background(),
			`SELECT t.date, t.description, c.name, c.type, t.amount_cents,
				t.original_amount_cents, t.original_currency, t.booked_rate, t.tags, t.notes
			FROM transactions t JOIN categories c ON t.category_id = c.id
			WHERE t.deleted_at IS NULL ORDER BY t.date DESC, t.id DESC LIMIT ?`, rows)
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
		runtime.KeepAlive(got)
	})

	delta := measureExport(t, fmt.Sprintf("export/transactions (%d rows)", rows), func(w http.ResponseWriter) {
		req := withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user)
		h.handleExportTransactions(w, req)
	})
	if rows == MaxExportRows && delta > exportPeakBudgetBytes {
		t.Errorf("a %d-row export peaked at %s above baseline, over the %s budget: "+
			"the largest export any member can issue must stay well clear of the "+
			"container's 1 GiB limit, twice over for two concurrent members",
			rows, mib(delta), mib(exportPeakBudgetBytes))
	}

	measureExport(t, "export/yearly/2020", func(w http.ResponseWriter) {
		req := withUserAndURLParams(
			httptest.NewRequest(http.MethodGet, "/api/export/yearly/2020", nil),
			user, map[string]string{"year": "2020"})
		h.handleExportYearly(w, req)
	})

	measureExport(t, "export/monthly/2020/1", func(w http.ResponseWriter) {
		req := withUserAndURLParams(
			httptest.NewRequest(http.MethodGet, "/api/export/monthly/2020/1", nil),
			user, map[string]string{"year": "2020", "month": "1"})
		h.handleExportMonthly(w, req)
	})
}

// TestMeasureExportConcurrent measures two members exporting simultaneously,
// which is the realistic worst case for a two-person household — export has no
// admin gate and no rate limit, so this is an ordinary event, not an attack.
//
// The figure is only meaningful if the two handlers are genuinely in flight at
// the same time, and that is NOT free to assume here: the production pool is
// capped at one connection, so the drain phases must serialise. If they
// serialised end to end the number would just be a noisy restatement of the
// single-export figure. They do not, because the connection is released before
// the workbook is built (~80 ms of a ~370 ms handler is the read), which leaves
// the builds free to run at once. This test measures the overlap and fails if it
// is not there, so the number can never quietly become a measurement of
// something else.
func TestMeasureExportConcurrent(t *testing.T) {
	if os.Getenv("SPENDROP_EXPORT_MEM") != "1" {
		t.Skip("set SPENDROP_EXPORT_MEM=1 to run the export memory measurement")
	}
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "mem-exporter-a", "admin")
	cat := seedExpenseCategory(t, h.queries, "Groceries")
	seedBulkTransactions(t, h.db, user.ID, cat, MaxExportRows)

	run := func(w http.ResponseWriter) {
		req := withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user)
		h.handleExportTransactions(w, req)
	}
	// Warm up.
	run(&discardResponseWriter{})

	type span struct{ start, end time.Time }
	spans := make([]span, 2)

	sampler := startHeapSampler()
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			start := time.Now()
			run(&discardResponseWriter{})
			spans[i] = span{start: start, end: time.Now()}
		}(i)
	}
	<-done
	<-done
	peak, peakInuse, baseline := sampler.Stop()

	// Overlap of the two handler calls: from the later start to the earlier end.
	overlapStart, overlapEnd := spans[0].start, spans[0].end
	if spans[1].start.After(overlapStart) {
		overlapStart = spans[1].start
	}
	if spans[1].end.Before(overlapEnd) {
		overlapEnd = spans[1].end
	}
	overlap := overlapEnd.Sub(overlapStart)
	shorter := spans[0].end.Sub(spans[0].start)
	if d := spans[1].end.Sub(spans[1].start); d < shorter {
		shorter = d
	}

	delta := peak - baseline
	t.Logf("2 concurrent exports: peak HeapAlloc=%s (baseline %s, delta %s), peak HeapInuse+Stack=%s",
		mib(peak), mib(baseline), mib(delta), mib(peakInuse))
	t.Logf("  overlap=%v of the shorter handler's %v (%.0f%%); handler spans %v and %v",
		overlap.Round(time.Millisecond), shorter.Round(time.Millisecond),
		100*float64(overlap)/float64(shorter),
		spans[0].end.Sub(spans[0].start).Round(time.Millisecond),
		spans[1].end.Sub(spans[1].start).Round(time.Millisecond))

	// Half the shorter handler, not merely "greater than zero": the drain phases
	// serialise on the single connection, so a small positive overlap is what a
	// fully-serialised pair would ALSO produce. Demanding a substantial fraction
	// is what distinguishes concurrent execution from queueing.
	if overlap < shorter/2 {
		t.Errorf("the two exports overlapped for only %v of the shorter %v run: they "+
			"queued on the single connection instead of running together, so the peak "+
			"below is not a concurrency measurement",
			overlap.Round(time.Millisecond), shorter.Round(time.Millisecond))
	}
	// A second, independent check on the same thing: two serialised exports would
	// peak at roughly ONE export's cost, because the first releases its heap
	// before the second allocates.
	if delta < exportPeakBudgetBytes/2 {
		t.Errorf("concurrent peak %s is no larger than a single export: the two runs "+
			"did not hold their workbooks at the same time", mib(delta))
	}

	if delta > 2*exportPeakBudgetBytes {
		t.Errorf("two concurrent exports peaked at %s above baseline, over the %s budget",
			mib(delta), mib(2*exportPeakBudgetBytes))
	}
}
