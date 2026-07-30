package backup

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// writeBackupAt creates an empty backup file (and a matching .sha256 sidecar
// when sidecar is true) for the given time in dir. It returns the filename so
// tests can assert against keep/remove sets.
func writeBackupAt(t *testing.T, dir string, ts time.Time, sidecar bool) string {
	t.Helper()
	name := FormatFilename(ts)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if sidecar {
		if err := os.WriteFile(path+".sha256", []byte("sum  "+name+"\n"), 0o644); err != nil {
			t.Fatalf("write sidecar %s: %v", name, err)
		}
	}
	return name
}

// TestPrune_KeepsNewestDaily asserts the first keepDaily newest files are
// always kept, regardless of week/month distribution.
func TestPrune_KeepsNewestDaily(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)

	// Create 10 consecutive daily backups ending "today".
	var names []string
	for i := 0; i < 10; i++ {
		ts := now.AddDate(0, 0, -i)
		names = append(names, writeBackupAt(t, dir, ts, false))
	}

	kept, removed, _, err := Prune(dir, now, 3, 0, 0, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// The three newest (today, today-1, today-2) are kept.
	wantKept := map[string]bool{names[0]: true, names[1]: true, names[2]: true}
	if len(kept) != 3 {
		t.Errorf("kept = %v (len %d), want 3", kept, len(kept))
	}
	for _, k := range kept {
		if !wantKept[k] {
			t.Errorf("unexpected kept file: %s", k)
		}
	}
	if len(removed) != 7 {
		t.Errorf("removed = %v (len %d), want 7", removed, len(removed))
	}

	// Filesystem must match the return values.
	for k := range wantKept {
		if _, err := os.Stat(filepath.Join(dir, k)); err != nil {
			t.Errorf("kept file missing from disk: %s", k)
		}
	}
}

// TestPrune_RemovesSidecarCompanion asserts Prune removes the .sha256 sidecar
// alongside any file it removes. An orphaned sidecar would trip the next
// backup run's overwrite guard, so this cleanup is load-bearing.
func TestPrune_RemovesSidecarCompanion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)

	var names []string
	for i := 0; i < 5; i++ {
		ts := now.AddDate(0, 0, -i)
		names = append(names, writeBackupAt(t, dir, ts, true))
	}

	_, removed, _, err := Prune(dir, now, 2, 0, 0, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("removed = %v (len %d), want 3", removed, len(removed))
	}

	// Removed files' sidecars must also be gone.
	for _, r := range removed {
		if _, err := os.Stat(filepath.Join(dir, r)); !os.IsNotExist(err) {
			t.Errorf("backup %s still exists: %v", r, err)
		}
		if _, err := os.Stat(filepath.Join(dir, r+".sha256")); !os.IsNotExist(err) {
			t.Errorf("sidecar for %s still exists: %v", r, err)
		}
	}
	// Kept files' sidecars must still exist.
	wantKept := map[string]bool{names[0]: true, names[1]: true}
	for k := range wantKept {
		if _, err := os.Stat(filepath.Join(dir, k+".sha256")); err != nil {
			t.Errorf("kept sidecar missing: %v", err)
		}
	}
}

// TestPrune_SidecarMissingOK asserts Prune does not error when a removed
// file has no sidecar. This can happen if a previous Phase 1.1 backup
// crashed after writing the .db but before the .sha256.
func TestPrune_SidecarMissingOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		ts := now.AddDate(0, 0, -i)
		writeBackupAt(t, dir, ts, false)
	}

	_, removed, _, err := Prune(dir, now, 2, 0, 0, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("removed = %v (len %d), want 3", removed, len(removed))
	}
}

// TestPrune_IgnoresUnrelatedFiles asserts Prune does not touch files whose
// names don't match the spendrop pattern. An operator might drop a README or
// a manual export into the backup dir; those must survive unscathed.
func TestPrune_IgnoresUnrelatedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)

	// Drop a README and an unrelated tarball alongside the backups.
	readme := filepath.Join(dir, "README.txt")
	if err := os.WriteFile(readme, []byte("backups live here\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	archive := filepath.Join(dir, "manual-export.tar")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	for i := 0; i < 5; i++ {
		ts := now.AddDate(0, 0, -i)
		writeBackupAt(t, dir, ts, true)
	}

	if _, _, _, err := Prune(dir, now, 1, 0, 0, 2); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Operator files must still exist.
	if _, err := os.Stat(readme); err != nil {
		t.Errorf("README.txt should not have been touched: %v", err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Errorf("manual-export.tar should not have been touched: %v", err)
	}
}

// TestPrune_40DaySimulation implements the acceptance criterion from the
// data-stewardship plan: 40 consecutive daily backups, pruned with (7,4,12),
// leaves a bounded keep set containing the 7 most recent files plus
// representative weekly and monthly picks.
func TestPrune_40DaySimulation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// "now" is 2026-04-13 03:00 UTC. We seed 40 daily files ending today.
	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)

	var names []string
	for i := 0; i < 40; i++ {
		ts := now.AddDate(0, 0, -i)
		names = append(names, writeBackupAt(t, dir, ts, true))
	}

	kept, removed, _, err := Prune(dir, now, 7, 4, 12, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Total must be bounded: at most 7 daily + 4 weekly + 12 monthly = 23.
	// In practice we expect less due to overlap (weekly/monthly buckets
	// land on the same file as daily ones for the first week/month).
	if len(kept) > 23 {
		t.Errorf("kept %d files, want <= 23", len(kept))
	}
	if len(kept)+len(removed) != 40 {
		t.Errorf("kept(%d) + removed(%d) = %d, want 40", len(kept), len(removed), len(kept)+len(removed))
	}

	keptSet := make(map[string]bool, len(kept))
	for _, k := range kept {
		keptSet[k] = true
	}

	// The 7 most recent files must be in the keep set. Sort names asc then
	// pick the last 7 for "most recent".
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	newest7 := sorted[len(sorted)-7:]
	for _, n := range newest7 {
		if !keptSet[n] {
			t.Errorf("newest-7 file not in keep set: %s", n)
		}
	}

	// At least one file from a week older than the most recent 7 must also
	// be kept — otherwise the weekly bucket did nothing. The weekly rep for
	// 40 days back (~2026-03-05) should land somewhere in the older range.
	olderKept := 0
	for _, k := range kept {
		isNewest7 := false
		for _, n := range newest7 {
			if k == n {
				isNewest7 = true
				break
			}
		}
		if !isNewest7 {
			olderKept++
		}
	}
	if olderKept == 0 {
		t.Errorf("weekly/monthly policy kept nothing beyond the newest 7: kept=%v", kept)
	}

	// Sanity: the filesystem state must agree with the return values.
	for k := range keptSet {
		if _, err := os.Stat(filepath.Join(dir, k)); err != nil {
			t.Errorf("kept file missing from disk: %s", k)
		}
	}
	for _, r := range removed {
		if _, err := os.Stat(filepath.Join(dir, r)); !os.IsNotExist(err) {
			t.Errorf("removed file still on disk: %s", r)
		}
	}
}

// TestPrune_OverlappingBuckets asserts that a single file satisfying daily
// and weekly and monthly buckets is counted once and not double-removed.
func TestPrune_OverlappingBuckets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)
	name := writeBackupAt(t, dir, now, true)

	kept, removed, _, err := Prune(dir, now, 7, 4, 12, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(kept) != 1 || kept[0] != name {
		t.Errorf("kept = %v, want [%s]", kept, name)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want empty", removed)
	}
}

// TestPrune_EmptyDir is a no-op; Prune must not error on an empty backup dir.
func TestPrune_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	kept, removed, _, err := Prune(dir, time.Now(), 7, 4, 12, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(kept) != 0 || len(removed) != 0 {
		t.Errorf("kept=%v removed=%v, want both empty", kept, removed)
	}
}

// TestPrune_MissingDir returns an error.
func TestPrune_MissingDir(t *testing.T) {
	t.Parallel()
	if _, _, _, err := Prune(filepath.Join(t.TempDir(), "does-not-exist"), time.Now(), 1, 0, 0, 2); err == nil {
		t.Fatal("Prune on missing dir: want error, got nil")
	}
}
