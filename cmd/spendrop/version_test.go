package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/version"
)

// TestHandleVersionFlag covers both directions of the seam: the spellings that
// print the release and exit, and the invocations that must fall through so
// main() goes on to load config and start the server exactly as before.
func TestHandleVersionFlag(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantHandled bool
	}{
		{name: "single dash", args: []string{"-version"}, wantHandled: true},
		{name: "double dash", args: []string{"--version"}, wantHandled: true},

		// Everything below must fall through. The no-args case is the
		// normal server start; the others prove the flag check did not
		// swallow the existing subcommands or a bare unknown argument
		// (which dispatchSubcommand is responsible for, not this).
		{name: "no arguments starts the server", args: nil, wantHandled: false},
		{name: "empty argument list", args: []string{}, wantHandled: false},
		{name: "backup subcommand", args: []string{"backup", "/tmp/x.db"}, wantHandled: false},
		{name: "audit subcommand", args: []string{"audit"}, wantHandled: false},
		{name: "unknown flag", args: []string{"-vers"}, wantHandled: false},
		{name: "version not in first position", args: []string{"backup", "-version"}, wantHandled: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			handled := handleVersionFlag(tc.args, &out)

			if handled != tc.wantHandled {
				t.Fatalf("handleVersionFlag(%q) handled = %v, want %v", tc.args, handled, tc.wantHandled)
			}
			if !tc.wantHandled {
				if out.Len() != 0 {
					t.Errorf("fall-through invocation wrote %q to stdout; it must stay silent "+
						"so it does not corrupt a subcommand's output", out.String())
				}
				return
			}

			// The printed value must be the one the server itself reports,
			// not a literal — that is the whole point of the flag.
			got := strings.TrimSuffix(out.String(), "\n")
			if got != version.Version() {
				t.Errorf("printed %q, want %q", got, version.Version())
			}
			// Exactly one trailing newline and nothing else: CI compares
			// this output byte-for-byte against the tag it built with, and
			// any label or banner would break that comparison.
			if out.String() != version.Version()+"\n" {
				t.Errorf("output was %q, want exactly %q — the flag must print a bare "+
					"value so callers can compare it without parsing",
					out.String(), version.Version()+"\n")
			}
		})
	}
}

// TestHandleVersionFlagReportsTheLinkedValue pins the flag to the version
// package for the same reason the HTTP handler is pinned: an unstamped test
// binary reports "dev", so a hardcoded "dev" here would satisfy the assertions
// above and still ship a binary that lies about every release.
//
// The dynamic half of this — that a STAMPED binary prints the stamp — is
// proven by the CI step that runs `spendrop -version` inside the built image
// and compares the output to the tag the image was built with.
func TestHandleVersionFlagReportsTheLinkedValue(t *testing.T) {
	var out bytes.Buffer
	if !handleVersionFlag([]string{"-version"}, &out) {
		t.Fatal("-version was not handled")
	}
	if got := strings.TrimSpace(out.String()); got != version.Dev {
		t.Fatalf("an unstamped test binary printed %q, want %q", got, version.Dev)
	}
}
