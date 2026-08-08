package backup

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRunBaselinesTheSourceBeforeSnapshotting pins the one ordering in Run
// that no behavioural test can reach: sourceBaseline must run BEFORE
// Snapshot.
//
// What the order protects. Run measures the source — row count, size, query
// budget — and then copies it. Those two moments are deliberately not
// atomic, and Verify's one-row tolerance is shaped for exactly that gap: a
// write landing between the measurement and VACUUM INTO's snapshot point is
// forgiven. Reverse the order and the comparison stops being a check of the
// copy at all. The baseline would describe a source that has moved on, so on
// the CLI path — `docker exec spendrop ./spendrop backup …` runs against a
// live server still taking writes — every row committed during the copy
// makes the expected count exceed the backup's, and Verify rejects a
// perfectly good backup with "row count drift". The result is fail-safe
// rather than corrupting, but it means manual backups start failing
// intermittently under ordinary household use, and only under load.
//
// Why this is pinned textually. There is no seam: the ordering lives inside
// one function with no injectable clock, hook, or callback, and every test
// in this package is quiet during Run, so a reversed order produces
// identical results everywhere. Reaching it behaviourally would mean landing
// a write in a window we cannot observe from outside — which needs a
// test-only hook inside Run. Adding production machinery whose only consumer
// is a test is the worse trade, so the property is asserted against the
// syntax tree instead. Same reasoning as
// cmd/spendrop.TestShutdownDrainsPushDeliveryAfterTheServerStops and
// internal/api.TestDockerfileHealthcheckScrapesDataEndpoint; the helpers
// below are deliberate copies of that file's, since they cannot cross a
// package boundary without exporting test machinery.
//
// The AST — rather than a regex over the file — is what makes this honest.
// "sourceBaseline" appears in two comments in backup.go, one of them ABOVE
// the Snapshot call, so a text scan for the first occurrence would happily
// pass with the real call moved to the bottom of the function. Parsing
// without comments cannot be fooled that way, and confining the search to
// Run's own statement list means a match elsewhere in the file cannot
// satisfy it either.
func TestRunBaselinesTheSourceBeforeSnapshotting(t *testing.T) {
	const (
		baselineCall = "sourceBaseline"
		snapshotCall = "Snapshot"
	)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "backup.go", nil, 0)
	if err != nil {
		t.Fatalf("parse backup.go: %v", err)
	}

	// A statement-position pin means nothing if the work also happens
	// somewhere else — a second call inside a closure could be doing the
	// real measuring while the pinned one sits dead. Exactly one call site
	// each is the condition under which the index comparison below is
	// evidence rather than decoration.
	for _, name := range []string{baselineCall, snapshotCall} {
		switch n := countCallsTo(file, name); {
		case n == 0:
			t.Fatalf("backup.go never calls %s.\n"+
				"Run cannot produce a trusted backup without both measuring the source and "+
				"copying it; whichever half is gone, the sidecar would now be vouching for "+
				"something nobody checked. See the ordering comment in Run.", name)
		case n > 1:
			t.Fatalf("backup.go contains %d calls to %s, want exactly 1.\n"+
				"This pin compares the POSITION of single call sites, so a second one — "+
				"especially inside a closure — makes the comparison decoration rather than "+
				"evidence. If the second call is deliberate, rewrite this test rather than "+
				"relaxing it.", n, name)
		}
	}

	body := runBody(t, file)

	baselineIdx := directCallIndexTo(body, baselineCall)
	if baselineIdx < 0 {
		t.Fatalf("Run() never calls %s as one of its own statements.\n"+
			"Without it Run has no expectation to verify the copy against: Verify would be "+
			"handed a zero-valued VerifyParams, whose ExpectedTxCount of 0 makes every backup "+
			"of a non-empty database fail, and whose MaxSize of 0 removes the upper bound "+
			"entirely. See the ordering comment above the call in Run.", baselineCall)
	}

	snapshotIdx := directCallIndexTo(body, snapshotCall)
	if snapshotIdx < 0 {
		t.Fatalf("Run() never calls %s as one of its own statements — this test can no "+
			"longer locate the sequence it is pinning. If Run was restructured, re-point "+
			"this pin rather than deleting it: the ordering it guards is still real.", snapshotCall)
	}

	if baselineIdx >= snapshotIdx {
		t.Errorf("Run() baselines the source at statement %d, at or after %s at statement %d.\n"+
			"The measurement must come FIRST. Taken afterwards it describes a source that has "+
			"already moved on, so under a live writer the expected count exceeds the backup's "+
			"and Verify rejects good backups — see the ordering comment above the %s call.",
			baselineIdx, snapshotCall, snapshotIdx, baselineCall)
	}
}

// runBody returns the statement list of func Run.
func runBody(t *testing.T, file *ast.File) []ast.Stmt {
	t.Helper()
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Run" && fn.Recv == nil {
			return fn.Body.List
		}
	}
	t.Fatal("backup.go has no func Run — this pin cannot vouch for a function it cannot find")
	return nil
}

// directCallIndexTo returns the index of the first statement in body that
// calls the named function at Run's own level. A call inside a func literal
// does not count: its position in the list says nothing about when it runs.
// Deferred and go statements are skipped for the same reason. Returns -1
// when there is no such statement.
func directCallIndexTo(body []ast.Stmt, name string) int {
	for i, stmt := range body {
		switch stmt.(type) {
		case *ast.DeferStmt, *ast.GoStmt:
			continue // runs at a time unrelated to its position in the list
		}
		if callsDirectlyTo(stmt, name) {
			return i
		}
	}
	return -1
}

// callsDirectlyTo reports whether n contains a call to the named function
// outside any func literal. Both a bare identifier (sourceBaseline(…), the
// package-local form used here) and a selector (pkg.Name(…)) count, so the
// pin survives either of these moving behind a package boundary.
func callsDirectlyTo(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if found {
			return false
		}
		switch v := x.(type) {
		case *ast.FuncLit:
			return false // do not descend into closures
		case *ast.CallExpr:
			if callName(v) == name {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// countCallsTo counts every call to the named function anywhere in the file,
// closures included. A function's own declaration is not a call and is
// therefore not counted.
func countCallsTo(file *ast.File, name string) int {
	n := 0
	ast.Inspect(file, func(x ast.Node) bool {
		if call, ok := x.(*ast.CallExpr); ok && callName(call) == name {
			n++
		}
		return true
	})
	return n
}

// callName returns the called function's name for the two forms that appear
// here, and "" for anything else (a call through a variable, a method value,
// a conversion).
func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}
