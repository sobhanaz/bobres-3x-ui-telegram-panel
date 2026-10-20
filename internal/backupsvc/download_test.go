package backupsvc

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

const downloadToken = "core-token-for-tests" //nolint:gosec // a test value

func getFile(t *testing.T, h http.Handler, token, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/internal/backups/file?name="+url.QueryEscape(name), nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The backup service gives core a backup file as it is on disk, encrypted, and nothing else.
func TestDownloadServesTheEncryptedFileAsItIs(t *testing.T) {
	r, dir := newRunner(t, nil)
	sc, err := r.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h := Download(r, downloadToken)
	disk, err := os.ReadFile(filepath.Join(dir, sc.File))
	if err != nil {
		t.Fatal(err)
	}
	rec := getFile(t, h, downloadToken, sc.File)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), disk) {
		t.Fatalf("%d: %d bytes, the file has %d", rec.Code, rec.Body.Len(), len(disk))
	}
	if rec.Header().Get("Content-Length") != strconv.Itoa(len(disk)) || rec.Header().Get("Content-Type") != "application/octet-stream" ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", rec.Header())
	}
	// The key that opens it is not in what is sent.
	if bytes.Contains(rec.Body.Bytes(), testKey) || bytes.Contains(rec.Body.Bytes(), []byte("PGDMP fake")) {
		t.Fatal("the download shows the key or the dump in the clear")
	}
	if _, err := backup.Verify(bytes.NewReader(rec.Body.Bytes()), testKey); err != nil {
		t.Fatalf("what was sent is not a backup the key opens: %v", err)
	}
}

func TestDownloadRefusesWhatIsNotACoreRequestForABackup(t *testing.T) {
	r, dir := newRunner(t, nil)
	sc, err := r.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h := Download(r, downloadToken)
	// A secret in the folder, a link to it, a sidecar, a temporary file and a pipe-like name are not served.
	secret := filepath.Join(t.TempDir(), "secret.bkp")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := "bobres-20250101T000000Z.bkp"
	if err := os.Symlink(secret, filepath.Join(dir, linked)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "bobres-20250202T000000Z.bkp"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		token, file string
		want        int
	}{
		"no token":        {"", sc.File, http.StatusUnauthorized},
		"another token":   {"x", sc.File, http.StatusUnauthorized},
		"a path":          {downloadToken, "../" + sc.File, http.StatusBadRequest},
		"an absolute":     {downloadToken, secret, http.StatusBadRequest},
		"a sidecar":       {downloadToken, backup.SidecarName(sc.File), http.StatusBadRequest},
		"a temp file":     {downloadToken, ".bobres-123.tmp", http.StatusBadRequest},
		"a hidden name":   {downloadToken, "." + sc.File, http.StatusBadRequest},
		"no name":         {downloadToken, "", http.StatusBadRequest},
		"a missing file":  {downloadToken, "bobres-20240101T000000Z.bkp", http.StatusNotFound},
		"a link":          {downloadToken, linked, http.StatusNotFound},
		"a directory":     {downloadToken, "bobres-20250202T000000Z.bkp", http.StatusNotFound},
		"a name with nul": {downloadToken, sc.File + "\x00.json", http.StatusBadRequest},
	} {
		rec := getFile(t, h, tc.token, tc.file)
		if rec.Code != tc.want {
			t.Errorf("%s: %d, want %d", name, rec.Code, tc.want)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("secret")) {
			t.Errorf("%s: the body shows a file outside the backups", name)
		}
	}
}

// A backup is not read into memory to be served: the handler copies it in chunks.
func TestDownloadStreamsALargeFile(t *testing.T) {
	r, dir := newRunner(t, nil)
	name := "bobres-20261011T033000Z.bkp"
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil { // 64 MiB, sparse
		t.Fatal(err)
	}
	_ = f.Close()
	srv := httptest.NewServer(Download(r, downloadToken))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/internal/backups/file?name="+name, nil)
	req.Header.Set("Authorization", "Bearer "+downloadToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || n != 64<<20 || resp.ContentLength != 64<<20 {
		t.Fatalf("%d bytes of %d: %v", n, resp.ContentLength, err)
	}
}
