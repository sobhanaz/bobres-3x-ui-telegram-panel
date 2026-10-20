package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Updating the install from the dashboard (System > Updates, docs/04-dashboard.md).
//
// Core never touches Docker or the host. The bot, which has internet access,
// finds the newest release of the channel and verifies its signature; when an
// owner confirms, core records the request (core.system_updates, the audit log,
// a Telegram notice to every owner), then drops a request file into the folder
// it shares with the server's updater (internal/sysupdate), a root service on
// the host that runs the same verified `bobres update` as the command line
// (signature check, backup, pull, recreate, health check, automatic rollback)
// and writes its progress to a status file. The update restarts core itself,
// so the end of an update is noticed by a worker (Updates.Run) that reads the
// status again whenever core is up: it ends the open request exactly once,
// with its audit entry and the notices to the owners.

// UpdateCheck is the bot's last look for a newer release (release.Status as the
// bot's /internal/update-check answers it).
type UpdateCheck struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	// Channel is the release channel the bot follows (stable or beta; an
	// older bot sent the latest release's own channel here).
	Channel string `json:"channel"`
	// Issued is when Latest was signed (2006-01-02T15:04Z), "" when the bot
	// does not say.
	Issued    string `json:"issued,omitempty"`
	Newer     bool   `json:"newer"`
	Verified  bool   `json:"verified"`
	CheckedAt int64  `json:"checked_at"`
	Error     string `json:"error"`
	Detail    string `json:"detail"`
	// Covered is core's own answer, never the bot's, and ADVISORY: whether
	// the installed license's update date (updates_until) is not before the
	// day Latest was issued. false adds a note ("this release is newer than
	// your license's update date") to the update check and hides nothing:
	// the update is offered and installed all the same. nil when no license
	// is in force (the free tier, the trial and an ended license have no
	// update date) or the day is not known.
	Covered *bool `json:"covered,omitempty"`
}

// UpdateSource is the bot, as core asks it for the newest release (botfiles.Client).
type UpdateSource interface {
	// CheckUpdates reads the bot's last outcome (it does not look again).
	CheckUpdates(ctx context.Context) (*UpdateCheck, error)
	// CheckUpdatesNow makes the bot look at GitHub now and returns what it found.
	CheckUpdatesNow(ctx context.Context) (*UpdateCheck, error)
}

// UpdateHelper is core's side of the folder it shares with the server's updater
// (sysupdate.Dir).
type UpdateHelper interface {
	ReadStatus() (sysupdate.Status, error)
	WriteRequest(r sysupdate.Request) error
	ReadRequest() (*sysupdate.Request, error)
	RemoveRequest(id string) (bool, error)
}

// Why an update is refused (UpdateError.Code); the dashboard translates them.
const (
	// UpdateHelperMissing: the server's updater is not installed (yet), or its
	// folder cannot be read.
	UpdateHelperMissing = "helper_missing"
	// UpdateAlreadyRunning: an update is being made (by the updater's own
	// word, or one core handed it that has not ended).
	UpdateAlreadyRunning = "already_running"
	// UpdateCheckUnavailable: the bot, which finds releases, did not answer.
	UpdateCheckUnavailable = "check_unavailable"
	// UpdateNoRelease: the bot has no verified release (GitHub unreachable or
	// the signature check failed).
	UpdateNoRelease = "no_release"
	// UpdateNotNewer: the release is not newer than what runs.
	UpdateNotNewer = "not_newer"
	// UpdateVersionChanged: the newest release is not the one the owner
	// confirmed (it was replaced since: check again).
	UpdateVersionChanged = "version_changed"
	// UpdateChannelChanged: the channel is not the one the owner saw.
	UpdateChannelChanged = "channel_changed"
	// UpdatePrereleaseUnconfirmed: a pre-release needs its own explicit yes.
	UpdatePrereleaseUnconfirmed = "prerelease_unconfirmed"
	// UpdateDevBuild: core is a development build, which has no version to
	// compare.
	UpdateDevBuild = "dev_build"
	// UpdateRequestFailed: the request could not be handed to the updater.
	UpdateRequestFailed = "request_failed"
)

// UpdateError is a refusal of an update, with the code the dashboard
// translates. It is an ErrState.
type UpdateError struct {
	Code    string
	Message string
	// Latest is the release the bot has now, for UpdateVersionChanged.
	Latest string
}

func (e *UpdateError) Error() string { return ErrState.Error() + ": " + e.Message }
func (e *UpdateError) Unwrap() error { return ErrState }

func updateRefusal(code, format string, args ...any) *UpdateError {
	return &UpdateError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Why the updater is not usable (UpdateHelperState.Problem).
const (
	HelperNoDir        = "no_dir"        // its folder is not mounted into core
	HelperNoStatus     = "no_status"     // the folder is there; the updater never wrote in it
	HelperNotInstalled = "not_installed" // its status says it is not installed
	HelperBadStatus    = "bad_status"    // its status is unreadable (a link, too big, not JSON)
	HelperUnsupported  = "unsupported"   // another protocol version
)

// UpdateHelperState is what core can tell about the server's updater.
type UpdateHelperState struct {
	// Available: the updater is installed and its status can be read.
	Available bool
	// Problem says why not (Helper*); "" when it is available.
	Problem string
	// Status is what the updater said (cleaned); zero when it said nothing
	// that can be read.
	Status sysupdate.Status
}

// Running reports that the updater says an update is being made.
func (h UpdateHelperState) Running() bool {
	return h.Available && h.Status.State == sysupdate.StateRunning
}

// Phases of the update core handed to the updater (UpdateProgress.Phase).
const (
	// PhaseQueued: the request is written; the updater has not taken it up.
	PhaseQueued = "queued"
	// PhaseRunning: the updater is making the update.
	PhaseRunning = "running"
)

// Updates is the update from the dashboard: what the dashboard shows, the
// request, and the worker that ends it.
type Updates struct {
	s       *Service
	src     UpdateSource
	helper  UpdateHelper
	version string
	log     *slog.Logger
	now     func() time.Time

	// PickupWait is how long a request may wait for the updater to take it
	// up before core gives up on it (removes it, records it as not started
	// and tells the owners). StuckAfter is how long an update the updater
	// says it is running may go without ending before core records it as
	// failed (the updater's own status stays what it says, and keeps another
	// update from starting).
	PickupWait, StuckAfter time.Duration
	// StatusStale is how long a "running" status may go unwritten before core
	// takes it for an update that was interrupted (the server lost power, the
	// updater was killed): the updater rewrites it at least every 30 seconds
	// while it runs, so this is many missed beats. Zero: never.
	StatusStale time.Duration
	// Every is how often Run looks.
	Every time.Duration

	lastErr string // the last Step failure logged, so one that repeats is one line
}

// UpdatesConfig wires Updates.
type UpdatesConfig struct {
	// Source is the bot (nil: no bot, so no release is known and nothing can
	// be requested).
	Source UpdateSource
	// Helper is the updater's folder.
	Helper UpdateHelper
	// Version is core's own version (default: this binary's).
	Version string
	Log     *slog.Logger
}

// NewUpdates builds the update service.
func (s *Service) NewUpdates(cfg UpdatesConfig) *Updates {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Version == "" {
		cfg.Version = version.Version
	}
	return &Updates{s: s, src: cfg.Source, helper: cfg.Helper, version: cfg.Version, log: cfg.Log.With("worker", "updates"), now: time.Now,
		PickupWait: 15 * time.Minute, StuckAfter: 60 * time.Minute, StatusStale: 10 * time.Minute, Every: 5 * time.Second}
}

// Version is the version core runs.
func (u *Updates) Version() string { return u.version }

// Helper reads the updater's status and says whether it can be used.
func (u *Updates) Helper() UpdateHelperState {
	if u.helper == nil {
		return UpdateHelperState{Problem: HelperNoDir}
	}
	st, err := u.helper.ReadStatus()
	switch {
	case err == nil:
		return UpdateHelperState{Available: true, Status: u.interrupted(st)}
	case errors.Is(err, sysupdate.ErrNotInstalled):
		return UpdateHelperState{Problem: HelperNotInstalled, Status: st}
	case errors.Is(err, sysupdate.ErrNoDir):
		return UpdateHelperState{Problem: HelperNoDir}
	case errors.Is(err, sysupdate.ErrNoStatus):
		return UpdateHelperState{Problem: HelperNoStatus}
	case errors.Is(err, sysupdate.ErrUnsupported):
		return UpdateHelperState{Problem: HelperUnsupported}
	}
	return UpdateHelperState{Problem: HelperBadStatus}
}

// interrupted reads a status that says "running" but that the updater has not
// written for StatusStale as what it is: an update that was cut off (the server
// lost power, the updater was killed). It is then a failed one, so the dashboard
// does not wait for it for ever and another update may be asked for (the
// updater's lock keeps two from running at once whatever core believes, and
// clears the status it finds in this state). A status of any other state, or
// whose age is not known, is returned as it is.
func (u *Updates) interrupted(st sysupdate.Status) sysupdate.Status {
	if st.State != sysupdate.StateRunning || st.ModTime.IsZero() || u.StatusStale <= 0 {
		return st
	}
	silent := u.now().Sub(st.ModTime)
	if silent <= u.StatusStale {
		return st
	}
	st.State = sysupdate.StateFailed
	st.Message = fmt.Sprintf("The update was interrupted: the updater has said nothing for %d minutes (did the server stop?). Check the store: sudo bobres status",
		int(silent.Minutes()))
	finished := st.ModTime
	st.FinishedAt = &finished
	return st
}

// Check reads the bot's last outcome (cached): nil with an error when the bot
// does not answer.
func (u *Updates) Check(ctx context.Context) (*UpdateCheck, error) {
	if u.src == nil {
		return nil, errors.New("no bot is set")
	}
	return u.src.CheckUpdates(ctx)
}

// CheckNow makes the bot look at GitHub now.
func (u *Updates) CheckNow(ctx context.Context) (*UpdateCheck, error) {
	if u.src == nil {
		return nil, errors.New("no bot is set")
	}
	return u.src.CheckUpdatesNow(ctx)
}

// Channel is the channel the owner chose in the dashboard ("" when none: the
// server's BOBRES_UPDATE_CHANNEL decides).
func (u *Updates) Channel(ctx context.Context) (string, error) {
	set, err := u.s.Settings(ctx)
	if err != nil {
		return "", err
	}
	return set[release.SettingChannel], nil
}

// SetChannel sets the update channel from the dashboard: "stable", "beta", or
// "" to follow the server's BOBRES_UPDATE_CHANNEL again. Only an owner may.
// It reports whether the setting changed (the change is audited as any
// setting is).
func (u *Updates) SetChannel(ctx context.Context, actor *store.User, channel string) (bool, error) {
	if actor == nil || actor.Role != "owner" {
		return false, ErrForbidden
	}
	value := strings.TrimSpace(channel)
	if _, err := NormalizeSetting(release.SettingChannel, value); err != nil {
		return false, err
	}
	changes, err := u.s.SetSettings(ctx, actor, map[string]string{release.SettingChannel: value})
	if err != nil {
		return false, err
	}
	return len(changes) > 0, nil
}

// UpdateProgress is the state of an update: the updater's own word, the update
// core handed it (if one is open) and the one that ended last.
type UpdateProgress struct {
	Helper UpdateHelperState
	// Run is the open update (nil when none), and Phase where it is: queued
	// until the updater takes it up, then running.
	Run   *store.SystemUpdate
	Phase string
	// Last is the update that ended last (nil when none did).
	Last *store.SystemUpdate
}

// Progress reads the updater's status and core's records. It asks nobody
// else, so it is cheap to poll, also while the rest of the install restarts.
func (u *Updates) Progress(ctx context.Context) (*UpdateProgress, error) {
	p := &UpdateProgress{Helper: u.Helper()}
	run, err := u.s.st.OpenSystemUpdate(ctx, u.s.st.Conn())
	switch {
	case err == nil:
		p.Run = run
		p.Phase = PhaseQueued
		if p.Helper.Available && p.Helper.Status.ID == run.ID {
			switch st := p.Helper.Status.State; {
			case st == sysupdate.StateRunning:
				p.Phase = PhaseRunning
			case sysupdate.Finished(st):
				p.Phase = st // about to be ended by the worker
			}
		}
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	last, err := u.s.st.LastSystemUpdate(ctx, u.s.st.Conn())
	switch {
	case err == nil:
		p.Last = last
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	return p, nil
}

// Target is the release an update would install: the newest verified release the
// bot has, newer than what runs. A refusal says why not (an *UpdateError).
func (u *Updates) Target(check *UpdateCheck) (string, *UpdateError) {
	switch {
	case check == nil:
		return "", updateRefusal(UpdateCheckUnavailable, "the bot, which looks for releases, did not answer")
	case !check.Verified || !release.ValidNumber(check.Latest):
		return "", updateRefusal(UpdateNoRelease, "no release whose signature was checked is known: check again")
	case !release.Valid(u.version):
		return "", updateRefusal(UpdateDevBuild, "this core is a development build (%s), which cannot say what is newer", u.version)
	case release.Compare(check.Latest, u.version) <= 0:
		return "", updateRefusal(UpdateNotNewer, "release %s is not newer than %s, which runs", check.Latest, u.version)
	}
	return check.Latest, nil
}

// CanApply says whether an update would be accepted now, as the dashboard
// shows it: "" when yes, else the code of the refusal.
func (u *Updates) CanApply(check *UpdateCheck, p *UpdateProgress) string {
	if !p.Helper.Available {
		return UpdateHelperMissing
	}
	if p.Helper.Running() || p.Run != nil {
		return UpdateAlreadyRunning
	}
	if _, refusal := u.Target(check); refusal != nil {
		return refusal.Code
	}
	return ""
}

// ApplyUpdate is what an owner confirmed in the dashboard.
type ApplyUpdate struct {
	// Version is the release the owner saw and confirmed: it must still be the
	// newest the bot has verified, or nothing is done (a release that appeared
	// since was never confirmed). It is what the updater is asked to install.
	Version string
	// Channel is the channel the owner saw ("" for any): it must be the
	// channel the bot follows now.
	Channel string
	// AcceptPrerelease is the owner's explicit yes to a pre-release.
	AcceptPrerelease bool
}

// Apply asks the server's updater to update the install to the release the
// owner confirmed. The caller has checked the owner's role, CSRF token and, where
// there is one, authenticator code. Nothing is done unless
//   - the updater is installed and says it is not busy, and no update of ours is open;
//   - the bot has verified the release, it is the one named, on the channel
//     the owner saw, and newer than what runs (a pre-release only with an
//     explicit yes).
//
// The request is recorded first (the row, the audit entry and the notices
// to every owner, in one transaction), and only then written for the updater,
// so no request exists that was not recorded and announced. A request that
// cannot be written is recorded as failed. The error is an *UpdateError for
// every refusal.
func (u *Updates) Apply(ctx context.Context, actor *store.User, in ApplyUpdate) (*store.SystemUpdate, error) {
	if actor == nil || actor.Role != "owner" {
		return nil, ErrForbidden
	}
	p, err := u.Progress(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case !p.Helper.Available:
		return nil, updateRefusal(UpdateHelperMissing, "the server's updater is not installed yet: run sudo bobres update once on the server")
	case p.Helper.Running() || p.Run != nil:
		return nil, updateRefusal(UpdateAlreadyRunning, "an update is already being made")
	}
	check, err := u.Check(ctx)
	if err != nil {
		u.log.Warn("update: the bot did not answer", "err", err)
		check = nil
	}
	target, refusal := u.Target(check)
	if refusal != nil {
		return nil, refusal
	}
	if in.Version != target {
		return nil, &UpdateError{Code: UpdateVersionChanged, Latest: target,
			Message: fmt.Sprintf("the newest release is %s, not the %s you confirmed: check again", target, in.Version)}
	}
	channel, perr := release.ParseChannel(check.Channel)
	if perr != nil || strings.TrimSpace(check.Channel) == "" {
		return nil, updateRefusal(UpdateCheckUnavailable, "the bot did not say which channel it follows: check again")
	}
	if want := strings.TrimSpace(in.Channel); want != "" && want != channel {
		return nil, updateRefusal(UpdateChannelChanged, "the install follows the %s channel now, not %s: check again", channel, want)
	}
	if release.Prerelease(target) && !in.AcceptPrerelease {
		return nil, updateRefusal(UpdatePrereleaseUnconfirmed, "release %s is a pre-release; confirm that you want it", target)
	}

	name := requesterName(actor)
	idv, err := newRequestID()
	if err != nil {
		return nil, err
	}
	run := &store.SystemUpdate{ID: idv, RequestedBy: &actor.ID, RequestedByName: name, From: u.version, To: target, Channel: channel}
	err = u.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := u.s.st.CreateSystemUpdate(ctx, tx, run); err != nil {
			if errors.Is(err, store.ErrUpdateOpen) {
				return updateRefusal(UpdateAlreadyRunning, "an update is already being made")
			}
			return err
		}
		if err := u.s.auditChange(ctx, tx, actor, "system.update.requested", "system_update", run.ID, nil,
			map[string]any{"from": run.From, "to": run.To, "channel": run.Channel, "by": name}, ""); err != nil {
			return err
		}
		return u.tellOwners(ctx, tx, run, events.UpdateRequested, "", run.RequestedAt)
	})
	if err != nil {
		return nil, err
	}

	req, err := sysupdate.NewRequest(run.ID, run.Channel, run.To, name, run.RequestedAt)
	if err == nil {
		err = u.helper.WriteRequest(req)
	}
	if err != nil {
		u.log.Error("update: the request could not be handed to the updater", "id", run.ID, "err", err)
		// Recorded and announced: its end is too, so nobody waits for it.
		endCtx := context.WithoutCancel(ctx)
		if _, eerr := u.end(endCtx, run, events.UpdateFailed, "the request could not be handed to the server's updater"); eerr != nil {
			u.log.Error("update: the failed request was not recorded", "id", run.ID, "err", eerr)
		}
		return nil, updateRefusal(UpdateRequestFailed, "the request could not be handed to the server's updater (is its folder writable by core?)")
	}
	u.log.Info("update requested", "id", run.ID, "from", run.From, "to", run.To, "channel", run.Channel, "by", name)
	return run, nil
}

// requesterName is how the audit log, the notices and the request name the
// owner who asked: their dashboard name, else their Telegram id.
func requesterName(u *store.User) string {
	name := sysupdate.Clean(u.Username, sysupdate.MaxBy)
	if name == "" {
		name = "tg:" + strconv.FormatInt(u.TelegramID, 10)
	}
	return name
}

// tellOwners writes one SystemUpdate event for each owner who has a Telegram
// account, in tx, in the owner's own language. at is when it was asked or ended.
func (u *Updates) tellOwners(ctx context.Context, tx pgx.Tx, run *store.SystemUpdate, state, message string, at time.Time) error {
	staff, err := u.s.st.ListStaffContacts(ctx, tx, false)
	if err != nil {
		return err
	}
	for _, o := range staff {
		if o.Role != "owner" || o.TelegramID <= 0 {
			continue
		}
		e := events.SystemUpdateEvent{TelegramID: o.TelegramID, Language: i18n.Normalize(o.Language), State: state, From: run.From, To: run.To,
			Channel: run.Channel, By: run.RequestedByName, At: at.Unix(), Message: message}
		if err := u.s.publish(ctx, tx, events.SystemUpdate, e); err != nil {
			return err
		}
	}
	return nil
}

// end gives the open update its outcome, exactly once: the row, the audit
// entry (made by the system) and the notices to the owners are written in one
// transaction, and only by the caller that ends it first. It reports whether
// this call ended it.
func (u *Updates) end(ctx context.Context, run *store.SystemUpdate, outcome, message string) (bool, error) {
	message = sysupdate.Clean(message, sysupdate.MaxMessage)
	ended := false
	err := u.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		ended = false
		ok, err := u.s.st.EndSystemUpdate(ctx, tx, run.ID, outcome, message)
		if err != nil || !ok {
			return err
		}
		ended = true
		if err := u.s.auditChange(ctx, tx, nil, "system.update."+outcome, "system_update", run.ID, nil,
			map[string]any{"from": run.From, "to": run.To, "channel": run.Channel, "by": run.RequestedByName, "message": message}, ""); err != nil {
			return err
		}
		return u.tellOwners(ctx, tx, run, outcome, message, u.now())
	})
	if err != nil {
		return false, err
	}
	if ended {
		u.log.Info("update ended", "id", run.ID, "outcome", outcome, "from", run.From, "to", run.To)
	}
	return ended, nil
}

// Step looks once at the open update (if there is one) and ends it when it is
// over: the updater's status names its id and says it finished; or core runs
// the release it asked for (the status was lost, or the updater answers
// nothing, but the update plainly happened); or the updater never took the
// request up (so it is withdrawn); or the updater has said it is running for too
// long.
func (u *Updates) Step(ctx context.Context) error {
	run, err := u.s.st.OpenSystemUpdate(ctx, u.s.st.Conn())
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	h := u.Helper()
	if h.Available && h.Status.ID == run.ID && sysupdate.Finished(h.Status.State) {
		if _, err := u.end(ctx, run, h.Status.State, h.Status.Message); err != nil {
			return err
		}
		u.forget(run.ID)
		return nil
	}
	// This very version runs: the update is done, whatever the status says
	// (it is lost when the updater clears it, or cannot be read).
	if release.Valid(u.version) && run.To == u.version {
		if _, err := u.end(ctx, run, events.UpdateDone, "core now runs "+run.To); err != nil {
			return err
		}
		u.forget(run.ID)
		return nil
	}
	age := u.now().Sub(run.RequestedAt)
	switch {
	case h.Available && h.Status.ID == run.ID && h.Status.State == sysupdate.StateRunning:
		if started := h.Status.StartedAt; started != nil && u.now().Sub(*started) > u.StuckAfter {
			if _, err := u.end(ctx, run, events.UpdateFailed, "the updater has said it is running for over "+strconv.Itoa(int(u.StuckAfter.Minutes()))+" minutes: look at the server (sudo bobres status)"); err != nil {
				return err
			}
		}
	case age > u.PickupWait && !h.Running():
		// Nobody took it up: withdraw it, so it does not run at some later
		// moment nobody expects, and tell the owners.
		u.forget(run.ID)
		if _, err := u.end(ctx, run, events.UpdateNotStarted, "the server's updater did not take the request up"); err != nil {
			return err
		}
	}
	return nil
}

// forget removes the request file when it is still the one of this update
// (best effort: the updater may have removed it already).
func (u *Updates) forget(id string) {
	if u.helper == nil {
		return
	}
	if _, err := u.helper.RemoveRequest(id); err != nil {
		u.log.Warn("update: the request file was not removed", "id", id, "err", err)
	}
}

// newRequestID is the id of a request: a UUID, which the updater echoes in its
// status.
func newRequestID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("update request id: %w", err)
	}
	return id.String(), nil
}

// Run steps every u.Every until ctx ends.
func (u *Updates) Run(ctx context.Context) error {
	t := time.NewTicker(u.Every)
	defer t.Stop()
	for {
		if err := u.Step(ctx); err != nil && ctx.Err() == nil {
			if msg := err.Error(); msg != u.lastErr {
				u.log.Warn("update: could not look at the open update", "err", err)
				u.lastErr = msg
			}
		} else if err == nil {
			u.lastErr = ""
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
