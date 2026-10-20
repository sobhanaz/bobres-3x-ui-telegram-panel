package botfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The Mini App's payments need three things only the bot can do, because core
// has no Telegram token: a Stars invoice link, sending a receipt to the
// customer's chat (Telegram answers a file id core can keep), and telling the
// staff about a proof (the bot reaches them).

// What went wrong with a call about the Mini App's payments, as far as core
// can tell the customer; any other error is the bot being unreachable or
// misbehaving.
var (
	// ErrInvoiceRefused: Telegram refused to make the invoice (say, for an
	// amount of Stars it does not take).
	ErrInvoiceRefused = errors.New("botfiles: telegram refused the invoice")
	// ErrReceiptTooLarge: the bot found the receipt over its limit.
	ErrReceiptTooLarge = errors.New("botfiles: the receipt is too large")
	// ErrReceiptType: the bot, or Telegram, would not take the receipt as an
	// image.
	ErrReceiptType = errors.New("botfiles: the receipt is not an image the bot can send")
	// ErrChatClosed: the bot cannot write to the customer (they never started
	// it, or blocked it).
	ErrChatClosed = errors.New("botfiles: the customer's chat with the bot is closed")
	// ErrBotBusy: the bot is sending other receipts, or Telegram asks it to
	// slow down; trying again in a moment works.
	ErrBotBusy = errors.New("botfiles: the bot is busy")
)

// InvoiceRequest asks for a Stars invoice link (POST /internal/miniapp/invoice).
type InvoiceRequest struct {
	// Payload is the payment's id: Telegram sends it back with the
	// pre-checkout and the payment.
	Payload string `json:"payload"`
	// Stars is the price.
	Stars int64 `json:"stars"`
	// Lang is the customer's language, for the texts of the invoice.
	Lang string `json:"lang"`
	// Title is the plan's name in that language ("" for the store's own
	// title); a top-up has the store's top-up title whatever it is.
	Title string `json:"title,omitempty"`
	// Amount and Currency are the price in the store's money, which the
	// invoice's description says ("Payment of 150,000 Toman").
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Topup    bool   `json:"topup,omitempty"`
}

// linkRe is what an invoice link looks like: Telegram's own address.
var linkRe = regexp.MustCompile(`^https://t\.me/[A-Za-z0-9$_+/-]{1,200}$`)

// answer is the JSON every one of these endpoints answers with.
type answer struct {
	OK      bool   `json:"ok"`
	Problem string `json:"problem"`
	Detail  string `json:"detail"`
	URL     string `json:"url"`
	FileID  string `json:"file_id"`
}

// plainErr is err without the URL a *url.Error carries: the receipt's address
// has the customer's chat id and the payment's reference in its query, and an
// error ends up in logs.
func plainErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

func (c *Client) post(ctx context.Context, hc *http.Client, path string, body io.Reader, ctype string) (int, *answer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, body)
	if err != nil {
		return 0, nil, fmt.Errorf("botfiles: %w", plainErr(err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", ctype)
	resp, err := hc.Do(req)
	if err != nil {
		route, _, _ := strings.Cut(path, "?")
		return 0, nil, fmt.Errorf("botfiles: %s: %w", route, plainErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	var out answer
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &out) // not JSON (a refused token): the status says it
	}
	return resp.StatusCode, &out, nil
}

// CreateInvoiceLink asks the bot for a Stars invoice link, which the app
// opens with Telegram.WebApp.openInvoice.
func (c *Client) CreateInvoiceLink(ctx context.Context, in InvoiceRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	status, out, err := c.post(ctx, c.hc, "/internal/miniapp/invoice", bytes.NewReader(body), "application/json")
	if err != nil {
		return "", err
	}
	switch {
	case status == http.StatusOK && out.OK && linkRe.MatchString(out.URL):
		return out.URL, nil
	case status == http.StatusUnprocessableEntity:
		return "", fmt.Errorf("%w: %s", ErrInvoiceRefused, clip(out.Detail, 200))
	case status == http.StatusTooManyRequests:
		return "", ErrBotBusy
	}
	return "", fmt.Errorf("botfiles: invoice link: the bot answered %d %s", status, clip(out.Problem, 40))
}

// Receipt is an image a customer uploaded as the receipt of a payment, already
// checked by core.
type Receipt struct {
	// ChatID is the customer's Telegram id: their own chat with the bot.
	ChatID int64
	// Lang is their language, for the caption.
	Lang string
	// Reference is the payment's reference, which the caption names.
	Reference string
	// ContentType is the type core found by looking at the bytes.
	ContentType string
	Data        []byte
}

// fileIDRe is a Telegram file id as the bot hands it out (and payments keeps it).
var fileIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{10,256}$`)

// SendReceipt has the bot send a receipt to the customer's chat and returns
// the Telegram file id to store as the payment's receipt. The body goes with
// its Content-Length; the bot checks it again.
func (c *Client) SendReceipt(ctx context.Context, r Receipt) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	q := url.Values{"chat": {strconv.FormatInt(r.ChatID, 10)}, "lang": {r.Lang}, "ref": {r.Reference}}
	status, out, err := c.post(ctx, c.upload, "/internal/miniapp/receipt?"+q.Encode(), bytes.NewReader(r.Data), r.ContentType)
	if err != nil {
		return "", err
	}
	switch {
	case status == http.StatusOK && out.OK && fileIDRe.MatchString(out.FileID):
		return out.FileID, nil
	case status == http.StatusRequestEntityTooLarge:
		return "", ErrReceiptTooLarge
	case status == http.StatusUnsupportedMediaType || status == http.StatusUnprocessableEntity:
		return "", ErrReceiptType
	case status == http.StatusConflict:
		return "", ErrChatClosed
	case status == http.StatusTooManyRequests:
		return "", ErrBotBusy
	}
	return "", fmt.Errorf("botfiles: receipt: the bot answered %d %s", status, clip(out.Problem, 40))
}

// ProofNotice is a payment proof core recorded for a Mini App customer.
type ProofNotice struct {
	TelegramID int64  `json:"telegram_id"`
	Username   string `json:"username"`
	IntentID   string `json:"intent_id"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
}

// NotifyProof asks the bot to tell the staff about a proof (the alert the bot
// sends for a proof that comes in its own chat). It returns once the bot has
// sent the alerts, within 10 seconds; the proof is already in the review list
// either way.
func (c *Client) NotifyProof(ctx context.Context, n ProofNotice) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	status, out, err := c.post(ctx, c.hc, "/internal/miniapp/proof-alert", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return fmt.Errorf("botfiles: proof alert: the bot answered %d %s", status, strings.TrimSpace(clip(out.Problem, 40)))
	}
	return nil
}
