package backup

import (
	"fmt"
	"strings"
	"time"
)

// Backup filename shape: spendrop-YYYY-MM-DDTHHMMZ.db
//
// The format is ISO-8601-ish, UTC, minute-precision, and deliberately avoids
// colons. Colons are illegal in filenames on FAT/exFAT/NTFS, and while ext4
// tolerates them, the backup directory may end up on any of these volumes
// (bind mounts, NAS shares, cloud sync targets, USB drives). Rejecting colons
// up-front means the names round-trip cleanly through every filesystem
// SpenDrop is likely to meet.
//
// Go's time package treats the literal "Z" ambiguously in format strings
// (it is also the zone designator for RFC 3339), so we keep it as a literal
// suffix outside the Format call and reattach it with string concatenation.
const (
	filenamePrefix     = "spendrop-"
	filenameSuffix     = "Z.db"
	filenameTimeLayout = "2006-01-02T1504"
)

// FormatFilename renders t as a backup filename. t is normalized to UTC
// before formatting so callers in different timezones produce identical names
// for the same instant.
func FormatFilename(t time.Time) string {
	return filenamePrefix + t.UTC().Format(filenameTimeLayout) + filenameSuffix
}

// ParseFilename extracts the timestamp encoded in a backup filename. It
// rejects names that do not carry the spendrop prefix/suffix or whose
// timestamp portion is unparseable. The returned time is UTC.
//
// Prune uses the parseability predicate to decide which files in the backup
// directory are candidates for retention — any file ParseFilename rejects is
// left alone. Keeping the parser strict protects operator files that happen
// to live in the same directory.
func ParseFilename(name string) (time.Time, error) {
	if !strings.HasPrefix(name, filenamePrefix) {
		return time.Time{}, fmt.Errorf("backup: name %q missing prefix %q", name, filenamePrefix)
	}
	if !strings.HasSuffix(name, filenameSuffix) {
		return time.Time{}, fmt.Errorf("backup: name %q missing suffix %q", name, filenameSuffix)
	}
	mid := name[len(filenamePrefix) : len(name)-len(filenameSuffix)]
	t, err := time.ParseInLocation(filenameTimeLayout, mid, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("backup: name %q has unparseable timestamp: %w", name, err)
	}
	return t, nil
}
