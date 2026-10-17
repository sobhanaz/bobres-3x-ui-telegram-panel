package server

import (
	"context"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// linksProv answers a service's links and nothing else.
type linksProv struct{ Provisioner }

func (linksProv) GetLinks(context.Context, string) (domain.Links, error) {
	return domain.Links{SubscriptionLink: "https://sub.example.com/x", QRPNG: []byte("png")}, nil
}

// GetSubscriptionLinks carries the page address too, for the "service ready"
// message: only with a public URL, and never for someone else's service.
func TestSubscriptionLinksHavePageURL(t *testing.T) {
	for _, withURL := range []bool{true, false} {
		f := newFixtureWith(t, func(s *Server) {
			s.SetProvisioner(linksProv{})
			if withURL {
				s.SetPublicURL("https://shop.example.com")
			}
		})
		c := f.client(t, botToken)
		ctx := context.Background()
		plan := &store.Plan{NameI18n: map[string]string{"en": "Month"}, Kind: "time", Price: 1000, Currency: "IRT", Enabled: true}
		if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
			t.Fatal(err)
		}
		u, err := f.st.UpsertUser(ctx, f.st.Conn(), 7310, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		other, err := f.st.UpsertUser(ctx, f.st.Conn(), 7311, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		o, _, err := f.st.CreateOrder(ctx, f.st.Conn(), &store.Order{UserID: u.ID, PlanID: plan.ID, Type: "new", Status: "active",
			Amount: 1, Currency: "IRT", IdempotencyKey: "pl-1"})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := f.st.CreateSubscription(ctx, f.st.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: "pl-1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.DB().Exec(ctx, `UPDATE core.subscriptions SET status = 'active' WHERE id = $1`, sub.ID); err != nil {
			t.Fatal(err)
		}
		l, err := c.GetSubscriptionLinks(ctx, &corev1.GetSubscriptionLinksRequest{UserId: u.ID, SubscriptionId: sub.ID})
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if withURL {
			want = "https://shop.example.com/sub/" + sub.PageToken
		}
		if l.GetPageUrl() != want || l.GetSubscriptionLink() == "" {
			t.Errorf("withURL=%v: page %q, want %q", withURL, l.GetPageUrl(), want)
		}
		if _, err := c.GetSubscriptionLinks(ctx, &corev1.GetSubscriptionLinksRequest{UserId: other.ID, SubscriptionId: sub.ID}); err == nil {
			t.Error("someone else's service gave links")
		}
	}
}

// page_url is the page address of a service: only while the store has a
// public URL, the page is on, and the service is neither pending nor deleted.
func TestSubscriptionPageURL(t *testing.T) {
	for _, withURL := range []bool{true, false} {
		f := newFixtureWith(t, func(s *Server) {
			if withURL {
				s.SetPublicURL("https://shop.example.com")
			}
		})
		c := f.client(t, botToken)
		ctx := context.Background()
		plan := &store.Plan{NameI18n: map[string]string{"en": "Month"}, Kind: "time", Price: 1000, Currency: "IRT", Enabled: true}
		if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
			t.Fatal(err)
		}
		u, err := f.st.UpsertUser(ctx, f.st.Conn(), 7300, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		tokens := map[string]string{}
		for _, status := range []string{"active", "pending", "deleted"} {
			o, _, err := f.st.CreateOrder(ctx, f.st.Conn(), &store.Order{UserID: u.ID, PlanID: plan.ID, Type: "new", Status: "active",
				Amount: 1, Currency: "IRT", IdempotencyKey: "pg-" + status})
			if err != nil {
				t.Fatal(err)
			}
			sub, err := f.st.CreateSubscription(ctx, f.st.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: "pg-" + status})
			if err != nil {
				t.Fatal(err)
			}
			if status != "pending" {
				if _, err := f.st.DB().Exec(ctx, `UPDATE core.subscriptions SET status = $2 WHERE id = $1`, sub.ID, status); err != nil {
					t.Fatal(err)
				}
			}
			tokens[sub.ID] = sub.PageToken
		}
		list := func() map[string]string {
			t.Helper()
			out := map[string]string{}
			res, err := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: u.ID})
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range res.GetSubscriptions() {
				out[s.GetStatus()] = s.GetPageUrl()
			}
			return out
		}
		got := list()
		if !withURL {
			for st, url := range got {
				if url != "" {
					t.Errorf("no public URL, but %s has %q", st, url)
				}
			}
			continue
		}
		var active string
		for id, tok := range tokens {
			sub, err := f.st.GetSubscription(ctx, f.st.Conn(), id)
			if err != nil {
				t.Fatal(err)
			}
			if sub.Status == "active" {
				active = tok
			}
		}
		if want := "https://shop.example.com/sub/" + active; got["active"] != want {
			t.Errorf("active: %q, want %q", got["active"], want)
		}
		if got["pending"] != "" || got["deleted"] != "" {
			t.Errorf("pending/deleted carry a page: %v", got)
		}
		if _, err := f.st.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('branding.sub_page.enabled', '"false"')`); err != nil {
			t.Fatal(err)
		}
		if got := list(); got["active"] != "" {
			t.Errorf("page off: %q", got["active"])
		}
		if strings.Contains(active, "-") || len(active) != 32 {
			t.Errorf("token %q", active)
		}
	}
}
