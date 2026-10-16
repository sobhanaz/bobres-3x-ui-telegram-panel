package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// The HTTPS files of an install. caddy/Caddyfile is Caddy's main file (static,
// written by this CLI from the embedded copy). tls/ holds what Caddy imports
// (tls/global.caddy, tls/sites.caddy) and a manual certificate (tls/certs/):
// the tls-agent, which runs as 65532, writes it; Caddy reads it through group
// 65532. This CLI writes only the default snippets of automatic mode, and only
// when they are missing, so the first start works before the agent has run.
const (
	caddyDirName = "caddy"
	tlsDirName   = "tls"
	// tlsOwner is the tls-agent's uid and gid, and Caddy's group.
	tlsOwner = 65532
)

// rootCalls are what prepareTLS and writeIfMissing ask of the machine that
// only root can have answered: who runs this, who owns a file, and giving the
// directory to the agent. Tests replace them to run the root branches as an
// ordinary user.
type rootCalls struct {
	euid func() int
	// chown gives an open file or directory away (by its descriptor, never by
	// a path another user could swap for a link).
	chown func(f *os.File, uid, gid int) error
	// owner is the uid that owns fi (false when the platform does not say).
	owner func(fi os.FileInfo) (uid int, ok bool)
	// chownPath gives the directory at path away (the backups, which this CLI
	// has just made itself).
	chownPath func(path string, uid, gid int) error
}

var asRoot = rootCalls{
	euid:  os.Geteuid,
	chown: func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) },
	owner: func(fi os.FileInfo) (int, bool) {
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, false
		}
		return int(st.Uid), true
	},
	chownPath: os.Chown,
}

// prepareTLS creates tls/ and the default snippets for env's domain. As root
// the directory belongs to 65532:65532 (mode 0750); a run that is not root
// cannot chown, so the directory gets mode 0777 and a note says so (a
// development or CI install; a server is installed as root).
//
// The tls-agent owns tls/'s contents, so nothing in it is trusted: every path
// is looked at without following links, a file is created with O_EXCL and
// O_NOFOLLOW (a link, a pipe or a socket the agent left where one of the two
// default files goes is removed first, with a warning), and the
// directory is changed through its open descriptor. notes gets what the
// operator may want to know (the mode of tls/), warnings what they should (an
// entry that was removed).
func prepareTLS(dir string, env map[string]string, notes, warnings io.Writer) error {
	path := filepath.Join(dir, tlsDirName)
	d, err := openDir(path, 0o700)
	if err != nil {
		return err
	}
	defer d.Close() //nolint:errcheck // read-only descriptor
	if asRoot.euid() == 0 {
		if err := asRoot.chown(d, tlsOwner, tlsOwner); err != nil {
			return fmt.Errorf("chown %s: %w", path, err)
		}
		err = d.Chmod(0o750)
	} else {
		fmt.Fprintf(notes, "Note: %s is writable by everyone (mode 0777) because this install is not run as root: the tls-agent "+
			"(uid %d) could not write to it otherwise. Fine for development and CI; install a server with sudo.\n", path, tlsOwner)
		err = d.Chmod(0o777)
	}
	if err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	global, sites := defaultTLSFiles(env)
	for _, f := range []struct{ name, text string }{{tlsconf.GlobalFile, global}, {tlsconf.SitesFile, sites}} {
		removed, err := writeIfMissing(filepath.Join(path, f.name), []byte(f.text))
		if err != nil {
			return err
		}
		if removed != "" {
			fmt.Fprintf(warnings, "warning: %s was %s, not a file: removed, and the default written in its place. "+
				"The tls-agent owns that directory: if you did not do this, look at what runs as uid %d (bobres logs tls-agent).\n",
				filepath.Join(path, f.name), removed, tlsOwner)
		}
	}
	return nil
}

// prepareCaddy creates caddy/ (root's, mode 0755) and writes Caddy's main
// Caddyfile in it. The directory is mounted into the container, not the file:
// a bind-mounted file keeps its old inode when it is replaced by a rename.
func prepareCaddy(dir string) error {
	path := filepath.Join(dir, caddyDirName)
	d, err := openDir(path, 0o755)
	if err != nil {
		return err
	}
	defer d.Close() //nolint:errcheck // read-only descriptor
	if err := d.Chmod(0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// openDir returns the directory at path open for reading, made with mode when
// missing (its parent must exist: MkdirAll would walk through a link). A
// symbolic link, or anything that is not a directory, is refused.
func openDir(path string, mode os.FileMode) (*os.File, error) {
	fi, err := os.Lstat(path) //nolint:gosec // a directory of this install, checked for links right here
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(path, mode); err != nil { //nolint:gosec // as above
			return nil, fmt.Errorf("create %s: %w", path, err)
		}
	case err != nil:
		return nil, fmt.Errorf("check %s: %w", path, err)
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symbolic link: not following it (remove it and run bobres install again)", path)
	case !fi.IsDir():
		return nil, fmt.Errorf("%s exists and is not a directory", path)
	}
	d, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0) //nolint:gosec // a directory of this install
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return d, nil
}

// mkdirBelow makes the directories of rel (a path inside root) one by one,
// never through a link; root must exist. mode is for the ones it creates.
func mkdirBelow(root, rel string, mode os.FileMode) error {
	cur := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		d, err := openDir(cur, mode)
		if err != nil {
			return err
		}
		_ = d.Close()
	}
	return nil
}

// writeIfMissing creates path with data (mode 0644, root's) when nothing is
// there; an existing regular file stays as it is (the tls-agent's version is
// the current one), unless it is neither root's nor the agent's, which is
// refused. A directory is refused too. Anything else the agent could have
// put there (a symbolic link, a pipe, a socket, a device) would stop every
// install and update for as long as it stays, so it is unlinked, which never
// follows a link, and replaced; removed says what it was ("" when there was
// none). The file is made with O_EXCL and O_NOFOLLOW, so an entry that appears
// between the look and the write is not followed or overwritten: the whole
// step is tried again, a few times.
func writeIfMissing(path string, data []byte) (removed string, err error) {
	const attempts = 3
	for try := 1; ; try++ {
		fi, err := os.Lstat(path)
		switch {
		case err == nil && fi.Mode().IsRegular():
			if uid, ok := asRoot.owner(fi); ok && asRoot.euid() == 0 && uid != 0 && uid != tlsOwner {
				return removed, fmt.Errorf("%s belongs to uid %d, not to root or the tls-agent: remove it and run bobres install again", path, uid)
			}
			return removed, nil
		case err == nil && fi.IsDir():
			return removed, fmt.Errorf("%s is a directory: not touching it; remove it and run bobres install again", path)
		case err == nil:
			removed = entryKind(fi.Mode())
			if err := syscall.Unlink(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return removed, fmt.Errorf("remove %s (%s): %w", path, removed, err)
			}
		case !errors.Is(err, fs.ErrNotExist):
			return removed, fmt.Errorf("check %s: %w", path, err)
		}
		err = createExclusive(path, data)
		if errors.Is(err, fs.ErrExist) && try < attempts {
			continue
		}
		return removed, err
	}
}

// entryKind says what a directory entry that is no regular file is.
func entryKind(m os.FileMode) string {
	switch {
	case m&os.ModeSymlink != 0:
		return "a symbolic link"
	case m&os.ModeNamedPipe != 0:
		return "a pipe"
	case m&os.ModeSocket != 0:
		return "a socket"
	case m&os.ModeDevice != 0:
		return "a device file"
	}
	return "something that is not a file"
}

// createExclusive makes path (mode 0644) with data, failing if anything is
// there, a link included.
func createExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644) //nolint:gosec // inside this install's tls/
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644) // whatever the umask took away
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return syncDir(filepath.Dir(path))
}

// defaultTLSFiles are the snippets of automatic mode for env's domain (and its
// Zarinpal domain), as the tls-agent would write them. When the HTTPS engine
// refuses the domain (an IP address that is not public, for one) the files
// are put together here from values the installer has already checked, so a
// first start does not stop at an HTTPS setting the agent can still replace.
func defaultTLSFiles(env map[string]string) (global, sites string) {
	f, err := tlsconf.Render(tlsconf.State{Mode: tlsconf.ModeAuto, Domain: env["BOBRES_DOMAIN"], ZarinpalHost: env["BOBRES_ZARINPAL_HOST"]})
	if err == nil {
		return f.Global, f.Sites
	}
	return fallbackTLSFiles(env["BOBRES_DOMAIN"], env["BOBRES_ZARINPAL_HOST"])
}

// fallbackTLSFiles writes the sites for domain and zarinpal by hand. Both go
// into the files as text, so each must be a host name or an IPv4 address in
// the installer's own form; anything else gives files with no site at all
// (Caddy then serves nothing, rather than a broken configuration).
func fallbackTLSFiles(domain, zarinpal string) (global, sites string) {
	const header = "# Written by bobres install: the HTTPS manager cannot render this domain itself, so this is a plain\n" +
		"# automatic-mode setting. The tls-agent replaces it when core asks for one.\n"
	global, sites = header, header
	if !domainRe.MatchString(domain) {
		return global, sites
	}
	domain = strings.ToLower(domain)
	if net.ParseIP(domain) != nil {
		global += "default_sni " + domain + "\n"
	}
	sites += "\n" + domain + " {\n\timport " + tlsconf.SiteSnippet + "\n}\n"
	if zarinpal = strings.ToLower(zarinpal); zarinpal != "" && zarinpal != domain && domainRe.MatchString(zarinpal) && net.ParseIP(zarinpal) == nil {
		sites += "\n# The Zarinpal domain: only the pay page and the return URL.\n" + zarinpal + " {\n" +
			"\t@bobres_not_zarinpal {\n\t\tnot path /pay/* /webhooks/zarinpal /webhooks/idpay /webhooks/nextpay\n\t}\n" +
			"\thandle @bobres_not_zarinpal {\n\t\trespond \"Not found\" 404\n\t}\n" +
			"\timport " + tlsconf.SiteSnippet + "\n}\n"
	}
	return global, sites
}
