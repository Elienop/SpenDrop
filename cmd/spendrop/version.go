package main

import (
	"fmt"
	"io"

	"github.com/elienop/spendrop/internal/version"
)

// handleVersionFlag prints the release this binary was linked with and reports
// whether it consumed the invocation. args is os.Args[1:]. When it returns
// true the caller must exit 0 without starting the server.
//
// This is the only way to read the linked version from outside the process
// without authenticating. Grepping the binary is NOT equivalent and must not
// be substituted for it: Go embeds the full -ldflags string as a build setting
// inside every binary (`go version -m` reads it back), so the version text is
// present in the file whether or not the linker actually patched the variable.
// A grep therefore passes on a binary whose -X symbol path is typo'd — the
// exact regression this flag exists to catch. Only running the binary reads
// the variable the server itself reads.
//
// Deliberately does NOT go through dispatchSubcommand: that runs after
// config.Load(), and asking "what version are you?" must not depend on the
// deployment holding a valid configuration. It is also why the flag spelling
// is checked by hand rather than via the flag package — flag.Parse() on a
// binary that also takes bare subcommand words would reject `spendrop backup
// <path>`.
func handleVersionFlag(args []string, out io.Writer) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "-version", "--version":
		// Bare value with no label, so callers can compare it directly
		// (`[ "$(spendrop -version)" = "v0.35.0" ]`) instead of parsing.
		fmt.Fprintln(out, version.Version())
		return true
	default:
		return false
	}
}
