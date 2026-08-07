package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/go-chi/chi/v5"
)

// THIS FILE GUARDS AN INVARIANT, NOT A LIST. Every route that can write ledger
// text validates its length today; what this file asserts is that a route added
// six months from now cannot quietly stop doing so.
//
// The distinction matters because the failure mode is a route nobody thought
// to enumerate. A test that hard-codes the write routes it knows about passes
// forever no matter what is added next to them, which is precisely the case it
// needs to catch. So nothing here is enumerated by hand as the source of truth:
//
//	1. chi.Walk enumerates the routes actually REGISTERED on the production
//	   router. That is the inventory — not a list in a test file.
//	2. A go/ast scan of this package decides which of those routes accept
//	   ledger text, by looking at the request structs each handler decodes and
//	   asking whether any field carries a json tag in transactionTextJSONFields.
//	   A new handler that decodes a `description` is classified automatically.
//	3. Every route the scan flags must appear in EXACTLY ONE of the two tables
//	   below: textLimitProbes (exercised over the wire) or
//	   unprobeableTextRoutes (opted out, with a reason AND the name of a test
//	   that covers it, which is checked to exist).
//
// A route that satisfies none of those fails TestTransactionTextRoutes_AreAllClassified
// with a message naming itself. Adding it to the exemption table is the only
// way to opt out, and an exemption that names no covering test — or names one
// that has since been deleted or renamed — fails too.
//
// WHAT THIS CANNOT SEE, stated plainly because a guard that overstates its
// reach is worse than none: the ast scan reads json tags on decoded structs, so
// a handler that takes its text through an untyped field is invisible to it.
// PATCH /api/import/{importID}/rows/{rowID} is exactly that — its wire shape is
// {field, value any}, and the field NAME arrives as data. It is in the probe
// table by hand for that reason, and the probe table is deliberately allowed to
// be a superset of what the scan finds. A future route of that shape has to be
// added the same way. The scan closes the ordinary case; it does not close the
// case where the wire format hides the field name from static reading.

// transactionTextJSONFields are the wire field names that carry text into the
// ledger, and whose lengths limits.go bounds. A handler decoding any of these
// is a route this file has an opinion about.
//
// `note` (checkpoints) is here alongside `notes` (transactions): it is bounded
// by MaxNotesLength like the others, and leaving it out would have made the
// scan silently narrower than the caps it is guarding.
var transactionTextJSONFields = map[string]bool{
	"description":     true,
	"new_description": true,
	"tags":            true,
	"notes":           true,
	"note":            true,
}

// --- the route inventory, read off the real router ---

type walkedRoute struct {
	method  string
	pattern string
	handler string // the Go function name behind it, e.g. "handleCreateTransaction"
}

func (w walkedRoute) key() string { return w.method + " " + w.pattern }

// walkRouter enumerates every route registered on the production router.
// handler is recovered from the function pointer, which is what lets the ast
// scan in this file join a URL pattern to the source that serves it.
func walkRouter(t *testing.T, r chi.Routes) []walkedRoute {
	t.Helper()
	var out []walkedRoute
	err := chi.Walk(r, func(method, route string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		full := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
		name := strings.TrimSuffix(full[strings.LastIndex(full, ".")+1:], "-fm")
		out = append(out, walkedRoute{method: method, pattern: route, handler: name})
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	return out
}

// bodyMethods are the methods that carry a request body in this API, and so the
// ones a text field can arrive on. GET is excluded because no GET REQUEST here
// decodes one: the json tags a GET handler's structs carry belong to its
// RESPONSE, which emits stored text rather than accepting new text. The one
// handler registered on both GET and PUT (handleDefaultBudget) switches on
// r.Method and decodes only on the PUT, and it is that PUT registration the
// classification below applies to.
var bodyMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// --- the static scan: which handlers decode ledger text ---

// packageStructTags indexes every struct type declared in this package (test
// files excluded) by name, recording the json tags on its fields and the type
// names those fields reference. Anonymous struct types declared inside a
// function body are indexed under a synthetic name so `var req struct{…}`
// handlers are covered too.
type packageStructTags struct {
	tags map[string][]string // type name -> json tags declared directly on it
	refs map[string][]string // type name -> type names its fields reference
	fns  map[string]*ast.FuncDecl
}

func scanPackage(t *testing.T) *packageStructTags {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package source: %v", err)
	}
	idx := &packageStructTags{
		tags: map[string][]string{},
		refs: map[string][]string{},
		fns:  map[string]*ast.FuncDecl{},
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						if st, ok := ts.Type.(*ast.StructType); ok {
							idx.addStruct(ts.Name.Name, st)
						}
					}
				case *ast.FuncDecl:
					if d.Body != nil {
						idx.fns[d.Name.Name] = d
					}
				}
			}
		}
	}
	return idx
}

func (idx *packageStructTags) addStruct(name string, st *ast.StructType) {
	for _, field := range st.Fields.List {
		if field.Tag != nil {
			if raw, err := strconv.Unquote(field.Tag.Value); err == nil {
				if jt := reflect.StructTag(raw).Get("json"); jt != "" {
					idx.tags[name] = append(idx.tags[name], strings.Split(jt, ",")[0])
				}
			}
		}
		// Field TYPES matter as much as tags: batchUpdateRequest carries its
		// text inside a nested patchRequest, and an embedded struct (
		// createTransactionRequest embeds transactionRequest) has no tag at
		// all. Both are reached by following these references.
		ast.Inspect(field.Type, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				idx.refs[name] = append(idx.refs[name], id.Name)
			}
			return true
		})
	}
}

// textFieldsReachedBy returns the ledger-text json tags reachable from the
// named function's body: every type it mentions, every type those types
// reference, and any anonymous struct declared inline.
func (idx *packageStructTags) textFieldsReachedBy(fnName string) []string {
	fn, ok := idx.fns[fnName]
	if !ok {
		return nil
	}
	var queue []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			queue = append(queue, n.Name)
		case *ast.StructType:
			synthetic := "struct literal in " + fnName
			idx.addStruct(synthetic, n)
			queue = append(queue, synthetic)
		}
		return true
	})

	found := map[string]bool{}
	visited := map[string]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for _, tag := range idx.tags[cur] {
			if transactionTextJSONFields[tag] {
				found[tag] = true
			}
		}
		queue = append(queue, idx.refs[cur]...)
	}

	out := make([]string, 0, len(found))
	for tag := range found {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// textWritingRoutes is the join: registered body-carrying routes whose handler
// decodes at least one ledger-text field.
func textWritingRoutes(t *testing.T, routes []walkedRoute, idx *packageStructTags) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, rt := range routes {
		if !bodyMethods[rt.method] {
			continue
		}
		if fields := idx.textFieldsReachedBy(rt.handler); len(fields) > 0 {
			out[rt.key()] = fields
		}
	}
	return out
}

// --- the probe table ---

// probeEnv carries the session and the ids a probe needs to build a request
// that is valid in every respect except the one over-long field.
type probeEnv struct {
	session    *http.Cookie
	categoryID int64
	txnID      int64
	importID   string
}

func (e probeEnv) cookie(t *testing.T) *http.Cookie {
	t.Helper()
	if e.session == nil {
		t.Fatal("probe environment has no session cookie")
	}
	return e.session
}

// textLimitProbe exercises one route with one text field. The AT-THE-LIMIT
// request is the load-bearing half: it proves the request is otherwise valid
// and reaches the handler's write path, so that the one-character-longer
// request's 400 can only be a length check and not a missing field, a bad id
// or an auth failure. Without it a probe that 400s for an unrelated reason
// would pass while asserting nothing.
//
// It also uses a multi-byte character, so a route that regresses from charLen
// to len() fails here as well: the at-limit request is refused the moment the
// count becomes bytes.
type textLimitProbe struct {
	method string
	// pattern is the chi route pattern, as chi.Walk emits it. It is matched
	// against the walk, so a renamed or deleted route fails the classification
	// test rather than silently skipping its probe.
	pattern string
	field   string
	limit   int
	// url builds the concrete request path; body builds the JSON body with
	// value in the field under test.
	url  func(probeEnv) string
	body func(env probeEnv, value string) string
	// okStatus is the status the at-the-limit request must return.
	okStatus int
}

func jsonBody(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // test-only: the fixtures below are all marshalable
	}
	return string(b)
}

var textLimitProbes = []textLimitProbe{
	// Single-row create: manual entry, quick-add and the API-token surface.
	{
		method: http.MethodPost, pattern: "/api/transactions", field: "description",
		limit: MaxDescriptionLength, okStatus: http.StatusCreated,
		url: func(probeEnv) string { return "/api/transactions" },
		body: func(env probeEnv, v string) string {
			return jsonBody(map[string]any{"date": "2026-01-15", "amount": 1.5, "category_id": env.categoryID, "description": v})
		},
	},
	{
		method: http.MethodPost, pattern: "/api/transactions", field: "tags",
		limit: MaxTagsLength, okStatus: http.StatusCreated,
		url: func(probeEnv) string { return "/api/transactions" },
		body: func(env probeEnv, v string) string {
			return jsonBody(map[string]any{"date": "2026-01-15", "amount": 1.5, "category_id": env.categoryID, "description": "tags probe", "tags": v})
		},
	},
	{
		method: http.MethodPost, pattern: "/api/transactions", field: "notes",
		limit: MaxNotesLength, okStatus: http.StatusCreated,
		url: func(probeEnv) string { return "/api/transactions" },
		body: func(env probeEnv, v string) string {
			return jsonBody(map[string]any{"date": "2026-01-15", "amount": 1.5, "category_id": env.categoryID, "description": "notes probe", "notes": v})
		},
	},

	// Batch create: the offline queue drains through here.
	{
		method: http.MethodPost, pattern: "/api/transactions/batch", field: "description",
		limit: MaxDescriptionLength, okStatus: http.StatusCreated,
		url: func(probeEnv) string { return "/api/transactions/batch" },
		body: func(env probeEnv, v string) string {
			return jsonBody([]map[string]any{{"date": "2026-01-16", "amount": 2, "category_id": env.categoryID, "description": v}})
		},
	},
	{
		method: http.MethodPost, pattern: "/api/transactions/batch", field: "tags",
		limit: MaxTagsLength, okStatus: http.StatusCreated,
		url: func(probeEnv) string { return "/api/transactions/batch" },
		body: func(env probeEnv, v string) string {
			return jsonBody([]map[string]any{{"date": "2026-01-16", "amount": 2, "category_id": env.categoryID, "description": "batch tags probe", "tags": v}})
		},
	},
	{
		method: http.MethodPost, pattern: "/api/transactions/batch", field: "notes",
		limit: MaxNotesLength, okStatus: http.StatusCreated,
		url: func(probeEnv) string { return "/api/transactions/batch" },
		body: func(env probeEnv, v string) string {
			return jsonBody([]map[string]any{{"date": "2026-01-16", "amount": 2, "category_id": env.categoryID, "description": "batch notes probe", "notes": v}})
		},
	},

	// Single-row full replace.
	{
		method: http.MethodPut, pattern: "/api/transactions/{id}", field: "description",
		limit: MaxDescriptionLength, okStatus: http.StatusOK,
		url: func(env probeEnv) string { return "/api/transactions/" + strconv.FormatInt(env.txnID, 10) },
		body: func(env probeEnv, v string) string {
			return jsonBody(map[string]any{"date": "2026-01-15", "amount": 1.5, "category_id": env.categoryID, "description": v})
		},
	},
	{
		method: http.MethodPut, pattern: "/api/transactions/{id}", field: "tags",
		limit: MaxTagsLength, okStatus: http.StatusOK,
		url: func(env probeEnv) string { return "/api/transactions/" + strconv.FormatInt(env.txnID, 10) },
		body: func(env probeEnv, v string) string {
			return jsonBody(map[string]any{"date": "2026-01-15", "amount": 1.5, "category_id": env.categoryID, "description": "put tags probe", "tags": v})
		},
	},
	{
		method: http.MethodPut, pattern: "/api/transactions/{id}", field: "notes",
		limit: MaxNotesLength, okStatus: http.StatusOK,
		url: func(env probeEnv) string { return "/api/transactions/" + strconv.FormatInt(env.txnID, 10) },
		body: func(env probeEnv, v string) string {
			return jsonBody(map[string]any{"date": "2026-01-15", "amount": 1.5, "category_id": env.categoryID, "description": "put notes probe", "notes": v})
		},
	},

	// Bulk edit by id list, and the same patch applied by filter.
	{
		method: http.MethodPost, pattern: "/api/transactions/batch-update", field: "description",
		limit: MaxDescriptionLength, okStatus: http.StatusOK,
		url: func(probeEnv) string { return "/api/transactions/batch-update" },
		body: func(env probeEnv, v string) string {
			return jsonBody(map[string]any{"ids": []int64{env.txnID}, "patch": map[string]any{"description": v}})
		},
	},
	{
		method: http.MethodPost, pattern: "/api/transactions/batch-update", field: "tags",
		limit: MaxTagsLength, okStatus: http.StatusOK,
		url: func(probeEnv) string { return "/api/transactions/batch-update" },
		body: func(env probeEnv, v string) string {
			return jsonBody(map[string]any{"ids": []int64{env.txnID}, "patch": map[string]any{"tags": v}, "tagsMode": "replace"})
		},
	},
	{
		method: http.MethodPost, pattern: "/api/transactions/update-by-filter", field: "description",
		limit: MaxDescriptionLength, okStatus: http.StatusOK,
		url: func(probeEnv) string { return "/api/transactions/update-by-filter?category_id=0" },
		body: func(_ probeEnv, v string) string {
			return jsonBody(map[string]any{"patch": map[string]any{"description": v}})
		},
	},
	{
		method: http.MethodPost, pattern: "/api/transactions/update-by-filter", field: "tags",
		limit: MaxTagsLength, okStatus: http.StatusOK,
		url: func(probeEnv) string { return "/api/transactions/update-by-filter?category_id=0" },
		body: func(_ probeEnv, v string) string {
			return jsonBody(map[string]any{"patch": map[string]any{"tags": v}, "tagsMode": "replace"})
		},
	},

	// Checkpoint notes are bounded by MaxNotesLength like a transaction note.
	{
		method: http.MethodPost, pattern: "/api/checkpoints", field: "note",
		limit: MaxNotesLength, okStatus: http.StatusCreated,
		url: func(probeEnv) string { return "/api/checkpoints" },
		body: func(_ probeEnv, v string) string {
			return jsonBody(map[string]any{"scope_type": "total", "date": "2026-01-15", "expected_amount": 10, "note": v})
		},
	},

	// The dismissed-recurring list stores a transaction description verbatim
	// and matches it back against the ledger.
	{
		method: http.MethodPost, pattern: "/api/reports/recurring/dismiss", field: "description",
		limit: MaxDescriptionLength, okStatus: http.StatusOK,
		url: func(probeEnv) string { return "/api/reports/recurring/dismiss" },
		body: func(_ probeEnv, v string) string {
			return jsonBody(map[string]any{"year": 2026, "description": v})
		},
	},

	// The import preview's row editor. INVISIBLE TO THE AST SCAN — its wire
	// shape is {field, value any}, so the field name arrives as data and no
	// json tag names it. Listed by hand; see the file header.
	{
		method: http.MethodPatch, pattern: "/api/import/{importID}/rows/{rowID}", field: "description",
		limit: MaxDescriptionLength, okStatus: http.StatusOK,
		url: func(env probeEnv) string { return "/api/import/" + env.importID + "/rows/0" },
		body: func(_ probeEnv, v string) string {
			return jsonBody(map[string]any{"field": "description", "value": v})
		},
	},
}

// unprobeableTextRoutes are routes the scan flags that cannot be exercised by
// putting an over-long string in a JSON field. Each one states why and names
// the tests that do cover it; TestTransactionTextRoutes_AreAllClassified
// checks those tests exist, so an exemption cannot outlive its coverage.
var unprobeableTextRoutes = []struct {
	method    string
	pattern   string
	reason    string
	coveredBy []string
}{
	{
		method:  http.MethodPost,
		pattern: "/api/import/upload",
		reason: "Ledger text arrives inside the uploaded workbook, not in a JSON field — " +
			"there is no request key to lengthen. The upload gate flags over-long rows in the " +
			"preview rather than refusing the file, which is a different assertion than a 400.",
		coveredBy: []string{
			"TestHandleImportUpload_FlagsOverLongFieldsWithoutRefusingTheFile",
			"TestCheckImportRowLengths_LimitsAreCharacters",
		},
	},
	{
		method:  http.MethodPost,
		pattern: "/api/import/confirm",
		reason: "Same: the text under test came from the workbook at upload time and this " +
			"request carries only the import_id. checkImportRowLengths is what blocks the " +
			"insert, and it is exercised through the handler by the tests named here.",
		coveredBy: []string{
			"TestHandleImportConfirm_BlocksOnOverLongField",
			"TestImportFieldLengths_MatchTheWritePath",
		},
	},
}

// --- the tests ---

// TestRouteWalk_SeesTheRealRouteTable is the anti-vacuity guard for everything
// else in this file. A walk that returned nothing — or that stopped resolving
// handler names — would make every other test here pass while checking no
// route at all, which is strictly worse than having no test.
func TestRouteWalk_SeesTheRealRouteTable(t *testing.T) {
	q, db := setupTestDB(t)
	routes := walkRouter(t, NewRouter(q, db, nil))

	// A floor, not the exact count: the router had 92 routes when this was
	// written and routes get added. Half of that is comfortably below any
	// legitimate shrink and far above a broken walk.
	const floor = 45
	if len(routes) < floor {
		t.Fatalf("chi.Walk found %d routes, want at least %d — the walk is not seeing the router, and every other test in this file has gone vacuous",
			len(routes), floor)
	}

	seen := map[string]string{}
	for _, rt := range routes {
		seen[rt.key()] = rt.handler
	}
	// Named routes, so a walk that silently returns a different tree (or one
	// with unresolvable handlers) is caught rather than merely counted.
	for pattern, handler := range map[string]string{
		"POST /api/transactions":                    "handleCreateTransaction",
		"POST /api/transactions/batch":              "handleBatchCreateTransactions",
		"PUT /api/transactions/{id}":                "handleUpdateTransaction",
		"POST /api/transactions/batch-update":       "handleBatchUpdateTransactions",
		"POST /api/transactions/update-by-filter":   "handleUpdateTransactionsByFilter",
		"POST /api/import/confirm":                  "handleImportConfirm",
		"PATCH /api/import/{importID}/rows/{rowID}": "handleImportPatchRow",
	} {
		got, ok := seen[pattern]
		if !ok {
			t.Errorf("chi.Walk did not report %s; either the route was removed or the walk is not reading the real router", pattern)
			continue
		}
		if got != handler {
			t.Errorf("%s resolves to %q, want %q — handler names are how this file joins a route to its source", pattern, got, handler)
		}
	}
}

// TestTransactionTextScan_FindsTheKnownWritePaths pins the static scan itself.
// The scan is what makes the classification automatic, and a scan that silently
// found nothing would turn the completeness test below into a no-op: with no
// flagged routes, every route is trivially classified.
func TestTransactionTextScan_FindsTheKnownWritePaths(t *testing.T) {
	q, db := setupTestDB(t)
	routes := walkRouter(t, NewRouter(q, db, nil))
	flagged := textWritingRoutes(t, routes, scanPackage(t))

	if len(flagged) < 8 {
		t.Fatalf("the scan flagged only %d routes (%v); it has stopped reading request structs and the completeness test below is now vacuous",
			len(flagged), flagged)
	}

	// Each of these is a route whose handler visibly decodes the named field.
	// If the scan stops finding one, it is the scan that broke, not the route.
	for pattern, want := range map[string]string{
		"POST /api/transactions":                  "description",
		"POST /api/transactions/batch":            "notes",
		"PUT /api/transactions/{id}":              "tags",
		"POST /api/transactions/batch-update":     "description",
		"POST /api/transactions/update-by-filter": "tags",
		"POST /api/checkpoints":                   "note",
	} {
		fields, ok := flagged[pattern]
		if !ok {
			t.Errorf("scan did not flag %s at all", pattern)
			continue
		}
		found := false
		for _, f := range fields {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("scan flagged %s with %v, missing %q", pattern, fields, want)
		}
	}
}

// TestTransactionTextRoutes_AreAllClassified is the guard proper. Every
// registered route that accepts ledger text must be probed or explicitly
// exempted; every probe and exemption must correspond to a route that is
// actually registered.
func TestTransactionTextRoutes_AreAllClassified(t *testing.T) {
	q, db := setupTestDB(t)
	routes := walkRouter(t, NewRouter(q, db, nil))

	registered := map[string]bool{}
	for _, rt := range routes {
		registered[rt.key()] = true
	}
	flagged := textWritingRoutes(t, routes, scanPackage(t))

	probed := map[string]bool{}
	for _, p := range textLimitProbes {
		probed[p.method+" "+p.pattern] = true
	}
	exempt := map[string]bool{}
	for _, e := range unprobeableTextRoutes {
		exempt[e.method+" "+e.pattern] = true
	}

	// 1. Every route that accepts ledger text is classified.
	for route, fields := range flagged {
		switch {
		case probed[route] && exempt[route]:
			t.Errorf("%s is both probed and exempted; it must be exactly one", route)
		case probed[route], exempt[route]:
		default:
			t.Errorf(`%s accepts ledger text %v and is neither probed nor exempted.

A route that writes user text must reject text longer than the caps in limits.go.
Add it to textLimitProbes with a request body this test can lengthen, or — only if
its text cannot arrive in a JSON field — to unprobeableTextRoutes with a reason and
the name of a test that does cover it.`, route, fields)
		}
	}

	// 2. No stale entries. A probe or exemption naming a route that no longer
	//    exists is dead weight that reads as coverage.
	for _, p := range textLimitProbes {
		if key := p.method + " " + p.pattern; !registered[key] {
			t.Errorf("textLimitProbes has an entry for %s (field %q), which is not a registered route — the route was renamed or removed", key, p.field)
		}
	}
	for _, e := range unprobeableTextRoutes {
		key := e.method + " " + e.pattern
		if !registered[key] {
			t.Errorf("unprobeableTextRoutes exempts %s, which is not a registered route", key)
			continue
		}
		// 3. An exemption must be exempting something. One granted to a route
		//    the scan does not flag is either stale or was never needed, and
		//    either way it makes the list look like it is doing more work than
		//    it is.
		if _, ok := flagged[key]; !ok {
			t.Errorf("unprobeableTextRoutes exempts %s, but the scan does not flag it as accepting ledger text — remove the exemption", key)
		}
	}

	// 4. Every exemption states a reason and names covering tests THAT EXIST.
	//    A reason alone is a promise; naming a test that can be checked is
	//    evidence, and it rots loudly instead of silently when the test is
	//    renamed away.
	testFns := testFunctionNames(t)
	for _, e := range unprobeableTextRoutes {
		key := e.method + " " + e.pattern
		if strings.TrimSpace(e.reason) == "" {
			t.Errorf("%s is exempted with no reason", key)
		}
		if len(e.coveredBy) == 0 {
			t.Errorf("%s is exempted without naming a covering test", key)
		}
		for _, name := range e.coveredBy {
			if !testFns[name] {
				t.Errorf("%s is exempted on the strength of %s, which does not exist in this package — the coverage it claims is gone",
					key, name)
			}
		}
	}
}

// testFunctionNames collects the test functions declared in this package, so an
// exemption's coveredBy reference can be checked rather than trusted.
func testFunctionNames(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse test sources: %v", err)
	}
	out := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Test") {
					out[fn.Name.Name] = true
				}
			}
		}
	}
	if len(out) < 100 {
		t.Fatalf("found only %d test functions in this package; the parse is not seeing the test files and every coveredBy check has gone vacuous", len(out))
	}
	return out
}

// TestTransactionTextRoutes_RejectOverLongText runs each probe through the real
// router, with a real session, against a real database.
func TestTransactionTextRoutes_RejectOverLongText(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)
	env := newProbeEnv(t, q, router)

	for _, probe := range textLimitProbes {
		t.Run(probe.method+" "+probe.pattern+" "+probe.field, func(t *testing.T) {
			// Multi-byte on purpose: at the limit this is 2x the bytes, so a
			// check that has slipped back to len() refuses the control.
			atLimit := repeatTo(arabicLetter, probe.limit)
			rec := sendProbe(t, router, env, probe, atLimit)
			if rec.Code != probe.okStatus {
				t.Fatalf("CONTROL FAILED: %d characters was answered %d, want %d; body=%s\n"+
					"The probe is not reaching this route's write path, so the rejection below would prove nothing. "+
					"Either the request shape is stale or the limit is being counted in bytes.",
					probe.limit, rec.Code, probe.okStatus, rec.Body.String())
			}

			overLimit := repeatTo(arabicLetter, probe.limit+1)
			rec = sendProbe(t, router, env, probe, overLimit)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%d characters in %q was answered %d, want 400; body=%s\n"+
					"One character more than the control, which this route accepted — so this route writes text past the %d-character cap limits.go states.",
					probe.limit+1, probe.field, rec.Code, rec.Body.String(), probe.limit)
			}
		})
	}
}

func sendProbe(t *testing.T, router http.Handler, env probeEnv, probe textLimitProbe, value string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(probe.method, probe.url(env), strings.NewReader(probe.body(env, value)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(env.cookie(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// newProbeEnv registers the admin the probes run as, finds a real category,
// creates a transaction for the id-addressed routes, and seeds one in-memory
// import session for the preview row editor.
func newProbeEnv(t *testing.T, q *database.Queries, router http.Handler) probeEnv {
	t.Helper()
	cookie := registerViaRouter(t, router, "probeadmin")

	cats, err := q.ListAllCategories(t.Context())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(cats) == 0 {
		t.Fatal("no seeded categories; the probes need a real category_id")
	}

	body := jsonBody(map[string]any{
		"date": "2026-01-15", "amount": 3.25, "category_id": cats[0].ID, "description": "probe seed",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed transaction: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode seeded transaction: %v", err)
	}

	users, err := q.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) == 0 {
		t.Fatal("no users after register")
	}

	// The preview row editor works on an in-memory session. Seeding the store
	// directly is what upload does; going through upload would mean building an
	// xlsx fixture for a route whose text never comes from one.
	importID := strings.Repeat("a", 32)
	importStore.Store(importID, &importEntry{
		UserID:    users[0].ID,
		Rows:      []importRow{{RowID: 0, Date: "2026-01-15", Description: "probe row", Amount: 1}},
		Columns:   []string{"date", "description", "amount"},
		CreatedAt: time.Now(),
	})
	t.Cleanup(func() { importStore.Delete(importID) })

	return probeEnv{
		categoryID: cats[0].ID,
		txnID:      created.ID,
		importID:   importID,
		session:    cookie,
	}
}
