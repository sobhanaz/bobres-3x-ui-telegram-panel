package domain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
)

// Downloading a backup and restoring one from the dashboard (System > Backups, docs/04-dashboard.md).
//
// A download is the owner's, with the authenticator code where the session has one: the backup
// service streams the encrypted file to core and core on to the owner's browser, never in memory;
// the key that opens it is never sent. Every download is audited and told to every owner on
// Telegram before the first byte is sent (RecordBackupDownload).
//
// A restore replaces ALL the store's data with a backup's. Core never touches Docker: like an
// update, it is a request file for the server's root helper (internal/sysupdate), which checks the
// backup, takes a safety backup of the current data, restores, checks that the store is healthy and
// goes back to the safety backup if it is not. The owner confirms by typing the store's domain, ticks
// that what happened after the backup is lost, and gives the authenticator code where there is one.
//
// The restore replaces this very database, so core's record of the request (the row in
// core.system_updates, the audit entry) is gone when it is back, whatever the outcome of a restore
// that worked. The end of the request is therefore found again from the helper's status, which
// carries the backup, who asked and when (adoptRestore): it is written as a row that is born ended,
// once (the id is its key), with its audit entry and the notices to the owners. A restore that
// failed or went back keeps its row (the safety backup holds it), and ends as an update does.

// Why a download or a restore is refused (UpdateError.Code), besides the update's own.
const (
	// RestoreUnsupported: the server's updater is from before the dashboard's restore.
	RestoreUnsupported = "restore_unsupported"
	// RestoreNoDomain: core does not know the store's domain, so there is nothing to confirm with.
	RestoreNoDomain = "no_domain"
	// RestoreConfirmWrong: what the owner typed is not the store's domain.
	RestoreConfirmWrong = "confirm_wrong"
	// RestoreAckRequired: the owner did not tick that the data after the backup is lost.
	RestoreAckRequired = "ack_required"
	// RestoreBackupUnknown: the backup service has no such backup.
	RestoreBackupUnknown = "backup_unknown"
	// RestoreUploadMissing: there is no upload by that id (it expired, was replaced, or core restarted).
	RestoreUploadMissing = "upload_missing"
	// RestoreUploadBusy: another upload is being received.
	RestoreUploadBusy = "upload_busy"
	// RestoreUploadTooLarge and RestoreUploadEmpty: the file is over the limit, or has nothing in it.
	RestoreUploadTooLarge = "upload_too_large"
	RestoreUploadEmpty    = "upload_empty"
)

const (
	// StagedTTL is how long an uploaded backup waits for the owner's confirmation.
	StagedTTL = 30 * time.Minute
	// uploadLeftover is how old a file in the upload folder is before core removes it: nothing of
	// an upload that was confirmed stays (the helper removes the file it used).
	uploadLeftover = 2 * time.Hour
	// adoptWindow is how long after the end of a restore core still records it from the helper's
	// status: core is back within minutes; a status older than this is another day's.
	adoptWindow = 3 * time.Hour
	// restoreStuck is how long a restore the helper says it runs may go on (systemd stops the
	// helper after 3 hours).
	restoreStuck = 3 * time.Hour
)

// restoreState is the restore's own state in Updates.
type restoreState struct {
	// receiving is held while an upload is being received: one at a time.
	receiving sync.Mutex
	// mu guards the rest.
	mu        sync.Mutex
	staged    *StagedUpload
	adopted   map[string]bool
	lastPurge time.Time
}

func (u *Updates) restoreStuckAfter() time.Duration { return restoreStuck }

// StagedUpload is a backup the owner uploaded, waiting in the upload folder for the confirmation.
type StagedUpload struct {
	ID     string
	Name   string // as the owner's browser named it, cut to one plain line
	Size   int64
	SHA256 string
	At     time.Time
	By     string // the dashboard user who uploaded it
}

// StoreDomain is what an owner types to confirm a restore ("" when core does not know it).
func (u *Updates) StoreDomain() string { return u.storeDomain }

// Staged is the upload waiting for confirmation (nil when none is, or it expired).
func (u *Updates) Staged() *StagedUpload {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.staged != nil && u.now().Sub(u.staged.At) > StagedTTL {
		u.dropStagedLocked()
	}
	if u.staged == nil {
		return nil
	}
	c := *u.staged
	return &c
}

func (u *Updates) dropStagedLocked() {
	if u.staged == nil {
		return
	}
	if u.helper != nil {
		if err := u.helper.RemoveUpload(u.staged.ID); err != nil {
			u.log.Warn("restore: an upload was not removed", "id", u.staged.ID, "err", err)
		}
	}
	u.staged = nil
}

// CanRestore says whether a restore would be accepted now: "" when yes, else the code of the
// refusal (the page enables its button by it; the request is checked again when it is made).
func (u *Updates) CanRestore(p *UpdateProgress) string {
	switch {
	case u.storeDomain == "":
		return RestoreNoDomain
	case !p.Helper.Available:
		return UpdateHelperMissing
	case !p.Helper.Status.Restore:
		return RestoreUnsupported
	case p.Helper.Running() || p.Run != nil:
		return UpdateAlreadyRunning
	}
	return ""
}

// restoreGate is CanRestore as an error, after the owner check.
func (u *Updates) restoreGate(ctx context.Context, actor *store.User) (*UpdateProgress, error) {
	if actor == nil || actor.Role != "owner" {
		return nil, ErrForbidden
	}
	p, err := u.Progress(ctx)
	if err != nil {
		return nil, err
	}
	switch u.CanRestore(p) {
	case "":
		return p, nil
	case RestoreNoDomain:
		return nil, updateRefusal(RestoreNoDomain, "core does not know this store's domain (BOBRES_DOMAIN), so a restore cannot be confirmed")
	case UpdateHelperMissing:
		return nil, updateRefusal(UpdateHelperMissing, "the server's updater is not installed yet: run sudo bobres update once on the server")
	case RestoreUnsupported:
		return nil, updateRefusal(RestoreUnsupported, "the server's updater is from before restoring from the dashboard: run sudo bobres update once on the server")
	default:
		return nil, updateRefusal(UpdateAlreadyRunning, "an update or a restore is already being made")
	}
}

// StageRestoreUpload receives a backup the owner uploads and keeps it in the folder shared with the
// updater, in a file of its own, until the owner confirms the restore (StagedTTL). One upload is
// kept at a time (a new one replaces it) and one is received at a time. Nothing in it is trusted:
// it is a file, hashed and counted as it is written, and the updater checks the format and the key
// before it stops anything. The caller has checked the owner's role and CSRF token.
func (u *Updates) StageRestoreUpload(ctx context.Context, actor *store.User, name string, body io.Reader) (*StagedUpload, error) {
	if _, err := u.restoreGate(ctx, actor); err != nil {
		return nil, err
	}
	if !u.receiving.TryLock() {
		return nil, updateRefusal(RestoreUploadBusy, "another backup is being uploaded")
	}
	defer u.receiving.Unlock()
	u.mu.Lock()
	u.dropStagedLocked()
	u.mu.Unlock()

	id, err := newRequestID()
	if err != nil {
		return nil, err
	}
	up, err := u.helper.StageUpload(id, body, sysupdate.MaxUpload)
	switch {
	case errors.Is(err, sysupdate.ErrTooLarge):
		return nil, updateRefusal(RestoreUploadTooLarge, "the file is larger than %d MiB", sysupdate.MaxUpload>>20)
	case errors.Is(err, sysupdate.ErrEmptyUpload):
		return nil, updateRefusal(RestoreUploadEmpty, "the file is empty")
	case err != nil:
		return nil, fmt.Errorf("restore: stage the upload: %w", err)
	}
	if name = sysupdate.Clean(name, sysupdate.MaxFileName); name == "" {
		name = "backup.bkp"
	}
	st := &StagedUpload{ID: up.ID, Name: name, Size: up.Size, SHA256: up.SHA256, At: u.now(), By: requesterName(actor)}
	// Recorded as soon as it is on the server: the file is a copy of the store's data from somewhere.
	err = u.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		return u.s.auditChange(ctx, tx, actor, "system.restore.upload", "system_update", st.ID, nil,
			map[string]any{"name": st.Name, "size": st.Size, "sha256": st.SHA256, "by": st.By}, "")
	})
	if err != nil {
		_ = u.helper.RemoveUpload(up.ID)
		return nil, err
	}
	u.mu.Lock()
	u.staged = st
	u.mu.Unlock()
	u.log.Info("restore: a backup was uploaded", "id", st.ID, "size", st.Size, "by", st.By)
	return st, nil
}

// RestoreBackup is a backup of the server as the backup service lists it: the file, its size and
// its SHA-256 (the caller took them from the service's own list).
type RestoreBackup struct {
	File   string
	Size   int64
	SHA256 string
}

// ApplyRestoreInput is what an owner confirmed in the dashboard.
type ApplyRestoreInput struct {
	// Backup is the backup of the server to restore (the page's list), or nil to restore the
	// upload named by UploadID.
	Backup   *RestoreBackup
	UploadID string
	// Confirm is what the owner typed: the store's domain. Acknowledge is the tick that says
	// everything after the backup is lost and the bot is unavailable meanwhile.
	Confirm     string
	Acknowledge bool
}

// ApplyRestore asks the server's updater to restore the backup the owner confirmed. The caller has
// checked the owner's role, CSRF token and, where there is one, authenticator code. Nothing is done
// unless
//   - the owner ticked the acknowledgement and typed the store's domain;
//   - the updater is installed, can restore, and is idle, and no update or restore of ours is open;
//   - the backup is one the backup service listed (name, size and hash taken from its list) or the
//     upload this core received and still holds.
//
// The request is recorded first (the row, the audit entry and the notices to every owner, in one
// transaction), and only then written for the updater, so no request exists that was not recorded
// and announced. A request that cannot be written is recorded as failed.
func (u *Updates) ApplyRestore(ctx context.Context, actor *store.User, in ApplyRestoreInput) (*store.SystemUpdate, error) {
	if _, err := u.restoreGate(ctx, actor); err != nil {
		return nil, err
	}
	if !in.Acknowledge {
		return nil, updateRefusal(RestoreAckRequired, "confirm that you understand that everything after the backup's time is lost")
	}
	if !strings.EqualFold(strings.TrimSpace(in.Confirm), u.storeDomain) {
		return nil, updateRefusal(RestoreConfirmWrong, "type the store's domain exactly to confirm")
	}
	var (
		source, file, upload, sum string
		size                      int64
	)
	if in.Backup != nil {
		b := in.Backup
		if !backup.ValidFileName(b.File) || b.Size < 1 || b.Size > sysupdate.MaxUpload || len(b.SHA256) != 64 {
			return nil, updateRefusal(RestoreBackupUnknown, "the backup service has no such backup")
		}
		source, file, sum, size = sysupdate.SourceBackup, b.File, b.SHA256, b.Size
	} else {
		st := u.Staged()
		if st == nil || st.ID != in.UploadID {
			return nil, updateRefusal(RestoreUploadMissing, "the uploaded backup is gone (it waits %d minutes): upload it again", int(StagedTTL.Minutes()))
		}
		source, file, upload, sum, size = sysupdate.SourceUpload, st.Name, st.ID, st.SHA256, st.Size
	}

	name := requesterName(actor)
	idv, err := newRequestID()
	if err != nil {
		return nil, err
	}
	run := &store.SystemUpdate{ID: idv, RequestedBy: &actor.ID, RequestedByName: name, From: u.version, To: u.version,
		Action: store.ActionRestore, Subject: file, Source: source}
	err = u.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := u.s.st.CreateSystemUpdate(ctx, tx, run); err != nil {
			if errors.Is(err, store.ErrUpdateOpen) {
				return updateRefusal(UpdateAlreadyRunning, "an update or a restore is already being made")
			}
			return err
		}
		if err := u.s.auditChange(ctx, tx, actor, "system.restore.requested", "system_update", run.ID, nil,
			map[string]any{"backup": file, "source": source, "size": size, "sha256": sum, "by": name}, ""); err != nil {
			return err
		}
		return u.tellOwners(ctx, tx, run, events.UpdateRequested, "", run.RequestedAt)
	})
	if err != nil {
		return nil, err
	}

	req, err := sysupdate.NewRestoreRequest(run.ID, source, file, upload, sum, size, name, run.RequestedAt)
	if err == nil {
		err = u.helper.WriteRestoreRequest(req)
	}
	if err != nil {
		u.log.Error("restore: the request could not be handed to the updater", "id", run.ID, "err", err)
		endCtx := context.WithoutCancel(ctx)
		if _, eerr := u.end(endCtx, run, events.UpdateFailed, "the request could not be handed to the server's updater"); eerr != nil {
			u.log.Error("restore: the failed request was not recorded", "id", run.ID, "err", eerr)
		}
		return nil, updateRefusal(UpdateRequestFailed, "the request could not be handed to the server's updater (is its folder writable by core?)")
	}
	if upload != "" { // the updater owns the file from here
		u.mu.Lock()
		u.staged = nil
		u.mu.Unlock()
	}
	u.log.Info("restore requested", "id", run.ID, "backup", file, "source", source, "by", name)
	return run, nil
}

// RecordBackupDownload records that actor is downloading the backup file (size bytes): the audit
// entry and the notices to every owner are written in one transaction, before the first byte is
// sent. An error means nothing was recorded and nothing may be sent.
func (u *Updates) RecordBackupDownload(ctx context.Context, actor *store.User, file string, size int64) error {
	if actor == nil || actor.Role != "owner" {
		return ErrForbidden
	}
	name := requesterName(actor)
	return u.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := u.s.auditChange(ctx, tx, actor, "system.backup.download", "backup", file, nil,
			map[string]any{"file": file, "size": size, "by": name}, ""); err != nil {
			return err
		}
		return u.tellOwnersBackup(ctx, tx, events.SystemBackupEvent{Kind: events.BackupDownload, State: events.BackupDownloaded, File: file, By: name,
			At: u.now().Unix(), Size: size})
	})
}

// tellOwnersBackup writes e for each owner who has a Telegram account, in tx, in the owner's own language.
func (u *Updates) tellOwnersBackup(ctx context.Context, tx pgx.Tx, e events.SystemBackupEvent) error {
	staff, err := u.s.st.ListStaffContacts(ctx, tx, false)
	if err != nil {
		return err
	}
	for _, o := range staff {
		if o.Role != "owner" || o.TelegramID <= 0 {
			continue
		}
		e.TelegramID, e.Language = o.TelegramID, i18n.Normalize(o.Language)
		if err := u.s.publish(ctx, tx, events.SystemBackup, e); err != nil {
			return err
		}
	}
	return nil
}

// adoptRestore records the end of a restore that replaced this database (see the top of this
// file), from the updater's status: its id, the backup, who asked and when, and how it ended. It
// does nothing for a status that is not a finished restore of the last few hours, or one it
// recorded already. The row, the audit entry and the notices are written in one transaction by
// whichever core inserts the row first.
func (u *Updates) adoptRestore(ctx context.Context) error {
	h := u.Helper()
	st := h.Status
	if !h.Available || st.Action != sysupdate.ActionRestore || !sysupdate.Finished(st.State) || st.ID == "" || st.Subject == "" ||
		st.Source == "" || st.RequestedAt == nil || st.FinishedAt == nil {
		return nil
	}
	if age := u.now().Sub(*st.FinishedAt); age > adoptWindow || age < -5*time.Minute {
		return nil
	}
	u.mu.Lock()
	done := u.adopted[st.ID]
	u.mu.Unlock()
	if done {
		return nil
	}
	by := sysupdate.Clean(st.By, sysupdate.MaxBy)
	if by == "" {
		by = "dashboard"
	}
	message := sysupdate.Clean(st.Message, sysupdate.MaxMessage)
	run := &store.SystemUpdate{ID: st.ID, RequestedByName: by, From: u.version, To: u.version, Action: store.ActionRestore, Subject: st.Subject,
		Source: st.Source, RequestedAt: *st.RequestedAt, Outcome: st.State, Message: message}
	var adopted bool
	err := u.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		adopted = false
		ok, err := u.s.st.AdoptSystemRestore(ctx, tx, run)
		if err != nil || !ok {
			return err
		}
		adopted = true
		// The audit log is the restored one: this entry says everything the lost ones did.
		if err := u.s.auditChange(ctx, tx, nil, "system.restore."+st.State, "system_update", run.ID, nil, endDetail(run, message), ""); err != nil {
			return err
		}
		return u.tellOwners(ctx, tx, run, st.State, message, u.now())
	})
	if err != nil {
		return err
	}
	u.mu.Lock()
	if u.adopted == nil {
		u.adopted = map[string]bool{}
	}
	u.adopted[st.ID] = true
	u.mu.Unlock()
	if adopted {
		u.log.Info("restore ended (recorded from the updater's status)", "id", run.ID, "outcome", st.State, "backup", run.Subject)
	}
	return nil
}

// purgeUploads removes what is left in the upload folder (an upload nobody confirmed, one that was
// cut off), at most once a minute; the upload waiting for confirmation is kept.
func (u *Updates) purgeUploads() {
	if u.helper == nil {
		return
	}
	u.mu.Lock()
	if u.now().Sub(u.lastPurge) < time.Minute {
		u.mu.Unlock()
		return
	}
	u.lastPurge = u.now()
	if u.staged != nil && u.now().Sub(u.staged.At) > StagedTTL {
		u.dropStagedLocked()
	}
	keep := map[string]bool{}
	if u.staged != nil {
		keep[u.staged.ID] = true
	}
	u.mu.Unlock()
	if _, err := u.helper.PurgeUploads(keep, uploadLeftover, u.now()); err != nil && !errors.Is(err, sysupdate.ErrNoDir) {
		u.log.Warn("restore: the upload folder was not cleaned", "err", err)
	}
}
