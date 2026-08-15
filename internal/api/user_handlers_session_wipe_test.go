package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// B20 part 2: handleUpdateUser and handleDeleteUser each discarded the error
// from DeleteSessionsByUserID (`_ = h.queries…`), so a DELETE that could not
// run was reported as success. B20 filed this as "untestable today without a
// fault-injection seam"; it is testable without touching production code at
// all, because SQLite can be told to refuse the statement.
//
// blockSessionDeletes installs a BEFORE DELETE trigger that aborts every
// delete against `sessions`, and returns a function that removes it. This is
// the smallest honest seam available: no interface, no test-only field on
// Handler, and the failure it injects is the real one — the same
// SQLITE_CONSTRAINT the driver would surface from a genuinely refused write,
// arriving at the same call site through the same *sql.DB.
//
// It deliberately does NOT block anything else. Reads of `sessions` still
// work, so the assertions below can observe that the sessions survived, and
// every other table is untouched, so a 500 here can only have come from the
// session wipe.
func blockSessionDeletes(t *testing.T, db *sql.DB) (remove func()) {
	t.Helper()
	const create = `CREATE TRIGGER test_block_session_deletes
		BEFORE DELETE ON sessions
		FOR EACH ROW
		BEGIN SELECT RAISE(ABORT, 'injected: sessions is refusing deletes'); END`
	if _, err := db.ExecContext(context.Background(), create); err != nil {
		t.Fatalf("install session-delete fault: %v", err)
	}
	// Prove the fault is live before any handler runs. Without this, a typo in
	// the trigger would make every assertion below pass for the wrong reason:
	// the handler would return 200, and "no 500" would look like a handler
	// that ignores errors rather than a seam that never armed.
	//
	// The probe deletes EVERY session row, and needs one to exist: the trigger
	// is FOR EACH ROW, so a DELETE that matches nothing never fires it and
	// would report the fault as armed while it was not. The abort rolls the
	// statement back, so the fixture survives — which the count below is what
	// actually establishes.
	before := countSessionRows(t, db)
	if before == 0 {
		t.Fatal("seed a session before arming the fault — a zero-row DELETE cannot fire a FOR EACH ROW trigger")
	}
	if _, err := db.ExecContext(context.Background(), `DELETE FROM sessions`); err == nil {
		t.Fatal("the injected fault did not arm — a DELETE against sessions still succeeds")
	}
	if after := countSessionRows(t, db); after != before {
		t.Fatalf("the arming probe destroyed the fixture: sessions %d -> %d", before, after)
	}
	return func() {
		if _, err := db.ExecContext(context.Background(), `DROP TRIGGER test_block_session_deletes`); err != nil {
			t.Fatalf("remove session-delete fault: %v", err)
		}
	}
}

// countSessionRows counts every session in the table, unscoped — the
// household-wide twin of countSessions, used only to prove the injected fault
// blocks and rolls back.
func countSessionRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func putUserRole(t *testing.T, h *Handler, actor database.User, targetID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/users/%d", targetID), strings.NewReader(body))
	req = withUserAndURLParam(req, actor, "id", fmt.Sprintf("%d", targetID))
	rec := httptest.NewRecorder()
	h.handleUpdateUser(rec, req)
	return rec
}

// TestHandleUpdateUser_RoleChange_FailedSessionWipe_Returns500AndRollsBack is
// the invariant B20 asks for: if this endpoint says "updated" after a role
// change, that user has no surviving sessions. It proves the contrapositive —
// when the wipe cannot run, the response is not a success — and the stronger
// property the fix chose: the role change is rolled back with it, so a retry
// is identical to the first attempt.
//
// That last part is why all-or-nothing was picked over "keep the demotion,
// report the failure". handleUpdateUser only wipes `if role != existing.Role`,
// so a role change that persisted without its wipe is invisible to the retry:
// the second attempt would see the role already correct, skip the wipe
// entirely, and return 200 over the very sessions it was asked to clear.
func TestHandleUpdateUser_RoleChange_FailedSessionWipe_Returns500AndRollsBack(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	ctx := context.Background()

	admin := seedTestUser(t, q, "admin", RoleAdmin)
	target := seedNamedUser(t, q, "bob", "Bob The Member", RoleMember)
	targetSession := seedSessionForUser(t, q, target.ID)

	restore := blockSessionDeletes(t, db)

	rec := putUserRole(t, h, admin, target.ID, `{"role":"admin"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: a role change whose session wipe failed must not report success; body: %s",
			rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "sessions") {
		t.Errorf("body = %s, want it to name the session invalidation as the failure", body)
	}

	// The promotion must NOT have landed: the write and the wipe share one
	// transaction, so a failed wipe rolls the role back too.
	after, err := q.GetUserByID(ctx, target.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.Role != RoleMember {
		t.Errorf("role = %q, want it rolled back to %q — the privilege change committed while its session wipe failed",
			after.Role, RoleMember)
	}
	if after.DisplayName != "Bob The Member" {
		t.Errorf("display_name = %q, want it rolled back to %q", after.DisplayName, "Bob The Member")
	}
	if got := countSessions(t, h, target.ID); got != 1 {
		t.Errorf("sessions = %d, want the fault to have preserved 1 — the fixture is not proving what it claims", got)
	}

	// CONTROL ARM. Remove the fault and re-send the identical request: it must
	// now succeed and do the whole job. Without this the test would also pass
	// against a handler that 500s on every role change, and against one whose
	// retry silently skips the wipe.
	restore()

	rec = putUserRole(t, h, admin, target.ID, `{"role":"admin"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	after, err = q.GetUserByID(ctx, target.ID)
	if err != nil {
		t.Fatalf("get user after retry: %v", err)
	}
	if after.Role != RoleAdmin {
		t.Errorf("role after retry = %q, want %q", after.Role, RoleAdmin)
	}
	if _, err := q.GetSession(ctx, targetSession); err == nil {
		t.Error("the retry returned 200 with the target's session still resolvable")
	}
	if got := countSessions(t, h, target.ID); got != 0 {
		t.Errorf("sessions after retry = %d, want 0", got)
	}
}

// TestHandleUpdateUser_DisplayNameOnly_UnaffectedByASessionDeleteFault is the
// negative arm of the same seam. A rename changes no role, so it must never
// reach DeleteSessionsByUserID — with deletes against `sessions` refused, it
// still has to return 200 and land the new name.
//
// This is what keeps the fix above from being written as an unconditional
// wipe: that version would turn every typo correction into a forced logout,
// and with the fault armed, into a 500.
func TestHandleUpdateUser_DisplayNameOnly_UnaffectedByASessionDeleteFault(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	ctx := context.Background()

	admin := seedTestUser(t, q, "admin", RoleAdmin)
	target := seedNamedUser(t, q, "bob", "Bob The Member", RoleMember)
	targetSession := seedSessionForUser(t, q, target.ID)

	defer blockSessionDeletes(t, db)()

	rec := putUserRole(t, h, admin, target.ID, `{"display_name":"Robert"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a rename must not touch sessions at all; body: %s",
			rec.Code, rec.Body.String())
	}
	after, err := q.GetUserByID(ctx, target.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.DisplayName != "Robert" {
		t.Errorf("display_name = %q, want %q — the rename did not land", after.DisplayName, "Robert")
	}
	if _, err := q.GetSession(ctx, targetSession); err != nil {
		t.Errorf("the target's session is gone after a rename (%v)", err)
	}
}

// TestHandleUpdateUser_ConcurrentDemotionAndRename_KeepsTheDemotion is the
// read-modify-write half of the same transaction: the row this handler merges
// from must be read INSIDE the transaction that writes it.
//
// The shape it kills is a lost update with no error anywhere. A rename that
// carries no role fills the role in from the stored row, so if that read
// happens on the pool — outside the transaction — a demotion can commit and
// wipe the sessions in the gap between the rename's read and its write. The
// rename then writes the stale 'admin' back, and its own `role !=
// existing.Role` comparison is false against that same stale value, so it
// wipes nothing and answers 200. An acknowledged demotion is silently
// reverted, with live sessions.
//
// THE ASSERTION IS NOT PROBABILISTIC even though the schedule is: whichever
// order the two requests commit in, if both succeed the final role is
// 'member'. Demotion last leaves 'member' by writing it; demotion first
// leaves 'member' because the rename merges the demoted value. Only an
// interleaved read-read-write-write produces 'admin', so a passing run is
// never "the race did not happen to fire" — 'admin' is unreachable when the
// read is inside the transaction. What the iteration count buys is the chance
// to OBSERVE the defect when it is present, and it is sized from measurement
// rather than taste. Against the pre-fix placement (the merge read on
// h.queries, before BeginTx) the lost update fired on roughly one iteration in
// seven — first failure landed at iterations 1, 4, 2, 15 and 10 across five
// runs — so at 100 iterations the mutant died 30/30 at default parallelism and
// 30/30 under GOMAXPROCS=2, while this handler passed 60/60 across the same
// two regimes and under -race. DO NOT LOWER THE COUNT: it costs 0.2s, and the
// fixed handler cannot fail here on any schedule, so it trades no flakiness
// for its sensitivity.
func TestHandleUpdateUser_ConcurrentDemotionAndRename_KeepsTheDemotion(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	ctx := context.Background()

	admin := seedTestUser(t, q, "admin", RoleAdmin)
	target := seedNamedUser(t, q, "bob", "Bob", RoleAdmin)

	const iterations = 100
	for i := 0; i < iterations; i++ {
		// Fresh state per iteration: a promotion back to admin, a fresh
		// session, and a distinct name so a stale write is identifiable.
		if err := q.UpdateUser(ctx, database.UpdateUserParams{
			DisplayName: "Bob",
			Role:        RoleAdmin,
			ID:          target.ID,
		}); err != nil {
			t.Fatalf("reset target: %v", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, target.ID); err != nil {
			t.Fatalf("clear sessions: %v", err)
		}
		seedSessionForUser(t, q, target.ID)

		start := make(chan struct{})
		var wg sync.WaitGroup
		codes := make([]int, 2)
		bodies := make([]string, 2)

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			rec := putUserRole(t, h, admin, target.ID, `{"role":"member"}`)
			codes[0], bodies[0] = rec.Code, rec.Body.String()
		}()
		go func() {
			defer wg.Done()
			<-start
			rec := putUserRole(t, h, admin, target.ID, fmt.Sprintf(`{"display_name":"Robert %d"}`, i))
			codes[1], bodies[1] = rec.Code, rec.Body.String()
		}()
		close(start)
		wg.Wait()

		if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
			t.Fatalf("iteration %d: demote=%d (%s) rename=%d (%s); both must succeed — neither request is contending for anything a caller should have to retry",
				i, codes[0], bodies[0], codes[1], bodies[1])
		}

		after, err := q.GetUserByID(ctx, target.ID)
		if err != nil {
			t.Fatalf("iteration %d: get user: %v", i, err)
		}
		if after.Role != RoleMember {
			t.Fatalf("iteration %d: role = %q after a demotion and a rename both returned 200, want %q — the rename merged a snapshot taken before the demotion committed and wrote the old role back",
				i, after.Role, RoleMember)
		}
		// The other half of the same promise: the demotion was acknowledged,
		// so nothing that predates it is still logged in. A reverted demotion
		// usually leaves this behind too, but a rename that clobbers the role
		// AFTER the wipe would pass a role-only check on some schedules.
		if n := countSessions(t, h, target.ID); n != 0 {
			t.Fatalf("iteration %d: sessions = %d after an acknowledged demotion, want 0", i, n)
		}
	}
}

// TestHandleUpdateUser_UnknownID_Returns404AndReleasesTheConnection covers the
// early exit that moved INSIDE the transaction when the merge read did.
//
// The 404 itself is old behaviour; what is new is that it now returns with a
// transaction open on the stack, released only by the deferred Rollback. The
// pool is pinned to one connection, so a leaked transaction does not degrade
// performance — it takes the database away from every later request. The
// follow-up query is deadline-bounded so that failure arrives as a failed
// assertion instead of a hung test binary.
func TestHandleUpdateUser_UnknownID_Returns404AndReleasesTheConnection(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	admin := seedTestUser(t, q, "admin", RoleAdmin)

	rec := putUserRole(t, h, admin, 9999, `{"role":"member"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}

	bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := q.GetUserByID(bounded, admin.ID); err != nil {
		t.Fatalf("a query after the 404 failed (%v) — the transaction opened for the merge read was not rolled back, and it holds the only connection",
			err)
	}
}

// TestHandleDeleteUser_FailedSessionWipe_Returns500AndKeepsTheUser covers the
// second discarded error, at user_handlers.go's delete path.
//
// The explicit wipe there is redundant on the default configuration —
// sessions.user_id is ON DELETE CASCADE and production runs _foreign_keys=on —
// but it is kept for the FK-off configuration, and either way its error must
// not be swallowed: a database refusing to delete session rows is not a
// database that should then be asked to delete the account row.
func TestHandleDeleteUser_FailedSessionWipe_Returns500AndKeepsTheUser(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	ctx := context.Background()

	admin := seedTestUser(t, q, "admin", RoleAdmin)
	// No transactions and no checkpoints, so the two 409 guards ahead of the
	// wipe pass and this test reaches the statement it is about.
	target := seedNamedUser(t, q, "bob", "Bob The Member", RoleMember)
	seedSessionForUser(t, q, target.ID)

	restore := blockSessionDeletes(t, db)

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/users/%d", target.ID), nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()
	h.handleDeleteUser(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the session cleanup cannot run; body: %s", rec.Code, rec.Body.String())
	}
	// THE MESSAGE IS THE ASSERTION, not the status code, and this is the one
	// place in these tests where that matters. Restoring the discarded `_ =`
	// here does NOT turn the 500 into a 200: the handler carries on to
	// DeleteUser, whose cascade over sessions (ON DELETE CASCADE, and FK
	// actions do fire this trigger — measured) aborts on the same fault. So
	// the swallowed-error mutant produces a 500 with the user row still
	// present, and every status-and-state assertion around it passes. What
	// separates the two is WHICH statement reported: a checked wipe answers
	// "failed to clean up user sessions" before the account delete is ever
	// attempted, a swallowed one answers "failed to delete user".
	if body := rec.Body.String(); !strings.Contains(body, "failed to clean up user sessions") {
		t.Errorf("body = %s, want the session cleanup named as the failure — a 500 from the account delete instead means the cleanup error was swallowed",
			body)
	}
	if _, err := q.GetUserByID(ctx, target.ID); err != nil {
		t.Errorf("the user row is gone after a 500 (%v) — the delete ran past a failed cleanup", err)
	}

	// CONTROL ARM: with the fault removed the same request deletes the account
	// and its session, so the 500 above was the fault talking and not a
	// permanently broken endpoint.
	restore()

	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/users/%d", target.ID), nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec = httptest.NewRecorder()
	h.handleDeleteUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if _, err := q.GetUserByID(ctx, target.ID); err == nil {
		t.Error("the user row survived a 200 delete")
	}
	if got := countSessions(t, h, target.ID); got != 0 {
		t.Errorf("sessions = %d after the account was deleted, want 0", got)
	}
}
