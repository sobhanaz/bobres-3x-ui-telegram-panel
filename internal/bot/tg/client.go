package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
)

const maxBody = 4 << 20

// APIError is a Telegram error reply. The request URL (which contains the
// bot token) is never part of it.
type APIError struct {
	Code        int
	Description string
	RetryAfter  int // seconds, on 429
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

// IsBlocked reports whether the user blocked the bot or the chat is gone:
// retrying will never deliver the message.
func IsBlocked(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	d := strings.ToLower(ae.Description)
	return ae.Code == http.StatusForbidden || strings.Contains(d, "chat not found") ||
		strings.Contains(d, "user is deactivated")
}

// IsNotModified reports an edit that changed nothing (harmless).
func IsNotModified(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && strings.Contains(ae.Description, "message is not modified")
}

// Client calls the Bot API.
type Client struct {
	base     string // <api root>/bot<token>/
	fileBase string // <api root>/file/bot<token>/
	hc       *http.Client
}

type settings struct {
	root string // API root, default https://api.telegram.org
	base string // full base including the token; set by WithBaseURL
	hc   *http.Client
}

// Option customizes a Client.
type Option func(*settings)

// WithAPIRoot points the client at another Bot API root, e.g. a local Bot API
// server or the fake used in tests ("http://host:8081").
func WithAPIRoot(root string) Option {
	return func(s *settings) {
		if root != "" {
			s.root = root
		}
	}
}

// WithBaseURL sets the full base URL including the bot path ("…/bot<token>").
func WithBaseURL(u string) Option {
	return func(s *settings) { s.base = strings.TrimRight(u, "/") + "/" }
}

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(hc *http.Client) Option { return func(s *settings) { s.hc = hc } }

// New returns a client for token.
func New(token string, opts ...Option) *Client {
	s := settings{
		root: "https://api.telegram.org",
		// Long polling holds requests open (timeout below); keep the client
		// timeout above it.
		hc: &http.Client{Timeout: 75 * time.Second},
	}
	for _, o := range opts {
		o(&s)
	}
	base := s.base
	if base == "" {
		base = strings.TrimRight(s.root, "/") + "/bot" + token + "/"
	}
	// Files are served beside the methods: <root>/file/bot<token>/<path>.
	fileBase := base
	if i := strings.LastIndex(strings.TrimRight(base, "/"), "/bot"); i >= 0 {
		fileBase = base[:i] + "/file" + base[i:]
	}
	return &Client{base: base, fileBase: fileBase, hc: s.hc}
}

// File is a file Telegram keeps for the bot (a photo or document a user sent).
type File struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}

// GetFile looks a file up by its id, for downloading it.
func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	var f File
	if err := c.call(ctx, "getFile", jsonBody(map[string]string{"file_id": fileID}), &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// ErrFileTooLarge: the file is bigger than the caller allows.
var ErrFileTooLarge = errors.New("telegram: file too large")

// Download fetches a file's content by the path GetFile returned, refusing
// more than max bytes. Errors never contain the URL (it holds the token).
func (c *Client) Download(ctx context.Context, filePath string, max int64) ([]byte, error) {
	if filePath == "" || strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "..") ||
		strings.ContainsAny(filePath, "?#\\") {
		return nil, errors.New("telegram: unexpected file path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.fileBase+filePath, nil)
	if err != nil {
		return nil, errors.New("telegram: build download request")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		var ue *url.Error // its message contains the URL, i.e. the token
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("telegram: download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Code: resp.StatusCode, Description: "file download failed"}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("telegram: download: read: %w", err)
	}
	if int64(len(data)) > max {
		return nil, ErrFileTooLarge
	}
	return data, nil
}

type reply struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call posts a JSON (or multipart) request and decodes result into out. A 429
// is retried once after retry_after (capped at 30 s).
func (c *Client) call(ctx context.Context, method string, body func() (io.Reader, string, error), out any) error {
	for attempt := 0; ; attempt++ {
		rd, ctype, err := body()
		if err != nil {
			return err
		}
		err = c.do(ctx, method, rd, ctype, out)
		var ae *APIError
		if attempt == 0 && errors.As(err, &ae) && ae.Code == http.StatusTooManyRequests && ae.RetryAfter > 0 {
			wait := time.Duration(min(ae.RetryAfter, 30)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
				continue
			}
		}
		return err
	}
}

func (c *Client) do(ctx context.Context, method string, body io.Reader, ctype string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, body)
	if err != nil {
		return fmt.Errorf("telegram: build %s request", method)
	}
	req.Header.Set("Content-Type", ctype)
	resp, err := c.hc.Do(req)
	if err != nil {
		var ue *url.Error // its message contains the URL, i.e. the token
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("telegram: %s: read: %w", method, err)
	}
	var r reply
	if err := json.Unmarshal(raw, &r); err != nil {
		return &APIError{Code: resp.StatusCode, Description: "unexpected non-JSON reply"}
	}
	if !r.OK {
		return &APIError{Code: r.ErrorCode, Description: r.Description, RetryAfter: r.Parameters.RetryAfter}
	}
	if out != nil && len(r.Result) > 0 {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return fmt.Errorf("telegram: %s: decode: %w", method, err)
		}
	}
	return nil
}

func jsonBody(v any) func() (io.Reader, string, error) {
	return func() (io.Reader, string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, "", err
		}
		return bytes.NewReader(b), "application/json", nil
	}
}

// GetMe returns the bot's own user (validates the token).
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", jsonBody(struct{}{}), &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// allowedUpdates are the update types the bot handles. pre_checkout_query must
// be listed explicitly, or Telegram never asks and every Stars payment times out.
var allowedUpdates = []string{"message", "callback_query", "pre_checkout_query"}

// GetUpdates long-polls for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var out []Update
	err := c.call(ctx, "getUpdates", jsonBody(map[string]any{
		"offset": offset, "timeout": timeoutSec,
		"allowed_updates": allowedUpdates,
	}), &out)
	return out, err
}

// SetWebhook switches the bot to webhook delivery. secret is echoed by
// Telegram in X-Telegram-Bot-Api-Secret-Token on every delivery.
func (c *Client) SetWebhook(ctx context.Context, webhookURL, secret string) error {
	return c.call(ctx, "setWebhook", jsonBody(map[string]any{
		"url": webhookURL, "secret_token": secret,
		"allowed_updates": allowedUpdates,
	}), nil)
}

// DeleteWebhook switches back to getUpdates (long polling).
func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", jsonBody(map[string]any{"drop_pending_updates": false}), nil)
}

// IsParseError reports that Telegram refused a text's HTML markup.
func IsParseError(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != http.StatusBadRequest {
		return false
	}
	d := strings.ToLower(ae.Description)
	return strings.Contains(d, "can't parse") && strings.Contains(d, "entities")
}

// callHTML posts body, whose field (text or caption) is HTML. When Telegram
// refuses the markup (say, a broken text override), it is sent once more as
// plain text, so the message still arrives instead of nothing.
func (c *Client) callHTML(ctx context.Context, method string, body map[string]any, field string, out any) error {
	body["parse_mode"] = "HTML"
	err := c.call(ctx, method, jsonBody(body), out)
	if !IsParseError(err) {
		return err
	}
	plain := maps.Clone(body)
	delete(plain, "parse_mode")
	if s, ok := plain[field].(string); ok {
		plain[field] = i18n.StripHTML(s)
	}
	return c.call(ctx, method, jsonBody(plain), out)
}

// chatRef is a chat_id: a numeric id (-100…) as a number, an @username as is.
func chatRef(chat string) any {
	if id, err := strconv.ParseInt(chat, 10, 64); err == nil {
		return id
	}
	return chat
}

// GetChat looks a chat up by its @username or numeric id.
func (c *Client) GetChat(ctx context.Context, chat string) (*Chat, error) {
	var out Chat
	if err := c.call(ctx, "getChat", jsonBody(map[string]any{"chat_id": chatRef(chat)}), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetChatMember reports a user's membership of a chat (@username or numeric
// id). For other users of a channel, the bot must be its administrator.
func (c *Client) GetChatMember(ctx context.Context, chat string, userID int64) (*ChatMember, error) {
	var out ChatMember
	if err := c.call(ctx, "getChatMember", jsonBody(map[string]any{"chat_id": chatRef(chat), "user_id": userID}), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SendMessage sends an HTML-formatted message.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *Keyboard) (*Message, error) {
	body := map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true}
	if kb != nil {
		body["reply_markup"] = kb
	}
	var m Message
	if err := c.callHTML(ctx, "sendMessage", body, "text", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// EditMessageText replaces a message's text and keyboard. An edit that
// changes nothing is not an error.
func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text string, kb *Keyboard) error {
	body := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text, "disable_web_page_preview": true}
	if kb != nil {
		body["reply_markup"] = kb
	}
	err := c.callHTML(ctx, "editMessageText", body, "text", nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

// EditMessageReplyMarkup replaces (or, with nil, removes) a message's buttons.
func (c *Client) EditMessageReplyMarkup(ctx context.Context, chatID int64, messageID int, kb *Keyboard) error {
	if kb == nil {
		kb = &Keyboard{InlineKeyboard: [][]Button{}}
	}
	err := c.call(ctx, "editMessageReplyMarkup", jsonBody(map[string]any{
		"chat_id": chatID, "message_id": messageID, "reply_markup": kb,
	}), nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

// AnswerCallback acknowledges a button press (optionally with a toast).
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error {
	return c.call(ctx, "answerCallbackQuery", jsonBody(map[string]any{
		"callback_query_id": callbackID, "text": text, "show_alert": alert,
	}), nil)
}

// SendInvoice sends a Telegram Stars invoice (currency XTR, one price line).
func (c *Client) SendInvoice(ctx context.Context, chatID int64, inv Invoice) (*Message, error) {
	body := map[string]any{
		"chat_id": chatID, "title": inv.Title, "description": inv.Description, "payload": inv.Payload,
		"currency": "XTR", "prices": []LabeledPrice{{Label: inv.Title, Amount: inv.Stars}},
	}
	if inv.StartParameter != "" {
		body["start_parameter"] = inv.StartParameter
	}
	var m Message
	if err := c.call(ctx, "sendInvoice", jsonBody(body), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// AnswerPreCheckoutQuery confirms (ok) or refuses a payment before Telegram
// charges it; errMsg is shown to the user when refusing.
func (c *Client) AnswerPreCheckoutQuery(ctx context.Context, queryID string, ok bool, errMsg string) error {
	body := map[string]any{"pre_checkout_query_id": queryID, "ok": ok}
	if !ok {
		body["error_message"] = errMsg
	}
	return c.call(ctx, "answerPreCheckoutQuery", jsonBody(body), nil)
}

// RefundStarPayment returns a Stars payment to the user (full refund).
func (c *Client) RefundStarPayment(ctx context.Context, userID int64, chargeID string) error {
	return c.call(ctx, "refundStarPayment", jsonBody(map[string]any{
		"user_id": userID, "telegram_payment_charge_id": chargeID,
	}), nil)
}

// Photo is either an uploaded PNG/JPEG (Data) or an existing Telegram file (FileID).
type Photo struct {
	FileID string
	Data   []byte
	Name   string
}

// SendPhoto sends a photo with an HTML caption.
func (c *Client) SendPhoto(ctx context.Context, chatID int64, p Photo, caption string, kb *Keyboard) (*Message, error) {
	var m Message
	if p.FileID != "" {
		body := map[string]any{"chat_id": chatID, "photo": p.FileID, "caption": caption}
		if kb != nil {
			body["reply_markup"] = kb
		}
		if err := c.callHTML(ctx, "sendPhoto", body, "caption", &m); err != nil {
			return nil, err
		}
		return &m, nil
	}
	// The upload is multipart; a refused caption is sent again as plain text.
	build := func(html bool) func() (io.Reader, string, error) {
		return func() (io.Reader, string, error) {
			return photoForm(chatID, p, caption, html, kb)
		}
	}
	err := c.call(ctx, "sendPhoto", build(true), &m)
	if IsParseError(err) {
		err = c.call(ctx, "sendPhoto", build(false), &m)
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// photoForm is the multipart body of a photo upload, with an HTML caption
// (html) or the same caption as plain text.
func photoForm(chatID int64, p Photo, caption string, html bool, kb *Keyboard) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if html {
		_ = w.WriteField("caption", caption)
		_ = w.WriteField("parse_mode", "HTML")
	} else {
		_ = w.WriteField("caption", i18n.StripHTML(caption))
	}
	if kb != nil {
		b, err := json.Marshal(kb)
		if err != nil {
			return nil, "", err
		}
		_ = w.WriteField("reply_markup", string(b))
	}
	name := p.Name
	if name == "" {
		name = "photo.png"
	}
	fw, err := w.CreateFormFile("photo", name)
	if err != nil {
		return nil, "", err
	}
	if _, err := fw.Write(p.Data); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

// SendDocument re-sends an existing Telegram file (e.g. a screenshot the user
// sent as a file, whose id sendPhoto refuses) with an HTML caption.
func (c *Client) SendDocument(ctx context.Context, chatID int64, fileID, caption string, kb *Keyboard) (*Message, error) {
	body := map[string]any{"chat_id": chatID, "document": fileID, "caption": caption}
	if kb != nil {
		body["reply_markup"] = kb
	}
	var m Message
	if err := c.callHTML(ctx, "sendDocument", body, "caption", &m); err != nil {
		return nil, err
	}
	return &m, nil
}
