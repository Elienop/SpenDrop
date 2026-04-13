package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Prune applies a GFS-style retention policy to dir and returns the names of
// backups that were kept and removed. The retention policy keeps:
//
//   - the keepDaily most recent backups, AND
//   - the first backup seen per distinct ISO week, up to keepWeekly weeks, AND
//   - the first backup seen per distinct calendar month, up to keepMonthly months.
//
// Buckets are unioned, so a single file can satisfy daily, weekly, and
// monthly retention simultaneously. Any file in dir whose name does not
// round-trip through ParseFilename is left alone — operator files (README,
// manual exports, etc.) are not candidates for deletion.
//
// Prune is pure with respect to now: the caller decides "what time is it"
// so scheduler tests can drive the function deterministically. The error
// return covers only directory-read failures; per-file os.Remove failures
// are counted as removed (they may race with another process) and do not
// short-circuit the sweep. A companion .sha256 sidecar is removed alongside
// the backup file; a NotExist error on the sidecar is ignored because a
// previous crashed backup may have left one without the other.
func Prune(dir string, now time.Time, keepDaily, keepWeekly, keepMonthly int) (kept, removed []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read backup directory: %w", err)
	}

	type candidate struct {
		name string
		ts   time.Time
	}
	var cands []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ts, perr := ParseFilename(e.Name())
		if perr != nil {
			// Not a spendrop backup — leave it alone.
			continue
		}
		cands = append(cands, candidate{name: e.Name(), ts: ts})
	}

	// Sort newest first so bucket-walking picks the most recent file in
	// each week/month, which is the GFS convention.
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].ts.After(cands[j].ts)
	})

	keepSet := make(map[string]bool, len(cands))

	// Daily bucket: the N most recent files, unconditionally.
	for i, c := range cands {
		if i >= keepDaily {
			break
		}
		keepSet[c.name] = true
	}

	// Weekly bucket: first file seen per distinct ISO week, up to N weeks.
	// Using ISOWeek() means the "week" for a late-Sunday backup and an
	// early-Monday backup are different, which is the behavior we want —
	// a backup taken on Sun 23:59 represents last week, not this week.
	seenWeek := make(map[[2]int]bool)
	weeksKept := 0
	for _, c := range cands {
		if weeksKept >= keepWeekly {
			break
		}
		y, w := c.ts.ISOWeek()
		key := [2]int{y, w}
		if seenWeek[key] {
			continue
		}
		seenWeek[key] = true
		keepSet[c.name] = true
		weeksKept++
	}

	// Monthly bucket: first file seen per distinct calendar month, up to N months.
	seenMonth := make(map[[2]int]bool)
	monthsKept := 0
	for _, c := range cands {
		if monthsKept >= keepMonthly {
			break
		}
		key := [2]int{c.ts.Year(), int(c.ts.Month())}
		if seenMonth[key] {
			continue
		}
		seenMonth[key] = true
		keepSet[c.name] = true
		monthsKept++
	}

	for _, c := range cands {
		if keepSet[c.name] {
			kept = append(kept, c.name)
			continue
		}
		path := filepath.Join(dir, c.name)
		sidecar := path + ".sha256"
		// Only count the file as removed if os.Remove actually deleted
		// it (or the file was already gone — IsNotExist means another
		// process beat us to it, which is the same end state). A real
		// error — permission denied, I/O error, file busy — means the
		// backup is still on disk and the scheduler log would otherwise
		// claim it had been pruned. Leave it for the next tick: Prune
		// is idempotent, so a failed remove here becomes the next
		// iteration's target with no special handling needed.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			continue
		}
		// Sidecar cleanup is best-effort. It may legitimately not
		// exist (legacy Phase 1.1 backup that crashed between VACUUM
		// INTO and the sidecar write), and even a real permission
		// error here is non-fatal because the authoritative `.db` is
		// already gone — a stranded sidecar is removed the next time
		// a backup lands on the same minute (which requires
		// Interval < 1m, rejected by Validate).
		_ = os.Remove(sidecar)
		removed = append(removed, c.name)
	}

	return kept, removed, nil
}
