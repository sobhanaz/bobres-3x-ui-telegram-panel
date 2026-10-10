package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/broadcast"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// A broadcast made in core reaches its audience through the bot's worker:
// each in their language with the link button; a user who blocked the bot
// is flagged (and left out next time); banned users get nothing.
func TestBroadcastReachesItsAudience(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	ctx := context.Background()
	en := w.customer(890, "eve")
	fa := w.person(891, "farid")
	fa.text("/start")
	fa.press("lang:fa")
	gone := w.customer(892, "gone")
	w.tg.setBlocked(892, true)
	banned := w.customer(893, "bad")
	if err := w.stack.Store.SetUserRole(ctx, w.stack.Store.Conn(), w.storeUser(893).ID, "user", "banned"); err != nil {
		t.Fatal(err)
	}

	d := w.dom()
	owner := w.storeUser(ownerID)
	send := func() string {
		t.Helper()
		b, err := d.CreateBroadcast(ctx, owner, coredomain.BroadcastInput{Segment: "all",
			Texts:     map[string]string{"fa": "<b>تخفیف</b> ویژه", "en": "<b>Sale</b> today"},
			ButtonURL: "https://example.com/sale", ButtonLabels: map[string]string{"fa": "مشاهده", "en": "See it"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.SendBroadcast(ctx, owner, b.ID, nil); err != nil {
			t.Fatal(err)
		}
		cw := d.NewBroadcastWorker(nil)
		if err := cw.Step(ctx); err != nil { // starts it: the audience is put in line
			t.Fatal(err)
		}
		bw := broadcast.New(w.stack.Core, w.tg, w.h.Settings(), &tg.Flood{}, &tg.Meter{}, nil)
		for range 10 {
			n, err := bw.Step(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
		}
		if err := cw.Step(ctx); err != nil { // nobody left: done
			t.Fatal(err)
		}
		return b.ID
	}

	id := send()
	b, err := w.stack.Store.GetBroadcast(ctx, w.stack.Store.Conn(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	// The owner, eve, farid and gone; not the banned user.
	if b.Status != "done" || b.Total != 4 || b.Sent != 3 || b.Blocked != 1 || b.Failed+b.Unknown != 0 {
		t.Fatalf("broadcast: %+v", b)
	}
	en.sees("<b>Sale</b> today")
	if l := en.link("See it"); l != "https://example.com/sale" {
		t.Fatalf("en button: %q", l)
	}
	fa.sees("<b>تخفیف</b> ویژه")
	if l := fa.link("مشاهده"); l != "https://example.com/sale" {
		t.Fatalf("fa button: %q", l)
	}
	if banned.count("Sale") != 0 || gone.count("Sale") != 0 {
		t.Fatal("a banned or blocked user got the broadcast")
	}
	if !w.blockedAt(892) {
		t.Fatal("the blocked user was not flagged")
	}

	id = send()
	if b, _ := w.stack.Store.GetBroadcast(ctx, w.stack.Store.Conn(), id, false); b.Total != 3 || b.Sent != 3 {
		t.Fatalf("the next broadcast: %+v", b)
	}
	if n := en.count("<b>Sale</b> today"); n != 2 {
		t.Fatalf("eve got %d broadcasts", n)
	}
	if strings.Contains(strings.Join(en.last().labels(), "|"), "مشاهده") {
		t.Fatal("eve got the Persian label")
	}
}
