package tlsconf

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
)

// Core never writes Caddy's files and never reaches Caddy's admin socket.
// It hands its request to the tls-agent through two small named volumes:
//   - tls_request: core writes RequestFile in it (core mounts it read-write
//     at CoreRequestDir; the agent read-only at AgentRequestDir);
//   - tls_result: the agent writes ResultFile in it (the agent mounts it
//     read-write at AgentResultDir; core read-only at CoreResultDir).
//
// Each file is one JSON document, replaced atomically (WriteFile).
const (
	RequestFile = "tls.json"
	ResultFile  = "result.json"

	CoreRequestDir  = "/request"
	CoreResultDir   = "/tls-result"
	AgentRequestDir = "/request"
	AgentResultDir  = "/result"
)

// Request is what core asks the agent to apply. In manual mode it carries
// the private key in clear: the file is mode 0600, in a volume only core
// and the agent mount. It never goes into a log (LogValue leaves the PEM
// out).
type Request struct {
	// Generation is a new number for every request: the agent applies a
	// request whose generation differs from the last one it applied, and
	// answers with the same number.
	Generation   int64  `json:"generation"`
	Mode         Mode   `json:"mode"`
	IP           string `json:"ip"`
	Domain       string `json:"domain"`
	ZarinpalHost string `json:"zarinpal_host"`
	CertPEM      string `json:"cert_pem"`
	KeyPEM       string `json:"key_pem"`
}

// State is the request as a state to render.
func (r Request) State() State {
	return State{Mode: r.Mode, IP: r.IP, Domain: r.Domain, ZarinpalHost: r.ZarinpalHost, HasCert: r.CertPEM != "" && r.KeyPEM != ""}
}

// LogValue keeps the certificate and the key out of logs.
func (r Request) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("generation", r.Generation), slog.String("mode", string(r.Mode)),
		slog.String("ip", r.IP), slog.String("domain", r.Domain), slog.String("zarinpal_host", r.ZarinpalHost),
		slog.Bool("pair", r.CertPEM != ""))
}

// String keeps the certificate and the key out of %v.
func (r Request) String() string { return r.LogValue().String() }

// Result is the agent's answer to one request.
type Result struct {
	Generation int64 `json:"generation"`
	OK         bool  `json:"ok"`
	// Code says why it failed (Result* below; "" when OK).
	Code string `json:"code"`
	// Error is the agent's or Caddy's own words.
	Error string `json:"error"`
	// AppliedAt is when the agent answered (unix seconds).
	AppliedAt int64 `json:"applied_at"`
}

// Result codes.
const (
	// ResultInvalid: the agent refused the request; nothing was written.
	ResultInvalid = "invalid"
	// ResultWriteFailed: the files could not be written; the previous ones
	// were put back.
	ResultWriteFailed = "write_failed"
	// ResultRefused: Caddy refused the new files; the previous ones were
	// put back and loaded again.
	ResultRefused = "caddy_refused"
	// ResultUnreachable: Caddy did not answer (it is stopped, restarting or
	// starting); the new files stay, Caddy reads them when it starts, and
	// the agent keeps trying to load them.
	ResultUnreachable = "caddy_unreachable"
	// ResultAgentError: the agent cannot do its part (it cannot read Caddy's
	// main Caddyfile); nothing was changed.
	ResultAgentError = "agent_error"
)

// File modes.
const (
	// RequestMode: the request may hold a private key.
	RequestMode os.FileMode = 0o600
	// ResultMode: the result holds no secret.
	ResultMode os.FileMode = 0o644
	// FileMode is every HTTPS file the agent writes: Caddy reads them as
	// group 65532 (core's and the agent's group).
	FileMode os.FileMode = 0o640
)

// maxRequest bounds a request file: two PEM texts and a little more.
const maxRequest = 4*MaxPEM + 4096

// Exchange is core's side of the volumes.
type Exchange struct {
	RequestPath string
	ResultPath  string
}

// NewExchange is the exchange with the request and result files in these
// directories (CoreRequestDir and CoreResultDir in the core container).
func NewExchange(requestDir, resultDir string) Exchange {
	return Exchange{RequestPath: filepath.Join(requestDir, RequestFile), ResultPath: filepath.Join(resultDir, ResultFile)}
}

// WriteRequest replaces the request.
func (e Exchange) WriteRequest(r Request) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return WriteFile(e.RequestPath, data, RequestMode)
}

// ReadRequest reads the request (an error wrapping os.ErrNotExist when
// there is none).
func (e Exchange) ReadRequest() (*Request, error) { return ReadRequest(e.RequestPath) }

// ReadResult reads the agent's last answer (an error wrapping
// os.ErrNotExist when there is none).
func (e Exchange) ReadResult() (*Result, error) {
	var r Result
	if err := readJSON(e.ResultPath, 64<<10, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ReadRequest reads a request file.
func ReadRequest(path string) (*Request, error) {
	var r Request
	if err := readJSON(path, maxRequest, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WriteResult replaces a result file.
func WriteResult(path string, r Result) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return WriteFile(path, data, ResultMode)
}

// readJSON decodes a small JSON file (at most limit bytes) into v.
func readJSON(path string, limit int64, v any) error {
	f, err := os.Open(path) //nolint:gosec // a fixed path under a volume
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("%s: larger than %d bytes", filepath.Base(path), limit)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

// WriteFile replaces path with data atomically: it writes the temp file
// .<name>.tmp beside it (a name no import or glob of ours matches), syncs
// it, gives it mode, and renames it over path. It never follows a symbolic
// link at the temp name. Two writers of one path must not run at once
// (core holds an advisory lock; the agent is a single loop).
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dir, name := filepath.Split(path)
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode) //nolint:gosec // our own fixed temp name
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode) // whatever the umask took away
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Clean(dir)); err == nil { //nolint:gosec // the file's own directory
		_ = d.Sync() // the rename survives a power cut
		_ = d.Close()
	}
	return nil
}
