package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/provisionerclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/redisx"
)

// Check statuses, as `bobres check` reads them.
const (
	checkPass = "PASS"
	checkWarn = "WARN"
	checkFail = "FAIL"
	checkSkip = "SKIP"
)

// checkResult is one line of `check --json`.
type checkResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// licenseWarnDays is how long before a license ends the check starts to warn.
const licenseWarnDays = 14

// coreChecker holds what the checks ask; each part is replaced in tests.
type coreChecker struct {
	pingDB    func(ctx context.Context) error
	pingRedis func(ctx context.Context) error
	// effective answers the license check (nil: skipped).
	effective func(ctx context.Context) (*domain.EffectiveLicense, error)
	// servers lists the 3x-ui servers with their last health check (nil:
	// the provisioner cannot be reached from here).
	servers domain.ServerLister
	// alerts lists the announced problems.
	alerts func(ctx context.Context) ([]*store.AlertState, error)
	now    func() time.Time
}

// checkCmd is `core check [--json]`: the checks only core can make (the
// database, Redis, the license, the 3x-ui servers, the watchdog's open
// problems). It prints a JSON array of {name,status,detail} with --json, one
// line per check otherwise, and exits 1 when a check failed.
// `bobres check` runs it inside the core container.
func checkCmd(args []string, out, errOut io.Writer) int {
	asJSON := false
	switch strings.Join(args, " ") {
	case "":
	case "--json":
		asJSON = true
	default:
		fmt.Fprintln(errOut, "usage: check [--json]")
		return 2
	}
	cfg, err := config.LoadCore()
	if err != nil {
		return writeChecks(out, asJSON, []checkResult{{"Core configuration", checkFail, "cannot be loaded: " + oneLine(err.Error())}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return writeChecks(out, asJSON, []checkResult{{"Database", checkFail, "cannot connect: " + oneLine(err.Error())}})
	}
	defer st.Close()
	dom := domain.New(st, domain.Config{OwnerTelegramID: cfg.AdminTelegramID, Timezone: cfg.Timezone,
		License: domain.LicenseConfig{Domain: cfg.Domain, SellerContact: cfg.SellerContact}})
	c := &coreChecker{
		pingDB:    func(c context.Context) error { return st.DB().Ping(c) },
		effective: dom.Effective,
		alerts:    func(c context.Context) ([]*store.AlertState, error) { return st.OpenAlerts(c, st.Conn()) },
		now:       time.Now,
	}
	if cfg.RedisURL != "" {
		c.pingRedis = func(c context.Context) error {
			rdb, err := redisx.New(c, cfg.RedisURL)
			if err != nil {
				return err
			}
			return rdb.Close()
		}
	}
	// The provisioner's gRPC connection is lazy: nothing is sent until the
	// list is asked for.
	if conn, err := grpcx.Dial(cfg.ProvisionerGRPCAddr, cfg.ProvisionerToken); err == nil {
		defer conn.Close()
		c.servers = provisionerclient.New(provisionerv1.NewProvisionerServiceClient(conn))
	}
	return writeChecks(out, asJSON, c.run(ctx))
}

// run makes every check.
func (c *coreChecker) run(ctx context.Context) []checkResult {
	rs := []checkResult{c.database(ctx), c.redis(ctx), c.license(ctx)}
	rs = append(rs, c.serverResults(ctx)...)
	rs = append(rs, c.alertResults(ctx))
	return rs
}

func (c *coreChecker) database(ctx context.Context) checkResult {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.pingDB(cctx); err != nil {
		return checkResult{"Database", checkFail, "core cannot reach it: " + oneLine(err.Error()) + " (bobres logs db)"}
	}
	return checkResult{"Database", checkPass, "core is connected"}
}

func (c *coreChecker) redis(ctx context.Context) checkResult {
	if c.pingRedis == nil {
		return checkResult{"Redis", checkSkip, "core has no Redis address"}
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.pingRedis(cctx); err != nil {
		return checkResult{"Redis", checkFail, "core cannot reach it: " + scrubURL(oneLine(err.Error())) + " (bobres logs redis)"}
	}
	return checkResult{"Redis", checkPass, "core is connected"}
}

// license: the license in effect and how long it lasts.
func (c *coreChecker) license(ctx context.Context) checkResult {
	const name = "License"
	if c.effective == nil {
		return checkResult{name, checkSkip, "not read"}
	}
	eff, err := c.effective(ctx)
	if err != nil {
		return checkResult{name, checkFail, "cannot be read: " + oneLine(err.Error())}
	}
	now := c.now()
	days := func(t time.Time) int { return int(t.Sub(now) / (24 * time.Hour)) }
	switch eff.Basis {
	case license.BasisLicense:
		if eff.License != nil && eff.License.Token != nil {
			end := eff.License.Token.ExpiresAt
			if d := days(end); d < licenseWarnDays {
				return checkResult{name, checkWarn, fmt.Sprintf("%s license ends in %d days: renew it (bobres license)", eff.Tier, max(d, 0))}
			}
			return checkResult{name, checkPass, fmt.Sprintf("%s license, ends %s", eff.Tier, end.UTC().Format("2006-01-02"))}
		}
		return checkResult{name, checkPass, eff.Tier + " license"}
	case license.BasisGrace:
		d := 0
		if eff.License != nil {
			d = max(days(eff.License.GraceEndsAt), 0)
		}
		return checkResult{name, checkFail, fmt.Sprintf("the %s license ran out and is in its grace period (%d days left): renew it (bobres license)", eff.Tier, d)}
	case license.BasisTrial:
		d := eff.Effective.TrialDaysLeft(now)
		if d < 7 {
			return checkResult{name, checkWarn, fmt.Sprintf("trial ends in %d days: get a license (bobres license)", d)}
		}
		return checkResult{name, checkPass, fmt.Sprintf("trial, %d days left", d)}
	}
	return checkResult{name, checkWarn, "free tier: the trial is over and no license is installed (bobres license)"}
}

// serverResults: one line for the 3x-ui servers, naming the ones that fail.
func (c *coreChecker) serverResults(ctx context.Context) []checkResult {
	const name = "3x-ui servers"
	if c.servers == nil {
		return []checkResult{{name, checkSkip, "the provisioner cannot be asked from here"}}
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	list, err := c.servers.ListServers(cctx)
	if err != nil {
		return []checkResult{{name, checkWarn, "the provisioner did not answer: " + oneLine(err.Error()) + " (bobres logs provisioner)"}}
	}
	h := list.Health()
	if h.Total == 0 {
		return []checkResult{{name, checkWarn, "no server is added or enabled yet (dashboard: Servers)"}}
	}
	if len(h.Down) == 0 {
		return []checkResult{{name, checkPass, fmt.Sprintf("%d of %d healthy", h.Healthy, h.Total)}}
	}
	var down []string
	for i := range list.Servers {
		sv := &list.Servers[i]
		if !sv.Enabled || sv.CheckedAt == 0 || sv.Healthy {
			continue
		}
		why := oneLine(sv.HealthError)
		if why == "" {
			why = "no answer"
		}
		down = append(down, sv.Name+": "+why)
	}
	return []checkResult{{name, checkFail, fmt.Sprintf("%d of %d healthy; down: %s", h.Healthy, h.Total, strings.Join(down, "; "))}}
}

// alertResults: the problems the watchdog has announced and not seen fixed.
func (c *coreChecker) alertResults(ctx context.Context) checkResult {
	const name = "Watchdog"
	if c.alerts == nil {
		return checkResult{name, checkSkip, "not read"}
	}
	list, err := c.alerts(ctx)
	if err != nil {
		return checkResult{name, checkWarn, "cannot be read: " + oneLine(err.Error())}
	}
	if len(list) == 0 {
		return checkResult{name, checkPass, "no open problem"}
	}
	parts := make([]string, 0, len(list))
	for _, a := range list {
		p := a.Key
		if a.Detail != "" {
			p += " (" + oneLine(a.Detail) + ")"
		}
		parts = append(parts, p)
	}
	return checkResult{name, checkWarn, fmt.Sprintf("%d open: %s (dashboard: Alerts)", len(list), strings.Join(parts, "; "))}
}

// writeChecks prints the results and gives the exit code: 1 when one failed.
func writeChecks(out io.Writer, asJSON bool, rs []checkResult) int {
	failed := false
	for _, r := range rs {
		failed = failed || r.Status == checkFail
	}
	if asJSON {
		_ = json.NewEncoder(out).Encode(rs)
	} else {
		for _, r := range rs {
			fmt.Fprintf(out, "%-5s %-14s %s\n", r.Status, r.Name, r.Detail)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// oneLine keeps the first line of an error, bounded.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// credsRe matches the user:password@ of a URL.
var credsRe = regexp.MustCompile(`://[^/\s@]*@`)

// scrubURL drops any user:password@ of a URL in an error, so a Redis
// password never reaches the output.
func scrubURL(s string) string { return credsRe.ReplaceAllString(s, "://") }
