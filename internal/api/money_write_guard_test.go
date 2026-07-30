package api

import (
	"io/fs"

	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// guardedMoneyWriteHandlers are the JSON-body handlers that convert a
// client-supplied dollar amount into the cents column they store.
var guardedMoneyWriteHandlers = map[string]string{
	"handleSetSavingsGoal":             "savings_goals.target_amount_cents",
	"handleSetBudget":                  "budgets.amount_cents",
	"handleSetCategoryBudget":          "category_budgets.amount_cents",
	"handleCreateCheckpoint":           "balance_checkpoints.expected_amount_cents",
	"handleUpdateNotificationSettings": "notification_settings.large_txn_threshold_cents",
}

// TestMoneyWriteHandlersUseTheGuardedConversion is a SOURCE-level pin, and it
// is deliberate that it is not a request-level one.
//
// Each handler above validates its amount with a `<= 0` (or `< 0`) and a
// `> MaxTransactionAmount` check, and NaN compares false against both — so on
// paper an unguarded dollarsToCents there stores int64 minimum. Measured, no
// HTTP request can get there: encoding/json rejects the bare tokens NaN and
// Infinity outright, and range-errors on 1e400 before the field is ever set, so
// the only float that survives the decoder and fails safeDollarsToCents is one
// past MaxTransactionAmount, which the existing bound already rejects.
//
//	{"amount":NaN}      -> invalid character 'N' looking for beginning of value
//	{"amount":Infinity} -> invalid character 'I' looking for beginning of value
//	{"amount":1e400}    -> cannot unmarshal number 1e400 into ... float64
//	{"amount":1e308}    -> decodes, then fails the > MaxTransactionAmount check
//
// That makes safeDollarsToCents here defence in depth, not a live fix, and it
// means no behavioural test can fail when a call site is reverted to the bare
// function — a black-box regression test would pass either way and prove
// nothing. What can regress is the SHAPE: a future validator reordering, a new
// numeric decoder (json.Number, a form/CSV body, a protobuf edge) or a copied
// handler reintroduces the reachable version. This asserts the shape directly,
// so reverting any one call site fails a test.
//
// parseImportAmount is the same guard on the genuinely reachable path — xlsx
// cells are strings and strconv.ParseFloat DOES accept "NaN" — and this is
// modelled on it rather than inventing a second pattern.
func TestMoneyWriteHandlersUseTheGuardedConversion(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	pkg, ok := pkgs["api"]
	if !ok {
		t.Fatalf("package api not found; parsed %v", keysOf(pkgs))
	}

	seen := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			column, watched := guardedMoneyWriteHandlers[fn.Name.Name]
			if !watched {
				continue
			}
			seen[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok || ident.Name != "dollarsToCents" {
					return true
				}
				t.Errorf("%s calls the unguarded dollarsToCents at %s (writes %s) — "+
					"NaN compares false against both the <= 0 and the > Max check, so an "+
					"unrepresentable amount that ever reaches here is stored as int64 "+
					"minimum. Use safeDollarsToCents and 400 on false.",
					fn.Name.Name, fset.Position(ident.Pos()), column)
				return true
			})
		}
	}

	// A typo in the map above would silently watch nothing, which is exactly
	// how a guard test rots into a no-op.
	for name := range guardedMoneyWriteHandlers {
		if !seen[name] {
			t.Errorf("handler %q was never found in package api — the watch list has "+
				"gone stale and this test is checking nothing", name)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
