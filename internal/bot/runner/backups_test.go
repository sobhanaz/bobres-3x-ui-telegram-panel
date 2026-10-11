package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgfake"
)

const (
	backupTok   = "backup-token-0123456789abcdef0123"
	backupOwner = int64(4242)
	backupName  = "bobres-20261011T033000Z.bkp"
)

// deliverEnv is Deliver behind a real server (so the deadlines are real),
// sending through the fake Bot API.
type deliverEnv struct {
	fake *tgfake.Server
	api  string // the fake's root
	srv  *httptest.Server
	tmp  string
}

func newDeliverEnv(t *testing.T, owner, limit int64, lang string) *deliverEnv {
	t.Helper()
	fake := tgfake.New()
	api := httptest.NewServer(fake.Handler())
	t.Cleanup(api.Close)
	tmp := t.TempDir()
	h := Deliver(Delivery{
		Bot: tg.New("123:abc", tg.WithAPIRoot(api.URL)), Token: backupTok, Owner: owner, Limit: limit,
		Texts: i18n.MustLoad(), Lang: func(context.Context) string { return lang }, TempDir: tmp,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &deliverEnv{fake: fake, api: api.URL, srv: srv, tmp: tmp}
}

func (e *deliverEnv) post(t *testing.T, token, name, query string, body []byte) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/internal/backups/deliver"+query, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if name != "" {
		req.Header.Set("X-Backup-Name", name)
	}
	req.Header.Set("X-Chat-Id", "999") // ignored: the owner is the only target
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (e *deliverEnv) assertSpoolGone(t *testing.T) {
	t.Helper()
	if left, _ := os.ReadDir(e.tmp); len(left) != 0 {
		t.Errorf("spooled file left behind: %v", left)
	}
}

func randomBackup(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDeliverOnlyForTheBackupService(t *testing.T) {
	e := newDeliverEnv(t, backupOwner, CloudUploadLimit, "en")
	for _, tok := range []string{"", "wrong-token-0123456789abcdef0123", coreTok} {
		if code, _ := e.post(t, tok, backupName, "", []byte("data")); code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", tok, code)
		}
	}
	// A handler built without a token refuses everyone, even an empty bearer.
	h := Deliver(Delivery{Owner: backupOwner, Limit: CloudUploadLimit})
	if code, _ := call(t, h, http.MethodPost, "/internal/backups/deliver", ""); code != http.StatusUnauthorized {
		t.Errorf("no token configured: %d", code)
	}
	if sent := e.fake.Sent(); len(sent) != 0 {
		t.Errorf("sent without the backup token: %+v", sent)
	}
}

func TestDeliverRefusesBadNamesAndSizes(t *testing.T) {
	e := newDeliverEnv(t, backupOwner, CloudUploadLimit, "en")
	for _, name := range []string{"x.bkp", "../bobres-20261011T033000Z.bkp", "bobres-20261011T033000Z.json",
		"bobres-20261399T996699Z.bkp", "bobres-20261011T033000Z.bkp.exe", "bobres-2026-10-11.bkp"} {
		if code, _ := e.post(t, backupTok, name, "", []byte("data")); code != http.StatusBadRequest {
			t.Errorf("name %q: %d", name, code)
		}
	}
	if code, _ := e.post(t, backupTok, "", "", []byte("data")); code != http.StatusBadRequest {
		t.Errorf("no name: %d", code)
	}
	if code, _ := e.post(t, backupTok, backupName, "", nil); code != http.StatusBadRequest {
		t.Errorf("empty body: %d", code)
	}
	h := Deliver(Delivery{Token: backupTok, Owner: backupOwner, Limit: CloudUploadLimit})
	for size, want := range map[int64]int{-1: http.StatusLengthRequired, MaxBackup + 1: http.StatusRequestEntityTooLarge} {
		req := httptest.NewRequest(http.MethodPost, "/internal/backups/deliver", strings.NewReader("x"))
		req.Header.Set("Authorization", "Bearer "+backupTok)
		req.Header.Set("X-Backup-Name", backupName)
		req.ContentLength = size
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Content-Length %d: %d, want %d", size, rec.Code, want)
		}
	}
	if sent := e.fake.Sent(); len(sent) != 0 {
		t.Errorf("sent: %+v", sent)
	}
}

func TestDeliverWithoutOwner(t *testing.T) {
	e := newDeliverEnv(t, 0, CloudUploadLimit, "en")
	code, out := e.post(t, backupTok, backupName, "?chat_id=999", []byte("data"))
	if code != http.StatusConflict || out["sent"] != false || out["reason"] != ReasonNoOwner {
		t.Fatalf("%d %v", code, out)
	}
	if sent := e.fake.Sent(); len(sent) != 0 {
		t.Errorf("sent with no owner: %+v", sent)
	}
}

func TestDeliverTooBigWarnsTheOwner(t *testing.T) {
	e := newDeliverEnv(t, backupOwner, 1<<10, "en")
	code, out := e.post(t, backupTok, backupName, "", randomBackup(t, 2<<10))
	if code != http.StatusOK || out["sent"] != false || out["reason"] != ReasonTooBig || out["error"] != nil {
		t.Fatalf("%d %v", code, out)
	}
	sent := e.fake.Sent()
	if len(sent) != 1 || sent[0].Method != "sendMessage" || sent[0].ChatID != backupOwner ||
		!strings.Contains(sent[0].Text, backupName) || !strings.Contains(sent[0].Text, "kept on the server") {
		t.Fatalf("warning: %+v", sent)
	}
	e.assertSpoolGone(t)
	if UploadLimit(false) != 50<<20 || UploadLimit(true) != 2000<<20 {
		t.Error("upload limits")
	}
}

func TestDeliverUploadsToTheOwner(t *testing.T) {
	e := newDeliverEnv(t, backupOwner, CloudUploadLimit, "fa")
	data := randomBackup(t, 3<<20+17)
	code, out := e.post(t, backupTok, backupName, "?chat_id=999", data)
	if code != http.StatusOK || out["sent"] != true || out["message_id"] == nil {
		t.Fatalf("%d %v", code, out)
	}
	sent := e.fake.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	sum := sha256.Sum256(data)
	got := sent[0]
	if got.Method != "sendDocument" || got.ChatID != backupOwner || got.FileName != backupName ||
		got.FileSize != int64(len(data)) || got.FileSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("upload: %+v", got)
	}
	if !strings.Contains(got.Text, backupName) || !strings.Contains(got.Text, "پشتیبان") || strings.Contains(got.Text, "<") {
		t.Errorf("caption: %q", got.Text)
	}
	e.assertSpoolGone(t)
}

func TestDeliverNeverRetriesAConsumedUpload(t *testing.T) {
	e := newDeliverEnv(t, backupOwner, CloudUploadLimit, "en")
	e.fake.FailChat(backupOwner, tgfake.Flood(1, 2))
	start := time.Now()
	code, out := e.post(t, backupTok, backupName, "", randomBackup(t, 64<<10))
	if code != http.StatusOK || out["sent"] != false || out["reason"] != ReasonTelegram ||
		!strings.Contains(out["error"].(string), "Too Many Requests") {
		t.Fatalf("%d %v", code, out)
	}
	if time.Since(start) > 900*time.Millisecond {
		t.Error("waited for a retry")
	}
	// One of the two 429s is left: the upload was tried exactly once.
	c := tg.New("123:abc", tg.WithAPIRoot(e.api), tg.WithNoRetry())
	if _, err := c.SendMessage(context.Background(), backupOwner, "probe", nil); err == nil {
		t.Error("both 429s used: the upload was sent twice")
	}
	if sent := e.fake.Sent(); len(sent) != 0 {
		t.Errorf("sent: %+v", sent)
	}
	e.assertSpoolGone(t)

	e.fake.FailChat(backupOwner, tgfake.Blocked())
	if _, out := e.post(t, backupTok, backupName, "", []byte("data")); out["reason"] != ReasonBlocked {
		t.Errorf("blocked: %v", out)
	}
}
