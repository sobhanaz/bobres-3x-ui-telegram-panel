package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/backupclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
)

// System > Backups: downloading a backup and restoring one (docs/04-dashboard.md,
// docs/02-events-and-apis.md, docs/07-security.md).
//
// Who may do what: owner and admin read the state of a restore (system.read); downloading,
// uploading and restoring are the owner's (system.write), with the session's CSRF token on every
// change. A download and a restore also need, for an owner with a password login and an
// authenticator app, a current code (the step-up that guards replacing the password). A restore
// needs the owner to type the store's domain and tick that everything after the backup is lost.
// Each is rate limited per owner, audited, and told to every owner on Telegram.

// BackupDownloader streams a backup file out of the backup service (backupclient.Client).
type BackupDownloader interface {
	Download(ctx context.Context, name string) (io.ReadCloser, int64, error)
}

const (
	// ticketTTL is how long a download ticket may be used after the owner's code was checked: the
	// page uses it at once, as the address of the browser's own download.
	ticketTTL = time.Minute
	// ticketMax bounds the tickets core remembers.
	ticketMax = 64
	// uploadWindow bounds how long receiving an upload may take (a backup of up to 2 GiB over a
	// slow line), downloadWindow how long sending one.
	uploadWindow   = 45 * time.Minute
	downloadWindow = time.Hour
)

// tickets are the download tickets: a random value the owner gets for one backup, for one
// session's user, once, for a minute. The page's download is the browser's own (a link, so the
// file goes to disk and never through the page's memory), and the authenticator code cannot go
// into an address, so the code buys a ticket and the ticket is in the address.
type tickets struct {
	mu sync.Mutex
	m  map[[32]byte]ticket
}

type ticket struct {
	actor, file string
	expires     time.Time
}

func ticketKey(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// issue makes a ticket for actor to download file; "" when core cannot make one.
func (t *tickets) issue(actor, file string, now time.Time) string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[[32]byte]ticket{}
	}
	for k, v := range t.m {
		if now.After(v.expires) {
			delete(t.m, k)
		}
	}
	if len(t.m) >= ticketMax {
		return ""
	}
	t.m[ticketKey(token)] = ticket{actor: actor, file: file, expires: now.Add(ticketTTL)}
	return token
}

// redeem takes the ticket: the file it is for, once, and only for the user it was made for.
func (t *tickets) redeem(token, actor string, now time.Time) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := ticketKey(token)
	v, ok := t.m[k]
	if !ok {
		return "", false
	}
	delete(t.m, k) // gone whoever asks, and whatever the answer
	if now.After(v.expires) || v.actor != actor {
		return "", false
	}
	return v.file, true
}

// backupRecord is a backup the backup service lists.
type backupRecord struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Error  string `json:"error"`
}

// findBackup looks the backup up in the backup service's own list: only a backup it lists (a file,
// not the record of a failed run) can be downloaded or restored.
func (s *Server) findBackup(ctx context.Context, name string) (*backupRecord, error) {
	if s.cfg.Backups == nil {
		return nil, backupclient.ErrNoBackup
	}
	if !backup.ValidFileName(name) {
		return nil, backupclient.ErrNoBackup
	}
	raw, err := s.cfg.Backups.Status(ctx)
	if err != nil {
		return nil, err
	}
	var st struct {
		Backups []backupRecord `json:"backups"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	for i := range st.Backups {
		if b := &st.Backups[i]; b.File == name && b.Error == "" && b.Size > 0 {
			return b, nil
		}
	}
	return nil, backupclient.ErrNoBackup
}

// stagedJSON is the upload waiting for the owner's confirmation.
func stagedJSON(st *domain.StagedUpload) map[string]any {
	if st == nil {
		return nil
	}
	return map[string]any{"id": st.ID, "name": st.Name, "size": st.Size, "sha256": st.SHA256, "at": st.At.Unix(),
		"expires_at": st.At.Add(domain.StagedTTL).Unix()}
}

// restoreView is everything the restore part of the Backups tab shows. It reads the updater's
// folder and the database only (nobody else is asked), so it answers at once, also while the rest
// of the install restarts, and the page polls it while a restore runs.
func (s *Server) restoreView(ctx context.Context, w http.ResponseWriter, r *http.Request, up *domain.Updates) (map[string]any, bool) {
	owner := Allowed(actor(r).Role, PermSystemWrite)
	p, err := up.Progress(ctx)
	if err != nil {
		s.fail(w, "restore progress", err)
		return nil, false
	}
	var run, other any
	if p.Run != nil {
		if p.Run.Action == store.ActionRestore {
			run = runJSON(p.Run, p.Phase)
		} else {
			other = store.ActionUpdate
		}
	}
	reason := up.CanRestore(p)
	out := map[string]any{
		"version": up.Version(), "helper": helperJSON(p.Helper, owner), "run": run, "last": runJSON(p.LastRestore, ""), "other": other,
		"can":    map[string]any{"allowed": owner, "ready": owner && reason == "", "reason": reason},
		"upload": nil,
		"limits": map[string]any{"max_upload": int64(sysupdate.MaxUpload), "staged_seconds": int64(domain.StagedTTL.Seconds())},
	}
	if owner {
		// what the owner types to confirm; the staff who may only read the page do not need it
		out["domain"] = up.StoreDomain()
		out["upload"] = stagedJSON(up.Staged())
	}
	return out, true
}

// getRestore: GET /api/v1/system/restore (owner and admin), the restore part of the Backups tab:
//
//	{version, helper, run, last, other, can, domain, upload, limits}
//
//   - helper: the server's updater as GET /system/updates gives it (action, restore (it can restore a
//     backup), subject, source, by, requested_at besides);
//   - run: the restore core handed the updater and has not ended, {id, by, action, subject, source,
//     requested_at, phase (queued | running | done | failed | rolled_back)}, or null; last: the restore
//     that ended last, with outcome, outcome_at and message, or null. other is "update" while an
//     update is open (a restore waits for it);
//   - can: {allowed (the role may), ready (a restore would be accepted now), reason (why not:
//     no_domain | helper_missing | restore_unsupported | already_running)};
//   - domain and upload (the file waiting for confirmation, {id, name, size, sha256, at, expires_at}) are
//     the owner's; limits: {max_upload (bytes), staged_seconds}.
func (s *Server) getRestore(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	if v, ok := s.restoreView(r.Context(), w, r, up); ok {
		writeJSON(w, http.StatusOK, v)
	}
}

// uploadRestore: POST /api/v1/system/restore/upload?name=<file name> (owner) receives the owner's
// backup file as the raw body (Content-Type: application/octet-stream), at most 2 GiB, 5 an hour,
// one at a time, streamed into a file of its own in the folder shared with the server's updater;
// nothing is held in memory. 201 {upload: {id, name, size, sha256, at, expires_at}}; it waits 30
// minutes for the restore to be confirmed. The file is only a file: the updater checks that it is
// a BOBRES backup, and that this server's key opens it, before it stops anything. 413 too_large,
// 415 for another content type, 400 empty, 409 busy (another upload is being received) or the
// updater cannot restore now, 429 rate_limited.
func (s *Server) uploadRestore(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/octet-stream" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "send the file as the raw body, with the type application/octet-stream")
		return
	}
	limit := up.UploadLimit()
	if r.ContentLength > limit {
		s.restoreFail(w, &domain.UpdateError{Code: domain.RestoreUploadTooLarge, Message: "the file is larger than the limit of " + up.UploadLimitText()})
		return
	}
	if !s.uploads.allow("restore-upload:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 5 uploads an hour")
		return
	}
	// A backup is large: the server's usual limits for small answers are lifted for this request.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(s.now().Add(uploadWindow))
	_ = rc.SetWriteDeadline(s.now().Add(uploadWindow + time.Minute))
	// One more byte than the limit is read, so a body over it is seen and refused.
	body := http.MaxBytesReader(w, r.Body, limit+1)
	st, err := up.StageRestoreUpload(r.Context(), actor(r), r.URL.Query().Get("name"), body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.restoreFail(w, &domain.UpdateError{Code: domain.RestoreUploadTooLarge, Message: "the file is larger than the limit of " + up.UploadLimitText()})
			return
		}
		s.restoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"upload": stagedJSON(st)})
}

// applyRestore: POST /api/v1/system/restore {backup, upload_id, confirm, acknowledge, code} (owner)
// restores the backup the owner confirmed: backup is the file name of one of the server's backups
// (the Backups tab's list), or upload_id the id of the upload that is waiting. Nothing is done
// unless
//   - confirm is the store's domain, typed, and acknowledge is true (everything after the backup's
//     time is lost, and the bot is unavailable meanwhile);
//   - an owner with a password login and an authenticator app gives a current code (403
//     code_required, 400 code_wrong);
//   - the server's updater is installed, can restore and is idle, and no update or restore of ours
//     is open.
//
// It answers 202 with the state as GET /system/restore gives it (the restore is queued), and every
// refusal as {error, code, message}: 400 invalid (ack_required, confirm_wrong), 404 not_found
// (backup_unknown, upload_missing), 409 state (helper_missing, restore_unsupported,
// already_running, no_domain) and 503 unavailable (request_failed). The request, its audit entry
// (system.restore.requested) and a Telegram notice to every owner are recorded first. At most 6 a
// hour.
func (s *Server) applyRestore(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	var in struct {
		Backup      string `json:"backup"`
		UploadID    string `json:"upload_id"`
		Confirm     string `json:"confirm"`
		Acknowledge bool   `json:"acknowledge"`
		Code        string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if (in.Backup == "") == (in.UploadID == "") {
		writeError(w, http.StatusBadRequest, "invalid", "name one backup of the server, or one upload")
		return
	}
	if !s.restores.allow("restore-apply:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 6 restore requests an hour")
		return
	}
	// The cheap checks first, so that a mistyped domain does not use up the authenticator code.
	if !in.Acknowledge {
		s.restoreFail(w, &domain.UpdateError{Code: domain.RestoreAckRequired, Message: "confirm that you understand that everything after the backup's time is lost"})
		return
	}
	if up.StoreDomain() == "" {
		s.restoreFail(w, &domain.UpdateError{Code: domain.RestoreNoDomain, Message: "core does not know this store's domain (BOBRES_DOMAIN), so a restore cannot be confirmed"})
		return
	}
	if !up.ConfirmMatches(in.Confirm) {
		s.restoreFail(w, &domain.UpdateError{Code: domain.RestoreConfirmWrong, Message: "type the store's domain exactly to confirm"})
		return
	}
	if !s.reauth(w, r, in.Code) {
		return
	}
	ai := domain.ApplyRestoreInput{UploadID: in.UploadID, Confirm: in.Confirm, Acknowledge: in.Acknowledge}
	if in.Backup != "" {
		rec, err := s.findBackup(r.Context(), in.Backup)
		switch {
		case errors.Is(err, backupclient.ErrNoBackup):
			s.restoreFail(w, &domain.UpdateError{Code: domain.RestoreBackupUnknown, Message: "the backup service has no such backup"})
			return
		case err != nil:
			s.cfg.Log.Warn("restore: the backup list was not read", "err", err)
			writeError(w, http.StatusServiceUnavailable, "unavailable", "the backup service did not answer; try again in a minute")
			return
		}
		ai.Backup = &domain.RestoreBackup{File: rec.File, Size: rec.Size, SHA256: rec.SHA256}
	}
	// Once asked, the request is recorded and handed over even if the page goes away.
	ctx := context.WithoutCancel(r.Context())
	if _, err := up.ApplyRestore(ctx, actor(r), ai); err != nil {
		s.restoreFail(w, err)
		return
	}
	v, ok := s.restoreView(ctx, w, r, up)
	if !ok {
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

// restoreFail answers a refusal with the code the page translates (domain.UpdateError).
func (s *Server) restoreFail(w http.ResponseWriter, err error) {
	var ue *domain.UpdateError
	if !errors.As(err, &ue) {
		s.fail(w, "restore", err)
		return
	}
	status, kind := http.StatusConflict, "state"
	switch ue.Code {
	case domain.RestoreAckRequired, domain.RestoreConfirmWrong, domain.RestoreUploadEmpty:
		status, kind = http.StatusBadRequest, "invalid"
	case domain.RestoreBackupUnknown, domain.RestoreUploadMissing:
		status, kind = http.StatusNotFound, "not_found"
	case domain.RestoreUploadTooLarge:
		status, kind = http.StatusRequestEntityTooLarge, domain.RestoreUploadTooLarge
	case domain.UpdateRequestFailed:
		status, kind = http.StatusServiceUnavailable, "unavailable"
	}
	writeJSON(w, status, map[string]any{"error": kind, "code": ue.Code, "message": ue.Message})
}

// downloadTicket: POST /api/v1/system/backups/download-ticket {file, code} (owner) checks the
// owner's authenticator code (where the session has one: 403 code_required, 400 code_wrong) and
// that the backup service lists the file, and answers {url, expires_in (seconds)}: a single-use
// address, good for a minute and for this session's user only, that the browser opens as its own
// download. At most 10 an hour.
func (s *Server) downloadTicket(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	if _, ok := s.cfg.Backups.(BackupDownloader); !ok {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "backups are not installed on this server")
		return
	}
	var in struct {
		File string `json:"file"`
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !backup.ValidFileName(in.File) {
		writeError(w, http.StatusNotFound, "not_found", "no such backup")
		return
	}
	if !s.downloads.allow("backup-download:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 10 downloads an hour")
		return
	}
	if !s.reauth(w, r, in.Code) {
		return
	}
	switch _, err := s.findBackup(r.Context(), in.File); {
	case errors.Is(err, backupclient.ErrNoBackup):
		writeError(w, http.StatusNotFound, "not_found", "no such backup")
		return
	case err != nil:
		s.cfg.Log.Warn("backup download: the backup list was not read", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the backup service did not answer; try again in a minute")
		return
	}
	token := s.tickets.issue(actor(r).ID, in.File, s.now())
	if token == "" {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "too many downloads are being prepared; try again in a minute")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": "/api/v1/system/backups/download/" + token, "expires_in": int(ticketTTL.Seconds())})
}

// downloadBackup: GET /api/v1/system/backups/download/{ticket} (owner) streams the backup the
// ticket is for, as it is on the server: encrypted, with Content-Disposition: attachment, copied
// through in chunks and never held in memory. The key that opens it is not sent, and not
// recoverable from it. The download is audited (system.backup.download) and told to every owner on
// Telegram before the first byte is sent: if that cannot be recorded, nothing is sent.
func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	dl, ok := s.cfg.Backups.(BackupDownloader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "backups are not installed on this server")
		return
	}
	file, ok := s.tickets.redeem(r.PathValue("ticket"), actor(r).ID, s.now())
	if !ok {
		writeError(w, http.StatusForbidden, "ticket", "this download link has expired or was used; ask for a new one")
		return
	}
	body, size, err := dl.Download(r.Context(), file)
	switch {
	case errors.Is(err, backupclient.ErrNoBackup):
		writeError(w, http.StatusNotFound, "not_found", "no such backup")
		return
	case err != nil:
		s.cfg.Log.Warn("backup download: the backup service did not give the file", "file", file, "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the backup service did not answer; try again in a minute")
		return
	}
	defer body.Close() //nolint:errcheck // read-only
	if err := up.RecordBackupDownload(r.Context(), actor(r), file, size); err != nil {
		s.cfg.Log.Error("backup download: it could not be recorded, so nothing was sent", "file", file, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "the download could not be recorded, so it was not sent; try again")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(s.now().Add(downloadWindow))
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("Content-Disposition", `attachment; filename="`+file+`"`) // a name that matches backup.ValidFileName: no quote or path in it
	w.WriteHeader(http.StatusOK)
	n, err := io.Copy(w, io.LimitReader(body, size))
	if err != nil || n != size {
		s.cfg.Log.Warn("backup download: it did not reach the end", "file", file, "sent", n, "size", size, "err", err)
	}
}
