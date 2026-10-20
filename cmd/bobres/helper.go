package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

const fromRequestUsage = "usage: bobres update --from-request [--dir D]   (bobres-updater.service runs it for the dashboard; it is not for people)"

// helperRequestCmd is `bobres update --from-request`: the entry bobres-updater.service runs when the
// dashboard dropped a request. It reads the one request in <dir>/run/update (helper_request.go),
// deletes it, and runs exactly what `bobres update --yes --channel C [--version V]` runs, with the
// progress reported to <dir>/run/update/status.json (helper_status.go). Exit 0 when the update is
// done (or there was nothing to update), 1 when it was refused, failed or rolled back, 2 for usage
// errors. The request carries nothing but a channel and a version; whatever else is in the file is
// a reason to refuse it.
func helperRequestCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("update --from-request", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errOut, fromRequestUsage)
		return 2
	}
	// The service's output is the journal's: plain lines, no banner.
	defer func(saved uiOptions) { uiOpts = saved }(uiOpts)
	uiOpts.plain, uiOpts.noBanner = true, true
	env, _, _ := readEnv(filepath.Join(*dir, ".env"))
	st := newStatusFile(statusPath(*dir), newRedactor(env).line)
	outTee, errTee := &logTee{w: out, st: st}, &logTee{w: errOut, st: st}
	o := newOps(in, outTee, errTee)
	stop := st.startFlusher(time.Second)
	defer stop()
	h := &helperRun{
		u: newUpdater(o), dir: *dir, coreUID: coreUID, now: o.clock, st: st, helper: helperVersion(),
		flush: func() { outTee.flush(); errTee.flush() },
	}
	return h.exec()
}

// helperRun is one run of the helper.
type helperRun struct {
	u       *updater
	dir     string
	coreUID int
	now     func() time.Time
	st      *statusFile
	// helper is the version of this bobres, which the status calls the helper.
	helper string
	// flush hands the status the last unfinished line of the output.
	flush func()
}

// exec takes the install lock, consumes the request and runs it. Whatever the request was, it is
// gone when this returns, and status.json says how it ended.
func (h *helperRun) exec() (code int) {
	u := h.u
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(u.errOut, "error: bobres update --from-request stopped on an internal error: %v\n%s\n", r, debug.Stack())
			h.fail(h.st.current().ID, "The update helper stopped on an internal error. The store may be half updated: sudo bobres status, and journalctl -u bobres-updater.service.", stepFinished)
			code = 1
		}
	}()
	env, ok := u.requireInstall(h.dir)
	if !ok {
		return 2
	}
	if err := checkUpdateDir(h.dir, h.coreUID); err != nil {
		// Nothing of that directory is trusted, so nothing is read from it or written to it.
		fmt.Fprintf(u.errOut, "error: %v: not looking at update requests until that is fixed (sudo bobres install puts the directory right).\n", err)
		return 1
	}
	unlock, lockErr := u.lock(h.dir)
	busy := errors.Is(lockErr, errInstallBusy)
	if lockErr == nil {
		defer unlock()
	}
	raw, rerr := takeRequest(updateDirPath(h.dir), h.coreUID)
	if errors.Is(rerr, errNoRequest) {
		fmt.Fprintln(u.out, "There is no update request: nothing to do.")
		return 0
	}
	id := ""
	var req *updateRequest
	if rerr == nil {
		id = requestIDHint(raw)
		req, rerr = parseUpdateRequest(raw, h.now())
	}
	switch {
	case lockErr != nil && !busy:
		return h.reject(id, fmt.Sprintf("The update could not start: %v.", lockErr), false)
	case busy:
		return h.reject(id, "Another update, a rollback or a restore is running on this server: try again when it has finished.", true)
	case rerr != nil:
		return h.reject(id, "The update request was refused: "+rerr.Error()+".", false)
	case dashboardUpdatesOff(env):
		return h.reject(id, "Updating from the dashboard is switched off on this server ("+dashboardUpdatesKey+"=off).", false)
	}
	again, err := markHandled(h.dir, req.ID, h.now())
	switch {
	case err != nil:
		return h.reject(id, "The request could not be recorded, so it was not run: "+firstLine(err.Error())+".", false)
	case again:
		fmt.Fprintf(u.errOut, "The request %s was handled before: ignored.\n", req.ID)
		return 0
	}
	return h.run(req, env)
}

// reject ends a request that did not run: status.json says why, under the request's id when the
// file showed one. keepRunning leaves the status alone when it says an update is running (this
// request came while another one is: that one's report is not this one's to replace).
func (h *helperRun) reject(id, message string, keepRunning bool) int {
	fmt.Fprintf(h.u.errOut, "error: %s\n", message)
	if keepRunning {
		if st, ok := readHelperStatus(h.st.path); ok && st.State == helperRunning {
			return 1
		}
	}
	h.fail(id, message, stepVerifying)
	return 1
}

// fail writes a final "failed" status.
func (h *helperRun) fail(id, message, step string) {
	now := h.now()
	h.st.update(func(s *helperStatus) {
		*s = helperStatus{V: requestProtocol, Installed: true, Helper: h.helper, State: helperFailed, ID: id, Step: step,
			Message: clipSentence(message), StartedAt: stamp(now), FinishedAt: stamp(now)}
	})
}

// run is the update itself, as `bobres update --yes --channel C [--version V]` makes it.
func (h *helperRun) run(req *updateRequest, env map[string]string) int {
	u := h.u
	from := u.installed(env)
	to := req.Version
	fmt.Fprintf(u.out, "Update requested by %s from the dashboard (request %s): channel %s, release %s.\n",
		req.By, req.ID, req.Channel, orDefault(req.Version, "the newest"))
	started := h.now()
	h.st.update(func(s *helperStatus) {
		*s = helperStatus{V: requestProtocol, Installed: true, Helper: h.helper, State: helperRunning, ID: req.ID, From: from, To: to,
			Channel: req.Channel, Step: stepVerifying, Message: "Checking the release signature.", StartedAt: stamp(started)}
	})
	u.event, u.locked, u.outcome = h.event, true, updateOutcome{}
	args := []string{"--dir", h.dir, "--yes", "--channel", req.Channel}
	if req.Version != "" {
		args = append(args, "--version", req.Version)
	}
	code := u.updateCmd(args)
	u.event = nil
	return h.conclude(code, req, from)
}

// event moves the status along as the update does.
func (h *helperRun) event(ev updateEvent) {
	h.st.update(func(s *helperStatus) {
		if ev.Step != "" {
			s.Step = ev.Step
		}
		if ev.Message != "" {
			s.Message = ev.Message
		}
		if ev.To != "" {
			s.To = ev.To
		}
	})
}

// conclude writes the final status of an update that ended with code and returns the exit code.
func (h *helperRun) conclude(code int, req *updateRequest, from string) int {
	o := h.u.outcome
	state, step, message, helper := helperFailed, "", "", h.helper
	switch {
	case code == 0:
		state, step = helperDone, stepFinished
		switch {
		case o.applied:
			message = fmt.Sprintf("BOBRES was updated from %s to %s.", from, o.to)
			helper = o.to // the bobres on disk is the new release's now
		case o.nothing != "":
			message = o.nothing
		default:
			message = "The update finished."
		}
		message += h.saveChannel(req.Channel)
	case o.rollback == rolledBack:
		state, step = helperRolledBack, stepRollingBk
		message = fmt.Sprintf("The update to %s did not go through: BOBRES runs %s again.", orDefault(h.st.current().To, "the new release"), from)
	case o.rollback == rollbackIncomplete:
		step = stepRollingBk
		message = fmt.Sprintf("The update to %s failed and the automatic rollback did not finish: connect to the server (docs/runbooks/update-rollback.md).",
			orDefault(h.st.current().To, "the new release"))
	default:
		step = h.st.current().Step
		message = h.st.lastError()
	}
	if h.flush != nil {
		h.flush()
	}
	finished := h.now()
	h.st.update(func(s *helperStatus) {
		s.State, s.Step, s.Message, s.Helper, s.FinishedAt = state, step, clipSentence(message), helper, stamp(finished)
	})
	return code
}

// saveChannel keeps BOBRES_UPDATE_CHANNEL in .env in step with the channel the dashboard asked for
// (so sudo bobres update and the menu follow what the owner picked), with `bobres config set`,
// which recreates the bot that checks for releases. It returns what to add to the message.
func (h *helperRun) saveChannel(channel string) string {
	env, _, err := readEnv(filepath.Join(h.dir, ".env"))
	if err != nil {
		return ""
	}
	if cur, perr := release.ParseChannel(env["BOBRES_UPDATE_CHANNEL"]); perr == nil && cur == channel {
		return ""
	}
	h.event(updateEvent{Step: stepWaiting, Message: "Saving the update channel."})
	if h.u.configSet(h.dir, "BOBRES_UPDATE_CHANNEL", channel, false) != 0 {
		// The setting is written before the bot is recreated: say which of the two failed.
		if env, _, err := readEnv(filepath.Join(h.dir, ".env")); err == nil && env["BOBRES_UPDATE_CHANNEL"] == channel {
			return " The update channel is " + channel + ", but the bot did not restart cleanly (sudo bobres status)."
		}
		return " The update channel could not be saved: sudo bobres config set BOBRES_UPDATE_CHANNEL " + channel
	}
	return " The update channel is now " + channel + "."
}

// lastError is the last "error: ..." line the update printed, the reason it failed.
func (s *statusFile) lastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.lines) - 1; i >= 0; i-- {
		if msg, ok := strings.CutPrefix(s.lines[i], "error: "); ok {
			return msg
		}
	}
	return "The update did not finish; the log below says why."
}

// clipSentence keeps a message to one short line.
func clipSentence(m string) string {
	m = firstLine(m)
	if r := []rune(m); len(r) > 240 {
		m = string(r[:237]) + "..."
	}
	return m
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
