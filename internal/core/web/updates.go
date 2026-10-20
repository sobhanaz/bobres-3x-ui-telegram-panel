package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
)

// System > Updates (docs/04-dashboard.md, docs/02-events-and-apis.md): the
// newest release the bot found, the channel updates follow, and "Update now",
// which has the server's updater (a root service on the host; core never
// touches Docker) run the same verified `bobres update` as the command line.
//
// Who may do what: owner and admin read the state (system.read), as they read
// the rest of the System page; checking again, choosing the channel and updating
// are the owner's (system.write), with the session's CSRF token on every change.
// Updating also needs the owner's explicit confirmation and, for an owner who has
// a password login with an authenticator app, a current code (the step-up that
// guards replacing the password). The updater's log lines are the owner's.

// Updates is the update service (domain.Updates) the pages ask; nil: these
// endpoints answer 503.
func (s *Server) updates(w http.ResponseWriter) *domain.Updates {
	if s.cfg.Updates == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "updating from the dashboard is not set up on this core")
		return nil
	}
	return s.cfg.Updates
}

// updateCheckJSON is the bot's check as the Updates tab reads it: what the bot
// said, whether the license covers the release (advisory), whether the release is a
// pre-release, and where its notes are (built by core from the version, never
// taken from the bot).
type updateCheckJSON struct {
	domain.UpdateCheck
	Prerelease bool   `json:"prerelease"`
	NotesURL   string `json:"notes_url,omitempty"`
}

// maxCheckDetail bounds the bot's own words about a failed check.
const maxCheckDetail = 300

// checkJSON shapes the bot's check; with none (the bot did not answer) it is
// {error: "bot unreachable"} with this install's version, as GET /system gives.
func (s *Server) checkJSON(ctx context.Context, u *domain.UpdateCheck, current string) updateCheckJSON {
	if u == nil {
		return updateCheckJSON{UpdateCheck: domain.UpdateCheck{Current: current, Error: "bot unreachable"}}
	}
	c := *u
	c.Detail = sysupdate.Clean(c.Detail, maxCheckDetail)
	c.Covered = s.licenseCovers(ctx, &c)
	out := updateCheckJSON{UpdateCheck: c}
	if release.ValidNumber(c.Latest) {
		out.Prerelease = release.Prerelease(c.Latest)
		out.NotesURL = release.NotesURL(c.Latest)
	}
	return out
}

// helperJSON is the updater as the page shows it: whether it can be used (and
// if not, why), what it says it is doing, and, for the owner, the last lines of
// its log. Every text in it is cleaned and cut by core (sysupdate) and shown
// as text by the page.
func helperJSON(h domain.UpdateHelperState, owner bool) map[string]any {
	st := h.Status
	out := map[string]any{
		"available": h.Available, "problem": h.Problem, "version": st.Helper, "state": st.State, "id": st.ID,
		"from": st.From, "to": st.To, "channel": st.Channel, "step": st.Step, "message": st.Message,
		"started_at": unix(st.StartedAt), "finished_at": unix(st.FinishedAt),
		// Added with the dashboard's restore: what the status is about (update | restore), whether this
		// updater can restore a backup, and what it says of a restore.
		"action": st.Action, "restore": st.Restore, "subject": st.Subject, "source": st.Source, "by": st.By, "requested_at": unix(st.RequestedAt),
	}
	if owner {
		log := st.Log
		if log == nil {
			log = []string{}
		}
		out["log"] = log
	}
	return out
}

// runJSON is an update core handed to the updater.
func runJSON(u *store.SystemUpdate, phase string) map[string]any {
	if u == nil {
		return nil
	}
	action := u.Action
	if action == "" {
		action = store.ActionUpdate
	}
	out := map[string]any{"id": u.ID, "by": u.RequestedByName, "from": u.From, "to": u.To, "channel": u.Channel,
		"requested_at": u.RequestedAt.Unix(), "outcome": u.Outcome, "outcome_at": unix(u.OutcomeAt), "message": u.Message,
		"action": action, "subject": u.Subject, "source": u.Source}
	if phase != "" {
		out["phase"] = phase
	}
	return out
}

// progressJSON is the light part of the state, which the page polls while an
// update restarts the install: the updater's status and core's records, read
// from the folder and the database only (nobody else is asked, so it answers
// at once even while the bot is restarting).
func (s *Server) progressJSON(up *domain.Updates, p *domain.UpdateProgress, owner bool) map[string]any {
	return map[string]any{"version": up.Version(), "helper": helperJSON(p.Helper, owner), "run": runJSON(p.Run, p.Phase), "last": runJSON(p.Last, "")}
}

// updatesView is everything the Updates tab shows. check is what to show of the
// bot's look (the cached one is read when nil and fresh is false).
func (s *Server) updatesView(ctx context.Context, w http.ResponseWriter, r *http.Request, up *domain.Updates, check *domain.UpdateCheck, haveCheck bool) (map[string]any, bool) {
	role := actor(r).Role
	owner := Allowed(role, PermSystemWrite)
	p, err := up.Progress(ctx)
	if err != nil {
		s.fail(w, "update progress", err)
		return nil, false
	}
	if !haveCheck {
		c, cerr := up.Check(ctx)
		if cerr != nil {
			s.cfg.Log.Info("updates: the bot's check was not read", "err", cerr)
			c = nil
		}
		check = c
	}
	setting, err := up.Channel(ctx)
	if err != nil {
		s.fail(w, "update channel", err)
		return nil, false
	}
	out := s.progressJSON(up, p, owner)
	cj := s.checkJSON(ctx, check, up.Version())
	out["check"] = cj
	// The channel: what the owner chose here ("" none: the server's
	// BOBRES_UPDATE_CHANNEL), and the channel the bot follows now.
	effective := ""
	if c, perr := release.ParseChannel(cj.Channel); perr == nil && strings.TrimSpace(cj.Channel) != "" {
		effective = c
	}
	source := "server"
	if setting != "" {
		source = "setting"
	}
	out["channel"] = map[string]any{"setting": setting, "effective": effective, "source": source}
	// Would an update be accepted now: the page enables its button by this; the
	// request is checked again when it is made.
	reason := up.CanApply(check, p)
	target := ""
	if t, refusal := up.Target(check); refusal == nil {
		target = t
	}
	out["apply"] = map[string]any{"allowed": owner, "ready": owner && reason == "", "reason": reason, "target": target}
	return out, true
}

// getUpdates: GET /api/v1/system/updates (owner and admin), the Updates tab's
// state:
//
//		{version, check, channel, helper, run, last, apply}
//
//	  - version: the version core runs;
//	  - check: the bot's last look for a release, {current, latest, channel,
//	    issued, newer, verified, checked_at, error, detail, covered?, prerelease,
//	    notes_url}; error "bot unreachable" when the bot does not answer;
//	  - channel: {setting ("" none), effective (the channel the bot follows), source
//	    (setting | server: where it comes from)};
//	  - helper: the server's updater, {available, problem (no_dir | no_status |
//	    not_installed | bad_status | unsupported), version, state, id, from, to,
//	    channel, step, message, started_at, finished_at, log (owner only)};
//	  - run: the update core handed the updater and has not ended, {id, by,
//	    from, to, channel, requested_at, phase (queued | running | done | failed |
//	    rolled_back)}, or null; last: the update that ended last, with outcome,
//	    outcome_at and message, or null;
//	  - apply: {allowed (the role may), ready (an update would be accepted now),
//	    reason (why not: helper_missing | already_running | check_unavailable |
//	    no_release | not_newer | dev_build), target (the release it would install)}.
func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	if v, ok := s.updatesView(r.Context(), w, r, up, nil, false); ok {
		writeJSON(w, http.StatusOK, v)
	}
}

// updateProgress: GET /api/v1/system/updates/progress (owner and admin), the
// light part of the state: {version, helper, run, last} as the full state gives
// them, read from the updater's folder and the database only. The page polls it
// while an update runs, and while core restarts in the middle of it.
func (s *Server) updateProgress(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	p, err := up.Progress(r.Context())
	if err != nil {
		s.fail(w, "update progress", err)
		return
	}
	writeJSON(w, http.StatusOK, s.progressJSON(up, p, Allowed(actor(r).Role, PermSystemWrite)))
}

// checkNowTimeout keeps "Check again" inside core's 30-second write limit.
const checkNowTimeout = 24 * time.Second

// checkUpdatesNow: POST /api/v1/system/updates/check (owner), "Check again":
// the bot reloads its settings, looks at GitHub now, verifies what it finds and
// answers; the state is returned as GET gives it, with the fresh check (a look
// that failed says why in check.error and check.detail; a bot that does not
// answer is error "bot unreachable"). At most 10 a minute.
func (s *Server) checkUpdatesNow(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	if !s.probes.allow("updates-check:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 10 checks a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), checkNowTimeout)
	defer cancel()
	c, err := up.CheckNow(ctx)
	if err != nil {
		s.cfg.Log.Info("updates: the bot did not look again", "err", err)
		c = nil
	}
	if v, ok := s.updatesView(r.Context(), w, r, up, c, true); ok {
		writeJSON(w, http.StatusOK, v)
	}
}

// putUpdateChannel: PUT /api/v1/system/updates/channel {channel} (owner) chooses
// the channel updates follow: "stable", "beta" (the newest of all releases,
// pre-releases included), or "" to follow the server's BOBRES_UPDATE_CHANNEL
// again. It is a setting like the others (audited as setting.set). A change
// makes the bot look again at once, and the state is returned with that look.
func (s *Server) putUpdateChannel(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	var in struct {
		Channel string `json:"channel"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	changed, err := up.SetChannel(r.Context(), actor(r), in.Channel)
	if err != nil {
		s.fail(w, "set the update channel", err)
		return
	}
	var (
		check *domain.UpdateCheck
		have  bool
	)
	if changed {
		ctx, cancel := context.WithTimeout(r.Context(), checkNowTimeout)
		defer cancel()
		c, cerr := up.CheckNow(ctx)
		if cerr != nil {
			// Saved all the same: the bot reloads its settings within a minute
			// and looks again by itself.
			s.cfg.Log.Info("updates: the bot did not look again after the channel changed", "err", cerr)
			c = nil
		}
		check, have = c, true
	}
	if v, ok := s.updatesView(r.Context(), w, r, up, check, have); ok {
		writeJSON(w, http.StatusOK, v)
	}
}

// applyUpdate: POST /api/v1/system/updates/apply {version, channel, confirm,
// accept_prerelease, code} (owner) updates the install to the release the owner
// confirmed. Nothing is done unless
//   - confirm is true, and version is the release the page showed (the newest the
//     bot has verified: a release that appeared since was never confirmed);
//   - channel (when given) is the channel the bot follows now, and a pre-release
//     comes with accept_prerelease: true;
//   - an owner with a password login and an authenticator app gives a current
//     code (403 code_required, 400 code_wrong), as for replacing the password;
//   - the server's updater is installed and idle, and no update of ours is open.
//
// It answers 202 with the state as GET .../progress gives it (the update is
// queued), and every refusal as {error, code, message}: 400 invalid
// (confirm_required, prerelease_unconfirmed), 409 state (helper_missing,
// already_running, no_release, not_newer, version_changed with latest,
// channel_changed, dev_build) and 503 unavailable (check_unavailable,
// request_failed). The request, its audit entry (system.update.requested) and a
// Telegram notice to every owner are recorded first. At most 10 a minute.
func (s *Server) applyUpdate(w http.ResponseWriter, r *http.Request) {
	up := s.updates(w)
	if up == nil {
		return
	}
	var in struct {
		Version          string `json:"version"`
		Channel          string `json:"channel"`
		Confirm          bool   `json:"confirm"`
		AcceptPrerelease bool   `json:"accept_prerelease"`
		Code             string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !in.Confirm {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "code": "confirm_required",
			"message": "confirm that you want to update: the dashboard and the bot are unavailable for a few minutes"})
		return
	}
	if !s.probes.allow("updates-apply:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 10 update requests a minute")
		return
	}
	if !s.reauth(w, r, in.Code) {
		return
	}
	// Once asked, the request is recorded and handed over even if the page goes away.
	ctx := context.WithoutCancel(r.Context())
	run, err := up.Apply(ctx, actor(r), domain.ApplyUpdate{Version: strings.TrimSpace(in.Version), Channel: in.Channel, AcceptPrerelease: in.AcceptPrerelease})
	if err != nil {
		s.updateFail(w, err)
		return
	}
	p, perr := up.Progress(ctx)
	if perr != nil {
		// Accepted all the same: the page reads the progress next.
		s.cfg.Log.Warn("updates: progress after the request", "id", run.ID, "err", perr)
		writeJSON(w, http.StatusAccepted, map[string]any{"version": up.Version(), "run": runJSON(run, domain.PhaseQueued)})
		return
	}
	writeJSON(w, http.StatusAccepted, s.progressJSON(up, p, true))
}

// updateFail answers an update that was refused, with the code the page
// translates (domain.UpdateError).
func (s *Server) updateFail(w http.ResponseWriter, err error) {
	var ue *domain.UpdateError
	if !errors.As(err, &ue) {
		s.fail(w, "update", err)
		return
	}
	status, kind := http.StatusConflict, "state"
	switch ue.Code {
	case domain.UpdatePrereleaseUnconfirmed:
		status, kind = http.StatusBadRequest, "invalid"
	case domain.UpdateCheckUnavailable, domain.UpdateRequestFailed:
		status, kind = http.StatusServiceUnavailable, "unavailable"
	}
	body := map[string]any{"error": kind, "code": ue.Code, "message": ue.Message}
	if ue.Latest != "" {
		body["latest"] = ue.Latest
	}
	writeJSON(w, status, body)
}
