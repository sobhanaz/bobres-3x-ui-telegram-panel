package collect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// WatchedUnits are the systemd units the snapshot reports (when installed):
// BOBRES's own helpers and Docker. A fixed list: nothing else of the host's
// services is looked at.
var WatchedUnits = []string{
	"bobres-monitor.timer", "bobres-monitor.service",
	"bobres-update.path", "bobres-update.service",
	"bobres-backup.timer", "bobres-backup.service",
	"docker.service",
}

// journalSources are the units whose error-level journal lines are counted:
// the engine and its runtime, and BOBRES's own units. The gateway (Caddy) is a
// container and is read through the engine instead.
var journalSources = []struct {
	name  string
	units []string
}{
	{"docker", []string{"docker.service", "containerd.service"}},
	{"bobres", []string{"bobres-monitor.service", "bobres-monitor.timer", "bobres-update.service", "bobres-update.path",
		"bobres-backup.service", "bobres-backup.timer"}},
}

// journalCap is how many lines one source's query reads at most.
const journalCap = 200

const (
	unitsTimeout   = 2 * time.Second
	journalTimeout = 3 * time.Second
)

// collectUnits asks systemd about WatchedUnits in one call. Units that are not
// installed are left out. nil with the reason when systemctl is not there or
// does not answer.
func collectUnits(ctx context.Context, run Runner, uptime time.Duration, now time.Time) ([]snapshot.Unit, string) {
	ctx, cancel := context.WithTimeout(ctx, unitsTimeout)
	defer cancel()
	args := append([]string{"show", "--no-pager", "-p", "Id,LoadState,ActiveState,SubState,UnitFileState,Result,NRestarts,ActiveEnterTimestampMonotonic"},
		WatchedUnits...)
	out, err := run(ctx, "systemctl", args...)
	if err != nil && len(out) == 0 {
		return nil, "systemd: not available"
	}
	return parseUnits(out, uptime, now), ""
}

// parseUnits reads `systemctl show` for several units: blocks of Key=Value
// lines separated by an empty line. The time a unit became active comes as
// microseconds since boot; it is turned into a wall-clock time with the uptime.
func parseUnits(out []byte, uptime time.Duration, now time.Time) []snapshot.Unit {
	var units []snapshot.Unit
	cur := map[string]string{}
	flush := func() {
		defer func() { cur = map[string]string{} }()
		name := cur["Id"]
		if name == "" || cur["LoadState"] != "loaded" {
			return
		}
		u := snapshot.Unit{Name: name, Load: cur["LoadState"], Active: cur["ActiveState"], Sub: cur["SubState"],
			Enabled: cur["UnitFileState"], Result: cur["Result"]}
		u.Restarts, _ = strconv.Atoi(cur["NRestarts"])
		if us, err := strconv.ParseInt(cur["ActiveEnterTimestampMonotonic"], 10, 64); err == nil && us > 0 && uptime > 0 {
			if since := now.Add(-uptime + time.Duration(us)*time.Microsecond); since.Before(now) {
				u.Since = since.Unix()
			}
		}
		units = append(units, u)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[k] = v
		}
	}
	flush()
	return units
}

// collectJournal counts the error-level lines of the last window for each
// journal source and keeps the newest few, scrubbed. nil when journalctl is not
// there.
func collectJournal(ctx context.Context, run Runner, window time.Duration) *snapshot.Journal {
	j := &snapshot.Journal{WindowS: int(window.Seconds())}
	type one struct {
		src snapshot.JournalSource
		ok  bool
	}
	results := make([]one, len(journalSources))
	done := make(chan int)
	for i, s := range journalSources {
		go func() {
			ctx, cancel := context.WithTimeout(ctx, journalTimeout)
			defer cancel()
			args := []string{"--no-pager", "-q", "-o", "json", "-p", "err", "--since", "-" + strconv.Itoa(int(window.Seconds())) + "s", "-n", strconv.Itoa(journalCap)}
			for _, u := range s.units {
				args = append(args, "-u", u)
			}
			out, err := run(ctx, "journalctl", args...)
			if err == nil || len(out) > 0 {
				count, lines := parseJournal(out)
				results[i] = one{snapshot.JournalSource{Name: s.name, Errors: count, Capped: count >= journalCap, Lines: lines}, true}
			}
			done <- i
		}()
	}
	for range journalSources {
		<-done
	}
	for _, r := range results {
		if r.ok {
			j.Sources = append(j.Sources, r.src)
		}
	}
	if len(j.Sources) == 0 {
		return nil
	}
	return j
}

// parseJournal reads `journalctl -o json`: one JSON object per line. It counts
// the lines and returns the last snapshot.MaxJournalLines, scrubbed. A line
// that is not JSON is skipped; MESSAGE is a string, or a list of byte values
// when the message was not text.
func parseJournal(out []byte) (int, []snapshot.JournalLine) {
	count := 0
	var lines []snapshot.JournalLine
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var e struct {
			Message json.RawMessage `json:"MESSAGE"`
			At      string          `json:"__REALTIME_TIMESTAMP"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || len(e.Message) == 0 {
			continue
		}
		count++
		var at int64
		if us, err := strconv.ParseInt(e.At, 10, 64); err == nil {
			at = us / 1_000_000
		}
		lines = append(lines, snapshot.JournalLine{At: at, Msg: Scrub(messageText(e.Message))})
		if len(lines) > snapshot.MaxJournalLines {
			lines = lines[1:]
		}
	}
	return count, lines
}

// messageText turns a journal MESSAGE into text.
func messageText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []int
	if json.Unmarshal(raw, &bs) == nil {
		b := make([]byte, 0, len(bs))
		for _, v := range bs {
			if v >= 0 && v <= 255 {
				b = append(b, byte(v))
			}
		}
		return string(b)
	}
	return ""
}

// collectClock asks whether the clock is kept by NTP (timedatectl).
func collectClock(ctx context.Context, run Runner) *snapshot.Clock {
	ctx, cancel := context.WithTimeout(ctx, unitsTimeout)
	defer cancel()
	out, err := run(ctx, "timedatectl", "show", "-p", "NTP", "-p", "NTPSynchronized")
	if err != nil && len(out) == 0 {
		return nil
	}
	c := &snapshot.Clock{}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		b := v == "yes"
		if v != "yes" && v != "no" {
			continue
		}
		switch k {
		case "NTP":
			c.NTP = &b
		case "NTPSynchronized":
			c.Synchronized = &b
		}
	}
	if c.NTP == nil && c.Synchronized == nil {
		return nil
	}
	return c
}
