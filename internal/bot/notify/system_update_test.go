package notify

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func updateEvent(state, lang string) events.SystemUpdateEvent {
	return events.SystemUpdateEvent{TelegramID: ownerTG, Language: lang, State: state, From: "0.6.1", To: "0.7.0", Channel: "stable", By: "sobhan", At: 1760000000}
}

func handleUpdate(t *testing.T, n *Notifier, e events.SystemUpdateEvent, createdAt time.Time) error {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return n.Handle(context.Background(), eventbus.Message{Topic: events.SystemUpdate, Payload: payload, CreatedAt: createdAt})
}

// An update asked for in the dashboard, and each way it ends, reach the owner
// in their language: who asked, from which version to which, on which channel.
func TestSystemUpdateNoticesInEnglish(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	for state, want := range map[string]string{
		events.UpdateRequested: "🔄 <b>Update requested</b>\nsobhan asked in the dashboard to update BOBRES from 0.6.1 to 0.7.0 (stable channel).\n" +
			"The dashboard and the bot will be unavailable for a few minutes; customers' VPN links keep working. If you did not expect this, check the audit log and the staff list.",
		events.UpdateDone: "✅ <b>Update finished</b>\nBOBRES now runs 0.7.0 (it was 0.6.1). Asked by sobhan.",
		events.UpdateFailed: "❌ <b>Update failed</b>\nThe update from 0.6.1 to 0.7.0 (asked by sobhan) did not finish. If the dashboard does not open, run sudo bobres status on the server.\n" +
			"<code>the backup failed: disk full</code>",
		events.UpdateRolledBack: "↩️ <b>Update rolled back</b>\nThe update from 0.6.1 to 0.7.0 (asked by sobhan) did not work, so BOBRES went back to 0.6.1 on its own.\n" +
			"<code>the backup failed: disk full</code>",
		events.UpdateNotStarted: "⚠️ <b>Update not started</b>\nThe update to 0.7.0 that sobhan asked for was never picked up by the server's updater, so nothing changed. Run sudo bobres update once on the server.\n" +
			"<code>the backup failed: disk full</code>",
	} {
		e := updateEvent(state, "en")
		e.Message = "the backup failed: disk full"
		if state == events.UpdateRequested || state == events.UpdateDone {
			want = strings.TrimSuffix(want, "\n<code>the backup failed: disk full</code>") // the sentence belongs to a bad end
		}
		if err := handleUpdate(t, n, e, time.Now()); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		got := tgf.take()
		if len(got) != 1 || got[0].chat != ownerTG || got[0].text != want || got[0].kb != nil {
			t.Errorf("%s: sent %+v\nwant   %s", state, got, want)
		}
	}
	// the beta channel is named as what it is
	e := updateEvent(events.UpdateRequested, "en")
	e.Channel = "beta"
	e.To = "0.7.0-rc.1"
	if err := handleUpdate(t, n, e, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || !strings.Contains(got[0].text, "to 0.7.0-rc.1 (beta channel, pre-releases included)") {
		t.Fatalf("beta: %+v", got)
	}
}

func TestSystemUpdateNoticesInPersian(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	for _, state := range []string{events.UpdateRequested, events.UpdateDone, events.UpdateFailed, events.UpdateRolledBack, events.UpdateNotStarted} {
		e := updateEvent(state, "fa")
		e.Message = "disk full"
		if err := handleUpdate(t, n, e, time.Now()); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		got := tgf.take()
		if len(got) != 1 || got[0].chat != ownerTG {
			t.Fatalf("%s: %+v", state, got)
		}
		text := got[0].text
		// Persian words, the versions and who asked, no hole left in the text
		if !regexp.MustCompile(`\p{Arabic}{3,}`).MatchString(text) || !strings.Contains(text, "0.7.0") || !strings.Contains(text, "sobhan") || strings.ContainsAny(text, "{}") {
			t.Errorf("%s: %s", state, text)
		}
		if state == events.UpdateRequested && !strings.Contains(text, "کانال پایدار") {
			t.Errorf("the channel is not named in Persian: %s", text)
		}
		if strings.Count(text, "<b>") != strings.Count(text, "</b>") || strings.Count(text, "<code>") != strings.Count(text, "</code>") {
			t.Errorf("%s: tags are not balanced: %s", state, text)
		}
		wantCode := state == events.UpdateFailed || state == events.UpdateRolledBack || state == events.UpdateNotStarted
		if strings.Contains(text, "<code>disk full</code>") != wantCode {
			t.Errorf("%s: the updater's sentence (code %v): %s", state, wantCode, text)
		}
	}
	// no language, or one this bot does not have: the default language (Persian)
	for _, lang := range []string{"", "de"} {
		if err := handleUpdate(t, n, updateEvent(events.UpdateDone, lang), time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := tgf.take(); len(got) != 1 || !strings.Contains(got[0].text, "به\u200cروزرسانی انجام شد") {
			t.Errorf("language %q: %+v", lang, got)
		}
	}
}

// Everything in a notice that came from outside (who asked, the updater's own
// words) is escaped, and cut to length.
func TestSystemUpdateNoticeEscapesWhatCameFromOutside(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	e := updateEvent(events.UpdateFailed, "en")
	e.By = "<a href=\"https://evil.example\">click</a>" + strings.Repeat("x", 200)
	e.From, e.To = "<b>0.6.1", "0.7.0</i>"
	e.Message = "<script>alert(1)</script> & " + strings.Repeat("m", 600)
	if err := handleUpdate(t, n, e, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	text := got[0].text
	for _, raw := range []string{"<a ", "<script>", "<b>0.6.1", "0.7.0</i>", "https://evil.example\">"} {
		if strings.Contains(text, raw) {
			t.Errorf("%q is in the message unescaped: %s", raw, text)
		}
	}
	if !strings.Contains(text, "&lt;script&gt;alert(1)&lt;/script&gt; &amp; ") || !strings.HasSuffix(text, "…</code>") {
		t.Errorf("the sentence: %s", text)
	}
	if strings.Count(text, "m") > 700 || len([]rune(text)) > 1200 {
		t.Errorf("not cut: %d runes", len([]rune(text)))
	}
	// no names or versions: placeholders, not holes
	e2 := events.SystemUpdateEvent{TelegramID: ownerTG, Language: "en", State: events.UpdateDone}
	if err := handleUpdate(t, n, e2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || got[0].text != "✅ <b>Update finished</b>\nBOBRES now runs ? (it was ?). Asked by ?." {
		t.Fatalf("empty values: %+v", got)
	}
}

// Notices are for owners: one for a person the bot knows is not an owner any
// more (or never was) is dropped; with no staff list yet, it is sent. One that
// waited longer than a day, a state a newer core writes, or a Telegram refusal
// is dropped too, and a failure that may pass is retried.
func TestSystemUpdateNoticeRecipientsAndAge(t *testing.T) {
	n, tgf, set := alertNotifier(t)
	now := time.Now()
	// the staff list is not known yet: the event decides
	if err := handleUpdate(t, n, updateEvent(events.UpdateRequested, "en"), now); err != nil || len(tgf.take()) != 1 {
		t.Fatalf("no staff list: %v", err)
	}
	set.SetStaff([]settings.Contact{{TelegramID: ownerTG, Language: "fa", Role: "owner"}, {TelegramID: adminTG, Language: "en", Role: "admin"}, {TelegramID: supportTG, Role: "support"}})
	if err := handleUpdate(t, n, updateEvent(events.UpdateRequested, "en"), now); err != nil || len(tgf.take()) != 1 {
		t.Fatalf("an owner: %v", err)
	}
	for _, tgID := range []int64{adminTG, supportTG, 999} {
		e := updateEvent(events.UpdateRequested, "en")
		e.TelegramID = tgID
		if err := handleUpdate(t, n, e, now); err != nil || len(tgf.take()) != 0 {
			t.Errorf("%d got an owner's notice: %v", tgID, err)
		}
	}
	// age
	if err := handleUpdate(t, n, updateEvent(events.UpdateDone, "en"), now.Add(-25*time.Hour)); err != nil || len(tgf.take()) != 0 {
		t.Fatalf("a notice a day old was sent: %v", err)
	}
	if err := handleUpdate(t, n, updateEvent(events.UpdateDone, "en"), now.Add(-23*time.Hour)); err != nil || len(tgf.take()) != 1 {
		t.Fatalf("a notice from 23 hours ago: %v", err)
	}
	if err := handleUpdate(t, n, updateEvent(events.UpdateDone, "en"), time.Time{}); err != nil || len(tgf.take()) != 1 {
		t.Fatalf("no queue time: %v", err)
	}
	// a state this bot does not know
	if err := handleUpdate(t, n, updateEvent("paused", "en"), now); err != nil || len(tgf.take()) != 0 {
		t.Fatalf("an unknown state: %v", err)
	}
	// a refusal is permanent, a failure that may pass is not
	tgf.fail = map[int64]error{ownerTG: &tg.APIError{Code: 400, Description: "Bad Request: PEER_ID_INVALID"}}
	if err := handleUpdate(t, n, updateEvent(events.UpdateDone, "en"), now); err == nil || !eventbus.IsPermanent(err) {
		t.Fatalf("a refusal: %v", err)
	}
	tgf.fail = map[int64]error{ownerTG: &tg.APIError{Code: 500, Description: "Internal Server Error"}}
	if err := handleUpdate(t, n, updateEvent(events.UpdateDone, "en"), now); err == nil || eventbus.IsPermanent(err) {
		t.Fatalf("a transient failure: %v", err)
	}
	// an owner who blocked the bot is skipped (and marked in core), not retried for ever
	core := &blockedCore{fakeCore: &fakeCore{}}
	blocked := New(core, tgf, n.cat, set, []int64{ownerTG}, nil)
	tgf.fail = map[int64]error{ownerTG: &tg.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}}
	if err := handleUpdate(t, blocked, updateEvent(events.UpdateDone, "en"), now); err != nil {
		t.Fatalf("a blocked owner: %v", err)
	}
	if len(core.blocked) != 1 || core.blocked[0] != ownerTG {
		t.Fatalf("marked as blocking the bot: %v", core.blocked)
	}
	// a payload that is not JSON is dead-lettered at once
	if err := n.Handle(context.Background(), eventbus.Message{Topic: events.SystemUpdate, Payload: []byte("{")}); err == nil || !eventbus.IsPermanent(err) {
		t.Fatalf("garbage: %v", err)
	}
}
