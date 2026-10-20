package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/receipt"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgfake"
)

const (
	payID      = "0a1b2c3d-1111-4222-8333-444455556666"
	customerTG = int64(5151)
)

// payEnv is the Mini App's payment endpoints in front of the fake Bot API.
type payEnv struct {
	fake *tgfake.Server
	bot  *tg.Client
	logs *bytes.Buffer
	log  *slog.Logger
}

func newPayEnv(t *testing.T) *payEnv {
	t.Helper()
	fake := tgfake.New()
	api := httptest.NewServer(fake.Handler())
	t.Cleanup(api.Close)
	logs := &bytes.Buffer{}
	return &payEnv{fake: fake, bot: tg.New("123:abc", tg.WithAPIRoot(api.URL), tg.WithNoRetry()), logs: logs,
		log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
}

// do sends a request straight to the handler; Content-Length is the body's,
// unless it is set by the caller (-1: unknown).
func do(h http.Handler, method, target, token, ctype string, body []byte) (int, map[string]any, http.Header) {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Header()
}

// ---- invoice links ----

func (e *payEnv) invoice(body any) (int, map[string]any, http.Header) {
	raw, _ := json.Marshal(body)
	return do(MiniAppInvoice(e.bot, i18n.MustLoad(), coreTok, e.log), http.MethodPost, "/internal/miniapp/invoice", coreTok, "application/json", raw)
}

func TestMiniAppInvoiceOnlyForCore(t *testing.T) {
	e := newPayEnv(t)
	h := MiniAppInvoice(e.bot, i18n.MustLoad(), coreTok, nil)
	body := []byte(`{"payload":"` + payID + `","stars":100,"lang":"en","amount":150000,"currency":"IRT"}`)
	for _, tok := range []string{"", "wrong-token-0123456789abcdef0123", "Bearer " + coreTok} {
		if code, _, _ := do(h, http.MethodPost, "/internal/miniapp/invoice", tok, "application/json", body); code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", tok, code)
		}
	}
	// A handler built without a token refuses everyone, even an empty bearer.
	if code, _, _ := do(MiniAppInvoice(e.bot, i18n.MustLoad(), "", nil), http.MethodPost, "/", "", "application/json", body); code != http.StatusUnauthorized {
		t.Errorf("no token configured: %d", code)
	}
	if n := len(e.fake.InvoiceLinks()); n != 0 {
		t.Errorf("%d links made without core's token", n)
	}
}

// The invoice is the one the bot sends as a message: the plan's name (or the
// store's text) as the title, "Payment of <amount>" as the description, in the
// customer's language, the payment's id as the payload, XTR and an empty
// provider token.
func TestMiniAppInvoiceIsTheBotsInvoiceAsALink(t *testing.T) {
	e := newPayEnv(t)
	code, out, _ := e.invoice(map[string]any{"payload": payID, "stars": 100, "lang": "en", "title": "Monthly 50GB", "amount": 150000, "currency": "IRT"})
	links := e.fake.InvoiceLinks()
	if code != http.StatusOK || out["ok"] != true || len(links) != 1 || out["url"] != links[0].URL || !strings.HasPrefix(links[0].URL, "https://t.me/") {
		t.Fatalf("link: %d %v %+v", code, out, links)
	}
	l := links[0]
	if l.Title != "Monthly 50GB" || l.Description != "Payment of 150,000 Toman" || l.Payload != payID || l.Stars != 100 || l.Currency != "XTR" || l.ProviderToken != "" {
		t.Fatalf("invoice: %+v", l)
	}

	// Persian, no plan name: the store's own title and Persian digits.
	e.invoice(map[string]any{"payload": payID, "stars": 7, "lang": "fa", "amount": 10000, "currency": "IRT"})
	cat := i18n.MustLoad()
	l = e.fake.InvoiceLinks()[1]
	if l.Title != cat.T("fa", "pay.stars_title") || l.Description != cat.T("fa", "pay.stars_desc", "amount", i18n.Money("fa", 10000, "IRT")) ||
		!strings.Contains(l.Description, "۱۰٬۰۰۰") || l.Stars != 7 {
		t.Fatalf("persian invoice: %+v", l)
	}

	// A top-up has the top-up title whatever name it is given; a USDT price is written in USDT.
	e.invoice(map[string]any{"payload": payID, "stars": 3, "lang": "en", "title": "ignored", "amount": 2_500_000, "currency": "USDT", "topup": true})
	l = e.fake.InvoiceLinks()[2]
	if l.Title != cat.T("en", "pay.topup_title") || !strings.Contains(l.Description, "2.5") {
		t.Fatalf("top-up invoice: %+v", l)
	}

	// Telegram's limits: a long name is cut to 32 UTF-16 units on a character boundary.
	e.invoice(map[string]any{"payload": payID, "stars": 1, "lang": "en", "title": strings.Repeat("x", 80), "amount": 1, "currency": "IRT"})
	e.invoice(map[string]any{"payload": payID, "stars": 1, "lang": "en", "title": strings.Repeat("😀", 20), "amount": 1, "currency": "IRT"})
	e.invoice(map[string]any{"payload": payID, "stars": 1, "lang": "fa", "title": "  a\n b\t c ", "amount": 1, "currency": "IRT"})
	links = e.fake.InvoiceLinks()
	if links[3].Title != strings.Repeat("x", 32) || links[4].Title != strings.Repeat("😀", 16) || links[5].Title != "a b c" {
		t.Fatalf("titles: %q %q %q", links[3].Title, links[4].Title, links[5].Title)
	}

	// Another language falls back to the first, as the bot does.
	e.invoice(map[string]any{"payload": payID, "stars": 1, "lang": "xx", "amount": 1, "currency": "IRT"})
	if l = e.fake.InvoiceLinks()[6]; l.Title != cat.T("fa", "pay.stars_title") {
		t.Fatalf("unknown language: %+v", l)
	}
}

func TestMiniAppInvoiceRefusesNonsense(t *testing.T) {
	e := newPayEnv(t)
	ok := map[string]any{"payload": payID, "stars": 100, "lang": "en", "amount": 150000, "currency": "IRT"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range ok {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	for name, body := range map[string]map[string]any{
		"payload is not a payment id": with("payload", "not-a-payment"),
		"payload empty":               with("payload", ""),
		"payload with a quote":        with("payload", payID+`"}`),
		"no stars":                    with("stars", 0),
		"negative stars":              with("stars", -5),
		"absurd stars":                with("stars", int64(1)<<40),
		"negative amount":             with("amount", -1),
		"unknown currency":            with("currency", "EUR"),
		"no currency":                 with("currency", ""),
		"unknown field":               with("provider_token", "123:secret"),
	} {
		if code, out, _ := e.invoice(body); code != http.StatusBadRequest || out["ok"] != false || out["problem"] != ProblemBadRequest {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, _, _ := do(MiniAppInvoice(e.bot, i18n.MustLoad(), coreTok, nil), http.MethodPost, "/", coreTok, "application/json", []byte("not json")); code != http.StatusBadRequest {
		t.Errorf("not json: %d", code)
	}
	huge := bytes.Repeat([]byte("x"), 5<<10)
	if code, _, _ := do(MiniAppInvoice(e.bot, i18n.MustLoad(), coreTok, nil), http.MethodPost, "/", coreTok, "application/json", huge); code != http.StatusBadRequest {
		t.Errorf("a body over its limit: %d", code)
	}
	if n := len(e.fake.InvoiceLinks()); n != 0 {
		t.Errorf("%d links made for refused requests", n)
	}
}

// What Telegram says is told to core in a status it can act on, and the
// answer never holds the bot token.
func TestMiniAppInvoiceTelegramFailures(t *testing.T) {
	e := newPayEnv(t)
	body := map[string]any{"payload": payID, "stars": 100, "lang": "en", "amount": 150000, "currency": "IRT"}
	for name, c := range map[string]struct {
		f       tgfake.Failure
		status  int
		problem string
	}{
		"refused":      {tgfake.BadRequest("AMOUNT_TOO_BIG"), http.StatusUnprocessableEntity, ProblemRefused},
		"telegram 500": {tgfake.Failure{Code: 500, Description: "Internal Server Error"}, http.StatusBadGateway, ProblemError},
		"flood":        {tgfake.Flood(7, 1), http.StatusTooManyRequests, ProblemFlood},
	} {
		e.fake.FailInvoiceLinks(c.f)
		code, out, hdr := e.invoice(body)
		if code != c.status || out["problem"] != c.problem || out["ok"] != false || strings.Contains(out["detail"].(string), "123:abc") {
			t.Errorf("%s: %d %v", name, code, out)
		}
		if c.problem == ProblemFlood && hdr.Get("Retry-After") != "7" {
			t.Errorf("flood: Retry-After %q", hdr.Get("Retry-After"))
		}
	}
	e.fake.FailInvoiceLinks(tgfake.Failure{}) // a failure with no count lasts until it is cleared
	if code, out, _ := e.invoice(body); code != http.StatusOK || out["ok"] != true {
		t.Errorf("after the failures: %d %v", code, out)
	}
	if strings.Contains(e.logs.String(), "123:abc") {
		t.Errorf("the bot token is in the logs:\n%s", e.logs)
	}
}

// ---- receipts ----

func pngImage(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegImage(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 64, 48)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngHeaderOnly is a PNG that is a signature and an IHDR chunk with a size:
// enough to be read as a picture of that size.
func pngHeaderOnly(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(ihdr)
	b.WriteString("IHDR")
	b.Write(ihdr)
	_ = binary.Write(&b, binary.BigEndian, crc.Sum32())
	return b.Bytes()
}

// webpImage is a lossy WebP of 320 x 240 pixels as far as its header goes: the
// frame tag, the start code and the two sizes.
func webpImage() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(4+8+16))
	b.WriteString("WEBPVP8 ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	b.Write([]byte{0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0x40, 0x01, 0xf0, 0x00})
	b.Write(make([]byte, 6))
	return b.Bytes()
}

func (e *payEnv) handler() http.Handler {
	return MiniAppReceipt(e.bot, i18n.MustLoad(), coreTok, e.log)
}

const receiptURL = "/internal/miniapp/receipt?chat=5151&lang=en&ref=AB12CD34"

func TestMiniAppReceiptOnlyForCore(t *testing.T) {
	e := newPayEnv(t)
	for _, tok := range []string{"", "wrong-token-0123456789abcdef0123"} {
		if code, _, _ := do(e.handler(), http.MethodPost, receiptURL, tok, "image/png", pngImage(t, 8, 8)); code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", tok, code)
		}
	}
	if code, _, _ := do(MiniAppReceipt(e.bot, i18n.MustLoad(), "", nil), http.MethodPost, receiptURL, "", "image/png", pngImage(t, 8, 8)); code != http.StatusUnauthorized {
		t.Errorf("no token configured: %d", code)
	}
	if sent := e.fake.Sent(); len(sent) != 0 {
		t.Errorf("sent without core's token: %+v", sent)
	}
}

// A picture Telegram takes as a photo goes as one: the customer's own chat
// gets it with a caption in their language, and the answer is the file id of
// the photo, like the one the bot reads from a photo a customer sends.
func TestMiniAppReceiptPhotoAndDocument(t *testing.T) {
	e := newPayEnv(t)
	img := pngImage(t, 64, 48)
	code, out, _ := do(e.handler(), http.MethodPost, receiptURL, coreTok, "application/octet-stream", img)
	sent := e.fake.Sent()
	sum := sha256.Sum256(img)
	if code != http.StatusOK || out["ok"] != true || out["kind"] != "photo" || !strings.HasPrefix(out["file_id"].(string), "fake-photo-") || len(sent) != 1 {
		t.Fatalf("photo: %d %v %+v", code, out, sent)
	}
	if s := sent[0]; s.Method != "sendPhoto" || s.ChatID != customerTG || s.FileName != "receipt.png" || s.FileSHA256 != hex.EncodeToString(sum[:]) ||
		s.Text != "🧾 Receipt for payment AB12CD34, sent from the store app." {
		t.Fatalf("recorded: %+v", s)
	}

	// A JPEG is a photo too, in Persian when the customer reads Persian.
	jp := jpegImage(t)
	code, out, _ = do(e.handler(), http.MethodPost, "/internal/miniapp/receipt?chat=5151&lang=fa&ref=ZZ99", coreTok, "image/jpeg", jp)
	sent = e.fake.Sent()
	if code != http.StatusOK || out["kind"] != "photo" || sent[1].FileName != "receipt.jpg" || !strings.Contains(sent[1].Text, "ZZ99") ||
		sent[1].Text != i18n.MustLoad().T("fa", "pay.receipt_caption", "ref", "ZZ99") {
		t.Fatalf("jpeg: %d %v %+v", code, out, sent[1])
	}

	// A WebP, and a picture beyond Telegram's photo limits, go as documents.
	for name, data := range map[string][]byte{"webp": webpImage(), "too wide for a photo": pngHeaderOnly(6000, 5000), "too thin": pngHeaderOnly(100, 3000)} {
		before := len(e.fake.Sent())
		code, out, _ = do(e.handler(), http.MethodPost, receiptURL, coreTok, "image/png", data)
		sent = e.fake.Sent()
		if code != http.StatusOK || out["kind"] != "document" || !strings.HasPrefix(out["file_id"].(string), "fake-document-") || len(sent) != before+1 {
			t.Fatalf("%s: %d %v", name, code, out)
		}
		s := sent[before]
		wantName := "receipt-AB12CD34.png"
		if name == "webp" {
			wantName = "receipt-AB12CD34.webp"
		}
		if s.Method != "sendDocument" || s.ChatID != customerTG || s.FileName != wantName || s.FileSize != int64(len(data)) || !strings.HasPrefix(s.Text, "🧾") {
			t.Fatalf("%s recorded: %+v", name, s)
		}
	}
}

// refusesPhotos is a Telegram that takes documents only: a photo is refused
// the way Telegram refuses a picture it cannot take as one.
type refusesPhotos struct{ *tg.Client }

func (refusesPhotos) SendPhoto(context.Context, int64, tg.Photo, string, *tg.Keyboard) (*tg.Message, error) {
	return nil, &tg.APIError{Code: 400, Description: "Bad Request: PHOTO_INVALID_DIMENSIONS"}
}

func TestMiniAppReceiptFallsBackToADocument(t *testing.T) {
	e := newPayEnv(t)
	h := MiniAppReceipt(refusesPhotos{e.bot}, i18n.MustLoad(), coreTok, nil)
	code, out, _ := do(h, http.MethodPost, receiptURL, coreTok, "image/png", pngImage(t, 64, 48))
	if code != http.StatusOK || out["kind"] != "document" {
		t.Fatalf("refused photo: %d %v", code, out)
	}
	// Blocked or down is not a reason to try again as a document.
	var photoTried int
	h = MiniAppReceipt(countingPhotos{e.bot, &photoTried, &tg.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}}, i18n.MustLoad(), coreTok, nil)
	if code, out, _ = do(h, http.MethodPost, receiptURL, coreTok, "image/png", pngImage(t, 64, 48)); code != http.StatusConflict || out["problem"] != ProblemBlocked {
		t.Fatalf("blocked: %d %v", code, out)
	}
	if n := len(e.fake.Sent()); n != 1 {
		t.Fatalf("a blocked chat got a document as well: %d messages", n)
	}
}

type countingPhotos struct {
	*tg.Client
	n   *int
	err error
}

func (c countingPhotos) SendPhoto(context.Context, int64, tg.Photo, string, *tg.Keyboard) (*tg.Message, error) {
	*c.n++
	return nil, c.err
}

func TestMiniAppReceiptTelegramFailures(t *testing.T) {
	e := newPayEnv(t)
	img := pngImage(t, 64, 48)
	for name, c := range map[string]struct {
		f       tgfake.Failure
		status  int
		problem string
	}{
		"blocked":   {tgfake.Blocked(), http.StatusConflict, ProblemBlocked},
		"no chat":   {tgfake.ChatNotFound(), http.StatusConflict, ProblemBlocked},
		"refused":   {tgfake.BadRequest("wrong file identifier"), http.StatusUnprocessableEntity, ProblemRefused},
		"flood":     {tgfake.Flood(3, 0), http.StatusTooManyRequests, ProblemFlood},
		"gateway":   {tgfake.Failure{Code: 502, Description: "Bad Gateway"}, http.StatusBadGateway, ProblemError},
		"throttled": {tgfake.Failure{Code: 500, Description: "Internal Server Error"}, http.StatusBadGateway, ProblemError},
	} {
		e.fake.FailChat(customerTG, c.f)
		code, out, hdr := do(e.handler(), http.MethodPost, receiptURL, coreTok, "image/png", img)
		if code != c.status || out["ok"] != false || out["problem"] != c.problem || out["file_id"] != nil {
			t.Errorf("%s: %d %v", name, code, out)
		}
		if c.problem == ProblemFlood && hdr.Get("Retry-After") != "3" {
			t.Errorf("flood: Retry-After %q", hdr.Get("Retry-After"))
		}
	}
	e.fake.ClearChat(customerTG)
	if sent := e.fake.Sent(); len(sent) != 0 {
		t.Errorf("failed sends were recorded: %+v", sent)
	}
	// Nothing of the picture, and no file id, is ever logged.
	if logs := e.logs.String(); strings.Contains(logs, "fake-") || strings.Contains(logs, "PNG") || strings.Contains(logs, "123:abc") {
		t.Errorf("the logs hold receipt data:\n%s", logs)
	}
}

func TestMiniAppReceiptRefusesWhatIsNotAnImage(t *testing.T) {
	e := newPayEnv(t)
	for name, data := range map[string][]byte{
		"html":      []byte("<html><script>alert(1)</script></html>"),
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`),
		"pdf":       []byte("%PDF-1.7 not a picture"),
		"gif":       []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"text":      []byte("a receipt, honest"),
		"png magic": []byte("\x89PNG\r\n\x1a\nnothing more"),
	} {
		// Whatever the sender says it is: the bytes decide.
		for _, ctype := range []string{"image/png", "image/jpeg", "text/html", ""} {
			if code, out, _ := do(e.handler(), http.MethodPost, receiptURL, coreTok, ctype, data); code != http.StatusUnsupportedMediaType || out["problem"] != ProblemBadType {
				t.Errorf("%s as %q: %d %v", name, ctype, code, out)
			}
		}
	}
	if code, out, _ := do(e.handler(), http.MethodPost, receiptURL, coreTok, "image/png", nil); code != http.StatusBadRequest || out["problem"] != ProblemEmpty {
		t.Errorf("empty: %d %v", code, out)
	}
	if sent := e.fake.Sent(); len(sent) != 0 {
		t.Errorf("something that is not an image was sent: %+v", sent)
	}
}

func TestMiniAppReceiptSizeAndFraming(t *testing.T) {
	e := newPayEnv(t)
	// Over the limit by its Content-Length: refused before a byte is read.
	req := httptest.NewRequest(http.MethodPost, receiptURL, strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+coreTok)
	req.ContentLength = MaxReceipt + 1
	rec := httptest.NewRecorder()
	e.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Content-Length over the limit: %d", rec.Code)
	}
	// A body longer than it said: exactly what it said is read, and nothing beyond it.
	req = httptest.NewRequest(http.MethodPost, receiptURL, bytes.NewReader(make([]byte, MaxReceipt+10)))
	req.Header.Set("Authorization", "Bearer "+coreTok)
	req.ContentLength = 100
	rec = httptest.NewRecorder()
	e.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType { // 100 bytes of nothing: not an image
		t.Errorf("a body longer than its Content-Length: %d", rec.Code)
	}
	img := pngImage(t, 8, 8)
	req = httptest.NewRequest(http.MethodPost, receiptURL, bytes.NewReader(append(bytes.Clone(img), []byte("trailing junk the length does not cover")...)))
	req.Header.Set("Authorization", "Bearer "+coreTok)
	req.ContentLength = int64(len(img))
	rec = httptest.NewRecorder()
	e.handler().ServeHTTP(rec, req)
	if sent := e.fake.Sent(); rec.Code != http.StatusOK || len(sent) != 1 || sent[0].FileSize != int64(len(img)) {
		t.Errorf("an image followed by more than its Content-Length: %d %+v", rec.Code, sent)
	}
	// A body shorter than it said.
	req = httptest.NewRequest(http.MethodPost, receiptURL, bytes.NewReader(pngImage(t, 8, 8)))
	req.Header.Set("Authorization", "Bearer "+coreTok)
	req.ContentLength = 5000
	rec = httptest.NewRecorder()
	e.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a body shorter than its Content-Length: %d", rec.Code)
	}
	// No Content-Length at all (chunked).
	req = httptest.NewRequest(http.MethodPost, receiptURL, io.NopCloser(bytes.NewReader(pngImage(t, 8, 8))))
	req.Header.Set("Authorization", "Bearer "+coreTok)
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	e.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusLengthRequired {
		t.Errorf("no Content-Length: %d", rec.Code)
	}
	// Exactly the limit is fine (a WebP padded to 8 MB).
	big := append(webpImage(), make([]byte, receipt.MaxBytes-len(webpImage()))...)
	if code, out, _ := do(e.handler(), http.MethodPost, receiptURL, coreTok, "image/webp", big); code != http.StatusOK || out["kind"] != "document" {
		t.Errorf("exactly the limit: %d %v", code, out)
	}
	if sent := e.fake.Sent(); len(sent) != 2 { // the image with the junk after it, and this one
		t.Errorf("sent: %d", len(sent))
	}
}

func TestMiniAppReceiptRefusesBadQueries(t *testing.T) {
	e := newPayEnv(t)
	img := pngImage(t, 8, 8)
	for _, q := range []string{
		"", "?chat=5151", "?ref=AB12CD34", "?chat=0&ref=AB", "?chat=-1001&ref=AB", "?chat=abc&ref=AB",
		"?chat=5151&ref=", "?chat=5151&ref=../x", "?chat=5151&ref=A%20B", "?chat=5151&ref=" + strings.Repeat("A", 33),
	} {
		if code, out, _ := do(e.handler(), http.MethodPost, "/internal/miniapp/receipt"+q, coreTok, "image/png", img); code != http.StatusBadRequest || out["problem"] != ProblemBadRequest {
			t.Errorf("query %q: %d %v", q, code, out)
		}
	}
	if sent := e.fake.Sent(); len(sent) != 0 {
		t.Errorf("sent: %+v", sent)
	}
}

// blockingSender holds every send until released.
type blockingSender struct {
	ReceiptSender
	started chan struct{}
	release chan struct{}
}

func (b blockingSender) SendPhoto(ctx context.Context, chat int64, p tg.Photo, caption string, kb *tg.Keyboard) (*tg.Message, error) {
	b.started <- struct{}{}
	<-b.release
	return b.ReceiptSender.SendPhoto(ctx, chat, p, caption, kb)
}

// Receipts are held in memory while they are sent: only a few at a time.
func TestMiniAppReceiptsAreSentFewAtATime(t *testing.T) {
	e := newPayEnv(t)
	b := blockingSender{e.bot, make(chan struct{}, maxReceiptsAtOnce), make(chan struct{})}
	h := MiniAppReceipt(b, i18n.MustLoad(), coreTok, nil)
	img := pngImage(t, 8, 8)
	var wg sync.WaitGroup
	codes := make(chan int, maxReceiptsAtOnce)
	for range maxReceiptsAtOnce {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _ := do(h, http.MethodPost, receiptURL, coreTok, "image/png", img)
			codes <- code
		}()
	}
	for range maxReceiptsAtOnce {
		select {
		case <-b.started:
		case <-time.After(10 * time.Second):
			t.Fatal("the receipts did not start")
		}
	}
	code, out, hdr := do(h, http.MethodPost, receiptURL, coreTok, "image/png", img)
	if code != http.StatusTooManyRequests || out["problem"] != ProblemBusy || hdr.Get("Retry-After") == "" {
		t.Errorf("one too many: %d %v", code, out)
	}
	close(b.release)
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != http.StatusOK {
			t.Errorf("a receipt in flight: %d", c)
		}
	}
	// Room again.
	if code, _, _ := do(h, http.MethodPost, receiptURL, coreTok, "image/png", img); code != http.StatusOK {
		t.Errorf("after they finished: %d", code)
	}
}

// ---- the staff alert ----

func TestMiniAppProofAlert(t *testing.T) {
	var got []ProofAlert
	var ctxErr error
	notify := func(ctx context.Context, a ProofAlert) {
		got = append(got, a)
		ctxErr = ctx.Err()
	}
	h := MiniAppProofAlert(notify, coreTok)
	body := `{"telegram_id":5151,"username":"mimi","intent_id":"` + payID + `","amount":150000,"currency":"IRT"}`
	for _, tok := range []string{"", "wrong-token-0123456789abcdef0123"} {
		if code, _, _ := do(h, http.MethodPost, "/internal/miniapp/proof-alert", tok, "application/json", []byte(body)); code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", tok, code)
		}
	}
	if len(got) != 0 {
		t.Fatalf("alerted without core's token: %+v", got)
	}
	if code, _, _ := do(h, http.MethodPost, "/internal/miniapp/proof-alert", coreTok, "application/json", []byte(body)); code != http.StatusNoContent {
		t.Fatalf("alert: %d", code)
	}
	if len(got) != 1 || got[0] != (ProofAlert{TelegramID: 5151, Username: "mimi", IntentID: payID, Amount: 150000, Currency: "IRT"}) {
		t.Fatalf("notified: %+v", got)
	}
	// A customer without a username.
	do(h, http.MethodPost, "/", coreTok, "application/json", []byte(`{"telegram_id":9,"username":"","intent_id":"`+payID+`","amount":1,"currency":"USDT"}`))
	if len(got) != 2 || got[1].Username != "" || got[1].Currency != "USDT" {
		t.Fatalf("no username: %+v", got)
	}
	for name, bad := range map[string]string{
		"no body":          ``,
		"unknown field":    `{"telegram_id":5,"username":"a","intent_id":"` + payID + `","amount":1,"currency":"IRT","chat_id":1}`,
		"no customer":      `{"telegram_id":0,"username":"a","intent_id":"` + payID + `","amount":1,"currency":"IRT"}`,
		"odd username":     `{"telegram_id":5,"username":"<b>","intent_id":"` + payID + `","amount":1,"currency":"IRT"}`,
		"not a payment id": `{"telegram_id":5,"username":"a","intent_id":"adm:ok","amount":1,"currency":"IRT"}`,
		"negative amount":  `{"telegram_id":5,"username":"a","intent_id":"` + payID + `","amount":-1,"currency":"IRT"}`,
		"unknown currency": `{"telegram_id":5,"username":"a","intent_id":"` + payID + `","amount":1,"currency":"EUR"}`,
	} {
		if code, _, _ := do(h, http.MethodPost, "/", coreTok, "application/json", []byte(bad)); code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, code)
		}
	}
	if len(got) != 2 {
		t.Errorf("a refused request alerted the staff: %+v", got)
	}

	// The alert goes on when core stops waiting: its context is not the request's.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+coreTok)
	cancel()
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(got) != 3 || ctxErr != nil {
		t.Errorf("a cancelled request cancelled the alert: %d alerts, %v", len(got), ctxErr)
	}
}
