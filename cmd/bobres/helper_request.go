package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// The dashboard's update helper. The dashboard (core, in its container) must never get the Docker
// socket or any other power over the host, so it only drops a small request file into a directory
// of the install that only it can write, and a systemd path unit on the host (bobres-updater.path,
// see helper_units.go) runs `bobres update --from-request` as root, which reads the request with
// the care this file is about, runs the same update an operator would (signature check, backup,
// rollback on failure) and reports into status.json (helper_status.go). Protocol version 1:
//
//	<install dir>/run/update/request.json   written by core (a temp file, then a rename)
//	<install dir>/run/update/status.json    written by the helper, read by core
//
// core sees the directory as /run/bobres-update. Nothing else of the host is writable by core.
const (
	runDirName      = "run"
	updateDirName   = "update"
	requestFileName = "request.json"
	statusFileName  = "status.json"
	// coreUID is the user core runs as in its image (deploy/Dockerfile: USER 65532:65532): the owner
	// of the run/update directory and of every request the helper accepts.
	coreUID = 65532

	requestProtocol = 1
	// maxRequestBytes bounds a request file; a larger one is refused unread.
	maxRequestBytes = 2048
	// requestMaxAge and requestMaxFuture bound the request's time: a request is for now, and the
	// clock of core is this host's.
	requestMaxAge    = 15 * time.Minute
	requestMaxFuture = time.Minute
	// maxRequestBy is the longest user name a request may carry, in characters.
	maxRequestBy = 64
	// maxRequestVersion bounds a release version in a request.
	maxRequestVersion = 64

	// handledFileName remembers, in the install directory (root's), the ids of the requests that
	// were run, so a request file that comes back (a crash between reading and deleting it, a copy
	// kept by whoever can write the directory) runs once.
	handledFileName = ".updater-handled"
	handledKeep     = 24 * time.Hour
	handledMax      = 200
)

// updateRequest is a validated request.
type updateRequest struct {
	ID      string
	Channel string // stable or beta
	// Version is "" for the newest verified release of the channel, else an exact release.
	Version string
	By      string
	At      time.Time
}

// rawRequest is the JSON of a request. Every field is a pointer so that one that is missing is not
// taken for an empty one.
type rawRequest struct {
	V       *int    `json:"v"`
	ID      *string `json:"id"`
	Action  *string `json:"action"`
	Channel *string `json:"channel"`
	Version *string `json:"version"`
	By      *string `json:"by"`
	At      *string `json:"at"`
}

var (
	// requestIDRe is a UUID (any version from 1 to 8), in lower case.
	requestIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	// requestVersionRe is an exact release: X.Y.Z or X.Y.Z-label.N (semver 2.0 without build
	// metadata and without a leading v). It has no character a URL path or a shell would read.
	requestVersionRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
)

// errNoRequest says there is no request file.
var errNoRequest = errors.New("there is no request")

// shown quotes a value of a request for a message, cut short.
func shown(s string) string {
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40]) + "..."
	}
	return strconv.Quote(s)
}

// parseUpdateRequest checks a request's bytes strictly: one JSON object with exactly the protocol's
// fields and no others, every value of the form the protocol says, dated within a quarter of an
// hour before now and a minute after it. Anything else is refused with the reason.
func parseUpdateRequest(b []byte, now time.Time) (*updateRequest, error) {
	if len(b) > maxRequestBytes {
		return nil, fmt.Errorf("it is larger than %d bytes", maxRequestBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var raw rawRequest
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("it is not the JSON object of the protocol (%s)", firstLine(err.Error()))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("there is more in the file than the one JSON object")
	}
	for _, f := range []struct {
		name    string
		missing bool
	}{
		{"v", raw.V == nil}, {"id", raw.ID == nil}, {"action", raw.Action == nil}, {"channel", raw.Channel == nil},
		{"version", raw.Version == nil}, {"by", raw.By == nil}, {"at", raw.At == nil},
	} {
		if f.missing {
			return nil, fmt.Errorf("the field %q is missing", f.name)
		}
	}
	if *raw.V != requestProtocol {
		return nil, fmt.Errorf("protocol version %d is not %d", *raw.V, requestProtocol)
	}
	if *raw.Action != "update" {
		return nil, fmt.Errorf("the action %s is not update", shown(*raw.Action))
	}
	if !requestIDRe.MatchString(strings.ToLower(*raw.ID)) {
		return nil, fmt.Errorf("the id %s is not a UUID", shown(*raw.ID))
	}
	if *raw.Channel != "stable" && *raw.Channel != "beta" {
		return nil, fmt.Errorf("the channel %s is neither stable nor beta", shown(*raw.Channel))
	}
	if v := *raw.Version; v != "" && (len(v) > maxRequestVersion || !requestVersionRe.MatchString(v)) {
		return nil, fmt.Errorf("the version %s is not a release like 1.2.3 or 1.2.3-rc.1", shown(v))
	}
	if err := checkRequestBy(*raw.By); err != nil {
		return nil, err
	}
	at, err := time.Parse(time.RFC3339, *raw.At)
	if err != nil {
		return nil, fmt.Errorf("the time %s is not RFC 3339", shown(*raw.At))
	}
	switch age := now.Sub(at); {
	case age > requestMaxAge:
		return nil, fmt.Errorf("it is older than %d minutes", int(requestMaxAge/time.Minute))
	case -age > requestMaxFuture:
		return nil, errors.New("it is dated in the future")
	}
	return &updateRequest{ID: strings.ToLower(*raw.ID), Channel: *raw.Channel, Version: *raw.Version, By: *raw.By, At: at}, nil
}

// checkRequestBy holds the dashboard user's name to a plain, short line: it goes into a log.
func checkRequestBy(by string) error {
	switch {
	case by == "" || !utf8.ValidString(by):
		return errors.New("the user name is empty or not text")
	case utf8.RuneCountInString(by) > maxRequestBy:
		return fmt.Errorf("the user name is longer than %d characters", maxRequestBy)
	}
	for _, r := range by {
		// Control characters and line breaks would forge log lines; the right-to-left overrides and
		// isolates would make one read differently from what it says.
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return errors.New("the user name has a control character in it")
		}
	}
	return nil
}

// requestIDHint is the id of a request that was refused, when the file shows one that looks like an
// id ("" otherwise): the dashboard finds its request's outcome by it. Nothing but a UUID comes back.
func requestIDHint(b []byte) string {
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	if id := strings.ToLower(v.ID); requestIDRe.MatchString(id) {
		return id
	}
	return ""
}

// updateDirPath is the directory core and the helper share.
func updateDirPath(dir string) string { return filepath.Join(dir, runDirName, updateDirName) }

// checkUpdateDir looks at the shared directory before anything is read from it: run/ must be a real
// directory that only root (this user, in a test) can change, so that core cannot put another
// directory in place of update/, and update/ a real directory of root's or core's that no one else
// can write to.
func checkUpdateDir(dir string, uid int) error {
	me := os.Geteuid()
	check := func(path string, owners ...int) error {
		fi, err := os.Lstat(path)
		switch {
		case err != nil:
			return err
		case fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir():
			return fmt.Errorf("%s is not a directory (a link or a file in its place?)", path)
		case fi.Mode().Perm()&0o022 != 0:
			return fmt.Errorf("%s can be written by others (mode %04o)", path, fi.Mode().Perm())
		}
		if owner, ok := asRoot.owner(fi); ok && !slices.Contains(owners, owner) {
			return fmt.Errorf("%s belongs to uid %d", path, owner)
		}
		return nil
	}
	if err := check(filepath.Join(dir, runDirName), me); err != nil {
		return err
	}
	return check(updateDirPath(dir), me, uid)
}

// readRequestFile reads the request in updDir without trusting a byte of the directory, which core
// can change as it likes: the file is opened without following a link and without waiting for a
// pipe, and must be a regular file that uid owns (core: nobody else can make one in that directory)
// of at most maxRequestBytes. errNoRequest says there is none.
func readRequestFile(updDir string, uid int) ([]byte, error) {
	path := filepath.Join(updDir, requestFileName)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0) //nolint:gosec // opened without following links, then looked at through the descriptor
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, errNoRequest
	case errors.Is(err, syscall.ELOOP):
		return nil, errors.New("it is a symbolic link")
	case err != nil:
		return nil, fmt.Errorf("it cannot be opened (%w)", err)
	}
	defer f.Close() //nolint:errcheck // read-only
	fi, err := f.Stat()
	switch {
	case err != nil:
		return nil, err
	case !fi.Mode().IsRegular():
		return nil, errors.New("it is not a regular file")
	case fi.Size() > maxRequestBytes:
		return nil, fmt.Errorf("it is larger than %d bytes", maxRequestBytes)
	}
	if owner, ok := asRoot.owner(fi); !ok || owner != uid {
		return nil, fmt.Errorf("it is not owned by core (uid %d)", uid)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxRequestBytes {
		return nil, fmt.Errorf("it is larger than %d bytes", maxRequestBytes)
	}
	return b, nil
}

// takeRequest reads the request and removes it, whatever it was: a file left would start the helper
// again as long as it is there. A link is unlinked (never followed), and a directory put in its place
// is removed with what it holds, without following links.
func takeRequest(updDir string, uid int) ([]byte, error) {
	b, err := readRequestFile(updDir, uid)
	if errors.Is(err, errNoRequest) {
		return nil, err
	}
	removeEntry(filepath.Join(updDir, requestFileName))
	return b, err
}

// removeEntry removes a directory entry of whatever kind.
func removeEntry(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.RemoveAll(path)
	}
}

// markHandled records the id of a request that is about to run and says when it was run before. The
// record is the install directory's own file (root's: core cannot reach it) and keeps the ids of the
// last day, which is longer than a request stays valid.
func markHandled(dir, id string, now time.Time) (again bool, err error) {
	path := filepath.Join(dir, handledFileName)
	b, err := os.ReadFile(path) //nolint:gosec // root's file in the install directory
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var keep []string
	for _, line := range strings.Split(string(b), "\n") {
		stamp, seen, ok := strings.Cut(strings.TrimSpace(line), " ")
		secs, perr := strconv.ParseInt(stamp, 10, 64)
		if !ok || perr != nil || now.Sub(time.Unix(secs, 0)) > handledKeep {
			continue
		}
		if seen == id {
			again = true
		}
		keep = append(keep, stamp+" "+seen)
	}
	if again {
		return true, nil
	}
	keep = append(keep, strconv.FormatInt(now.Unix(), 10)+" "+id)
	if len(keep) > handledMax {
		keep = keep[len(keep)-handledMax:]
	}
	return false, writeAtomic(path, []byte(strings.Join(keep, "\n")+"\n"), 0o600)
}
