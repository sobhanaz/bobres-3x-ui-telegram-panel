package handler

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonaddr"
)

// The TON screen shows the exact amount with all 9 decimals, the address and the comment, each copyable, the
// price and the time left; it is shown only from complete, well-formed details, and never once the time ran out.
func TestTONScreen(t *testing.T) {
	ftg := newFakeTG()
	h := New(nil, ftg, state.NewMemory(), i18n.MustLoad(), nil, Config{}, nil)
	addr := tonaddr.Address{Hash: [32]byte{0x11, 0x22}}.Canonical()
	expires := strconv.FormatInt(time.Now().Add(30*time.Minute).Unix(), 10)
	good := func() *corev1.PaymentIntentRef {
		return &corev1.PaymentIntentRef{Id: "intent-1", Status: "pending", Provider: tonAuto, OrderId: "order-1",
			Amount:        &commonv1.Money{Amount: 150_000, Currency: "IRT"},
			GatewayAmount: &commonv1.Money{Amount: 777_726_247, Currency: "TON"},
			Details:       map[string]string{"pay_address": addr, "network": "TON", "memo": "BOB-1A2B3C4D", "expires_at": expires}}
	}
	show := func(in *corev1.PaymentIntentRef, lang string) string {
		h.showTON(&req{ctx: context.Background(), h: h, from: &tg.User{ID: 7}, chatID: 7, lang: lang}, in)
		return ftg.last(7).text
	}
	got := show(good(), "en")
	for _, want := range []string{"<code>0.777726247</code> TON", "<code>" + addr + "</code>", "<code>BOB-1A2B3C4D</code>", "150,000"} {
		if !strings.Contains(got, want) {
			t.Fatalf("good details: %q lacks %q", got, want)
		}
	}
	// Persian: the amount to copy stays in Latin digits; the price is in Toman.
	if fa := show(good(), "fa"); !strings.Contains(fa, "<code>0.777726247</code>") || !strings.Contains(fa, "تومان") ||
		!strings.Contains(fa, "<code>BOB-1A2B3C4D</code>") {
		t.Fatalf("fa: %q", fa)
	}
	for name, spoil := range map[string]func(*corev1.PaymentIntentRef){
		"amount in USDT":  func(in *corev1.PaymentIntentRef) { in.GatewayAmount.Currency = "USDT" },
		"no amount":       func(in *corev1.PaymentIntentRef) { in.GatewayAmount = nil },
		"no address":      func(in *corev1.PaymentIntentRef) { delete(in.Details, "pay_address") },
		"a TRON address":  func(in *corev1.PaymentIntentRef) { in.Details["pay_address"] = "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r" },
		"markup":          func(in *corev1.PaymentIntentRef) { in.Details["pay_address"] = "<b>" + addr[3:] },
		"no memo":         func(in *corev1.PaymentIntentRef) { delete(in.Details, "memo") },
		"a markup memo":   func(in *corev1.PaymentIntentRef) { in.Details["memo"] = "BOB-<b>1234" },
		"another network": func(in *corev1.PaymentIntentRef) { in.Details["network"] = "TRC20" },
		"no window":       func(in *corev1.PaymentIntentRef) { delete(in.Details, "expires_at") },
	} {
		in := good()
		spoil(in)
		if got := show(in, "en"); got != h.cat.T("en", "pay.not_configured") {
			t.Errorf("%s: %q", name, got)
		}
	}
	late := good()
	late.Details["expires_at"] = strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10)
	if got := show(late, "en"); !strings.Contains(got, "The time to pay ran out") || strings.Contains(got, "<code>") {
		t.Fatalf("past its window: %q", got)
	}
}
