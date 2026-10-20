package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The Settings page's Join card (join_chats.go): the owner edits the whole list
// of required channels and groups, the license says how many count, and admins
// see it without changing it.

func chatsOf(out map[string]any) []map[string]any {
	list, _ := out["chats"].([]any)
	var res []map[string]any
	for _, v := range list {
		m, _ := v.(map[string]any)
		res = append(res, m)
	}
	return res
}

func joinBody(rows ...[3]string) map[string]any {
	chats := make([]map[string]string, len(rows))
	for i, r := range rows {
		chats[i] = map[string]string{"chat": r[0], "link": r[1], "label": r[2]}
	}
	return map[string]any{"chats": chats}
}

func TestJoinChatsAPI(t *testing.T) {
	f, b, bot := setupFixture(t)
	ctx := context.Background()

	// Nothing is required yet; the install is in its trial, which has no limit.
	code, out := b.do(http.MethodGet, "/api/v1/join-chats", nil)
	if code != 200 || len(chatsOf(out)) != 0 || out["on"] != false || out["limit"] != float64(-1) || out["editable"] != true || out["max"] != float64(50) || out["tier"] != "trial" {
		t.Fatalf("empty: %d %v", code, out)
	}

	// The owner saves three: the first is the setting join.channel.
	refreshes := bot.refreshes
	code, out = b.do(http.MethodPut, "/api/v1/join-chats", joinBody(
		[3]string{"@first_news", "", "News"},
		[3]string{"-1001234567890", "https://t.me/+AbCdEf123", "VIP room"},
		[3]string{"@third_group", "", ""}))
	chats := chatsOf(out)
	if code != 200 || len(chats) != 3 || out["on"] != true || out["used"] != float64(3) {
		t.Fatalf("saved: %d %v", code, out)
	}
	if chats[0]["primary"] != true || chats[1]["primary"] != false || chats[0]["chat"] != "@first_news" || chats[1]["label"] != "VIP room" || chats[2]["active"] != true {
		t.Fatalf("the list: %v", chats)
	}
	waitFor(t, "the bot being told", func() bool { bot.mu.Lock(); defer bot.mu.Unlock(); return bot.refreshes > refreshes })
	code, out = b.do(http.MethodGet, "/api/v1/settings", nil)
	if code != 200 || settingByKey(out, "join.channel")["value"] != "@first_news" || settingByKey(out, "join.label")["value"] != "News" {
		t.Fatalf("the settings: %d %v %v", code, settingByKey(out, "join.channel"), settingByKey(out, "join.label"))
	}
	if e := lastAudit(t, b, "action=join_chat.add"); e["entity"] != "join_chat" || !strings.Contains(asJSON(e["after"]), "third_group") {
		t.Errorf("audit of the added chat: %v", e)
	}

	// A row core refuses names its field, and nothing is saved.
	for _, c := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a chat that is no chat", joinBody([3]string{"@first_news", "", ""}, [3]string{"news", "", ""}), "chats.1.chat"},
		{"a private chat with no link", joinBody([3]string{"@first_news", "", ""}, [3]string{"-1009876543210", "", ""}), "chats.1.link"},
		{"a chat twice", joinBody([3]string{"@first_news", "", ""}, [3]string{"@First_News", "", ""}), "chats.1.chat"},
	} {
		code, out := b.do(http.MethodPut, "/api/v1/join-chats", c.body)
		if code != 400 || out["field"] != c.field {
			t.Errorf("%s: %d %v", c.name, code, out)
		}
	}
	if _, out := b.do(http.MethodGet, "/api/v1/join-chats", nil); len(chatsOf(out)) != 3 {
		t.Fatalf("a refused save changed the list: %v", out)
	}

	// An admin sees the list but cannot change it; support sees nothing.
	f.staffUser(8801, "admin")
	adm := f.browser()
	if adm.loginWithLink(8801) != 200 {
		t.Fatal("login")
	}
	if code, out := adm.do(http.MethodGet, "/api/v1/join-chats", nil); code != 200 || out["editable"] != false || len(chatsOf(out)) != 3 {
		t.Errorf("admin's view: %d %v", code, out)
	}
	if code, _ := adm.do(http.MethodPut, "/api/v1/join-chats", joinBody([3]string{"@admin_chan", "", ""})); code != 403 {
		t.Errorf("an admin changed the list: %d", code)
	}
	f.staffUser(8802, "support")
	sup := f.browser()
	if sup.loginWithLink(8802) != 200 {
		t.Fatal("login")
	}
	if code, _ := sup.do(http.MethodGet, "/api/v1/join-chats", nil); code != 403 {
		t.Errorf("support read the list: %d", code)
	}

	// The trial ends: the free tier counts one. The saved chats stay, the others are inactive,
	// nothing can be added, and the owner can still remove, edit and reorder.
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	code, out = b.do(http.MethodGet, "/api/v1/join-chats", nil)
	chats = chatsOf(out)
	if code != 200 || out["limit"] != float64(1) || out["tier"] != "free" || chats[0]["active"] != true || chats[1]["active"] != false || chats[2]["active"] != false {
		t.Fatalf("after the trial: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/join-chats", joinBody(
		[3]string{"@first_news", "", "News"}, [3]string{"-1001234567890", "https://t.me/+AbCdEf123", "VIP room"},
		[3]string{"@third_group", "", ""}, [3]string{"@fourth_group", "", ""}))
	if code != 409 || out["error"] != "state" || out["license"] != "max_join_chats" ||
		!strings.Contains(asJSON(out["message"]), "the free tier allows 1 required chat") || !strings.Contains(asJSON(out["message"]), "Pro") {
		t.Fatalf("a fourth chat: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/join-chats", joinBody(
		[3]string{"@third_group", "", ""}, [3]string{"@first_news", "", "News"}))
	chats = chatsOf(out)
	if code != 200 || len(chats) != 2 || chats[0]["chat"] != "@third_group" || chats[0]["active"] != true || chats[1]["active"] != false {
		t.Fatalf("removing one and reordering: %d %v", code, out)
	}
}
