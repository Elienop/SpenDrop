package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestShutdownDrainsPushDeliveryAfterTheServerStops pins the graceful-shutdown
// wiring for background push delivery.
//
// api.WaitForPushDelivery is unit-tested to exhaustion, but its ONE call site is
// a wiring seam nothing else can see: delete the call, hoist it above
// srv.Shutdown, or bury it in a closure, and every test in the repo still
// passes. What would actually happen is invisible in CI and quiet in production
// — notifications for saves the household just made vanish on every container
// restart, which on this deployment is every app update.
//
// The invariant has two halves and the misplacements break one each:
//
//   - AFTER srv.Shutdown. Before it, the drain would run while handlers are
//     still serving requests and queueing new fan-outs, so it would either
//     finish against a moving target or refuse deliveries for requests that
//     have not been answered yet.
//   - BEFORE the database handle closes. A delivery that meets HTTP 404/410
//     prunes the dead subscription row, and that write needs the handle. The
//     handle is released by `defer sqlDB.Close()`, so any statement in main's
//     body is before it — which is exactly why the drain has to BE a statement
//     in main's body rather than a call inside some closure.
//
// This is a source-level pin, in the spirit of
// internal/version.TestDockerfileStampsTheLinkedVersionVar: the property is
// about where a call sits in a program that cannot be started from a test, so it
// is asserted against the syntax tree rather than at runtime.
func TestShutdownDrainsPushDeliveryAfterTheServerStops(t *testing.T) {
	const (
		drainCall    = "WaitForPushDelivery"
		shutdownCall = "Shutdown"
		closeCall    = "Close"
	)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	// The direct statement pin below only means something if this is the only
	// call in the file — otherwise a second one hidden in a closure could be
	// doing the real work while the pinned one is dead.
	if n := countCalls(file, drainCall); n != 1 {
		t.Fatalf("main.go contains %d calls to %s, want exactly 1: the shutdown drain "+
			"is a single wiring seam and a second call site would make this pin meaningless",
			n, drainCall)
	}

	body := mainBody(t, file)

	drainIdx := directCallIndex(body, drainCall)
	if drainIdx < 0 {
		t.Fatalf("main() never calls %s as one of its own statements.\n"+
			"Background push delivery outlives the request that queued it, so without this "+
			"drain every notification still in flight is abandoned when the process exits — "+
			"and a delivery that prunes a dead subscription loses its database handle mid-write.",
			drainCall)
	}

	shutdownIdx := directCallIndex(body, shutdownCall)
	if shutdownIdx < 0 {
		t.Fatalf("main() never calls srv.%s as one of its own statements — "+
			"this test can no longer locate the shutdown sequence it is pinning", shutdownCall)
	}

	if drainIdx <= shutdownIdx {
		t.Errorf("main() drains push delivery (statement %d) before srv.%s (statement %d).\n"+
			"The drain must come AFTER the HTTP server stops accepting, or it races handlers "+
			"that are still queueing fan-outs.", drainIdx, shutdownCall, shutdownIdx)
	}

	// The database handle must still be open when the drain runs. `defer
	// sqlDB.Close()` satisfies that for free — defers run after every statement
	// in the body — so only a direct close needs an ordering check.
	if closeIdx := directCallIndex(body, closeCall); closeIdx >= 0 && closeIdx < drainIdx {
		t.Errorf("main() closes a handle at statement %d, before the push drain at statement %d.\n"+
			"A fan-out that meets HTTP 404/410 prunes the stale subscription row, and that "+
			"write needs the database handle.", closeIdx, drainIdx)
	}
}

// mainBody returns the statement list of func main.
func mainBody(t *testing.T, file *ast.File) []ast.Stmt {
	t.Helper()
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "main" && fn.Recv == nil {
			return fn.Body.List
		}
	}
	t.Fatal("main.go has no func main")
	return nil
}

// directCallIndex returns the index of the first statement in body that calls
// the named method on the statement's OWN goroutine and at main's own level —
// a call inside a func literal (a `defer func(){…}()`, a `go func(){…}()`, or
// any other closure) does not count, because its position in the list says
// nothing about when it runs. Returns -1 when there is no such statement.
func directCallIndex(body []ast.Stmt, name string) int {
	for i, stmt := range body {
		switch stmt.(type) {
		case *ast.DeferStmt, *ast.GoStmt:
			continue // runs at a time unrelated to its position in the list
		}
		if callsDirectly(stmt, name) {
			return i
		}
	}
	return -1
}

// callsDirectly reports whether n contains a call to the named method outside
// any func literal.
func callsDirectly(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if found {
			return false
		}
		switch v := x.(type) {
		case *ast.FuncLit:
			return false // do not descend into closures
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// countCalls counts every call to the named method anywhere in the file,
// closures included.
func countCalls(file *ast.File, name string) int {
	n := 0
	ast.Inspect(file, func(x ast.Node) bool {
		if call, ok := x.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
				n++
			}
		}
		return true
	})
	return n
}
