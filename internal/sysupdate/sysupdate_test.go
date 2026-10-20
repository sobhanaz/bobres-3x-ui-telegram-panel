package sysupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const goodID = "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60"

var at0 = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func newDir(t *testing.T) *Dir {
	t.Helper()
	return NewDir(t.TempDir())
}

func writeFile(t *testing.T, d *Dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(d.Path(), name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRequestValidation(t *testing.T) {
	good, err := NewRequest(goodID, "stable", "0.7.0", "boss", at0)
	if err != nil {
		t.Fatal(err)
	}
	if good.V != 1 || good.Action != "update" || good.At != "2026-10-20T10:00:00Z" || good.By != "boss" {
		t.Fatalf("%+v", good)
	}
	for _, v := range []string{"", "0.7.0", "1.5.0-rc.10", "10.20.30-beta"} {
		if _, err := NewRequest(goodID, "beta", v, "boss", at0); err != nil {
			t.Errorf("version %q: %v", v, err)
		}
	}
	for name, mk := range map[string]func() (Request, error){
		"upper-case id":   func() (Request, error) { return NewRequest(strings.ToUpper(goodID), "stable", "0.7.0", "b", at0) },
		"not an id":       func() (Request, error) { return NewRequest("abc", "stable", "0.7.0", "b", at0) },
		"empty id":        func() (Request, error) { return NewRequest("", "stable", "0.7.0", "b", at0) },
		"no channel":      func() (Request, error) { return NewRequest(goodID, "", "0.7.0", "b", at0) },
		"unknown channel": func() (Request, error) { return NewRequest(goodID, "nightly", "0.7.0", "b", at0) },
		"v-prefixed":      func() (Request, error) { return NewRequest(goodID, "stable", "v0.7.0", "b", at0) },
		"build metadata":  func() (Request, error) { return NewRequest(goodID, "stable", "0.7.0+x", "b", at0) },
		"a flag":          func() (Request, error) { return NewRequest(goodID, "stable", "--channel beta", "b", at0) },
		"a path":          func() (Request, error) { return NewRequest(goodID, "stable", "../0.7.0", "b", at0) },
		"a url":           func() (Request, error) { return NewRequest(goodID, "stable", "https://evil.example/x", "b", at0) },
		"a huge version": func() (Request, error) {
			return NewRequest(goodID, "stable", "1.0.0-"+strings.Repeat("a", 80), "b", at0)
		},
		"two-part version": func() (Request, error) { return NewRequest(goodID, "stable", "0.7", "b", at0) },
	} {
		if r, err := mk(); err == nil {
			t.Errorf("%s was accepted: %+v", name, r)
		}
	}
	// the fields of a request someone built by hand are checked the same way
	for name, edit := range map[string]func(r *Request){
		"version 2":     func(r *Request) { r.V = 2 },
		"action":        func(r *Request) { r.Action = "rollback" },
		"empty by":      func(r *Request) { r.By = "" },
		"long by":       func(r *Request) { r.By = strings.Repeat("x", 65) },
		"by with a tab": func(r *Request) { r.By = "a\tb" },
		"local time":    func(r *Request) { r.At = "2026-10-20T13:30:00+03:30" },
		"no time":       func(r *Request) { r.At = "" },
		"date only":     func(r *Request) { r.At = "2026-10-20" },
	} {
		r := good
		edit(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestRequestMarshal(t *testing.T) {
	r, _ := NewRequest(goodID, "stable", "0.7.0", "boss", at0)
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"id":"` + goodID + `","action":"update","channel":"stable","version":"0.7.0","by":"boss","at":"2026-10-20T10:00:00Z"}` + "\n"
	if string(data) != want {
		t.Fatalf("got  %s\nwant %s", data, want)
	}
	// "" asks for the newest of the channel
	r, _ = NewRequest(goodID, "beta", "", "boss", at0)
	if data, _ := r.Marshal(); !strings.Contains(string(data), `"channel":"beta","version":"","by"`) {
		t.Fatalf("%s", data)
	}

	// who asked is one short line, whatever the account is called
	for in, want := range map[string]string{
		"sara_k":                         "sara_k",
		"  two   words \n":               "two words",
		"a\u202eevil":                    "aevil", // no bidirectional override
		"line\r\nbreak":                  "line break",
		"سبحان":                          "سبحان",
		"<b>x</b> & y":                   "<b>x</b> & y", // JSON, not HTML: nothing is escaped into <
		strings.Repeat("a", 200):         strings.Repeat("a", 63) + "…",
		strings.Repeat("م", 100):         strings.Repeat("م", 63) + "…",
		"":                               "dashboard",
		"\x00\x01":                       "dashboard",
		"tab\there":                      "tab here",
		"zero\u200bwidth \ufeffno-break": "zero\u200bwidth no-break",
	} {
		r, err := NewRequest(goodID, "stable", "", in, at0)
		if err != nil || r.By != want {
			t.Errorf("by %q: %q, %v (want %q)", in, r.By, err, want)
			continue
		}
		data, err := r.Marshal()
		if err != nil || len(data) > MaxRequest {
			t.Errorf("by %q: %d bytes, %v", in, len(data), err)
		}
		if strings.Contains(string(data), "\\"+"u003c") {
			t.Errorf("html-escaped: %s", data)
		}
	}
	// the longest request is far from the limit
	r, _ = NewRequest(goodID, "beta", "100.200.300-"+strings.Repeat("z", 40), strings.Repeat("م", 100), at0)
	if data, err := r.Marshal(); err != nil || len(data) > MaxRequest/2 {
		t.Fatalf("%d bytes, %v", len(data), err)
	}
}

func TestWriteRequest(t *testing.T) {
	// A permissive process umask must not widen the mode, a strict one not narrow it below 0640.
	for _, umask := range []int{0o022, 0o077, 0o000} {
		old := syscall.Umask(umask)
		d := newDir(t)
		r, _ := NewRequest(goodID, "stable", "0.7.0", "boss", at0)
		err := d.WriteRequest(r)
		syscall.Umask(old)
		if err != nil {
			t.Fatalf("umask %o: %v", umask, err)
		}
		fi, err := os.Lstat(filepath.Join(d.Path(), RequestFile))
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o640 {
			t.Fatalf("umask %o: %v %v", umask, fi, err)
		}
		got, err := d.ReadRequest()
		if err != nil || *got != r {
			t.Fatalf("read back: %+v %v", got, err)
		}
		// nothing else was left in the folder
		entries, _ := os.ReadDir(d.Path())
		if len(entries) != 1 {
			t.Fatalf("left behind: %v", entries)
		}
	}

	t.Run("replaces an earlier request whole", func(t *testing.T) {
		d := newDir(t)
		first, _ := NewRequest(goodID, "stable", "0.6.2", "boss", at0)
		second, _ := NewRequest("0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f61", "beta", "0.7.0-rc.1", "sara", at0.Add(time.Minute))
		for _, r := range []Request{first, second} {
			if err := d.WriteRequest(r); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := d.ReadRequest(); err != nil || *got != second {
			t.Fatalf("%+v %v", got, err)
		}
	})

	t.Run("never follows a link at the temporary name", func(t *testing.T) {
		d := newDir(t)
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(d.Path(), "."+RequestFile+".tmp")); err != nil {
			t.Fatal(err)
		}
		r, _ := NewRequest(goodID, "stable", "0.7.0", "boss", at0)
		if err := d.WriteRequest(r); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(victim); string(b) != "precious" {
			t.Fatalf("the file behind the link was written: %q", b)
		}
		if _, err := d.ReadRequest(); err != nil {
			t.Fatalf("the request was not written: %v", err)
		}
	})

	t.Run("never follows a link at its place", func(t *testing.T) {
		d := newDir(t)
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(d.Path(), RequestFile)); err != nil {
			t.Fatal(err)
		}
		r, _ := NewRequest(goodID, "stable", "0.7.0", "boss", at0)
		if err := d.WriteRequest(r); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(victim); string(b) != "precious" {
			t.Fatalf("the file behind the link was written: %q", b)
		}
		if fi, err := os.Lstat(filepath.Join(d.Path(), RequestFile)); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("the link was not replaced: %v %v", fi, err)
		}
	})

	t.Run("refuses a folder that is missing, a file or a link", func(t *testing.T) {
		r, _ := NewRequest(goodID, "stable", "0.7.0", "boss", at0)
		root := t.TempDir()
		if err := NewDir(filepath.Join(root, "none")).WriteRequest(r); !errors.Is(err, ErrNoDir) {
			t.Fatalf("missing: %v", err)
		}
		file := filepath.Join(root, "file")
		_ = os.WriteFile(file, nil, 0o600)
		if err := NewDir(file).WriteRequest(r); !errors.Is(err, ErrNoDir) {
			t.Fatalf("a file: %v", err)
		}
		real := filepath.Join(root, "real")
		_ = os.Mkdir(real, 0o700)
		link := filepath.Join(root, "link")
		_ = os.Symlink(real, link)
		if err := NewDir(link).WriteRequest(r); !errors.Is(err, ErrNoDir) {
			t.Fatalf("a link: %v", err)
		}
		if entries, _ := os.ReadDir(real); len(entries) != 0 {
			t.Fatalf("a request went through the link: %v", entries)
		}
	})

	t.Run("a read-only folder is an error, not a silent success", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes anywhere")
		}
		d := newDir(t)
		if err := os.Chmod(d.Path(), 0o500); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(d.Path(), 0o700) }()
		r, _ := NewRequest(goodID, "stable", "0.7.0", "boss", at0)
		if err := d.WriteRequest(r); err == nil {
			t.Fatal("written into a folder that cannot be written")
		}
	})

	t.Run("an invalid request is never written", func(t *testing.T) {
		d := newDir(t)
		if err := d.WriteRequest(Request{V: 1, ID: "x"}); err == nil {
			t.Fatal("written")
		}
		if entries, _ := os.ReadDir(d.Path()); len(entries) != 0 {
			t.Fatalf("left behind: %v", entries)
		}
	})

	t.Run("a reader never sees half a request", func(t *testing.T) {
		d := newDir(t)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		var bad error
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := d.ReadRequest(); err != nil && !errors.Is(err, os.ErrNotExist) {
					bad = err
					return
				}
			}
		}()
		for i := 0; i < 60; i++ {
			r, _ := NewRequest(goodID, "stable", "0.7.0", strings.Repeat("n", 1+i%60), at0)
			if err := d.WriteRequest(r); err != nil {
				t.Fatal(err)
			}
		}
		close(stop)
		wg.Wait()
		if bad != nil {
			t.Fatalf("a reader saw a partial or invalid request: %v", bad)
		}
	})
}

func TestReadAndRemoveRequest(t *testing.T) {
	d := newDir(t)
	if _, err := d.ReadRequest(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("none: %v", err)
	}
	if removed, err := d.RemoveRequest(goodID); removed || err != nil {
		t.Fatalf("remove none: %v %v", removed, err)
	}
	r, _ := NewRequest(goodID, "stable", "0.7.0", "boss", at0)
	_ = d.WriteRequest(r)
	// another id is left alone
	if removed, err := d.RemoveRequest("0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99"); removed || err != nil {
		t.Fatalf("another id: %v %v", removed, err)
	}
	if _, err := d.ReadRequest(); err != nil {
		t.Fatal("a request of another id was removed")
	}
	if removed, err := d.RemoveRequest(goodID); !removed || err != nil {
		t.Fatalf("own id: %v %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(d.Path(), RequestFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("still there: %v", err)
	}
	// something in its place that is not our request is never removed
	for name, content := range map[string]string{"garbage": "not json", "oversize": `{"x":"` + strings.Repeat("a", MaxRequest) + `"}`, "empty": ""} {
		writeFile(t, d, RequestFile, content)
		if removed, err := d.RemoveRequest(goodID); removed || err == nil {
			t.Errorf("%s: removed=%v err=%v", name, removed, err)
		}
		if _, err := os.Stat(filepath.Join(d.Path(), RequestFile)); err != nil {
			t.Errorf("%s: the file is gone", name)
		}
	}
	// a link in its place is not read through
	_ = os.Remove(filepath.Join(d.Path(), RequestFile))
	other := filepath.Join(t.TempDir(), "r.json")
	data, _ := r.Marshal()
	_ = os.WriteFile(other, data, 0o600)
	_ = os.Symlink(other, filepath.Join(d.Path(), RequestFile))
	if _, err := d.ReadRequest(); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a link was read: %v", err)
	}
}

// statusJSON is a status.json as the updater writes it, with fields changed.
func statusJSON(t *testing.T, over map[string]any) string {
	t.Helper()
	m := map[string]any{"v": 1, "installed": true, "helper": "0.7.0", "state": "idle", "id": "", "from": "", "to": "", "channel": "",
		"step": "", "message": "", "started_at": "", "finished_at": "", "log": []string{}}
	for k, v := range over {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadStatusStates(t *testing.T) {
	d := newDir(t)
	// idle
	writeFile(t, d, StatusFile, statusJSON(t, nil))
	st, err := d.ReadStatus()
	if err != nil || !st.Installed || st.State != StateIdle || st.Helper != "0.7.0" || st.ID != "" || st.StartedAt != nil || len(st.Log) != 0 {
		t.Fatalf("idle: %+v %v", st, err)
	}
	// running, with everything
	writeFile(t, d, StatusFile, statusJSON(t, map[string]any{"state": "running", "id": goodID, "from": "0.6.1", "to": "0.7.0", "channel": "stable",
		"step": "pull", "message": "Pulling the new images.", "started_at": "2026-10-20T10:00:05Z", "log": []string{"verified the release", "backup written"}}))
	st, err = d.ReadStatus()
	if err != nil || st.State != StateRunning || st.ID != goodID || st.From != "0.6.1" || st.To != "0.7.0" || st.Channel != "stable" || st.Step != "pull" ||
		st.Message != "Pulling the new images." || st.StartedAt == nil || !st.StartedAt.Equal(at0.Add(5*time.Second)) || st.FinishedAt != nil ||
		strings.Join(st.Log, "|") != "verified the release|backup written" {
		t.Fatalf("running: %+v %v", st, err)
	}
	// every end
	for _, state := range []string{"done", "failed", "rolled_back"} {
		step := map[string]string{"done": "done", "failed": "verify", "rolled_back": "rollback"}[state]
		writeFile(t, d, StatusFile, statusJSON(t, map[string]any{"state": state, "id": goodID, "from": "0.6.1", "to": "0.7.0", "channel": "beta", "step": step,
			"message": "m", "started_at": "2026-10-20T10:00:05Z", "finished_at": "2026-10-20T10:05:00Z"}))
		st, err = d.ReadStatus()
		if err != nil || st.State != state || !Finished(st.State) || st.Step != step || st.FinishedAt == nil || st.Channel != "beta" {
			t.Fatalf("%s: %+v %v", state, st, err)
		}
	}
	if Finished(StateIdle) || Finished(StateRunning) || Finished("") {
		t.Fatal("Finished")
	}
	// fields core does not know are ignored; a v-prefixed version is a version
	writeFile(t, d, StatusFile, `{"v":1,"installed":true,"state":"done","from":"v0.6.1","to":"0.7.0","future":{"x":1},"extra":[1,2]}`)
	if st, err = d.ReadStatus(); err != nil || st.From != "0.6.1" || st.State != StateDone {
		t.Fatalf("unknown fields: %+v %v", st, err)
	}
}

func TestReadStatusNotInstalled(t *testing.T) {
	d := newDir(t)
	writeFile(t, d, StatusFile, statusJSON(t, map[string]any{"installed": false}))
	st, err := d.ReadStatus()
	if !errors.Is(err, ErrNotInstalled) || st.Installed {
		t.Fatalf("%+v %v", st, err)
	}
	// no status at all: the folder exists (Docker made it) and nobody wrote in it
	empty := newDir(t)
	if _, err := empty.ReadStatus(); !errors.Is(err, ErrNoStatus) {
		t.Fatalf("no status: %v", err)
	}
	// no folder
	if _, err := NewDir(filepath.Join(t.TempDir(), "missing")).ReadStatus(); !errors.Is(err, ErrNoDir) {
		t.Fatalf("no folder: %v", err)
	}
	// the default folder when none is named
	if NewDir("").Path() != DefaultDir || DefaultDir != "/run/bobres-update" {
		t.Fatalf("default folder %q", NewDir("").Path())
	}
}

// Everything in status.json is untrusted: files that are not what protocol 1
// describes are refused with an error that never repeats their content.
func TestReadStatusRefusesBadFiles(t *testing.T) {
	const marker = "SECRET-MARKER-9f31"
	d := newDir(t)
	bad := map[string]string{
		"not json":             "this is not json " + marker,
		"empty":                "",
		"an array":             `[1,2,3]`,
		"null":                 `null`,
		"a string":             `"` + marker + `"`,
		"trailing junk":        statusJSON(t, nil) + " " + marker,
		"two documents":        statusJSON(t, nil) + statusJSON(t, nil),
		"installed as text":    `{"v":1,"installed":"` + marker + `","state":"idle"}`,
		"state as a number":    `{"v":1,"installed":true,"state":5}`,
		"log as text":          `{"v":1,"installed":true,"state":"idle","log":"` + marker + `"}`,
		"log of numbers":       `{"v":1,"installed":true,"state":"idle","log":[1,2]}`,
		"an unknown state":     statusJSON(t, map[string]any{"state": "exploding"}),
		"no state":             statusJSON(t, map[string]any{"state": nil}),
		"an empty state":       statusJSON(t, map[string]any{"state": ""}),
		"a state in capitals":  statusJSON(t, map[string]any{"state": "RUNNING"}),
		"truncated":            statusJSON(t, nil)[:20],
		"a NUL byte":           "{\"v\":1,\"installed\":true,\"state\":\"idle\"}\x00" + marker,
		"binary":               "\x89PNG\r\n\x1a\n" + marker,
		"deeply nested":        strings.Repeat("[", 100000) + marker,
		"bom then json":        "\ufeff" + statusJSON(t, nil),
		"huge string literal":  `{"v":1,"installed":true,"state":"idle","message":"` + strings.Repeat("a", MaxStatus) + `"}`,
		"huge number of lines": `{"v":1,"installed":true,"state":"idle","log":[` + strings.Repeat(`"x",`, MaxStatus/3) + `"x"]}`,
	}
	for name, content := range bad {
		writeFile(t, d, StatusFile, content)
		st, err := d.ReadStatus()
		if !errors.Is(err, ErrBadStatus) {
			t.Errorf("%s: %+v, %v", name, st, err)
			continue
		}
		if strings.Contains(err.Error(), marker) {
			t.Errorf("%s: the error repeats the file: %v", name, err)
		}
	}
	// exactly at the limit is read; one byte more is not
	pad := func(n int) string {
		base := statusJSON(t, map[string]any{"message": "x"})
		return base[:len(base)-1] + `,"pad":"` + strings.Repeat("p", n) + `"}`
	}
	exact := pad(MaxStatus - len(pad(0)))
	if len(exact) != MaxStatus {
		t.Fatalf("test setup: %d", len(exact))
	}
	writeFile(t, d, StatusFile, exact)
	if _, err := d.ReadStatus(); err != nil {
		t.Fatalf("a file of exactly the limit: %v", err)
	}
	writeFile(t, d, StatusFile, exact+" ")
	if _, err := d.ReadStatus(); !errors.Is(err, ErrBadStatus) {
		t.Fatalf("one byte over the limit: %v", err)
	}
}

func TestReadStatusProtocolVersion(t *testing.T) {
	d := newDir(t)
	for _, v := range []string{`2`, `0`, `-1`, `"1"`} {
		writeFile(t, d, StatusFile, `{"v":`+v+`,"installed":true,"state":"idle"}`)
		if _, err := d.ReadStatus(); err == nil || (!errors.Is(err, ErrUnsupported) && !errors.Is(err, ErrBadStatus)) {
			t.Errorf("v %s: %v", v, err)
		}
	}
	writeFile(t, d, StatusFile, `{"installed":true,"state":"idle"}`) // no v at all
	if _, err := d.ReadStatus(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("no v: %v", err)
	}
	writeFile(t, d, StatusFile, `{"v":2,"installed":true,"state":"idle"}`)
	if _, err := d.ReadStatus(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("v 2: %v", err)
	}
}

func TestReadStatusNeverFollowsLinks(t *testing.T) {
	good := statusJSON(t, map[string]any{"state": "done"})
	elsewhere := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(elsewhere, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDir(t)
	if err := os.Symlink(elsewhere, filepath.Join(d.Path(), StatusFile)); err != nil {
		t.Fatal(err)
	}
	if st, err := d.ReadStatus(); !errors.Is(err, ErrBadStatus) {
		t.Fatalf("a link to a good status was read: %+v %v", st, err)
	}
	// a link to a file that does not exist
	d2 := newDir(t)
	_ = os.Symlink(filepath.Join(t.TempDir(), "nowhere"), filepath.Join(d2.Path(), StatusFile))
	if _, err := d2.ReadStatus(); !errors.Is(err, ErrBadStatus) {
		t.Fatalf("a dangling link: %v", err)
	}
	// a link to /dev/zero or to a huge file would read for ever if followed
	d3 := newDir(t)
	_ = os.Symlink("/dev/zero", filepath.Join(d3.Path(), StatusFile))
	if _, err := d3.ReadStatus(); !errors.Is(err, ErrBadStatus) {
		t.Fatalf("a link to /dev/zero: %v", err)
	}
	// the folder itself being a link
	real := newDir(t)
	writeFile(t, real, StatusFile, good)
	link := filepath.Join(t.TempDir(), "link")
	_ = os.Symlink(real.Path(), link)
	if _, err := NewDir(link).ReadStatus(); !errors.Is(err, ErrNoDir) {
		t.Fatalf("a folder that is a link: %v", err)
	}
}

func TestReadStatusRefusesNonPlainFiles(t *testing.T) {
	d := newDir(t)
	if err := os.Mkdir(filepath.Join(d.Path(), StatusFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReadStatus(); !errors.Is(err, ErrBadStatus) {
		t.Fatalf("a folder: %v", err)
	}
	// a named pipe would block a reader that waits for a writer
	d2 := newDir(t)
	if err := syscall.Mkfifo(filepath.Join(d2.Path(), StatusFile), 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := d2.ReadStatus(); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBadStatus) {
			t.Fatalf("a named pipe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a named pipe blocks")
	}
	// the same for a request
	if err := syscall.Mkfifo(filepath.Join(d2.Path(), RequestFile), 0o600); err == nil {
		done2 := make(chan error, 1)
		go func() { _, err := d2.ReadRequest(); done2 <- err }()
		select {
		case err := <-done2:
			if err == nil {
				t.Fatal("a named pipe was read as a request")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reading a request pipe blocks")
		}
	}
}

func TestReadStatusCleansEveryField(t *testing.T) {
	d := newDir(t)
	long := strings.Repeat("لورم ", 200)
	lines := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		lines = append(lines, "line "+strings.Repeat("x", i%3*200)+" #"+string(rune('A'+i%26)))
	}
	lines = append(lines, "", "   ", "\t\n")
	writeFile(t, d, StatusFile, statusJSON(t, map[string]any{
		"state": "running", "helper": "<img src=x onerror=alert(1)>", "id": "not-an-id", "from": "0.6.1; rm -rf /", "to": "javascript:alert(1)",
		"channel": "nightly", "step": "format-disk", "message": "line one\nline two\u202e override <b>bold</b> " + long,
		"started_at": "yesterday", "finished_at": "2026-10-20T13:30:00+03:30", "log": lines,
	}))
	st, err := d.ReadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Helper != "" || st.ID != "" || st.From != "" || st.To != "" || st.Channel != "" || st.Step != "" || st.StartedAt != nil {
		t.Fatalf("what is not a version, an id, a channel or a step was kept: %+v", st)
	}
	if st.FinishedAt == nil || !st.FinishedAt.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) || st.FinishedAt.Location() != time.UTC {
		t.Fatalf("finished_at: %v", st.FinishedAt)
	}
	if strings.ContainsAny(st.Message, "\n\u202e") || len([]rune(st.Message)) != MaxMessage || !strings.HasSuffix(st.Message, "…") ||
		!strings.HasPrefix(st.Message, "line one line two override <b>bold</b>") {
		t.Fatalf("message: %q", st.Message)
	}
	if len(st.Log) != MaxLog {
		t.Fatalf("%d log lines, want the newest %d", len(st.Log), MaxLog)
	}
	for _, l := range st.Log {
		if l == "" || len([]rune(l)) > MaxLine || strings.ContainsAny(l, "\n\t\r") {
			t.Fatalf("log line %q", l)
		}
	}
	// the newest are kept: the last non-empty line of the file is the last one
	if want := "line " + strings.Repeat("x", 99%3*200) + " #" + string(rune('A'+99%26)); st.Log[len(st.Log)-1] != Clean(want, MaxLine) {
		t.Fatalf("last line %q", st.Log[len(st.Log)-1])
	}
}

func TestReadStatusMasksSecretsInTheLog(t *testing.T) {
	d := newDir(t)
	writeFile(t, d, StatusFile, statusJSON(t, map[string]any{"state": "running", "log": []string{
		"BOBRES_TELEGRAM_BOT_TOKEN=123456789:AAH-abcdefghijklmnopqrstuvwxyz012345 was read",
		"sending Authorization: Bearer abc.def.ghi to the registry",
		"password: hunter2 accepted",
		"api_key=sk-live-123 set",
		"Checking the key list signature",
		"pulling ghcr.io/sobhanaz/bobres-core:0.7.0",
		"token = abc123",
		"bot 987654321:ZZZ-abcdefghijklmnopqrstuvwxyz0123456789 started",
	}}))
	st, err := d.ReadStatus()
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(st.Log, "\n")
	for _, secret := range []string{"123456789:AAH", "abc.def.ghi", "hunter2", "sk-live-123", "abc123", "987654321:ZZZ"} {
		if strings.Contains(all, secret) {
			t.Errorf("%q is in the log: %s", secret, all)
		}
	}
	// ordinary lines are untouched
	for _, keep := range []string{"Checking the key list signature", "pulling ghcr.io/sobhanaz/bobres-core:0.7.0"} {
		if !strings.Contains(all, keep) {
			t.Errorf("%q was changed: %s", keep, all)
		}
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"":                     "",
		"plain":                "plain",
		"  a   b  ":            "a b",
		"a\x00b":               "ab",
		"a\u0085b":             "a b",
		"\u202aleft\u202c":     "left",
		"\u2066iso\u2069":      "iso",
		"\u200fmark":           "mark",
		"x\u2028y":             "x y",
		"zwnj\u200cstays":      "zwnj\u200cstays", // the Persian half-space is text
		"bad \xff\xfe bytes":   "bad bytes",
		strings.Repeat("a", 5): "aaaaa",
	} {
		if got := Clean(in, 300); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Clean("abcdef", 4); got != "abc…" {
		t.Errorf("cut: %q", got)
	}
	if got := Clean("abcd", 4); got != "abcd" {
		t.Errorf("exact: %q", got)
	}
}
