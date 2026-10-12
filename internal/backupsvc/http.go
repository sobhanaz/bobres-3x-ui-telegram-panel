package backupsvc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

// authorized reports whether r carries core's service token.
func authorized(r *http.Request, token string) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// List answers GET /internal/backups for core: the Status.
func List(r *Runner, coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !authorized(req, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, r.Status())
	})
}

// Start answers POST /internal/backups/run for core: 202 {"started":true}
// when a backup starts, 409 {"started":false,"reason":"running"} when one is
// already running.
func Start(r *Runner, coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !authorized(req, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !r.Trigger() {
			writeJSON(w, http.StatusConflict, map[string]any{"started": false, "reason": "running"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
	})
}

// RunNow is `/app run-now` inside the backup container (`bobres backup`): it
// asks the running service at base for a backup, waits until no backup runs,
// and reports the newest one. If a backup is already running it waits for
// that one instead. The error is the backup's own when it failed.
func RunNow(ctx context.Context, c *http.Client, base, token string, out io.Writer, poll time.Duration) error {
	base = strings.TrimRight(base, "/")
	call := func(method, path string, v any) (int, error) {
		req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := c.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close() //nolint:errcheck // read below
		if resp.StatusCode == http.StatusUnauthorized {
			return resp.StatusCode, errors.New("the backup service refused the token")
		}
		return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
	}
	var started struct {
		Started bool `json:"started"`
	}
	code, err := call(http.MethodPost, "/internal/backups/run", &started)
	switch {
	case err != nil:
		return fmt.Errorf("start the backup: %w", err)
	case code == http.StatusConflict:
		fmt.Fprintln(out, "A backup is already running; waiting for it to finish...")
	case code == http.StatusAccepted:
		fmt.Fprintln(out, "Backup started; waiting for it to finish...")
	default:
		return fmt.Errorf("start the backup: HTTP %d", code)
	}
	for {
		var st Status
		if _, err := call(http.MethodGet, "/internal/backups", &st); err != nil {
			return fmt.Errorf("backup status: %w", err)
		}
		if !st.Running {
			if len(st.Backups) == 0 {
				return errors.New("no backup was recorded")
			}
			return report(out, st.Backups[0], st.Dir)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func report(out io.Writer, sc backup.Sidecar, dir string) error {
	if sc.Error != "" {
		return fmt.Errorf("backup failed: %s", sc.Error)
	}
	fmt.Fprintf(out, "Backup %s written (%s) in %s.\n", sc.File, humanBytes(uint64(max(sc.Size, 0))), dir) //nolint:gosec // non-negative
	switch {
	case sc.SentAt != nil:
		fmt.Fprintln(out, "Sent to the owner on Telegram.")
	case sc.TelegramError != "":
		fmt.Fprintf(out, "Not sent to Telegram (%s); it is kept on the server.\n", sc.TelegramError)
	default:
		fmt.Fprintln(out, "Kept on the server only (sending to Telegram is not set up).")
	}
	switch {
	case sc.UploadedAt != nil:
		fmt.Fprintf(out, "Copied off the server to %s.\n", sc.UploadedTo)
	case sc.UploadError != "":
		fmt.Fprintf(out, "Not copied off the server (%s); it is kept here.\n", sc.UploadError)
	}
	return nil
}
