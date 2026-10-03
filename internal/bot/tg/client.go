package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	base string // https://api.telegram.org/bot<token>/
	hc   *http.Client
}

// Option customizes a Client.
type Option func(*Client)

// WithBaseURL points the client at another API root (tests, local Bot API server).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.base = strings.TrimRight(u, "/") + "/" }
}

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// New returns a client for token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		base: "https://api.telegram.org/bot" + token + "/",
		// Long polling holds requests open (timeout below); keep the client
		// timeout above it.
		hc: &http.Client{Timeout: 75 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
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

// GetUpdates long-polls for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var out []Update
	err := c.call(ctx, "getUpdates", jsonBody(map[string]any{
		"offset": offset, "timeout": timeoutSec,
		"allowed_updates": []string{"message", "callback_query"},
	}), &out)
	return out, err
}

// SetWebhook switches the bot to webhook delivery. secret is echoed by
// Telegram in X-Telegram-Bot-Api-Secret-Token on every delivery.
func (c *Client) SetWebhook(ctx context.Context, webhookURL, secret string) error {
	return c.call(ctx, "setWebhook", jsonBody(map[string]any{
		"url": webhookURL, "secret_token": secret,
		"allowed_updates": []string{"message", "callback_query"},
	}), nil)
}

// DeleteWebhook switches back to getUpdates (long polling).
func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", jsonBody(map[string]any{"drop_pending_updates": false}), nil)
}

// SendMessage sends an HTML-formatted message.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *Keyboard) (*Message, error) {
	body := map[string]any{"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
	if kb != nil {
		body["reply_markup"] = kb
	}
	var m Message
	if err := c.call(ctx, "sendMessage", jsonBody(body), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// EditMessageText replaces a message's text and keyboard. An edit that
// changes nothing is not an error.
func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text string, kb *Keyboard) error {
	body := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text,
		"parse_mode": "HTML", "disable_web_page_preview": true}
	if kb != nil {
		body["reply_markup"] = kb
	}
	err := c.call(ctx, "editMessageText", jsonBody(body), nil)
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
		body := map[string]any{"chat_id": chatID, "photo": p.FileID, "caption": caption, "parse_mode": "HTML"}
		if kb != nil {
			body["reply_markup"] = kb
		}
		if err := c.call(ctx, "sendPhoto", jsonBody(body), &m); err != nil {
			return nil, err
		}
		return &m, nil
	}
	build := func() (io.Reader, string, error) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		_ = w.WriteField("chat_id", strconv.FormatInt(chatID, 10))
		_ = w.WriteField("caption", caption)
		_ = w.WriteField("parse_mode", "HTML")
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
	if err := c.call(ctx, "sendPhoto", build, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
