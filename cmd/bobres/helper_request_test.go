package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var reqNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

const reqID = "0192f9e0-7a1b-7c3d-8e4f-123456789abc"

// reqJSON is a request as core writes it, changed by mods.
func reqJSON(mods ...func(map[string]any)) []byte {
	m := map[string]any{
		"v": 1, "id": reqID, "action": "update", "channel": "stable", "version": "", "by": "owner",
		"at": reqNow.Add(-time.Minute).Format(time.RFC3339),
	}
	for _, f := range mods {
		f(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func set(k string, v any) func(map[string]any) { return func(m map[string]any) { m[k] = v } }
func drop(k string) func(map[string]any)       { return func(m map[string]any) { delete(m, k) } }

func TestParseUpdateRequestAccepts(t *testing.T) {
	for name, tc := range map[string]struct {
		mods []func(map[string]any)
		want updateRequest
	}{
		"newest stable": {nil, updateRequest{ID: reqID, Channel: "stable", By: "owner", At: reqNow.Add(-time.Minute)}},
		"newest beta":   {[]func(map[string]any){set("channel", "beta")}, updateRequest{ID: reqID, Channel: "beta", By: "owner", At: reqNow.Add(-time.Minute)}},
		"a release":     {[]func(map[string]any){set("version", "1.2.3")}, updateRequest{ID: reqID, Channel: "stable", Version: "1.2.3", By: "owner", At: reqNow.Add(-time.Minute)}},
		"a pre-release": {[]func(map[string]any){set("version", "10.20.30-rc.1"), set("channel", "beta")}, updateRequest{ID: reqID, Channel: "beta", Version: "10.20.30-rc.1", By: "owner", At: reqNow.Add(-time.Minute)}},
		"an upper case id is the same id": {[]func(map[string]any){set("id", strings.ToUpper(reqID))},
			updateRequest{ID: reqID, Channel: "stable", By: "owner", At: reqNow.Add(-time.Minute)}},
		"a name in Persian with a half space": {[]func(map[string]any){set("by", "علی\xe2\x80\x8cرضا")},
			updateRequest{ID: reqID, Channel: "stable", By: "علی\xe2\x80\x8cرضا", At: reqNow.Add(-time.Minute)}},
		"a name of 64 characters": {[]func(map[string]any){set("by", strings.Repeat("ب", 64))},
			updateRequest{ID: reqID, Channel: "stable", By: strings.Repeat("ب", 64), At: reqNow.Add(-time.Minute)}},
		"fifteen minutes old": {[]func(map[string]any){set("at", reqNow.Add(-15*time.Minute).Format(time.RFC3339))},
			updateRequest{ID: reqID, Channel: "stable", By: "owner", At: reqNow.Add(-15 * time.Minute)}},
		"a minute ahead": {[]func(map[string]any){set("at", reqNow.Add(time.Minute).Format(time.RFC3339))},
			updateRequest{ID: reqID, Channel: "stable", By: "owner", At: reqNow.Add(time.Minute)}},
		"fractions and an offset": {[]func(map[string]any){set("at", "2026-10-20T15:29:30.123456+03:30")},
			updateRequest{ID: reqID, Channel: "stable", By: "owner", At: time.Date(2026, 10, 20, 11, 59, 30, 123456000, time.UTC)}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseUpdateRequest(reqJSON(tc.mods...), reqNow)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got.ID != tc.want.ID || got.Channel != tc.want.Channel || got.Version != tc.want.Version || got.By != tc.want.By || !got.At.Equal(tc.want.At) {
				t.Errorf("got %+v, want %+v", *got, tc.want)
			}
		})
	}
}

func TestParseUpdateRequestRefuses(t *testing.T) {
	long := strings.Repeat("a", maxRequestBytes)
	for name, tc := range map[string]struct {
		body []byte
		why  string
	}{
		"not JSON":                 {[]byte("update please"), "not the JSON object"},
		"empty":                    {nil, "not the JSON object"},
		"an array":                 {[]byte(`[]`), "not the JSON object"},
		"null":                     {[]byte(`null`), `"v" is missing`},
		"two objects":              {append(reqJSON(), reqJSON()...), "more in the file"},
		"garbage after it":         {append(reqJSON(), "x"...), "more in the file"},
		"too big":                  {reqJSON(set("by", long)), "larger than 2048"},
		"a field too many":         {reqJSON(set("url", "https://evil.example/bobres")), `unknown field "url"`},
		"an env field":             {reqJSON(set("env", map[string]string{"BOBRES_STATE_DIR": "/"})), `unknown field "env"`},
		"a base url":               {reqJSON(set("base_url", "http://127.0.0.1:1")), `unknown field "base_url"`},
		"flags":                    {reqJSON(set("flags", []string{"--dir", "/"})), `unknown field "flags"`},
		"no v":                     {reqJSON(drop("v")), `"v" is missing`},
		"no id":                    {reqJSON(drop("id")), `"id" is missing`},
		"no action":                {reqJSON(drop("action")), `"action" is missing`},
		"no channel":               {reqJSON(drop("channel")), `"channel" is missing`},
		"no version":               {reqJSON(drop("version")), `"version" is missing`},
		"no by":                    {reqJSON(drop("by")), `"by" is missing`},
		"no at":                    {reqJSON(drop("at")), `"at" is missing`},
		"another protocol":         {reqJSON(set("v", 2)), "protocol version 2"},
		"v as text":                {reqJSON(set("v", "1")), "not the JSON object"},
		"another action":           {reqJSON(set("action", "rollback")), "is not update"},
		"an id that is not":        {reqJSON(set("id", "../../etc")), "not a UUID"},
		"a short id":               {reqJSON(set("id", "1234")), "not a UUID"},
		"a channel unknown":        {reqJSON(set("channel", "nightly")), "neither stable nor beta"},
		"a channel in caps":        {reqJSON(set("channel", "Stable")), "neither stable nor beta"},
		"no channel at all":        {reqJSON(set("channel", "")), "neither stable nor beta"},
		"a v in the version":       {reqJSON(set("version", "v1.2.3")), "not a release"},
		"a short version":          {reqJSON(set("version", "1.2")), "not a release"},
		"latest":                   {reqJSON(set("version", "latest")), "not a release"},
		"a path in the label":      {reqJSON(set("version", "1.2.3-../../x")), "not a release"},
		"a slash in the label":     {reqJSON(set("version", "1.2.3-rc/1")), "not a release"},
		"build metadata":           {reqJSON(set("version", "1.2.3+build")), "not a release"},
		"a leading zero":           {reqJSON(set("version", "01.2.3")), "not a release"},
		"a space":                  {reqJSON(set("version", "1.2.3 ")), "not a release"},
		"a shell word":             {reqJSON(set("version", "1.2.3;reboot")), "not a release"},
		"a version too long":       {reqJSON(set("version", "1.2.3-"+strings.Repeat("a", 70))), "not a release"},
		"no name":                  {reqJSON(set("by", "")), "user name is empty"},
		"a name too long":          {reqJSON(set("by", strings.Repeat("ب", 65))), "longer than 64"},
		"a line break":             {reqJSON(set("by", "owner\nerror: forged")), "control character"},
		"an escape":                {reqJSON(set("by", "owner\x1b[2J")), "control character"},
		"a NUL":                    {reqJSON(set("by", "own\x00er")), "control character"},
		"a right-to-left override": {reqJSON(set("by", "owner\xe2\x80\xae")), "control character"},
		"a line separator":         {reqJSON(set("by", "a\xe2\x80\xa8b")), "control character"},
		"a time that is not":       {reqJSON(set("at", "yesterday")), "not RFC 3339"},
		"a date only":              {reqJSON(set("at", "2026-10-20")), "not RFC 3339"},
		"too old":                  {reqJSON(set("at", reqNow.Add(-15*time.Minute-time.Second).Format(time.RFC3339))), "older than 15 minutes"},
		"a day old":                {reqJSON(set("at", reqNow.Add(-24*time.Hour).Format(time.RFC3339))), "older than 15 minutes"},
		"in the future":            {reqJSON(set("at", reqNow.Add(time.Minute+time.Second).Format(time.RFC3339))), "in the future"},
		"far in the future":        {reqJSON(set("at", reqNow.Add(48*time.Hour).Format(time.RFC3339))), "in the future"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseUpdateRequest(tc.body, reqNow)
			if err == nil {
				t.Fatalf("accepted: %+v", *got)
			}
			if !strings.Contains(err.Error(), tc.why) {
				t.Errorf("the reason is %q, want one with %q", err, tc.why)
			}
			if strings.ContainsAny(err.Error(), "\n\x1b\x00") {
				t.Errorf("the reason carries a control character: %q", err)
			}
		})
	}
}

// An id in a refused request is told only when it is a UUID: it goes into status.json, which core
// reads, and into nothing else.
func TestRequestIDHint(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{string(reqJSON()), reqID},
		{string(reqJSON(set("channel", "x"))), reqID},
		{string(reqJSON(set("id", "../../etc"))), ""},
		{string(reqJSON(set("id", strings.ToUpper(reqID)))), reqID},
		{"not json", ""},
		{`{"id": 7}`, ""},
		{"", ""},
	} {
		if got := requestIDHint([]byte(tc.body)); got != tc.want {
			t.Errorf("requestIDHint(%.40q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// updateDir is <install>/run/update as `bobres install` makes it for a user who is not root: run/
// 0755 and update/ 0700, both ours.
func updateDirFixture(t *testing.T) (dir, upd string) {
	t.Helper()
	dir = t.TempDir()
	upd = updateDirPath(dir)
	if err := os.MkdirAll(upd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, runDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, upd
}

func TestReadRequestFile(t *testing.T) {
	me := os.Getuid()
	_, upd := updateDirFixture(t)
	path := filepath.Join(upd, requestFileName)
	write := func(b []byte) {
		t.Helper()
		_ = os.Remove(path)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := readRequestFile(upd, me); !errors.Is(err, errNoRequest) {
		t.Errorf("no file: %v", err)
	}
	write(reqJSON())
	if b, err := readRequestFile(upd, me); err != nil || string(b) != string(reqJSON()) {
		t.Errorf("a request: %q %v", b, err)
	}

	// Exactly the size limit is read; a byte more is refused without being read.
	pad := func(n int) []byte { return append(reqJSON(), []byte(strings.Repeat(" ", n-len(reqJSON())))...) }
	write(pad(maxRequestBytes))
	if b, err := readRequestFile(upd, me); err != nil || len(b) != maxRequestBytes {
		t.Errorf("2048 bytes: %d %v", len(b), err)
	}
	write(pad(maxRequestBytes + 1))
	if _, err := readRequestFile(upd, me); err == nil || !strings.Contains(err.Error(), "larger than 2048") {
		t.Errorf("2049 bytes: %v", err)
	}

	// Not core's file: refused whoever it is (the check is the file's owner against core's uid).
	write(reqJSON())
	if _, err := readRequestFile(upd, me+1); err == nil || !strings.Contains(err.Error(), "not owned by core") {
		t.Errorf("another owner: %v", err)
	}

	// A link, to a file of the right owner even, is never followed.
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, reqJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	if err := os.Symlink(secret, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readRequestFile(upd, me); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a link: %v", err)
	}

	// A pipe does not hold the helper up.
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readRequestFile(upd, me); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("a pipe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a pipe blocked")
	}

	// A directory is not a request.
	_ = os.Remove(path)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readRequestFile(upd, me); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a directory: %v", err)
	}
}

// Whatever sits at request.json is gone after takeRequest, or the path unit would start the helper
// again and again; a link is unlinked, not followed.
func TestTakeRequestRemovesWhatItRefuses(t *testing.T) {
	me := os.Getuid()
	_, upd := updateDirFixture(t)
	path := filepath.Join(upd, requestFileName)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, plant := range map[string]func() error{
		"a link":          func() error { return os.Symlink(outside, path) },
		"a link to a dir": func() error { return os.Symlink(filepath.Dir(outside), path) },
		"a pipe":          func() error { return syscall.Mkfifo(path, 0o600) },
		"a directory": func() error {
			if err := os.Mkdir(path, 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(path, "inner"), []byte("x"), 0o600)
		},
		"a big file": func() error { return os.WriteFile(path, make([]byte, 5000), 0o600) },
		"garbage":    func() error { return os.WriteFile(path, []byte("hello"), 0o600) },
		"a request":  func() error { return os.WriteFile(path, reqJSON(), 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := plant(); err != nil {
				t.Fatal(err)
			}
			_, _ = takeRequest(upd, me)
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("still there: %v", err)
			}
			if b, err := os.ReadFile(outside); err != nil || string(b) != "mine" {
				t.Errorf("the target of the link was touched: %q %v", b, err)
			}
			if _, err := os.Stat(filepath.Dir(outside)); err != nil {
				t.Errorf("the directory a link pointed to was touched: %v", err)
			}
		})
	}
	if _, err := takeRequest(upd, me); !errors.Is(err, errNoRequest) {
		t.Errorf("nothing there: %v", err)
	}
}

func TestCheckUpdateDir(t *testing.T) {
	me := os.Getuid()
	t.Run("as install makes it", func(t *testing.T) {
		dir, _ := updateDirFixture(t)
		if err := checkUpdateDir(dir, me); err != nil {
			t.Error(err)
		}
	})
	t.Run("not there", func(t *testing.T) {
		if err := checkUpdateDir(t.TempDir(), me); err == nil {
			t.Error("accepted a missing directory")
		}
	})
	t.Run("update is a link", func(t *testing.T) {
		dir, upd := updateDirFixture(t)
		other := t.TempDir()
		if err := os.Remove(upd); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, upd); err != nil {
			t.Fatal(err)
		}
		if err := checkUpdateDir(dir, me); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("%v", err)
		}
	})
	t.Run("run is a link", func(t *testing.T) {
		dir := t.TempDir()
		real := t.TempDir()
		if err := os.Mkdir(filepath.Join(real, "update"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(dir, runDirName)); err != nil {
			t.Fatal(err)
		}
		if err := checkUpdateDir(dir, me); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("%v", err)
		}
	})
	t.Run("others can write", func(t *testing.T) {
		for _, which := range []string{runDirName, filepath.Join(runDirName, updateDirName)} {
			dir, _ := updateDirFixture(t)
			if err := os.Chmod(filepath.Join(dir, which), 0o775); err != nil {
				t.Fatal(err)
			}
			if err := checkUpdateDir(dir, me); err == nil || !strings.Contains(err.Error(), "can be written by others") {
				t.Errorf("%s mode 0775: %v", which, err)
			}
		}
	})
	t.Run("run belongs to someone else", func(t *testing.T) {
		dir, _ := updateDirFixture(t)
		saved := asRoot.owner
		t.Cleanup(func() { asRoot.owner = saved })
		asRoot.owner = func(fi os.FileInfo) (int, bool) {
			if fi.Name() == runDirName {
				return me + 7, true
			}
			return saved(fi)
		}
		if err := checkUpdateDir(dir, me); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("belongs to uid %d", me+7)) {
			t.Errorf("%v", err)
		}
	})
	t.Run("update belongs to core or to root only", func(t *testing.T) {
		dir, _ := updateDirFixture(t)
		saved := asRoot.owner
		t.Cleanup(func() { asRoot.owner = saved })
		asRoot.owner = func(fi os.FileInfo) (int, bool) {
			if fi.Name() == updateDirName {
				return 4242, true
			}
			return saved(fi)
		}
		if err := checkUpdateDir(dir, me); err == nil || !strings.Contains(err.Error(), "belongs to uid 4242") {
			t.Errorf("a stranger's update/: %v", err)
		}
		if err := checkUpdateDir(dir, 4242); err != nil {
			t.Errorf("core's update/: %v", err)
		}
	})
}

func TestMarkHandled(t *testing.T) {
	dir := t.TempDir()
	again, err := markHandled(dir, reqID, reqNow)
	if err != nil || again {
		t.Fatalf("first time: %v %v", again, err)
	}
	if again, err = markHandled(dir, reqID, reqNow.Add(time.Minute)); err != nil || !again {
		t.Fatalf("second time: %v %v", again, err)
	}
	if st, err := os.Stat(filepath.Join(dir, handledFileName)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("the record: %v %v", st, err)
	}
	// Another id is another request.
	other := "0192f9e0-7a1b-7c3d-8e4f-123456789abd"
	if again, err = markHandled(dir, other, reqNow); err != nil || again {
		t.Errorf("another id: %v %v", again, err)
	}
	// A day later the first is forgotten (a request that old is refused for its age anyway).
	if again, err = markHandled(dir, reqID, reqNow.Add(handledKeep+time.Minute)); err != nil || again {
		t.Errorf("a day later: %v %v", again, err)
	}
	// Garbage in the record is skipped, and the record stays small.
	if err := os.WriteFile(filepath.Join(dir, handledFileName), []byte("junk\n12 \n x y z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < handledMax+50; i++ {
		if _, err := markHandled(dir, fmt.Sprintf("0192f9e0-7a1b-7c3d-8e4f-%012d", i), reqNow); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, handledFileName))
	if n := strings.Count(string(b), "\n"); n != handledMax {
		t.Errorf("%d records kept, want %d", n, handledMax)
	}
	// The newest are the ones kept.
	if again, _ = markHandled(dir, fmt.Sprintf("0192f9e0-7a1b-7c3d-8e4f-%012d", handledMax+49), reqNow); !again {
		t.Error("the newest id was forgotten")
	}
}
