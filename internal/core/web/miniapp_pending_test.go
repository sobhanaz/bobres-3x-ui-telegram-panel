package web

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// GET /api/mini/v1/payments/pending: the customer's own manual payments that
// still wait for their proof, which a customer takes up again after the screen
// that showed them was lost (Telegram reloaded the app).

const pendingPath = "/api/mini/v1/payments/pending"

// pendingRows are the rows of the list, as the app gets them.
func (p *payFix) pendingRows(tok string) []map[string]any {
	p.t.Helper()
	code, out := p.call(http.MethodGet, pendingPath, tok, nil)
	if code != 200 {
		p.t.Fatalf("pending payments: %d %v", code, out)
	}
	list, ok := out["payments"].([]any)
	if !ok { // an empty list is [], never null, so the app can count on an array
		p.t.Fatalf("pending payments is not a list: %v", out)
	}
	rows := make([]map[string]any, 0, len(list))
	for _, r := range list {
		rows = append(rows, r.(map[string]any))
	}
	return rows
}

// pendingIDs are the ids of the payments in the list, in its order.
func (p *payFix) pendingIDs(tok string) []string {
	p.t.Helper()
	ids := []string{}
	for _, r := range p.pendingRows(tok) {
		ids = append(ids, r["intent_id"].(string))
	}
	return ids
}

func sameIDs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: %v, want %v", what, got, want)
	}
}

// pendingFix is a store in which alice has four manual payments waiting, and
// bob one; payments that are not manual are among them.
type pendingFix struct {
	*payFix
	gold               *store.Plan
	alice, bob         string // their sessions
	aliceID, bobID     string // their ids in core
	order              string // the order of alice's card payment
	card, crypto, link string // alice's payments for orders, oldest first
	top                string // alice's top-up by card, the newest of the four
	bobCard            string
	// what the app was shown when it started them
	cardRes, cryptoRes, linkRes, topRes map[string]any
}

func newPendingFix(t *testing.T) *pendingFix {
	t.Helper()
	p := newPayFix(t)
	p.loosen()
	f := &pendingFix{payFix: p, gold: p.plan("Gold", 30, 50<<30, 150_000)}
	f.alice, f.bob = p.signIn("tg:110:alice:en"), p.signIn("tg:111:bob:en")
	for tg, dst := range map[int64]*string{110: &f.aliceID, 111: &f.bobID} {
		u, err := p.st.GetUserByTelegramID(context.Background(), p.st.Conn(), tg)
		if err != nil {
			t.Fatal(err)
		}
		*dst = u.ID
	}
	f.order = p.buy(f.alice, f.gold, "pendkey0001")
	f.card, f.cardRes = p.payOK(f.alice, f.order, "manual_card")
	f.crypto, f.cryptoRes = p.payOK(f.alice, p.buy(f.alice, f.gold, "pendkey0002"), "manual_crypto")
	f.link, f.linkRes = p.payOK(f.alice, p.buy(f.alice, f.gold, "pendkey0003"), "manual_zarinpal")
	code, res := p.call(http.MethodPost, "/api/mini/v1/topup", f.alice, map[string]any{"amount": 200_000, "method": "manual_card", "key": "pendtopup001"})
	if code != 200 {
		t.Fatalf("top-up: %d %v", code, res)
	}
	f.top, f.topRes = res["intent_id"].(string), res
	// Payments that are not manual take no receipt: not listed.
	p.payOK(f.alice, p.buy(f.alice, f.gold, "pendkey0004"), "zarinpal")
	p.payOK(f.alice, p.buy(f.alice, f.gold, "pendkey0005"), "stars")
	f.bobCard, _ = p.payOK(f.bob, p.buy(f.bob, f.gold, "pendkey0006"), "manual_card")
	return f
}

// ---- what is listed ----

func TestMiniAppPendingPaymentsAreTheCustomersOwnManualOnes(t *testing.T) {
	f := newPendingFix(t)

	// Alice's four manual payments, the newest first, whatever their method; the ones paid online are not here,
	// nor is anything of Bob's.
	rows := f.pendingRows(f.alice)
	got := []string{}
	for _, r := range rows {
		got = append(got, r["intent_id"].(string))
	}
	sameIDs(t, "alice's list", got, f.top, f.link, f.crypto, f.card)

	// Each is what the call that started it showed the customer, so the app draws the same screen: the card, the
	// addresses and the link as the store has them, the payment reference, the exact amount; and when it began.
	for i, start := range []map[string]any{f.topRes, f.linkRes, f.cryptoRes, f.cardRes} {
		row := rows[i]
		created, ok := row["created_at"].(float64)
		if !ok || created != float64(f.model.intent(row["intent_id"].(string)).created.Unix()) {
			t.Errorf("row %d began at %v", i, row["created_at"])
		}
		delete(row, "created_at")
		if !reflect.DeepEqual(row, start) {
			t.Errorf("row %d is not what the payment showed:\n got  %v\n want %v", i, row, start)
		}
	}
	if d := rows[0]["details"].(map[string]any); rows[0]["order_id"] != "" || d["card_number"] != "6037-9911-2222-3333" || d["reference"] != domain.PaymentReference(f.top) {
		t.Errorf("the top-up by card: %v", rows[0])
	}
	if rows[2]["usdt_amount"] != float64(2_500_000) || rows[2]["currency"] != "IRT" || rows[2]["amount"] != float64(150_000) {
		t.Errorf("the crypto payment: %v", rows[2])
	}
	if rows[1]["details"].(map[string]any)["zarinpal_link"] != "https://zarinp.al/shop" {
		t.Errorf("the payment link: %v", rows[1])
	}

	// Bob has his own and nothing else; a customer with nothing waiting gets an empty list.
	sameIDs(t, "bob's list", f.pendingIDs(f.bob), f.bobCard)
	sameIDs(t, "a new customer's list", f.pendingIDs(f.signIn("tg:112:carol:en")))
}

// Everything comes from the payment: the customer is the session's, and what the request says changes nothing.
func TestMiniAppPendingPaymentsTakeNothingFromTheRequest(t *testing.T) {
	f := newPendingFix(t)
	before := f.model.filterCount()

	query := "?user_id=" + f.aliceID + "&user=" + f.aliceID + "&telegram_id=110&status=succeeded&provider=zarinpal&amount=1&currency=USDT&limit=100&page=2&order_id=" + f.order
	code, out := f.call(http.MethodGet, pendingPath+query, f.bob, nil)
	if code != 200 {
		t.Fatalf("with a query: %d %v", code, out)
	}
	ids := []string{}
	for _, r := range out["payments"].([]any) {
		ids = append(ids, r.(map[string]any)["intent_id"].(string))
	}
	sameIDs(t, "bob's list with alice's id in the query", ids, f.bobCard)

	// Payments was only ever asked about Bob's pending payments of a manual method.
	asked := f.model.filtersFrom(before)
	if len(asked) != 3 {
		t.Fatalf("payments was asked %d times: %+v", len(asked), asked)
	}
	var methods []string
	for _, a := range asked {
		if a.UserID != f.bobID || a.Status != "pending" || a.OrderID != "" || a.IntentID != "" || !domain.IsManual(a.Provider) {
			t.Errorf("payments was asked for %+v", a)
		}
		methods = append(methods, a.Provider)
	}
	slices.Sort(methods)
	if !slices.Equal(methods, []string{"manual_card", "manual_crypto", "manual_zarinpal"}) {
		t.Errorf("payments was asked for the methods %v", methods)
	}

	// A body on a GET is not read either, and the other verbs are not this call.
	if code, out := f.call(http.MethodGet, pendingPath, f.bob, map[string]any{"user_id": f.aliceID}); code != 200 || len(out["payments"].([]any)) != 1 {
		t.Errorf("a body: %d %v", code, out)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if code, _ := f.call(method, pendingPath, f.bob, map[string]any{}); code != 404 && code != 405 {
			t.Errorf("%s: %d", method, code)
		}
	}
}

// Only a payment that waits for its proof is listed, whatever else payments answers with.
func TestMiniAppPendingPaymentsAreOnlyThoseWaitingForTheirProof(t *testing.T) {
	f := newPendingFix(t)
	for _, st := range []string{"confirming", "succeeded", "failed", "expired", "rejected"} {
		f.model.setStatus(f.card, st)
		sameIDs(t, "list with the card payment "+st, f.pendingIDs(f.alice), f.top, f.link, f.crypto)
	}
	f.model.setStatus(f.card, "pending")
	sameIDs(t, "list with it waiting again", f.pendingIDs(f.alice), f.top, f.link, f.crypto, f.card)

	// A payments service that ignored the filter would answer every payment of everyone, in every state: still only
	// the customer's own, manual, waiting ones are shown.
	f.model.setStatus(f.crypto, "succeeded")
	f.model.ignoreFilter = true
	sameIDs(t, "alice's list from a service that does not filter", f.pendingIDs(f.alice), f.top, f.link, f.card)
	sameIDs(t, "bob's list from a service that does not filter", f.pendingIDs(f.bob), f.bobCard)
}

// A payment of the customer's that is sent its proof leaves the list at once, and the proof is the existing call.
func TestMiniAppPendingPaymentIsTakenUpAgainByTheProofCall(t *testing.T) {
	f := newPendingFix(t)
	img := pngReceipt(t, 40, 30)

	rows := f.pendingRows(f.alice)
	card := rows[3] // the screen the app draws again for it is this row
	if card["intent_id"] != f.card {
		t.Fatalf("row 3: %v", card)
	}
	if code, out := f.receipt(f.alice, card["intent_id"].(string), "998877", img); code != 200 || out["status"] != "confirming" {
		t.Fatalf("the receipt for a payment taken up again: %d %v", code, out)
	}
	sameIDs(t, "after the card payment's receipt", f.pendingIDs(f.alice), f.top, f.link, f.crypto)
	if sent := f.bot.sent(); len(sent) != 1 || sent[0].Reference != domain.PaymentReference(f.card) {
		t.Errorf("the receipt went to the bot as %+v", sent)
	}
	// The crypto payment is taken up the same way, by its transaction id.
	if code, out := f.txid(f.alice, f.crypto, map[string]any{"txid": hash64, "network": "TRC20"}); code != 200 || out["status"] != "confirming" {
		t.Fatalf("the hash for a payment taken up again: %d %v", code, out)
	}
	sameIDs(t, "after the crypto payment's hash", f.pendingIDs(f.alice), f.top, f.link)
	// The payment link's, and the top-up's.
	if code, out := f.receipt(f.alice, f.link, "", img); code != 200 {
		t.Fatalf("the receipt for the payment link: %d %v", code, out)
	}
	if code, out := f.receipt(f.alice, f.top, "445566", img); code != 200 {
		t.Fatalf("the receipt for the top-up: %d %v", code, out)
	}
	sameIDs(t, "when every proof is in", f.pendingIDs(f.alice))
	sameIDs(t, "bob's list is as it was", f.pendingIDs(f.bob), f.bobCard)
}

// What the list shows is another customer's no more than what the proof call takes: an id from it is a 404 for anyone else.
func TestMiniAppPendingPaymentsOfOneCustomerAreNotReachableByAnother(t *testing.T) {
	f := newPendingFix(t)
	img := pngReceipt(t, 40, 30)
	for _, r := range f.pendingRows(f.alice) {
		id := r["intent_id"].(string)
		if code, out := f.receipt(f.bob, id, "998877", img); code != 404 || out["error"] != "not_found" {
			t.Errorf("bob's receipt for %s: %d %v", id, code, out)
		}
		if code, out := f.txid(f.bob, id, map[string]any{"txid": hash64}); code != 404 {
			t.Errorf("bob's hash for %s: %d %v", id, code, out)
		}
		if code, out := f.call(http.MethodPost, "/api/mini/v1/payments/"+id+"/check", f.bob, map[string]any{}); code != 404 {
			t.Errorf("bob's check of %s: %d %v", id, code, out)
		}
	}
	// An id that is not one of hers is none of hers either: she cannot reach Bob's, or a guess.
	for _, id := range []string{f.bobCard, "0000000a-0000-4000-8000-0000000000ff", "not-an-id"} {
		if code, out := f.receipt(f.alice, id, "998877", img); code != 404 {
			t.Errorf("alice's receipt for %s: %d %v", id, code, out)
		}
	}
	if len(f.bot.sent()) != 0 || f.model.calls("SubmitReceipt")+f.model.calls("SubmitTXID") != 0 {
		t.Errorf("a proof for a payment that is not the sender's went on: %d sent", len(f.bot.sent()))
	}
	sameIDs(t, "alice's list is as it was", f.pendingIDs(f.alice), f.top, f.link, f.crypto, f.card)
}

// ---- what the store would no longer take ----

func TestMiniAppPendingPaymentsAreOnlyThoseTheStoreWouldStillTake(t *testing.T) {
	f := newPendingFix(t)
	all := []string{f.top, f.link, f.crypto, f.card}
	sameIDs(t, "to begin with", f.pendingIDs(f.alice), all...)

	for _, c := range []struct {
		what, key, off string
		gone           []string
	}{
		{"card switched off", "payments.manual_card.enabled", "false", []string{f.top, f.card}},
		{"no card number", "payments.card_number", "", []string{f.top, f.card}},
		{"crypto switched off", "payments.manual_crypto.enabled", "false", []string{f.crypto}},
		{"no rate to turn Toman into USDT", "payments.usdt_rate", "", []string{f.crypto}},
		{"payment link switched off", "payments.manual_zarinpal.enabled", "false", []string{f.link}},
		{"no payment link", "payments.zarinpal_link", "", []string{f.link}},
	} {
		old := ""
		if s, err := f.dom.Settings(context.Background()); err == nil {
			old = s[c.key]
		}
		f.setting(c.key, c.off)
		want := slices.DeleteFunc(slices.Clone(all), func(id string) bool { return slices.Contains(c.gone, id) })
		sameIDs(t, c.what, f.pendingIDs(f.alice), want...)
		if old == "" {
			old = "true"
		}
		f.setting(c.key, old)
		sameIDs(t, c.what+", then back", f.pendingIDs(f.alice), all...)
	}

	// The settings refuse a payment link that is not a web address. One that got into the database anyway (by hand) is not
	// shown, as a payment by it is not started: there is nothing to open.
	f.sql(`UPDATE core.settings SET value = to_jsonb($2::text) WHERE key = $1`, "payments.zarinpal_link", "zarinp.al/shop")
	sameIDs(t, "a payment link that is not a web address", f.pendingIDs(f.alice), f.top, f.crypto, f.card)
	f.setting("payments.zarinpal_link", "https://zarinp.al/shop")

	// With neither USDT address there is nothing to send to; with one, only that one is shown.
	f.setting("payments.usdt_trc20", "")
	rows := f.pendingRows(f.alice)
	if d := rows[2]["details"].(map[string]any); rows[2]["intent_id"] != f.crypto || d["usdt_trc20"] != nil || d["usdt_erc20"] != "0x0000000000000000000000000000000000000001" {
		t.Errorf("with the TRC20 address gone: %v", rows[2])
	}
	f.setting("payments.usdt_erc20", "")
	sameIDs(t, "no USDT address", f.pendingIDs(f.alice), f.top, f.link, f.card)
	f.setting("payments.usdt_trc20", "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r")
	f.setting("payments.usdt_erc20", "0x0000000000000000000000000000000000000001")

	// Without the bot a receipt cannot be sent, so there is nothing to take up.
	f.api.cfg.MiniAppBot = nil
	sameIDs(t, "without the bot", f.pendingIDs(f.alice))
	f.api.cfg.MiniAppBot = f.bot
	sameIDs(t, "with it again", f.pendingIDs(f.alice), all...)
}

// What is shown is the payment's own, and what to pay to is the store's as it is now.
func TestMiniAppPendingPaymentsShowThePaymentsOwnAmountAndTheStoresDetailsNow(t *testing.T) {
	f := newPendingFix(t)
	// The plan costs more today, the order has changed: the payment was for what it was for.
	f.sql(`UPDATE core.plans SET price = 999000 WHERE id = $1`, f.gold.ID)
	f.sql(`UPDATE core.orders SET amount = 999000 WHERE id = $1`, f.order)
	// The card was changed and the rate moved.
	f.setting("payments.card_number", "5022-2910-0000-1111")
	f.setting("payments.card_holder", "Sara Karimi")
	f.setting("payments.usdt_rate", "30000")
	rows := f.pendingRows(f.alice)
	if len(rows) != 4 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, r := range rows {
		switch r["intent_id"] {
		case f.card:
			if d := r["details"].(map[string]any); r["amount"] != float64(150_000) || d["card_number"] != "5022-2910-0000-1111" || d["card_holder"] != "Sara Karimi" || d["reference"] != domain.PaymentReference(f.card) {
				t.Errorf("the card payment: %v", r)
			}
		case f.crypto:
			if r["amount"] != float64(150_000) || r["usdt_amount"] != float64(5_000_000) { // 150,000 Toman at 30,000 a USDT
				t.Errorf("the crypto payment: %v", r)
			}
		case f.top:
			if r["amount"] != float64(200_000) {
				t.Errorf("the top-up: %v", r)
			}
		}
	}
}

// A payment waiting for an order that is no longer one to pay is not one to send a proof for.
func TestMiniAppPendingPaymentsOfAnOrderThatIsClosedAreNotListed(t *testing.T) {
	f := newPendingFix(t)
	all := []string{f.top, f.link, f.crypto, f.card}
	for _, st := range []string{"paid", "provisioning", "active", "provision_failed", "cancelled", "expired"} {
		f.sql(`UPDATE core.orders SET status = $2 WHERE id = $1`, f.order, st)
		sameIDs(t, "the order "+st, f.pendingIDs(f.alice), f.top, f.link, f.crypto)
	}
	for _, st := range []string{"created", "awaiting_payment"} {
		f.sql(`UPDATE core.orders SET status = $2 WHERE id = $1`, f.order, st)
		sameIDs(t, "the order "+st, f.pendingIDs(f.alice), all...)
	}

	// A payment for an order that is not there, or that is another customer's, is not shown: the order is read from
	// core, never taken from the payment's word.
	ctx := context.Background()
	if _, _, err := f.model.CreateIntent(ctx, "00000000-0000-4000-8000-0000000000ee", f.aliceID, domain.ManualCard, 150_000, "IRT", "ghost-order"); err != nil {
		t.Fatal(err)
	}
	bobOrder := f.buy(f.bob, f.gold, "pendkey0007")
	if _, _, err := f.model.CreateIntent(ctx, bobOrder, f.aliceID, domain.ManualCard, 150_000, "IRT", "bobs-order"); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "payments for orders that are not alice's", f.pendingIDs(f.alice), all...)
}

// The list is the newest ten, however many are waiting.
func TestMiniAppPendingPaymentsAreTheNewestFew(t *testing.T) {
	f := newPendingFix(t)
	var made []string
	for i := 0; i < 24; i++ {
		id, _ := f.payOK(f.alice, f.buy(f.alice, f.gold, fmt.Sprintf("manykey%04d", i)), "manual_card")
		made = append(made, id)
	}
	slices.Reverse(made) // the newest first
	got := f.pendingIDs(f.alice)
	if len(got) != miniPendingMax {
		t.Fatalf("%d payments listed, want %d", len(got), miniPendingMax)
	}
	sameIDs(t, "the newest of 28", got, made[:miniPendingMax]...)
}

// ---- who may ask, and when ----

func TestMiniAppPendingPaymentsNeedASessionAndAnActiveCustomer(t *testing.T) {
	f := newPendingFix(t)
	for _, token := range []string{"", "not-a-token"} {
		if code, _ := f.call(http.MethodGet, pendingPath, token, nil); code != 401 {
			t.Errorf("token %q: %d", token, code)
		}
	}
	f.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, f.aliceID)
	if code, out := f.call(http.MethodGet, pendingPath, f.alice, nil); code != 403 || out["error"] != "banned" {
		t.Errorf("banned: %d %v", code, out)
	}
	// The app being switched off closes this too.
	f.sql(`UPDATE core.users SET status = 'active' WHERE id = $1`, f.aliceID)
	f.setting("miniapp.enabled", "false")
	if code, out := f.call(http.MethodGet, pendingPath, f.alice, nil); code != 404 || out["error"] != "disabled" {
		t.Errorf("app off: %d %v", code, out)
	}
}

// A payment already started is finished in maintenance too, as the proof call is.
func TestMiniAppPendingPaymentsAreServedInMaintenance(t *testing.T) {
	f := newPendingFix(t)
	f.setting("maintenance.enabled", "true")
	if code, out := f.call(http.MethodGet, "/api/mini/v1/methods?currency=IRT&amount=150000", f.alice, nil); code != 503 || out["error"] != "maintenance" {
		t.Fatalf("methods in maintenance: %d %v", code, out)
	}
	if code, out := f.call(http.MethodGet, "/api/mini/v1/wallet", f.alice, nil); code != 503 || out["error"] != "maintenance" {
		t.Fatalf("wallet in maintenance: %d %v", code, out)
	}
	sameIDs(t, "list in maintenance", f.pendingIDs(f.alice), f.top, f.link, f.crypto, f.card)
	if code, out := f.receipt(f.alice, f.card, "998877", pngReceipt(t, 40, 30)); code != 200 || out["status"] != "confirming" {
		t.Errorf("a proof for a payment from the list: %d %v", code, out)
	}
	sameIDs(t, "list in maintenance, after it", f.pendingIDs(f.alice), f.top, f.link, f.crypto)
}

// The call counts against the limits every call of the customer counts against.
func TestMiniAppPendingPaymentsShareTheRateLimits(t *testing.T) {
	f := newPendingFix(t)
	// Reads: 120 a minute for everything the customer reads; the writes' limit is not touched by a GET.
	f.api.mini.api = newLimiter(4, time.Minute)
	f.api.mini.write = newLimiter(1, time.Minute)
	for i := 0; i < 2; i++ {
		if code, out := f.call(http.MethodGet, pendingPath, f.alice, nil); code != 200 {
			t.Fatalf("read %d: %d %v", i, code, out)
		}
	}
	if code, _ := f.call(http.MethodGet, "/api/mini/v1/wallet", f.alice, nil); code != 200 {
		t.Fatalf("another read: %d", code)
	}
	if code, _ := f.call(http.MethodGet, pendingPath, f.alice, nil); code != 200 {
		t.Fatalf("the fourth read: %d", code)
	}
	code, out, hdr := f.send(http.MethodGet, pendingPath, f.alice, "", nil)
	if code != 429 || out["error"] != "rate_limited" || hdr.Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d %v %v", code, out, hdr)
	}
	// Another customer is not held up by it.
	if code, _ := f.call(http.MethodGet, pendingPath, f.bob, nil); code != 200 {
		t.Errorf("another customer: %d", code)
	}
	if got := f.model.calls("ListPayments"); got != 4*3 {
		t.Errorf("payments was asked %d times for 4 lists", got) // three methods each: a refused call asks nothing
	}
}

func TestMiniAppPendingPaymentsWhenPaymentsIsAway(t *testing.T) {
	f := newPendingFix(t)
	f.model.down = true
	if code, out := f.call(http.MethodGet, pendingPath, f.alice, nil); code != 503 || out["error"] != "unavailable" {
		t.Errorf("payments away: %d %v", code, out)
	}
	f.model.down = false
	f.api.cfg.Payments = nil
	if code, out := f.call(http.MethodGet, pendingPath, f.alice, nil); code != 503 || out["error"] != "unavailable" {
		t.Errorf("no payments service: %d %v", code, out)
	}
	f.api.cfg.Payments = f.model
	if got := f.pendingIDs(f.alice); len(got) != 4 {
		t.Errorf("after it came back: %v", got)
	}
}
