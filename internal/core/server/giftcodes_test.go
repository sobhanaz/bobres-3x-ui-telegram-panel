package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type giftFixture struct {
	*fixture
	c     corev1.CoreServiceClient
	dom   *domain.Service
	admin *store.User
	n     int
}

func newGiftFixture(t *testing.T) *giftFixture {
	t.Helper()
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	var dom *domain.Service
	f := newFixtureWith(t, func(srv *Server) {
		dom = domain.New(srv.st, domain.Config{Secrets: env})
		srv.dom = dom
	})
	ctx := context.Background()
	if _, err := f.st.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = 'gift_codes.enabled'`); err != nil {
		t.Fatal(err)
	}
	admin, err := f.st.UpsertUser(ctx, f.st.Conn(), 7700, "boss", "en", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetUserRole(ctx, f.st.Conn(), admin.ID, "owner", "active"); err != nil {
		t.Fatal(err)
	}
	return &giftFixture{fixture: f, c: f.client(t, botToken), dom: dom, admin: admin}
}

func (g *giftFixture) make(t *testing.T, amount int64, uses int) domain.NewGiftCode {
	t.Helper()
	codes, err := g.dom.CreateGiftCodes(context.Background(), g.admin, domain.GiftCodeSpec{Amount: amount, Currency: "IRT", MaxRedemptions: uses})
	if err != nil {
		t.Fatal(err)
	}
	return codes[0]
}

func (g *giftFixture) code(t *testing.T, amount int64, uses int) string {
	t.Helper()
	return g.make(t, amount, uses).Code
}

func (g *giftFixture) customer(t *testing.T, tg int64) string {
	t.Helper()
	u, err := g.c.UpsertUser(context.Background(), &corev1.UpsertUserRequest{TelegramId: tg, Language: "fa"})
	if err != nil {
		t.Fatal(err)
	}
	return u.GetId()
}

func (g *giftFixture) redeem(t *testing.T, user, code, key string) *corev1.RedeemGiftCodeResponse {
	t.Helper()
	g.n++
	if key == "" {
		key = fmt.Sprintf("k%d", g.n)
	}
	r, err := g.c.RedeemGiftCode(context.Background(), &corev1.RedeemGiftCodeRequest{UserId: user, Code: code, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("RedeemGiftCode: %v", err)
	}
	return r
}

func TestRedeemGiftCodeOverGRPC(t *testing.T) {
	g := newGiftFixture(t)
	ctx := context.Background()
	user := g.customer(t, 7701)
	code := g.code(t, 125_000, 2)

	// What the customer typed: capitals, small letters and no dashes are the same code.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	r := g.redeem(t, user, typed, "msg-1")
	if r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_REDEEMED || r.GetCredited().GetAmount() != 125_000 ||
		r.GetCredited().GetCurrency() != "IRT" || r.GetBalance().GetAmount() != 125_000 || r.GetReplayed() || r.GetRetryAfterSeconds() != 0 {
		t.Fatalf("redeem: %+v", r)
	}
	w, err := g.c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: user, Currency: "IRT"})
	if err != nil || w.GetBalance() != 125_000 {
		t.Fatalf("wallet: %v %+v", err, w)
	}
	led, err := g.c.ListLedgerEntries(ctx, &corev1.ListLedgerEntriesRequest{UserId: user})
	if err != nil || len(led.GetEntries()) != 1 || led.GetEntries()[0].GetKind() != "gift_code" || led.GetEntries()[0].GetAmount() != 125_000 {
		t.Fatalf("ledger: %v %+v", err, led)
	}

	// The same request again is answered again, with nothing credited.
	again := g.redeem(t, user, typed, "msg-1")
	if again.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_REDEEMED || !again.GetReplayed() || again.GetBalance().GetAmount() != 125_000 {
		t.Fatalf("replay: %+v", again)
	}
	if w, _ := g.c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: user, Currency: "IRT"}); w.GetBalance() != 125_000 {
		t.Fatalf("a repeated request credited: %d", w.GetBalance())
	}
	// A new request by the same customer for the same code: they have it.
	if r := g.redeem(t, user, code, ""); r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_ALREADY_REDEEMED || r.GetCredited() != nil {
		t.Fatalf("again: %+v", r)
	}
	// Another customer takes the last use; a third is told it is used up.
	if r := g.redeem(t, g.customer(t, 7702), code, ""); r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_REDEEMED {
		t.Fatalf("second customer: %+v", r)
	}
	if r := g.redeem(t, g.customer(t, 7703), code, ""); r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_USED_UP {
		t.Fatalf("third customer: %+v", r)
	}
}

func TestRedeemGiftCodeWrongCodesAreOneOutcomeAndLockAfterFive(t *testing.T) {
	g := newGiftFixture(t)
	ctx := context.Background()
	off := g.make(t, 10, 5)
	if _, err := g.dom.SetGiftCodeEnabled(ctx, g.admin, off.ID, false); err != nil {
		t.Fatal(err)
	}
	expired := g.make(t, 10, 5)
	if _, err := g.st.DB().Exec(ctx, `UPDATE core.gift_codes SET expires_at = now() - interval '1 minute' WHERE id = $1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	for i, code := range []string{"ABCD-EFGH-JKMN", off.Code, expired.Code, "", "not a code"} {
		r := g.redeem(t, g.customer(t, int64(7710+i)), code, "")
		if r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_INVALID || r.GetCredited() != nil || r.GetBalance() != nil || r.GetRetryAfterSeconds() != 0 {
			t.Errorf("wrong code %d (%q): %+v", i, code, r)
		}
	}
	// One customer sending five wrong codes: the fifth closes it, with how long.
	user := g.customer(t, 7720)
	for i := 1; i <= 4; i++ {
		if r := g.redeem(t, user, "WRONG-"+strings.Repeat("X", i+3), ""); r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_INVALID {
			t.Fatalf("wrong code %d: %+v", i, r)
		}
	}
	r := g.redeem(t, user, "WRONG-FIVE-XXXX", "")
	if r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_LOCKED || r.GetRetryAfterSeconds() != 3600 {
		t.Fatalf("the fifth wrong code: %+v", r)
	}
	// Locked, even the right code waits.
	live := g.code(t, 10, 5)
	if r := g.redeem(t, user, live, ""); r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_LOCKED || r.GetRetryAfterSeconds() <= 0 || r.GetRetryAfterSeconds() > 3600 {
		t.Fatalf("locked: %+v", r)
	}
	if w, _ := g.c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: user, Currency: "IRT"}); w.GetBalance() != 0 {
		t.Fatalf("a locked customer was credited: %d", w.GetBalance())
	}
}

func TestRedeemGiftCodeSwitchedOff(t *testing.T) {
	g := newGiftFixture(t)
	ctx := context.Background()
	code := g.code(t, 10, 5)
	user := g.customer(t, 7730)
	if _, err := g.st.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('gift_codes.enabled', '"false"')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = g.st.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = 'gift_codes.enabled'`) })
	for _, c := range []string{code, "WRONG-WRONG-WRONG"} {
		if r := g.redeem(t, user, c, ""); r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_OFF {
			t.Fatalf("switched off, %q: %+v", c, r)
		}
	}
	if _, err := g.st.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = 'gift_codes.enabled'`); err != nil {
		t.Fatal(err)
	}
	if r := g.redeem(t, user, code, ""); r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_REDEEMED {
		t.Fatalf("switched on again: %+v", r)
	}
}

// What is a broken request or a refused customer is an error with a code the bot acts on, and no error says anything of a code.
func TestRedeemGiftCodeErrors(t *testing.T) {
	g := newGiftFixture(t)
	ctx := context.Background()
	code := g.code(t, 10, 5)
	other := g.code(t, 10, 5)
	user := g.customer(t, 7740)
	call := func(userID, c, key string) error {
		_, err := g.c.RedeemGiftCode(ctx, &corev1.RedeemGiftCodeRequest{UserId: userID, Code: c, IdempotencyKey: key})
		return err
	}
	for name, tc := range map[string]struct {
		err  error
		want codes.Code
	}{
		"no user":             {call("", code, "k1"), codes.InvalidArgument},
		"a user that is none": {call("nobody", code, "k2"), codes.InvalidArgument},
		"an unknown user":     {call("00000000-0000-0000-0000-00000000dead", code, "k3"), codes.NotFound},
		"no key":              {call(user, code, ""), codes.InvalidArgument},
		"a key too long":      {call(user, code, strings.Repeat("k", 200)), codes.InvalidArgument},
	} {
		if status.Code(tc.err) != tc.want {
			t.Errorf("%s: %v, want %v", name, tc.err, tc.want)
		}
	}
	// A key of an earlier request used for another code.
	if r := g.redeem(t, user, code, "same-key"); r.GetOutcome() != corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_REDEEMED {
		t.Fatal(r)
	}
	err := call(user, other, "same-key")
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("a key reused for another code: %v", err)
	}
	for _, c := range []string{code, other} {
		if strings.Contains(err.Error(), strings.ReplaceAll(c, "-", "")) || strings.Contains(err.Error(), c) {
			t.Errorf("an error carries a code: %v", err)
		}
	}
	// A banned account: the error the bot already tells as "banned", and nothing credited.
	banned := g.customer(t, 7741)
	if err := g.st.SetUserStatus(ctx, g.st.Conn(), banned, "banned"); err != nil {
		t.Fatal(err)
	}
	err = call(banned, other, "k9")
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(strings.ToLower(err.Error()), "banned") {
		t.Fatalf("a banned account: %v", err)
	}
	if w, _ := g.c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: banned, Currency: "IRT"}); w.GetBalance() != 0 {
		t.Fatal("a banned account was credited")
	}
}

func TestOnlyTheBotMayRedeem(t *testing.T) {
	g := newGiftFixture(t)
	other := g.client(t, "a-different-services-token")
	if _, err := other.RedeemGiftCode(context.Background(), &corev1.RedeemGiftCodeRequest{UserId: "x", Code: "y", IdempotencyKey: "z"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}
