package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const cliKey = "BOBRES-7K2Q-9XMV-4HTD"

// fakeVendor is the license server: it answers status and body and keeps
// what it was sent.
type fakeVendor struct {
	mu      sync.Mutex
	sent    []string
	status  int
	body    string
	headers map[string]string
}

func (f *fakeVendor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, r.Method+" "+r.URL.Path+" "+string(b))
	for k, v := range f.headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(f.status)
	_, _ = io.WriteString(w, f.body)
}

// fakeCore answers `/app license info --json` with info and keeps what
// `/app license import --activated` was piped.
type fakeCore struct {
	info      string
	infoErr   error
	importErr error
	calls     []string
	imported  []string
}

func (c *fakeCore) pipe(_ context.Context, _ string, in io.Reader, w io.Writer, args ...string) error {
	cmd := strings.Join(args, " ")
	c.calls = append(c.calls, cmd)
	switch {
	case strings.HasSuffix(cmd, "exec -T core /app license info --json"):
		if c.infoErr != nil {
			return c.infoErr
		}
		_, _ = io.WriteString(w, c.info)
	case strings.HasSuffix(cmd, "exec -T core /app license import --activated"):
		b, _ := io.ReadAll(in)
		c.imported = append(c.imported, string(b))
		if c.importErr != nil {
			return c.importErr
		}
		_, _ = io.WriteString(w, "License installed.\nState:          active\n")
	default:
		return errors.New("unexpected command: " + cmd)
	}
	return nil
}

const coreInfo = `{"install_id":"0b5c1f0e-0000-4000-8000-000000000001","domain":"panel.example.com","version":"1.4.0",
"state":"missing","reason":"","valid":false,"source":null,"updated_at":null,"refreshed_at":null,"license":null}`

func activateOps(t *testing.T, stdin string, c *fakeCore, hc *http.Client) (*ops, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	return &ops{in: strings.NewReader(stdin), out: &out, errOut: &errOut, pipe: c.pipe, httpc: hc,
		run:    func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("run is not used") },
		stream: func(context.Context, string, ...string) error { return errors.New("stream is not used") },
		sleep:  func(time.Duration) {}, now: time.Now}, &out, &errOut
}

// bobres license activate: core's install id and domain go with the key to
// the license server, and its token into core's import (activated).
func TestLicenseActivate(t *testing.T) {
	t.Setenv("BOBRES_LICENSE_SERVER", "")
	v := &fakeVendor{status: http.StatusOK, body: `{"token":"tok.sig"}`}
	srv := httptest.NewTLSServer(v)
	defer srv.Close()
	dir := installDir(t)
	c := &fakeCore{info: coreInfo}
	o, out, errOut := activateOps(t, "", c, srv.Client())
	if code := o.licenseCmd([]string{"activate", cliKey, "--dir", dir, "--server", srv.URL + "/"}); code != 0 {
		t.Fatalf("activate: %d %s", code, errOut)
	}
	path, body, _ := strings.Cut(strings.TrimPrefix(v.sent[0], "POST "), " ")
	var sent licenseActivation
	if err := json.Unmarshal([]byte(body), &sent); err != nil || path != "/v1/activate" ||
		sent != (licenseActivation{Key: cliKey, InstallID: "0b5c1f0e-0000-4000-8000-000000000001", Domain: "panel.example.com", Version: "1.4.0"}) {
		t.Fatalf("sent %s", v.sent[0])
	}
	if len(c.imported) != 1 || c.imported[0] != "tok.sig\n" {
		t.Fatalf("imported %q", c.imported)
	}
	if !strings.Contains(out.String(), "License installed.") || !strings.Contains(out.String(), "panel.example.com") {
		t.Fatalf("out: %s", out)
	}
	if strings.Contains(out.String()+errOut.String(), cliKey) || strings.Contains(out.String()+errOut.String(), "tok.sig") {
		t.Fatalf("the key or the token was printed:\n%s%s", out, errOut)
	}

	// The key on stdin (out of the shell history), the server from .env.
	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), append(env, []byte("BOBRES_LICENSE_SERVER="+srv.URL+"\n")...), 0o600); err != nil { //nolint:gosec // the test's own folder
		t.Fatal(err)
	}
	o, _, errOut = activateOps(t, "  "+cliKey+"\nrest\n", c, srv.Client())
	if code := o.licenseCmd([]string{"activate", "--dir", dir}); code != 0 || len(v.sent) != 2 || !strings.Contains(v.sent[1], `"key":"`+cliKey+`"`) {
		t.Fatalf("stdin: %d %s %v", code, errOut, v.sent)
	}
	// The environment wins over .env (here: not https, refused).
	t.Setenv("BOBRES_LICENSE_SERVER", "http://license.example.com")
	o, _, errOut = activateOps(t, "", c, srv.Client())
	if code := o.licenseCmd([]string{"activate", cliKey, "--dir", dir}); code != 2 || !strings.Contains(errOut.String(), "https") {
		t.Fatalf("http server: %d %s", code, errOut)
	}
}

func TestLicenseActivateRefusals(t *testing.T) {
	t.Setenv("BOBRES_LICENSE_SERVER", "")
	v := &fakeVendor{}
	srv := httptest.NewTLSServer(v)
	defer srv.Close()
	dir := installDir(t)

	// Wrong use: nothing is asked of core or the server.
	for name, c := range map[string]struct {
		args  []string
		stdin string
		want  string
	}{
		"no key":           {[]string{"activate", "--dir", dir, "--server", srv.URL}, "", "usage:"},
		"two keys":         {[]string{"activate", "a", "b", "--dir", dir, "--server", srv.URL}, "", "usage:"},
		"a key with space": {[]string{"activate", "BOBRES-1 2", "--dir", dir, "--server", srv.URL}, "", "usage:"},
		"no install":       {[]string{"activate", cliKey, "--dir", t.TempDir(), "--server", srv.URL}, "", "no install found"},
		"no server":        {[]string{"activate", cliKey, "--dir", dir}, "", "no license server: set BOBRES_LICENSE_SERVER"},
		"a plain server":   {[]string{"activate", cliKey, "--dir", dir, "--server", "http://license.example.com"}, "", "https URL"},
		"a bad flag":       {[]string{"activate", cliKey, "--dir", dir, "--nope"}, "", "flag provided but not defined"},
	} {
		core := &fakeCore{info: coreInfo}
		o, _, errOut := activateOps(t, c.stdin, core, srv.Client())
		if code := o.licenseCmd(c.args); code != 2 || !strings.Contains(errOut.String(), c.want) || len(core.calls) != 0 {
			t.Errorf("%s: %d %q %v", name, code, errOut, core.calls)
		}
	}
	if len(v.sent) != 0 {
		t.Fatalf("wrong use reached the server: %v", v.sent)
	}

	// Core cannot say who it is: the server is not asked.
	for name, core := range map[string]*fakeCore{
		"core down":  {infoErr: errors.New("exit status 1")},
		"older core": {info: "usage: license status | license import"},
		"no domain":  {info: `{"install_id":"0b5c","domain":""}`},
	} {
		o, _, errOut := activateOps(t, "", core, srv.Client())
		if code := o.licenseCmd([]string{"activate", cliKey, "--dir", dir, "--server", srv.URL}); code != 1 || len(core.imported) != 0 {
			t.Errorf("%s: %d %s", name, code, errOut)
		}
	}
	if len(v.sent) != 0 {
		t.Fatalf("the server was asked without an install id: %v", v.sent)
	}

	// The server refuses or fails: nothing is imported.
	for name, c := range map[string]struct {
		status  int
		body    string
		headers map[string]string
		want    string
	}{
		"a wrong key": {http.StatusForbidden, `{"error":"invalid_key","message":"this license key is not valid"}`, nil,
			"the license server refused: this license key is not valid (invalid_key)"},
		"slow down": {http.StatusTooManyRequests, `{"error":"rate_limited","message":"too many requests"}`, map[string]string{"Retry-After": "30"},
			"(rate_limited); try again in 30 seconds"},
		"broken":      {http.StatusInternalServerError, `{"error":"internal","message":"x"}`, nil, "the license server answered 500"},
		"a redirect":  {http.StatusFound, ``, map[string]string{"Location": "http://elsewhere.example/v1/activate"}, "answered 302"},
		"no token":    {http.StatusOK, `{"token":""}`, nil, "answered 200"},
		"not JSON":    {http.StatusOK, `tok.sig`, nil, "answered 200"},
		"a huge body": {http.StatusOK, `{"token":"` + strings.Repeat("a", maxLicenseAnswer) + `"}`, nil, "answered 200"},
	} {
		v.status, v.body, v.headers = c.status, c.body, c.headers
		core := &fakeCore{info: coreInfo}
		o, _, errOut := activateOps(t, "", core, srv.Client())
		if code := o.licenseCmd([]string{"activate", cliKey, "--dir", dir, "--server", srv.URL}); code != 1 ||
			!strings.Contains(errOut.String(), c.want) || !strings.Contains(errOut.String(), "Nothing was changed") ||
			len(core.imported) != 0 || strings.Contains(errOut.String(), cliKey) {
			t.Errorf("%s: %d %q", name, code, errOut)
		}
	}

	// Core refuses the token (it does not verify here).
	v.status, v.body, v.headers = http.StatusOK, `{"token":"tok.sig"}`, nil
	core := &fakeCore{info: coreInfo, importErr: errors.New("exit status 1")}
	o, _, errOut := activateOps(t, "", core, srv.Client())
	if code := o.licenseCmd([]string{"activate", cliKey, "--dir", dir, "--server", srv.URL}); code != 1 ||
		!strings.Contains(errOut.String(), "core did not install the license") {
		t.Fatalf("refused by core: %d %s", code, errOut)
	}

	// The server is down.
	srv.Close()
	o, _, errOut = activateOps(t, "", &fakeCore{info: coreInfo}, srv.Client())
	if code := o.licenseCmd([]string{"activate", cliKey, "--dir", dir, "--server", srv.URL}); code != 1 ||
		!strings.Contains(errOut.String(), "the license server did not answer") || strings.Contains(errOut.String(), cliKey) {
		t.Fatalf("server down: %d %s", code, errOut)
	}
}
