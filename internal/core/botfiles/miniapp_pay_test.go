package botfiles

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// fakeBot answers the Mini App's payment endpoints with the status and body
// it is told, and keeps the last request.
type fakeBot struct {
	status int
	body   string
	req    *http.Request
	data   []byte
}

func (f *fakeBot) serve(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f.req = r
		f.data, _ = io.ReadAll(r.Body)
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "tok")
}

func TestCreateInvoiceLink(t *testing.T) {
	f := &fakeBot{status: 200, body: `{"ok":true,"url":"https://t.me/$AbCdEf-_12"}`}
	c := f.serve(t)
	ctx := context.Background()
	in := InvoiceRequest{Payload: "0a1b2c3d-1111-4222-8333-444455556666", Stars: 100, Lang: "fa", Title: "ماهانه", Amount: 150000, Currency: "IRT"}

	link, err := c.CreateInvoiceLink(ctx, in)
	if err != nil || link != "https://t.me/$AbCdEf-_12" {
		t.Fatalf("link %q: %v", link, err)
	}
	if f.req.URL.Path != "/internal/miniapp/invoice" || f.req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("request: %s %s", f.req.URL, f.req.Header.Get("Content-Type"))
	}
	want := `{"payload":"0a1b2c3d-1111-4222-8333-444455556666","stars":100,"lang":"fa","title":"ماهانه","amount":150000,"currency":"IRT"}`
	if string(f.data) != want {
		t.Fatalf("body %s, want %s", f.data, want)
	}
	in.Topup = true
	if _, err := c.CreateInvoiceLink(ctx, in); err != nil || !strings.Contains(string(f.data), `"topup":true`) {
		t.Fatalf("top-up body %s: %v", f.data, err)
	}

	// Telegram's refusal, a busy bot, and trouble are told apart.
	f.status, f.body = http.StatusUnprocessableEntity, `{"ok":false,"problem":"refused","detail":"Bad Request: AMOUNT_TOO_BIG"}`
	if _, err := c.CreateInvoiceLink(ctx, in); !errors.Is(err, ErrInvoiceRefused) || !strings.Contains(err.Error(), "AMOUNT_TOO_BIG") {
		t.Errorf("refused: %v", err)
	}
	f.status, f.body = http.StatusTooManyRequests, `{"ok":false,"problem":"flood"}`
	if _, err := c.CreateInvoiceLink(ctx, in); !errors.Is(err, ErrBotBusy) {
		t.Errorf("flood: %v", err)
	}
	for name, c2 := range map[string]struct {
		status int
		body   string
	}{
		"the bot failed":             {http.StatusBadGateway, `{"ok":false,"problem":"error","detail":"telegram: 502"}`},
		"an answer that is not json": {http.StatusOK, `<html>`},
		"ok false":                   {http.StatusOK, `{"ok":false}`},
		"no url":                     {http.StatusOK, `{"ok":true}`},
		"a link to elsewhere":        {http.StatusOK, `{"ok":true,"url":"https://evil.example/pay"}`},
		"not https":                  {http.StatusOK, `{"ok":true,"url":"http://t.me/$abc"}`},
		"a script":                   {http.StatusOK, `{"ok":true,"url":"javascript:alert(1)"}`},
		"a look-alike host":          {http.StatusOK, `{"ok":true,"url":"https://t.me.evil.example/$abc"}`},
		"a link with a space":        {http.StatusOK, `{"ok":true,"url":"https://t.me/$abc def"}`},
		"a link with a quote":        {http.StatusOK, `{"ok":true,"url":"https://t.me/$abc\"onload=1"}`},
	} {
		f.status, f.body = c2.status, c2.body
		if link, err := c.CreateInvoiceLink(ctx, in); err == nil || errors.Is(err, ErrInvoiceRefused) || errors.Is(err, ErrBotBusy) || link != "" {
			t.Errorf("%s: %q %v", name, link, err)
		}
	}
	// A bot that does not know core's token.
	if _, err := New(f.serveURL(t), "wrong").CreateInvoiceLink(ctx, in); err == nil {
		t.Error("a wrong token worked")
	}
}

// serveURL is the URL of a fake of its own.
func (f *fakeBot) serveURL(t *testing.T) string {
	t.Helper()
	return f.serve(t).base
}

func TestSendReceipt(t *testing.T) {
	f := &fakeBot{status: 200, body: `{"ok":true,"file_id":"AgACAgQAAxkBAAIB-fileid_1","kind":"photo"}`}
	c := f.serve(t)
	ctx := context.Background()
	r := Receipt{ChatID: 5151, Lang: "fa", Reference: "AB12CD34", ContentType: "image/png", Data: []byte("\x89PNG pretend")}

	id, err := c.SendReceipt(ctx, r)
	if err != nil || id != "AgACAgQAAxkBAAIB-fileid_1" {
		t.Fatalf("file id %q: %v", id, err)
	}
	q, _ := url.ParseQuery(f.req.URL.RawQuery)
	if f.req.URL.Path != "/internal/miniapp/receipt" || q.Get("chat") != "5151" || q.Get("lang") != "fa" || q.Get("ref") != "AB12CD34" ||
		f.req.Header.Get("Content-Type") != "image/png" || f.req.ContentLength != int64(len(r.Data)) || !bytes.Equal(f.data, r.Data) {
		t.Fatalf("request: %s type %q length %d body %q", f.req.URL, f.req.Header.Get("Content-Type"), f.req.ContentLength, f.data)
	}
	if got := f.req.Header.Get("Transfer-Encoding"); got != "" {
		t.Fatalf("the body must go with its Content-Length, not chunked: %q", got)
	}
	if strings.Contains(f.req.URL.String(), "PNG") {
		t.Fatal("the image is in the URL")
	}

	for name, c2 := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"too large":        {http.StatusRequestEntityTooLarge, `{"ok":false,"problem":"too_large"}`, ErrReceiptTooLarge},
		"not an image":     {http.StatusUnsupportedMediaType, `{"ok":false,"problem":"bad_type"}`, ErrReceiptType},
		"telegram refuses": {http.StatusUnprocessableEntity, `{"ok":false,"problem":"refused"}`, ErrReceiptType},
		"chat closed":      {http.StatusConflict, `{"ok":false,"problem":"blocked"}`, ErrChatClosed},
		"busy":             {http.StatusTooManyRequests, `{"ok":false,"problem":"busy"}`, ErrBotBusy},
		"flood":            {http.StatusTooManyRequests, `{"ok":false,"problem":"flood"}`, ErrBotBusy},
	} {
		f.status, f.body = c2.status, c2.body
		if id, err := c.SendReceipt(ctx, r); !errors.Is(err, c2.want) || id != "" {
			t.Errorf("%s: %q %v", name, id, err)
		}
	}
	for name, c2 := range map[string]struct {
		status int
		body   string
	}{
		"the bot failed":      {http.StatusBadGateway, `{"ok":false,"problem":"error"}`},
		"not json":            {http.StatusOK, `ok`},
		"ok false":            {http.StatusOK, `{"ok":false}`},
		"no file id":          {http.StatusOK, `{"ok":true}`},
		"a file id with junk": {http.StatusOK, `{"ok":true,"file_id":"abc def; drop table"}`},
		"a file id too short": {http.StatusOK, `{"ok":true,"file_id":"abc"}`},
		"a file id too long":  {http.StatusOK, `{"ok":true,"file_id":"` + strings.Repeat("a", 257) + `"}`},
		"a bad request":       {http.StatusBadRequest, `{"ok":false,"problem":"bad_request"}`},
	} {
		f.status, f.body = c2.status, c2.body
		id, err := c.SendReceipt(ctx, r)
		if err == nil || id != "" || errors.Is(err, ErrReceiptType) || errors.Is(err, ErrChatClosed) || errors.Is(err, ErrBotBusy) || errors.Is(err, ErrReceiptTooLarge) {
			t.Errorf("%s: %q %v", name, id, err)
		}
	}
	if _, err := New(f.serveURL(t), "wrong").SendReceipt(ctx, r); err == nil {
		t.Error("a wrong token worked")
	}
	// An error ends up in logs: it names the route, not the customer's chat or
	// the payment's reference (the query), nor the image.
	_, err = New("http://127.0.0.1:1", "tok").SendReceipt(ctx, r)
	if err == nil || !strings.Contains(err.Error(), "/internal/miniapp/receipt") {
		t.Fatalf("an unreachable bot: %v", err)
	}
	for _, leak := range []string{"PNG", "5151", "AB12CD34", "chat=", "?"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the error shows %q: %v", leak, err)
		}
	}
	// A deadline is still a deadline to whoever asks.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.SendReceipt(gone, r); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call: %v", err)
	}
}

func TestNotifyProof(t *testing.T) {
	f := &fakeBot{status: http.StatusNoContent}
	c := f.serve(t)
	ctx := context.Background()
	n := ProofNotice{TelegramID: 5151, Username: "mimi", IntentID: "0a1b2c3d-1111-4222-8333-444455556666", Amount: 150000, Currency: "IRT"}
	if err := c.NotifyProof(ctx, n); err != nil {
		t.Fatal(err)
	}
	want := `{"telegram_id":5151,"username":"mimi","intent_id":"0a1b2c3d-1111-4222-8333-444455556666","amount":150000,"currency":"IRT"}`
	if f.req.URL.Path != "/internal/miniapp/proof-alert" || string(f.data) != want {
		t.Fatalf("request: %s %s", f.req.URL, f.data)
	}
	f.status, f.body = http.StatusBadRequest, `{"ok":false,"problem":"bad_request"}`
	if err := c.NotifyProof(ctx, n); err == nil || !strings.Contains(err.Error(), strconv.Itoa(http.StatusBadRequest)) {
		t.Errorf("a refusal: %v", err)
	}
	f.status, f.body = http.StatusOK, ``
	if err := c.NotifyProof(ctx, n); err == nil {
		t.Error("an answer that is not 204 was taken as sent")
	}
	if err := New(f.serveURL(t), "wrong").NotifyProof(ctx, n); err == nil {
		t.Error("a wrong token worked")
	}
}
