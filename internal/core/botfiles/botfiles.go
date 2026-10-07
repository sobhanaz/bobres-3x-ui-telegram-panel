// Package botfiles fetches files customers sent the bot (receipt photos) for
// the dashboard, from the bot's internal endpoint: core keeps no bot token
// and has no internet access.
package botfiles

import (
	"context"
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
