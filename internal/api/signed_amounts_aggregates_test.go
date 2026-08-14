package api

// B10 signed-amount fixtures for every amount aggregate on the queries map.
//
// A refund is a NEGATIVE amount_cents on an EXPENSE row (migration 019 replaced
// CHECK(amount_cents > 0) with CHECK(amount_cents != 0)). Every aggregate below
// is a plain SUM, so the arithmetic is supposed to net without any code change
// — but "supposed to" was a reading of the SQL, not an execution of it, and
// before this file not one test in the suite had ever put a negative amount
// into any of these queries. These tests are what turn that reading into
// evidence: each seeds real purchases plus a refund and asserts the exact
// signed net, so a future change that reintroduces a positivity assumption
// anywhere in the chain (an ABS, a `> 0` filter, an unsigned scan) fails here
// instead of silently over-reporting a household's spending.
//
// Every fixture writes through the sqlc layer (q.CreateTransaction) rather than
// the HTTP handlers, so these tests are independent of whatever state the
// request-side validation gates happen to be in.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// seedRefundRow seeds a refund: a negative-amount row on an expense category.
// It is seedExpenseRow with the sign spelled out at the call site, plus a guard
// — a fixture that means to seed a refund and passes a positive amount by
// mistake would otherwise still produce a green test, just one measuring
// nothing.
func seedRefundRow(t *testing.T, q *database.Queries, userID, categoryID int64, date string, cents int64) database.Transaction {
	t.Helper()
	if cents >= 0 {
		t.Fatalf("seedRefundRow needs a NEGATIVE amount in cents, got %d", cents)
	}
	return seedExpenseRow(t, q, userID, categoryID, date, cents)
}

func TestSumExpensesByMonth_SignedNet(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedExpenseRow(t, q, user.ID, cat, "2026-05-03", 10000) // $100.00
	seedExpenseRow(t, q, user.ID, cat, "2026-05-09", 3000)  // $30.00
	seedRefundRow(t, q, user.ID, cat, "2026-05-14", -2500)  // -$25.00 returned

	got, err := q.SumExpensesByMonth(ctx, database.SumExpensesByMonthParams{Year: "2026", Month: "05"})
	if err != nil {
		t.Fatalf("SumExpensesByMonth: %v", err)
	}
	if got != 10500 {
		t.Errorf("month total = %d cents, want 10500 (100 + 30 - 25)", got)
	}
}

// A month whose refunds outweigh its purchases nets NEGATIVE. The dashboard
// reports this as total_spent, and month_remaining then exceeds the budget —
// both legal states under B10, and both unreachable before it.
func TestSumExpensesByMonth_RefundsCanDriveTheMonthNegative(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Electronics")

	seedExpenseRow(t, q, user.ID, cat, "2026-05-03", 4000)  // $40.00
	seedRefundRow(t, q, user.ID, cat, "2026-05-20", -90000) // -$900.00 laptop returned

	got, err := q.SumExpensesByMonth(ctx, database.SumExpensesByMonthParams{Year: "2026", Month: "05"})
	if err != nil {
		t.Fatalf("SumExpensesByMonth: %v", err)
	}
	if got != -86000 {
		t.Errorf("month total = %d cents, want -86000 (40 - 900)", got)
	}
}

// Negative INCOME is defined, not forbidden (design decision 2): it is an
// income reversal — a bounced payment, a clawed-back reimbursement — and it
// nets in the income sum exactly the way a refund nets in the expense sum.
func TestSumIncomeByMonth_SignedNet(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedIncomeCategory(t, q, "Salary")

	seedExpenseRow(t, q, user.ID, cat, "2026-05-01", 200000) // $2000.00 paid in
	seedRefundRow(t, q, user.ID, cat, "2026-05-06", -50000)  // -$500.00 clawed back

	got, err := q.SumIncomeByMonth(ctx, database.SumIncomeByMonthParams{Year: "2026", Month: "05"})
	if err != nil {
		t.Fatalf("SumIncomeByMonth: %v", err)
	}
	if got != 150000 {
		t.Errorf("income total = %d cents, want 150000 (2000 - 500)", got)
	}
}

// The homepage widget is user-scoped, so the fixture proves two things at once:
// the actor's own refund nets, and a housemate's refund does not reach into it.
func TestSumExpensesByMonthForUser_SignedNet(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	alice := seedTestUser(t, q, "alice", RoleMember)
	bob := seedTestUser(t, q, "bob", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedExpenseRow(t, q, alice.ID, cat, "2026-05-03", 8000) // $80.00
	seedRefundRow(t, q, alice.ID, cat, "2026-05-11", -1500) // -$15.00
	seedRefundRow(t, q, bob.ID, cat, "2026-05-12", -70000)  // bob's, must not count

	got, err := q.SumExpensesByMonthForUser(ctx, database.SumExpensesByMonthForUserParams{
		UserID: alice.ID, Year: "2026", Month: "05",
	})
	if err != nil {
		t.Fatalf("SumExpensesByMonthForUser: %v", err)
	}
	if got != 6500 {
		t.Errorf("alice's month total = %d cents, want 6500 (80 - 15); bob's -700 must not leak", got)
	}
}

// SumByCategoryForMonth feeds the dashboard pie, cellOverBudget and
// countOverBudgetCategories. Two things are pinned: the per-category totals net
// with sign, and ORDER BY total_cents DESC is a SIGNED sort — the refunded
// category sorts LAST, below a category that spent less in absolute terms.
func TestSumByCategoryForMonth_SignedNetAndOrdering(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	groceries := seedExpenseCategory(t, q, "Groceries")
	electronics := seedExpenseCategory(t, q, "Electronics")

	seedExpenseRow(t, q, user.ID, groceries, "2026-05-03", 6000) // net +$60.00
	seedExpenseRow(t, q, user.ID, electronics, "2026-05-04", 90000)
	seedRefundRow(t, q, user.ID, electronics, "2026-05-19", -95000) // net -$50.00

	rows, err := q.SumByCategoryForMonth(ctx, database.SumByCategoryForMonthParams{Year: "2026", Month: "05"})
	if err != nil {
		t.Fatalf("SumByCategoryForMonth: %v", err)
	}
	totals := make(map[int64]int64, len(rows))
	for _, r := range rows {
		totals[r.ID] = r.TotalCents
	}
	if totals[groceries] != 6000 {
		t.Errorf("groceries total = %d cents, want 6000", totals[groceries])
	}
	if totals[electronics] != -5000 {
		t.Errorf("electronics total = %d cents, want -5000 (900 - 950)", totals[electronics])
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 category rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].ID != groceries || rows[1].ID != electronics {
		t.Errorf("ORDER BY total_cents DESC must be a SIGNED sort: want groceries(+6000) then electronics(-5000), got %+v", rows)
	}
}

// TopDescriptions ranks merchants by total and applies a LIMIT, so a refund can
// push a merchant down the list or off it entirely. That is decided behavior
// under signed amounts, not an accident — this test is what makes it decided.
func TestTopDescriptions_SignedNetAndRanking(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Shopping")

	seedTestTransaction(t, q, user.ID, cat, "2026-05-02", 120.00, "Corner Store")
	seedTestTransaction(t, q, user.ID, cat, "2026-05-05", 900.00, "Big Box")
	seedTestTransaction(t, q, user.ID, cat, "2026-05-21", -880.00, "Big Box") // returned

	rows, err := q.TopDescriptions(ctx, database.TopDescriptionsParams{
		DateFrom: "2026-05-01", DateTo: "2026-05-31", Limit: 10,
	})
	if err != nil {
		t.Fatalf("TopDescriptions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 description rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Description != "Corner Store" || rows[0].TotalCents != 12000 {
		t.Errorf("rank 1 = %q/%d, want Corner Store/12000", rows[0].Description, rows[0].TotalCents)
	}
	if rows[1].Description != "Big Box" || rows[1].TotalCents != 2000 {
		t.Errorf("rank 2 = %q/%d, want Big Box/2000 (900 - 880 net)", rows[1].Description, rows[1].TotalCents)
	}
	// The refund does not reduce the COUNT — both rows are still real activity.
	if rows[1].TxCount != 2 {
		t.Errorf("Big Box tx_count = %d, want 2 (purchase + refund are both rows)", rows[1].TxCount)
	}
}

// The heatmap's whole point under B10: a day can hold rows and still net to
// zero. total_cents says nothing about whether the day has anything to show;
// txn_count does.
func TestSumExpensesByDay_SignedNetAndTxnCount(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedExpenseRow(t, q, user.ID, cat, "2026-05-03", 4000) // ordinary day
	seedExpenseRow(t, q, user.ID, cat, "2026-05-10", 3000) // net-zero day: bought
	seedRefundRow(t, q, user.ID, cat, "2026-05-10", -3000) // ... and returned

	rows, err := q.SumExpensesByDay(ctx, "2026")
	if err != nil {
		t.Fatalf("SumExpensesByDay: %v", err)
	}
	byDate := make(map[string]database.SumExpensesByDayRow, len(rows))
	for _, r := range rows {
		byDate[r.Date.Format("2006-01-02")] = r
	}
	ordinary, ok := byDate["2026-05-03"]
	if !ok {
		t.Fatalf("2026-05-03 missing from heatmap rows: %+v", rows)
	}
	if ordinary.TotalCents != 4000 || ordinary.TxnCount != 1 {
		t.Errorf("2026-05-03 = %d cents / %d rows, want 4000 / 1", ordinary.TotalCents, ordinary.TxnCount)
	}
	netZero, ok := byDate["2026-05-10"]
	if !ok {
		t.Fatalf("net-zero day 2026-05-10 dropped from heatmap rows — a day with rows must still appear: %+v", rows)
	}
	if netZero.TotalCents != 0 {
		t.Errorf("2026-05-10 total = %d cents, want 0 (30 - 30)", netZero.TotalCents)
	}
	if netZero.TxnCount != 2 {
		t.Errorf("2026-05-10 txn_count = %d, want 2 — this is the only signal that a net-zero day has rows to open", netZero.TxnCount)
	}
}

// Soft-delete discipline for the NEW column: a tombstoned row on a day must
// inflate neither the total nor the count. The count is the one that would slip
// through a copy-pasted review, because it is new and its predicate set is
// inherited rather than written.
func TestSumExpensesByDay_HidesTombstoned(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedExpenseRow(t, q, user.ID, cat, "2026-05-03", 4000)
	ghost := seedExpenseRow(t, q, user.ID, cat, "2026-05-03", 99900) // sentinel
	if err := q.SoftDeleteTransaction(ctx, ghost.ID); err != nil {
		t.Fatalf("tombstone: %v", err)
	}

	rows, err := q.SumExpensesByDay(ctx, "2026")
	if err != nil {
		t.Fatalf("SumExpensesByDay: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 day row, got %d: %+v", len(rows), rows)
	}
	if rows[0].TotalCents != 4000 {
		t.Errorf("total = %d cents, want 4000 (tombstoned 99900 must be excluded)", rows[0].TotalCents)
	}
	if rows[0].TxnCount != 1 {
		t.Errorf("txn_count = %d, want 1 (tombstoned row must not be counted)", rows[0].TxnCount)
	}
}

func TestSumExpensesByDayInMonth_SignedNet(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedExpenseRow(t, q, user.ID, cat, "2026-05-03", 4000)
	seedExpenseRow(t, q, user.ID, cat, "2026-05-10", 2000)
	seedRefundRow(t, q, user.ID, cat, "2026-05-10", -5000) // day 10 nets NEGATIVE

	rows, err := q.SumExpensesByDayInMonth(ctx, database.SumExpensesByDayInMonthParams{
		Year: "2026", Month: "05",
	})
	if err != nil {
		t.Fatalf("SumExpensesByDayInMonth: %v", err)
	}
	byDay := make(map[int64]int64, len(rows))
	for _, r := range rows {
		byDay[r.Day] = r.DailyTotalCents
	}
	if byDay[3] != 4000 {
		t.Errorf("day 3 = %d cents, want 4000", byDay[3])
	}
	if byDay[10] != -3000 {
		t.Errorf("day 10 = %d cents, want -3000 (20 - 50) — a cumulative spend line may dip", byDay[10])
	}
}

// RecurringDescriptions' HAVING counts DISTINCT months, not amounts, so it is
// sign-blind by construction; the annual total nets and the handler's
// monthly_avg division stays safe because month_count >= 3 can never be zero.
func TestRecurringDescriptions_SignedNet(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Subscriptions")

	for _, d := range []string{"2026-01-05", "2026-02-05", "2026-03-05", "2026-04-05"} {
		seedTestTransaction(t, q, user.ID, cat, d, 12.00, "Streaming")
	}
	// One month was refunded in full plus a goodwill credit.
	seedTestTransaction(t, q, user.ID, cat, "2026-04-20", -20.00, "Streaming")

	rows, err := q.RecurringDescriptions(ctx, "2026")
	if err != nil {
		t.Fatalf("RecurringDescriptions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 recurring row, got %d: %+v", len(rows), rows)
	}
	if rows[0].MonthCount != 4 {
		t.Errorf("month_count = %d, want 4 (the refund shares April's month key)", rows[0].MonthCount)
	}
	if rows[0].AnnualTotalCents != 2800 {
		t.Errorf("annual_total = %d cents, want 2800 (4*1200 - 2000)", rows[0].AnnualTotalCents)
	}
}

// The tag breakdown accumulates in Go rather than SQL, so the sign has to
// survive the scan AND the accumulator. Asserted at both ends: the raw rows
// carry the negative, and the handler's per-tag wire total nets.
func TestTagBreakdown_SignedNet(t *testing.T) {
	q, db := setupTestDB(t)
	ctx := context.Background()
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Shopping")

	seedTestTransactionWithTags(t, q, user.ID, cat, "2026-05-02", 150.00, "Jacket", "clothes")
	seedTestTransactionWithTags(t, q, user.ID, cat, "2026-05-19", -60.00, "Jacket refund", "clothes")

	raw, err := q.TransactionAmountsAndTags(ctx, database.TransactionAmountsAndTagsParams{
		DateFrom: "2026-05-01", DateTo: "2026-05-31",
	})
	if err != nil {
		t.Fatalf("TransactionAmountsAndTags: %v", err)
	}
	var rawSum int64
	for _, r := range raw {
		rawSum += r.AmountCents
	}
	if rawSum != 9000 {
		t.Errorf("raw rows sum to %d cents, want 9000 (15000 - 6000)", rawSum)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/reports/tags?year=2026&month=5", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleTagBreakdown(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("want 1 tag entry, got %d: %+v", len(resp.Data), resp.Data)
	}
	if got, _ := resp.Data[0]["total"].(float64); got != 90.00 {
		t.Errorf("tag total on the wire = %v, want 90 dollars", resp.Data[0]["total"])
	}
	if got, _ := resp.Data[0]["count"].(float64); got != 2 {
		t.Errorf("tag count = %v, want 2 — the refund is a row, not a subtraction from the count", resp.Data[0]["count"])
	}
}

// SumByMonthRange splits expense and income with a CASE, so each half has to
// net independently. Feeds dashboard savings YTD, budget-vs-actual and the
// income-expenses report.
func TestSumByMonthRange_SignedNetBothHalves(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	spend := seedExpenseCategory(t, q, "Groceries")
	earn := seedIncomeCategory(t, q, "Salary")

	seedExpenseRow(t, q, user.ID, spend, "2026-05-03", 30000)
	seedRefundRow(t, q, user.ID, spend, "2026-05-17", -5000)
	seedExpenseRow(t, q, user.ID, earn, "2026-05-01", 200000)
	seedRefundRow(t, q, user.ID, earn, "2026-05-08", -20000) // income reversal

	rows, err := q.SumByMonthRange(ctx, database.SumByMonthRangeParams{
		DateFrom: "2026-05-01", DateTo: "2026-05-31",
	})
	if err != nil {
		t.Fatalf("SumByMonthRange: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 month row, got %d: %+v", len(rows), rows)
	}
	if rows[0].ExpensesCents != 25000 {
		t.Errorf("expenses = %d cents, want 25000 (300 - 50)", rows[0].ExpensesCents)
	}
	if rows[0].IncomeCents != 180000 {
		t.Errorf("income = %d cents, want 180000 (2000 - 200)", rows[0].IncomeCents)
	}
	// Savings arithmetic downstream is income - expenses and was already signed.
	if net := rows[0].IncomeCents - rows[0].ExpensesCents; net != 155000 {
		t.Errorf("net = %d cents, want 155000", net)
	}
}

func TestSumByCategoryForRange_SignedNet(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Travel")

	seedExpenseRow(t, q, user.ID, cat, "2026-04-10", 80000)
	seedExpenseRow(t, q, user.ID, cat, "2026-05-02", 10000)
	seedRefundRow(t, q, user.ID, cat, "2026-05-25", -45000) // cancelled flight

	rows, err := q.SumByCategoryForRange(ctx, database.SumByCategoryForRangeParams{
		DateFrom: "2026-04-01", DateTo: "2026-05-31",
	})
	if err != nil {
		t.Fatalf("SumByCategoryForRange: %v", err)
	}
	byMonth := make(map[int64]int64, len(rows))
	for _, r := range rows {
		if r.ID == cat {
			byMonth[r.Month] = r.TotalCents
		}
	}
	if byMonth[4] != 80000 {
		t.Errorf("April = %d cents, want 80000", byMonth[4])
	}
	if byMonth[5] != -35000 {
		t.Errorf("May = %d cents, want -35000 (100 - 450) — a trend point may go negative", byMonth[5])
	}
}

// VerifyCheckpointTotal was already sign-agnostic (the handler bounds
// expected_amount by magnitude), so all three scopes need is a signed fixture
// proving the actual total nets the same way the user's own arithmetic does.
func TestVerifyCheckpointTotal_SignedNetAllScopes(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")
	other := seedExpenseCategory(t, q, "Transport")

	seedTestTransactionWithTags(t, q, user.ID, cat, "2026-05-02", 300.00, "Big shop", "reimbursable")
	seedTestTransactionWithTags(t, q, user.ID, cat, "2026-05-11", -120.00, "Returned", "reimbursable")
	seedTestTransaction(t, q, user.ID, other, "2026-05-12", 40.00, "Bus pass")
	// After the checkpoint date — must not be counted by any scope.
	seedTestTransaction(t, q, user.ID, cat, "2026-06-01", -999.00, "Later refund")

	for _, tc := range []struct {
		name  string
		param database.VerifyCheckpointTotalParams
		want  int64
	}{
		{
			name:  "total",
			param: database.VerifyCheckpointTotalParams{Date: "2026-05-31", ScopeType: "total"},
			want:  22000, // 300 - 120 + 40
		},
		{
			name:  "category",
			param: database.VerifyCheckpointTotalParams{Date: "2026-05-31", ScopeType: "category", ScopeID: cat},
			want:  18000, // 300 - 120
		},
		{
			name:  "tag",
			param: database.VerifyCheckpointTotalParams{Date: "2026-05-31", ScopeType: "tag", ScopeLabel: "reimbursable"},
			want:  18000, // 300 - 120
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := q.VerifyCheckpointTotal(ctx, tc.param)
			if err != nil {
				t.Fatalf("VerifyCheckpointTotal: %v", err)
			}
			if got != tc.want {
				t.Errorf("actual_cents = %d, want %d", got, tc.want)
			}
		})
	}
}

// A scoped checkpoint total can itself be negative — refunds booked after the
// only purchase in scope. The handler compares by equality, so the query has to
// report the negative rather than clamp it.
func TestVerifyCheckpointTotal_ScopedTotalCanBeNegative(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Electronics")

	seedExpenseRow(t, q, user.ID, cat, "2026-05-02", 5000)
	seedRefundRow(t, q, user.ID, cat, "2026-05-20", -30000)

	got, err := q.VerifyCheckpointTotal(ctx, database.VerifyCheckpointTotalParams{
		Date: "2026-05-31", ScopeType: "category", ScopeID: cat,
	})
	if err != nil {
		t.Fatalf("VerifyCheckpointTotal: %v", err)
	}
	if got != -25000 {
		t.Errorf("actual_cents = %d, want -25000 (50 - 300)", got)
	}
}
