package runner

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// MaxFile is the largest file the bot hands to core (receipts are photos or
// small PDFs; Telegram lets bots download up to 20 MB).
const MaxFile = 10 << 20

// FileGetter is what Files needs from the Telegram client.
type FileGetter interface {
	GetFile(ctx context.Context, fileID string) (*tg.File, error)
	Download(ctx context.Context, filePath string, max int64) ([]byte, error)
}

var fileIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{10,256}$`)

// fileTypes are the kinds of file handed out, by their sniffed type.
var fileTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true, "application/pdf": true,
}

// Files serves files users sent the bot (receipt photos) to core, for the
// dashboard's review queue, at GET /internal/files/{id}. Core keeps no bot
// token and has no internet access; only core may call this (its service
// token), and Caddy never routes /internal/ from outside.
func Files(bot FileGetter, coreToken string, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || coreToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(coreToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("id")
		if !fileIDRe.MatchString(id) {
			http.Error(w, "bad file id", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		f, err := bot.GetFile(ctx, id)
		var ae *tg.APIError
		switch {
		case errors.As(err, &ae) && ae.Code == http.StatusBadRequest:
			http.Error(w, "no such file", http.StatusNotFound)
			return
		case err != nil:
			log.Warn("receipt file lookup failed", "err", err)
			http.Error(w, "telegram did not answer", http.StatusBadGateway)
			return
		case f.FileSize > MaxFile:
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		data, err := bot.Download(ctx, f.FilePath, MaxFile)
		switch {
		case errors.Is(err, tg.ErrFileTooLarge):
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		case err != nil:
			log.Warn("receipt file download failed", "err", err)
			http.Error(w, "telegram did not answer", http.StatusBadGateway)
			return
		}
		ctype := http.DetectContentType(data)
		if !fileTypes[ctype] {
			http.Error(w, "not an image or PDF", http.StatusUnsupportedMediaType)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data) //nolint:gosec // G705: a sniffed image or PDF (never HTML), sent with nosniff, to core only
	})
}
