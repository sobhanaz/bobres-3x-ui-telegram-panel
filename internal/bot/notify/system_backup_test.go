package notify

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func backupEvent(kind, state, lang string) events.SystemBackupEvent {
	return events.SystemBackupEvent{TelegramID: ownerTG, Language: lang, Kind: kind, State: state, File: "bobres-20261011T033000Z.bkp", Source: "backup", By: "sobhan", At: 1760000000,
		Size: 1536 * 1024}
}

func handleBackup(t *testing.T, n *Notifier, e events.SystemBackupEvent, createdAt time.Time) error {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return n.Handle(context.Background(), eventbus.Message{Topic: events.SystemBackup, Payload: payload, CreatedAt: createdAt})
}

// A backup downloaded from the dashboard, and a restore asked for there and each way it ends, reach
// the owner in their language: who, which backup, and for a restore where it came from.
func TestSystemBackupNoticesInEnglish(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	for _, tc := range []struct {
		kind, state string
		want        []string
	}{
		{events.BackupDownload, events.BackupDownloaded, []string{"Backup downloaded", "sobhan downloaded the backup <code>bobres-20261011T033000Z.bkp</code> (1.5 MB)", "key stays on the server"}},
		{events.BackupRestore, events.UpdateRequested, []string{"Restore requested", "sobhan asked in the dashboard to restore the backup <code>bobres-20261011T033000Z.bkp</code> (a backup kept on the server)", "Everything after the backup's time", "audit log"}},
		{events.BackupRestore, events.UpdateDone, []string{"Restore finished", "BOBRES now runs the data of the backup <code>bobres-20261011T033000Z.bkp</code> (asked by sobhan)"}},
		{events.BackupRestore, events.UpdateFailed, []string{"Restore failed", "(asked by sobhan) did not finish", "<code>disk full</code>"}},
		{events.BackupRestore, events.UpdateRolledBack, []string{"Restore rolled back", "went back to the data from before it on its own", "<code>disk full</code>"}},
		{events.BackupRestore, events.UpdateNotStarted, []string{"Restore not started", "never picked up", "<code>disk full</code>"}},
	} {
		e := backupEvent(tc.kind, tc.state, "en")
		e.Message = "disk full"
		if err := handleBackup(t, n, e, time.Now()); err != nil {
			t.Fatalf("%s/%s: %v", tc.kind, tc.state, err)
		}
		got := tgf.take()
		if len(got) != 1 || got[0].chat != ownerTG || got[0].kb != nil {
			t.Fatalf("%s/%s: sent %+v", tc.kind, tc.state, got)
		}
		for _, w := range tc.want {
			if !strings.Contains(got[0].text, w) {
				t.Errorf("%s/%s: lacks %q:\n%s", tc.kind, tc.state, w, got[0].text)
			}
		}
		// the updater's sentence belongs to a bad end only
		wantSentence := tc.state == events.UpdateFailed || tc.state == events.UpdateRolledBack || tc.state == events.UpdateNotStarted
		if strings.Contains(got[0].text, "disk full") != wantSentence {
			t.Errorf("%s/%s: the sentence (wanted %v):\n%s", tc.kind, tc.state, wantSentence, got[0].text)
		}
	}
	// an uploaded backup says so
	e := backupEvent(events.BackupRestore, events.UpdateRequested, "en")
	e.Source = "upload"
	if err := handleBackup(t, n, e, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || !strings.Contains(got[0].text, "(a file the owner uploaded)") {
		t.Fatalf("upload: %+v", got)
	}
}

func TestSystemBackupNoticesInPersian(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	for _, tc := range []struct{ kind, state string }{
		{events.BackupDownload, events.BackupDownloaded}, {events.BackupRestore, events.UpdateRequested}, {events.BackupRestore, events.UpdateDone},
		{events.BackupRestore, events.UpdateFailed}, {events.BackupRestore, events.UpdateRolledBack}, {events.BackupRestore, events.UpdateNotStarted},
	} {
		if err := handleBackup(t, n, backupEvent(tc.kind, tc.state, "fa"), time.Now()); err != nil {
			t.Fatalf("%s/%s: %v", tc.kind, tc.state, err)
		}
		got := tgf.take()
		if len(got) != 1 || got[0].chat != ownerTG {
			t.Fatalf("%s/%s: %+v", tc.kind, tc.state, got)
		}
		text := got[0].text
		if !regexp.MustCompile(`\p{Arabic}{3,}`).MatchString(text) || !strings.Contains(text, "bobres-20261011T033000Z.bkp") || !strings.Contains(text, "sobhan") || strings.ContainsAny(text, "{}") {
			t.Errorf("%s/%s: %s", tc.kind, tc.state, text)
		}
		if strings.Count(text, "<b>") != strings.Count(text, "</b>") || strings.Count(text, "<code>") != strings.Count(text, "</code>") {
			t.Errorf("%s/%s: tags are not balanced: %s", tc.kind, tc.state, text)
		}
	}
}

func TestSystemBackupNoticeEscapesWhatCameFromOutside(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	e := backupEvent(events.BackupRestore, events.UpdateFailed, "en")
	e.By = "<a href=\"https://evil.example\">click</a>"
	e.File = "<script>alert(1)</script>" + strings.Repeat("x", 200) + ".bkp"
	e.Message = "<b>boom</b> & " + strings.Repeat("m", 600)
	if err := handleBackup(t, n, e, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	text := got[0].text
	for _, raw := range []string{"<a ", "<script>", "<b>boom", "https://evil.example\">"} {
		if strings.Contains(text, raw) {
			t.Errorf("%q is in the message unescaped: %s", raw, text)
		}
	}
	if len([]rune(text)) > 1200 {
		t.Errorf("not cut: %d runes", len([]rune(text)))
	}
}

func TestSystemBackupNoticeRecipientsAndAge(t *testing.T) {
	n, tgf, set := alertNotifier(t)
	now := time.Now()
	set.SetStaff([]settings.Contact{{TelegramID: ownerTG, Language: "fa", Role: "owner"}, {TelegramID: adminTG, Language: "en", Role: "admin"}})
	if err := handleBackup(t, n, backupEvent(events.BackupDownload, events.BackupDownloaded, "en"), now); err != nil || len(tgf.take()) != 1 {
		t.Fatalf("an owner: %v", err)
	}
	e := backupEvent(events.BackupDownload, events.BackupDownloaded, "en")
	e.TelegramID = adminTG
	if err := handleBackup(t, n, e, now); err != nil || len(tgf.take()) != 0 {
		t.Fatalf("an admin was told: %v", err)
	}
	if err := handleBackup(t, n, backupEvent(events.BackupDownload, events.BackupDownloaded, "en"), now.Add(-25*time.Hour)); err != nil || len(tgf.take()) != 0 {
		t.Fatalf("a notice a day old was sent: %v", err)
	}
	// a kind or a state a newer core writes
	for _, e := range []events.SystemBackupEvent{backupEvent("copy", events.BackupDownloaded, "en"), backupEvent(events.BackupRestore, "paused", "en"),
		backupEvent(events.BackupDownload, events.UpdateDone, "en")} {
		if err := handleBackup(t, n, e, now); err != nil || len(tgf.take()) != 0 {
			t.Fatalf("%+v: %v", e, err)
		}
	}
}
