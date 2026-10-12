// Package backupclient calls the backup service for the dashboard's System
// page: the list of kept backups and "back up now". The service is optional
// (the Compose profile `backup`); core has no client when BOBRES_BACKUP_URL
// is empty.
package backupclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Timeout bounds every call: the System page shows a service that does not
// answer as unreachable instead of waiting.
const Timeout = 3 * time.Second

// ErrRunning: a backup is already running (the service answered 409).
var ErrRunning = errors.New("backupclient: a backup is already running")

// Client calls the backup service.
type Client struct {
	base, token string
	hc          *http.Client
}

// New returns a client for the backup service at baseURL
// (http://backup:8080), presenting token: core's token for that service
// only (BOBRES_BACKUP_TOKEN), never its own service token.
func New(baseURL, token string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), token: token, hc: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) call(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("backupclient: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backupclient: %w", err)
	}
	return resp, nil
}

// Status reads the service's GET /internal/backups answer ({backups,
// running, last_error, next_run, disk_free, dir, offsite}) as it is, within
// 3 seconds: fields core does not know (each backup's uploaded_at,
// upload_error and uploaded_to, the offsite object) reach the dashboard
// unchanged.
func (c *Client) Status(ctx context.Context) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	resp, err := c.call(ctx, http.MethodGet, "/internal/backups")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backupclient: list: the backup service answered %d", resp.StatusCode)
	}
	// Seven backups plus failed runs' sidecars are a few KB.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("backupclient: list: %w", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil || probe["backups"] == nil {
		return nil, errors.New("backupclient: list: not a backup list")
	}
	return data, nil
}

// Run asks for a backup now (POST /internal/backups/run, which answers at
// once and backs up in the background), within 3 seconds; ErrRunning when
// one is already running.
func (c *Client) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	resp, err := c.call(ctx, http.MethodPost, "/internal/backups/run")
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusAccepted:
		return nil
	case http.StatusConflict:
		return ErrRunning
	default:
		return fmt.Errorf("backupclient: run: the backup service answered %d", resp.StatusCode)
	}
}

// Summary is the newest backup and the schedule, for the System page's
// health tab and core's watchdog.
type Summary struct {
	// LastCreatedAt is the newest backup that worked (nil: none kept).
	LastCreatedAt *time.Time
	LastError     string
	NextRun       int64
	// DiskFree is the free space on the backup disk, in bytes (0: unknown).
	DiskFree uint64
}

// Summarize reads a Status answer.
func Summarize(status json.RawMessage) (Summary, error) {
	var st struct {
		Backups []struct {
			CreatedAt time.Time `json:"created_at"`
			Error     string    `json:"error"`
		} `json:"backups"`
		LastError string `json:"last_error"`
		NextRun   int64  `json:"next_run"`
		DiskFree  uint64 `json:"disk_free"`
	}
	if err := json.Unmarshal(status, &st); err != nil {
		return Summary{}, fmt.Errorf("backupclient: summary: %w", err)
	}
	sum := Summary{LastError: st.LastError, NextRun: st.NextRun, DiskFree: st.DiskFree}
	// The list is newest first; a failed run's sidecar has an error and no file.
	for _, b := range st.Backups {
		if b.Error == "" {
			sum.LastCreatedAt = &b.CreatedAt
			break
		}
	}
	return sum, nil
}
