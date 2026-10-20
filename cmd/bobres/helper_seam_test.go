package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The two halves of the dashboard's update protocol were written apart: core's
// (internal/sysupdate) writes request.json and reads status.json, the helper's
// (this package) reads the request as root and writes the status. These tests are
// where they meet: what one side writes is read by the other, byte for byte, with
// the checks of both sides in place.

// coreRequest writes a request the way core does, into dir, and reads it back the
// way the helper does (as the user of this test, which stands for core's uid).
func coreRequest(t *testing.T, dir, id, channel, version, by string, at time.Time) (*updateRequest, error) {
	t.Helper()
	req, err := sysupdate.NewRequest(id, channel, version, by, at)
	if err != nil {
		t.Fatalf("core cannot make the request: %v", err)
	}
	if err := sysupdate.NewDir(dir).WriteRequest(req); err != nil {
		t.Fatalf("core cannot write the request: %v", err)
	}
	raw, err := readRequestFile(dir, os.Geteuid())
	if err != nil {
		t.Fatalf("the helper cannot read the file core wrote: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, requestFileName)); err != nil {
		t.Fatal(err)
	}
	return parseUpdateRequest(raw, at.Add(time.Second))
}

func TestSeamEveryRequestCoreWritesIsAccepted(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct{ name, channel, version, by string }{
		{"stable release", "stable", "0.7.0", "sobhan"},
		{"beta pre-release", "beta", "0.7.1-rc.1", "tg:7102607089"},
		{"label with dashes", "beta", "1.2.3-beta-2.12", "owner"},
		{"zero parts", "stable", "0.0.1", "o"},
		{"large numbers", "stable", "10.20.30", "owner"},
		{"persian name", "stable", "0.7.0", "سبحان آذرزاده"},
		{"name at the limit", "stable", "0.7.0", strings.Repeat("ب", maxRequestBy)},
		{"name with spaces", "stable", "0.7.0", "  two   words  "},
		{"name with controls and bidi", "stable", "0.7.0", "a\u202eb\nc\x00d\u2066e"},
		{"name too long", "stable", "0.7.0", strings.Repeat("x", 300)},
		{"no name", "stable", "0.7.0", ""},
		{"newest of the channel", "stable", "", "owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			id, err := uuid.NewV7()
			if err != nil {
				t.Fatal(err)
			}
			got, err := coreRequest(t, dir, id.String(), tc.channel, tc.version, tc.by, now)
			if err != nil {
				t.Fatalf("the helper refuses a request core makes: %v", err)
			}
			if got.ID != id.String() || got.Channel != tc.channel || got.Version != tc.version {
				t.Fatalf("the request changed on the way: %+v", got)
			}
			if !got.At.Equal(now) {
				t.Fatalf("the time changed on the way: %v, not %v", got.At, now)
			}
			if got.By == "" || strings.ContainsAny(got.By, "\n\x00\u202e\u2066") {
				t.Fatalf("the name is not a plain line: %q", got.By)
			}
		})
	}
}

func TestSeamCoreWritesRequestsOfEveryUUIDVersionItMakes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 50; i++ {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := coreRequest(t, t.TempDir(), id.String(), "stable", "0.7.0", "owner", now); err != nil {
			t.Fatalf("the helper refuses the id %s: %v", id, err)
		}
	}
}

func TestSeamRequestFileCoreLeavesIsWhatTheHelperChecks(t *testing.T) {
	dir := t.TempDir()
	id, _ := uuid.NewV7()
	now := time.Now().UTC().Truncate(time.Second)
	req, err := sysupdate.NewRequest(id.String(), "stable", "0.7.0", "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := sysupdate.NewDir(dir).WriteRequest(req); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(dir, requestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o640 {
		t.Fatalf("the request is %v, not a regular 0640 file", fi.Mode())
	}
	if fi.Size() > maxRequestBytes {
		t.Fatalf("the request is %d bytes, over the helper's %d", fi.Size(), maxRequestBytes)
	}
	// Nothing else of core's stays in the folder next to it.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("core left %d entries, not just the request", len(entries))
	}
}

func TestSeamAStaleRequestFromCoreIsRefusedByTheHelper(t *testing.T) {
	// Core stamps the time of the request in UTC; the helper refuses one older than its window.
	id, _ := uuid.NewV7()
	then := time.Now().UTC().Add(-requestMaxAge - time.Minute).Truncate(time.Second)
	dir := t.TempDir()
	req, err := sysupdate.NewRequest(id.String(), "stable", "0.7.0", "owner", then)
	if err != nil {
		t.Fatal(err)
	}
	if err := sysupdate.NewDir(dir).WriteRequest(req); err != nil {
		t.Fatal(err)
	}
	raw, err := readRequestFile(dir, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseUpdateRequest(raw, time.Now()); err == nil {
		t.Fatal("a request older than the window was accepted")
	}
}

// helperStatusFor writes st the way the helper does and reads it the way core does.
func helperStatusFor(t *testing.T, st helperStatus) (sysupdate.Status, error) {
	t.Helper()
	dir := t.TempDir()
	if err := writeHelperStatus(filepath.Join(dir, statusFileName), &st); err != nil {
		t.Fatalf("the helper cannot write its status: %v", err)
	}
	return sysupdate.NewDir(dir).ReadStatus()
}

func TestSeamEveryStatusTheHelperWritesIsUnderstoodByCore(t *testing.T) {
	id, _ := uuid.NewV7()
	started := time.Now().UTC().Add(-3 * time.Minute).Truncate(time.Second)
	finished := started.Add(2 * time.Minute)
	steps := []string{stepVerifying, stepBackingUp, stepPulling, stepRestarting, stepWaiting, stepRollingBk, stepFinished}
	states := []string{helperRunning, helperDone, helperFailed, helperRolledBack}
	for _, state := range states {
		for _, step := range steps {
			st := helperStatus{V: requestProtocol, Installed: true, Helper: "0.7.0", State: state, ID: id.String(), From: "0.6.1", To: "0.7.0",
				Channel: "beta", Step: step, Message: "Installing the new release: pulling its images.", StartedAt: stamp(started),
				Log: []string{"Checking the release signature...", "Downloading bobres 0.7.0..."}}
			if state != helperRunning {
				st.FinishedAt = stamp(finished)
			}
			got, err := helperStatusFor(t, st)
			if err != nil {
				t.Fatalf("core cannot read the %s status at step %s: %v", state, step, err)
			}
			if got.State != state || got.Step != step || got.ID != id.String() || got.From != "0.6.1" || got.To != "0.7.0" ||
				got.Channel != "beta" || got.Helper != "0.7.0" || !got.Installed {
				t.Fatalf("core reads %+v, not what the helper wrote (%s, %s)", got, state, step)
			}
			if got.StartedAt == nil || !got.StartedAt.Equal(started) {
				t.Fatalf("started_at changed on the way: %v", got.StartedAt)
			}
			if (state != helperRunning) != (got.FinishedAt != nil) {
				t.Fatalf("finished_at is %v in the %s state", got.FinishedAt, state)
			}
			if len(got.Log) != 2 || got.Message == "" {
				t.Fatalf("core lost the message or the log: %q %q", got.Message, got.Log)
			}
		}
	}
}

func TestSeamTheIdleAndSwitchedOffStatuses(t *testing.T) {
	got, err := helperStatusFor(t, idleStatus(true, "0.7.0"))
	if err != nil {
		t.Fatalf("core cannot read the idle status: %v", err)
	}
	if got.State != sysupdate.StateIdle || got.ID != "" || got.StartedAt != nil || got.FinishedAt != nil || len(got.Log) != 0 || !got.Installed {
		t.Fatalf("the idle status was read as %+v", got)
	}
	// An owner who switched the helper off: core must know it is not installed.
	got, err = helperStatusFor(t, idleStatus(false, "0.7.0"))
	if err == nil || got.Installed {
		t.Fatalf("a helper that is switched off was taken for installed: %+v, %v", got, err)
	}
}

func TestSeamAnIdLessFailureStaysAnswerable(t *testing.T) {
	// A request the helper could not read gets a failed status with no id; core must read it and not match it to a request.
	got, err := helperStatusFor(t, helperStatus{V: requestProtocol, Installed: true, Helper: "0.7.0", State: helperFailed, Step: stepVerifying,
		Message: "The update request was refused: it is larger than 2048 bytes.", StartedAt: stamp(time.Now()), FinishedAt: stamp(time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != sysupdate.StateFailed || got.ID != "" {
		t.Fatalf("read as %+v", got)
	}
}
