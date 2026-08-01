// Package version reports the release this binary was built from.
//
// The value is baked in at link time by the Docker build (see the -ldflags
// -X line in the Dockerfile), so a shipped binary needs no environment
// variable, config file, or filesystem read to know what it is — the same
// image can never be told it is a different release than the one it was
// built as. Any build that does not stamp it — `go build ./...`, `go test`,
// docker-compose.dev.yml — reports Dev.
package version

import "strings"

// Dev is what an unstamped build reports. Local builds are the common case
// (every `go run`, every test binary), so this is a normal value, not an
// error state.
const Dev = "dev"

// version is overwritten at link time by
//
//	-ldflags "-X github.com/elienop/spendrop/internal/version.version=v0.35.0"
//
// Two constraints keep that working, both of them silent when broken — the
// build still succeeds and the binary just reports "dev":
//
//   - The linker can only patch a string variable declared with a constant
//     initializer. Turning this into a const, deriving it from another
//     value, or moving it into a struct disables the stamp.
//   - The -X flag addresses the symbol by import path and name, which is
//     plain text in the Dockerfile with nothing to typo-check it. Renaming
//     this variable or moving this package breaks the stamp.
//
// TestDockerfileStampsTheLinkedVersionVar pins both against the real
// Dockerfile so either mistake fails the test suite instead of shipping.
// Unexported so no runtime code can rewrite it; the linker resolves the
// symbol by name and is unaffected by Go visibility.
var version = Dev

// Version returns the release this binary was built from — a tag with its
// leading "v", e.g. "v0.35.0" — or Dev for an unstamped build.
//
// Never returns an empty string. `docker build --build-arg APP_VERSION=`
// passes an empty value straight through to the linker, and a blank version
// rendered in the UI reads as a broken template rather than as a local
// build.
func Version() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	return Dev
}
