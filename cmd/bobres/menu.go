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
	"sort"
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
	// upd runs update and rollback.
	upd   *updater
	color bool
	ui    termUI
	// quit ends the menu after an item: bobres itself was replaced.
	quit bool
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
	m.upd = newUpdater(m.ops)
	m.shield = func(fn func()) {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt) // caught, not ignored: the child still gets the default action
		defer signal.Stop(c)
		fn()
	}
	// The banner once, when a person is at the terminal; the menu's own header
	// below it is drawn again for each choice.
	ui := detectUI(out, os.Getenv)
	m.color, m.ui = ui.fancy, ui
	if ui.tty && !bannerOff(os.Getenv) {
		printBanner(out, ui)
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
		if m.quit {
			return 0
		}
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
			{label: "About BOBRES (version, license, links)", run: m.about, group: true},
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
		{label: "Change the first 3x-ui panel (used until the dashboard has a server)", run: func() { m.changePanel(env) }},
		{label: "Back up now", run: func() { m.ops.backupCmd(d) }, group: true},
		{label: "Backup key", run: func() { m.backupKey(env) }},
		{label: "Restore a backup", run: m.restore},
		{label: "Update BOBRES (check for a new release)", run: m.update, group: true},
		{label: "Roll back the last update", run: m.rollback},
		{label: "Dashboard login link (if the bot is down)", run: func() { m.ops.adminCmd([]string{"link", "--dir", m.dir}) }, group: true},
		{label: "Reset admin password (sign in without Telegram)", run: func() { m.ops.adminCmd([]string{"password", "--dir", m.dir}) }},
		{label: "Check this server", run: func() { m.doctor([]string{"--dir", m.dir, "--domain", env["BOBRES_DOMAIN"]}) }},
		{label: "Check everything (server, settings, services, bot, backups, license)", run: func() { m.ops.checkCmd(d) }},
		{label: "HTTPS certificate", run: func() { m.https(env) }, group: true},
		{label: "Uninstall", run: func() { m.ops.uninstallCmd(d) }, group: true},
		{label: "About BOBRES (version, license, links)", run: m.about, group: true},
	}
}

// about is the About item: the information of bobres about (the menu has shown
// the banner already).
func (m *menu) about() { printAbout(m.out, m.dir, false) }

// https is the HTTPS certificate item: a short menu of its own whose choices
// are the commands of bobres ssl. One choice runs, then the main menu is back.
// An install that predates the HTTPS manager (tlsAgentOn says no) is told so
// instead: none of the choices could work there.
func (m *menu) https(env map[string]string) {
	if msg := predatesHTTPS(m.dir, env); msg != "" {
		fmt.Fprintf(m.errOut, "error: %s\n", msg)
		return
	}
	fmt.Fprintln(m.out, "HTTPS certificate")
	for i, label := range []string{
		"Status",
		"Domain (automatic certificate)",
		"Server IP address (Let's Encrypt, 6 days, renewed automatically)",
		"Your own certificate files",
	} {
		fmt.Fprintf(m.out, "  %s %s\n", m.paint("32", fmt.Sprintf("%2d.", i+1)), label)
	}
	fmt.Fprintf(m.out, "  %s Back\n", m.paint("32", " 0."))
	choice, ok := m.ask("\nChoose an option [0-4]: ")
	if !ok {
		return
	}
	fmt.Fprintln(m.out)
	switch choice {
	case "1":
		m.ops.sslCmd([]string{"status", "--dir", m.dir})
	case "2":
		m.ops.sslCmd([]string{"auto", "--dir", m.dir})
	case "3":
		m.httpsIP()
	case "4":
		m.httpsOwn()
	case "", "0":
	default:
		fmt.Fprintf(m.out, "There is no option %q.\n", choice)
	}
}

// httpsIP asks for the server's address (Enter: the detected one) and runs
// bobres ssl ip, which asks again when the address is not the detected one.
func (m *menu) httpsIP() {
	fmt.Fprintln(m.out, "Let's Encrypt must reach this server on ports 80 and 443 at that address (no NAT in front of it).")
	ip, ok := m.ask("The server's public IP address (Enter: detect it): ")
	if !ok {
		return
	}
	args := []string{"ip", "--dir", m.dir}
	if ip != "" {
		args = append(args, "--ip", ip)
	}
	m.ops.sslCmd(args)
}

// httpsOwn asks for the two files of an own certificate and runs bobres ssl
// manual.
func (m *menu) httpsOwn() {
	fmt.Fprintln(m.out, "The certificate must cover the domain. If the browser rejects it, sudo bobres ssl auto switches back.")
	cert, ok := m.ask("Certificate chain file, PEM, the server's certificate first (Enter to cancel): ")
	if !ok || cert == "" {
		fmt.Fprintln(m.out, "Cancelled.")
		return
	}
	key, ok := m.ask("Private key file, PEM, without a passphrase (Enter to cancel): ")
	if !ok || key == "" {
		fmt.Fprintln(m.out, "Cancelled.")
		return
	}
	m.ops.sslCmd([]string{"manual", "--cert", cert, "--key", key, "--dir", m.dir})
}

// beaver is BOBRES the mascot in plain ASCII, beside the header's lines.
var beaver = [...]string{" n____n ", "( o  o )", ` \_||_/ `}

// header is the menu's title and, beside the beaver, the status dashboard of
// the install (collectStatus: every probe has a timeout and shows "unknown"
// rather than hang).
func (m *menu) header(env map[string]string, installed bool) {
	lines := []string{m.paint("1", "BOBRES manager") + "  bobres " + version.Version}
	if installed {
		info := m.ops.collectStatus(context.Background(), m.dir, env)
		lines = append(lines, fmt.Sprintf("%s  ·  %s  ·  version %s", m.dir, orDash(env["BOBRES_DOMAIN"]), orDash(env["BOBRES_VERSION"])))
		lines = append(lines, statusLines(info, m.paint)...)
	} else {
		lines = append(lines, fmt.Sprintf("No install in %s yet.", m.dir))
	}
	fmt.Fprintln(m.out)
	for i := 0; i < max(len(beaver), len(lines)); i++ {
		art, text := strings.Repeat(" ", len(beaver[0])), ""
		if i < len(beaver) {
			art = beaver[i]
		}
		if i < len(lines) {
			text = lines[i]
		}
		fmt.Fprintln(m.out, strings.TrimRight("  "+art+"  "+text, " "))
	}
	m.rule()
}

func (m *menu) stop() {
	if !m.confirm("Stop BOBRES? The bot stops answering until you start it again. [y/N]: ") {
		fmt.Fprintln(m.out, "Cancelled.")
		return
	}
	m.ops.stopCmd([]string{"--dir", m.dir})
}

// backupState describes the backup schedule for the settings list.
func (m *menu) backupState(env map[string]string) string {
	if !backupEnabled(env) {
		return "off (this version has no backup service)"
	}
	return fmt.Sprintf("daily at %s, kept in %s", orDash(env["BOBRES_BACKUP_TIME"]), filepath.Join(m.dir, "backups"))
}

// backupKey shows BOBRES_BACKUP_KEY after a confirmation: whoever sees it can
// read every backup.
func (m *menu) backupKey(env map[string]string) {
	key := env["BOBRES_BACKUP_KEY"]
	if key == "" {
		fmt.Fprintln(m.out, "This install has no backup key yet: run  bobres install  to create one.")
		return
	}
	if !m.confirm("Show the backup key? Anyone who has it can read your backups. [y/N]: ") {
		fmt.Fprintln(m.out, "Cancelled.")
		return
	}
	fmt.Fprintf(m.out, "\n  %s\n\nStore this somewhere safe, away from this server: without it the backups cannot be read.\n", key)
}

// maxListedBackups is how many of the newest backups the restore item lists.
const maxListedBackups = 15

// restore lists the newest backups in <dir>/backups, takes one by number or
// any file by path (with the key file of another server), then runs
// bobres restore, which checks the file and asks before it changes anything.
func (m *menu) restore() {
	bdir := filepath.Join(m.dir, "backups")
	files := listBackups(bdir)
	if len(files) == 0 {
		fmt.Fprintf(m.out, "No backups in %s.\n", bdir)
	} else {
		fmt.Fprintln(m.out, "Backups on this server, newest first:")
		for i, f := range files {
			fmt.Fprintf(m.out, "  %2d. %s  (%s)\n", i+1, f.Name(), humanSize(f.Size()))
		}
	}
	choice, ok := m.ask("Choose a backup by its number, or type the path of a backup file (Enter to cancel): ")
	if !ok || choice == "" {
		fmt.Fprintln(m.out, "Cancelled.")
		return
	}
	var args []string
	if n, err := strconv.Atoi(choice); err == nil {
		if n < 1 || n > len(files) {
			fmt.Fprintf(m.out, "There is no backup %d.\n", n)
			return
		}
		args = []string{filepath.Join(bdir, files[n-1].Name())}
	} else {
		args = []string{choice}
		kf, ok := m.ask("Key file, for a backup made on another server (Enter: this install's backup key): ")
		if !ok {
			return
		}
		if kf != "" {
			args = append(args, "--key-file", kf)
		}
	}
	m.ops.restoreCmd(append(args, "--dir", m.dir))
}

// listBackups are the newest backup files in dir (regular files only: the
// backup service's user owns the folder), newest first.
func listBackups(dir string) []os.FileInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []os.FileInfo
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".bkp") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			files = append(files, fi)
		}
	}
	// The names carry the UTC time they were made: bobres-20261012T033000Z...
	sort.Slice(files, func(i, j int) bool { return files[i].Name() > files[j].Name() })
	if len(files) > maxListedBackups {
		files = files[:maxListedBackups]
	}
	return files
}

// update checks for a newer release (of the install's update channel) and,
// when there is one, runs bobres update, which asks
// before it changes anything. After an update the menu ends: the bobres
// running it was replaced.
func (m *menu) update() {
	newer, code := m.upd.checkNewer(context.Background(), m.dir, "", "")
	if code != 0 || !newer {
		return
	}
	fmt.Fprintln(m.out)
	if m.upd.updateCmd([]string{"--dir", m.dir}) == 0 {
		fmt.Fprintln(m.out, "\nbobres itself was updated: run  bobres  again for the new menu.")
		m.quit = true
	}
}

// rollback runs bobres rollback, which asks first. Afterwards the menu ends:
// the previous bobres is back.
func (m *menu) rollback() {
	if m.upd.rollbackCmd([]string{"--dir", m.dir}) == 0 {
		fmt.Fprintln(m.out, "\nThe previous bobres is back: run  bobres  again for its menu.")
		m.quit = true
	}
}

func (m *menu) showSettings(env map[string]string) {
	// The dashboard keeps the gateway settings once payments has its key:
	// .env only seeded them, so it is no guide to what is live.
	zp := "managed in the dashboard (Payment gateways)"
	if env["BOBRES_PAYMENTS_MASTER_KEY"] == "" {
		zp = "set in .env (re-run bobres install to manage it in the dashboard)"
	}
	rows := [][2]string{
		{"Install directory", m.dir},
		{"Version", env["BOBRES_VERSION"]},
		{"Domain", env["BOBRES_DOMAIN"]},
		{"Owner Telegram ID", env["BOBRES_ADMIN_TELEGRAM_ID"]},
		{"Bot token", mask(env["BOBRES_TELEGRAM_BOT_TOKEN"])},
		{"First 3x-ui panel", env["BOBRES_XUI_URL"]},
		{"3x-ui API token", mask(env["BOBRES_XUI_TOKEN"])},
		{"Subscription prefix", env["BOBRES_XUI_SUB_URL"]},
		{"Panel on a private net", env["BOBRES_XUI_ALLOW_PRIVATE"]},
		{"Zarinpal gateway", zp},
		{"Time zone", env["BOBRES_TIMEZONE"]},
		{"Backups", m.backupState(env)},
	}
	for _, r := range rows {
		fmt.Fprintf(m.out, "  %-23s %s\n", r[0]+":", orDash(r[1]))
	}
	fmt.Fprintln(m.out, "\nThe first panel only seeds an empty server list: servers are managed in the dashboard (Servers).")
	fmt.Fprintln(m.out, "Payment methods, plans and texts are set in the dashboard (/admin in the bot) or with /set.")
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
	fmt.Fprintln(m.out, "BOBRES registers this panel only while it has no server; once one exists, add and change")
	fmt.Fprintln(m.out, "servers in the dashboard (Servers). Press Enter to keep a value.")
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

// paint wraps s in an ANSI style when the output is a terminal that takes color.
func (m *menu) paint(code, s string) string {
	if !m.color {
		return s
	}
	// The menu's few styles are the palette's: green is teal, yellow is brown.
	r, ok := map[string]role{"32": roleTeal, "1": roleBold, "31": roleRed, "33": roleBrown}[code]
	if !ok || m.ui.color == colorNone {
		return "\033[" + code + "m" + s + "\033[0m"
	}
	return m.ui.paint(r, s)
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
