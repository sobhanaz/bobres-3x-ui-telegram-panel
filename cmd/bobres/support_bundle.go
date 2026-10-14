package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

const supportUsage = "usage: bobres support-bundle [--dir D] [--offline]"

const (
	// bundleLogLines is how many of the newest log lines each service gives.
	bundleLogLines = 300
	// maxBundleLog caps one service's log (its newest part is kept).
	maxBundleLog = 2 << 20
	// bundleCmdTimeout bounds each docker command, so a hung daemon cannot
	// hang the bundle that is meant to diagnose it.
	bundleCmdTimeout = 30 * time.Second
)

// bundler builds the support bundle. Its probes beyond Docker (the doctor,
// files under /proc and /etc, disk sizes) are replaceable in tests.
type bundler struct {
	*ops
	doctor   func(dir, domain string, offline bool) []Result
	readFile func(path string) ([]byte, error)
	// disk reports a file system's size and free space in GB (ok false when
	// unknown).
	disk func(path string) (total, free float64, ok bool)
}

func newBundler(o *ops) *bundler {
	return &bundler{ops: o, doctor: doctorResults, readFile: os.ReadFile, disk: diskGB}
}

// bundleFile is one text file of the bundle.
type bundleFile struct {
	name, text string
}

// supportBundleCmd writes a tar.gz for support into the install directory:
// versions, compose ps, the doctor's checks, the newest log lines of every
// service, the .env key names (never a value), disk, memory and the list of
// backups. Every text is redacted before it is written; the database, the
// backups and .env's values are never read into it. Exit 0 when the bundle is
// written, 1 when it could not be, 2 for usage errors.
func (b *bundler) supportBundleCmd(args []string) int {
	fs := flag.NewFlagSet("support-bundle", flag.ContinueOnError)
	fs.SetOutput(b.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	offline := fs.Bool("offline", false, "skip the doctor's network checks (DNS, public IP)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(b.errOut, supportUsage)
		return 2
	}
	env, ok := b.requireInstall(*dir)
	if !ok {
		return 2
	}
	fmt.Fprintln(b.out, "Collecting the support bundle (versions, status, checks, logs, disk)...")
	now := b.now().UTC()
	name := "bobres-support-" + now.Format("20060102T150405Z")
	c := &collector{b: b, dir: *dir, red: newRedactor(env)}

	c.add("versions.txt", c.versions(env))
	c.add("compose-ps.txt", c.docker("docker compose ps", composeArgs("ps", "--all")...))
	c.add("doctor.txt", c.doctorText(env, *offline))
	for _, s := range allServices(*dir, env) {
		c.add("logs/"+s+".log", c.logs(s))
	}
	c.add("env-keys.txt", c.envKeys(env))
	c.add("system.txt", c.system())
	c.add("backups.txt", c.backups())
	files := append([]bundleFile{{"README.txt", c.readme(now)}}, c.files...)

	data, err := tarGz(name, now, files)
	if err == nil {
		err = writeAtomic(filepath.Join(*dir, name+".tar.gz"), data, 0o600)
	}
	if err != nil {
		fmt.Fprintf(b.errOut, "error: the support bundle could not be written: %v\n", err)
		return 1
	}
	path := filepath.Join(*dir, name+".tar.gz")
	fmt.Fprintf(b.out, "\nSupport bundle: %s (%s, readable by root only)\n", path, sizeText(int64(len(data))))
	if len(c.missing) > 0 {
		fmt.Fprintf(b.out, "Some parts could not be collected (README.txt in the bundle says why): %s\n", strings.Join(c.missing, ", "))
	}
	fmt.Fprintf(b.out, `It holds no secret values, no database and no backups. Public IP addresses are replaced
with [IP-1], [IP-2]... and browsers' User-Agent is removed, but the logs can still show Telegram
ids, usernames, order ids, panel host names and which pages were opened when. Read it before you
send it:
  mkdir -p /root/support-bundle && tar -xzf %s -C /root/support-bundle && ls -R /root/support-bundle
Delete it once support is done: rm %s
`, path, path)
	return 0
}

// collector gathers the bundle's files, redacting each one, and notes the
// parts that could not be collected.
type collector struct {
	b       *bundler
	dir     string
	red     *redactor
	files   []bundleFile
	missing []string
}

func (c *collector) add(name, text string) {
	c.files = append(c.files, bundleFile{name, c.red.text(text)})
}

// docker runs one docker command (what names it for the report) and returns
// its output, or why it failed.
func (c *collector) docker(what string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), bundleCmdTimeout)
	defer cancel()
	out, err := c.b.run(ctx, c.dir, args...)
	if err != nil {
		c.missing = append(c.missing, what)
		return fmt.Sprintf("%s failed: %v\n%s\n", what, err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

func (c *collector) versions(env map[string]string) string {
	var w strings.Builder
	fmt.Fprintf(&w, "bobres CLI:      %s\n", version.Version)
	fmt.Fprintf(&w, "Install version: %s (BOBRES_VERSION)\n", orDash(env["BOBRES_VERSION"]))
	fmt.Fprintf(&w, "System:          %s (%s/%s)\n", c.osName(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&w, "Docker:          %s\n", strings.TrimSpace(c.docker("docker version", "version", "--format", "{{.Server.Version}}")))
	fmt.Fprintf(&w, "Docker Compose:  %s\n", strings.TrimSpace(c.docker("docker compose version", "compose", "version", "--short")))
	fmt.Fprintf(&w, "\nImages (docker compose images):\n%s", c.docker("docker compose images", composeArgs("images")...))
	return w.String()
}

// osName is the distribution's name from /etc/os-release.
func (c *collector) osName() string {
	b, err := c.b.readFile("/etc/os-release")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return "unknown"
}

func (c *collector) doctorText(env map[string]string, offline bool) string {
	var w strings.Builder
	w.WriteString("bobres doctor on this server. On a running install Caddy holds ports 80 and 443,\n" +
		"so those two checks fail here: that is expected.\n\n")
	results := c.b.doctor(c.dir, env["BOBRES_DOMAIN"], offline)
	for _, r := range results {
		fmt.Fprintf(&w, "%-5s %-18s %s\n", r.Status, r.Name, r.Detail)
	}
	pass, warn, fail, _ := Summarize(results)
	fmt.Fprintf(&w, "\n%d passed, %d warnings, %d failed\n", pass, warn, fail)
	return w.String()
}

func (c *collector) logs(service string) string {
	out := c.docker("logs of "+service,
		composeArgs("logs", "--no-color", "--timestamps", "--tail", strconv.Itoa(bundleLogLines), service)...)
	if len(out) > maxBundleLog {
		out = out[len(out)-maxBundleLog:]
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
		out = fmt.Sprintf("[older lines left out: over %s]\n%s", sizeText(maxBundleLog), out)
	}
	return out
}

// envKeys lists .env's keys and whether each has a value: never the value.
func (c *collector) envKeys(env map[string]string) string {
	var w strings.Builder
	w.WriteString("The keys of .env. Values are never included: \"set\" means the key has a value.\n")
	if st, err := os.Stat(filepath.Join(c.dir, ".env")); err == nil {
		fmt.Fprintf(&w, "File mode: %04o\n", st.Mode().Perm())
	}
	w.WriteString("\n")
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		state := "set"
		switch v := env[k]; {
		case v == "":
			state = "empty"
		case strings.Contains(v, "CHANGE_ME"):
			state = "placeholder (CHANGE_ME)"
		}
		fmt.Fprintf(&w, "%-34s %s\n", k, state)
	}
	return w.String()
}

func (c *collector) system() string {
	var w strings.Builder
	w.WriteString("Disk\n")
	for _, p := range []struct{ label, path string }{{"Install directory", c.dir}, {"Docker data", "/var/lib/docker"}} {
		if total, free, ok := c.b.disk(p.path); ok {
			fmt.Fprintf(&w, "  %-18s %s: %.1f GB free of %.1f GB\n", p.label, p.path, free, total)
		} else {
			fmt.Fprintf(&w, "  %-18s %s: not known\n", p.label, p.path)
		}
	}
	if n, size, err := dirSize(filepath.Join(c.dir, "backups")); err == nil {
		fmt.Fprintf(&w, "  %-18s %d files, %s\n", "Backups folder", n, sizeText(size))
	}
	w.WriteString("\nMemory\n")
	w.WriteString(c.memory())
	fmt.Fprintf(&w, "\nDocker disk use (docker system df)\n%s", c.docker("docker system df", "system", "df"))
	return w.String()
}

// memory reports RAM and swap from /proc/meminfo.
func (c *collector) memory() string {
	b, err := c.b.readFile("/proc/meminfo")
	if err != nil {
		return "  not known on this system\n"
	}
	var w strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch k {
		case "MemTotal", "MemAvailable", "SwapTotal", "SwapFree":
			var kb int64
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &kb); err == nil {
				fmt.Fprintf(&w, "  %-13s %d MB\n", k+":", kb/1024)
			}
		}
	}
	return w.String()
}

// backups lists the backups folder (names, sizes, and what each sidecar
// says) and the restore point. It never opens a backup file.
func (c *collector) backups() string {
	var w strings.Builder
	dir := filepath.Join(c.dir, "backups")
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		fmt.Fprintf(&w, "No backups folder (%s).\n", dir)
	case err != nil:
		fmt.Fprintf(&w, "The backups folder could not be read: %v\n", err)
	case len(entries) == 0:
		fmt.Fprintf(&w, "The backups folder %s is empty.\n", dir)
	default:
		fmt.Fprintf(&w, "Backups folder %s (the files themselves are not in this bundle):\n", dir)
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || !e.Type().IsRegular() {
				fmt.Fprintf(&w, "  %s (not a regular file)\n", e.Name())
				continue
			}
			fmt.Fprintf(&w, "  %-44s %10s  %s\n", e.Name(), sizeText(info.Size()), info.ModTime().UTC().Format(time.RFC3339))
			if strings.HasSuffix(e.Name(), ".json") {
				w.WriteString(sidecarText(filepath.Join(dir, e.Name())))
			}
		}
	}
	w.WriteString("\n" + restorePointText(c.dir))
	return w.String()
}

// sidecarText summarises a backup's sidecar. The backups folder belongs to
// the backup service's user, so a link there is never followed, and the file
// is checked again once open: swapped for a named pipe after backups() looked,
// it would block a plain open until a writer came (O_NONBLOCK returns at once).
func sidecarText(path string) string {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // a sidecar in the install's backups folder
	if err != nil {
		return fmt.Sprintf("    (not readable: %v)\n", err)
	}
	defer f.Close() //nolint:errcheck // read-only
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return "    (not a regular file)\n"
	}
	var s backup.Sidecar
	if err := json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&s); err != nil {
		return "    (not a sidecar)\n"
	}
	sent := "not sent to Telegram"
	if s.SentAt != nil {
		sent = "sent to Telegram " + s.SentAt.UTC().Format(time.RFC3339)
	}
	out := fmt.Sprintf("    made %s, %s, %s\n", s.CreatedAt.UTC().Format(time.RFC3339), sizeText(s.Size), sent)
	if s.TelegramError != "" {
		out += "    telegram: " + s.TelegramError + "\n"
	}
	switch {
	case s.UploadedAt != nil:
		out += "    copied to " + s.UploadedTo + " " + s.UploadedAt.UTC().Format(time.RFC3339) + "\n"
	case s.UploadError != "":
		out += "    s3: " + s.UploadError + "\n"
	}
	if s.Error != "" {
		out += "    error: " + s.Error + "\n"
	}
	return out
}

// restorePointText describes the last update's restore point, read without
// touching it (bobres rollback is the one that repairs or trusts it).
func restorePointText(dir string) string {
	b, err := os.ReadFile(filepath.Join(restorePointPath(dir), restorePointName)) //nolint:gosec // root's own file in the install
	if os.IsNotExist(err) {
		return "Restore point: none (no update has left one).\n"
	}
	if err != nil {
		return fmt.Sprintf("Restore point: not readable: %v\n", err)
	}
	var rp restorePoint
	if err := json.Unmarshal(b, &rp); err != nil {
		return fmt.Sprintf("Restore point: not readable: %v\n", err)
	}
	used := "not used"
	if rp.UsedAt != nil {
		used = "used " + rp.UsedAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("Restore point: the update from %s to %s, made %s, %s.\n",
		orDash(rp.FromVersion), orDash(rp.ToVersion), rp.CreatedAt.UTC().Format(time.RFC3339), used)
}

func (c *collector) readme(now time.Time) string {
	var w strings.Builder
	fmt.Fprintf(&w, `BOBRES support bundle, made %s by bobres %s (bobres support-bundle).

  versions.txt     bobres, the install's version, the system, Docker and the images
  compose-ps.txt   the containers and their state (docker compose ps)
  doctor.txt       bobres doctor on this server
  logs/*.log       the newest %d log lines of each service
  env-keys.txt     the keys of .env and whether each is set (never a value)
  system.txt       disk, memory and Docker's disk use
  backups.txt      the backups folder (names, sizes, sidecars) and the restore point

Not included: the database, the backups, the .env values or any other secret. Every text was
redacted before it was written: the install's own secret values wherever they appear,
passwords in addresses, bot tokens, Bearer tokens, key=value and JSON fields whose name says
token, key, secret, password and the like (the same names the services' logging hides), Zarinpal
merchant ids, and long hex or base64 strings (keys and tokens). Visitors' data too: browsers'
User-Agent, and every public IP address, replaced with [IP-1], [IP-2]... (the same address keeps
its number; private and Docker addresses stay). The logs can still show Telegram ids, usernames,
order ids, panel host names and which pages were opened when.
`, now.Format(time.RFC3339), version.Version, bundleLogLines)
	if len(c.missing) > 0 {
		w.WriteString("\nCould not be collected:\n")
		for _, m := range c.missing {
			w.WriteString("  - " + m + " (the file says why)\n")
		}
	}
	return w.String()
}

// tarGz packs files under the folder name, each readable by its owner only.
func tarGz(name string, mod time.Time, files []bundleFile) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o700, ModTime: mod}); err != nil {
		return nil, err
	}
	for _, f := range files {
		h := &tar.Header{Typeflag: tar.TypeReg, Name: name + "/" + f.name, Mode: 0o600, Size: int64(len(f.text)), ModTime: mod}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(tw, f.text); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// dirSize counts a folder's regular files and their total size (links are
// not followed).
func dirSize(dir string) (int, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	var n int
	var size int64
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil {
			n++
			size += info.Size()
		}
	}
	return n, size, nil
}

// diskGB reports a file system's size and free space in GB.
func diskGB(path string) (total, free float64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false
	}
	bs := float64(st.Bsize)
	return float64(st.Blocks) * bs / (1 << 30), float64(st.Bavail) * bs / (1 << 30), true
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ---- redaction ----

// redacted replaces every secret, as in internal/logging.
const redacted = "[REDACTED]"

// bundleSecretWords are internal/logging's sensitiveKeys, plus merchant (a
// Zarinpal merchant id) and agent (a visitor's User-Agent, which Caddy's
// access log has for every request): a key=value or JSON field whose name
// holds one of them loses its value.
var bundleSecretWords = []string{
	"token", "secret", "password", "passwd", "apikey", "api_key", "authorization", "auth",
	"cookie", "private", "bearer", "credential", "dsn", "database_url", "redis_url",
	"license", "signature", "key", "merchant", "agent",
}

var (
	secretKeyRe = `[A-Za-z0-9_.\-]*(?i:` + strings.Join(bundleSecretWords, "|") + `)[A-Za-z0-9_.\-]*`
	// "name": "value" in JSON, as the services log; escapedPairRe is the same
	// inside a JSON string (an error that quotes a request body).
	jsonPairRe    = regexp.MustCompile(`"(` + secretKeyRe + `)"(\s*:\s*)"((?:[^"\\]|\\.)*)"`)
	jsonListRe    = regexp.MustCompile(`"(` + secretKeyRe + `)"(\s*:\s*)\[((?:\s*"(?:[^"\\]|\\.)*"\s*,?)*)\s*\]`)
	escapedPairRe = regexp.MustCompile(`\\"(` + secretKeyRe + `)\\"(\s*:\s*)\\"((?:[^"\\]|\\[^"])*)\\"`)
	// name=value: slog's text format, .env lines, query strings, Postgres DSNs;
	// the value bare, quoted, single-quoted or quoted inside a JSON string.
	textPairRe = regexp.MustCompile(`\b(` + secretKeyRe + `)=(\\"(?:[^"\\]|\\[^"])*\\"|"(?:[^"\\]|\\.)*"|'[^']*'|[^\s"'&,;]*)`)
	// urlCredsRe is internal/logging's urlCreds: scheme://user:pass@.
	urlCredsRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@:]*:?[^\s/@]*@`)
	// A PEM block, a certificate or a key: the HTTPS files (tls/) and an own
	// certificate's key are never read into the bundle, and a block that
	// reached a log is dropped whole, whatever it holds.
	pemBlockRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]+-----.*?-----END [A-Z0-9 ]+-----`)
	// A Telegram bot token: the bot's id, a colon and 35 characters.
	botTokenShapeRe = regexp.MustCompile(`\d{5,15}:[A-Za-z0-9_-]{30,64}`)
	bearerRe        = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9\-._~+/]{8,}=*`)
	// HTTP headers that carry a credential (or a visitor's browser), up to the
	// end of the line or a quote.
	headerRe = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|(?:set-)?cookie|x-api-key|x-telegram-bot-api-secret-token|user-agent)(\s*:\s*)[^"\r\n]+`)
	// A UUID within a few characters after "merchant": a Zarinpal merchant id.
	// Other UUIDs (orders, users, services) are what support needs: kept.
	merchantIDRe = regexp.MustCompile(`(?i)(merchant.{0,40}?)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	longHexRe    = regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`)
	// A run of base64 (standard or URL) characters long enough to be a key.
	longRunRe = regexp.MustCompile(`[A-Za-z0-9+/_-]{32,}={0,2}`)
	// keptIDRe are what a long run may hold and still be no secret: UUIDs
	// (orders, users, services) and the stamps in backup names.
	keptIDRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|\d{8}T\d{6}Z`)
	// Candidates for an IP address; maskIPs keeps those netip accepts and that
	// stand on their own (1.2.3.4.5 is no address, nor is 10:00:00).
	ipv6Re = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:\.\d{1,3}){0,3}`)
	ipv4Re = regexp.MustCompile(`\d{1,3}(?:\.\d{1,3}){3}`)
)

// notSecretEnv are .env keys whose values are settings, not secrets, so the
// logs keep them (a domain is what support needs). Every other value is
// scrubbed wherever it appears.
var notSecretEnv = map[string]bool{
	"BOBRES_ENV": true, "BOBRES_LOG_LEVEL": true, "BOBRES_LOG_FORMAT": true, "BOBRES_VERSION": true,
	"BOBRES_DOMAIN": true, "BOBRES_PUBLIC_URL": true, "BOBRES_TIMEZONE": true, "BOBRES_BACKUP_TIME": true,
	"COMPOSE_PROFILES": true, "BOBRES_BACKUP_URL": true, "BOBRES_ADMIN_TELEGRAM_ID": true,
	"BOBRES_XUI_ALLOW_PRIVATE": true, "BOBRES_ZARINPAL_SANDBOX": true, "BOBRES_ZARINPAL_PUBLIC_URL": true,
	"BOBRES_ZARINPAL_HOST": true, "BOBRES_TELEGRAM_WEBHOOK_URL": true, "BOBRES_TELEGRAM_API_URL": true,
	"BOBRES_UPDATE_CHANNEL": true, "BOBRES_LICENSE_SERVER": true, "BOBRES_SELLER_CONTACT": true,
	"BOBRES_BACKUP_S3_ENDPOINT": true, "BOBRES_BACKUP_S3_BUCKET": true, "BOBRES_BACKUP_S3_REGION": true,
	"BOBRES_BACKUP_S3_PREFIX": true, "BOBRES_BACKUP_S3_KEEP": true, "BOBRES_BACKUP_NETWORK": true,
	// The images' public digests: support needs to see which ones run.
	"BOBRES_IMAGE_DIGEST_CORE": true, "BOBRES_IMAGE_DIGEST_BOT": true, "BOBRES_IMAGE_DIGEST_PAYMENTS": true,
	"BOBRES_IMAGE_DIGEST_PROVISIONER": true, "BOBRES_IMAGE_DIGEST_BACKUP": true,
}

// minLiteral is the shortest .env value scrubbed as it is: shorter ones
// ("true", "prod") would blank ordinary words.
const minLiteral = 6

// redactor scrubs the bundle's text: first the install's own secret values
// wherever they appear, then everything shaped like a secret, then public IP
// addresses (customers opening the pay page, staff on the dashboard).
type redactor struct {
	literals []string // longest first, so a URL goes before the password in it
	// ips numbers each public address in the order met, so support can still
	// tell that two lines came from the same address.
	ips map[netip.Addr]string
}

func newRedactor(env map[string]string) *redactor {
	r := &redactor{ips: map[netip.Addr]string{}}
	for k, v := range env {
		if v = strings.TrimSpace(v); !notSecretEnv[k] && len(v) >= minLiteral {
			r.literals = append(r.literals, v)
		}
	}
	sort.Slice(r.literals, func(i, j int) bool { return len(r.literals[i]) > len(r.literals[j]) })
	return r
}

func (r *redactor) text(s string) string {
	s = pemBlockRe.ReplaceAllString(s, redacted)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = r.line(l)
	}
	return strings.Join(lines, "\n")
}

func (r *redactor) line(s string) string {
	for _, v := range r.literals {
		s = strings.ReplaceAll(s, v, redacted)
	}
	s = urlCredsRe.ReplaceAllString(s, "${1}"+redacted+"@")
	s = botTokenShapeRe.ReplaceAllString(s, redacted)
	s = bearerRe.ReplaceAllString(s, "${1}${2}"+redacted)
	s = headerRe.ReplaceAllString(s, "${1}${2}"+redacted)
	s = replacePairs(escapedPairRe, s, `\"%s\"%s\"%s\"`)
	s = replacePairs(jsonPairRe, s, `"%s"%s"%s"`)
	s = replacePairs(jsonListRe, s, `"%s"%s["%s"]`)
	s = textPairRe.ReplaceAllStringFunc(s, func(m string) string {
		k, v, _ := strings.Cut(m, "=")
		if keepValue(strings.Trim(v, `\"'`)) {
			return m
		}
		return k + "=" + redacted
	})
	s = merchantIDRe.ReplaceAllString(s, "${1}"+redacted)
	s = longHexRe.ReplaceAllString(s, redacted)
	s = longRunRe.ReplaceAllStringFunc(s, scrubRun)
	// IPv6 first: an IPv4-mapped address (::ffff:198.51.100.7) is one address.
	s = r.maskIPs(ipv6Re, s)
	return r.maskIPs(ipv4Re, s)
}

// maskIPs replaces each public address re finds with [IP-n]. Private,
// loopback and link-local addresses (Docker's networks, the containers) stay:
// they belong to no visitor and support needs them.
func (r *redactor) maskIPs(re *regexp.Regexp, s string) string {
	var w strings.Builder
	last := 0
	for _, m := range re.FindAllStringIndex(s, -1) {
		i, j := m[0], m[1]
		if !standsAlone(s, i, j) {
			continue
		}
		a, err := netip.ParseAddr(s[i:j])
		if err != nil {
			continue
		}
		if a = a.Unmap(); !a.IsGlobalUnicast() || a.IsPrivate() {
			continue
		}
		label, ok := r.ips[a]
		if !ok {
			label = fmt.Sprintf("[IP-%d]", len(r.ips)+1)
			r.ips[a] = label
		}
		w.WriteString(s[last:i])
		w.WriteString(label)
		last = j
	}
	if last == 0 {
		return s
	}
	w.WriteString(s[last:])
	return w.String()
}

// standsAlone: s[i:j] is not part of a longer word or number (a version such
// as 1.2.3.4.5, a hex string, a time such as T10:00:00.123). A port after it
// (198.51.100.7:443) or a colon before it (from:198.51.100.7) is fine.
func standsAlone(s string, i, j int) bool {
	if i > 0 && (wordByte(s[i-1]) || s[i-1] == '.') {
		return false
	}
	if j < len(s) && (wordByte(s[j]) || s[j] == '.' && j+1 < len(s) && wordByte(s[j+1])) {
		return false
	}
	return true
}

func wordByte(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// replacePairs blanks the value of each name/value pair re finds (groups:
// name, separator, value), writing it back with format.
func replacePairs(re *regexp.Regexp, s, format string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		g := re.FindStringSubmatch(m)
		if keepValue(g[3]) {
			return m
		}
		return fmt.Sprintf(format, g[1], g[2], redacted)
	})
}

// keepValue: values that cannot carry a secret, as internal/logging keeps a
// boolean ("database_url_set=true" is what an operator needs to see).
func keepValue(v string) bool {
	switch strings.ToLower(v) {
	case "", "true", "false", "null", strings.ToLower(redacted):
		return true
	}
	return false
}

// scrubRun decides whether a long run of base64 characters is a secret. A
// run with a slash that reads as a path (/opt/bobres/backups/...) is judged
// segment by segment.
func scrubRun(run string) string {
	if strings.Contains(run, "/") && pathLike(run) {
		parts := strings.Split(run, "/")
		for i, p := range parts {
			if looksSecret(p) {
				parts[i] = redacted
			}
		}
		return strings.Join(parts, "/")
	}
	if looksSecret(run) {
		return redacted
	}
	return run
}

// pathLike: a segment of plain lower-case letters (opt, backups, api) is
// a path's, which random base64 almost never has.
func pathLike(s string) bool {
	for _, seg := range strings.Split(s, "/") {
		if len(seg) >= 2 && strings.Trim(seg, "abcdefghijklmnopqrstuvwxyz") == "" {
			return true
		}
	}
	return false
}

// looksSecret: 32 or more characters, letters and digits mixed, once the
// UUIDs and backup stamps in it (keptIDRe) are left out.
func looksSecret(s string) bool {
	for _, part := range keptIDRe.Split(strings.TrimRight(s, "="), -1) {
		if len(part) >= 32 && strings.ContainsAny(part, "0123456789") &&
			strings.ContainsAny(part, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return true
		}
	}
	return false
}
