package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
}

func newDeliverEnv(t *testing.T, owner, limit int64, lang string) *deliverEnv {
	t.Helper()
	var owners []int64
	if owner != 0 {
		owners = []int64{owner}
	}
	return newDeliverEnvOwners(t, owners, limit, func(int64) string { return lang })
}

// newDeliverEnvOwners is newDeliverEnv for several owners, the first the
// main one, each in the language langOf gives.
func newDeliverEnvOwners(t *testing.T, owners []int64, limit int64, langOf func(int64) string) *deliverEnv {
	t.Helper()
	fake := tgfake.New()
	api := httptest.NewServer(fake.Handler())
	t.Cleanup(api.Close)
	h := Deliver(Delivery{
		Bot: tg.New("123:abc", tg.WithAPIRoot(api.URL)), Token: backupTok, Owners: owners, Limit: limit,
		Texts: i18n.MustLoad(), Lang: func(_ context.Context, owner int64) string { return langOf(owner) },
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &deliverEnv{fake: fake, api: api.URL, srv: srv}
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
	h := Deliver(Delivery{Owners: []int64{backupOwner}, Limit: CloudUploadLimit})
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
	h := Deliver(Delivery{Token: backupTok, Owners: []int64{backupOwner}, Limit: CloudUploadLimit})
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

	e.fake.FailChat(backupOwner, tgfake.Blocked())
	if _, out := e.post(t, backupTok, backupName, "", []byte("data")); out["reason"] != ReasonBlocked {
		t.Errorf("blocked: %v", out)
	}
}

// streamSender reads an upload as Telegram would, and closes started once
// the first part has arrived.
type streamSender struct {
	first   int
	started chan struct{}
	got     []byte
}

func (s *streamSender) SendMessage(context.Context, int64, string, *tg.Keyboard) (*tg.Message, error) {
	return &tg.Message{MessageID: 1}, nil
}

func (s *streamSender) SendDocument(context.Context, int64, string, string, *tg.Keyboard) (*tg.Message, error) {
	return &tg.Message{MessageID: 2}, nil
}

func (s *streamSender) SendDocumentUpload(_ context.Context, _ int64, _ string, r io.Reader, size int64, _ string) (*tg.Message, error) {
	head := make([]byte, s.first)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, err
	}
	close(s.started)
	rest, err := io.ReadAll(io.LimitReader(r, size-int64(s.first)))
	if err != nil {
		return nil, err
	}
	s.got = append(head, rest...)
	return &tg.Message{MessageID: 77}, nil
}

func deliverRequest(body io.Reader, size int64) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/backups/deliver", body)
	req.ContentLength = size
	req.Header.Set("Authorization", "Bearer "+backupTok)
	req.Header.Set("X-Backup-Name", backupName)
	return req
}

// The backup goes to Telegram as it arrives, never waiting for the whole
// file: through a local Bot API it may be 2000 MB, and the bot's /tmp is a
// tmpfs inside its 512 MB memory limit.
func TestDeliverStreamsTheBody(t *testing.T) {
	data := randomBackup(t, 256<<10)
	half := len(data) / 2
	bot := &streamSender{first: half, started: make(chan struct{})}
	h := Deliver(Delivery{Bot: bot, Token: backupTok, Owners: []int64{backupOwner}, Limit: LocalUploadLimit, Texts: i18n.MustLoad()})
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(data[:half])
		select {
		case <-bot.started:
			_, _ = pw.Write(data[half:])
			_ = pw.Close()
		case <-time.After(5 * time.Second):
			_ = pw.CloseWithError(errors.New("the upload did not start before the whole backup arrived"))
		}
	}()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deliverRequest(pr, int64(len(data))))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out["sent"] != true || out["message_id"] != float64(77) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !bytes.Equal(bot.got, data) {
		t.Fatalf("uploaded %d bytes, not the backup", len(bot.got))
	}
}

// A body that ends before its Content-Length, or breaks, is refused, and
// Telegram never gets a whole file.
func TestDeliverRefusesAShortBody(t *testing.T) {
	fake := tgfake.New()
	api := httptest.NewServer(fake.Handler())
	t.Cleanup(api.Close)
	h := Deliver(Delivery{Bot: tg.New("123:abc", tg.WithAPIRoot(api.URL)), Token: backupTok, Owners: []int64{backupOwner},
		Limit: CloudUploadLimit, Texts: i18n.MustLoad()})

	data := randomBackup(t, 64<<10)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deliverRequest(bytes.NewReader(data[:1000]), int64(len(data))))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ended before Content-Length") {
		t.Fatalf("short body: %d %s", rec.Code, rec.Body)
	}

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(data[:4096])
		_ = pw.CloseWithError(errors.New("connection reset"))
	}()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, deliverRequest(pr, int64(len(data))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("broken body: %d %s", rec.Code, rec.Body)
	}
	if sent := fake.Sent(); len(sent) != 0 {
		t.Fatalf("sent: %+v", sent)
	}
}

// Every owner gets the backup, the main one by the upload and the others by
// Telegram's file id, each caption in that owner's language.
func TestDeliverCopiesTheBackupToEveryOwner(t *testing.T) {
	const second, third = int64(5151), int64(6262)
	langs := map[int64]string{backupOwner: "fa", second: "en", third: "fa"}
	e := newDeliverEnvOwners(t, []int64{backupOwner, second, third}, CloudUploadLimit, func(o int64) string { return langs[o] })
	data := randomBackup(t, 1<<20+5)
	code, out := e.post(t, backupTok, backupName, "", data)
	if code != http.StatusOK || out["sent"] != true || out["message_id"] == nil {
		t.Fatalf("%d %v", code, out)
	}
	sent := e.fake.Sent()
	if len(sent) != 3 {
		t.Fatalf("sent: %+v", sent)
	}
	sum := sha256.Sum256(data)
	if sent[0].Method != "sendDocument" || sent[0].ChatID != backupOwner || sent[0].FileSize != int64(len(data)) ||
		sent[0].FileSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("the upload goes to the main owner: %+v", sent[0])
	}
	for i, want := range []struct {
		chat int64
		word string
	}{{second, "backup"}, {third, "پشتیبان"}} {
		got := sent[i+1]
		if got.Method != "sendDocument" || got.ChatID != want.chat || got.FileName != "" ||
			!strings.Contains(got.Text, backupName) || !strings.Contains(strings.ToLower(got.Text), want.word) {
			t.Errorf("copy %d: %+v", i, got)
		}
	}
}

// An owner who cannot be reached does not fail the delivery, and the owners
// after them still get the file.
func TestDeliverCopyFailureIsOnlyLogged(t *testing.T) {
	const second, third = int64(5151), int64(6262)
	e := newDeliverEnvOwners(t, []int64{backupOwner, second, third}, CloudUploadLimit, func(int64) string { return "en" })
	e.fake.FailChat(second, tgfake.Blocked())
	code, out := e.post(t, backupTok, backupName, "", randomBackup(t, 4096))
	if code != http.StatusOK || out["sent"] != true {
		t.Fatalf("%d %v", code, out)
	}
	var chats []int64
	for _, s := range e.fake.Sent() {
		chats = append(chats, s.ChatID)
	}
	if len(chats) != 2 || chats[0] != backupOwner || chats[1] != third {
		t.Fatalf("delivered to %v", chats)
	}
}

// A file too big for Telegram is kept on the server, and every owner is told.
func TestDeliverTooBigWarnsEveryOwner(t *testing.T) {
	const second = int64(5151)
	e := newDeliverEnvOwners(t, []int64{backupOwner, second}, 1<<10, func(int64) string { return "en" })
	code, out := e.post(t, backupTok, backupName, "", randomBackup(t, 2<<10))
	if code != http.StatusOK || out["sent"] != false || out["reason"] != ReasonTooBig {
		t.Fatalf("%d %v", code, out)
	}
	sent := e.fake.Sent()
	if len(sent) != 2 || sent[0].ChatID != backupOwner || sent[1].ChatID != second ||
		!strings.Contains(sent[1].Text, "kept on the server") {
		t.Fatalf("warnings: %+v", sent)
	}
}
