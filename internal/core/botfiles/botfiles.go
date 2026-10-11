// Package botfiles calls the bot's internal endpoints for the dashboard:
// files customers sent (receipt photos), reloading settings, checking a
// channel, the update check. Core keeps no bot token and has no internet access.
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
}

// New returns a client for the bot at baseURL (http://bot:8080), presenting
// core's service token.
func New(baseURL, token string) *Client {
	return &Client{base: baseURL, token: token, hc: &http.Client{Timeout: 40 * time.Second}}
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
type UpdateCheck struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Channel   string `json:"channel"`
	Newer     bool   `json:"newer"`
	Verified  bool   `json:"verified"`
	CheckedAt int64  `json:"checked_at"`
	Error     string `json:"error"`
	Detail    string `json:"detail"`
}

// CheckUpdates reads the bot's cached update check (it does not make the bot
// look again), within 3 seconds.
func (c *Client) CheckUpdates(ctx context.Context) (*UpdateCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/internal/update-check", nil)
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
