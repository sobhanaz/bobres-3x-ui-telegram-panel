package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/receipt"
)

// The Mini App's payments (docs/17-mini-app.md) need the bot for what only it
// can do: core, which serves the Mini App, has no bot token. Three endpoints
// for core, with the same protection as the others (core's service token,
// never routed from outside): a Stars invoice link, a receipt sent to the
// customer's chat, and the staff alert for a proof core has recorded.

// More problems the Mini App's endpoints report (beside ProblemError,
// ProblemBlocked and ProblemBadRequest).
const (
	// ProblemRefused: Telegram refused what the bot asked it to make or send.
	ProblemRefused = "refused"
	// ProblemTooLarge: the receipt is over the limit.
	ProblemTooLarge = "too_large"
	// ProblemBadType: the receipt is not a JPEG, PNG or WebP image.
	ProblemBadType = "bad_type"
	// ProblemEmpty: the receipt has no bytes.
	ProblemEmpty = "empty"
	// ProblemBusy: too many receipts are being sent at once.
	ProblemBusy = "busy"
	// ProblemFlood: Telegram asks the bot to slow down.
	ProblemFlood = "flood"
)

// problem answers an endpoint's failure: its HTTP status and
// {"ok":false,"problem","detail"}.
func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "problem": code, "detail": detail})
}

// telegramProblem is how a failed call to Telegram is told to core. A flood
// wait asks core to come back later.
func telegramProblem(w http.ResponseWriter, err error) {
	_, detail := telegramError(err)
	if wait, ok := tg.IsFlood(err); ok {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(wait/time.Second), 10))
		problem(w, http.StatusTooManyRequests, ProblemFlood, detail)
		return
	}
	switch {
	case tg.IsBlocked(err):
		problem(w, http.StatusConflict, ProblemBlocked, detail)
	case tg.IsRefused(err):
		problem(w, http.StatusUnprocessableEntity, ProblemRefused, detail)
	default:
		problem(w, http.StatusBadGateway, ProblemError, detail)
	}
}

// clipUnits cuts s to at most n UTF-16 code units, on a character boundary:
// how Telegram counts the length of an invoice's title and description.
func clipUnits(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

var paymentIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ---- Stars invoice links ----

// InvoiceLinker is what MiniAppInvoice needs from the Telegram client.
type InvoiceLinker interface {
	CreateInvoiceLink(ctx context.Context, inv tg.Invoice) (string, error)
}

const (
	maxInvoiceBody = 4 << 10
	// maxStars and maxMinor only keep nonsense out; Telegram sets its own limit.
	maxStars = 1_000_000_000
	maxMinor = 1_000_000_000_000_000
)

// MiniAppInvoice makes a Telegram Stars invoice link for the Mini App, at POST
// /internal/miniapp/invoice with {"payload","stars","lang","title","amount",
// "currency","topup"}: the invoice the bot would send as a message, with the
// same title and description (the plan's name, or the texts pay.stars_title and
// pay.topup_title; "Payment of <amount>" in the customer's language), as a link
// the app opens with Telegram.WebApp.openInvoice. The payload is the payment's
// id; Telegram sends the bot its pre_checkout_query and successful_payment
// with it, which the bot's own handlers settle. It answers 200 with
// {"ok":true,"url"}, else a status and {"ok":false,"problem","detail"}.
func MiniAppInvoice(bot InvoiceLinker, texts *i18n.Catalog, coreToken string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in struct {
			Payload  string `json:"payload"`
			Stars    int64  `json:"stars"`
			Lang     string `json:"lang"`
			Title    string `json:"title"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
			Topup    bool   `json:"topup"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInvoiceBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			problem(w, http.StatusBadRequest, ProblemBadRequest, `body must be {"payload","stars","lang","title","amount","currency","topup"}`)
			return
		}
		switch {
		case !paymentIDRe.MatchString(in.Payload):
			problem(w, http.StatusBadRequest, ProblemBadRequest, "payload must be a payment id")
			return
		case in.Stars < 1 || in.Stars > maxStars:
			problem(w, http.StatusBadRequest, ProblemBadRequest, "stars out of range")
			return
		case in.Amount < 0 || in.Amount > maxMinor || (in.Currency != "IRT" && in.Currency != "USDT"):
			problem(w, http.StatusBadRequest, ProblemBadRequest, "amount or currency out of range")
			return
		}
		lang := i18n.Normalize(in.Lang)
		title := clipUnits(strings.Join(strings.Fields(in.Title), " "), 32)
		if in.Topup {
			title = clipUnits(texts.T(lang, "pay.topup_title"), 32)
		} else if title == "" {
			title = clipUnits(texts.T(lang, "pay.stars_title"), 32)
		}
		desc := clipUnits(texts.T(lang, "pay.stars_desc", "amount", i18n.Money(lang, in.Amount, in.Currency)), 255)
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		link, err := bot.CreateInvoiceLink(ctx, tg.Invoice{Title: title, Description: desc, Payload: in.Payload, Stars: in.Stars})
		if err != nil {
			log.Warn("mini app invoice link not made", "err", err)
			telegramProblem(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": link})
	})
}

// ---- receipts ----

// MaxReceipt is the largest receipt the bot takes from core.
const MaxReceipt = receipt.MaxBytes

// maxReceiptsAtOnce bounds the receipts being read and sent at the same time:
// each is held in memory (up to MaxReceipt) while it is checked and sent.
const maxReceiptsAtOnce = 3

// ReceiptSender is what MiniAppReceipt needs from the Telegram client.
type ReceiptSender interface {
	SendPhoto(ctx context.Context, chatID int64, p tg.Photo, caption string, kb *tg.Keyboard) (*tg.Message, error)
	SendDocumentUpload(ctx context.Context, chatID int64, name string, r io.Reader, size int64, caption string) (*tg.Message, error)
}

var referenceRe = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)

// MiniAppReceipt sends the receipt a customer uploaded in the Mini App to the
// customer's own chat with the bot, and answers the Telegram file id, which
// core stores as the payment's receipt (the same kind of id the bot's own
// flow stores, so the review queue and the admin card work unchanged). It is
// POST /internal/miniapp/receipt?chat=<the customer's Telegram id>&lang=<fa|en>
// &ref=<the payment reference>, the body the image itself with a
// Content-Length (at most MaxReceipt bytes; there is no other framing). The
// body is checked again here (receipt.Inspect: a JPEG, PNG or WebP by its
// bytes, whatever the Content-Type says), then sent as a photo when Telegram
// takes it as one and as a document when not (a WebP, or a size beyond
// Telegram's photo limits), with a short caption in the customer's language
// (pay.receipt_caption). It answers 200 with {"ok":true,"file_id","kind"
// ("photo" or "document")}, else a status and {"ok":false,"problem",
// "detail"}: 400 bad_request or empty, 411 without a Content-Length, 413
// too_large, 415 bad_type, 409 blocked (the customer has not started the bot,
// or blocked it), 422 refused, 429 busy or flood, 502 error. Nothing of the
// image or its file id is logged.
func MiniAppReceipt(bot ReceiptSender, texts *i18n.Catalog, coreToken string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	slots := make(chan struct{}, maxReceiptsAtOnce)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		chat, err := strconv.ParseInt(q.Get("chat"), 10, 64)
		ref := q.Get("ref")
		if err != nil || chat <= 0 || !referenceRe.MatchString(ref) {
			problem(w, http.StatusBadRequest, ProblemBadRequest, "chat must be the customer's Telegram id and ref the payment reference")
			return
		}
		size := r.ContentLength
		switch {
		case size < 0:
			problem(w, http.StatusLengthRequired, ProblemBadRequest, "Content-Length required")
			return
		case size == 0:
			problem(w, http.StatusBadRequest, ProblemEmpty, "the receipt is empty")
			return
		case size > MaxReceipt:
			problem(w, http.StatusRequestEntityTooLarge, ProblemTooLarge, "the receipt is too large")
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "5")
			problem(w, http.StatusTooManyRequests, ProblemBusy, "receipts are being sent; try again in a moment")
			return
		}
		// The server's deadlines are seconds; reading and sending a receipt
		// may take longer. Not every ResponseWriter can (a test recorder): no matter.
		rc := http.NewResponseController(w)
		until := time.Now().Add(2 * time.Minute)
		_ = rc.SetReadDeadline(until)
		_ = rc.SetWriteDeadline(until)

		// Exactly Content-Length bytes (at most MaxReceipt, checked above): a body
		// that ends sooner is refused, and nothing beyond is read.
		data := make([]byte, size)
		if _, err := io.ReadFull(http.MaxBytesReader(w, r.Body, MaxReceipt), data); err != nil {
			problem(w, http.StatusBadRequest, ProblemBadRequest, "the body ended before Content-Length")
			return
		}
		info, err := receipt.Inspect(data)
		switch {
		case errors.Is(err, receipt.ErrEmpty):
			problem(w, http.StatusBadRequest, ProblemEmpty, "the receipt is empty")
			return
		case errors.Is(err, receipt.ErrTooLarge):
			problem(w, http.StatusRequestEntityTooLarge, ProblemTooLarge, "the receipt is too large")
			return
		case err != nil:
			problem(w, http.StatusUnsupportedMediaType, ProblemBadType, "the receipt must be a JPEG, PNG or WebP image")
			return
		}
		caption := texts.T(i18n.Normalize(q.Get("lang")), "pay.receipt_caption", "ref", ref)
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		fileID, kind, err := sendReceipt(ctx, bot, chat, info, data, ref, caption)
		if err != nil {
			log.Warn("mini app receipt not sent", "chat", chat, "err", err)
			telegramProblem(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "file_id": fileID, "kind": kind})
	})
}

// errNoFileID: Telegram took the receipt but named no file.
var errNoFileID = &tg.APIError{Code: http.StatusBadGateway, Description: "telegram sent no file id for the receipt"}

// sendReceipt sends the image to the chat as a photo when Telegram takes it
// as one (the file id of its largest size, as the bot reads a photo a
// customer sent), else, or when Telegram refuses the photo, as a document.
func sendReceipt(ctx context.Context, bot ReceiptSender, chat int64, info receipt.Info, data []byte, ref, caption string) (fileID, kind string, err error) {
	if info.PhotoSize() {
		m, err := bot.SendPhoto(ctx, chat, tg.Photo{Data: data, Name: "receipt." + info.Ext}, caption, nil)
		switch {
		case err == nil && m != nil && m.LargestPhoto() != "":
			return m.LargestPhoto(), "photo", nil
		case err == nil:
			return "", "", errNoFileID
		case !tg.IsRefused(err):
			return "", "", err // blocked, a flood, Telegram down: a document would fail the same way
		}
		// A picture Telegram will not take as a photo after all: as a file.
	}
	m, err := bot.SendDocumentUpload(ctx, chat, "receipt-"+ref+"."+info.Ext, bytes.NewReader(data), int64(len(data)), caption)
	if err != nil {
		return "", "", err
	}
	if m == nil || m.Document == nil || m.Document.FileID == "" {
		return "", "", errNoFileID
	}
	return m.Document.FileID, "document", nil
}

// ---- the staff alert ----

// ProofAlert is the proof of a manual payment core recorded for a customer of
// the Mini App.
type ProofAlert struct {
	TelegramID int64
	Username   string
	IntentID   string
	Amount     int64
	Currency   string
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{0,64}$`)

// MiniAppProofAlert has the staff told about a payment proof core recorded for
// a customer of the Mini App, at POST /internal/miniapp/proof-alert with
// {"telegram_id","username","intent_id","amount","currency"}: the alert the bot
// sends when a proof comes in its own chat is sent from the bot's handler,
// which core cannot reach, so core asks for it here (notify does the sending).
// It answers 204 once the alert has been sent to everyone who gets it; a
// recipient who cannot be reached is only logged by notify. The sending goes
// on if core stops waiting.
func MiniAppProofAlert(notify func(ctx context.Context, a ProofAlert), coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in struct {
			TelegramID int64  `json:"telegram_id"`
			Username   string `json:"username"`
			IntentID   string `json:"intent_id"`
			Amount     int64  `json:"amount"`
			Currency   string `json:"currency"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInvoiceBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			problem(w, http.StatusBadRequest, ProblemBadRequest, `body must be {"telegram_id","username","intent_id","amount","currency"}`)
			return
		}
		if in.TelegramID <= 0 || !usernameRe.MatchString(in.Username) || !paymentIDRe.MatchString(in.IntentID) ||
			in.Amount < 0 || in.Amount > maxMinor || (in.Currency != "IRT" && in.Currency != "USDT") {
			problem(w, http.StatusBadRequest, ProblemBadRequest, "a field is out of range")
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
		defer cancel()
		notify(ctx, ProofAlert{TelegramID: in.TelegramID, Username: in.Username, IntentID: in.IntentID, Amount: in.Amount, Currency: in.Currency})
		w.WriteHeader(http.StatusNoContent)
	})
}
