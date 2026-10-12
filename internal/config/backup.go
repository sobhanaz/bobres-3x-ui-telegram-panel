package config

import (
	"errors"
	"fmt"
	"log/slog"
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
	// S3 is the optional copy of each backup to S3-compatible storage
	// (BOBRES_BACKUP_S3_*; off without an endpoint). S3Err says why settings
	// that are there cannot be used: not a reason to stop backing up, so
	// LoadBackup does not fail on it, and each backup records it instead.
	S3    BackupS3
	S3Err error
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

	s3env := map[string]string{}
	for _, name := range BackupS3Keys {
		if name == "BOBRES_BACKUP_S3_ACCESS_KEY" || name == "BOBRES_BACKUP_S3_SECRET_KEY" {
			v, err := Secret(name)
			if err != nil {
				b.S3Err = err
			}
			s3env[name] = v
			continue
		}
		s3env[name] = getenv(name, "")
	}
	if b.S3Err == nil {
		b.S3, b.S3Err = ParseBackupS3(s3env)
	}

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

// BackupS3Keys are the .env names of the optional S3 upload's settings.
var BackupS3Keys = []string{
	"BOBRES_BACKUP_S3_ENDPOINT", "BOBRES_BACKUP_S3_BUCKET", "BOBRES_BACKUP_S3_REGION", "BOBRES_BACKUP_S3_PREFIX",
	"BOBRES_BACKUP_S3_ACCESS_KEY", "BOBRES_BACKUP_S3_SECRET_KEY", "BOBRES_BACKUP_S3_KEEP",
}

const (
	// DefaultS3Keep is how many backups the bucket keeps without
	// BOBRES_BACKUP_S3_KEEP.
	DefaultS3Keep = 30
	maxS3Keep     = 1000
)

// BackupS3 is the optional off-server copy of each backup: after a backup
// is written, its (already encrypted) file goes to Bucket under Prefix, and
// the bucket keeps the newest Keep.
type BackupS3 struct {
	// Endpoint is https://host[:port] (BOBRES_BACKUP_S3_ENDPOINT); empty = off.
	Endpoint string
	Bucket   string
	// Region signs the requests; empty = the client's default (us-east-1).
	Region string
	// Prefix is "" or a folder ending in "/" (shop/backups/).
	Prefix    string
	AccessKey string
	SecretKey string
	Keep      int
}

// Enabled reports whether backups are uploaded.
func (s BackupS3) Enabled() bool { return s.Endpoint != "" }

// Target is where the backups go, s3://bucket/prefix ("" when off).
func (s BackupS3) Target() string {
	if !s.Enabled() {
		return ""
	}
	return "s3://" + s.Bucket + "/" + s.Prefix
}

// LogValue never shows the keys.
func (s BackupS3) LogValue() slog.Value {
	return slog.GroupValue(slog.String("endpoint", s.Endpoint), slog.String("target", s.Target()),
		slog.String("region", s.Region), slog.Int("keep", s.Keep), slog.Bool("keys_set", s.AccessKey != "" && s.SecretKey != ""))
}

// String never shows the keys either.
func (s BackupS3) String() string { return fmt.Sprint(s.LogValue()) }

var (
	bucketRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	s3PrefixRe = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
)

// ValidBucket reports whether name is an S3 bucket name: 3 to 63 lower-case
// letters, digits, dots and hyphens, beginning and ending with a letter or
// digit.
func ValidBucket(name string) bool {
	return bucketRe.MatchString(name) && !strings.Contains(name, "..")
}

// ParseBackupS3 reads the BOBRES_BACKUP_S3_* settings from env (the backup
// service's environment, or an install's .env for bobres). Without an
// endpoint the upload is off and the rest is not checked. The errors name
// the settings, never a key's value.
func ParseBackupS3(env map[string]string) (BackupS3, error) {
	get := func(k string) string { return strings.TrimSpace(env[k]) }
	s := BackupS3{
		Endpoint:  strings.TrimRight(get("BOBRES_BACKUP_S3_ENDPOINT"), "/"),
		Bucket:    get("BOBRES_BACKUP_S3_BUCKET"),
		Region:    get("BOBRES_BACKUP_S3_REGION"),
		Prefix:    strings.Trim(get("BOBRES_BACKUP_S3_PREFIX"), "/"),
		AccessKey: get("BOBRES_BACKUP_S3_ACCESS_KEY"),
		SecretKey: get("BOBRES_BACKUP_S3_SECRET_KEY"),
		Keep:      DefaultS3Keep,
	}
	if s.Prefix != "" {
		s.Prefix += "/"
	}
	var errs []error
	if k := get("BOBRES_BACKUP_S3_KEEP"); k != "" {
		n, err := strconv.Atoi(k)
		if err != nil || n < 1 || n > maxS3Keep {
			errs = append(errs, fmt.Errorf("BOBRES_BACKUP_S3_KEEP must be a number from 1 to %d, got %q", maxS3Keep, k))
		} else {
			s.Keep = n
		}
	}
	if s.Endpoint == "" {
		return BackupS3{}, errors.Join(errs...)
	}
	if u, err := url.Parse(s.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(s.Endpoint, "?#") {
		errs = append(errs, errors.New("BOBRES_BACKUP_S3_ENDPOINT must be an https URL without a path or credentials (https://s3.example.com)"))
	}
	switch {
	case s.Bucket == "":
		errs = append(errs, errors.New("BOBRES_BACKUP_S3_BUCKET is required with BOBRES_BACKUP_S3_ENDPOINT"))
	case !ValidBucket(s.Bucket):
		errs = append(errs, fmt.Errorf("BOBRES_BACKUP_S3_BUCKET %q is not a bucket name (3-63 lower-case letters, digits, dots and hyphens)", s.Bucket))
	}
	if s.Region != "" && !regionRe.MatchString(s.Region) {
		errs = append(errs, fmt.Errorf("BOBRES_BACKUP_S3_REGION %q is not a region name (e.g. us-east-1, ir-thr-at1)", s.Region))
	}
	if !s3PrefixRe.MatchString(s.Prefix) || strings.Contains(s.Prefix, "//") || strings.Contains(s.Prefix, "..") || len(s.Prefix) > 200 {
		errs = append(errs, errors.New("BOBRES_BACKUP_S3_PREFIX may hold letters, digits, '.', '_', '-' and '/' only (e.g. shop/backups)"))
	}
	for _, k := range []struct{ name, v string }{
		{"BOBRES_BACKUP_S3_ACCESS_KEY", s.AccessKey}, {"BOBRES_BACKUP_S3_SECRET_KEY", s.SecretKey},
	} {
		switch {
		case k.v == "":
			errs = append(errs, fmt.Errorf("%s is required with BOBRES_BACKUP_S3_ENDPOINT", k.name))
		case strings.Contains(k.v, placeholderMarker):
			errs = append(errs, fmt.Errorf("%s still contains the %s placeholder", k.name, placeholderMarker))
		case strings.ContainsAny(k.v, " \t"):
			errs = append(errs, fmt.Errorf("%s must not contain spaces", k.name))
		}
	}
	if len(errs) > 0 {
		return BackupS3{}, errors.Join(errs...)
	}
	return s, nil
}
