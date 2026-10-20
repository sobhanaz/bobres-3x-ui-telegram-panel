package collect

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

func TestUnitsFromSystemctl(t *testing.T) {
	uptime := 58698 * time.Second
	units := parseUnits(fixture(t, "system/systemctl-show.txt"), uptime, now0)
	var names []string
	for _, u := range units {
		names = append(names, u.Name)
	}
	// The units that are not installed are left out.
	if got := strings.Join(names, " "); got != "bobres-monitor.timer bobres-monitor.service docker.service" {
		t.Fatalf("units: %s", got)
	}
	timer := units[0]
	if timer.Active != "active" || timer.Sub != "waiting" || timer.Enabled != "enabled" || timer.Load != "loaded" || timer.Result != "success" {
		t.Errorf("timer: %+v", timer)
	}
	// It became active 9899.87 seconds after boot, which was 58698 seconds ago.
	if want := now0.Add(-uptime + 9899870*time.Millisecond).Unix(); timer.Since != want {
		t.Errorf("since %d, want %d", timer.Since, want)
	}
	if units[1].Active != "inactive" || units[1].Enabled != "static" {
		t.Errorf("service: %+v", units[1])
	}
	// Without an uptime the time is not guessed.
	if got := parseUnits(fixture(t, "system/systemctl-show.txt"), 0, now0); got[0].Since != 0 {
		t.Errorf("since without uptime: %d", got[0].Since)
	}
	if got := parseUnits([]byte("garbage without equals\n\n\n"), uptime, now0); len(got) != 0 {
		t.Errorf("garbage: %+v", got)
	}
}

func TestCollectUnitsAsksOnceForTheFixedList(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return fixture(t, "system/systemctl-show.txt"), nil
	}
	units, why := collectUnits(context.Background(), run, time.Hour, now0)
	if why != "" || len(units) != 3 || len(calls) != 1 {
		t.Fatalf("%v %q %d", units, why, len(calls))
	}
	line := strings.Join(calls[0], " ")
	for _, u := range WatchedUnits {
		if !strings.Contains(line, u) {
			t.Errorf("%s was not asked about", u)
		}
	}
	if !strings.HasPrefix(line, "systemctl show --no-pager -p ") {
		t.Errorf("command: %s", line)
	}
	failing := func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("not found") }
	if units, why := collectUnits(context.Background(), failing, time.Hour, now0); units != nil || why != "systemd: not available" {
		t.Errorf("no systemd: %v %q", units, why)
	}
}

func TestJournalCountsAndScrubs(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		asked = append(asked, name+" "+strings.Join(args, " "))
		mu.Unlock()
		if strings.Contains(strings.Join(args, " "), "docker.service") {
			return fixture(t, "system/journal.jsonl"), nil
		}
		return nil, nil // BOBRES's own units have no errors
	}
	j := collectJournal(context.Background(), run, time.Hour)
	if j == nil || j.WindowS != 3600 || len(j.Sources) != 2 {
		t.Fatalf("%+v", j)
	}
	docker, bobres := j.Sources[0], j.Sources[1]
	if docker.Name != "docker" || bobres.Name != "bobres" {
		t.Fatalf("order: %s %s", docker.Name, bobres.Name)
	}
	// Six lines have a message; the last five are kept.
	if docker.Errors != 6 || docker.Capped || len(docker.Lines) != snapshot.MaxJournalLines {
		t.Fatalf("docker: %+v", docker)
	}
	if bobres.Errors != 0 || len(bobres.Lines) != 0 {
		t.Errorf("bobres: %+v", bobres)
	}
	all := ""
	for _, l := range docker.Lines {
		all += l.Msg + "\n"
		if l.At == 0 {
			t.Errorf("a line without a time: %+v", l)
		}
	}
	for _, leak := range []string{"123456789", "AAEhBOweik6", "203.0.113", "10.0.0.7", "admin@example.com", "<script>", "api.telegram.org/bot1"} {
		if strings.Contains(all, leak) {
			t.Errorf("%q survived the scrubbing:\n%s", leak, all)
		}
	}
	for _, want := range []string{"hello world", "‹script›", "[email]", "[ip]", "one more error"} {
		if !strings.Contains(all, want) {
			t.Errorf("%q is missing:\n%s", want, all)
		}
	}
	line := strings.Join(asked, "\n")
	for _, want := range []string{"journalctl --no-pager -q -o json -p err --since -3600s -n 200 -u docker.service -u containerd.service",
		"-u bobres-monitor.service"} {
		if !strings.Contains(line, want) {
			t.Errorf("journalctl was not run as %q:\n%s", want, line)
		}
	}

	failing := func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("exec: journalctl: not found")
	}
	if collectJournal(context.Background(), failing, time.Hour) != nil {
		t.Error("a section without journalctl")
	}
}

func TestJournalLinesThatAreNotText(t *testing.T) {
	out := []byte(`{"MESSAGE":[104,105,300,-1,0],"__REALTIME_TIMESTAMP":"x"}` + "\n" + `{"MESSAGE":42}` + "\n" + `{"MESSAGE":"` + strings.Repeat("a", 5000) + `"}` + "\n")
	count, lines := parseJournal(out)
	if count != 3 || len(lines) != 3 || !strings.HasPrefix(lines[0].Msg, "hi") || lines[1].Msg != "" || len([]rune(lines[2].Msg)) > snapshot.MaxLine+1 {
		t.Errorf("%d %+v", count, lines)
	}
}

func TestClock(t *testing.T) {
	run := func(out string) Runner {
		return func(context.Context, string, ...string) ([]byte, error) { return []byte(out), nil }
	}
	c := collectClock(context.Background(), run(string(fixture(t, "system/timedatectl.txt"))))
	if c == nil || c.NTP == nil || c.Synchronized == nil || *c.NTP || *c.Synchronized {
		t.Errorf("the lab's clock is not synchronized: %+v", c)
	}
	c = collectClock(context.Background(), run("NTP=yes\nNTPSynchronized=yes\n"))
	if c == nil || !*c.NTP || !*c.Synchronized {
		t.Errorf("synchronized: %+v", c)
	}
	if collectClock(context.Background(), run("nonsense\nNTP=maybe\n")) != nil {
		t.Error("nonsense was believed")
	}
	if collectClock(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no") }) != nil {
		t.Error("no timedatectl")
	}
}
