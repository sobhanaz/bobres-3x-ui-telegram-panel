package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/trongrid"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/provider"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tronfake"
)

// Valid TRON mainnet addresses (checksums verified), test fixtures.
const (
	storeAddr     = "TCKR37NfLumfScqBQX6qrnJR3mZkv6xGqi"
	storeAddr2    = "TWxtFn4btLxwjWcZJvyS6T5iQaHztZnxcF"
	otherContract = "TVmTPtVpHTgWzHYnGDbih1QDTuQo6J1Eef"   // a contract that calls its coin "USDT"
	tronKey       = "11111111-2222-4333-8444-555555555555" //nolint:gosec // test fixture
	tronKey2      = "66666666-7777-4888-8999-000000000000" //nolint:gosec // test fixture
)

type chainFix struct {
	svc  *Service
	st   *store.Store
	fake *tronfake.Server
	opts GatewayOptions
}

// chainService is a payments service whose watcher reads the TronGrid fake
// through the real client; enabled switches automatic USDT on at storeAddr.
func chainService(t *testing.T, enabled bool) *chainFix {
	t.Helper()
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.chain_transfers, payments.chain_cursors`); err != nil {
		t.Fatal(err)
	}
	fake := tronfake.New()
	t.Cleanup(fake.Close)
	b := &builder{}
	o := GatewayOptions{Env: "prod", Envelope: envelope(t, keyA), Build: b.build,
		BuildChain: func(c ChainSettings) (gateway.ChainSource, error) {
			cl, err := trongrid.New(trongrid.Config{APIKey: c.APIKey, Proxy: c.Proxy, BaseURL: fake.URL})
			if err != nil {
				return nil, err
			}
			return cl, nil
		}}
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if enabled {
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: storeAddr, ActorID: actor}); err != nil {
			t.Fatal(err)
		}
	}
	return &chainFix{svc: svc, st: st, fake: fake, opts: o}
}

// chainIntent asks for base micro-USDT for a 500,000 Toman order of its own
// (one order per key).
func chainIntent(key string, base int64) *store.Intent {
	usdt, o := "USDT", keyUUID("order", key)
	return &store.Intent{UserID: uid, OrderID: &o, Provider: USDTAuto, Amount: 500_000, Currency: "IRT",
		IdempotencyKey: key, GatewayAmount: &base, GatewayCurrency: &usdt}
}

// keyUUID is a fixed UUID for a name (test orders and customers).
func keyUUID(kind, name string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(kind + ":" + name))
	return fmt.Sprintf("00000000-0000-7000-8000-%012x", h.Sum64()&0xffffffffffff)
}

func (f *chainFix) start(t *testing.T, key string, base int64) *store.Intent {
	t.Helper()
	in, err := f.svc.StartChain(context.Background(), chainIntent(key, base))
	if err != nil {
		t.Fatalf("start %s: %v", key, err)
	}
	return in
}

func (f *chainFix) scan(t *testing.T) int {
	t.Helper()
	n, err := f.svc.ScanChainOnce(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return n
}

func (f *chainFix) intent(t *testing.T, id string) *store.Intent {
	t.Helper()
	in, err := f.st.GetIntent(context.Background(), nil, id)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func (f *chainFix) transfers(t *testing.T) []store.ChainTransfer {
	t.Helper()
	list, _, err := f.st.ListChainTransfers(context.Background(), store.ChainTransferFilter{}, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func (f *chainFix) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.st.DB().Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

// endHolds ends every pending hold on unmatched deposits and announces them,
// as the watcher does once the hold has passed; it returns how many.
func (f *chainFix) endHolds(t *testing.T) int {
	t.Helper()
	f.exec(t, `UPDATE payments.chain_transfers SET announce_at = now() - interval '1 second' WHERE announce_at IS NOT NULL`)
	n, err := f.svc.announceHeld(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func topicCount(t *testing.T, st *store.Store, topic string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(context.Background(), `SELECT count(*) FROM payments.outbox_payments WHERE topic = $1`, topic).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func unmatchedEvents(t *testing.T, st *store.Store) []DepositUnmatchedEvent {
	t.Helper()
	rows, err := st.DB().Query(context.Background(), `SELECT payload FROM payments.outbox_payments WHERE topic = $1 ORDER BY id`, TopicDepositUnmatched)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []DepositUnmatchedEvent
	for rows.Next() {
		var (
			raw []byte
			e   DepositUnmatchedEvent
		)
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// An automatic USDT payment asks for the core amount plus a unique micro
// offset at the store's address for 45 minutes; the exact transfer settles it
// once, even with two replicas reading and customers checking at once, with
// the payment's own amount on the ledger and the event.
func TestUSDTExactPaymentSettlesOnceEvenConcurrently(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	in := f.start(t, "u1", 5_000_000)
	if in.GatewayAmount == nil || *in.GatewayAmount != 5_000_001 || *in.GatewayCurrency != "USDT" ||
		in.PayAddress == nil || *in.PayAddress != storeAddr || in.Status != "pending" || in.ExternalID != nil {
		t.Fatalf("intent: %+v", in)
	}
	if left := time.Until(*in.ExpiresAt); left < 44*time.Minute || left > 46*time.Minute {
		t.Fatalf("pay window: %v", left)
	}
	if again := f.start(t, "u1", 5_000_000); again.ID != in.ID || *again.GatewayAmount != 5_000_001 {
		t.Fatalf("retry made another payment: %+v", again)
	}
	if f.scan(t) != 0 || f.intent(t, in.ID).Status != "pending" {
		t.Fatal("settled before anything was paid")
	}
	txid := f.fake.Pay(storeAddr, 5_000_001)

	replica := New(f.st) // a second payments replica on the same database
	if err := replica.ConfigureGateways(ctx, f.opts); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			switch i % 3 {
			case 0:
				_, err = f.svc.ScanChainOnce(ctx)
			case 1:
				_, err = replica.ScanChainOnce(ctx)
			default:
				_, err = f.svc.CheckIntent(ctx, uid, in.ID, "check")
			}
			if err != nil {
				t.Errorf("read %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	got := f.intent(t, in.ID)
	if got.Status != "succeeded" || got.ExternalID == nil || *got.ExternalID != txid || got.ProviderRef == nil || *got.ProviderRef != txid {
		t.Fatalf("settled intent: %+v", got)
	}
	ev := outbox(t, f.st)
	if len(ev[events.PaymentsPaymentSucceeded]) != 1 || ev[events.PaymentsPaymentSucceeded][0].Amount != 500_000 ||
		ev[events.PaymentsPaymentSucceeded][0].Currency != "IRT" || ev[events.PaymentsPaymentSucceeded][0].Provider != USDTAuto ||
		ledgerCount(t, f.st) != 1 {
		t.Fatalf("settled more than once or with the wrong amount: %+v ledger=%d", ev, ledgerCount(t, f.st))
	}
	list := f.transfers(t)
	if len(list) != 1 || list[0].Outcome != store.TransferMatched || list[0].IntentID == nil || *list[0].IntentID != in.ID ||
		list[0].Value != 5_000_001 || list[0].TxID != txid || list[0].EventIndex != 0 {
		t.Fatalf("transfer log: %+v", list)
	}
	if f.scan(t) != 0 || ledgerCount(t, f.st) != 1 || len(f.transfers(t)) != 1 {
		t.Fatal("a second read recorded the transfer again")
	}
	r, err := f.svc.GatewayReport(ctx, USDTAuto, time.Time{}, time.Time{})
	if err != nil || r.LastPaid == nil || r.LastPaid.Kind != "match" || r.Chain == nil || r.Chain.UnmatchedCount != 0 ||
		r.Chain.Cursor == nil || r.Chain.Cursor.OKAt == nil {
		t.Fatalf("report: %+v %v", r, err)
	}
}

// The token is USDT only by its contract: a token named USDT from another
// contract, an approval, a transfer to someone else and a zero-value
// "transfer" (address poisoning) settle nothing and are not recorded.
func TestUSDTFakeTokensAndNonTransfersAreIgnored(t *testing.T) {
	f := chainService(t, true)
	in := f.start(t, "f1", 2_000_000)
	amount := "2000001"
	f.fake.LeakOtherTokens(true) // the indexer ignores contract_address
	f.fake.Add(tronfake.Transfer{To: storeAddr, Token: otherContract, Symbol: "USDT", Value: amount})
	f.fake.Add(tronfake.Transfer{To: storeAddr, Type: "Approval", Value: amount})
	f.fake.Add(tronfake.Transfer{To: storeAddr, Value: "0"})
	f.fake.Add(tronfake.Transfer{To: storeAddr, Value: amount, Unconfirmed: true})
	if f.scan(t) != 0 {
		t.Fatal("recorded a transfer that is not a confirmed USDT transfer")
	}
	if got := f.intent(t, in.ID); got.Status != "pending" || ledgerCount(t, f.st) != 0 || len(f.transfers(t)) != 0 ||
		topicCount(t, f.st, events.PaymentsPaymentSucceeded) != 0 || topicCount(t, f.st, TopicDepositUnmatched) != 0 {
		t.Fatalf("a fake payment counted: %+v", got)
	}

	// What the indexer says is checked field by field, whatever it claims.
	at := time.Now().Add(-time.Minute)
	tx := strings.Repeat("ab", 32)
	got := []gateway.ChainTransfer{
		{TxID: tx, From: tronfake.Sender, To: storeAddr, Token: trongrid.USDTContract, Type: "Transfer", Value: amount, BlockTS: at},
		{TxID: tx, From: tronfake.Sender, To: storeAddr2, Token: trongrid.USDTContract, Type: "Transfer", Value: "2000002", BlockTS: at},
		{TxID: tx, From: tronfake.Sender, To: storeAddr, Token: trongrid.USDTContract, Type: "Transfer", Value: "-5", BlockTS: at},
		{TxID: tx, From: tronfake.Sender, To: storeAddr, Token: trongrid.USDTContract, Type: "Transfer", Value: "1e6", BlockTS: at},
		{TxID: tx, From: tronfake.Sender, To: storeAddr, Token: trongrid.USDTContract, Type: "Transfer", Value: "99999999999999999999", BlockTS: at},
		{TxID: "0x" + tx, From: tronfake.Sender, To: storeAddr, Token: trongrid.USDTContract, Type: "Transfer", Value: amount, BlockTS: at},
		{TxID: tx, From: "", To: storeAddr, Token: trongrid.USDTContract, Type: "Transfer", Value: amount, BlockTS: at},
		{TxID: tx, From: tronfake.Sender, To: storeAddr, Token: trongrid.USDTContract, Type: "Transfer", Value: amount, BlockTS: time.Now().Add(2 * time.Hour)},
		{TxID: tx, From: tronfake.Sender, To: storeAddr, Token: "usdt", Type: "Transfer", Value: amount, BlockTS: at},
	}
	valid, _, late := f.svc.validTransfers(storeAddr, got, true, time.Now())
	if len(valid) != 1 || valid[0].value != 2_000_001 || valid[0].txid != tx || late != 1 {
		t.Fatalf("validated: %+v (late %d)", valid, late)
	}
}

// Underpaid, overpaid and unknown deposits are recorded once as unmatched,
// announced to staff (payment.deposit_unmatched.v1) once no manual proof
// claimed them within the hold, and reported from then; dust is recorded
// silently; a second exact payment for a settled intent is unmatched too,
// and announced at once (no manual proof can take an automatic amount).
// Nothing is credited for them.
func TestUSDTUnmatchedDepositsAreRecordedAndAnnounced(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	in := f.start(t, "m1", 10_000_000) // 10.000001 USDT
	under := f.fake.Pay(storeAddr, 9_000_001)
	over := f.fake.Pay(storeAddr, 10_000_002)
	f.fake.Pay(storeAddr, 7_000_000)
	f.fake.Pay(storeAddr, 50_000) // 0.05 USDT: dust
	if n := f.scan(t); n != 4 {
		t.Fatalf("recorded %d", n)
	}
	if f.intent(t, in.ID).Status != "pending" || ledgerCount(t, f.st) != 0 || topicCount(t, f.st, events.PaymentsPaymentSucceeded) != 0 {
		t.Fatal("an inexact deposit settled the payment")
	}
	// Held: a manual customer's proof may still claim them.
	if r, err := f.svc.GatewayReport(ctx, USDTAuto, time.Time{}, time.Time{}); err != nil || len(unmatchedEvents(t, f.st)) != 0 ||
		r.Chain.UnmatchedCount != 0 || len(r.Chain.Unmatched) != 0 {
		t.Fatalf("announced or reported during the hold: %+v %v", r.Chain, err)
	}
	for _, tr := range f.transfers(t) {
		if want := tr.Outcome == store.TransferUnmatched; (tr.AnnounceAt != nil) != want ||
			want && time.Until(*tr.AnnounceAt) < chainUnmatchedHold-time.Minute {
			t.Fatalf("hold of %+v", tr)
		}
	}
	f.scan(t)
	if len(unmatchedEvents(t, f.st)) != 0 {
		t.Fatal("announced before the hold ended")
	}
	if n := f.endHolds(t); n != 3 {
		t.Fatalf("announced %d after the hold", n)
	}
	ev := unmatchedEvents(t, f.st)
	byTx := map[string]DepositUnmatchedEvent{}
	for _, e := range ev {
		byTx[e.TxID] = e
	}
	if u := byTx[under]; len(ev) != 3 || u.Amount != 9_000_001 || u.Currency != "USDT" || u.Network != "TRC20" ||
		u.Reason != UnmatchedNoPayment || u.To != storeAddr || u.From != tronfake.Sender || byTx[over].Amount != 10_000_002 {
		t.Fatalf("announced: %+v", ev)
	}
	outcomes := map[string]int{}
	for _, tr := range f.transfers(t) {
		outcomes[tr.Outcome+"/"+*tr.Reason]++
	}
	if outcomes["unmatched/no_payment"] != 3 || outcomes["ignored/dust"] != 1 {
		t.Fatalf("recorded: %v", outcomes)
	}

	paid := f.fake.Pay(storeAddr, 10_000_001)
	twice := f.fake.Pay(storeAddr, 10_000_001)
	f.scan(t)
	if got := f.intent(t, in.ID); got.Status != "succeeded" || ledgerCount(t, f.st) != 1 {
		t.Fatalf("exact payment: %+v", got)
	}
	settledBy := *f.intent(t, in.ID).ExternalID
	second := paid
	if settledBy == paid {
		second = twice
	}
	ev = unmatchedEvents(t, f.st)
	if len(ev) != 4 || ev[3].TxID != second || ev[3].Reason != UnmatchedAlreadyPaid {
		t.Fatalf("second payment: %+v", ev)
	}
	r, err := f.svc.GatewayReport(ctx, USDTAuto, time.Time{}, time.Time{})
	if err != nil || r.Chain.UnmatchedCount != 4 || len(r.Chain.Unmatched) != 4 {
		t.Fatalf("report: %+v %v", r.Chain, err)
	}
	if f.scan(t) != 0 || f.endHolds(t) != 0 || len(unmatchedEvents(t, f.st)) != 4 {
		t.Fatal("announced twice")
	}
}

// The window: an open payment expires only after a read covering its window
// and its grace has gone through; a late exact payment still settles it
// within 48 hours of its creation, not after; a transfer older than the
// payment is not for it.
func TestUSDTLatePaymentsAndExpiry(t *testing.T) {
	f := chainService(t, true)
	in := f.start(t, "l1", 3_000_000)
	f.exec(t, `UPDATE payments.payment_intents SET created_at = now() - interval '2 hours',
		expires_at = now() - interval '75 minutes' WHERE id = $1`, in.ID)
	// TronGrid is down: nothing is known, so nothing expires.
	f.fake.Fail(1, 503)
	f.scan(t)
	if f.intent(t, in.ID).Status != "pending" {
		t.Fatal("expired without a read")
	}
	f.scan(t)
	got := f.intent(t, in.ID)
	if got.Status != "expired" || got.FailureReason == nil || *got.FailureReason != ReasonExpired {
		t.Fatalf("not expired: %+v", got)
	}
	if ev := outbox(t, f.st); len(ev[events.PaymentsPaymentRejected]) != 1 || ev[events.PaymentsPaymentRejected][0].Reason != ReasonExpired {
		t.Fatalf("events: %+v", ev)
	}
	f.fake.Pay(storeAddr, 3_000_001)
	f.scan(t)
	if got := f.intent(t, in.ID); got.Status != "succeeded" || got.FailureReason != nil || ledgerCount(t, f.st) != 1 {
		t.Fatalf("late payment: %+v", got)
	}

	// Past the late-payment window, an exact amount is someone else's problem.
	old := f.start(t, "l2", 3_000_000)
	f.exec(t, `UPDATE payments.payment_intents SET status = 'expired', failure_reason = 'expired',
		created_at = now() - interval '49 hours', expires_at = now() - interval '48 hours' WHERE id = $1`, old.ID)
	// A payment made before the intent existed is not for it either.
	early := f.start(t, "l3", 4_000_000)
	f.exec(t, `UPDATE payments.payment_intents SET created_at = now() + interval '5 minutes' WHERE id = $1`, early.ID)
	f.fake.Pay(storeAddr, *old.GatewayAmount)
	f.fake.Pay(storeAddr, *early.GatewayAmount)
	f.scan(t)
	if f.intent(t, old.ID).Status != "expired" || f.intent(t, early.ID).Status != "pending" || ledgerCount(t, f.st) != 1 {
		t.Fatal("a transfer outside a payment's window settled it")
	}
	f.endHolds(t)
	if ev := unmatchedEvents(t, f.st); len(ev) != 2 || ev[0].Reason != UnmatchedNoPayment || ev[1].Reason != UnmatchedNoPayment {
		t.Fatalf("outside the window: %+v", ev)
	}

	// A read cut short at its page limit has not seen everything yet: it
	// expires nothing.
	cut := f.start(t, "l4", 6_000_000)
	f.exec(t, `UPDATE payments.payment_intents SET created_at = now() - interval '2 hours',
		expires_at = now() - interval '75 minutes' WHERE id = $1`, cut.ID)
	for i := range 12 {
		f.fake.Pay(storeAddr, int64(50_000_000+i))
	}
	f.fake.PageSize(1)
	f.scan(t)
	if f.intent(t, cut.ID).Status != "pending" {
		t.Fatal("expired after a read cut short")
	}
	f.fake.PageSize(0)
	f.scan(t)
	if f.intent(t, cut.ID).Status != "expired" {
		t.Fatal("not expired after a complete read")
	}
}

// Every open payment to an address has its own amount, also when many
// customers start at once; a retry is the same payment; when every offset is
// taken the method is refused with its own message rather than ambiguous;
// old payments free theirs; a unique amount is never a whole cent.
func TestUSDTUniqueAmountsUnderConcurrency(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	const n = 40
	amounts := make(chan int64, 2*n)
	var wg sync.WaitGroup
	for i := range 2 * n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := "c" + string(rune('A'+i%n)) // every key twice
			req := chainIntent(key, 1_230_000)
			req.UserID = keyUUID("user", key)
			in, err := f.svc.StartChain(ctx, req)
			if err != nil {
				t.Errorf("start: %v", err)
				return
			}
			amounts <- *in.GatewayAmount
		}()
	}
	wg.Wait()
	close(amounts)
	seen := map[int64]bool{}
	for a := range amounts {
		if a <= 1_230_000 || a > 1_230_000+maxChainOffset {
			t.Fatalf("amount %d outside the offsets", a)
		}
		seen[a] = true
	}
	var intents int
	if err := f.st.DB().QueryRow(ctx, `SELECT count(DISTINCT gateway_amount) FROM payments.payment_intents WHERE provider = $1`, USDTAuto).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if len(seen) != n || intents != n {
		t.Fatalf("%d distinct amounts for %d payments (%d intents)", len(seen), n, intents)
	}

	// Fill every offset of another price.
	f.exec(t, `INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key,
		gateway_amount, gateway_currency, pay_address, expires_at)
		SELECT gen_random_uuid(), $1, 'usdt_trc20_auto', 1000, 'IRT', 'succeeded', 'fill-' || g, 2_000_000 + g, 'USDT', $2, now()
		FROM generate_series(1, $3::int) g`, uid, storeAddr, maxChainOffset)
	if _, err := f.svc.StartChain(ctx, chainIntent("full", 2_000_000)); !errors.Is(err, ErrNoUniqueAmount) ||
		!errors.Is(err, ErrPrecondition) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("all offsets taken: %v", err)
	}
	f.exec(t, `UPDATE payments.payment_intents SET created_at = now() - interval '50 hours' WHERE idempotency_key = 'fill-7'`)
	if in, err := f.svc.StartChain(ctx, chainIntent("full", 2_000_000)); err != nil || *in.GatewayAmount != 2_000_007 {
		t.Fatalf("an old payment's amount was not reused: %+v %v", in, err)
	}
	// A price that is not a whole cent: base+1 would be one (what a manual
	// USDT customer sends), so it is skipped.
	if in, err := f.svc.StartChain(ctx, chainIntent("cent", 1_239_999)); err != nil || *in.GatewayAmount != 1_240_001 {
		t.Fatalf("a whole-cent amount was handed out: %+v %v", in, err)
	}
	for name, bad := range map[string]*store.Intent{
		"no amount": func() *store.Intent { in := chainIntent("b1", 1); in.GatewayAmount = nil; return in }(),
		"wrong unit": func() *store.Intent {
			in := chainIntent("b2", 1_000_000)
			irr := "IRR"
			in.GatewayCurrency = &irr
			return in
		}(),
		"too much":      chainIntent("b3", maxChainAmount+1),
		"no idempotent": chainIntent("", 1_000_000),
	} {
		if _, err := f.svc.StartChain(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A payment amount that is somehow not unique settles nothing: the watcher
// never guesses whose money it is.
func TestUSDTAmbiguousAmountSettlesNothing(t *testing.T) {
	f := chainService(t, true)
	in := f.start(t, "a1", 1_500_000)
	f.exec(t, `INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key,
		gateway_amount, gateway_currency, pay_address, expires_at)
		VALUES (gen_random_uuid(), $1, 'usdt_trc20_auto', 1000, 'IRT', 'pending', 'dup', $2, 'USDT', $3, now() + interval '1 hour')`,
		uid, *in.GatewayAmount, storeAddr)
	f.fake.Pay(storeAddr, *in.GatewayAmount)
	f.scan(t)
	if ledgerCount(t, f.st) != 0 || f.intent(t, in.ID).Status != "pending" {
		t.Fatal("an ambiguous amount settled a payment")
	}
	if ev := unmatchedEvents(t, f.st); len(ev) != 1 || ev[0].Reason != UnmatchedAmbiguous {
		t.Fatalf("events: %+v", ev)
	}
}

// The address cannot change while payments to the old one are open; the key
// and the proxy can, and switching off keeps watching the open payments.
// Once they close the address can change, and a late payment to the old one
// still settles.
func TestUSDTAddressChangeRefusedWhileOpen(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	in := f.start(t, "o1", 1_000_000)
	_, err := f.svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: storeAddr2})
	var oe *OpenIntentsError
	if !errors.As(err, &oe) || oe.Count != 1 || !errors.Is(err, ErrPrecondition) || !strings.Contains(err.Error(), "address") {
		t.Fatalf("address change with an open payment: %v", err)
	}
	if c := f.svc.GatewayConfigs()[1]; c.Address != storeAddr || !c.Offered {
		t.Fatalf("settings after a refused change: %+v", c)
	}
	c, err := f.svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: false, APIKey: tronKey})
	if err != nil || c.Enabled || c.Offered || !c.Configured || !c.APIKeySet || c.Address != storeAddr {
		t.Fatalf("switch off with a key: %+v %v", c, err)
	}
	if offers(f.svc, USDTAuto) {
		t.Fatal("still offered when switched off")
	}
	if _, err := f.svc.StartChain(ctx, chainIntent("o2", 1_000_000)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new payment while switched off: %v", err)
	}
	f.fake.RequireKey(tronKey)
	f.fake.Pay(storeAddr, *in.GatewayAmount)
	f.scan(t)
	if f.intent(t, in.ID).Status != "succeeded" || f.fake.LastKey() != tronKey {
		t.Fatal("the open payment was not watched with the new key after switching off")
	}

	// Closed now: the address can change. An expired payment to the old
	// address is still watched for a late payment.
	f.fake.RequireKey("")
	c, err = f.svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: true})
	if err != nil || !c.Offered {
		t.Fatalf("switch on: %+v %v", c, err)
	}
	late := f.start(t, "o3", 1_000_000)
	f.exec(t, `UPDATE payments.payment_intents SET status = 'expired', failure_reason = 'expired' WHERE id = $1`, late.ID)
	if c, err = f.svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: storeAddr2, ClearAPIKey: true}); err != nil ||
		c.Address != storeAddr2 || c.APIKeySet {
		t.Fatalf("address change once closed: %+v %v", c, err)
	}
	next := f.start(t, "o4", 1_000_000)
	if *next.PayAddress != storeAddr2 || *next.GatewayAmount != 1_000_001 {
		t.Fatalf("payment at the new address: %+v", next)
	}
	f.fake.Pay(storeAddr, *late.GatewayAmount)
	f.scan(t)
	if f.intent(t, late.ID).Status != "succeeded" || f.intent(t, next.ID).Status != "pending" {
		t.Fatal("a late payment to the old address did not settle its payment, or settled the new one")
	}
}

// A transaction pays once across the automatic and manual paths, in every
// order, with or without 0x.
func TestUSDTCrossPathTXIDReplayRefused(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	manual := func(key string) *store.Intent {
		t.Helper()
		in, err := f.svc.CreateIntent(ctx, &store.Intent{UserID: uid, Provider: provider.ManualCrypto, Amount: 500_000,
			Currency: "IRT", IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return in
	}

	// 1. The watcher settled it: no manual proof can use it, in any spelling.
	auto := f.start(t, "x1", 2_000_000)
	t1 := f.fake.Pay(storeAddr, *auto.GatewayAmount)
	f.scan(t)
	m1 := manual("xm1")
	for _, spelled := range []string{t1, "0x" + t1, strings.ToUpper(t1), " 0X" + strings.ToUpper(t1) + " "} {
		if _, err := f.svc.SubmitTXID(ctx, uid, m1.ID, "TRC20", spelled); !errors.Is(err, store.ErrDuplicateProof) {
			t.Fatalf("manual proof %q of an automatic payment: %v", spelled, err)
		}
	}

	// 2. Staff approved a manual proof first: the watcher leaves the transfer to it.
	t2 := strings.Repeat("2b", 32)
	m2 := manual("xm2")
	if _, err := f.svc.SubmitTXID(ctx, uid, m2.ID, "TRC20", "0x"+t2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Review(ctx, admin, m2.ID, "approved", ""); err != nil {
		t.Fatal(err)
	}
	auto2 := f.start(t, "x2", 2_000_000)
	f.fake.Add(tronfake.Transfer{TxID: t2, To: storeAddr, Value: itoa(*auto2.GatewayAmount)})
	f.scan(t)
	if f.intent(t, auto2.ID).Status != "pending" || ledgerCount(t, f.st) != 2 {
		t.Fatal("a transfer approved manually also settled an automatic payment")
	}

	// 3. A manual proof is waiting, and the transfer is an automatic
	// payment's exact amount: the automatic payment wins and the proof can no
	// longer be approved (only rejected).
	t3 := strings.Repeat("3c", 32)
	m3 := manual("xm3")
	if _, err := f.svc.SubmitTXID(ctx, uid, m3.ID, "TRC20", t3); err != nil {
		t.Fatal(err)
	}
	f.fake.Add(tronfake.Transfer{TxID: t3, To: storeAddr, Value: itoa(*auto2.GatewayAmount)})
	f.scan(t)
	if f.intent(t, auto2.ID).Status != "succeeded" {
		t.Fatal("the exact automatic payment did not settle")
	}
	if _, err := f.svc.Review(ctx, admin, m3.ID, "approved", ""); !errors.Is(err, ErrTXIDUsed) {
		t.Fatalf("approving a proof the watcher used: %v", err)
	}
	if got, err := f.svc.Review(ctx, admin, m3.ID, "rejected", "paid automatically"); err != nil || got.Status != "failed" {
		t.Fatalf("reject: %+v %v", got, err)
	}
	if ledgerCount(t, f.st) != 3 {
		t.Fatalf("ledger: %d", ledgerCount(t, f.st))
	}

	// 4. An unmatched deposit becomes the proof of the manual payment that
	// sends its TXID (the store uses one address for both).
	t4 := f.fake.Pay(storeAddr, 12_345_678)
	f.scan(t)
	m4 := manual("xm4")
	if _, err := f.svc.SubmitTXID(ctx, uid, m4.ID, "TRC20", t4); err != nil {
		t.Fatalf("manual proof of an unmatched deposit: %v", err)
	}
	if list, _, _ := f.st.ListChainTransfers(ctx, store.ChainTransferFilter{TxID: t4}, 10, 0); len(list) != 1 ||
		list[0].Outcome != store.TransferManual || *list[0].IntentID != m4.ID {
		t.Fatalf("claimed deposit: %+v", list)
	}
	if _, err := f.svc.Review(ctx, admin, m4.ID, "approved", ""); err != nil {
		t.Fatalf("approve the claimed deposit: %v", err)
	}
	if _, err := f.svc.ResolveChainTransfer(ctx, admin, t4, 0, ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("resolving a claimed deposit: %v", err)
	}

	// 5. Staff credited an unmatched deposit by hand: no manual proof can use it.
	t5 := f.fake.Pay(storeAddr, 23_456_789)
	f.scan(t)
	res, err := f.svc.ResolveChainTransfer(ctx, admin, "0x"+strings.ToUpper(t5), 0, "credited by hand")
	if err != nil || res.Outcome != store.TransferResolved || res.ResolvedBy == nil || *res.ResolvedBy != admin || *res.Note != "credited by hand" {
		t.Fatalf("resolve: %+v %v", res, err)
	}
	if _, err := f.svc.SubmitTXID(ctx, uid, manual("xm5").ID, "TRC20", t5); !errors.Is(err, store.ErrDuplicateProof) {
		t.Fatalf("manual proof of a resolved deposit: %v", err)
	}
	if _, err := f.svc.ResolveChainTransfer(ctx, admin, t1, 0, ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("resolving a matched transfer: %v", err)
	}

	// 6. A manual proof sent before the watcher saw an unmatched transfer
	// keeps it: no unmatched announcement.
	t6 := strings.Repeat("6d", 32)
	m6 := manual("xm6")
	if _, err := f.svc.SubmitTXID(ctx, uid, m6.ID, "TRC20", t6); err != nil {
		t.Fatal(err)
	}
	before := len(unmatchedEvents(t, f.st))
	f.fake.Add(tronfake.Transfer{TxID: t6, To: storeAddr, Value: "31000000"})
	f.scan(t)
	if list, _, _ := f.st.ListChainTransfers(ctx, store.ChainTransferFilter{TxID: t6}, 10, 0); len(list) != 1 ||
		list[0].Outcome != store.TransferManual || *list[0].IntentID != m6.ID || len(unmatchedEvents(t, f.st)) != before {
		t.Fatalf("transfer of a waiting manual proof: %+v", list)
	}

	// Rejecting that proof makes the deposit unmatched again, announced:
	// nobody was credited for it.
	if _, err := f.svc.Review(ctx, admin, m6.ID, "rejected", "not ours"); err != nil {
		t.Fatal(err)
	}
	ev := unmatchedEvents(t, f.st)
	if list, _, _ := f.st.ListChainTransfers(ctx, store.ChainTransferFilter{TxID: t6}, 10, 0); len(list) != 1 ||
		list[0].Outcome != store.TransferUnmatched || list[0].IntentID != nil || *list[0].Reason != UnmatchedNoPayment ||
		len(ev) != before+1 || ev[len(ev)-1].TxID != t6 || ev[len(ev)-1].Amount != 31_000_000 {
		t.Fatalf("after rejecting the proof: %+v", list)
	}

	// A proof rejected before its transfer arrives does not hide it either.
	t8 := strings.Repeat("8a", 32)
	m8 := manual("xm9")
	if _, err := f.svc.SubmitTXID(ctx, uid, m8.ID, "TRC20", t8); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Review(ctx, admin, m8.ID, "rejected", "not found on chain"); err != nil {
		t.Fatal(err)
	}
	f.fake.Add(tronfake.Transfer{TxID: t8, To: storeAddr, Value: "32000000"})
	f.scan(t)
	if list, _, _ := f.st.ListChainTransfers(ctx, store.ChainTransferFilter{TxID: t8}, 10, 0); len(list) != 1 ||
		list[0].Outcome != store.TransferUnmatched || len(unmatchedEvents(t, f.st)) != before+2 {
		t.Fatalf("transfer of a rejected proof: %+v", list)
	}

	// The same hash with and without 0x is one manual proof too.
	t7 := strings.Repeat("7e", 32)
	if _, err := f.svc.SubmitTXID(ctx, uid, manual("xm7").ID, "ERC20", "0x"+t7); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SubmitTXID(ctx, uid, manual("xm8").ID, "TRC20", t7); !errors.Is(err, store.ErrDuplicateProof) {
		t.Fatalf("the same hash without 0x: %v", err)
	}
}

// Reads page through TronGrid by fingerprint (never its next URL), record
// every transfer once whatever the pages repeat, move the cursor only past
// what was recorded, and leave a read cut short at its newest block to the
// next one.
func TestUSDTPagingCursorAndRepeats(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	in := f.start(t, "p1", 1_000_000)
	base := time.Now().Add(-time.Minute)
	for i := range 14 {
		f.fake.PayAt(storeAddr, int64(20_000_000+i), base.Add(time.Duration(i)*time.Second))
	}
	f.fake.PayAt(storeAddr, *in.GatewayAmount, base.Add(20*time.Second))
	f.fake.PageSize(1) // 10 pages per read: the first read is cut short
	f.fake.DuplicateAcrossPages(true)
	first := f.scan(t)
	if first == 0 || first >= 15 {
		t.Fatalf("first read recorded %d", first)
	}
	second := f.scan(t)
	if first+second != 15 || len(f.transfers(t)) != 15 || f.intent(t, in.ID).Status != "succeeded" {
		t.Fatalf("recorded %d + %d, log %d", first, second, len(f.transfers(t)))
	}
	cur, err := f.st.GetChainCursor(ctx, storeAddr)
	if err != nil || cur.LastTS.UnixMilli() != base.Add(20*time.Second).UnixMilli() {
		t.Fatalf("cursor: %+v %v", cur, err)
	}
	f.fake.PageSize(0)
	if f.scan(t) != 0 || f.endHolds(t) != 14 || f.scan(t) != 0 || f.endHolds(t) != 0 || len(unmatchedEvents(t, f.st)) != 14 {
		t.Fatal("a repeated read recorded or announced again")
	}
}

// Two transfers to the store in one transaction are two deposits, numbered
// the same way on every read.
func TestUSDTTwoTransfersInOneTransaction(t *testing.T) {
	f := chainService(t, true)
	a := f.start(t, "t1", 1_000_000)
	b := f.start(t, "t2", 1_000_000)
	tx := strings.Repeat("9f", 32)
	at := time.Now()
	f.fake.Add(tronfake.Transfer{TxID: tx, To: storeAddr, Value: itoa(*b.GatewayAmount), BlockTS: at})
	f.fake.Add(tronfake.Transfer{TxID: tx, To: storeAddr, Value: itoa(*a.GatewayAmount), BlockTS: at})
	if n := f.scan(t); n != 2 {
		t.Fatalf("recorded %d", n)
	}
	ga, gb := f.intent(t, a.ID), f.intent(t, b.ID)
	if ga.Status != "succeeded" || gb.Status != "succeeded" || *ga.ExternalID != tx || *gb.ExternalID != tx+"#1" || ledgerCount(t, f.st) != 2 {
		t.Fatalf("batch: %+v %+v", ga, gb)
	}
	if f.scan(t) != 0 {
		t.Fatal("renumbered on a second read")
	}
}

// A read that fails is recorded on the cursor, logged and kept as a gateway
// event once per new error, and cleared by the next read that works.
func TestUSDTReadErrorsAreRecordedOnce(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	f.start(t, "e1", 1_000_000)
	f.fake.Fail(2, 503)
	f.scan(t)
	f.scan(t)
	r, err := f.svc.GatewayReport(ctx, USDTAuto, time.Time{}, time.Time{})
	if err != nil || r.Chain.Cursor == nil || r.Chain.Cursor.LastError == nil || *r.Chain.Cursor.LastError != "trongrid: HTTP 503" ||
		r.LastError == nil || r.LastError.Kind != "scan" || r.LastError.Detail != "trongrid: HTTP 503" {
		t.Fatalf("report after errors: %+v %+v %v", r.Chain.Cursor, r.LastError, err)
	}
	var events int
	if err := f.st.DB().QueryRow(ctx, `SELECT count(*) FROM payments.gateway_events WHERE kind = 'scan' AND outcome = 'error'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("error events: %d %v", events, err)
	}
	f.scan(t)
	if cur, _ := f.st.GetChainCursor(ctx, storeAddr); cur.LastError != nil || cur.OKAt == nil {
		t.Fatalf("after a good read: %+v", cur)
	}
}

// The customer's check reads the payment's address now, settling a payment
// that just arrived, at most every few seconds per address however many
// customers press it.
func TestUSDTCheckReadsNowWithAGap(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	a := f.start(t, "k1", 1_000_000)
	b := f.start(t, "k2", 1_000_000)
	f.fake.Pay(storeAddr, *a.GatewayAmount)
	got, err := f.svc.CheckIntent(ctx, uid, a.ID, "check")
	if err != nil || got.Status != "succeeded" {
		t.Fatalf("check: %+v %v", got, err)
	}
	calls := f.fake.Calls()
	for range 5 {
		if got, err := f.svc.CheckIntent(ctx, uid, b.ID, "check"); err != nil || got.Status != "pending" {
			t.Fatalf("check b: %+v %v", got, err)
		}
	}
	if f.fake.Calls() != calls {
		t.Fatalf("checks within the gap read the chain %d more times", f.fake.Calls()-calls)
	}
	if _, err := f.svc.CheckIntent(ctx, "00000000-0000-7000-8000-00000000dfff", b.ID, "check"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else's payment: %v", err)
	}
	// The reconciler leaves automatic USDT payments to the watcher.
	if n, err := f.svc.ReconcileOnce(ctx, time.Second); err != nil || n != 0 {
		t.Fatalf("reconciler: %d %v", n, err)
	}
}

// Settings: the address is checked (checksum included) and shown, the key
// and the proxy are write-only and never logged; a test reads the chain once
// with stored or candidate settings; nothing is stored without the master key.
func TestUSDTSettingsAndTest(t *testing.T) {
	f := chainService(t, false)
	ctx := context.Background()
	if c := f.svc.GatewayConfigs()[1]; c.Provider != USDTAuto || c.Enabled || c.Configured || c.Address != "" || c.KeyMissing {
		t.Fatalf("before: %+v", c)
	}
	broken := storeAddr[:33] + "j" // one letter off: the checksum fails
	for name, u := range map[string]GatewayUpdate{
		"switch on without an address": {Enabled: true},
		"bad checksum":                 {Address: broken},
		"too short":                    {Address: "TCKR37NfLumfScqBQX6qrnJR3mZkv6xGq"},
		"the USDT contract":            {Address: trongrid.USDTContract},
		"bad api key":                  {Address: storeAddr, APIKey: "key with spaces"},
		"key and clear":                {Address: storeAddr, APIKey: tronKey, ClearAPIKey: true},
		"bad proxy":                    {Address: storeAddr, Proxy: "socks5://relay:pr0xy-pw@203.0.113.10:1080"},
		"sandbox":                      {Address: storeAddr, Sandbox: true},
		"actor":                        {Address: storeAddr, ActorID: "staff"},
	} {
		u.Provider = USDTAuto
		if _, err := f.svc.SetGatewayConfig(ctx, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	var logs bytes.Buffer
	f.svc.SetLogger(slog.New(slog.NewTextHandler(&lockedWriter{w: &logs}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	c, err := f.svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: storeAddr, APIKey: tronKey,
		Proxy: proxyURL, ActorID: actor})
	if err != nil || !c.Enabled || c.Address != storeAddr || !c.APIKeySet || !c.ProxySet || c.UpdatedBy != actor || c.Problem != "" {
		t.Fatalf("set: %+v %v", c, err)
	}
	assertNoSecrets(t, "logs", logs.String())
	if strings.Contains(logs.String(), tronKey) {
		t.Fatalf("the API key was logged: %s", logs.String())
	}
	row, err := f.st.GetGatewayConfig(ctx, nil, USDTAuto)
	if err != nil || strings.Contains(string(row.AddressEnc), storeAddr) || strings.Contains(string(row.APIKeyEnc), tronKey) {
		t.Fatal("settings stored in the clear")
	}

	// The stored proxy is unreachable: the test fails, without its address.
	r, err := f.svc.TestGateway(ctx, USDTAuto, nil)
	if err != nil || r.OK || r.Detail == "" || strings.Contains(r.Detail, "203.0.113.10") || strings.Contains(r.Detail, "pr0xy") {
		t.Fatalf("test through a dead proxy: %+v %v", r, err)
	}
	// A candidate without the proxy, with a key the fake refuses, then accepts.
	f.fake.RequireKey(tronKey2)
	if r, err := f.svc.TestGateway(ctx, USDTAuto, &GatewayCandidate{ClearProxy: true}); err != nil || r.OK || !strings.Contains(r.Detail, "API key") {
		t.Fatalf("test with a refused key: %+v %v", r, err)
	}
	calls := f.fake.Calls()
	if r, err := f.svc.TestGateway(ctx, USDTAuto, &GatewayCandidate{ClearProxy: true, APIKey: tronKey2}); err != nil || !r.OK || f.fake.Calls() != calls+1 {
		t.Fatalf("test with a good key: %+v %v (calls %d)", r, err, f.fake.Calls()-calls)
	}
	if got := f.svc.GatewayConfigs()[1]; !got.APIKeySet || !got.ProxySet {
		t.Fatal("a test changed the settings")
	}
	if _, err := f.svc.TestGateway(ctx, USDTAuto, &GatewayCandidate{Address: broken}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("test of a bad address: %v", err)
	}

	// Another master key: the settings are unreadable until entered again.
	other := New(f.st)
	o := f.opts
	o.Envelope = envelope(t, keyB)
	if err := other.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if c := other.GatewayConfigs()[1]; c.Problem != ProblemUnreadable || c.Offered || c.Address != "" {
		t.Fatalf("unreadable: %+v", c)
	}
	if _, err := other.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unreadable, address not entered again: %v", err)
	}
	if c, err := other.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: storeAddr}); err != nil || !c.Offered || c.APIKeySet || c.ProxySet {
		t.Fatalf("entered again: %+v %v", c, err)
	}
	// No master key: read-only.
	none := New(f.st)
	o.Envelope = nil
	if err := none.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := none.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Address: storeAddr}); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("no master key: %v", err)
	}
	// No watcher built into this install (tests that never configure it).
	if _, err := New(f.st).SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Address: storeAddr}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("no watcher: %v", err)
	}
}

// Switching automatic USDT on again does not report what the address got
// while nobody watched it as unmatched deposits.
func TestUSDTSwitchingOnSkipsUnwatchedHistory(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	f.scan(t)
	if _, err := f.svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	f.fake.PayAt(storeAddr, 9_000_000, time.Now().Add(-time.Hour)) // while off
	f.exec(t, `UPDATE payments.chain_cursors SET last_ts = now() - interval '2 hours'`)
	if f.scan(t) != 0 {
		t.Fatal("read while switched off with nothing open")
	}
	if _, err := f.svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: USDTAuto, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if f.scan(t) != 0 || len(unmatchedEvents(t, f.st)) != 0 {
		t.Fatal("old transfers reported as unmatched deposits")
	}
	f.fake.Pay(storeAddr, 9_000_000)
	if f.scan(t) != 1 {
		t.Fatal("a new deposit was missed")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// A transfer that cannot be recorded stops the read there and is read again
// next time, also when it showed up late (older than the cursor) in a read
// cut short at its page limit.
func TestUSDTRecordErrorIsReadAgain(t *testing.T) {
	f := chainService(t, true)
	base := time.Now().Add(-90 * time.Second)
	f.fake.PayAt(storeAddr, 30_000_000, base)
	if f.scan(t) != 1 {
		t.Fatal("first deposit not recorded")
	}
	poisoned := strings.Repeat("5a", 32)
	f.exec(t, `CREATE OR REPLACE FUNCTION payments.refuse_poisoned() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.txid = '`+poisoned+`' THEN RAISE EXCEPTION 'refused'; END IF; RETURN NEW; END $$`)
	f.exec(t, `CREATE TRIGGER refuse_poisoned BEFORE INSERT ON payments.chain_transfers
		FOR EACH ROW EXECUTE FUNCTION payments.refuse_poisoned()`)
	t.Cleanup(func() {
		_, _ = f.st.DB().Exec(context.Background(), `DROP TRIGGER IF EXISTS refuse_poisoned ON payments.chain_transfers;
			DROP FUNCTION IF EXISTS payments.refuse_poisoned()`)
	})
	// Indexed late: its block is older than the newest one read.
	f.fake.Add(tronfake.Transfer{TxID: poisoned, To: storeAddr, Value: "31000000", BlockTS: base.Add(-30 * time.Second)})
	// Later ones, spread so the cursor ends far past the late one's block.
	for i := 1; i <= 12; i++ {
		f.fake.PayAt(storeAddr, int64(40_000_000+i), base.Add(time.Duration(i)*15*time.Second))
	}
	f.fake.PageSize(1) // every read is cut short
	if _, err := f.svc.ScanChainOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.transfers(t)); n != 1 {
		t.Fatalf("recorded past a transfer that failed: %d", n)
	}
	if cur, _ := f.st.GetChainCursor(context.Background(), storeAddr); cur.LastError == nil || !strings.Contains(*cur.LastError, poisoned) {
		t.Fatalf("the failure is not on the cursor: %+v", cur)
	}
	f.exec(t, `DROP TRIGGER refuse_poisoned ON payments.chain_transfers`)
	for range 5 {
		f.scan(t)
	}
	list := f.transfers(t)
	found := false
	for _, tr := range list {
		found = found || tr.TxID == poisoned
	}
	if len(list) != 14 || !found {
		t.Fatalf("after the failure: %d transfers, late one recorded %v", len(list), found)
	}
}

// manual is a manual crypto payment of 500,000 Toman.
func (f *chainFix) manual(t *testing.T, key string) *store.Intent {
	t.Helper()
	in, err := f.svc.CreateIntent(context.Background(), &store.Intent{UserID: uid, Provider: provider.ManualCrypto,
		Amount: 500_000, Currency: "IRT", IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func claimedEvents(t *testing.T, st *store.Store) []DepositClaimedEvent {
	t.Helper()
	rows, err := st.DB().Query(context.Background(), `SELECT payload FROM payments.outbox_payments WHERE topic = $1 ORDER BY id`, TopicDepositClaimed)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []DepositClaimedEvent
	for rows.Next() {
		var (
			raw []byte
			e   DepositClaimedEvent
		)
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (f *chainFix) transfer(t *testing.T, txid string) store.ChainTransfer {
	t.Helper()
	list, _, err := f.st.ListChainTransfers(context.Background(), store.ChainTransferFilter{TxID: txid}, 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("transfer %s: %+v %v", txid, list, err)
	}
	return list[0]
}

// A block time the watcher does not believe (far ahead from a broken
// indexer, or hours ahead because this server's clock is behind) moves
// nothing: the cursor stays at what was read, the reads say why, and a
// payment made later is still read and settles.
func TestUSDTRefusedBlockTimesDoNotMoveTheCursor(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	in := f.start(t, "bt1", 7_000_000)
	f.fake.Pay(storeAddr, 8_000_000) // somebody's deposit, now
	far := f.fake.Add(tronfake.Transfer{To: storeAddr, Value: "9000000", BlockTS: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)})
	ahead := f.fake.Add(tronfake.Transfer{To: storeAddr, Value: "9100000", BlockTS: time.Now().Add(2 * time.Hour)})
	if n := f.scan(t); n != 1 {
		t.Fatalf("recorded %d", n)
	}
	cur, err := f.st.GetChainCursor(ctx, storeAddr)
	if err != nil || cur.LastTS.After(time.Now()) || cur.LastError == nil || !strings.Contains(*cur.LastError, "clock") {
		t.Fatalf("cursor after refused block times: %+v %v", cur, err)
	}
	r, err := f.svc.GatewayReport(ctx, USDTAuto, time.Time{}, time.Time{})
	if err != nil || r.LastError == nil || r.LastError.Kind != "scan" || !strings.Contains(r.LastError.Detail, "clock") {
		t.Fatalf("report: %+v %v", r.LastError, err)
	}
	f.fake.Pay(storeAddr, *in.GatewayAmount)
	f.scan(t)
	if got := f.intent(t, in.ID); got.Status != "succeeded" || ledgerCount(t, f.st) != 1 {
		t.Fatalf("a later payment was skipped: %+v", got)
	}
	for _, tr := range f.transfers(t) {
		if tr.TxID == far || tr.TxID == ahead {
			t.Fatalf("recorded a transfer from the future: %+v", tr)
		}
	}
}

// A page that fails after good ones keeps what they gave: recorded, the
// cursor moved on, the error on the cursor; nothing expires (the read did not
// see everything); the next read picks up the rest.
func TestUSDTPageErrorKeepsTheGoodPages(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	due := f.start(t, "pg1", 6_000_000)
	f.exec(t, `UPDATE payments.payment_intents SET created_at = now() - interval '2 hours',
		expires_at = now() - interval '75 minutes' WHERE id = $1`, due.ID)
	base := time.Now().Add(-time.Minute)
	for i := range 5 {
		f.fake.PayAt(storeAddr, int64(60_000_000+i), base.Add(time.Duration(i)*time.Second))
	}
	f.fake.PageSize(1)
	f.fake.FailAfter(2, 1, 503) // pages 1 and 2 come, page 3 fails
	if _, err := f.svc.ScanChainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(f.transfers(t)); n != 1 { // page 2's block may go on: left to the next read
		t.Fatalf("recorded %d from the good pages", n)
	}
	cur, err := f.st.GetChainCursor(ctx, storeAddr)
	if err != nil || cur.LastTS.UnixMilli() != base.UnixMilli() || cur.LastError == nil || *cur.LastError != "trongrid: HTTP 503" {
		t.Fatalf("cursor: %+v %v", cur, err)
	}
	if f.intent(t, due.ID).Status != "pending" {
		t.Fatal("expired after a read that failed partway")
	}
	f.fake.PageSize(0)
	f.scan(t)
	if n := len(f.transfers(t)); n != 5 || f.intent(t, due.ID).Status != "expired" {
		t.Fatalf("after a complete read: %d transfers, %s", n, f.intent(t, due.ID).Status)
	}
	if cur, _ := f.st.GetChainCursor(ctx, storeAddr); cur.LastError != nil {
		t.Fatalf("error kept after a good read: %s", *cur.LastError)
	}
}

// The store's address may take manual USDT too: a manual customer pays first
// and sends the TXID minutes later. Until then the deposit is held: staff
// are not told to credit it by hand, nor see it in the report. Claimed, it
// is the proof's, and staff are told of the claim instead; unclaimed, it is
// announced once the hold ends, once.
func TestUSDTUnmatchedDepositHeldForAManualProof(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	m := f.manual(t, "hm1")
	paid := f.fake.Pay(storeAddr, 25_000_000)
	other := f.fake.Pay(storeAddr, 26_000_000)
	f.scan(t)
	r, err := f.svc.GatewayReport(ctx, USDTAuto, time.Time{}, time.Time{})
	if err != nil || len(unmatchedEvents(t, f.st)) != 0 || r.Chain.UnmatchedCount != 0 {
		t.Fatalf("announced or reported at once: %+v %v", r.Chain, err)
	}
	if _, err := f.svc.SubmitTXID(ctx, uid, m.ID, "TRC20", paid); err != nil {
		t.Fatal(err)
	}
	claims := claimedEvents(t, f.st)
	if len(claims) != 1 || claims[0].TxID != paid || claims[0].IntentID != m.ID || claims[0].Proof != "waiting" ||
		claims[0].Amount != 25_000_000 || claims[0].Reason != UnmatchedNoPayment || claims[0].From != tronfake.Sender {
		t.Fatalf("claim: %+v", claims)
	}
	if tr := f.transfer(t, paid); tr.Outcome != store.TransferManual || tr.AnnounceAt != nil {
		t.Fatalf("claimed deposit: %+v", tr)
	}
	if n := f.endHolds(t); n != 1 {
		t.Fatalf("announced %d once the hold ended", n)
	}
	ev := unmatchedEvents(t, f.st)
	r, err = f.svc.GatewayReport(ctx, USDTAuto, time.Time{}, time.Time{})
	if err != nil || len(ev) != 1 || ev[0].TxID != other || ev[0].Reason != UnmatchedNoPayment ||
		r.Chain.UnmatchedCount != 1 || r.Chain.Unmatched[0].TxID != other {
		t.Fatalf("after the hold: %+v %+v %v", ev, r.Chain, err)
	}
	f.scan(t)
	if f.endHolds(t) != 0 || len(unmatchedEvents(t, f.st)) != 1 || len(claimedEvents(t, f.st)) != 1 {
		t.Fatal("announced twice")
	}
	if _, err := f.svc.Review(ctx, admin, m.ID, "approved", ""); err != nil || ledgerCount(t, f.st) != 1 {
		t.Fatalf("approve the claimed proof: %v (ledger %d)", err, ledgerCount(t, f.st))
	}
	// The hold can be shortened, e.g. where manual USDT is not used.
	f.svc.SetUnmatchedHold(0)
	quick := f.fake.Pay(storeAddr, 27_000_000)
	f.scan(t)
	if ev := unmatchedEvents(t, f.st); len(ev) != 2 || ev[1].TxID != quick {
		t.Fatalf("without a hold: %+v", ev)
	}
}

// The TXID of a transfer to the store is public. A manual proof carrying
// someone's inexact deposit takes it only in plain sight (staff are told of
// the claim). A transfer carrying an automatic payment's unique amount is the
// automatic path's: a proof of it is refused, cannot be approved, or (one
// approved before the watcher saw it) is announced at once as an automatic
// payer nobody credited; it never also settles the automatic payment.
func TestUSDTManualProofCannotQuietlyTakeAnAutomaticDeposit(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()

	// The victim's exchange took its fee; the attacker sends its TXID first.
	victim := f.start(t, "v1", 10_000_000)
	feeTx := strings.Repeat("a1", 32)
	if _, err := f.svc.SubmitTXID(ctx, uid, f.manual(t, "a1").ID, "TRC20", feeTx); err != nil {
		t.Fatal(err)
	}
	f.fake.Add(tronfake.Transfer{TxID: feeTx, To: storeAddr, Value: itoa(*victim.GatewayAmount - 1_000_000)})
	f.scan(t)
	if c := claimedEvents(t, f.st); len(c) != 1 || c[0].TxID != feeTx || c[0].Proof != "waiting" || c[0].Reason != UnmatchedNoPayment ||
		c[0].Amount != *victim.GatewayAmount-1_000_000 {
		t.Fatalf("claim of an inexact deposit: %+v", c)
	}

	// Approved before the watcher saw it, with an automatic payment's exact
	// amount: not settled twice, announced at once.
	victim2 := f.start(t, "v2", 11_000_000)
	exactTx := strings.Repeat("b2", 32)
	a2 := f.manual(t, "a2")
	if _, err := f.svc.SubmitTXID(ctx, uid, a2.ID, "TRC20", exactTx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Review(ctx, admin, a2.ID, "approved", ""); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerCount(t, f.st)
	f.fake.Add(tronfake.Transfer{TxID: exactTx, To: storeAddr, Value: itoa(*victim2.GatewayAmount)})
	f.scan(t)
	if f.intent(t, victim2.ID).Status != "pending" || ledgerCount(t, f.st) != ledger {
		t.Fatal("one transfer credited twice")
	}
	ev := unmatchedEvents(t, f.st)
	if tr := f.transfer(t, exactTx); len(ev) != 1 || ev[0].TxID != exactTx || ev[0].Reason != UnmatchedShadowed ||
		tr.Outcome != store.TransferUnmatched || tr.IntentID != nil || tr.AnnounceAt != nil {
		t.Fatalf("shadowed automatic payment: %+v %+v", ev, tr)
	}
	if _, err := f.svc.ResolveChainTransfer(ctx, admin, exactTx, 0, "credited the automatic payer by hand"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// A second transfer of a paid automatic amount: no proof may take it.
	victim3 := f.start(t, "v3", 12_000_000)
	f.fake.Pay(storeAddr, *victim3.GatewayAmount)
	f.scan(t)
	second := f.fake.Pay(storeAddr, *victim3.GatewayAmount)
	f.scan(t)
	if ev := unmatchedEvents(t, f.st); len(ev) != 2 || ev[1].TxID != second || ev[1].Reason != UnmatchedAlreadyPaid {
		t.Fatalf("second transfer: %+v", ev)
	}
	_, err := f.svc.SubmitTXID(ctx, uid, f.manual(t, "a3").ID, "TRC20", second)
	if !errors.Is(err, ErrTXIDAutomatic) || !errors.Is(err, ErrPrecondition) {
		t.Fatalf("proof of an automatic deposit: %v", err)
	}

	// Nor can a proof sent before such a transfer was seen be approved.
	lateTx := strings.Repeat("c4", 32)
	a4 := f.manual(t, "a4")
	if _, err := f.svc.SubmitTXID(ctx, uid, a4.ID, "TRC20", lateTx); err != nil {
		t.Fatal(err)
	}
	f.fake.Add(tronfake.Transfer{TxID: lateTx, To: storeAddr, Value: itoa(*victim3.GatewayAmount)})
	f.scan(t)
	if tr := f.transfer(t, lateTx); tr.Outcome != store.TransferUnmatched || *tr.Reason != UnmatchedAlreadyPaid {
		t.Fatalf("a waiting proof took an automatic deposit: %+v", tr)
	}
	if _, err := f.svc.Review(ctx, admin, a4.ID, "approved", ""); !errors.Is(err, ErrTXIDAutomatic) {
		t.Fatalf("approving a proof of an automatic deposit: %v", err)
	}
	if _, err := f.svc.Review(ctx, admin, a4.ID, "rejected", "an automatic payment's transfer"); err != nil {
		t.Fatal(err)
	}
	if len(claimedEvents(t, f.st)) != 1 {
		t.Fatalf("claims: %+v", claimedEvents(t, f.st))
	}
}

// One customer cannot hold every unique amount of a price: asking again for
// the same payment shows the one still open, and a customer may hold at most
// chainUserUnpaid unpaid automatic payments from the last 2 days; paid ones
// do not count, and other customers are not affected.
func TestUSDTUnpaidPaymentsAreLimitedPerCustomer(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	topup := func(key string, amount int64) *store.Intent {
		usdt, base := "USDT", int64(5_000_000)
		return &store.Intent{UserID: uid, Provider: USDTAuto, Amount: amount, Currency: "IRT", IdempotencyKey: key,
			GatewayAmount: &base, GatewayCurrency: &usdt}
	}
	first, err := f.svc.StartChain(ctx, topup("top:1:500000:a", 500_000))
	if err != nil {
		t.Fatal(err)
	}
	// A new menu (a new nonce): the payment still open, its amount kept.
	if again, err := f.svc.StartChain(ctx, topup("top:1:500000:b", 500_000)); err != nil || again.ID != first.ID ||
		*again.GatewayAmount != *first.GatewayAmount {
		t.Fatalf("asked again: %+v %v", again, err)
	}
	// Near its end it is not shown again.
	f.exec(t, `UPDATE payments.payment_intents SET expires_at = now() + interval '5 minutes' WHERE id = $1`, first.ID)
	fresh, err := f.svc.StartChain(ctx, topup("top:1:500000:c", 500_000))
	if err != nil || fresh.ID == first.ID || *fresh.GatewayAmount == *first.GatewayAmount {
		t.Fatalf("near its end: %+v %v", fresh, err)
	}
	for i := 2; i < chainUserUnpaid; i++ {
		if _, err := f.svc.StartChain(ctx, topup(fmt.Sprintf("top:1:%d:x", 500_000+i), int64(500_000+i))); err != nil {
			t.Fatalf("payment %d: %v", i+1, err)
		}
	}
	if _, err := f.svc.StartChain(ctx, topup("over", 600_000)); !errors.Is(err, ErrTooManyUnpaid) || !errors.Is(err, ErrPrecondition) {
		t.Fatalf("over the limit: %v", err)
	}
	// A retry of one already made still answers.
	if again, err := f.svc.StartChain(ctx, topup("top:1:500000:c", 500_000)); err != nil || again.ID != fresh.ID {
		t.Fatalf("retry over the limit: %+v %v", again, err)
	}
	other := topup("other", 600_000)
	other.UserID = keyUUID("user", "other")
	if _, err := f.svc.StartChain(ctx, other); err != nil {
		t.Fatalf("another customer: %v", err)
	}
	f.exec(t, `UPDATE payments.payment_intents SET status = 'succeeded' WHERE id = $1`, first.ID)
	if _, err := f.svc.StartChain(ctx, topup("over", 600_000)); err != nil {
		t.Fatalf("after paying one: %v", err)
	}
}

// Staff write in Persian: the note on a handled deposit and the reason of a
// review are limited to 500 characters, not bytes.
func TestUSDTNotesCountCharacters(t *testing.T) {
	f := chainService(t, true)
	ctx := context.Background()
	a := f.fake.Pay(storeAddr, 14_000_000)
	b := f.fake.Pay(storeAddr, 15_000_000)
	f.scan(t)
	note := strings.Repeat("ب", 300) // 600 bytes
	if res, err := f.svc.ResolveChainTransfer(ctx, admin, a, 0, note); err != nil || res.Note == nil || *res.Note != note {
		t.Fatalf("a 300-character note: %+v %v", res, err)
	}
	if _, err := f.svc.ResolveChainTransfer(ctx, admin, b, 0, strings.Repeat("ب", maxReasonLen+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a note over the limit: %v", err)
	}
	m1, m2 := f.manual(t, "rn1"), f.manual(t, "rn2")
	for _, m := range []*store.Intent{m1, m2} {
		if _, err := f.svc.SubmitReceipt(ctx, uid, m.ID, "photo-"+m.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Review(ctx, admin, m1.ID, "rejected", note); err != nil {
		t.Fatalf("a 300-character reason: %v", err)
	}
	if _, err := f.svc.Review(ctx, admin, m2.ID, "rejected", strings.Repeat("ب", maxReasonLen+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a reason over the limit: %v", err)
	}
}
