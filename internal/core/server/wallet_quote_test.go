package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

// The bot pays a plan priced in USDT from the Toman wallet in two calls: QuoteWalletPayment shows the price, PayOrderWithWallet carries the quote.
func TestWalletQuoteOverGRPC(t *testing.T) {
	var (
		mu  sync.Mutex
		now = time.Now()
	)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	f := newFixtureWith(t, func(s *Server) {
		env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			t.Fatal(err)
		}
		s.dom = domain.New(s.st, domain.Config{Secrets: env, Now: clock})
	})
	c := f.client(t, botToken)
	ctx := context.Background()
	setRate := func(rate string) {
		t.Helper()
		raw, _ := json.Marshal(rate)
		if err := f.st.SetSetting(ctx, f.st.Conn(), "payments.usdt_rate", raw); err != nil {
			t.Fatal(err)
		}
	}
	days := int32(30)
	plan := &store.Plan{NameI18n: map[string]string{"en": "Gold USDT"}, Kind: "time", DurationDays: &days, Price: 4_000_000, Currency: "USDT", Enabled: true}
	if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	buyer, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 71})
	other, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 72})
	err := f.st.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := f.st.Credit(ctx, tx, buyer.GetId(), "IRT", 4_000_000, &store.LedgerEntry{Kind: "topup", IdempotencyKey: "fund-71"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	order := func(key string) *corev1.Order {
		t.Helper()
		o, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, IdempotencyKey: key})
		if err != nil || o.GetAmount().GetCurrency() != "USDT" || o.GetAmount().GetAmount() != 4_000_000 || o.GetRate() != 0 || o.GetListPrice() != nil {
			t.Fatalf("order: %v %+v", err, o)
		}
		return o
	}
	failedWith := func(err error, code codes.Code, text string) {
		t.Helper()
		if status.Code(err) != code || !strings.Contains(strings.ToLower(err.Error()), text) {
			t.Fatalf("want %s %q, got %v", code, text, err)
		}
	}

	// No rate: nothing is quoted, and the bot can tell why.
	o1 := order("grpc-q-1")
	_, err = c.QuoteWalletPayment(ctx, &corev1.QuoteWalletPaymentRequest{OrderId: o1.GetId(), UserId: buyer.GetId()})
	failedWith(err, codes.FailedPrecondition, "usdt rate is not known")

	// With a rate: the USDT price, the rate, the Toman and the wallet.
	setRate("300000")
	q, err := c.QuoteWalletPayment(ctx, &corev1.QuoteWalletPaymentRequest{OrderId: o1.GetId(), UserId: buyer.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if q.GetOrderId() != o1.GetId() || q.GetListPrice().GetAmount() != 4_000_000 || q.GetListPrice().GetCurrency() != "USDT" || q.GetRate() != 300_000 ||
		q.GetPrice().GetAmount() != 1_200_000 || q.GetPrice().GetCurrency() != "IRT" || q.GetBalance().GetAmount() != 4_000_000 || q.GetBalance().GetCurrency() != "IRT" ||
		q.GetCreditLimit() != 0 || !q.GetEnough() || q.GetToken() == "" || q.GetExpiresAt() != clock().Add(domain.WalletQuoteTTL).Unix() {
		t.Fatalf("quote: %+v", q)
	}
	// Someone else's order is not quoted or paid.
	_, err = c.QuoteWalletPayment(ctx, &corev1.QuoteWalletPaymentRequest{OrderId: o1.GetId(), UserId: other.GetId()})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("another user's quote: %v", err)
	}
	_, err = c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o1.GetId(), UserId: other.GetId(), Quote: q.GetToken()})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("another user's payment: %v", err)
	}
	// A quote that is not one, and no quote at all (the old way: the USDT wallet, which is empty).
	_, err = c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o1.GetId(), UserId: buyer.GetId(), Quote: "made.up"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("a made-up quote: %v", err)
	}
	_, err = c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o1.GetId(), UserId: buyer.GetId()})
	failedWith(err, codes.FailedPrecondition, "insufficient")

	// The rate moves; the quote that was shown is what is charged, in Toman, and the order says what it was listed at.
	setRate("330000")
	paid, err := c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o1.GetId(), UserId: buyer.GetId(), Quote: q.GetToken()})
	if err != nil {
		t.Fatal(err)
	}
	if paid.GetStatus() != "paid" || paid.GetAmount().GetAmount() != 1_200_000 || paid.GetAmount().GetCurrency() != "IRT" ||
		paid.GetListPrice().GetAmount() != 4_000_000 || paid.GetListPrice().GetCurrency() != "USDT" || paid.GetRate() != 300_000 {
		t.Fatalf("paid: %+v", paid)
	}
	if w, _ := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: buyer.GetId(), Currency: "IRT"}); w.GetBalance() != 2_800_000 {
		t.Fatalf("the wallet: %+v", w)
	}
	// A repeat of the payment (a double tap) charges nothing more.
	again, err := c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o1.GetId(), UserId: buyer.GetId(), Quote: q.GetToken()})
	if err != nil || again.GetStatus() != "paid" || again.GetAmount().GetAmount() != 1_200_000 {
		t.Fatalf("repeat: %v %+v", err, again)
	}
	if w, _ := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: buyer.GetId(), Currency: "IRT"}); w.GetBalance() != 2_800_000 {
		t.Fatalf("the wallet after a repeat: %+v", w)
	}
	// The paid order's quote is refused.
	_, err = c.QuoteWalletPayment(ctx, &corev1.QuoteWalletPaymentRequest{OrderId: o1.GetId(), UserId: buyer.GetId()})
	failedWith(err, codes.FailedPrecondition, "not payable")

	// A quote that ran out is refused with words the bot recognises, and a new quote at the new rate is paid.
	o2 := order("grpc-q-2")
	q2, err := c.QuoteWalletPayment(ctx, &corev1.QuoteWalletPaymentRequest{OrderId: o2.GetId(), UserId: buyer.GetId()})
	if err != nil || q2.GetRate() != 330_000 || q2.GetPrice().GetAmount() != 1_320_000 {
		t.Fatalf("second quote: %v %+v", err, q2)
	}
	mu.Lock()
	now = now.Add(domain.WalletQuoteTTL + time.Second)
	mu.Unlock()
	_, err = c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o2.GetId(), UserId: buyer.GetId(), Quote: q2.GetToken()})
	failedWith(err, codes.FailedPrecondition, "quote expired")
	q3, err := c.QuoteWalletPayment(ctx, &corev1.QuoteWalletPaymentRequest{OrderId: o2.GetId(), UserId: buyer.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o2.GetId(), UserId: buyer.GetId(), Quote: q3.GetToken()}); err != nil {
		t.Fatalf("pay at the new quote: %v", err)
	}
	if w, _ := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: buyer.GetId(), Currency: "IRT"}); w.GetBalance() != 2_800_000-1_320_000 {
		t.Fatalf("the wallet after the second: %+v", w)
	}
}
