package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// defaultKeepCorrupt bounds the quarantine when a caller leaves it unset.
const defaultKeepCorrupt = 2

// Prune applies a GFS-style retention policy to dir and returns the names of
// backups that were kept and removed. The retention policy keeps:
//
//   - the keepDaily most recent backups, AND
//   - the newest backup per distinct calendar day, up to keepDaily days, AND
//   - the first backup seen per distinct ISO week, up to keepWeekly weeks, AND
//   - the first backup seen per distinct calendar month, up to keepMonthly months.
//
// The daily tier has two halves on purpose. The recency half keeps intra-day
// restore points when BACKUP_INTERVAL is sub-daily; the calendar half is what
// makes the tier's COVERAGE independent of the interval, matching its ISO-week
// and calendar-month siblings. With only the recency half, keepDaily meant
// "keep N files", so the recent-recovery window was keepDaily × BACKUP_INTERVAL
// rather than keepDaily days: measured worst-case at-least-daily coverage with
// the default 7/4/12 was 144h at a 24h interval but 72h at 12h (which the
// README recommends as a common adjustment) and 6h at 1h — turning the backup
// dial toward "safer" silently made recovery worse.
//
// Buckets are unioned, so a single file can satisfy daily, weekly, and
// monthly retention simultaneously. Any file in dir whose name does not
// round-trip through ParseFilename is left alone — operator files (README,
// manual exports, etc.) are not candidates for deletion.
//
// Quarantined `.corrupt` files are the one exception to that rule: they are
// parsed (after stripping the suffix) and bounded by keepCorrupt. They used to
// fall outside every retention mechanism in the system, and because a failed
// verify never writes a sidecar the size baseline never advances, so a stuck
// failure deposited one FULL-SIZE copy of the database per tick, forever, onto
// the same volume as the live database.
//
// Prune is pure with respect to now: the caller decides "what time is it"
// so scheduler tests can drive the function deterministically. The error
// return covers only directory-read failures; per-file os.Remove failures
// are counted as removed (they may race with another process) and do not
// short-circuit the sweep. A companion .sha256 sidecar is removed alongside
// the backup file; a NotExist error on the sidecar is ignored because a
// previous crashed backup may have left one without the other.
func Prune(dir string, now time.Time, keepDaily, keepWeekly, keepMonthly, keepCorrupt int) (kept, removed []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read backup directory: %w", err)
	}

	type candidate struct {
		name string
		ts   time.Time
	}
	var cands []candidate
	var corrupts []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ts, perr := ParseFilename(e.Name()); perr == nil {
			cands = append(cands, candidate{name: e.Name(), ts: ts})
			continue
		}
		// Quarantined backup? Strip the suffix and retry. Only a name that
		// parses AFTER stripping is a candidate, so genuine operator files
		// (README, manual exports) still fall through untouched — that
		// narrowness is the whole safety property of this branch.
		if stripped, ok := strings.CutSuffix(e.Name(), ".corrupt"); ok {
			if ts, perr := ParseFilename(stripped); perr == nil {
				corrupts = append(corrupts, candidate{name: e.Name(), ts: ts})
			}
		}
	}

	// Sort newest first so bucket-walking picks the most recent file in
	// each week/month, which is the GFS convention.
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].ts.After(cands[j].ts)
	})

	keepSet := make(map[string]bool, len(cands))

	// Daily bucket, recency half: the N most recent files, unconditionally.
	// Keeps intra-day restore points when the interval is sub-daily.
	for i, c := range cands {
		if i >= keepDaily {
			break
		}
		keepSet[c.name] = true
	}

	// Daily bucket, calendar half: the newest file per distinct calendar day,
	// up to keepDaily days. This is what makes the tier's coverage independent
	// of BACKUP_INTERVAL. Buckets are unioned, so adding it can only retain
	// more files — it can never delete something the previous policy kept.
	seenDay := make(map[[3]int]bool)
	daysKept := 0
	for _, c := range cands {
		if daysKept >= keepDaily {
			break
		}
		y, m, d := c.ts.Date()
		key := [3]int{y, int(m), d}
		if seenDay[key] {
			continue
		}
		seenDay[key] = true
		keepSet[c.name] = true
		daysKept++
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

	// Quarantine retention: keep the newest keepCorrupt samples for forensics,
	// delete the rest. Evidence needs one or two samples, not every sample
	// forever.
	//
	// A non-positive keepCorrupt falls back to the default rather than meaning
	// "keep none". In a backup subsystem the zero value of a struct field must
	// not silently destroy the only evidence of why backups are failing, and
	// every caller that genuinely wants a bound sets one (config defaults to
	// 2). The bound is what matters here; the exact number is not worth a
	// footgun.
	if keepCorrupt <= 0 {
		keepCorrupt = defaultKeepCorrupt
	}
	sort.Slice(corrupts, func(i, j int) bool { return corrupts[i].ts.After(corrupts[j].ts) })
	for i, c := range corrupts {
		if i < keepCorrupt {
			kept = append(kept, c.name)
			continue
		}
		path := filepath.Join(dir, c.name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			continue
		}
		_ = os.Remove(path + ".sha256")
		removed = append(removed, c.name)
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
