package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

// The thresholds of `bobres check`.
const (
	// backupWarnAge: the newest backup is older than this (the backup runs
	// daily: two days means a night was missed).
	backupWarnAge = 48 * time.Hour
	// releaseWarnDays: the release's signature runs out within this.
	releaseWarnDays = 14
	// checkProbeTimeout bounds each probe that talks to a container or a server.
	checkProbeTimeout = 15 * time.Second
	// releaseTimeLayout is the date format of checksums.txt's header (UTC).
	releaseTimeLayout = "2006-01-02T15:04Z"
)

const checkUsage = "usage: bobres check [--dir D] [--json]"

// webhookInfo is what Telegram says about how the bot gets its updates.
type webhookInfo struct {
	// URL is the webhook address ("" for long polling).
	URL string
	// Pending is how many updates wait for the bot.
	Pending int
	// LastError is Telegram's last delivery error ("" for none).
	LastError string
}

// checker holds what `bobres check` looks at beyond the install's files, each
// replaceable in tests.
type checker struct {
	o *ops
	// getMe asks Telegram whose token this is (root "": api.telegram.org).
	getMe func(ctx context.Context, token, root string) (string, error)
	// webhook asks Telegram for the bot's webhook state.
	webhook func(ctx context.Context, token, root string) (webhookInfo, error)
	// stateDir holds the release state install.sh records.
	stateDir string
}

func newChecker(o *ops) *checker {
	return &checker{o: o, getMe: botUsername, webhook: telegramWebhook, stateDir: defaultStateDir()}
}

// checkCmd runs every check of the install: this host's side (settings,
// services, disk, HTTPS, backups, the bot token, the release signature) and
// core's (database, Redis, license, servers, alerts: `core check --json`).
// It prints a line per check and exits 1 when one FAILs.
func (o *ops) checkCmd(args []string) int { return newChecker(o).run(args) }

func (c *checker) run(args []string) int {
	o := c.o
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	asJSON := fs.Bool("json", false, "print the results as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(o.errOut, checkUsage)
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	results := c.all(context.Background(), *dir, env)
	pass, warn, fail, failed := Summarize(results)
	if *asJSON {
		type line struct {
			Name   string `json:"name"`
			Status Status `json:"status"`
			Detail string `json:"detail"`
		}
		doc := struct {
			Results []line `json:"results"`
			Pass    int    `json:"pass"`
			Warn    int    `json:"warn"`
			Fail    int    `json:"fail"`
		}{Results: []line{}, Pass: pass, Warn: warn, Fail: fail}
		for _, r := range results {
			doc.Results = append(doc.Results, line{r.Name, r.Status, r.Detail})
		}
		b, _ := json.MarshalIndent(doc, "", "  ")
		fmt.Fprintln(o.out, string(b))
	} else {
		for _, r := range results {
			fmt.Fprintf(o.out, "%-5s %-18s %s\n", r.Status, r.Name, r.Detail)
		}
		fmt.Fprintf(o.out, "\n%d passed, %d warnings, %d failed\n", pass, warn, fail)
	}
	if failed {
		return 1
	}
	return 0
}

// all runs the checks in the order they are shown.
func (c *checker) all(ctx context.Context, dir string, env map[string]string) []Result {
	o := c.o
	now := o.clock()
	rs := []Result{checkEnv(env), checkEnvMode(filepath.Join(dir, ".env"))}

	rs = append(rs, c.servicesResult(ctx, dir, env))
	gb := -1.0
	if o.freeGB != nil {
		gb = o.freeGB(dir)
	}
	rs = append(rs, checkDisk(gb))
	rs = append(rs, c.coreResults(ctx, dir)...)
	rs = append(rs, hostTLSResults(ctx, o, dir, env["BOBRES_DOMAIN"])...)
	rs = append(rs, checkBackupAge(listBackups(filepath.Join(dir, "backups")), now))
	rs = append(rs, checkReleaseExpiry(c.stateDir, now))
	rs = append(rs, c.botResults(ctx, env)...)
	return rs
}

// requiredSecrets are the generated values every install has (the installer's
// generated map) with the hex length each has: bytes*2.
func requiredSecrets() []string {
	keys := make([]string, 0, len(generated))
	for k := range generated {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checkEnv judges the install's .env: the values the stack cannot start
// without are there and shaped as the installer made them. It never prints a
// secret's value.
func checkEnv(env map[string]string) Result {
	var missing, bad []string
	for _, k := range requiredSecrets() {
		v := env[k]
		switch {
		case v == "":
			missing = append(missing, k)
		case strings.HasSuffix(k, "_KEY") || strings.Contains(k, "MASTER_KEY") || strings.HasPrefix(k, "BOBRES_TOKEN"):
			// Random bytes, hex-encoded: the length the installer made.
			if _, err := hex.DecodeString(v); err != nil || len(v) != generated[k]*2 {
				bad = append(bad, k+" (not the "+strconv.Itoa(generated[k]*2)+" hex characters the installer makes)")
			}
		}
	}
	if env["BOBRES_REDIS_URL"] == "" {
		missing = append(missing, "BOBRES_REDIS_URL")
	}
	switch d := env["BOBRES_DOMAIN"]; {
	case d == "":
		missing = append(missing, "BOBRES_DOMAIN")
	case !domainRe.MatchString(d):
		bad = append(bad, "BOBRES_DOMAIN (not a domain name)")
	}
	switch t := env["BOBRES_TELEGRAM_BOT_TOKEN"]; {
	case t == "":
		missing = append(missing, "BOBRES_TELEGRAM_BOT_TOKEN")
	case !botTokenRe.MatchString(t):
		bad = append(bad, "BOBRES_TELEGRAM_BOT_TOKEN (not shaped like a @BotFather token)")
	}
	switch id := env["BOBRES_ADMIN_TELEGRAM_ID"]; {
	case id == "":
		missing = append(missing, "BOBRES_ADMIN_TELEGRAM_ID")
	default:
		if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
			bad = append(bad, "BOBRES_ADMIN_TELEGRAM_ID (not a positive number)")
		}
	}
	if len(missing)+len(bad) == 0 {
		return Result{"Config", Pass, ".env has every required setting"}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing: "+strings.Join(missing, ", "))
	}
	if len(bad) > 0 {
		parts = append(parts, "invalid: "+strings.Join(bad, ", "))
	}
	return Result{"Config", Fail, strings.Join(parts, "; ") + " (bobres config list; sudo bobres install fills in missing secrets)"}
}

// checkEnvMode warns when .env can be read by others: it holds the secrets.
func checkEnvMode(path string) Result {
	fi, err := os.Stat(path)
	if err != nil {
		return Result{"Config permissions", Skip, "could not stat .env"}
	}
	if m := fi.Mode().Perm(); m&0o077 != 0 {
		return Result{"Config permissions", Warn, fmt.Sprintf(".env is mode %04o; it holds the secrets: chmod 600 %s", m, path)}
	}
	return Result{"Config permissions", Pass, ".env is readable by its owner only"}
}

// servicesResult is `docker compose ps` against the services this install runs.
func (c *checker) servicesResult(ctx context.Context, dir string, env map[string]string) Result {
	ctx, cancel := context.WithTimeout(ctx, checkProbeTimeout)
	defer cancel()
	out, err := c.o.run(ctx, dir, composeArgs("ps", "--all", "--format", "json")...)
	if err != nil {
		return Result{"Services", Fail, "docker compose ps failed (is Docker running? are you root?): " + firstLine(err.Error())}
	}
	return servicesVerdict(servicesOf(parseServices(out), allServices(dir, env)))
}

// servicesVerdict turns the service counts into a result.
func servicesVerdict(s serviceStatus) Result {
	if s.State == svcRunning {
		return Result{"Services", Pass, fmt.Sprintf("all %d services are running and healthy", s.Total)}
	}
	return Result{"Services", Fail, fmt.Sprintf("%d of %d running; not running or unhealthy: %s (bobres logs <service>)",
		s.Up, s.Total, strings.Join(s.Down, ", "))}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// checkBackupAge judges the newest backup (files newest first, as listBackups
// gives them): a Warn when it is over two days old, a Fail when there is none.
func checkBackupAge(files []os.FileInfo, now time.Time) Result {
	if len(files) == 0 {
		return Result{"Backup", Fail, "no backup on this server: run `sudo bobres backup`, and keep the backup key (bobres, Backup key) somewhere safe"}
	}
	age := max(now.Sub(backupTime(files[0])), 0)
	detail := fmt.Sprintf("newest is %s old (%s)", strings.TrimSuffix(ageText(age), " ago"), humanSize(files[0].Size()))
	if age > backupWarnAge {
		return Result{"Backup", Warn, detail + ": the nightly backup missed; see bobres logs backup, or run bobres backup"}
	}
	return Result{"Backup", Pass, detail}
}

// checkReleaseExpiry judges the signature of the release this server runs: a
// signed release stops being accepted at its "expires" date, and no update or
// reinstall works after it until a newer release is published. The header of
// the verified checksums.txt is read from the state directory, when it was
// kept there.
func checkReleaseExpiry(stateDir string, now time.Time) Result {
	const name = "Release signature"
	if stateDir == "" {
		return Result{name, Skip, "no release state on this server"}
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "checksums.txt")) //nolint:gosec // the release state, root's
	if err != nil {
		return Result{name, Skip, "no release manifest recorded in " + stateDir}
	}
	h := release.ParseHeader(b)
	if h.Expires == "" {
		return Result{name, Skip, "the recorded release manifest has no expiry"}
	}
	exp, err := time.Parse(releaseTimeLayout, h.Expires)
	if err != nil {
		return Result{name, Skip, "cannot read the release expiry " + strconv.Quote(h.Expires)}
	}
	left := exp.Sub(now)
	ver := ""
	if h.Version != "" {
		ver = " of " + h.Version
	}
	switch {
	case left <= 0:
		return Result{name, Fail, fmt.Sprintf("the signature%s expired on %s: publish or re-sign a newer release (updates and reinstalls are refused until then)", ver, h.Expires)}
	case left < releaseWarnDays*24*time.Hour:
		return Result{name, Warn, fmt.Sprintf("the signature%s expires %s (%s): publish or re-sign a newer release", ver, h.Expires, leftText(left))}
	}
	return Result{name, Pass, fmt.Sprintf("the signature%s is good until %s (%s)", ver, h.Expires, leftText(left))}
}

// botResults asks Telegram about the bot token in .env (getMe) and the way
// the bot gets its updates (getWebhookInfo). The token is never printed.
func (c *checker) botResults(ctx context.Context, env map[string]string) []Result {
	token := env["BOBRES_TELEGRAM_BOT_TOKEN"]
	if !botTokenRe.MatchString(token) {
		return []Result{{"Bot token", Skip, "no valid token in .env (see Config)"}}
	}
	root := telegramAPIRoot(env)
	via := ""
	if root != "" {
		via = " (through the Bot API server " + root + ")"
	}
	cctx, cancel := context.WithTimeout(ctx, checkProbeTimeout)
	defer cancel()
	name, err := c.getMe(cctx, token, root)
	if err != nil {
		return []Result{
			{"Bot token", Fail, "Telegram rejected the token or is unreachable" + via + ": " + scrub(err.Error(), token) + " (sudo bobres secrets rotate bot-token)"},
			{"Bot updates", Skip, "the token was not accepted"},
		}
	}
	rs := []Result{{"Bot token", Pass, "@" + name + via}}
	wctx, wcancel := context.WithTimeout(ctx, checkProbeTimeout)
	defer wcancel()
	w, err := c.webhook(wctx, token, root)
	return append(rs, webhookVerdict(w, err, token))
}

// webhookVerdict says how the bot gets its updates.
func webhookVerdict(w webhookInfo, err error, token string) Result {
	const name = "Bot updates"
	if err != nil {
		return Result{name, Skip, "could not ask Telegram: " + scrub(err.Error(), token)}
	}
	mode := "long polling (no webhook)"
	if w.URL != "" {
		mode = "webhook " + hostOf(w.URL)
	}
	switch {
	case w.LastError != "":
		return Result{name, Warn, mode + ", Telegram's last delivery error: " + w.LastError}
	case w.Pending > 100:
		return Result{name, Warn, fmt.Sprintf("%s, %d updates are waiting: is the bot running?", mode, w.Pending)}
	}
	return Result{name, Pass, mode}
}

// hostOf is a URL's host (what is shown of a webhook address: its path can
// hold a secret).
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "(address hidden)"
}

// scrub removes the token from a message: it must reach no output.
func scrub(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "<token>")
}

// telegramWebhook is getWebhookInfo at root ("": api.telegram.org). Its
// errors never carry the token (the address it is in is not repeated).
func telegramWebhook(ctx context.Context, token, root string) (webhookInfo, error) {
	if root == "" {
		root = "https://api.telegram.org"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(root, "/")+"/bot"+token+"/getWebhookInfo", http.NoBody)
	if err != nil {
		return webhookInfo{}, errors.New("bad Bot API address")
	}
	resp, err := (&http.Client{Timeout: checkProbeTimeout}).Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // without the request address, which holds the token
		}
		return webhookInfo{}, errors.New(scrub(err.Error(), token))
	}
	defer resp.Body.Close()
	var v struct {
		OK     bool `json:"ok"`
		Result struct {
			URL          string `json:"url"`
			Pending      int    `json:"pending_update_count"`
			LastErrorMsg string `json:"last_error_message"`
			LastErrorAt  int64  `json:"last_error_date"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil || !v.OK {
		return webhookInfo{}, fmt.Errorf("the Bot API answered %d %s", resp.StatusCode, scrub(v.Description, token))
	}
	w := webhookInfo{URL: v.Result.URL, Pending: v.Result.Pending}
	// An error from before the last day is history, not a state.
	if v.Result.LastErrorMsg != "" && time.Since(time.Unix(v.Result.LastErrorAt, 0)) < 24*time.Hour {
		w.LastError = v.Result.LastErrorMsg
	}
	return w, nil
}

// coreResult is one check of core's `check --json`.
type coreResult struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

// coreResults runs `/app check --json` in the core container: what only core
// can see (database, Redis, license, servers, alerts). A core that cannot be
// reached is one FAIL line, not a crash.
func (c *checker) coreResults(ctx context.Context, dir string) []Result {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.o.run(ctx, dir, composeArgs("exec", "-T", "core", "/app", "check", "--json")...)
	return parseCoreResults(out, err)
}

// parseCoreResults reads core's answer. An error that still came with a JSON
// array (core exits 1 when one check fails) is that array.
func parseCoreResults(out []byte, runErr error) []Result {
	if i, j := bytes.IndexByte(out, '['), bytes.LastIndexByte(out, ']'); i >= 0 && j > i {
		var list []coreResult
		if json.Unmarshal(out[i:j+1], &list) == nil && len(list) > 0 {
			rs := make([]Result, 0, len(list))
			for _, r := range list {
				switch r.Status {
				case Pass, Warn, Fail, Skip:
				default:
					r.Status = Warn
				}
				rs = append(rs, Result{r.Name, r.Status, r.Detail})
			}
			return rs
		}
	}
	why := "no answer"
	if runErr != nil {
		why = firstLine(runErr.Error())
	} else if s := firstLine(string(out)); s != "" {
		why = s
	}
	return []Result{{"Core", Fail, "core is not running or did not answer (bobres status; bobres logs core): " + why}}
}
