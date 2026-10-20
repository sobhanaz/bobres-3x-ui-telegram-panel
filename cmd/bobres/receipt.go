package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The install receipt: a small JSON file that records what installing BOBRES did to this
// server, so that `bobres uninstall --purge` can undo exactly that and nothing else. It is
// written by `bobres install` (so also by `bobres update`, which runs it) and by `bobres
// harden`, and it lives in the state directory (/var/lib/bobres, $BOBRES_STATE_DIR), outside
// the install directory the uninstall wipes: it is the last thing the uninstall deletes, so
// an interrupted uninstall can still be finished.
//
// It is additive and tolerant. A server installed by an older release has none: the uninstall
// then falls back to inspecting the server for the markers the installer wrote (comments, file
// names, labels) and takes the conservative choice wherever a marker cannot prove ownership.
// A key this release does not know is kept when the file is rewritten, so a receipt written by
// a newer bobres survives an older one. A receipt that cannot be read is treated as absent.
// Writing it never fails an install: a non-root run (a development or CI install) simply
// leaves none.
const (
	receiptFile   = "install-receipt.json"
	receiptSchema = 1
	// scriptMarkerDir is where install.sh records what it installed itself (Docker, openssl):
	// one text file per component, "package NAME", "file PATH" and "group NAME" lines. The
	// uninstall reads them directly too, so an install that stopped before `bobres install`
	// could write the receipt still tells which packages are BOBRES's.
	scriptMarkerDir = "installed"
	// maxReceiptBefore bounds the content of a file kept so that it can be put back.
	maxReceiptBefore = 64 << 10
)

// receipt is the install receipt.
type receipt struct {
	Schema     int           `json:"schema"`
	Created    time.Time     `json:"created"`
	Updated    time.Time     `json:"updated"`
	CLIVersion string        `json:"cli_version,omitempty"`
	InstallDir string        `json:"install_dir,omitempty"`
	Domain     string        `json:"domain,omitempty"`
	CLIPath    string        `json:"cli_path,omitempty"`
	Packages   []pkgGroup    `json:"packages,omitempty"`
	Firewall   *fwReceipt    `json:"firewall,omitempty"`
	Files      []fileReceipt `json:"files,omitempty"`
	Swap       *swapReceipt  `json:"swap,omitempty"`
	Images     *imgReceipt   `json:"images,omitempty"`

	// unknown holds the keys of a newer receipt, written back as they were.
	unknown map[string]json.RawMessage
}

// pkgGroup is a set of packages that one step of the installation added: Docker's (install.sh),
// openssl (install.sh), or one of the hardening step's (ufw, fail2ban, unattended-upgrades).
type pkgGroup struct {
	Name string    `json:"name"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	// Requested are the packages that were asked of apt; Packages is everything that was new
	// afterwards, dependencies included. Only an install of BOBRES's own makes either list, and
	// the uninstall purges a dependency only while apt still counts it as installed
	// automatically: one the operator installed on purpose since is theirs.
	Requested []string `json:"requested,omitempty"`
	Packages  []string `json:"packages"`
	// Files are the files the step added next to the packages (Docker's apt source and key),
	// SystemGroup the system group a package created.
	Files       []string `json:"files,omitempty"`
	SystemGroup string   `json:"system_group,omitempty"`
}

// fwReceipt is what the hardening did to ufw.
type fwReceipt struct {
	// WasActive: ufw was on before BOBRES's hardening first touched it; EnabledByBobres: that
	// hardening turned it on.
	WasActive       bool            `json:"was_active"`
	EnabledByBobres bool            `json:"enabled_by_bobres"`
	Rules           []fwRuleReceipt `json:"rules,omitempty"`
}

// fwRuleReceipt is a rule BOBRES added, in ufw's own words (`ufw show added`), without the
// comment; Preexisting: the same rule was already there, so it is not BOBRES's to delete.
type fwRuleReceipt struct {
	Spec        string `json:"spec"`
	Comment     string `json:"comment"`
	Preexisting bool   `json:"preexisting,omitempty"`
}

// fileReceipt is a file the hardening wrote: with Existed false BOBRES created it; with Existed
// true it replaced the file's content, which Before keeps (base64) so that it can be put back.
type fileReceipt struct {
	Path          string `json:"path"`
	Existed       bool   `json:"existed"`
	Before        string `json:"before_b64,omitempty"`
	BeforeUnknown bool   `json:"before_unknown,omitempty"`
	Wrote         string `json:"wrote_sha256"`
}

// swapReceipt is the swap file the hardening made.
type swapReceipt struct {
	Path       string    `json:"path"`
	SizeMB     int       `json:"size_mb"`
	FstabLine  string    `json:"fstab_line,omitempty"`
	FstabAdded bool      `json:"fstab_added"`
	At         time.Time `json:"at"`
}

// imgReceipt lists the image references the install pulled (every reference its compose file
// named, over all installs and updates) and which of them were on the server before.
type imgReceipt struct {
	Refs        []string `json:"refs"`
	Preexisting []string `json:"preexisting,omitempty"`
}

// receiptKeys are the JSON keys this release knows, read from the struct's tags.
var receiptKeys = func() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(receipt{})
	for i := range t.NumField() {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}()

type plainReceipt receipt

// UnmarshalJSON reads the known keys and keeps the others.
func (r *receipt) UnmarshalJSON(b []byte) error {
	var p plainReceipt
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	for k := range all {
		if receiptKeys[k] {
			delete(all, k)
		}
	}
	*r = receipt(p)
	r.unknown = all
	return nil
}

// MarshalJSON writes the known keys and the ones kept from a newer receipt.
func (r receipt) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(plainReceipt(r))
	if err != nil || len(r.unknown) == 0 {
		return b, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, v := range r.unknown {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// receiptStore reads and updates the receipt in a state directory. A nil store does nothing,
// which is what the tests that do not care about the receipt get.
type receiptStore struct {
	dir string
	mu  sync.Mutex
}

func newReceiptStore(stateDir string) *receiptStore {
	if strings.TrimSpace(stateDir) == "" {
		return nil
	}
	return &receiptStore{dir: stateDir}
}

func (s *receiptStore) path() string { return filepath.Join(s.dir, receiptFile) }

// load reads the receipt: an empty one when there is none; an empty one and an error when the
// file cannot be read or parsed (the caller decides whether to mention it).
func (s *receiptStore) load() (*receipt, error) {
	if s == nil {
		return &receipt{}, nil
	}
	b, err := os.ReadFile(s.path())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return &receipt{}, nil
	case err != nil:
		return &receipt{}, fmt.Errorf("read the install receipt: %w", err)
	}
	var r receipt
	if err := json.Unmarshal(b, &r); err != nil {
		return &receipt{}, fmt.Errorf("read the install receipt %s: %w", s.path(), err)
	}
	return &r, nil
}

// update reads the receipt, lets fn change it and writes it back in one rename (mode 0600). A
// receipt that does not parse is replaced. The error is for the caller to log: nothing may fail
// because of it.
func (s *receiptStore) update(fn func(r *receipt)) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _ := s.load() // an unreadable receipt is started over
	fn(r)
	now := time.Now().UTC().Truncate(time.Second)
	if r.Created.IsZero() {
		r.Created = now
	}
	r.Updated = now
	r.Schema = max(r.Schema, receiptSchema)
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil { //nolint:gosec // as install.sh makes the state directory
		return err
	}
	return writeAtomic(s.path(), append(b, '\n'), 0o600)
}

// setPackages records (or replaces) the package group of that name.
func (r *receipt) setPackages(g pkgGroup) {
	g.Requested, g.Packages, g.Files = uniqSorted(g.Requested), uniqSorted(g.Packages), uniqSorted(g.Files)
	for i := range r.Packages {
		if r.Packages[i].Name == g.Name {
			// Packages that an earlier run recorded stay recorded: a second run sees them as
			// installed already and would otherwise forget they were BOBRES's.
			g.Packages = uniqSorted(append(g.Packages, r.Packages[i].Packages...))
			g.Requested = uniqSorted(append(g.Requested, r.Packages[i].Requested...))
			g.Files = uniqSorted(append(g.Files, r.Packages[i].Files...))
			if g.SystemGroup == "" {
				g.SystemGroup = r.Packages[i].SystemGroup
			}
			if g.At.IsZero() || r.Packages[i].At.Before(g.At) {
				g.At = r.Packages[i].At
			}
			r.Packages[i] = g
			return
		}
	}
	r.Packages = append(r.Packages, g)
}

// group returns the package group of that name, if recorded.
func (r *receipt) group(name string) (pkgGroup, bool) {
	for _, g := range r.Packages {
		if g.Name == name {
			return g, true
		}
	}
	return pkgGroup{}, false
}

// file returns the record of a file the hardening wrote.
func (r *receipt) file(path string) (fileReceipt, bool) {
	for _, f := range r.Files {
		if f.Path == path {
			return f, true
		}
	}
	return fileReceipt{}, false
}

// recordFile notes that BOBRES is about to write path with data, and what the file holds now. The
// first record of a path is the one that counts: a later run of the hardening sees BOBRES's own
// earlier content and must not take it for the original.
func (r *receipt) recordFile(path string, before []byte, existed bool, data []byte) {
	sum := sha256.Sum256(data)
	wrote := hex.EncodeToString(sum[:])
	for i := range r.Files {
		if r.Files[i].Path == path {
			r.Files[i].Wrote = wrote
			return
		}
	}
	f := fileReceipt{Path: path, Existed: existed, Wrote: wrote}
	switch {
	case existed && len(before) > maxReceiptBefore:
		f.BeforeUnknown = true
	case existed:
		f.Before = base64.StdEncoding.EncodeToString(before)
	}
	r.Files = append(r.Files, f)
}

// recordUnknownBefore notes a file that already carries BOBRES's own words (an older release wrote
// it): what it held before cannot be known.
func (r *receipt) recordUnknownBefore(path string, data []byte) {
	r.recordFile(path, nil, true, data)
	for i := range r.Files {
		if r.Files[i].Path == path && r.Files[i].Before == "" {
			r.Files[i].BeforeUnknown = true
		}
	}
}

// beforeContent is the content a recorded file held before BOBRES wrote it.
func (f fileReceipt) beforeContent() ([]byte, bool) {
	if !f.Existed || f.BeforeUnknown {
		return nil, false
	}
	b, err := base64.StdEncoding.DecodeString(f.Before)
	if err != nil {
		return nil, false
	}
	return b, true
}

// addRule records a ufw rule of BOBRES's, once.
func (r *receipt) addRule(rule fwRuleReceipt) {
	if r.Firewall == nil {
		r.Firewall = &fwReceipt{}
	}
	for _, x := range r.Firewall.Rules {
		if x.Spec == rule.Spec {
			return
		}
	}
	r.Firewall.Rules = append(r.Firewall.Rules, rule)
}

// noteImages records the image references of an install about to pull them. A reference is
// preexisting when the image was on the server already and nothing of this install runs it: a
// first receipt on a server that has run BOBRES for a while must not take its own images for
// someone else's. have tells which references are present now, inUse which ones a container of
// the install runs.
func (r *receipt) noteImages(refs []string, have, inUse func(ref string) bool) {
	if r.Images == nil {
		r.Images = &imgReceipt{}
	}
	known := map[string]bool{}
	for _, x := range r.Images.Refs {
		known[x] = true
	}
	for _, ref := range refs {
		if known[ref] {
			continue
		}
		known[ref] = true
		r.Images.Refs = append(r.Images.Refs, ref)
		if have(ref) && !inUse(ref) {
			r.Images.Preexisting = append(r.Images.Preexisting, ref)
		}
	}
	r.Images.Refs, r.Images.Preexisting = uniqSorted(r.Images.Refs), uniqSorted(r.Images.Preexisting)
}

// uniqSorted returns the distinct non-empty strings of list, sorted.
func uniqSorted(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// importScriptMarkers adds the package groups install.sh recorded in dir/installed/ to r (the
// groups it records are replaced). A missing directory is no error.
func importScriptMarkers(dir string, r *receipt) {
	for _, g := range readScriptMarkers(dir) {
		if old, ok := r.group(g.Name); ok && old.By != "install.sh" {
			continue
		}
		r.setPackages(g)
	}
}

// readScriptMarkers parses install.sh's marker files.
func readScriptMarkers(stateDir string) []pkgGroup {
	entries, err := os.ReadDir(filepath.Join(stateDir, scriptMarkerDir))
	if err != nil {
		return nil
	}
	var groups []pkgGroup
	for _, e := range entries {
		if !e.Type().IsRegular() || !markerNameRe.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(stateDir, scriptMarkerDir, e.Name())) //nolint:gosec // install.sh's own state
		if err != nil {
			continue
		}
		g := pkgGroup{Name: e.Name(), By: "install.sh"}
		if fi, err := e.Info(); err == nil {
			g.At = fi.ModTime().UTC()
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			kind, val, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
			val = strings.TrimSpace(val)
			switch {
			case val == "" || strings.HasPrefix(kind, "#"):
			case kind == "package" && packageNameRe.MatchString(val):
				g.Packages = append(g.Packages, val)
			case kind == "file" && filepath.IsAbs(val):
				g.Files = append(g.Files, val)
			case kind == "group" && packageNameRe.MatchString(val):
				g.SystemGroup = val
			}
		}
		g.Requested = requestedPackages(g.Name, g.Packages)
		if len(g.Packages) > 0 {
			groups = append(groups, g)
		}
	}
	return groups
}

var (
	markerNameRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	packageNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.:_-]{0,100}$`)
)

// requestedPackages are the packages of a group that were asked of apt by name, as against the
// dependencies that came with them: Docker's script asks for docker-ce, docker-ce-cli,
// containerd.io and its plugins; install.sh for openssl.
func requestedPackages(group string, pkgs []string) []string {
	var out []string
	for _, p := range pkgs {
		switch group {
		case "docker":
			if strings.HasPrefix(p, "docker-") || p == "containerd.io" {
				out = append(out, p)
			}
		case "openssl":
			if p == "openssl" {
				out = append(out, p)
			}
		default:
			out = append(out, p)
		}
	}
	return out
}
