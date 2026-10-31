package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Backup delivery limits.
const (
	// MaxBackup is the largest backup Deliver reads.
	MaxBackup = 2 << 30
	// CloudUploadLimit is the largest file a bot may upload to Telegram's
	// cloud Bot API; LocalUploadLimit through a local Bot API server.
	CloudUploadLimit = 50 << 20
	LocalUploadLimit = 2000 << 20
)

// UploadLimit is the largest file the bot uploads: more through a local Bot
// API server (BOBRES_TELEGRAM_API_URL) than to Telegram's cloud.
func UploadLimit(localAPI bool) int64 {
	if localAPI {
		return LocalUploadLimit
	}
	return CloudUploadLimit
}

// Reasons Deliver gives for a backup it did not send.
const (
	ReasonNoOwner  = "no_owner"
	ReasonTooBig   = "too_big"
	ReasonBusy     = "busy"
	ReasonBlocked  = "blocked"
	ReasonTelegram = "telegram_error"
)

// backupNameRe is a name internal/backup's FileName gives.
var backupNameRe = regexp.MustCompile(`^bobres-(\d{8}T\d{6}Z)\.bkp$`)

// DocumentSender is what Deliver needs from the Telegram client.
type DocumentSender interface {
	MessageSender
	SendDocumentUpload(ctx context.Context, chatID int64, name string, r io.Reader, size int64, caption string) (*tg.Message, error)
	// SendDocument re-sends a file Telegram already has, by its file id.
	SendDocument(ctx context.Context, chatID int64, fileID, caption string, kb *tg.Keyboard) (*tg.Message, error)
}

// Delivery configures Deliver.
type Delivery struct {
	Bot DocumentSender
	// Token is the backup service's token (BOBRES_PEER_BACKUP_TOKEN).
	Token string
	// Owners are the configured owners' Telegram ids, the only recipients,
	// the main owner first (none: nobody, every backup is refused). The file
	// is uploaded once, to the first; the others get the same file from
	// Telegram's servers by its file id.
	Owners []int64
	// Limit is the largest file uploaded (UploadLimit).
	Limit int64
	Texts *i18n.Catalog
	// Lang is an owner's language (nil: the first language).
	Lang func(ctx context.Context, owner int64) string
	Log  *slog.Logger
}

// Deliver sends an encrypted backup to the owners' Telegram, for the backup
// service, at POST /internal/backups/deliver. The body is the file itself,
// named by X-Backup-Name (bobres-<UTC stamp>.bkp), with a Content-Length.
// Only the backup service may call it (its token); it takes no chat id: the
// file goes to the configured owners and nobody else.
//
// The body is streamed to Telegram as it arrives (Content-Length gives the
// upload its exact size), never held in memory or on disk: the bot's /tmp
// is a tmpfs inside its memory limit, and a backup through a local Bot API
// may be 2000 MB. It is uploaded before answering, with the connection's
// deadlines extended for it. A body that ends early fails the upload (the
// size no longer matches) and is answered 400. The answer is
// {"sent":true,"message_id"} or {"sent":false,"reason","error"}.
// A file over Limit is not uploaded: each owner gets a text saying it is kept
// on the server, and the reason is "too_big". With no owner configured the
// answer is 409 with the reason "no_owner". The answer is about the main
// owner, the one the file is uploaded to; the other owners get it from
// Telegram afterwards, and one that cannot be reached is only logged.
func Deliver(d Delivery) http.Handler {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	busy := make(chan struct{}, 1) // one delivery at a time: each is a long upload
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The same bearer check as core's endpoints, with the backup token.
		if !fromCore(r, d.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name := r.Header.Get("X-Backup-Name")
		if m := backupNameRe.FindStringSubmatch(name); m == nil || !validStamp(m[1]) {
			http.Error(w, "X-Backup-Name must be bobres-<YYYYMMDDTHHMMSSZ>.bkp", http.StatusBadRequest)
			return
		}
		size := r.ContentLength
		switch {
		case size < 0:
			http.Error(w, "Content-Length required", http.StatusLengthRequired)
			return
		case size == 0:
			http.Error(w, "empty backup", http.StatusBadRequest)
			return
		case size > MaxBackup:
			http.Error(w, "backup too large", http.StatusRequestEntityTooLarge)
			return
		}
		answer := func(status int, v map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		notSent := func(status int, reason string, err error) {
			v := map[string]any{"sent": false, "reason": reason}
			if err != nil {
				_, v["error"] = telegramError(err)
			}
			answer(status, v)
		}
		if len(d.Owners) == 0 {
			notSent(http.StatusConflict, ReasonNoOwner, nil)
			return
		}
		langOf := func(owner int64) string {
			if d.Lang == nil {
				return i18n.Default()
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			return d.Lang(ctx, owner)
		}
		lang := langOf(d.Owners[0])
		if size > d.Limit {
			var first error
			for i, owner := range d.Owners {
				ol := lang
				if i > 0 {
					ol = langOf(owner)
				}
				ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
				_, err := d.Bot.SendMessage(ctx, owner, d.Texts.T(ol, "backup.too_big", "name", name,
					"size", i18n.FileSize(ol, size), "limit", i18n.FileSize(ol, d.Limit)), nil)
				cancel()
				if err != nil {
					d.Log.Warn("backup too large for Telegram; the warning was not sent either", "name", name, "err", err)
					if i == 0 {
						first = err
					}
				}
			}
			notSent(http.StatusOK, ReasonTooBig, first)
			return
		}
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			notSent(http.StatusTooManyRequests, ReasonBusy, nil)
			return
		}
		// The server's read and write timeouts are seconds; an upload takes
		// minutes. Not every ResponseWriter can (a test recorder): no matter.
		rc := http.NewResponseController(w)
		until := time.Now().Add(2 * tg.UploadTimeout)
		_ = rc.SetReadDeadline(until)
		_ = rc.SetWriteDeadline(until)

		src := &bodyReader{r: http.MaxBytesReader(w, r.Body, MaxBackup)}
		ctx, cancel := context.WithTimeout(r.Context(), tg.UploadTimeout)
		defer cancel()
		caption := d.Texts.T(lang, "backup.caption", "name", name, "size", i18n.FileSize(lang, size))
		m, err := d.Bot.SendDocumentUpload(ctx, d.Owners[0], name, src, size, caption)
		if src.short(size) {
			// The upload could not be complete: the size it announced was wrong.
			d.Log.Warn("backup body ended before Content-Length", "name", name, "size", size, "read", src.n, "err", src.err)
			http.Error(w, "the body ended before Content-Length", http.StatusBadRequest)
			return
		}
		if err != nil {
			reason := ReasonTelegram
			if tg.IsBlocked(err) {
				reason = ReasonBlocked
			}
			d.Log.Warn("backup not sent to the owner", "name", name, "size", size, "err", err)
			notSent(http.StatusOK, reason, err)
			return
		}
		d.Log.Info("backup sent to the owner", "name", name, "size", size)
		d.copyToOtherOwners(r.Context(), m, name, size, langOf)
		answer(http.StatusOK, map[string]any{"sent": true, "message_id": m.MessageID})
	})
}

// copyToOtherOwners sends the uploaded backup m to the owners after the main
// one, by its Telegram file id (no second upload). Failing to reach one is
// logged and changes nothing: the backup is safe on the server and with the
// main owner.
func (d Delivery) copyToOtherOwners(ctx context.Context, m *tg.Message, name string, size int64, langOf func(int64) string) {
	if len(d.Owners) < 2 {
		return
	}
	if m == nil || m.Document == nil || m.Document.FileID == "" {
		d.Log.Warn("backup not copied to the other owners: Telegram gave no file id", "name", name)
		return
	}
	for _, owner := range d.Owners[1:] {
		ol := langOf(owner)
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := d.Bot.SendDocument(cctx, owner, m.Document.FileID,
			d.Texts.T(ol, "backup.caption", "name", name, "size", i18n.FileSize(ol, size)), nil)
		cancel()
		if err != nil {
			d.Log.Warn("backup not sent to another owner", "name", name, "err", err)
		}
	}
}

// validStamp reports whether s is a real time in FileName's layout.
func validStamp(s string) bool {
	_, err := time.Parse("20060102T150405Z", s)
	return err == nil
}

// bodyReader counts what the request body gave and keeps how it ended, to
// tell a short or broken body from a refusal by Telegram.
type bodyReader struct {
	r   io.Reader
	n   int64
	eof bool  // the body ended (io.EOF)
	err error // reading it failed otherwise
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += int64(n)
	switch {
	case errors.Is(err, io.EOF):
		b.eof = true
	case err != nil:
		b.err = err
	}
	return n, err
}

// short reports whether the body failed or ended before size bytes.
func (b *bodyReader) short(size int64) bool {
	return b.err != nil || (b.eof && b.n < size)
}
