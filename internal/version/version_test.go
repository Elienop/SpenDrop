package version

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is where this package sits relative to the tree root. Go runs a
// test binary with its own package directory as the working directory, so
// the source-level pins below reach the Dockerfile and the release workflow
// from here.
const repoRoot = "../.."

func TestVersion(t *testing.T) {
	// The test binary itself is never stamped, so the zero-configuration
	// path is exactly what `go test` exercises: this asserts the value a
	// plain `go build` / `go run` / docker-compose.dev.yml build reports.
	t.Run("unstamped build reports dev", func(t *testing.T) {
		if got := Version(); got != Dev {
			t.Fatalf("Version() = %q on an unstamped build, want %q", got, Dev)
		}
	})

	// An empty or blank stamp must degrade to "dev" rather than to "". The
	// reachable path is `docker build --build-arg APP_VERSION=`, which puts
	// an empty value on the -X flag; the UI renders whatever comes back, and
	// a blank string there reads as a broken template rather than as a local
	// build.
	tests := []struct {
		name    string
		stamped string
		want    string
	}{
		{name: "release tag passes through", stamped: "v0.35.0", want: "v0.35.0"},
		{name: "empty stamp falls back", stamped: "", want: Dev},
		{name: "blank stamp falls back", stamped: "   ", want: Dev},
		{name: "surrounding whitespace trimmed", stamped: " v1.2.3\n", want: "v1.2.3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: these mutate the package-level stamp that
			// Version() reads, so they must not overlap each other or the
			// unstamped subtest above.
			original := version
			t.Cleanup(func() { version = original })

			version = tc.stamped
			if got := Version(); got != tc.want {
				t.Errorf("Version() with stamp %q = %q, want %q", tc.stamped, got, tc.want)
			}
		})
	}
}

// TestDockerfileStampsTheLinkedVersionVar pins the -ldflags -X symbol in the
// Dockerfile against this package's actual import path and variable name.
//
// The -X flag addresses a linker symbol by plain text. Nothing in the Go
// toolchain checks it: a stale path, a renamed variable, or a moved package
// all still build, still link, and still produce a working image — one that
// reports "dev" for every release forever. Renaming an unexported variable
// is exactly the kind of change a reviewer waves through, so the pin lives
// here, next to the variable, rather than in a doc comment nobody re-reads.
//
// This pin is static — it checks the Dockerfile's text. The dynamic half
// lives in CI, which runs `spendrop -version` inside the built image and
// compares the output to the tag the image was built with; that is what
// proves the linker actually patched the variable. Note that grepping a
// built binary is NOT a substitute: Go embeds the whole -ldflags string as
// a build setting, so the version text is in the file whether or not the
// stamp took.
func TestDockerfileStampsTheLinkedVersionVar(t *testing.T) {
	dockerfile := dockerfileInstructions(t)

	// -ldflags "-X github.com/elienop/spendrop/internal/version.version=${APP_VERSION}"
	xFlag := regexp.MustCompile(`-X\s+([^\s=]+)=(\S+?)"`)
	match := xFlag.FindStringSubmatch(dockerfile)
	if match == nil {
		t.Fatal("Dockerfile has no -ldflags -X stamp — the released binary would report " +
			`"dev" for every version. Expected a go build line carrying ` +
			`-ldflags "-X <module>/internal/version.version=${APP_VERSION}".`)
	}
	gotSymbol, gotValue := match[1], match[2]

	wantSymbol := modulePath(t) + "/internal/version." + versionVarName(t)
	if gotSymbol != wantSymbol {
		t.Errorf("Dockerfile stamps symbol %q, but this package's variable is %q.\n"+
			"A wrong symbol path is silently ignored by the linker: the image builds "+
			"and ships, and every release reports %q. Update the Dockerfile's -X flag.",
			gotSymbol, wantSymbol, Dev)
	}

	// The stamp must come from the build arg, not from a value hardcoded in
	// the Dockerfile — a literal there would pin every image ever built to
	// one version.
	if gotValue != "${APP_VERSION}" {
		t.Errorf("Dockerfile stamps the version as %q, want ${APP_VERSION} so the value "+
			"comes from the single build arg both artifacts read", gotValue)
	}
}

// TestDockerfileStampsBothArtifactsFromOneArg is the no-drift invariant: the
// Go binary and the frontend bundle must take their version from the same
// APP_VERSION build arg within a single build, so no image can report one
// version through its API and another in its UI. It also pins the arg's
// default, which is what makes an unstamped local build report "dev" instead
// of an empty string.
func TestDockerfileStampsBothArtifactsFromOneArg(t *testing.T) {
	dockerfile := dockerfileInstructions(t)

	// A global ARG (declared before the first FROM) is what lets both
	// builder stages read one value; a per-stage ARG with its own default
	// would let the two drift. Matched line-wise so the position is judged
	// against real instructions, not against a FROM mentioned in prose.
	lines := strings.Split(dockerfile, "\n")
	firstFrom := -1
	declaredGlobally := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "FROM ") {
			firstFrom = i
			break
		}
		if trimmed == "ARG APP_VERSION=dev" {
			declaredGlobally = true
		}
	}
	if firstFrom < 0 {
		t.Fatal("Dockerfile has no FROM instruction")
	}
	if !declaredGlobally {
		t.Error("Dockerfile does not declare `ARG APP_VERSION=dev` before its first FROM. " +
			"The global declaration is what feeds both builder stages from one value, and " +
			`the =dev default is what makes an unstamped build report "dev" rather than "".`)
	}

	// The frontend half of the contract. Vite only inlines VITE_-prefixed
	// env vars, so this exact assignment is what reaches the bundle as
	// import.meta.env.VITE_APP_VERSION.
	if !strings.Contains(dockerfile, "ENV VITE_APP_VERSION=${APP_VERSION}") {
		t.Error("Dockerfile does not export APP_VERSION to the Vite build as " +
			"`ENV VITE_APP_VERSION=${APP_VERSION}` — the bundle would report a different " +
			"version than the binary in the same image.")
	}
}

// TestReleaseWorkflowPassesTheComputedTag pins the connection between the
// release's computed version and the build that bakes it in. Break that link
// and the whole chain still works, every published image reports "dev", and
// the failure surfaces only when a human reads the version in a deployed UI.
//
// Pinned as two properties rather than one literal line, because the value
// legitimately travels via `env:` now (it must, so that shell quoting applies
// to it at all — a ${{ }} expression is pasted in as script text before bash
// sees it). Asserting the old inline `--build-arg APP_VERSION=${{ ... }}`
// string would fail on a workflow that is strictly better.
func TestReleaseWorkflowPassesTheComputedTag(t *testing.T) {
	// Scoped to the build step, NOT the whole file. Every assertion below
	// also matches text in the push step, which binds the same value — so a
	// file-wide search reports success while the build step is broken.
	// Mutation testing caught exactly that: removing the build step's binding
	// and removing its validation both left a file-wide version of this test
	// green.
	build := workflowStep(t, filepath.Join(".github", "workflows", "main.yml"), "Build the Docker image")

	// 1. The dry-run step's output is what the build consumes. steps.version
	//    is the dry run; the later real-tag step recreates the same value, so
	//    building from the dry-run output is what keeps the image tag and the
	//    git tag in lockstep. Accept either the env: binding or a direct
	//    interpolation — both carry the same value into this step.
	if !strings.Contains(build, "APP_VERSION: ${{ steps.version.outputs.new_tag }}") &&
		!strings.Contains(build, "--build-arg APP_VERSION=${{ steps.version.outputs.new_tag }}") {
		t.Errorf("the build step never binds steps.version.outputs.new_tag to APP_VERSION — "+
			"the computed tag would not reach the build and every published image "+
			"would report %q", Dev)
	}

	// 2. docker build actually receives it. The binding above is inert if the
	//    build command stops passing --build-arg. Quote optional, because
	//    `--build-arg "APP_VERSION=$APP_VERSION"` is the correct form and a
	//    literal match would reject it.
	buildArg := regexp.MustCompile(`--build-arg\s+"?APP_VERSION=`)
	if !buildArg.MatchString(build) {
		t.Errorf("the build step does not pass --build-arg APP_VERSION to docker build — "+
			"every published image would report %q", Dev)
	}

	// 3. The value is validated BEFORE it is used. It reaches -ldflags as a
	//    word list, which go build splits on whitespace into separate linker
	//    flags, and -extldflags reaches the system linker — so an unvalidated
	//    tag is a flag-injection vector, not a cosmetic problem.
	//
	//    Both guards are pinned because each catches what the other misses,
	//    and the charset one is the load-bearing half: the shape glob ends in
	//    `*`, which is unanchored, so on its own it happily accepts
	//    "v1.2.3 --extldflags=...". Verified by running both patterns against
	//    that input.
	for _, guard := range []struct{ pattern, why string }{
		{`*[!0-9v.]*`, "the charset guard, which is what rejects whitespace and metacharacters"},
		{`v[0-9]*.[0-9]*.[0-9]*`, "the shape guard, which rejects well-formed-but-wrong values like \"v..\""},
	} {
		if !strings.Contains(build, guard.pattern) {
			t.Errorf("the build step is missing %s (pattern %q). The tag is interpolated "+
				"into a -ldflags word list, so dropping this guard is a flag-injection "+
				"hole. If the validation is deliberately rewritten in another form, "+
				"update this test to pin the new one — do not just delete the check.",
				guard.why, guard.pattern)
		}
	}
}

// workflowStep returns the YAML text of one named step from a workflow file,
// from its `- name:` line up to the next step at the same indentation.
//
// Scoping matters: several steps in main.yml bind and use the same
// APP_VERSION value, so a file-wide assertion can be satisfied by a step other
// than the one under test.
func workflowStep(t *testing.T, rel, stepName string) string {
	t.Helper()

	lines := strings.Split(readRepoFile(t, rel), "\n")
	start := -1
	var indent string
	for i, line := range lines {
		if strings.HasSuffix(strings.TrimSpace(line), "- name: "+stepName) {
			start = i
			indent = line[:strings.Index(line, "-")]
			break
		}
	}
	if start < 0 {
		t.Fatalf("workflow %s has no step named %q", rel, stepName)
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], indent+"- ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// dockerfileInstructions returns the Dockerfile with its comment lines
// removed, so the pins above match real instructions only.
//
// Without this, a commented-out example is indistinguishable from the live
// line — and the -X regex takes the first match in the file, so a future
// comment showing the old or a sample symbol path above the RUN line would
// silently become the thing under test while the real instruction went
// unchecked. Comment lines are dropped rather than blanked; every assertion
// here is either line-wise or position-independent.
func dockerfileInstructions(t *testing.T) string {
	t.Helper()

	var kept []string
	for _, line := range strings.Split(readRepoFile(t, "Dockerfile"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// readRepoFile reads a file relative to the repository root.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(repoRoot, rel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// modulePath returns the module path declared in go.mod, so the expected -X
// symbol is derived from the repository rather than restated as a literal
// that could drift from it.
func modulePath(t *testing.T) string {
	t.Helper()
	for _, line := range strings.Split(readRepoFile(t, "go.mod"), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(path)
		}
	}
	t.Fatal("go.mod has no module directive")
	return ""
}

// versionVarName returns the name of the package-level variable the linker is
// meant to overwrite, and checks it is still declared in a form -X can patch.
//
// This package deliberately holds exactly ONE package-level var, which is what
// lets the name be discovered rather than restated as a literal that could
// drift from the Dockerfile. That is an assumption about this file, so it is
// asserted rather than trusted: if a second var ever appears, the right fix is
// to extend this helper to identify which one is the stamp target, not to
// guess.
//
// cmd/link only rewrites a string variable initialized to a constant
// expression. Deriving the value at init time (os.Getenv, a function call,
// concatenation of two vars) compiles and passes every other test in this file
// while silently disabling the stamp, so the declaration form is asserted too.
func versionVarName(t *testing.T) string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "version.go", nil, 0)
	if err != nil {
		t.Fatalf("parse version.go: %v", err)
	}

	// Every package-level var, regardless of how it is initialized. Filtering
	// by initializer here would let an unpatchable declaration masquerade as
	// "no var found" and report the wrong problem.
	type declaredVar struct {
		name string
		spec *ast.ValueSpec
	}
	var vars []declaredVar
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				vars = append(vars, declaredVar{name: name.Name, spec: value})
			}
		}
	}

	switch len(vars) {
	case 1:
		// The expected shape; fall through to the initializer check.
	case 0:
		t.Fatal("version.go declares no package-level var. The release stamp needs one " +
			`for -X to patch, and without it every build reports "dev".`)
	default:
		names := make([]string, 0, len(vars))
		for _, v := range vars {
			names = append(names, v.name)
		}
		t.Fatalf("version.go declares %d package-level vars (%s), but this test assumes "+
			"exactly one so it can derive the -X symbol name from the source. This is not "+
			"necessarily a bug in version.go — if a second var is wanted, extend "+
			"versionVarName to identify which one is the linker's target rather than "+
			"removing the check.", len(vars), strings.Join(names, ", "))
	}

	only := vars[0]
	if len(only.spec.Values) != 1 {
		t.Fatalf("package-level var %s at %s has no single initializer. -X only rewrites a "+
			"string variable initialized to a constant expression, so this build would "+
			"silently report %q.", only.name, fset.Position(only.spec.Pos()), Dev)
	}
	switch v := only.spec.Values[0].(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			t.Fatalf("package-level var %s at %s is initialized with a non-string literal. "+
				"-X only rewrites string variables; this build would silently report %q.",
				only.name, fset.Position(only.spec.Pos()), Dev)
		}
	case *ast.Ident:
		// A reference to a string constant (`var version = Dev`). Still a
		// constant expression and still patchable — confirmed by building
		// this package with -X against exactly this form.
	default:
		t.Fatalf("package-level var %s at %s is initialized with a %T, which the linker "+
			"cannot patch. -X only rewrites a string variable initialized to a constant "+
			"expression; this build would silently report %q.",
			only.name, fset.Position(only.spec.Pos()), v, Dev)
	}
	return only.name
}
