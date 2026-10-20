package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

func TestSupportTickets(t *testing.T) {
	f, b, _ := setupFixture(t)
	ctx := context.Background()
	f.api.cfg.Files = fakeFiles{files: map[string][]byte{"photo-file": []byte("\xff\xd8\xffjpeg")}}
	c := f.customer(9101, "mina")
	r1, err := f.dom.AddCustomerMessage(ctx, domain.CustomerMessage{UserID: c.ID, Category: "payment", Text: "My card payment\nis not here", TGMessageID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.dom.AddCustomerMessage(ctx, domain.CustomerMessage{UserID: c.ID, FileID: "photo-file", FileType: "photo", FileMime: "image/jpeg", TGMessageID: 2}); err != nil {
		t.Fatal(err)
	}
	id := r1.Ticket.ID

	// Support staff work tickets too.
	supU := f.staffUser(9102, "support")
	sup := f.browser()
	sup.loginWithLink(9102)
	code, out := sup.do(http.MethodGet, "/api/v1/tickets?status=open", nil)
	if code != 200 || out["total"] != float64(1) {
		t.Fatalf("list: %d %v", code, out)
	}
	it := items(out)[0]
	if it["subject"] != "My card payment" || it["user"].(map[string]any)["username"] != "mina" || it["last_message"].(map[string]any)["preview"] != "📎" {
		t.Fatalf("ticket: %v", it)
	}
	for q, want := range map[string]float64{fmt.Sprintf("%%23%d", r1.Ticket.Number): 1, "@mina": 1, "card": 1, id[len(id)-6:]: 1, "nothing": 0} {
		if _, out := sup.do(http.MethodGet, "/api/v1/tickets?q="+q, nil); out["total"] != want {
			t.Errorf("search %q: %v", q, out["total"])
		}
	}
	if _, out := sup.do(http.MethodGet, "/api/v1/tickets/counts", nil); out["waiting"] != float64(1) || out["unassigned"] != float64(1) {
		t.Fatalf("counts: %v", out)
	}
	code, out = sup.do(http.MethodGet, "/api/v1/tickets/"+id, nil)
	msgs := out["messages"].([]any)
	if code != 200 || len(msgs) != 2 || out["can_manage"] != false {
		t.Fatalf("detail: %d %v", code, out)
	}
	file := msgs[1].(map[string]any)["file"].(map[string]any)
	resp, err := sup.c.Get(f.srv.URL + file["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") || string(body) != "\xff\xd8\xffjpeg" {
		t.Fatalf("file: %d %v", resp.StatusCode, resp.Header)
	}

	// A reply (sent once even when retried), a note, and the audit without text.
	key := key()
	code, out = sup.do(http.MethodPost, "/api/v1/tickets/"+id+"/messages", map[string]any{"body": "We are on it.", "kind": "reply", "key": key})
	if code != 200 || out["ticket"].(map[string]any)["status"] != "answered" || out["message"].(map[string]any)["delivery"].(map[string]any)["status"] != "pending" {
		t.Fatalf("reply: %d %v", code, out)
	}
	first := out["message"].(map[string]any)["id"]
	if _, out := sup.do(http.MethodPost, "/api/v1/tickets/"+id+"/messages", map[string]any{"body": "We are on it.", "kind": "reply", "key": key}); out["message"].(map[string]any)["id"] != first {
		t.Fatal("a retried reply was stored twice")
	}
	if code, _ := sup.do(http.MethodPost, "/api/v1/tickets/"+id+"/messages", map[string]any{"body": "checked the bank", "kind": "note"}); code != 200 {
		t.Fatalf("note: %d", code)
	}
	if code, out := sup.do(http.MethodPost, "/api/v1/tickets/"+id+"/messages", map[string]any{"body": "", "kind": "reply"}); code != 400 || out["field"] != "body" {
		t.Fatalf("empty reply: %d %v", code, out)
	}
	e := lastAudit(t, b, "entity_id="+id+"&action=ticket.reply")
	if strings.Contains(asJSON(e), "We are on it") {
		t.Fatalf("the audit log holds the reply's text: %v", e)
	}
	// The reply gave the ticket to support; handing it to someone else is a manager's call.
	if _, out := sup.do(http.MethodGet, "/api/v1/tickets?assigned=me", nil); out["total"] != float64(1) {
		t.Fatalf("mine: %v", out)
	}
	ownerU := owner(t, f)
	if code, _ := sup.do(http.MethodPut, "/api/v1/tickets/"+id+"/assignee", map[string]any{"user_id": ownerU.ID}); code != 403 {
		t.Fatalf("support handed a ticket on: %d", code)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/tickets/"+id+"/assignee", map[string]any{"user_id": ownerU.ID}); code != 200 || out["ticket"].(map[string]any)["assignee"].(map[string]any)["id"] != ownerU.ID {
		t.Fatalf("owner assigned: %d %v", code, out)
	}
	// Nor may support take it back or clear it once a manager gave it on.
	for _, to := range []any{supU.ID, nil} {
		if code, _ := sup.do(http.MethodPut, "/api/v1/tickets/"+id+"/assignee", map[string]any{"user_id": to}); code != 403 {
			t.Fatalf("support took or cleared the owner's ticket (%v): %d", to, code)
		}
	}
	if _, out := sup.do(http.MethodGet, "/api/v1/tickets/"+id, nil); out["ticket"].(map[string]any)["assignee"].(map[string]any)["id"] != ownerU.ID {
		t.Fatalf("assignee after refusals: %v", out["ticket"])
	}
	// Close, then a reply is refused until it is reopened.
	if code, out := sup.do(http.MethodPost, "/api/v1/tickets/"+id+"/status", map[string]any{"status": "closed", "reason": "solved"}); code != 200 || out["ticket"].(map[string]any)["status"] != "closed" {
		t.Fatalf("close: %d %v", code, out)
	}
	if code, out := sup.do(http.MethodPost, "/api/v1/tickets/"+id+"/messages", map[string]any{"body": "x", "kind": "reply"}); code != 409 || out["error"] != "state" {
		t.Fatalf("reply to closed: %d %v", code, out)
	}
	if code, _ := sup.do(http.MethodPost, "/api/v1/tickets/"+id+"/status", map[string]any{"status": "open"}); code != 200 {
		t.Fatalf("reopen: %d", code)
	}

	// Saved replies: everyone reads, managers write.
	if code, _ := sup.do(http.MethodPost, "/api/v1/canned-replies", map[string]any{"title": "Wait", "body": map[string]string{"fa": "صبر کنید"}}); code != 403 {
		t.Fatalf("support saved a reply: %d", code)
	}
	code, out = b.do(http.MethodPost, "/api/v1/canned-replies", map[string]any{"title": "Wait", "body": map[string]string{"fa": "لطفاً کمی صبر کنید", "en": "Please wait a little"}, "sort": 1})
	if code != 200 {
		t.Fatalf("save canned: %d %v", code, out)
	}
	cid := out["item"].(map[string]any)["id"].(string)
	if code, out := b.do(http.MethodPost, "/api/v1/canned-replies", map[string]any{"title": "", "body": map[string]string{"fa": "x"}}); code != 400 || out["field"] != "title" {
		t.Fatalf("untitled canned: %d %v", code, out)
	}
	if _, out := sup.do(http.MethodGet, "/api/v1/canned-replies", nil); len(items(out)) != 1 {
		t.Fatalf("canned list: %v", out)
	}
	if code, _ := b.do(http.MethodDelete, "/api/v1/canned-replies/"+cid, nil); code != 204 {
		t.Fatalf("delete canned: %d", code)
	}

	// The user page and the overview count tickets.
	if _, out := b.do(http.MethodGet, "/api/v1/users/"+c.ID, nil); out["counts"].(map[string]any)["tickets"] != float64(1) {
		t.Fatalf("user counts: %v", out["counts"])
	}
	if _, out := b.do(http.MethodGet, "/api/v1/overview", nil); out["tickets"].(map[string]any)["waiting"] != float64(1) {
		t.Fatalf("overview: %v", out["tickets"])
	}
	// Nobody logged out reads tickets.
	if code, _ := f.browser().do(http.MethodGet, "/api/v1/tickets", nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
}

// A reseller application is a ticket of its own category: the inbox lists it
// under that name, filters by it, and its page says so; staff answer it like
// any ticket.
func TestSupportResellerApplicationsInTheInbox(t *testing.T) {
	f, b, _ := setupFixture(t)
	ctx := context.Background()
	shop := f.customer(9111, "shopper")
	app, err := f.dom.AddCustomerMessage(ctx, domain.CustomerMessage{UserID: shop.ID, Category: domain.CategoryReseller, Text: "I run a shop\nin Tehran", TGMessageID: 1})
	if err != nil {
		t.Fatal(err)
	}
	payer := f.customer(9112, "payer")
	if _, err := f.dom.AddCustomerMessage(ctx, domain.CustomerMessage{UserID: payer.ID, Category: "payment", Text: "My card payment is not here", TGMessageID: 2}); err != nil {
		t.Fatal(err)
	}

	for filter, want := range map[string]float64{"": 2, "reseller": 1, "payment": 1, "service": 0, "other": 0, "nonsense": 0} {
		code, out := b.do(http.MethodGet, "/api/v1/tickets?category="+filter, nil)
		if code != 200 || out["total"] != want {
			t.Errorf("category %q: %d, %v tickets, want %v", filter, code, out["total"], want)
		}
	}
	_, out := b.do(http.MethodGet, "/api/v1/tickets?category=reseller", nil)
	it := items(out)[0]
	if it["id"] != app.Ticket.ID || it["category"] != "reseller" || it["subject"] != "I run a shop" || it["user"].(map[string]any)["username"] != "shopper" {
		t.Fatalf("the application in the inbox: %v", it)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/tickets/"+app.Ticket.ID, nil); code != 200 || out["ticket"].(map[string]any)["category"] != "reseller" {
		t.Fatalf("the application's page: %d %v", code, out)
	}
	// Answered like any other: the customer is told through the bot's feed, and the
	// ticket can be closed, which lets them apply again.
	if code, out := b.do(http.MethodPost, "/api/v1/tickets/"+app.Ticket.ID+"/messages", map[string]any{"body": "Please send your shop's address", "kind": "reply", "close": true}); code != 200 ||
		out["ticket"].(map[string]any)["status"] != "closed" {
		t.Fatalf("answering the application: %d %v", code, out)
	}
	if _, err := f.dom.AddCustomerMessage(ctx, domain.CustomerMessage{UserID: shop.ID, Category: domain.CategoryReseller, Text: "The address is in Valiasr", TGMessageID: 3}); err != nil {
		t.Fatalf("a new application after the first was closed: %v", err)
	}
}

func TestBroadcastsAPI(t *testing.T) {
	f, b, bot := setupFixture(t)
	ctx := context.Background()
	for _, tg := range []int64{9201, 9202} {
		f.customer(tg, fmt.Sprintf("c%d", tg))
	}
	f.staffUser(9203, "support")
	sup := f.browser()
	sup.loginWithLink(9203)
	if code, _ := sup.do(http.MethodGet, "/api/v1/broadcasts", nil); code != 403 {
		t.Fatalf("support listed broadcasts: %d", code)
	}

	// Checks: every needed language, safe HTML, an https button.
	code, out := b.do(http.MethodPost, "/api/v1/broadcasts", map[string]any{"segment": "all", "language": "", "texts": map[string]string{"fa": "سلام", "en": ""}})
	if code != 400 || !strings.Contains(asJSON(out["problems"]), `"lang":"en"`) {
		t.Fatalf("missing English: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/broadcasts", map[string]any{"segment": "all", "language": "fa", "texts": map[string]string{"fa": "<b>سلام"}}); code != 400 || !strings.Contains(asJSON(out["problems"]), "tag_unclosed") {
		t.Fatalf("broken HTML: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/broadcasts", map[string]any{"segment": "all", "language": "fa", "texts": map[string]string{"fa": "x"},
		"button": map[string]any{"url": "http://x.ir", "labels": map[string]string{"fa": "برو"}}}); code != 400 || out["field"] != "button.url" {
		t.Fatalf("http button: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/broadcasts", map[string]any{"segment": "all", "language": "", "texts": map[string]string{"fa": "<b>تخفیف</b>", "en": "<b>Sale</b>"},
		"button": map[string]any{"url": "https://t.me/shop", "labels": map[string]string{"fa": "خرید", "en": "Buy"}}})
	item, _ := out["item"].(map[string]any)
	if code != 200 || item["status"] != "draft" || item["button"].(map[string]any)["labels"].(map[string]any)["en"] != "Buy" {
		t.Fatalf("create: %d %v", code, out)
	}
	id := item["id"].(string)
	version := item["version"]

	if _, out := b.do(http.MethodPost, "/api/v1/broadcasts/audience", map[string]any{"segment": "all", "language": ""}); out["total"] != float64(4) { // owner, support, 2 customers
		t.Fatalf("audience: %v", out)
	}
	if _, out := b.do(http.MethodPost, "/api/v1/broadcasts/check", map[string]any{"lang": "fa", "text": "<i>x"}); out["ok"] != false || out["max"] != float64(4096) {
		t.Fatalf("check: %v", out)
	}

	// Test sends go to me, through the bot, at most 5 a minute.
	code, out = b.do(http.MethodPost, "/api/v1/broadcasts/test", map[string]any{"lang": "en", "text": "<b>Sale</b>",
		"button": map[string]any{"url": "https://t.me/shop", "labels": map[string]string{"fa": "خرید", "en": "Buy"}}})
	if code != 200 || out["ok"] != true || len(bot.tests) != 1 || bot.tests[0].ChatID != ownerTG || bot.tests[0].Button.Text != "Buy" {
		t.Fatalf("test send: %d %v %+v", code, out, bot.tests)
	}
	for i := 0; i < 4; i++ {
		b.do(http.MethodPost, "/api/v1/broadcasts/test", map[string]any{"lang": "en", "text": "x"})
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/broadcasts/test", map[string]any{"lang": "en", "text": "x"}); code != 429 {
		t.Fatalf("sixth test send: %d", code)
	}

	// Edits need the version read; a stale one is refused.
	upd := map[string]any{"segment": "active", "language": "", "texts": map[string]string{"fa": "الف", "en": "A"}, "version": version}
	if code, _ := b.do(http.MethodPut, "/api/v1/broadcasts/"+id, upd); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/broadcasts/"+id, upd); code != 409 {
		t.Fatalf("stale update: %d %v", code, out)
	}
	upd["segment"], upd["version"] = "all", float64(2)
	b.do(http.MethodPut, "/api/v1/broadcasts/"+id, upd)

	// Send: scheduled now, started by the worker, paused, resumed, cancelled.
	// It needs the version read, so nobody sends what they never saw.
	if code, out := b.do(http.MethodPost, "/api/v1/broadcasts/"+id+"/send", map[string]any{"at": nil}); code != 400 || out["field"] != "version" {
		t.Fatalf("send without a version: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/broadcasts/"+id+"/send", map[string]any{"at": nil, "version": 2}); code != 409 || out["error"] != "state" {
		t.Fatalf("send of a stale version: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/broadcasts/"+id+"/send", map[string]any{"at": 1, "version": 3}); code != 400 || out["field"] != "at" {
		t.Fatalf("send in the past: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/broadcasts/"+id+"/send", map[string]any{"at": nil, "version": 3}); code != 200 || out["item"].(map[string]any)["status"] != "scheduled" {
		t.Fatalf("send: %d %v", code, out)
	}
	if err := f.dom.NewBroadcastWorker(nil).Step(ctx); err != nil {
		t.Fatal(err)
	}
	code, out = b.do(http.MethodGet, "/api/v1/broadcasts/"+id, nil)
	counts := out["item"].(map[string]any)["counts"].(map[string]any)
	if code != 200 || out["item"].(map[string]any)["status"] != "running" || counts["total"] != float64(4) || counts["pending"] != float64(4) {
		t.Fatalf("running: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodDelete, "/api/v1/broadcasts/"+id, nil); code != 409 {
		t.Fatalf("deleted a running broadcast: %d", code)
	}
	for _, action := range []string{"pause", "resume", "cancel"} {
		if code, out := b.do(http.MethodPost, "/api/v1/broadcasts/"+id+"/"+action, map[string]any{}); code != 200 {
			t.Fatalf("%s: %d %v", action, code, out)
		}
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/broadcasts/"+id+"/resume", map[string]any{}); code != 409 {
		t.Fatalf("resumed a cancelled broadcast: %d", code)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/broadcasts/"+id+"/recipients?status=failed", nil); code != 200 || out["total"] != float64(0) {
		t.Fatalf("recipients: %d %v", code, out)
	}
	if e := lastAudit(t, b, "entity_id="+id+"&action=broadcast.send"); e["actor"] == nil {
		t.Fatalf("send audit: %v", e)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/broadcasts?status=cancelled", nil); out["total"] != float64(1) {
		t.Fatalf("list: %v", out)
	}
}
