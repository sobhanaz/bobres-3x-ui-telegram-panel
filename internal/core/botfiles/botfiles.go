// Package botfiles calls the bot's internal endpoints for the dashboard:
// files customers sent (receipt photos), reloading settings, checking a
// channel, the update check, the license server, the server's public IP
// address. Core keeps no bot token and has no internet access.
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
	"time"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// MaxFile matches the bot's own limit.
const MaxFile = 10 << 20

var (
	// ErrNotFound: Telegram does not know the file (or it expired).
	ErrNotFound = errors.New("botfiles: file not found")
	// ErrRefused: the file is too large or not an image or PDF.
	ErrRefused = errors.New("botfiles: file refused")
)

// File is a downloaded file and its sniffed type.
type File struct {
	Data        []byte
	ContentType string
}

// Client calls the bot.
type Client struct {
	base, token string
	hc          *http.Client
	// upload carries a Mini App customer's receipt, which the bot reads,
	// checks and sends on to Telegram before it answers.
	upload *http.Client
}

// New returns a client for the bot at baseURL (http://bot:8080), presenting
// core's service token.
func New(baseURL, token string) *Client {
	return &Client{base: baseURL, token: token, hc: &http.Client{Timeout: 40 * time.Second},
		upload: &http.Client{Timeout: 110 * time.Second}}
}

// Fetch downloads a file by its Telegram file id.
func (c *Client) Fetch(ctx context.Context, fileID string) (*File, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/internal/files/"+url.PathEscape(fileID), nil)
	if err != nil {
		return nil, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("botfiles: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusBadRequest:
		return nil, ErrNotFound
	case http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		return nil, ErrRefused
	default:
		return nil, fmt.Errorf("botfiles: the bot answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxFile+1))
	if err != nil {
		return nil, fmt.Errorf("botfiles: read: %w", err)
	}
	if len(data) > MaxFile {
		return nil, ErrRefused
	}
	return &File{Data: data, ContentType: resp.Header.Get("Content-Type")}, nil
}

// RefreshSettings asks the bot to reload the settings now instead of at its
// next minute, so a change made in the dashboard shows at once.
func (c *Client) RefreshSettings(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/settings/refresh", nil)
	if err != nil {
		return fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("botfiles: refresh: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("botfiles: refresh: the bot answered %d", resp.StatusCode)
	}
	return nil
}

// ChannelCheck is what the bot found out about a channel customers must join.
type ChannelCheck struct {
	OK       bool   `json:"ok"`
	Title    string `json:"title,omitempty"`
	BotAdmin bool   `json:"bot_admin"`
	Problem  string `json:"problem,omitempty"` // not_found, not_admin, not_channel, error
	Detail   string `json:"detail,omitempty"`
}

// CheckChannel asks the bot whether it can see who is in the channel.
func (c *Client) CheckChannel(ctx context.Context, chat string) (*ChannelCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/internal/channel-check?chat="+url.QueryEscape(chat), nil)
	if err != nil {
		return nil, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("botfiles: channel check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("botfiles: channel check: the bot answered %d", resp.StatusCode)
	}
	var out ChannelCheck
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, fmt.Errorf("botfiles: channel check: %w", err)
	}
	return &out, nil
}

// TestBroadcast is a broadcast sent to one chat (a staff member's own) to
// see how it looks.
type TestBroadcast struct {
	ChatID int64             `json:"chat_id"`
	Text   string            `json:"text"`
	Button *TestBroadcastBtn `json:"button"`
}

// TestBroadcastBtn is a test broadcast's link button.
type TestBroadcastBtn struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// TestResult is how the bot's test send went.
type TestResult struct {
	OK        bool   `json:"ok"`
	MessageID int64  `json:"message_id,omitempty"`
	Problem   string `json:"problem,omitempty"` // blocked, bad_request, error
	Detail    string `json:"detail,omitempty"`
}

// TestBroadcast asks the bot to send a broadcast to one chat now.
func (c *Client) TestBroadcast(ctx context.Context, t TestBroadcast) (*TestResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/broadcasts/test", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("botfiles: test broadcast: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("botfiles: test broadcast: the bot answered %d", resp.StatusCode)
	}
	var out TestResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, fmt.Errorf("botfiles: test broadcast: %w", err)
	}
	return &out, nil
}

// UpdateCheck is the bot's last look for a newer release (release.Status).
type UpdateCheck = domain.UpdateCheck

// CheckUpdates reads the bot's cached update check (it does not make the bot
// look again), within 3 seconds.
func (c *Client) CheckUpdates(ctx context.Context) (*UpdateCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.updateCheck(ctx, http.MethodGet, "/internal/update-check")
}

// CheckUpdatesNow makes the bot look for a newer release now (the dashboard's
// "Check again"): the bot reloads its settings, asks GitHub, verifies what it
// finds and answers with the outcome, a failed look included (its error and
// detail say why). The bot bounds its own look; core waits at most 25 seconds,
// inside the dashboard's 30-second write limit.
func (c *Client) CheckUpdatesNow(ctx context.Context) (*UpdateCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return c.updateCheck(ctx, http.MethodPost, "/internal/update-check/refresh")
}

func (c *Client) updateCheck(ctx context.Context, method, path string) (*UpdateCheck, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("botfiles: update check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("botfiles: update check: the bot answered %d", resp.StatusCode)
	}
	var out UpdateCheck
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&out); err != nil {
		return nil, fmt.Errorf("botfiles: update check: %w", err)
	}
	return &out, nil
}

// RecentLogs reads the bot's kept warnings and errors (already redacted),
// oldest first, within 3 seconds.
func (c *Client) RecentLogs(ctx context.Context) ([]logging.Line, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/internal/logs", nil)
	if err != nil {
		return nil, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("botfiles: logs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("botfiles: logs: the bot answered %d", resp.StatusCode)
	}
	var out struct {
		Lines []logging.Line `json:"lines"`
	}
	// 500 lines of at most about 2 KB each.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("botfiles: logs: %w", err)
	}
	return out.Lines, nil
}

// PublicIP asks the bot for this server's public IP address (core has no
// internet access), within 15 seconds: the bot asks two public services in
// turn. Core checks the answer again: only a public address is returned,
// in canonical form.
func (c *Client) PublicIP(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/internal/public-ip", nil)
	if err != nil {
		return "", fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("botfiles: public ip: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("botfiles: public ip: the bot answered %d", resp.StatusCode)
	}
	var out struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<10)).Decode(&out); err != nil {
		return "", fmt.Errorf("botfiles: public ip: %w", err)
	}
	ip, err := tlsconf.PublicIP(out.IP)
	if err != nil {
		return "", fmt.Errorf("botfiles: public ip: %w", err)
	}
	return ip, nil
}

// ActivateLicense asks the vendor's license server, through the bot, for a
// token for a license key bound to this install (domain.LicenseServer).
func (c *Client) ActivateLicense(ctx context.Context, a domain.LicenseActivation) (string, error) {
	return c.license(ctx, "activate", a)
}

// RefreshLicense asks the license server, through the bot, to renew the
// installed token (the token is the credential), saying core's version.
func (c *Client) RefreshLicense(ctx context.Context, token string) (string, error) {
	return c.license(ctx, "refresh", map[string]string{"token": token, "version": version.Version})
}

// maxLicenseAnswer bounds what the bot passes back from the license server.
const maxLicenseAnswer = 64 << 10

// license POSTs body to the bot's /internal/license/<call> and reads the
// license server's answer: {"token"} on 200, else {"error","message"} as a
// *domain.LicenseServerError (the server's refusal, or the bot's own
// not_configured, unreachable or server_error). Neither the key nor a token
// is ever in an error.
func (c *Client) license(ctx context.Context, call string, body any) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/license/"+call, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", &domain.LicenseServerError{Status: http.StatusBadGateway, Code: domain.LicenseUnreachable,
			Message: "the bot did not answer"}
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Token   string `json:"token"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxLicenseAnswer+1))
	if err == nil && len(raw) <= maxLicenseAnswer {
		err = json.Unmarshal(raw, &out)
	} else if err == nil {
		err = errors.New("too long")
	}
	switch {
	case err == nil && resp.StatusCode == http.StatusOK && out.Token != "":
		return out.Token, nil
	case err == nil && resp.StatusCode != http.StatusOK && out.Error != "":
		return "", &domain.LicenseServerError{Status: resp.StatusCode, Code: clip(out.Error, 64), Message: clip(out.Message, 300)}
	}
	// Not the bot's or the server's JSON (the bot refused core's token, or
	// an older bot without these endpoints).
	return "", &domain.LicenseServerError{Status: http.StatusBadGateway, Code: domain.LicenseServerFailed,
		Message: fmt.Sprintf("the bot answered %d", resp.StatusCode)}
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
