package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// The required chats (joinchats.go): the first is the setting join.channel, the
// others a table, and the license says how many count.

func chat(name string) JoinChat { return JoinChat{Chat: "@" + name} }

func names(list []JoinChat) string {
	var out []string
	for _, c := range list {
		out = append(out, c.Chat)
	}
	return strings.Join(out, ",")
}

func itemNames(l *JoinChatList) string {
	var out []string
	for _, it := range l.Items {
		s := it.Chat
		if it.Active {
			s += "*"
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

func (w *gateWorld) setJoin(list ...JoinChat) (*JoinChatList, error) {
	return w.svc.SetJoinChats(context.Background(), w.own, list)
}

func (w *gateWorld) mustJoin(list ...JoinChat) *JoinChatList {
	w.t.Helper()
	l, err := w.setJoin(list...)
	if err != nil {
		w.t.Fatalf("set %v: %v", names(list), err)
	}
	return l
}

// required is the list customers are held to now, as the Mini App builds it: the first chat of the
// settings and the extras the license allows.
func (w *gateWorld) required(ctx context.Context, settings map[string]string) ([]JoinChat, error) {
	extras, err := w.svc.EnforcedJoinExtras(ctx)
	return JoinList(settings, extras), err
}

func (w *gateWorld) audits(action string) int {
	w.t.Helper()
	var n int
	if err := w.s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// The trial and Pro hold any number; the first row is the setting, the others
// the table, in order; an empty list turns the requirement off.
func TestJoinChatsList(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t) // the trial is running
	l := w.mustJoin(
		JoinChat{Chat: "@first_channel", Link: "https://t.me/first_channel", Label: "News"},
		JoinChat{Chat: "-1001234567890", Link: "https://t.me/+AbCdEf123", Label: "VIP"},
		chat("third_group"))
	if itemNames(l) != "@first_channel*,-1001234567890*,@third_group*" || !l.On || l.Limit != license.Unlimited || l.Tier != "trial" {
		t.Fatalf("list: %s on=%v limit=%d tier=%s", itemNames(l), l.On, l.Limit, l.Tier)
	}
	if !l.Items[0].Primary || l.Items[1].Primary {
		t.Error("only the first row is the setting")
	}
	set, err := w.svc.Settings(ctx)
	if err != nil || set["join.channel"] != "@first_channel" || set["join.link"] != "https://t.me/first_channel" || set["join.label"] != "News" {
		t.Fatalf("the first row is not in the settings: %v %v", set, err)
	}
	rows, err := w.s.ListJoinChats(ctx, w.s.Conn())
	if err != nil || len(rows) != 2 || rows[0].Chat != "-1001234567890" || rows[0].Position != 1 || rows[1].Chat != "@third_group" || rows[1].Position != 2 {
		t.Fatalf("extras: %+v %v", rows, err)
	}
	req, err := w.required(ctx, set)
	if err != nil || names(req) != "@first_channel,-1001234567890,@third_group" {
		t.Fatalf("required: %s %v", names(req), err)
	}

	// Reorder (the first stays the setting), edit a name, remove one.
	l = w.mustJoin(chat("third_group"), JoinChat{Chat: "@first_channel", Label: "Other name"})
	if itemNames(l) != "@third_group*,@first_channel*" {
		t.Fatalf("reordered: %s", itemNames(l))
	}
	if set, _ = w.svc.Settings(ctx); set["join.channel"] != "@third_group" || set["join.link"] != "" || set["join.label"] != "" {
		t.Fatalf("the setting follows the first row: %v", set)
	}
	if rows, _ = w.s.ListJoinChats(ctx, w.s.Conn()); len(rows) != 1 || rows[0].Label != "Other name" {
		t.Fatalf("extras after the edit: %+v", rows)
	}
	// An empty list: the requirement is off, nothing is left.
	l = w.mustJoin()
	if l.On || len(l.Items) != 0 {
		t.Fatalf("empty list: %+v", l)
	}
	if set, _ = w.svc.Settings(ctx); set["join.channel"] != "" {
		t.Fatalf("join.channel after an empty list: %q", set["join.channel"])
	}
	if req, _ = w.required(ctx, set); len(req) != 0 {
		t.Fatalf("required while off: %v", req)
	}
}

// Every row is checked as the settings check their keys, and the list as a
// whole; nothing is saved when one row is wrong.
func TestJoinChatsValidation(t *testing.T) {
	w := newGateWorld(t)
	w.mustJoin(chat("kept_channel"))
	long := strings.Repeat("x", JoinLabelMax+1)
	var many []JoinChat
	for i := range MaxJoinChatsTotal + 1 {
		many = append(many, chat(fmt.Sprintf("channel_%03d", i)))
	}
	for _, c := range []struct {
		name  string
		list  []JoinChat
		field string
	}{
		{"a chat that is no chat", []JoinChat{chat("ok_channel"), {Chat: "channel"}}, "chats.1.chat"},
		{"an empty chat", []JoinChat{chat("ok_channel"), {Chat: ""}}, "chats.1.chat"},
		{"a short @name", []JoinChat{{Chat: "@abc"}}, "chats.0.chat"},
		{"a private chat with no link", []JoinChat{{Chat: "-1001234567890"}}, "chats.0.link"},
		{"a link that is not t.me", []JoinChat{{Chat: "@ok_channel", Link: "https://example.com/x"}}, "chats.0.link"},
		{"a link that is not https", []JoinChat{{Chat: "@ok_channel", Link: "http://t.me/x"}}, "chats.0.link"},
		{"a label that is too long", []JoinChat{chat("ok_channel"), {Chat: "@other_channel", Label: long}}, "chats.1.label"},
		{"a label of two lines", []JoinChat{{Chat: "@ok_channel", Label: "a\nb"}}, "chats.0.label"},
		{"a chat twice", []JoinChat{chat("same_channel"), chat("other_channel"), chat("same_channel")}, "chats.2.chat"},
		{"a chat twice, case aside", []JoinChat{chat("Same_Channel"), chat("same_channel")}, "chats.1.chat"},
		{"too many", many, "chats"},
	} {
		_, err := w.setJoin(c.list...)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != c.field || !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want a field error at %s", c.name, err, c.field)
		}
	}
	l, err := w.svc.JoinChats(context.Background())
	if err != nil || itemNames(l) != "@kept_channel*" {
		t.Fatalf("a refused list changed something: %s %v", itemNames(l), err)
	}
	// What is typed with spaces or another case of the same word is cleaned.
	l = w.mustJoin(JoinChat{Chat: " @tidy_channel ", Link: " https://t.me/tidy_channel ", Label: "  Two   words "})
	if l.Items[0].Label != "Two words" || l.Items[0].Link != "https://t.me/tidy_channel" || l.Items[0].Chat != "@tidy_channel" {
		t.Fatalf("not cleaned: %+v", l.Items[0])
	}
}

// Adding past the limit is refused with words naming the limit and the plan
// that has none; the free tier and a Standard license allow one.
func TestJoinChatsLimit(t *testing.T) {
	const buy = "buy or renew a license to add more required channels and groups"
	for _, c := range []struct {
		name  string
		setup func(w *gateWorld)
		who   string
	}{
		{"the free tier", func(w *gateWorld) { w.endTrial() }, "the free tier allows 1 required chat"},
		{"a standard license", func(w *gateWorld) {
			w.install(func(tok *license.Token) { tok.Entitlements.MaxJoinChats = 1 })
		}, "the license allows 1 required chat"},
		{"a license that says nothing about it", func(w *gateWorld) { w.install(nil) }, "the license allows 1 required chat"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newGateWorld(t)
			c.setup(w)
			w.mustJoin(chat("first_channel"))
			_, err := w.setJoin(chat("first_channel"), chat("second_channel"))
			wantRefusal(t, "a second chat", err, GateJoinChats, c.who, "Pro", buy)
			l, err := w.svc.JoinChats(context.Background())
			if err != nil || itemNames(l) != "@first_channel*" || l.Limit != 1 {
				t.Fatalf("the refused list was saved: %s limit %d %v", itemNames(l), l.Limit, err)
			}
			// Changing the one chat is not adding.
			w.mustJoin(JoinChat{Chat: "@first_channel", Label: "Renamed"})
			w.mustJoin(chat("replacement_channel"))
		})
	}

	t.Run("Pro", func(t *testing.T) {
		w := newGateWorld(t)
		w.endTrial()
		w.install(func(tok *license.Token) {
			tok.Tier = license.TierPro
			tok.Entitlements.MaxJoinChats = license.Unlimited
		})
		var list []JoinChat
		for i := range 12 {
			list = append(list, chat(fmt.Sprintf("channel_%02d", i)))
		}
		if l := w.mustJoin(list...); len(l.Items) != 12 || l.Limit != license.Unlimited || !l.Items[11].Active {
			t.Fatalf("pro: %s", itemNames(l))
		}
	})
}

// A license that ends keeps every saved chat and enforces the first N, the
// first one always; what is saved can still be edited, removed and reordered,
// but not grown; and the chats count again when the limit allows.
func TestJoinChatsAfterTheLicenseEnds(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	w.endTrial()
	pro := func(tok *license.Token) {
		tok.Tier = license.TierPro
		tok.Entitlements.MaxJoinChats = license.Unlimited
	}
	w.install(pro)
	w.mustJoin(chat("first_channel"), chat("second_channel"), chat("third_channel"))

	// The license ends: the free tier's one chat is enforced; two are inactive but saved.
	w.install(func(tok *license.Token) { pro(tok); tok.ExpiresAt = w.now.Add(-60 * 24 * time.Hour) })
	l, err := w.svc.JoinChats(ctx)
	if err != nil || itemNames(l) != "@first_channel*,@second_channel,@third_channel" || l.Limit != 1 || l.Basis != license.BasisFree {
		t.Fatalf("after the license ended: %s limit %d basis %s %v", itemNames(l), l.Limit, l.Basis, err)
	}
	if extras, err := w.svc.EnforcedJoinExtras(ctx); err != nil || len(extras) != 0 {
		t.Fatalf("enforced extras: %v %v", extras, err)
	}
	set, _ := w.svc.Settings(ctx)
	if req, err := w.required(ctx, set); err != nil || names(req) != "@first_channel" {
		t.Fatalf("required: %s %v", names(req), err)
	}
	if rows, _ := w.s.ListJoinChats(ctx, w.s.Conn()); len(rows) != 2 {
		t.Fatalf("saved extras: %+v", rows)
	}

	// Growing is refused; the same length or shorter is fine, so is moving a saved chat first.
	_, err = w.setJoin(chat("first_channel"), chat("second_channel"), chat("third_channel"), chat("fourth_channel"))
	wantRefusal(t, "a fourth", err, GateJoinChats, "the free tier allows 1 required chat")
	w.mustJoin(chat("second_channel"), chat("first_channel"), chat("third_channel"))
	if l, _ = w.svc.JoinChats(ctx); itemNames(l) != "@second_channel*,@first_channel,@third_channel" {
		t.Fatalf("reordered: %s", itemNames(l))
	}
	w.mustJoin(chat("second_channel"), chat("fourth_channel"), chat("third_channel")) // one swapped for another, not grown
	w.mustJoin(chat("second_channel"), chat("fourth_channel"))
	if l, _ = w.svc.JoinChats(ctx); itemNames(l) != "@second_channel*,@fourth_channel" {
		t.Fatalf("shortened: %s", itemNames(l))
	}

	// The license comes back: the saved chats count again.
	w.install(pro)
	if extras, err := w.svc.EnforcedJoinExtras(ctx); err != nil || names(extras) != "@fourth_channel" {
		t.Fatalf("enforced extras after the renewal: %v %v", extras, err)
	}

	// Two chats saved, a limit of 2 (a license of its own): the first two count.
	w.install(func(tok *license.Token) { tok.Entitlements.MaxJoinChats = 2 })
	_, err = w.setJoin(chat("second_channel"), chat("fourth_channel"), chat("fifth_channel"))
	wantRefusal(t, "a third with a limit of 2", err, GateJoinChats, "the license allows 2 required chats")
	w.mustJoin(chat("fourth_channel"), chat("second_channel"))
}

// While the first chat is empty the requirement is off, and the extras saved
// do nothing until it is set again.
func TestJoinChatsOffWhileTheFirstIsEmpty(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	w.mustJoin(chat("first_channel"), chat("second_channel"))
	if _, err := w.svc.SetSettings(ctx, w.own, map[string]string{"join.channel": ""}); err != nil {
		t.Fatal(err)
	}
	l, err := w.svc.JoinChats(ctx)
	if err != nil || l.On || len(l.Items) != 1 || l.Items[0].Active || l.Items[0].Primary {
		t.Fatalf("while off: %+v %v", l, err)
	}
	set, _ := w.svc.Settings(ctx)
	if req, _ := w.required(ctx, set); len(req) != 0 {
		t.Fatalf("required while off: %v", req)
	}
	if extras, _ := w.svc.EnforcedJoinExtras(ctx); len(extras) != 0 {
		t.Fatalf("enforced extras while off: %v", extras)
	}
	if u, err := w.svc.LicenseUsage(ctx, nil, nil); err != nil || u.JoinChats != 1 {
		t.Fatalf("usage while off: %+v %v", u, err)
	}
	// A chat typed as the first one and also an extra is asked once.
	if _, err := w.svc.SetSettings(ctx, w.own, map[string]string{"join.channel": "@SECOND_channel"}); err != nil {
		t.Fatal(err)
	}
	set, _ = w.svc.Settings(ctx)
	if req, _ := w.required(ctx, set); names(req) != "@SECOND_channel" {
		t.Fatalf("a chat twice: %v", req)
	}
}

// The audit log has an entry for the setting and for each chat that changed.
func TestJoinChatsAreAudited(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	w.mustJoin(chat("first_channel"), chat("second_channel"), chat("third_channel"))
	if n := w.audits("setting.set"); n != 1 { // join.channel; the empty link and label are not written
		t.Errorf("%d setting.set entries", n)
	}
	if n := w.audits("join_chat.add"); n != 2 {
		t.Errorf("%d join_chat.add entries, want 2", n)
	}
	w.mustJoin(chat("first_channel"), chat("second_channel"), chat("third_channel")) // nothing changes
	if n := w.audits("join_chat.add") + w.audits("join_chat.update") + w.audits("join_chat.move") + w.audits("join_chat.remove"); n != 2 {
		t.Errorf("a save without a change was audited: %d", n)
	}
	w.mustJoin(chat("first_channel"), JoinChat{Chat: "@second_channel", Label: "Two"}, chat("third_channel"))
	if n := w.audits("join_chat.update"); n != 1 {
		t.Errorf("%d join_chat.update entries", n)
	}
	w.mustJoin(chat("first_channel"), chat("third_channel"), JoinChat{Chat: "@second_channel", Label: "Two"})
	if n := w.audits("join_chat.move"); n != 2 {
		t.Errorf("%d join_chat.move entries, want the 2 that swapped", n)
	}
	w.mustJoin(chat("first_channel"), chat("third_channel"))
	if n := w.audits("join_chat.remove"); n != 1 {
		t.Errorf("%d join_chat.remove entries", n)
	}
	var entity, id string
	if err := w.s.DB().QueryRow(ctx, `SELECT entity, entity_id FROM core.audit_log WHERE action = 'join_chat.remove'`).Scan(&entity, &id); err != nil ||
		entity != "join_chat" || id != "@second_channel" {
		t.Errorf("the removal: %q %q %v", entity, id, err)
	}
	var before, after string
	if err := w.s.DB().QueryRow(ctx, `SELECT before::text, coalesce(after::text, '') FROM core.audit_log WHERE action = 'join_chat.update'`).Scan(&before, &after); err != nil ||
		!strings.Contains(before, `"label": ""`) || !strings.Contains(after, `"label": "Two"`) {
		t.Errorf("the update: %s -> %s %v", before, after, err)
	}
}
