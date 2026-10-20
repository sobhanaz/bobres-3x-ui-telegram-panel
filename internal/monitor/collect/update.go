package collect

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// maxRestorePoint bounds the restore point file that is read (it is a few hundred bytes).
const maxRestorePoint = 64 << 10

// restorePointFile is where `bobres update` leaves its restore point, below the install directory
// (cmd/bobres: restorePointDir, restorePointName).
var restorePointFile = filepath.Join(".restore-point", "restore-point.json")

// lastUpdate reads the newest update from the restore point: only the two versions and the times,
// never the paths and files the restore point lists. Nil when there is no restore point (a fresh
// install, or one from before restore points) or it is not readable.
func (c *Collector) lastUpdate() *snapshot.LastUpdate {
	if c.InstallDir == "" {
		return nil
	}
	b, err := c.FS.ReadFile(filepath.Join(c.InstallDir, restorePointFile))
	if err != nil || len(b) > maxRestorePoint {
		return nil
	}
	var rp struct {
		From      string     `json:"from_version"`
		To        string     `json:"to_version"`
		CreatedAt time.Time  `json:"created_at"`
		UsedAt    *time.Time `json:"used_at"`
	}
	if json.Unmarshal(b, &rp) != nil || strings.TrimSpace(rp.To) == "" || rp.CreatedAt.IsZero() {
		return nil
	}
	u := &snapshot.LastUpdate{From: rp.From, To: rp.To, At: rp.CreatedAt.Unix()}
	if rp.UsedAt != nil && !rp.UsedAt.IsZero() {
		u.RolledBackAt = rp.UsedAt.Unix()
	}
	return u
}
