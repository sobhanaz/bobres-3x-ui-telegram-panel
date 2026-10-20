package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Keep is how many backups retention keeps.
const Keep = 7

const stampLayout = "20060102T150405Z"

var nameRe = regexp.MustCompile(`^bobres-(\d{8}T\d{6}Z)\.(bkp|json)$`)

// FileName is the backup file name for t, e.g. bobres-20261011T033000Z.bkp.
func FileName(t time.Time) string {
	return "bobres-" + t.UTC().Format(stampLayout) + ".bkp"
}

// fileRe is a backup file as the backup service and `bobres restore` write it: bobres-<UTC time>.bkp,
// and with a tag after the time (before-restore, before-rotation) for a safety backup.
var fileRe = regexp.MustCompile(`^bobres-[0-9]{8}T[0-9]{6}Z(-[a-z][a-z0-9-]{0,39})?\.bkp$`)

// ValidFileName reports whether name is a backup file name and nothing else: no path, no dot
// segment, nothing a shell or a URL would read. It is what the dashboard's download and restore
// accept before they look at a file.
func ValidFileName(name string) bool { return fileRe.MatchString(name) }

// SidecarName is the .json sidecar name of a backup file name.
func SidecarName(file string) string {
	return strings.TrimSuffix(file, ".bkp") + ".json"
}

// Prune returns the names in a directory listing to delete so that only the
// newest keep backups remain. An old backup goes with its sidecar. Sidecars
// with no backup (records of failed runs) are capped at keep too, so they never
// push out a good backup and never pile up. Other files are ignored.
func Prune(names []string, keep int) []string {
	if keep < 0 {
		keep = 0
	}
	bkps := map[string]bool{}
	jsons := map[string]bool{}
	for _, n := range names {
		m := nameRe.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		if _, err := time.Parse(stampLayout, m[1]); err != nil {
			continue
		}
		if m[2] == "bkp" {
			bkps[m[1]] = true
		} else {
			jsons[m[1]] = true
		}
	}
	var withFile, orphans []string
	for s := range bkps {
		withFile = append(withFile, s)
	}
	for s := range jsons {
		if !bkps[s] {
			orphans = append(orphans, s)
		}
	}
	// The layout sorts as text in time order; newest first.
	sort.Sort(sort.Reverse(sort.StringSlice(withFile)))
	sort.Sort(sort.Reverse(sort.StringSlice(orphans)))

	var del []string
	for i, s := range withFile {
		if i < keep {
			continue
		}
		del = append(del, "bobres-"+s+".bkp")
		if jsons[s] {
			del = append(del, "bobres-"+s+".json")
		}
	}
	for i, s := range orphans {
		if i >= keep {
			del = append(del, "bobres-"+s+".json")
		}
	}
	return del
}

// Sidecar is the .json record written next to each backup.
type Sidecar struct {
	File          string     `json:"file"`
	Size          int64      `json:"size"`
	SHA256        string     `json:"sha256"`
	CreatedAt     time.Time  `json:"created_at"`
	SentAt        *time.Time `json:"sent_at"`
	TelegramError string     `json:"telegram_error,omitempty"`
	// UploadedAt is when the file was copied to S3-compatible storage and
	// UploadedTo where (s3://bucket/key); UploadError is why it was not. The
	// file stays here either way. All three are absent without an upload.
	UploadedAt  *time.Time `json:"uploaded_at,omitempty"`
	UploadedTo  string     `json:"uploaded_to,omitempty"`
	UploadError string     `json:"upload_error,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// ReadSidecar reads a sidecar file.
func ReadSidecar(path string) (Sidecar, error) {
	var s Sidecar
	b, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("backup: read sidecar: %w", err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("backup: decode sidecar %s: %w", filepath.Base(path), err)
	}
	return s, nil
}

// WriteSidecar writes a sidecar atomically: a temp file in the same
// directory, then a rename, so readers never see half a file.
func WriteSidecar(path string, s Sidecar) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: encode sidecar: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sidecar-*.tmp")
	if err != nil {
		return fmt.Errorf("backup: write sidecar: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("backup: write sidecar: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("backup: write sidecar: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("backup: write sidecar: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("backup: write sidecar: %w", err)
	}
	return nil
}
