package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// pageCore is core as the bot sees it with the public URL set: every
// service it returns carries a page address (the test stack has no public URL).
type pageCore struct {
	corev1.CoreServiceClient
	url string
}

func (p pageCore) ListSubscriptions(ctx context.Context, in *corev1.ListSubscriptionsRequest, o ...grpc.CallOption) (*corev1.ListSubscriptionsResponse, error) {
	res, err := p.CoreServiceClient.ListSubscriptions(ctx, in, o...)
	if err == nil && p.url != "" {
		for _, s := range res.GetSubscriptions() {
			s.PageUrl = p.url
		}
	}
	return res, err
}

// detail is the service's page as the customer last saw it (the newest
// message with the traffic line: a QR photo may have come after it).
func detail(u *person) *msg {
	all := u.w.tg.all(u.id)
	for i := len(all) - 1; i >= 0; i-- {
		if (strings.Contains(all[i].text, "50 GB") || strings.Contains(all[i].text, "۵۰")) && all[i].kb != nil {
			return all[i]
		}
	}
	return nil
}

func urlButtons(m *msg) []tg.Button {
	var out []tg.Button
	if m == nil || m.kb == nil {
		return nil
	}
	for _, row := range m.kb.InlineKeyboard {
		for _, b := range row {
			if b.URL != "" {
				out = append(out, b)
			}
		}
	}
	return out
}

func TestServicePageHasAnOpenPageButton(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.person(721, "pagey")
	u.text("/start")
	u.press("lang:en")
	w.fund(721, 1_000_000)
	u.press("buy")
	u.press("plan:")
	u.press("pay:w:")
	u.eventuallySees("Your service is ready")

	// Core without a public URL: no page, no button.
	u.press("home")
	u.press("subs")
	u.press("sub:")
	u.sees("of 50 GB")
	if detail(u) == nil {
		t.Fatal("the service page was not shown")
	}
	if got := urlButtons(detail(u)); len(got) != 0 {
		t.Fatalf("a page button without a page address: %+v", got)
	}

	// With a page address: one URL button, in the customer's language.
	w.h.core = pageCore{CoreServiceClient: w.stack.Core, url: "https://shop.example.com/sub/0123456789abcdef0123456789abcdef"}
	u.press("home")
	u.press("subs")
	u.press("sub:")
	got := urlButtons(detail(u))
	if len(got) != 1 || got[0].URL != "https://shop.example.com/sub/0123456789abcdef0123456789abcdef?l=en" || got[0].CallbackData != "" {
		t.Fatalf("page button: %+v", got)
	}
	if !strings.Contains(strings.ToLower(got[0].Text), "page") {
		t.Errorf("button text %q", got[0].Text)
	}

	// Persian customers get the Persian page and Persian text.
	u.press("home")
	u.press("lang")
	u.press("lang:fa")
	u.press("subs")
	u.press("sub:")
	got = urlButtons(detail(u))
	if len(got) != 1 || !strings.HasSuffix(got[0].URL, "?l=fa") || strings.Contains(got[0].Text, "page") {
		t.Fatalf("persian page button: %+v", got)
	}
}

func TestPageLink(t *testing.T) {
	for _, c := range []struct{ u, lang, want string }{
		{"https://a.test/sub/x", "en", "https://a.test/sub/x?l=en"},
		{"https://a.test/sub/x", "fa", "https://a.test/sub/x?l=fa"},
		{"https://a.test/sub/x?l=en", "fa", "https://a.test/sub/x?l=en"},
	} {
		if got := pageLink(c.u, c.lang); got != c.want {
			t.Errorf("pageLink(%q, %q) = %q, want %q", c.u, c.lang, got, c.want)
		}
	}
}

// linksCore is core with a fixed answer for a service's links.
type linksCore struct {
	corev1.CoreServiceClient
	links *corev1.SubscriptionLinks
	err   error
}

func (l linksCore) GetSubscriptionLinks(context.Context, *corev1.GetSubscriptionLinksRequest, ...grpc.CallOption) (*corev1.SubscriptionLinks, error) {
	return l.links, l.err
}

func provisioned(t *testing.T) eventbus.Message {
	t.Helper()
	p, err := json.Marshal(events.SubscriptionProvisionedEvent{SubscriptionID: "s1", UserID: "u1", TelegramID: 731,
		SubscriptionLink: "https://sub.example.com/x"})
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Message{Topic: events.SubscriptionProvisioned, Payload: p}
}

// The "service ready" message carries an Open page button when core names a
// page, and still goes out, without it, when core has none or cannot say.
func TestServiceReadyMessageHasThePageButton(t *testing.T) {
	const page = "https://shop.example.com/sub/0123456789abcdef0123456789abcdef"
	cases := []struct {
		name  string
		core  linksCore
		wantU string
		photo bool
	}{
		{"with a page", linksCore{links: &corev1.SubscriptionLinks{PageUrl: page, QrPng: []byte("png")}}, page + "?l=" + i18n.Languages[0], true},
		{"no page", linksCore{links: &corev1.SubscriptionLinks{QrPng: []byte("png")}}, "", true},
		{"core fails", linksCore{err: errors.New("down")}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			c.core.CoreServiceClient = w.stack.Core
			n := notify.New(c.core, w.tg, w.h.cat, w.h.Settings(), ownerID, nil)
			if err := n.Handle(context.Background(), provisioned(t)); err != nil {
				t.Fatal(err)
			}
			var ready *msg
			var qr bool
			for _, m := range w.tg.all(731) {
				if m.photo {
					qr = true
				} else if strings.Contains(m.text, "https://sub.example.com/x") {
					ready = m
				}
			}
			if ready == nil {
				t.Fatalf("no ready message: %+v", w.tg.all(731))
			}
			got := urlButtons(ready)
			if c.wantU == "" && len(got) != 0 || c.wantU != "" && (len(got) != 1 || got[0].URL != c.wantU) {
				t.Fatalf("page buttons: %+v, want %q", got, c.wantU)
			}
			if qr != c.photo {
				t.Fatalf("QR sent: %v, want %v", qr, c.photo)
			}
		})
	}
}
