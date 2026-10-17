package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// probeTimeout bounds every probe of the status screen: a stopped or hung
// container must not hold the menu up. What a probe cannot answer in time is
// shown as "unknown".
const probeTimeout = 3 * time.Second

// Service states of a statusInfo.
const (
	svcRunning = "running"
	svcPartly  = "partly running"
	svcStopped = "stopped"
	svcUnknown = "unknown"
)

// statusInfo is what the status screen (the menu's header) and
// `bobres status --json` show: one value per line of the dashboard. A field a
// probe could not answer is nil/"unknown", never a guess.
type statusInfo struct {
	Installed bool   `json:"installed"`
	Dir       string `json:"dir"`
	Domain    string `json:"domain"`
	Version   string `json:"version"`
	// URL is the dashboard's address; "" without a domain.
	URL      string        `json:"dashboard_url"`
	Services serviceStatus `json:"services"`
	License  licenseStatus `json:"license"`
	HTTPS    httpsStatus   `json:"https"`
	Backup   backupStatus  `json:"backup"`
	// DiskFreeGB is nil when it could not be read.
	DiskFreeGB *float64 `json:"disk_free_gb"`
}

// serviceStatus counts the containers that are up.
type serviceStatus struct {
	// State is running, partly running, stopped or unknown (Docker did not answer).
	State string `json:"state"`
	Up    int    `json:"up"`
	Total int    `json:"total"`
	// Down names the services that are not running or not healthy.
	Down []string `json:"down"`
}

// licenseStatus is the license in effect, as core says.
type licenseStatus struct {
	// Known is false when core did not answer.
	Known bool `json:"known"`
	// Basis is license, grace, trial or free; Tier names it.
	Basis string `json:"basis,omitempty"`
	Tier  string `json:"tier,omitempty"`
	State string `json:"state,omitempty"` // the installed token's: missing, invalid, active, grace, expired
	// DaysLeft is nil for the free tier and when unknown; negative once over.
	DaysLeft *int `json:"days_left"`
}

// httpsStatus is the HTTPS mode and the certificate the domain serves.
type httpsStatus struct {
	Known bool `json:"known"`
	// Mode is auto, ip or manual ("" when core did not say).
	Mode     string `json:"mode,omitempty"`
	DaysLeft *int   `json:"cert_days_left"`
}

// backupStatus is the newest backup on this server.
type backupStatus struct {
	// Enabled: this install has the backup service.
	Enabled bool `json:"enabled"`
	// Newest is nil when there is no backup file.
	Newest    *time.Time `json:"newest,omitempty"`
	AgeHours  *float64   `json:"age_hours"`
	SizeBytes int64      `json:"size_bytes"`
}

// clock is the time of this ops (injectable).
func (o *ops) clock() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

// collectStatus gathers the dashboard. The probes run side by side, each with
// its own timeout (probeTimeout), so the whole takes about the longest one and
// never hangs: what does not answer is unknown.
func (o *ops) collectStatus(ctx context.Context, dir string, env map[string]string) statusInfo {
	info := statusInfo{Installed: true, Dir: dir, Domain: env["BOBRES_DOMAIN"], Version: env["BOBRES_VERSION"]}
	if info.Domain != "" {
		info.URL = "https://" + info.Domain + "/admin"
	}
	now := o.clock()
	var wg sync.WaitGroup
	probe := func(fn func(ctx context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			fn(pctx)
		}()
	}
	probe(func(c context.Context) { info.Services = o.serviceStatusOf(c, dir, env) })
	probe(func(c context.Context) { info.License = o.licenseStatusOf(c, dir, now) })
	probe(func(c context.Context) { info.HTTPS = o.httpsStatusOf(c, dir, env, now) })
	wg.Wait()
	info.Backup = backupStatusOf(dir, env, now)
	if o.freeGB != nil {
		if gb := o.freeGB(dir); gb >= 0 {
			info.DiskFreeGB = &gb
		}
	}
	return info
}

// serviceStatusOf asks Compose which services are up.
func (o *ops) serviceStatusOf(ctx context.Context, dir string, env map[string]string) serviceStatus {
	out, err := o.run(ctx, dir, composeArgs("ps", "--all", "--format", "json")...)
	if err != nil {
		return serviceStatus{State: svcUnknown, Down: []string{}}
	}
	return servicesOf(parseServices(out), allServices(dir, env))
}

// servicesOf judges the rows of compose ps against the services an install
// should run.
func servicesOf(rows map[string]serviceRow, want []string) serviceStatus {
	st := serviceStatus{Total: len(want), Down: []string{}}
	for _, s := range want {
		if r, ok := rows[s]; ok && r.State == "running" && (r.Health == "" || r.Health == "healthy") {
			st.Up++
		} else {
			st.Down = append(st.Down, s)
		}
	}
	switch st.Up {
	case st.Total:
		st.State = svcRunning
	case 0:
		st.State = svcStopped
	default:
		st.State = svcPartly
	}
	return st
}

// jsonObject is the JSON object in a command's output: Compose may print a
// warning before it.
func jsonObject(out []byte) []byte {
	i, j := bytes.IndexByte(out, '{'), bytes.LastIndexByte(out, '}')
	if i < 0 || j < i {
		return nil
	}
	return out[i : j+1]
}

// daysUntil is the whole days from now to t (negative once past).
func daysUntil(t, now time.Time) int {
	d := t.Sub(now)
	if d < 0 {
		return -int((-d + 24*time.Hour - 1) / (24 * time.Hour))
	}
	return int(d / (24 * time.Hour))
}

// licenseStatusOf asks core: `/app license info --json`.
func (o *ops) licenseStatusOf(ctx context.Context, dir string, now time.Time) licenseStatus {
	out, err := o.run(ctx, dir, composeArgs("exec", "-T", "core", "/app", "license", "info", "--json")...)
	if err != nil {
		return licenseStatus{}
	}
	return parseLicenseInfo(out, now)
}

// parseLicenseInfo reads core's `license info` JSON; it is not Known when the
// output is not that.
func parseLicenseInfo(out []byte, now time.Time) licenseStatus {
	var v struct {
		State     string `json:"state"`
		Effective struct {
			Basis         string `json:"basis"`
			Tier          string `json:"tier"`
			TrialDaysLeft int    `json:"trial_days_left"`
		} `json:"effective"`
		License *struct {
			ExpiresAt     int64  `json:"expires_at"`
			GraceEndsAt   int64  `json:"grace_ends_at"`
			LicenseEndsAt *int64 `json:"license_ends_at"`
		} `json:"license"`
	}
	raw := jsonObject(out)
	if raw == nil || json.Unmarshal(raw, &v) != nil || v.Effective.Basis == "" {
		return licenseStatus{}
	}
	st := licenseStatus{Known: true, Basis: v.Effective.Basis, Tier: v.Effective.Tier, State: v.State}
	days := func(unix int64) { d := daysUntil(time.Unix(unix, 0), now); st.DaysLeft = &d }
	switch {
	case v.Effective.Basis == "trial":
		d := v.Effective.TrialDaysLeft
		st.DaysLeft = &d
	case v.Effective.Basis == "grace" && v.License != nil:
		days(v.License.GraceEndsAt)
	case v.Effective.Basis == "license" && v.License != nil:
		// A renewed token names the day the license itself ends; the token's
		// own expiry is only when core asks for the next one.
		if v.License.LicenseEndsAt != nil {
			days(*v.License.LicenseEndsAt)
		} else {
			days(v.License.ExpiresAt)
		}
	}
	return st
}

// httpsStatusOf reads the mode from core (`ssl status --json`, where this
// install has the HTTPS manager) and the certificate expiry from what port 443
// of this host serves for the domain (what a visitor gets), else from core's
// own look.
func (o *ops) httpsStatusOf(ctx context.Context, dir string, env map[string]string, now time.Time) httpsStatus {
	var st httpsStatus
	var coreDays *int
	domain := strings.TrimSpace(env["BOBRES_DOMAIN"])
	var wg sync.WaitGroup
	if tlsAgentOn(dir, env) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := o.run(ctx, dir, composeArgs("exec", "-T", "core", "/app", "ssl", "status", "--json")...)
			if err == nil {
				st.Mode, coreDays = parseSSLMode(out)
			}
		}()
	}
	var hostDays *int
	if domain != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := o.probeLocal(ctx, domain); r.Served && r.NameOK {
				d := daysUntil(r.Info.NotAfter, now)
				hostDays = &d
			}
		}()
	}
	wg.Wait()
	st.DaysLeft = hostDays
	if st.DaysLeft == nil {
		st.DaysLeft = coreDays
	}
	st.Known = st.Mode != "" || st.DaysLeft != nil
	return st
}

// parseSSLMode reads the HTTPS mode, and the days the first domain address's
// certificate has left, out of `ssl status --json`.
func parseSSLMode(out []byte) (string, *int) {
	var v struct {
		State struct {
			Mode string `json:"mode"`
		} `json:"state"`
		Status struct {
			Addresses []struct {
				Role     string `json:"role"`
				Served   bool   `json:"served"`
				DaysLeft *int   `json:"days_left"`
			} `json:"addresses"`
		} `json:"status"`
	}
	raw := jsonObject(out)
	if raw == nil || json.Unmarshal(raw, &v) != nil {
		return "", nil
	}
	var days *int
	for _, a := range v.Status.Addresses {
		if a.Role == tlsconf.RoleDomain && a.Served && a.DaysLeft != nil {
			days = a.DaysLeft
			break
		}
	}
	return v.State.Mode, days
}

// backupStatusOf looks at <dir>/backups.
func backupStatusOf(dir string, env map[string]string, now time.Time) backupStatus {
	st := backupStatus{Enabled: backupEnabled(env)}
	b, ok := newestBackup(filepath.Join(dir, "backups"))
	if !ok {
		return st
	}
	when := backupTime(b)
	age := now.Sub(when).Hours()
	if age < 0 {
		age = 0
	}
	st.Newest, st.AgeHours, st.SizeBytes = &when, &age, b.Size()
	return st
}

// newestBackup is the newest backup file in dir.
func newestBackup(dir string) (os.FileInfo, bool) {
	if files := listBackups(dir); len(files) > 0 {
		return files[0], true
	}
	return nil, false
}

// backupTime is when a backup was made: the UTC time in its name
// (bobres-20261012T033000Z...), else the file's modification time.
func backupTime(fi os.FileInfo) time.Time {
	name := fi.Name()
	if i := strings.IndexAny(name, "0123456789"); i >= 0 && len(name) >= i+16 {
		if t, err := time.Parse("20060102T150405Z", name[i:i+16]); err == nil {
			return t
		}
	}
	return fi.ModTime()
}

// ---- rendering ----

// statusLines are the dashboard's lines, painted with paint (identity without
// colour), after the title and the install line.
func statusLines(s statusInfo, paint func(code, text string) string) []string {
	const (
		green, yellow, red = "32", "33", "31"
	)
	label := func(l string) string { return fmt.Sprintf("%-10s", l) }
	var lines []string

	// services
	var sv string
	switch s.Services.State {
	case svcRunning:
		sv = paint(green, fmt.Sprintf("running (%d/%d services)", s.Services.Up, s.Services.Total))
	case svcStopped:
		sv = paint(red, "stopped") + " (down: " + strings.Join(s.Services.Down, ", ") + ")"
	case svcPartly:
		sv = paint(yellow, fmt.Sprintf("partly running (%d/%d services)", s.Services.Up, s.Services.Total)) +
			" - down: " + strings.Join(s.Services.Down, ", ")
	default:
		sv = paint(yellow, "unknown (is Docker running?)")
	}
	lines = append(lines, label("Store")+sv)

	// license
	lines = append(lines, label("License")+licenseLine(s.License, paint))
	// HTTPS
	lines = append(lines, label("HTTPS")+httpsText(s.HTTPS, paint))
	// backup
	lines = append(lines, label("Backup")+backupText(s.Backup, paint))
	// dashboard
	if s.URL != "" {
		lines = append(lines, label("Dashboard")+s.URL+"   (bobres admin link: one-time login link)")
	} else {
		lines = append(lines, label("Dashboard")+"unknown (no domain set); bobres admin link prints a login link")
	}
	return lines
}

// plural is "1 day" / "5 days".
func plural(n int, unit string) string {
	if n == 1 || n == -1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func licenseLine(l licenseStatus, paint func(code, text string) string) string {
	if !l.Known {
		return paint("33", "unknown (core did not answer)")
	}
	tier := l.Tier
	if tier == "" {
		tier = l.Basis
	}
	left := func(prefix string) string {
		if l.DaysLeft == nil {
			return prefix
		}
		d := *l.DaysLeft
		switch {
		case d < 0:
			return paint("31", prefix+", expired "+plural(-d, "day")+" ago")
		case d <= 7:
			return paint("33", prefix+", "+plural(d, "day")+" left")
		}
		return paint("32", prefix+", "+plural(d, "day")+" left")
	}
	switch l.Basis {
	case "license":
		return left(tier + " license")
	case "grace":
		if l.DaysLeft != nil && *l.DaysLeft >= 0 {
			return paint("33", tier+" license expired, in its grace period: "+plural(*l.DaysLeft, "day")+" left")
		}
		return paint("31", tier+" license expired")
	case "trial":
		return left("trial")
	case "free":
		return "free tier"
	}
	return tier
}

func httpsText(h httpsStatus, paint func(code, text string) string) string {
	if !h.Known {
		return paint("33", "unknown")
	}
	mode := h.Mode
	if mode == "" {
		mode = "on"
	}
	if h.DaysLeft == nil {
		return mode + ", certificate expiry unknown"
	}
	d := *h.DaysLeft
	switch {
	case d < 0:
		return mode + ", " + paint("31", "certificate expired "+plural(-d, "day")+" ago")
	case d <= 7:
		return mode + ", " + paint("33", "certificate expires in "+plural(d, "day"))
	}
	return mode + ", certificate expires in " + plural(d, "day")
}

func backupText(b backupStatus, paint func(code, text string) string) string {
	if b.AgeHours == nil {
		if !b.Enabled {
			return "none (this install has no backup service; bobres backup)"
		}
		return paint("33", "none yet (bobres backup makes one)")
	}
	age := ageText(time.Duration(*b.AgeHours * float64(time.Hour)))
	text := fmt.Sprintf("newest %s (%s)", age, humanSize(b.SizeBytes))
	if *b.AgeHours > backupStaleHours {
		return paint("33", text)
	}
	return text
}

// backupStaleHours is how old the newest backup may be before it is flagged.
const backupStaleHours = 48

// ageText says how long ago in the largest unit.
func ageText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	}
	return plural(int(d.Hours()/24), "day") + " ago"
}

// ---- bobres status --json ----

// statusJSON prints the dashboard as one JSON object: the same collector as
// the menu's header. It exits 1 when a service is down or Docker does not
// answer, as the plain status does.
func (o *ops) statusJSON(dir string, env map[string]string) int {
	info := o.collectStatus(context.Background(), dir, env)
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %v\n", err)
		return 1
	}
	fmt.Fprintln(o.out, string(b))
	if info.Services.State != svcRunning {
		return 1
	}
	return 0
}
