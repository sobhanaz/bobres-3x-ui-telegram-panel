package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func TestLinkSecretReadsALink(t *testing.T) {
	for in, want := range map[string]string{
		"https://sub.example.com:2096/sub/abcd1234efgh5678":                 "abcd1234efgh5678",
		"  https://shop.example.com/sub/0123456789abcdef0123456789abcdef/ ": "0123456789abcdef0123456789abcdef",
		"https://sub.example.com/sub/abcd1234efgh5678?name=x#frag":          "abcd1234efgh5678",
		"sub.example.com/sub/abcd1234efgh5678":                              "abcd1234efgh5678",
		"/sub/abcd1234efgh5678":                                             "abcd1234efgh5678",
		"abcd1234efgh5678":                                                  "abcd1234efgh5678",
		"abcd1234efgh5678?x=1":                                              "abcd1234efgh5678",
	} {
		if got, err := LinkSecret(in); err != nil || got != want {
			t.Errorf("LinkSecret(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "short", "https://sub.example.com/sub/", "vless://uuid@host:443?security=tls#name", "has space in it 12345", "نماینده", "https://x/sub/" + string(make([]byte, 70))} {
		if _, err := LinkSecret(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("LinkSecret(%q): %v", bad, err)
		}
	}
}

// A service is found by its subscription link or its page's address; with the link's secret anyone may renew it (paying themselves), but
// not add traffic to it, and without it a service that is not theirs is refused; a deleted service is not found.
func TestServiceFoundByLinkCanBeRenewedByItsHolder(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	owner, holder, stranger := seedUser(t, e.s, 9941), seedUser(t, e.s, 9942), seedUser(t, e.s, 9943)
	fund(t, e.s, owner.ID, e.plan.Price)
	fund(t, e.s, holder.ID, e.plan.Price*3)
	fund(t, e.s, stranger.ID, e.plan.Price)
	o, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: owner.ID, PlanID: e.plan.ID, IdempotencyKey: "fl-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PayOrderWithWallet(ctx, o.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := e.svc.NewProvisionWorker(&fakeProvisioner{}, nil).ProvisionNext(ctx); err != nil || !worked {
		t.Fatalf("provisioning: %v %v", worked, err)
	}
	sub, err := e.s.SubscriptionByOrder(ctx, e.s.Conn(), o.ID)
	if err != nil || sub.SubID == nil || sub.PageToken == "" {
		t.Fatalf("subscription %+v %v", sub, err)
	}

	// Found by the subscription id, in a link or alone, and by the page token.
	for _, in := range []string{"https://sub.example.com:2096/sub/" + *sub.SubID, *sub.SubID, "https://shop.example.com/sub/" + sub.PageToken} {
		got, secret, err := e.svc.FindServiceByLink(ctx, holder.ID, in)
		if err != nil || got.ID != sub.ID || secret == "" {
			t.Fatalf("find %q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"https://sub.example.com/sub/doesnotexist1234", "https://sub.example.com/sub/" + *sub.SubID + "x"} {
		if _, _, err := e.svc.FindServiceByLink(ctx, holder.ID, in); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("find %q: %v", in, err)
		}
	}
	if _, _, err := e.svc.FindServiceByLink(ctx, holder.ID, "not a link"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a non-link: %v", err)
	}

	// Without the secret, or with the wrong one, the holder's renewal of it is refused; with it, it is made and paid by the holder.
	renew := func(user *store.User, key, secret, typ string) (*store.Order, error) {
		return e.svc.CreateOrder(ctx, CreateOrderParams{UserID: user.ID, PlanID: e.plan.ID, Type: typ, SubscriptionID: sub.ID, IdempotencyKey: key, LinkSecret: secret})
	}
	for name, secret := range map[string]string{"none": "", "wrong": "abcdefgh12345678", "a prefix": (*sub.SubID)[:8]} {
		if _, err := renew(stranger, "fl-x-"+name, secret, OrderRenew); !errors.Is(err, ErrForbidden) {
			t.Errorf("a renewal with %s secret: %v", name, err)
		}
	}
	if _, err := renew(holder, "fl-top", *sub.SubID, OrderTrafficTopup); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrPlanUnavailable) {
		t.Errorf("a traffic top-up with the secret: %v", err)
	}
	for i, secret := range []string{*sub.SubID, sub.PageToken} {
		ro, err := renew(holder, "fl-ok-"+string(rune('a'+i)), secret, OrderRenew)
		if err != nil {
			t.Fatalf("a renewal with the secret: %v", err)
		}
		if ro.UserID != holder.ID || ro.SubscriptionID == nil || *ro.SubscriptionID != sub.ID {
			t.Fatalf("the order %+v", ro)
		}
		if _, err := e.svc.PayOrderWithWallet(ctx, ro.ID, holder.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := walletBalance(t, e.s, holder.ID); got != e.plan.Price {
		t.Fatalf("the holder paid %d, balance %d", e.plan.Price*2, got)
	}
	if got := walletBalance(t, e.s, owner.ID); got != 0 {
		t.Fatalf("the owner's balance moved: %d", got)
	}

	// A quote needs the secret too; a deleted service is not found.
	if _, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: holder.ID, PlanID: e.plan.ID, Type: OrderRenew, SubscriptionID: sub.ID}); !errors.Is(err, ErrForbidden) {
		t.Errorf("a quote without the secret: %v", err)
	}
	if q, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: holder.ID, PlanID: e.plan.ID, Type: OrderRenew, SubscriptionID: sub.ID, LinkSecret: *sub.SubID}); err != nil || q.Total != e.plan.Price {
		t.Errorf("a quote with it: %+v %v", q, err)
	}
	if _, err := e.s.DB().Exec(ctx, `UPDATE core.subscriptions SET status = 'deleted' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.FindServiceByLink(ctx, holder.ID, *sub.SubID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted service: %v", err)
	}
}

func walletBalance(t *testing.T, s *store.Store, userID string) int64 {
	t.Helper()
	w, err := s.GetWallet(context.Background(), s.Conn(), userID, "IRT")
	if err != nil {
		t.Fatal(err)
	}
	return w.Balance
}
