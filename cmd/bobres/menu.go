package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
	"golang.org/x/term"
)

// menu is the management menu that `bobres` opens on a terminal when it is run
// without a command, in the spirit of the x-ui script. Every item runs the
// code of a command (status, logs, start, install...), so the menu itself only
// asks questions.
type menu struct {
	in     *bufio.Reader
	out    io.Writer
	errOut io.Writer
	dir    string
	ops    *ops
	// readHide reads a secret without echo; nil when stdin is not a terminal.
	readHide func(prompt string) (string, error)
	// install runs `bobres install` with extra environment values (the
	// secrets); quiet leaves out the first-steps summary.
	install func(args []string, env map[string]string, quiet bool) int
	doctor  func(args []string) int
	// shield runs fn with Ctrl+C reaching only the child process (live logs),
	// so it ends the logs and not the menu.
	shield func(fn func())
	color  bool
}

type menuItem struct {
	label string
	run   func()
	group bool // a separator line before this item
}

func menuCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("menu", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	br := bufio.NewReader(in)
	m := &menu{in: br, out: out, errOut: errOut, dir: *dir, ops: newOps(br, out, errOut)}
	if f, ok := in.(*os.File); ok {
		m.readHide = hiddenInput(f, out)
	}
	m.install = func(args []string, env map[string]string, quiet bool) int {
		ins := newInstaller(br, out, errOut)
		if env != nil {
			ins.getenv = func(k string) string { return env[k] }
		}
		ins.readHide, ins.quiet = m.readHide, quiet
		return ins.install(args)
	}
	m.doctor = func(args []string) int { return doctorCmd(args, out, errOut) }
	m.shield = func(fn func()) {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt) // caught, not ignored: the child still gets the default action
		defer signal.Stop(c)
		fn()
	}
	if f, ok := out.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		m.color = true
	}
	return m.loop()
}

// interactive reports whether both ends are a terminal (a person, not a script).
func interactive(in io.Reader, out io.Writer) bool {
	fi, ok1 := in.(*os.File)
	fo, ok2 := out.(*os.File)
	return ok1 && ok2 && term.IsTerminal(int(fi.Fd())) && term.IsTerminal(int(fo.Fd()))
}

func (m *menu) loop() int {
	for {
		env, installed, err := readEnv(filepath.Join(m.dir, ".env"))
		if err != nil {
			fmt.Fprintf(m.errOut, "error: %v\nThe install files are readable by root only: run  sudo bobres\n", err)
			return 1
		}
		items := m.items(env, installed)
		m.header(env, installed)
		for i, it := range items {
			if it.group {
				m.rule()
			}
			fmt.Fprintf(m.out, "  %s %s\n", m.paint("32", fmt.Sprintf("%2d.", i+1)), it.label)
		}
		m.rule()
		fmt.Fprintf(m.out, "  %s Exit\n", m.paint("32", " 0."))
		choice, ok := m.ask(fmt.Sprintf("\nChoose an option [0-%d]: ", len(items)))
		if !ok {
			fmt.Fprintln(m.out)
			return 0
		}
		switch choice {
		case "":
			continue
		case "0", "q", "exit":
			return 0
		}
		n, err := strconv.Atoi(choice)
		if err != nil || n < 1 || n > len(items) {
			fmt.Fprintf(m.out, "There is no option %q.\n", choice)
			continue
		}
		fmt.Fprintln(m.out)
		items[n-1].run()
		if _, ok := m.ask("\nPress Enter to return to the menu..."); !ok {
			fmt.Fprintln(m.out)
			return 0
		}
	}
}

func (m *menu) items(env map[string]string, installed bool) []menuItem {
	d := []string{"--dir", m.dir}
	if !installed {
		return []menuItem{
			{label: "Install BOBRES", run: func() { m.install(d, nil, false) }},
			{label: "Check this server", run: func() { m.doctor(nil) }},
		}
	}
	return []menuItem{
		{label: "Status", run: func() { m.ops.statusCmd(d) }},
		{label: "Live logs (Ctrl+C returns here)", run: func() {
			m.shield(func() { m.ops.logsCmd(append(d, "-f", "--tail", "100")) })
		}},
		{label: "Start", run: func() { m.ops.startCmd(d) }},
		{label: "Stop", run: m.stop},
		{label: "Restart", run: func() { m.ops.restartCmd(d) }},
		{label: "Show settings", run: func() { m.showSettings(env) }, group: true},
		{label: "Change the bot token", run: m.changeBotToken},
		{label: "Change the owner (admin) Telegram ID", run: m.changeAdmin},
		{label: "Change the 3x-ui panel (URL, API token, subscription link)", run: func() { m.changePanel(env) }},
		{label: "Dashboard login link (if the bot is down)", run: func() { m.ops.adminCmd([]string{"link", "--dir", m.dir}) }, group: true},
		{label: "Check this server", run: func() { m.doctor([]string{"--domain", env["BOBRES_DOMAIN"]}) }},
		{label: "Uninstall", run: func() { m.ops.uninstallCmd(d) }},
	}
}

func (m *menu) header(env map[string]string, installed bool) {
	fmt.Fprintf(m.out, "\n  %s  %s\n", m.paint("1", "BOBRES manager"), "bobres "+version.Version)
	if !installed {
		fmt.Fprintf(m.out, "  No install in %s yet.\n", m.dir)
		m.rule()
		return
	}
	fmt.Fprintf(m.out, "  %s  ·  %s  ·  version %s\n", m.dir, orDash(env["BOBRES_DOMAIN"]), orDash(env["BOBRES_VERSION"]))
	fmt.Fprintf(m.out, "  Store: %s\n", m.state())
	m.rule()
}

// state summarises the containers in one line.
func (m *menu) state() string {
	out, err := m.ops.run(context.Background(), m.dir, composeArgs("ps", "--all", "--format", "json")...)
	if err != nil {
		return m.paint("33", "unknown (is Docker running?)")
	}
	rows := parseServices(out)
	want := []string{"caddy", "postgres", "redis", "core", "payments", "provisioner", "bot"}
	up := 0
	for _, s := range want {
		if r, ok := rows[s]; ok && r.State == "running" && (r.Health == "" || r.Health == "healthy") {
			up++
		}
	}
	switch {
	case up == len(want):
		return m.paint("32", fmt.Sprintf("running (%d/%d services)", up, len(want)))
	case up == 0:
		return m.paint("31", "stopped")
	default:
		return m.paint("33", fmt.Sprintf("partly running (%d/%d services; see Status)", up, len(want)))
	}
}

func (m *menu) stop() {
	if !m.confirm("Stop BOBRES? The bot stops answering until you start it again. [y/N]: ") {
		fmt.Fprintln(m.out, "Cancelled.")
		return
	}
	m.ops.stopCmd([]string{"--dir", m.dir})
}

func (m *menu) showSettings(env map[string]string) {
	zp := "off"
	if id := env["BOBRES_ZARINPAL_MERCHANT_ID"]; id != "" {
		zp = "on (merchant " + mask(id) + ")"
	}
	rows := [][2]string{
		{"Install directory", m.dir},
		{"Version", env["BOBRES_VERSION"]},
		{"Domain", env["BOBRES_DOMAIN"]},
		{"Owner Telegram ID", env["BOBRES_ADMIN_TELEGRAM_ID"]},
		{"Bot token", mask(env["BOBRES_TELEGRAM_BOT_TOKEN"])},
		{"3x-ui panel", env["BOBRES_XUI_URL"]},
		{"3x-ui API token", mask(env["BOBRES_XUI_TOKEN"])},
		{"Subscription prefix", env["BOBRES_XUI_SUB_URL"]},
		{"Panel on a private net", env["BOBRES_XUI_ALLOW_PRIVATE"]},
		{"Zarinpal gateway", zp},
		{"Time zone", env["BOBRES_TIMEZONE"]},
	}
	for _, r := range rows {
		fmt.Fprintf(m.out, "  %-23s %s\n", r[0]+":", orDash(r[1]))
	}
	fmt.Fprintln(m.out, "\nPayment details (card, Zarinpal link, USDT), plans and texts are set in the bot: /admin and /set.")
}

func (m *menu) changeBotToken() {
	tok, ok := m.secret("New bot token from @BotFather (Enter to cancel): ")
	if !ok || tok == "" {
		fmt.Fprintln(m.out, "Cancelled.")
		return
	}
	if !botTokenRe.MatchString(tok) {
		fmt.Fprintln(m.errOut, "error: that does not look like a @BotFather token (123456789:AAH...). Nothing changed.")
		return
	}
	m.apply(nil, map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": tok})
}

func (m *menu) changeAdmin() {
	id, ok := m.ask("New owner Telegram user id, numbers only (ask @userinfobot; Enter to cancel): ")
	if !ok || id == "" {
		fmt.Fprintln(m.out, "Cancelled.")
		return
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
		fmt.Fprintln(m.errOut, "error: the Telegram id is a positive number. Nothing changed.")
		return
	}
	m.apply([]string{"--admin-id", id}, nil)
}

func (m *menu) changePanel(env map[string]string) {
	fmt.Fprintln(m.out, "Press Enter to keep a value.")
	u, ok := m.ask(fmt.Sprintf("3x-ui panel URL with its web base path [%s]: ", orDash(env["BOBRES_XUI_URL"])))
	if !ok {
		return
	}
	tok, ok := m.secret("3x-ui API token, Settings > Security [keep]: ")
	if !ok {
		return
	}
	sub, ok := m.ask(fmt.Sprintf("Subscription link prefix [%s]: ", orDash(env["BOBRES_XUI_SUB_URL"])))
	if !ok {
		return
	}
	var args []string
	if u != "" {
		args = append(args, "--xui-url", u)
		if strings.HasPrefix(strings.ToLower(u), "http://") && env["BOBRES_XUI_ALLOW_PRIVATE"] != "true" &&
			m.confirm("Plain http is only for a panel on this server or a private network. Is it? [y/N]: ") {
			args = append(args, "--xui-allow-private")
		}
	}
	if sub != "" {
		args = append(args, "--xui-sub-url", sub)
	}
	var secrets map[string]string
	if tok != "" {
		secrets = map[string]string{"BOBRES_XUI_TOKEN": tok}
	}
	if len(args) == 0 && secrets == nil {
		fmt.Fprintln(m.out, "Nothing changed.")
		return
	}
	m.apply(args, secrets)
}

// apply re-runs the installer with one change: it validates the new value
// (the bot token and panel URL online), rewrites the files keeping every
// secret, and recreates the services the change affects.
func (m *menu) apply(args []string, secrets map[string]string) {
	args = append([]string{"--dir", m.dir, "--skip-checks"}, args...)
	if m.install(args, secrets, true) == 0 {
		fmt.Fprintln(m.out, m.paint("32", "Saved. The change is live."))
		return
	}
	fmt.Fprintln(m.errOut, "The change did not go through; see the messages above.")
}

// ask reads one line; ok is false at the end of input.
func (m *menu) ask(prompt string) (string, bool) {
	fmt.Fprint(m.out, prompt)
	line, err := m.in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// secret reads a value without echo when stdin is a terminal.
func (m *menu) secret(prompt string) (string, bool) {
	if m.readHide == nil {
		return m.ask(prompt)
	}
	v, err := m.readHide(prompt)
	if err != nil {
		return "", false
	}
	return v, true
}

func (m *menu) confirm(prompt string) bool {
	a, ok := m.ask(prompt)
	return ok && (strings.EqualFold(a, "y") || strings.EqualFold(a, "yes"))
}

func (m *menu) rule() { fmt.Fprintln(m.out, "  "+strings.Repeat("─", 58)) }

// paint wraps s in an ANSI style when the output is a terminal.
func (m *menu) paint(code, s string) string {
	if !m.color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// mask shows enough of a secret to recognise it: a bot token keeps its public
// bot id, anything else its last four characters.
func mask(v string) string {
	switch {
	case v == "":
		return ""
	case botTokenRe.MatchString(v):
		id, _, _ := strings.Cut(v, ":")
		return id + ":••••"
	case len(v) <= 8:
		return "••••"
	default:
		return "••••" + v[len(v)-4:]
	}
}
