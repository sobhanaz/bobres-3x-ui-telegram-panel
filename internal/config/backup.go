package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BackupMasterKeys are the .env names of the services' master keys. Each
// backup carries the ones that are set, so the file and BOBRES_BACKUP_KEY are
// enough to restore.
var BackupMasterKeys = []string{"BOBRES_CORE_MASTER_KEY", "BOBRES_PROVISIONER_MASTER_KEY", "BOBRES_PAYMENTS_MASTER_KEY"}

// minBackupKeyLen matches internal/backup's MinKeyLen.
const minBackupKeyLen = 32

// Backup is the backup service configuration.
type Backup struct {
	Common
	// Key encrypts every backup (BOBRES_BACKUP_KEY). Without it the backups
	// cannot be read, so it is required and 32+ characters in every env.
	Key string
	// DumpURL is the database pg_dump reads, as the read-only svc_backup role
	// (BOBRES_BACKUP_DATABASE_URL).
	DumpURL string
	// Dir holds the backups and their sidecars (BOBRES_BACKUP_DIR).
	Dir string
	// Hour and Minute are the daily run time (BOBRES_BACKUP_TIME, HH:MM) in
	// Location (BOBRES_TIMEZONE).
	Hour, Minute int
	Location     *time.Location
	// CoreToken is the token core lists and starts backups with over HTTP
	// (BOBRES_TOKEN_CORE_BACKUP in .env): one for this service only, not
	// core's own service token, which payments, provisioner and the bot
	// accept. Empty = no HTTP API (the schedule still runs).
	CoreToken string
	// BotURL and ServiceToken (this service's own token, BOBRES_TOKEN_BACKUP
	// in .env) hand each backup to the bot for the owner's Telegram. Either
	// one empty = backups are kept on the server only.
	BotURL       string
	ServiceToken string
	// MasterKeys are the BackupMasterKeys that are set, by name; MissingKeys
	// names the others.
	MasterKeys  map[string]string
	MissingKeys []string
}

// LoadBackup reads the backup service configuration.
func LoadBackup() (Backup, error) {
	var errs []error
	keep := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	c, err := Load("backup")
	keep(err)
	b := Backup{Common: c, MasterKeys: map[string]string{}}

	b.Key, err = Secret("BOBRES_BACKUP_KEY")
	keep(err)
	switch {
	case err != nil:
	case len(b.Key) < minBackupKeyLen:
		keep(fmt.Errorf("BOBRES_BACKUP_KEY must be at least %d characters", minBackupKeyLen))
	case strings.Contains(b.Key, placeholderMarker):
		keep(fmt.Errorf("BOBRES_BACKUP_KEY still contains the %s placeholder", placeholderMarker))
	}

	b.DumpURL, err = Secret("BOBRES_BACKUP_DATABASE_URL")
	keep(err)
	if err == nil {
		keep(checkDumpURL(b.DumpURL))
	}

	b.Dir = strings.TrimSpace(getenv("BOBRES_BACKUP_DIR", "/backups"))
	b.Hour, b.Minute, err = ParseClock(getenv("BOBRES_BACKUP_TIME", "03:30"))
	keep(err)
	b.Location = time.UTC
	if tz := strings.TrimSpace(getenv("BOBRES_TIMEZONE", "")); tz != "" {
		if b.Location, err = time.LoadLocation(tz); err != nil {
			b.Location = time.UTC
			keep(fmt.Errorf("BOBRES_TIMEZONE: unknown time zone %q", tz))
		}
	}

	b.CoreToken, err = optionalToken("BOBRES_PEER_CORE_TOKEN", c.Env)
	keep(err)
	b.ServiceToken, err = optionalToken("BOBRES_SERVICE_TOKEN", c.Env)
	keep(err)
	keep(requireDistinct(map[string]string{"BOBRES_SERVICE_TOKEN": b.ServiceToken, "BOBRES_PEER_CORE_TOKEN": b.CoreToken}))
	b.BotURL, err = peerURL("BOBRES_BOT_URL", "", "http://bot:8080")
	keep(err)

	for _, name := range BackupMasterKeys {
		v, err := Secret(name)
		switch {
		case err != nil:
			keep(err)
		case v == "":
			b.MissingKeys = append(b.MissingKeys, name)
		case len(v) < 32:
			keep(fmt.Errorf("%s must be at least 32 characters", name))
		case strings.Contains(v, placeholderMarker):
			keep(fmt.Errorf("%s still contains the %s placeholder", name, placeholderMarker))
		default:
			b.MasterKeys[name] = v
		}
	}
	return b, errors.Join(errs...)
}

// checkDumpURL accepts a postgres:// URL without the placeholder. The error
// never quotes the URL: it holds a password.
func checkDumpURL(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("BOBRES_BACKUP_DATABASE_URL is required")
	}
	if strings.Contains(v, placeholderMarker) {
		return fmt.Errorf("BOBRES_BACKUP_DATABASE_URL still contains the %s placeholder", placeholderMarker)
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return errors.New("BOBRES_BACKUP_DATABASE_URL must be a postgres:// URL")
	}
	return nil
}

var clockRe = regexp.MustCompile(`^\d{1,2}:\d{2}$`)

// ParseClock reads a time of day as HH:MM (24-hour).
func ParseClock(s string) (hour, minute int, err error) {
	v := strings.TrimSpace(s)
	if !clockRe.MatchString(v) {
		return 0, 0, fmt.Errorf("BOBRES_BACKUP_TIME must be a time of day like 03:30, got %q", s)
	}
	h, m, _ := strings.Cut(v, ":")
	hour, _ = strconv.Atoi(h)
	minute, _ = strconv.Atoi(m)
	if hour > 23 || minute > 59 {
		return 0, 0, fmt.Errorf("BOBRES_BACKUP_TIME must be a time of day like 03:30, got %q", s)
	}
	return hour, minute, nil
}
