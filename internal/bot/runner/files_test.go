package runner

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgfake"
)

// Core fetches receipt photos through the bot: only with its token, only
// images and PDFs, and the bot token never shows.
func TestFilesServesReceiptsToCoreOnly(t *testing.T) {
	fake := tgfake.New()
	tgSrv := httptest.NewServer(fake.Handler())
	defer tgSrv.Close()
	jpeg := append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), bytes.Repeat([]byte{1}, 64)...)
	fake.AddFile("AgACAgIAAxkBAAIBpg", jpeg)
	fake.AddFile("BQACAgIAAxkBAAIBph", []byte("just some text, not an image"))
	bot := tg.New("123:secret-bot-token", tg.WithAPIRoot(tgSrv.URL))

	mux := http.NewServeMux()
	mux.Handle("GET /internal/files/{id}", Files(bot, "core-token-0123456789abcdef0123", slog.New(slog.DiscardHandler)))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	get := func(id, token string) (int, string, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/internal/files/"+id, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), body
	}
	const good = "core-token-0123456789abcdef0123"
	if code, _, _ := get("AgACAgIAAxkBAAIBpg", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	if code, _, _ := get("AgACAgIAAxkBAAIBpg", "wrong-token-0123456789abcdef012"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", code)
	}
	code, ctype, body := get("AgACAgIAAxkBAAIBpg", good)
	if code != http.StatusOK || ctype != "image/jpeg" || !bytes.Equal(body, jpeg) {
		t.Fatalf("photo: %d %q %d bytes", code, ctype, len(body))
	}
	if code, _, _ := get("BQACAgIAAxkBAAIBph", good); code != http.StatusUnsupportedMediaType {
		t.Fatalf("a text file must not be served: %d", code)
	}
	if code, _, _ := get("AgACAgIAAxkBAAIZZZ", good); code != http.StatusNotFound {
		t.Fatalf("unknown file: %d", code)
	}
	if code, _, body := get("bad..id", good); code != http.StatusBadRequest || bytes.Contains(body, []byte("secret-bot-token")) {
		t.Fatalf("bad id: %d %s", code, body)
	}
}
