package backup

import (
	"strings"
	"testing"
	"time"
)

// TestFormatFilename_Shape asserts the generated filename matches the
// documented format spendrop-YYYY-MM-DDTHHMMZ.db. The format uses UTC, minute
// precision, and deliberately avoids colons so the name is safe on every
// filesystem (notably FAT/exFAT/NTFS, which reject ":" in filenames).
func TestFormatFilename_Shape(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)
	got := FormatFilename(ts)
	want := "spendrop-2026-04-13T0300Z.db"
	if got != want {
		t.Errorf("FormatFilename = %q, want %q", got, want)
	}
}

// TestFormatFilename_ConvertsToUTC asserts the formatter normalizes non-UTC
// times into UTC before rendering. This is load-bearing: if two nodes in
// different timezones produce the same instant they must produce the same
// filename.
func TestFormatFilename_ConvertsToUTC(t *testing.T) {
	t.Parallel()
	// 2026-04-13 05:00:00 +02:00 is 2026-04-13 03:00:00 UTC.
	tz := time.FixedZone("CEST", 2*60*60)
	ts := time.Date(2026, 4, 13, 5, 0, 0, 0, tz)
	got := FormatFilename(ts)
	want := "spendrop-2026-04-13T0300Z.db"
	if got != want {
		t.Errorf("FormatFilename = %q, want %q", got, want)
	}
}

// TestFormatFilename_NoColons asserts the generated name contains no ":" so
// it is usable on every filesystem SpenDrop targets.
func TestFormatFilename_NoColons(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC)
	got := FormatFilename(ts)
	if strings.ContainsRune(got, ':') {
		t.Errorf("FormatFilename = %q, must not contain ':'", got)
	}
}

// TestRoundTrip covers 10+ representative instants through Format/Parse and
// asserts equality. Minute precision means sub-minute fields are lost, so the
// inputs are rounded to whole minutes before comparison.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []time.Time{
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 13, 23, 59, 0, 0, time.UTC),
		time.Date(2026, 2, 28, 12, 34, 0, 0, time.UTC),
		time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), // leap day
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC),
		time.Date(1999, 12, 31, 23, 59, 0, 0, time.UTC),
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2037, 7, 4, 15, 30, 0, 0, time.UTC),
		time.Date(2026, 6, 15, 9, 15, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 2, 1, 0, 0, time.UTC),
		time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	for _, in := range cases {
		name := FormatFilename(in)
		got, err := ParseFilename(name)
		if err != nil {
			t.Errorf("ParseFilename(%q): %v", name, err)
			continue
		}
		if !got.Equal(in) {
			t.Errorf("round-trip %v: got %v (from %q)", in, got, name)
		}
	}
}

// TestParseFilename_Rejects asserts names that do not match the documented
// shape are rejected. The parser is strict on purpose: Prune uses the
// parseability predicate to decide which files to consider for retention, and
// silently accepting malformed names would risk deleting operator files.
func TestParseFilename_Rejects(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"random.db",
		"spendrop-2026-04-13T0300Z.txt",     // wrong suffix
		"backup-2026-04-13T0300Z.db",        // wrong prefix
		"spendrop-not-a-timestamp.db",       // unparseable timestamp
		"spendrop-2026-13-01T0000Z.db",      // invalid month
		"spendrop-2026-04-13T2500Z.db",      // invalid hour
		"spendrop-2026-04-13T0300.db",       // missing Z
		"spendrop-2026-04-13T0300Zextra.db", // extra chars
		"README.txt",
		"spendrop.db",
	}
	for _, name := range cases {
		if _, err := ParseFilename(name); err == nil {
			t.Errorf("ParseFilename(%q): want error, got nil", name)
		}
	}
}
