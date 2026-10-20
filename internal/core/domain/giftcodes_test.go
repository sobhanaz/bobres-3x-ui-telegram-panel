package domain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// giftEnv is a store with an admin to make codes and a log that is read at the end.
type giftEnv struct {
	t     *testing.T
	svc   *Service
	s     *store.Store
	admin *store.User
	logs  *bytes.Buffer
	n     atomic.Int64
}

func newGiftEnv(t *testing.T) *giftEnv {
	t.Helper()
	svc, s := testService(t)
	e := &giftEnv{t: t, svc: svc, s: s, admin: staffMember(t, s, 9300, "admin"), logs: &bytes.Buffer{}}
	svc.cfg.Log = slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return e
}

// spec is a code worth 1000 Toman, once in all, once per customer.
func spec() GiftCodeSpec {
	return GiftCodeSpec{Amount: 1000, Currency: "IRT", MaxRedemptions: 1}
}

func (e *giftEnv) make(sp GiftCodeSpec) []NewGiftCode {
	e.t.Helper()
	codes, err := e.svc.CreateGiftCodes(context.Background(), e.admin, sp)
	if err != nil {
		e.t.Fatalf("create gift codes: %v", err)
	}
	return codes
}

func (e *giftEnv) one(sp GiftCodeSpec) NewGiftCode { e.t.Helper(); return e.make(sp)[0] }

func (e *giftEnv) key() string { return fmt.Sprintf("req-%d", e.n.Add(1)) }

func (e *giftEnv) redeem(u *store.User, code string) (*GiftRedeemed, error) {
	return e.svc.RedeemGiftCode(context.Background(), RedeemGiftCodeParams{UserID: u.ID, Code: code, RequestKey: e.key()})
}

func (e *giftEnv) balance(u *store.User, currency string) int64 {
	e.t.Helper()
	w, err := e.s.GetWallet(context.Background(), e.s.Conn(), u.ID, currency)
	if err != nil {
		e.t.Fatal(err)
	}
	return w.Balance
}

func (e *giftEnv) sql(q string, args ...any) {
	e.t.Helper()
	if _, err := e.s.DB().Exec(context.Background(), q, args...); err != nil {
		e.t.Fatalf("%v\n%s", err, q)
	}
}

func (e *giftEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.s.DB().QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%v\n%s", err, q)
	}
	return n
}

func (e *giftEnv) giftCredits() int {
	return e.count(`SELECT count(*) FROM core.ledger_entries WHERE kind = 'gift_code'`)
}

// outcome names what a redemption came to: "ok", "replay", or the reason it was refused.
func outcome(r *GiftRedeemed, err error) string {
	var ge *GiftCodeError
	switch {
	case errors.As(err, &ge):
		return ge.Reason
	case err != nil:
		return "error: " + err.Error()
	case r.Replayed:
		return "replay"
	}
	return "ok"
}

func mustEnvelope(t *testing.T, key string) *bcrypto.Envelope {
	t.Helper()
	env, err := bcrypto.NewEnvelope([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// A code is never in the database, in an audit entry, in an event or in a log: only a keyed hash of it and its
// last four characters. Whoever holds a dump sees neither a code nor anything to try one against.
func TestGiftCodesAreNeverStoredInClear(t *testing.T) {
	e := newGiftEnv(t)
	ctx := context.Background()
	batch := e.make(GiftCodeSpec{Amount: 5000, Currency: "IRT", MaxRedemptions: 3, Count: 4, Prefix: "spring", Note: "launch"})
	own := e.one(GiftCodeSpec{Amount: 5000, Currency: "IRT", MaxRedemptions: 3, Code: "nowruz-1405"})
	all := append(batch, own)
	cust := seedUser(t, e.s, 9301)
	for _, c := range all[:2] {
		if got := outcome(e.redeem(cust, c.Code)); got != "ok" && got != GiftAlreadyRedeemed {
			t.Fatalf("redeem: %s", got)
		}
	}
	e.redeem(seedUser(t, e.s, 9302), "wrong-code-ABCD-9999") // a wrong one too

	dump := &strings.Builder{}
	for _, q := range []string{
		`SELECT coalesce(string_agg(t::text, E'\n'), '') FROM core.gift_codes t`,
		`SELECT coalesce(string_agg(t::text, E'\n'), '') FROM core.gift_code_redemptions t`,
		`SELECT coalesce(string_agg(t::text, E'\n'), '') FROM core.gift_code_failures t`,
		`SELECT coalesce(string_agg(t::text, E'\n'), '') FROM core.audit_log t`,
		`SELECT coalesce(string_agg(t::text, E'\n'), '') FROM core.ledger_entries t`,
		`SELECT coalesce(string_agg(t::text, E'\n'), '') FROM core.outbox_core t`,
	} {
		var part string
		if err := e.s.DB().QueryRow(ctx, q).Scan(&part); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
		dump.WriteString(part)
	}
	everything := strings.ToUpper(dump.String() + e.logs.String())
	for _, c := range all {
		canon, ok := normalizeGiftCode(c.Code)
		if !ok {
			t.Fatalf("%q is not a code", c.Code)
		}
		for _, form := range []string{canon, c.Code, strings.ReplaceAll(c.Code, "-", "")} {
			if strings.Contains(everything, strings.ToUpper(form)) {
				t.Errorf("the code %q is in the database, an audit entry, an event or a log", form)
			}
		}
		// What is stored: the keyed hash, and the last four characters.
		var hash []byte
		var tail string
		if err := e.s.DB().QueryRow(ctx, `SELECT code_hash, code_tail FROM core.gift_codes WHERE id = $1`, c.ID).Scan(&hash, &tail); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(hash, e.svc.giftHash(canon)) || len(hash) != 32 || bytes.Contains(hash, []byte(canon)) {
			t.Errorf("the stored hash of %q is not the keyed hash", c.Code)
		}
		if tail != canon[len(canon)-4:] || c.Tail != tail {
			t.Errorf("tail %q for %q", tail, c.Code)
		}
	}
	// The hash is keyed: the same code under another master key is another hash, and an unkeyed SHA-256 does not match.
	other := New(e.s, Config{Secrets: mustEnvelope(t, "another-master-key-0123456789abcdef")})
	canon, _ := normalizeGiftCode(own.Code)
	if bytes.Equal(other.giftHash(canon), e.svc.giftHash(canon)) {
		t.Error("two master keys hash a code alike")
	}
	for _, plain := range []string{canon, giftHashContext + canon} {
		sum := sha256.Sum256([]byte(plain))
		if bytes.Equal(sum[:], e.svc.giftHash(canon)) {
			t.Errorf("the stored hash is a plain SHA-256 of %q: anyone can try codes against it", plain)
		}
	}
}

func TestGiftCodeCreationRules(t *testing.T) {
	e := newGiftEnv(t)
	ctx := context.Background()
	future := time.Now().Add(48 * time.Hour)
	past := time.Now().Add(-time.Hour)
	far := time.Now().Add(11 * 365 * 24 * time.Hour)
	bad := func(name, field string, sp GiftCodeSpec) {
		t.Helper()
		_, err := e.svc.CreateGiftCodes(ctx, e.admin, sp)
		var fe *FieldError
		if !errors.As(err, &fe) || !errors.Is(err, ErrInvalid) || (field != "" && fe.Field != field) {
			t.Errorf("%s: want a refusal of %q, got %v", name, field, err)
		}
	}
	with := func(f func(*GiftCodeSpec)) GiftCodeSpec { sp := spec(); f(&sp); return sp }

	bad("amount 0", "amount", with(func(s *GiftCodeSpec) { s.Amount = 0 }))
	bad("a negative amount", "amount", with(func(s *GiftCodeSpec) { s.Amount = -1 }))
	bad("above the largest top-up", "amount", with(func(s *GiftCodeSpec) { s.Amount = maxTopupMinor + 1 }))
	bad("the largest int64", "amount", with(func(s *GiftCodeSpec) { s.Amount = math.MaxInt64 }))
	bad("no currency", "currency", with(func(s *GiftCodeSpec) { s.Currency = "" }))
	bad("a currency that is not sold", "currency", with(func(s *GiftCodeSpec) { s.Currency = "EUR" }))
	bad("no redemptions", "max_redemptions", with(func(s *GiftCodeSpec) { s.MaxRedemptions = 0 }))
	bad("a negative count of redemptions", "max_redemptions", with(func(s *GiftCodeSpec) { s.MaxRedemptions = -5 }))
	bad("too many redemptions", "max_redemptions", with(func(s *GiftCodeSpec) { s.MaxRedemptions = maxGiftRedemptions + 1 }))
	bad("a huge count of redemptions", "max_redemptions", with(func(s *GiftCodeSpec) { s.MaxRedemptions = math.MaxInt32 }))
	bad("more per customer than in all", "per_customer_limit", with(func(s *GiftCodeSpec) { s.MaxRedemptions, s.PerCustomerLimit = 2, 3 }))
	bad("a negative limit per customer", "per_customer_limit", with(func(s *GiftCodeSpec) { s.PerCustomerLimit = -1 }))
	bad("too many per customer", "per_customer_limit", with(func(s *GiftCodeSpec) { s.MaxRedemptions, s.PerCustomerLimit = 1000, maxGiftPerCustomer+1 }))
	bad("an expiry in the past", "expires_at", with(func(s *GiftCodeSpec) { s.ExpiresAt = &past }))
	bad("an expiry ten years away and more", "expires_at", with(func(s *GiftCodeSpec) { s.ExpiresAt = &far }))
	bad("a long note", "note", with(func(s *GiftCodeSpec) { s.Note = strings.Repeat("x", maxGiftNote+1) }))
	bad("a note with a line break", "note", with(func(s *GiftCodeSpec) { s.Note = "a\nb" }))
	bad("a note with a control character", "note", with(func(s *GiftCodeSpec) { s.Note = "a" + runes(0x07) }))
	bad("a note that is not text", "note", with(func(s *GiftCodeSpec) { s.Note = "a\xffb" }))
	bad("a batch of 501", "count", with(func(s *GiftCodeSpec) { s.Count = maxGiftBatch + 1 }))
	bad("a negative batch", "count", with(func(s *GiftCodeSpec) { s.Count = -2 }))
	bad("a prefix that is too long", "prefix", with(func(s *GiftCodeSpec) { s.Prefix = "NOWRUZ1405" }))
	bad("a prefix with a symbol", "prefix", with(func(s *GiftCodeSpec) { s.Prefix = "A$" }))
	bad("a code of your own that is short", "code", with(func(s *GiftCodeSpec) { s.Code = "SHORT1" }))
	bad("a code of your own with five different characters at most", "code", with(func(s *GiftCodeSpec) { s.Code = "ABABABABAB" }))
	bad("a code of your own made of symbols", "code", with(func(s *GiftCodeSpec) { s.Code = "GIFT*2026" }))
	bad("a batch of codes of your own", "count", with(func(s *GiftCodeSpec) { s.Code, s.Count = "NOWRUZ1405", 2 }))
	bad("a prefix on a code of your own", "prefix", with(func(s *GiftCodeSpec) { s.Code, s.Prefix = "NOWRUZ1405", "AB" }))
	if n := e.count(`SELECT count(*) FROM core.gift_codes`); n != 0 {
		t.Fatalf("%d codes were made by refused requests", n)
	}

	// The largest terms that are allowed, and a currency in any case.
	big := e.one(GiftCodeSpec{Amount: maxTopupMinor, Currency: "usdt", MaxRedemptions: maxGiftRedemptions, PerCustomerLimit: maxGiftPerCustomer, ExpiresAt: &future})
	if big.Amount != maxTopupMinor || big.Currency != "USDT" || big.MaxRedemptions != maxGiftRedemptions || big.PerCustomerLimit != maxGiftPerCustomer {
		t.Fatalf("the largest terms: %+v", big.GiftCode)
	}
	if total := big.Amount * int64(big.MaxRedemptions); total/int64(big.MaxRedemptions) != big.Amount || total < 0 {
		t.Fatalf("the largest terms overflow an int64 in all: %d", total)
	}

	// A code of your own cannot be made twice, in any spelling.
	e.one(GiftCodeSpec{Amount: 1, Currency: "IRT", MaxRedemptions: 1, Code: "Nowruz-1405"})
	for _, again := range []string{"NOWRUZ-1405", "nowruz1405", " nowruz 1405 "} {
		sp := spec()
		sp.Code = again
		_, err := e.svc.CreateGiftCodes(ctx, e.admin, sp)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != "code" {
			t.Errorf("%q made twice: %v", again, err)
		}
	}
	// A batch is all or nothing: a refused code of your own leaves no half batch.
	before := e.count(`SELECT count(*) FROM core.gift_codes`)

	// Generated codes: the shape, uniqueness, the prefix, the amount and the limits as asked, one audit entry each.
	batch := e.make(GiftCodeSpec{Amount: 250_000, Currency: "IRT", MaxRedemptions: 10, PerCustomerLimit: 2, Count: 50, Prefix: "gift", Note: " spring giveaway ", ExpiresAt: &future})
	if len(batch) != 50 || e.count(`SELECT count(*) FROM core.gift_codes`) != before+50 {
		t.Fatalf("a batch of 50 made %d", len(batch))
	}
	seen := map[string]bool{}
	for _, c := range batch {
		canon, ok := normalizeGiftCode(c.Code)
		if !ok || !strings.HasPrefix(c.Code, "GIFT-") || len(canon) != len("GIFT")+giftBodyLen || seen[canon] {
			t.Fatalf("code %q (%q)", c.Code, canon)
		}
		seen[canon] = true
		if c.Amount != 250_000 || c.Currency != "IRT" || c.MaxRedemptions != 10 || c.PerCustomerLimit != 2 || c.Note != "spring giveaway" ||
			!c.Generated || !c.Enabled || c.Redeemed != 0 || c.ExpiresAt == nil || c.CreatedBy == nil || *c.CreatedBy != e.admin.ID {
			t.Fatalf("terms of %q: %+v", c.Code, c.GiftCode)
		}
	}
	if n := e.count(`SELECT count(*) FROM core.audit_log WHERE action = 'gift_code.create' AND actor_id = $1`, e.admin.ID); n != before+50 {
		t.Fatalf("%d audit entries for %d new codes", n, 50)
	}
	// Without a prefix, groups of four.
	plain := e.one(GiftCodeSpec{Amount: 1, Currency: "IRT", MaxRedemptions: 1})
	if len(plain.Code) != 14 || plain.Code[4] != '-' || plain.Code[9] != '-' || !plain.Generated {
		t.Fatalf("a generated code: %q", plain.Code)
	}
}

func TestRedeemCreditsTheWalletOnceWithEverythingRecorded(t *testing.T) {
	e := newGiftEnv(t)
	ctx := context.Background()
	c := e.one(GiftCodeSpec{Amount: 25_000, Currency: "IRT", MaxRedemptions: 5})
	cust := seedUser(t, e.s, 9310)
	fund(t, e.s, cust.ID, 1_000)

	// What a customer types is not what is shown: lower case, no dashes, Persian digits, a ZWNJ from the keyboard.
	typed := strings.ToLower(strings.ReplaceAll(c.Code, "-", "")) + runes(0x200C)
	r, err := e.svc.RedeemGiftCode(ctx, RedeemGiftCodeParams{UserID: cust.ID, Code: typed, RequestKey: "req-main"})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if r.Amount != 25_000 || r.Currency != "IRT" || r.Balance != 26_000 || r.Replayed {
		t.Fatalf("result: %+v", r)
	}
	if bal, sum, rows := ledger(t, e.s, cust.ID); bal != 26_000 || sum != 26_000 || rows != 2 {
		t.Fatalf("wallet %d, ledger sum %d over %d entries", bal, sum, rows)
	}
	// The ledger entry: its kind, what it refers to, and its key from the code, the customer and the number.
	var kind, refType, refID, key string
	var amount, after int64
	if err := e.s.DB().QueryRow(ctx, `SELECT kind, ref_type, ref_id::text, idempotency_key, amount, balance_after
		FROM core.ledger_entries WHERE kind = 'gift_code'`).Scan(&kind, &refType, &refID, &key, &amount, &after); err != nil {
		t.Fatal(err)
	}
	if refType != "gift_code" || refID != c.ID || key != "gift:"+c.ID+":"+cust.ID+":1" || amount != 25_000 || after != 26_000 {
		t.Fatalf("ledger entry: %s %s %s %s %d %d", kind, refType, refID, key, amount, after)
	}
	// The redemption names that very entry, the request and the customer's number.
	var seq int
	var reqKey, entryID, ledgerID string
	if err := e.s.DB().QueryRow(ctx, `SELECT r.seq, r.request_key, r.ledger_entry_id::text, l.id::text
		FROM core.gift_code_redemptions r JOIN core.ledger_entries l ON l.kind = 'gift_code' WHERE r.gift_code_id = $1 AND r.user_id = $2`,
		c.ID, cust.ID).Scan(&seq, &reqKey, &entryID, &ledgerID); err != nil || seq != 1 || reqKey != "req-main" || entryID != ledgerID {
		t.Fatalf("redemption: %d %q %s %s (%v)", seq, reqKey, entryID, ledgerID, err)
	}
	g, err := e.s.GetGiftCode(ctx, e.s.Conn(), c.ID)
	if err != nil || g.Redeemed != 1 || g.LastRedeemedAt == nil {
		t.Fatalf("the code after: %+v %v", g, err)
	}
	// The audit entry is the customer's, with the code's id and last four characters, not the code.
	var actor, action, entity, entityID string
	var auditAfter json.RawMessage
	if err := e.s.DB().QueryRow(ctx, `SELECT actor_id::text, action, entity, entity_id, after FROM core.audit_log WHERE action = 'gift_code.redeem'`).
		Scan(&actor, &action, &entity, &entityID, &auditAfter); err != nil {
		t.Fatal(err)
	}
	var detail map[string]any
	if err := json.Unmarshal(auditAfter, &detail); err != nil {
		t.Fatal(err)
	}
	if actor != cust.ID || entity != "gift_code" || entityID != c.ID || detail["amount"] != float64(25_000) || detail["balance"] != float64(26_000) ||
		detail["n"] != float64(1) || detail["tail"] != c.Tail || len(detail) != 5 {
		t.Fatalf("audit entry: %s %s %s %s", actor, entity, entityID, auditAfter)
	}
	// The wallet-credited event the bot's feed carries, with the reason.
	evs := outboxEvents(t, e.s, events.WalletCredited)
	if len(evs) != 1 {
		t.Fatalf("%d wallet.credited.v1 events", len(evs))
	}
	var ev events.WalletCreditedEvent
	if err := json.Unmarshal(evs[0], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Reason != events.CreditGiftCode || ev.UserID != cust.ID || ev.TelegramID != cust.TelegramID || ev.Amount != 25_000 || ev.Currency != "IRT" || ev.Balance != 26_000 {
		t.Fatalf("event: %+v", ev)
	}
	// The digits are the customer's own: a Persian-digit spelling of a custom code is the same code.
	own := e.one(GiftCodeSpec{Amount: 10, Currency: "IRT", MaxRedemptions: 2, Code: "bobres-1405"})
	if got := outcome(e.redeem(seedUser(t, e.s, 9311), "BOBRES-"+runes(0x06F1, 0x06F4, 0x06F0, 0x06F5))); got != "ok" {
		t.Fatalf("the Persian digits of %q: %s", own.Code, got)
	}
}

// A customer or a reseller, a staff member too: any active account. A banned one gets nothing, and the code is
// left as it was.
func TestWhoMayRedeem(t *testing.T) {
	e := newGiftEnv(t)
	for i, role := range []string{"user", "reseller", "support", "admin", "owner"} {
		c := e.one(spec())
		who := staffMember(t, e.s, int64(9320+i), role)
		if got := outcome(e.redeem(who, c.Code)); got != "ok" {
			t.Errorf("a %s: %s", role, got)
		}
	}
	// A reseller who owes the store: the gift lowers the debt, the credit limit is not touched.
	tier := seedTier(t, e.svc, e.admin, "Gold", 10, 5, nil)
	debtor := reseller(t, e.svc, e.s, e.admin, 9330, tier, 5000)
	e.sql(`UPDATE core.wallets SET balance = -3000 WHERE user_id = $1 AND currency = 'IRT'`, debtor.ID)
	c := e.one(spec())
	r, err := e.redeem(debtor, c.Code)
	if err != nil || r.Balance != -2000 {
		t.Fatalf("a reseller in debt: %+v %v", r, err)
	}

	// Banned: refused as every purchase of a banned account is, whether the code is right or wrong, and nothing is
	// counted against the code or the account.
	banned := seedUser(t, e.s, 9340)
	if _, err := e.svc.SetUserStatus(context.Background(), e.admin, banned.ID, "banned", "abuse"); err != nil {
		t.Fatal(err)
	}
	good := e.one(spec())
	for _, code := range []string{good.Code, "WRONG-CODE-NOT-REAL"} {
		if _, err := e.redeem(banned, code); !errors.Is(err, ErrUserBanned) {
			t.Errorf("banned with %q: %v", code, err)
		}
	}
	if e.balance(banned, "IRT") != 0 {
		t.Error("a banned account was credited")
	}
	if g, _ := e.s.GetGiftCode(context.Background(), e.s.Conn(), good.ID); g.Redeemed != 0 {
		t.Error("a banned account used the code up")
	}
	if n := e.count(`SELECT count(*) FROM core.gift_code_failures WHERE user_id = $1`, banned.ID); n != 0 {
		t.Error("a banned account's attempt was counted")
	}
	// Unbanned, the very code works.
	if _, err := e.svc.SetUserStatus(context.Background(), e.admin, banned.ID, "active", "sorry"); err != nil {
		t.Fatal(err)
	}
	if got := outcome(e.redeem(banned, good.Code)); got != "ok" {
		t.Fatalf("after the ban: %s", got)
	}
	// Somebody who does not exist.
	if _, err := e.svc.RedeemGiftCode(context.Background(), RedeemGiftCodeParams{UserID: "00000000-0000-0000-0000-00000000dead", Code: good.Code, RequestKey: "k"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown user: %v", err)
	}
	if _, err := e.svc.RedeemGiftCode(context.Background(), RedeemGiftCodeParams{UserID: "not-an-id", Code: good.Code, RequestKey: "k"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a user that is not an id: %v", err)
	}
}

func TestRedeemLimits(t *testing.T) {
	e := newGiftEnv(t)
	a, b, c3 := seedUser(t, e.s, 9350), seedUser(t, e.s, 9351), seedUser(t, e.s, 9352)

	// Once per customer: the second try is told so, and the balance does not move.
	once := e.one(GiftCodeSpec{Amount: 700, Currency: "IRT", MaxRedemptions: 3})
	if got := outcome(e.redeem(a, once.Code)); got != "ok" {
		t.Fatal(got)
	}
	if got := outcome(e.redeem(a, once.Code)); got != GiftAlreadyRedeemed {
		t.Fatalf("the same customer again: %s", got)
	}
	if e.balance(a, "IRT") != 700 {
		t.Fatalf("balance %d after a second try", e.balance(a, "IRT"))
	}
	// Told only about their own redemptions: it does not count against them as a wrong code.
	if n := e.count(`SELECT count(*) FROM core.gift_code_failures WHERE user_id = $1`, a.ID); n != 0 {
		t.Error("a code a customer used already was counted as wrong")
	}

	// A limit of two per customer.
	twice := e.one(GiftCodeSpec{Amount: 100, Currency: "IRT", MaxRedemptions: 10, PerCustomerLimit: 2})
	for i, want := range []string{"ok", "ok", GiftAlreadyRedeemed} {
		if got := outcome(e.redeem(b, twice.Code)); got != want {
			t.Fatalf("try %d of two: %s, want %s", i+1, got, want)
		}
	}
	if e.balance(b, "IRT") != 200 {
		t.Fatalf("two redemptions of 100: %d", e.balance(b, "IRT"))
	}
	var keys []string
	rows, err := e.s.DB().Query(context.Background(), `SELECT idempotency_key FROM core.ledger_entries WHERE user_id = $1 ORDER BY created_at, id`, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		keys = append(keys, k)
	}
	rows.Close()
	if want := []string{"gift:" + twice.ID + ":" + b.ID + ":1", "gift:" + twice.ID + ":" + b.ID + ":2"}; strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("ledger keys %v, want %v", keys, want)
	}

	// Used up: three customers of a code that two may use. The third is told it is used up, having sent it whole.
	two := e.one(GiftCodeSpec{Amount: 50, Currency: "IRT", MaxRedemptions: 2})
	for i, who := range []*store.User{a, b, c3} {
		want := "ok"
		if i == 2 {
			want = GiftUsedUp
		}
		if got := outcome(e.redeem(who, two.Code)); got != want {
			t.Fatalf("customer %d of a code with two uses: %s, want %s", i+1, got, want)
		}
	}
	if e.balance(c3, "IRT") != 0 {
		t.Error("a used up code credited")
	}
	g, _ := e.s.GetGiftCode(context.Background(), e.s.Conn(), two.ID)
	if g.Redeemed != 2 || GiftCodeStatus(g, time.Now()) != "used_up" {
		t.Fatalf("after: %+v", g)
	}
	// Used up is no wrong code either.
	if n := e.count(`SELECT count(*) FROM core.gift_code_failures WHERE user_id = $1`, c3.ID); n != 0 {
		t.Error("a used up code was counted as wrong")
	}
	// Whoever redeemed it and asks again is told what they did, not that it is used up.
	if got := outcome(e.redeem(a, two.Code)); got != GiftAlreadyRedeemed {
		t.Fatalf("a customer who has it, once used up: %s", got)
	}
}

// Unknown, switched off and expired codes, and what is not a code, all answer alike: nothing tells a guesser
// which of them exist.
func TestWrongCodesAllAnswerTheSame(t *testing.T) {
	e := newGiftEnv(t)
	off := e.one(spec())
	expired := e.one(spec())
	live := e.one(spec())
	if _, err := e.svc.SetGiftCodeEnabled(context.Background(), e.admin, off.ID, false); err != nil {
		t.Fatal(err)
	}
	e.sql(`UPDATE core.gift_codes SET expires_at = now() - interval '1 minute' WHERE id = $1`, expired.ID)

	cases := []struct{ name, code string }{
		{"unknown", "ABCD-EFGH-JKMN"},
		{"switched off", off.Code},
		{"expired", expired.Code},
		{"empty", ""},
		{"not a code", "hello there!"},
		{"too short", "ABCD"},
		{"in Persian", runes(0x06A9, 0x062F, 0x0020, 0x0647, 0x062F, 0x06CC, 0x0647)},
	}
	answers := map[string]bool{}
	for i, c := range cases {
		// A customer for each, so the lock after five of them does not stand in for the answer.
		r, err := e.redeem(seedUser(t, e.s, int64(9360+i)), c.code)
		var ge *GiftCodeError
		if r != nil || !errors.As(err, &ge) {
			t.Fatalf("%s: %+v %v", c.name, r, err)
		}
		if ge.Reason != GiftInvalid || ge.RetryAfter != 0 {
			t.Errorf("%s answered %+v: a wrong code is %q, with no wait", c.name, ge, GiftInvalid)
		}
		answers[err.Error()] = true
	}
	if len(answers) != 1 {
		t.Fatalf("wrong codes answered differently: %v", answers)
	}
	// None was credited, and a live code still is.
	if e.giftCredits() != 0 {
		t.Fatalf("%d gift credits from wrong codes", e.giftCredits())
	}
	if got := outcome(e.redeem(seedUser(t, e.s, 9399), live.Code)); got != "ok" {
		t.Fatalf("a live code: %s", got)
	}
	// Switched on again, the code works.
	if _, err := e.svc.SetGiftCodeEnabled(context.Background(), e.admin, off.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := outcome(e.redeem(seedUser(t, e.s, 9398), off.Code)); got != "ok" {
		t.Fatalf("a code switched on again: %s", got)
	}
}

// A request repeated with its key credits once and answers again the same; whatever has become of the code.
func TestRedeemRepeatedRequestCreditsOnce(t *testing.T) {
	e := newGiftEnv(t)
	ctx := context.Background()
	c := e.one(GiftCodeSpec{Amount: 400, Currency: "IRT", MaxRedemptions: 5, PerCustomerLimit: 3})
	cust := seedUser(t, e.s, 9380)
	p := RedeemGiftCodeParams{UserID: cust.ID, Code: c.Code, RequestKey: "the-message-42"}
	first, err := e.svc.RedeemGiftCode(ctx, p)
	if err != nil || first.Replayed || first.Balance != 400 {
		t.Fatalf("first: %+v %v", first, err)
	}
	for i := range 3 {
		again, err := e.svc.RedeemGiftCode(ctx, p)
		if err != nil || !again.Replayed || again.Amount != 400 || again.Balance != 400 {
			t.Fatalf("repeat %d: %+v %v", i, again, err)
		}
	}
	if e.balance(cust, "IRT") != 400 || e.giftCredits() != 1 || e.count(`SELECT count(*) FROM core.gift_code_redemptions`) != 1 {
		t.Fatalf("a repeated request credited more than once: balance %d, %d credits", e.balance(cust, "IRT"), e.giftCredits())
	}
	if n := len(outboxEvents(t, e.s, events.WalletCredited)); n != 1 {
		t.Fatalf("%d events for one credit", n)
	}
	// The balance a repeat shows is the balance now.
	fund(t, e.s, cust.ID, 7)
	if again, _ := e.svc.RedeemGiftCode(ctx, p); again == nil || again.Balance != 407 {
		t.Fatalf("a repeat after a top-up: %+v", again)
	}
	// Even when the code has been switched off, used up or has run out meanwhile, and when the customer is locked.
	if _, err := e.svc.SetGiftCodeEnabled(ctx, e.admin, c.ID, false); err != nil {
		t.Fatal(err)
	}
	e.sql(`UPDATE core.gift_codes SET expires_at = now() - interval '1 hour' WHERE id = $1`, c.ID)
	e.sql(`INSERT INTO core.gift_code_failures (user_id, failures, window_start, locked_until) VALUES ($1, 5, now(), now() + interval '1 hour')`, cust.ID)
	if again, err := e.svc.RedeemGiftCode(ctx, p); err != nil || !again.Replayed {
		t.Fatalf("a repeat of a request that had succeeded: %+v %v", again, err)
	}
	// A new request is another matter: the code is off now.
	if got := outcome(e.redeem(cust, c.Code)); got != GiftLocked {
		t.Fatalf("a new request while locked: %s", got)
	}
	e.sql(`DELETE FROM core.gift_code_failures`)
	if got := outcome(e.redeem(cust, c.Code)); got != GiftInvalid {
		t.Fatalf("a new request for a code that is off: %s", got)
	}
	if e.balance(cust, "IRT") != 407 {
		t.Fatal("a refused request moved money")
	}

	// A key that redeemed another code, or no code at all, is a caller's mistake: never an answer, never a credit.
	other := e.one(GiftCodeSpec{Amount: 1, Currency: "IRT", MaxRedemptions: 5})
	for name, code := range map[string]string{"another code": other.Code, "a malformed code": "???"} {
		_, err := e.svc.RedeemGiftCode(ctx, RedeemGiftCodeParams{UserID: cust.ID, Code: code, RequestKey: "the-message-42"})
		if !errors.Is(err, store.ErrIdempotencyConflict) {
			t.Errorf("the key of an earlier request with %s: %v", name, err)
		}
	}
	// Another customer's key of the same text is theirs.
	if r, err := e.svc.RedeemGiftCode(ctx, RedeemGiftCodeParams{UserID: seedUser(t, e.s, 9381).ID, Code: other.Code, RequestKey: "the-message-42"}); err != nil || r.Replayed {
		t.Fatalf("the same key text from another customer: %+v %v", r, err)
	}
	// A request needs a key.
	for _, k := range []string{"", "  ", strings.Repeat("k", 129)} {
		if _, err := e.svc.RedeemGiftCode(ctx, RedeemGiftCodeParams{UserID: cust.ID, Code: other.Code, RequestKey: k}); !errors.Is(err, ErrInvalid) {
			t.Errorf("key %q: %v", k, err)
		}
	}
}

// The last use of a code is won by exactly one of the customers who ask for it at once. Run with -race.
func TestRedeemLastUseHasExactlyOneWinner(t *testing.T) {
	for _, uses := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d use", uses), func(t *testing.T) {
			e := newGiftEnv(t)
			const customers = 24
			c := e.one(GiftCodeSpec{Amount: 900, Currency: "IRT", MaxRedemptions: uses})
			who := make([]*store.User, customers)
			for i := range who {
				who[i] = seedUser(t, e.s, int64(9400+i))
			}
			results := make([]string, customers)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range who {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					results[i] = outcome(e.redeem(who[i], c.Code))
				}()
			}
			close(start)
			wg.Wait()
			won, usedUp := 0, 0
			for i, r := range results {
				switch r {
				case "ok":
					won++
					if e.balance(who[i], "IRT") != 900 {
						t.Errorf("a winner has %d", e.balance(who[i], "IRT"))
					}
				case GiftUsedUp:
					usedUp++
					if e.balance(who[i], "IRT") != 0 {
						t.Errorf("a loser has %d", e.balance(who[i], "IRT"))
					}
				default:
					t.Errorf("customer %d: %s", i, r)
				}
			}
			if won != uses || usedUp != customers-uses {
				t.Fatalf("%d won and %d were told it is used up, of %d customers asking for %d uses", won, usedUp, customers, uses)
			}
			g, _ := e.s.GetGiftCode(context.Background(), e.s.Conn(), c.ID)
			if g.Redeemed != uses || e.giftCredits() != uses || e.count(`SELECT count(*) FROM core.gift_code_redemptions`) != uses {
				t.Fatalf("code counts %d, %d credits, %d redemptions, for %d uses", g.Redeemed, e.giftCredits(), e.count(`SELECT count(*) FROM core.gift_code_redemptions`), uses)
			}
			if sum := e.count(`SELECT COALESCE(sum(amount), 0)::int FROM core.ledger_entries WHERE kind = 'gift_code'`); sum != 900*uses {
				t.Fatalf("%d paid out for %d uses of 900", sum, uses)
			}
			if n := e.count(`SELECT count(*) FROM core.gift_code_failures`); n != 0 {
				t.Errorf("%d customers were counted as wrong", n)
			}
		})
	}
}

// One customer, many requests at once: the limit per customer holds, a repeated request credits once.
func TestRedeemOneCustomerAtOnce(t *testing.T) {
	e := newGiftEnv(t)
	cust := seedUser(t, e.s, 9440)

	c := e.one(GiftCodeSpec{Amount: 300, Currency: "IRT", MaxRedemptions: 100, PerCustomerLimit: 2})
	var wg sync.WaitGroup
	res := make([]string, 12)
	for i := range res {
		wg.Add(1)
		go func() { defer wg.Done(); res[i] = outcome(e.redeem(cust, c.Code)) }() // twelve requests, twelve keys
	}
	wg.Wait()
	ok := 0
	for _, r := range res {
		switch r {
		case "ok":
			ok++
		case GiftAlreadyRedeemed:
		default:
			t.Errorf("answer %s", r)
		}
	}
	if ok != 2 || e.balance(cust, "IRT") != 600 || e.giftCredits() != 2 {
		t.Fatalf("%d redemptions, balance %d, %d credits, for a limit of 2", ok, e.balance(cust, "IRT"), e.giftCredits())
	}

	// The same request twelve times at once: one credit, and every answer is a success.
	again := e.one(GiftCodeSpec{Amount: 11, Currency: "IRT", MaxRedemptions: 100, PerCustomerLimit: 5})
	fresh := seedUser(t, e.s, 9441)
	same := make([]string, 12)
	for i := range same {
		wg.Add(1)
		go func() {
			defer wg.Done()
			same[i] = outcome(e.svc.RedeemGiftCode(context.Background(), RedeemGiftCodeParams{UserID: fresh.ID, Code: again.Code, RequestKey: "one-request"}))
		}()
	}
	wg.Wait()
	nOK, nReplay := 0, 0
	for _, r := range same {
		switch r {
		case "ok":
			nOK++
		case "replay":
			nReplay++
		default:
			t.Errorf("a copy of one request: %s", r)
		}
	}
	if nOK != 1 || nReplay != 11 || e.balance(fresh, "IRT") != 11 {
		t.Fatalf("%d first answers and %d replays, balance %d", nOK, nReplay, e.balance(fresh, "IRT"))
	}
}

// Five wrong codes in an hour close entering codes for an hour. Only wrong codes count; a success does not wash
// them; the lock opens by itself, and the count starts over after the window.
func TestWrongCodesLockEnteringCodes(t *testing.T) {
	e := newGiftEnv(t)
	good := e.one(GiftCodeSpec{Amount: 10, Currency: "IRT", MaxRedemptions: 100})
	cust, other := seedUser(t, e.s, 9450), seedUser(t, e.s, 9451)
	wrong := func(u *store.User, n int) []string {
		var out []string
		for range n {
			r, err := e.redeem(u, "ZZZZ-ZZZZ-ZZZZ")
			if r != nil {
				t.Fatal("a wrong code credited")
			}
			out = append(out, outcome(r, err))
		}
		return out
	}

	// Four wrong ones are four wrong ones; the right code still works, and counts for nothing against them.
	if got := wrong(cust, 4); strings.Join(got, ",") != strings.Repeat(GiftInvalid+",", 3)+GiftInvalid {
		t.Fatalf("four wrong codes: %v", got)
	}
	if got := outcome(e.redeem(cust, good.Code)); got != "ok" {
		t.Fatalf("the right code after four wrong: %s", got)
	}
	// The fifth wrong code is the one that closes it, and says so.
	_, err := e.redeem(cust, "QQQQ-QQQQ-QQQQ")
	var ge *GiftCodeError
	if !errors.As(err, &ge) || ge.Reason != GiftLocked || ge.RetryAfter != time.Hour {
		t.Fatalf("the fifth wrong code: %v", err)
	}
	// A success did not wash the four: with the fifth, they were five.
	if n := e.count(`SELECT failures FROM core.gift_code_failures WHERE user_id = $1`, cust.ID); n != 5 {
		t.Fatalf("%d wrong codes counted", n)
	}
	// Locked: even the right code is refused, and it is not used.
	before := e.balance(cust, "IRT")
	for _, code := range []string{good.Code, "ZZZZ-ZZZZ-ZZZZ", "", "x"} {
		_, err := e.redeem(cust, code)
		if !errors.As(err, &ge) || ge.Reason != GiftLocked || ge.RetryAfter <= 0 || ge.RetryAfter > time.Hour {
			t.Fatalf("locked, with %q: %v", code, err)
		}
	}
	if e.balance(cust, "IRT") != before {
		t.Fatal("a locked customer was credited")
	}
	// Once; in the audit log, with the customer as the actor.
	if n := e.count(`SELECT count(*) FROM core.audit_log WHERE action = 'gift_code.lock' AND actor_id = $1`, cust.ID); n != 1 {
		t.Fatalf("%d audit entries for the lock", n)
	}
	// Somebody else is not affected.
	if got := outcome(e.redeem(other, good.Code)); got != "ok" {
		t.Fatalf("another customer while the first is locked: %s", got)
	}
	// An hour on, the lock is open; the window has passed too, so the count starts over.
	e.sql(`UPDATE core.gift_code_failures SET locked_until = now() - interval '1 second', window_start = now() - interval '61 minutes' WHERE user_id = $1`, cust.ID)
	if got := wrong(cust, 1); got[0] != GiftInvalid {
		t.Fatalf("the first wrong code after the lock: %v", got)
	}
	if n := e.count(`SELECT failures FROM core.gift_code_failures WHERE user_id = $1`, cust.ID); n != 1 {
		t.Fatalf("the count after the window: %d", n)
	}
	if got := wrong(cust, 3); strings.Join(got, ",") != strings.Repeat(GiftInvalid+",", 2)+GiftInvalid {
		t.Fatalf("wrong codes 2 to 4: %v", got)
	}

	// Malformed text counts too (it is a try, and costs no database work to refuse), and an old window forgets.
	fresh := seedUser(t, e.s, 9452)
	for i, code := range []string{"", "x", "????????????", strings.Repeat("A", 500), "AB"} {
		_, err := e.redeem(fresh, code)
		want := GiftInvalid
		if i == 4 {
			want = GiftLocked
		}
		if !errors.As(err, &ge) || ge.Reason != want {
			t.Fatalf("malformed %d (%q): %v", i, code, err)
		}
	}
	if strings.Contains(e.logs.String(), good.Code) {
		t.Error("a code is in the log")
	}
}

// A burst of wrong codes at once: every one is counted, and exactly one closes the entry.
func TestWrongCodesAtOnceAreAllCounted(t *testing.T) {
	e := newGiftEnv(t)
	cust := seedUser(t, e.s, 9460)
	const tries = 40
	res := make([]string, tries)
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func() { defer wg.Done(); res[i] = outcome(e.redeem(cust, fmt.Sprintf("WRONG-%02d-AAAA", i))) }()
	}
	wg.Wait()
	invalid, locked := 0, 0
	for _, r := range res {
		switch r {
		case GiftInvalid:
			invalid++
		case GiftLocked:
			locked++
		default:
			t.Errorf("answer %s", r)
		}
	}
	// The ones that were let through before the lock closed answer "invalid", the 5th "locked", and the rest, which
	// came after it, "locked" too; none can be let through after more than the limit.
	if n := e.count(`SELECT failures FROM core.gift_code_failures WHERE user_id = $1`, cust.ID); n < giftFailLimit || n > tries {
		t.Fatalf("%d wrong codes counted of %d", n, tries)
	}
	if invalid > giftFailLimit-1 || locked < tries-(giftFailLimit-1) {
		t.Fatalf("%d invalid and %d locked: at most %d may be let through", invalid, locked, giftFailLimit-1)
	}
	if n := e.count(`SELECT count(*) FROM core.audit_log WHERE action = 'gift_code.lock'`); n != 1 {
		t.Fatalf("%d audit entries for one lock", n)
	}
}

func TestGiftCodesOffAndWithoutAKey(t *testing.T) {
	e := newGiftEnv(t)
	ctx := context.Background()
	c := e.one(GiftCodeSpec{Amount: 10, Currency: "IRT", MaxRedemptions: 5})
	cust := seedUser(t, e.s, 9470)

	// The owner's switch off: nobody can redeem; codes can still be made and listed, and the switch on again works.
	if err := e.svc.SetSetting(ctx, e.admin, SettingGiftCodes, "false"); err != nil {
		t.Fatal(err)
	}
	if e.svc.GiftCodesOn(ctx) {
		t.Fatal("the switch is off and gift codes are on")
	}
	if got := outcome(e.redeem(cust, c.Code)); got != GiftOff {
		t.Fatalf("switched off: %s", got)
	}
	if got := outcome(e.redeem(cust, "wrong")); got != GiftOff {
		t.Fatalf("a wrong code, switched off: %s", got)
	}
	if n := e.count(`SELECT count(*) FROM core.gift_code_failures`); n != 0 {
		t.Fatal("an attempt was counted while the feature is off")
	}
	e.one(spec()) // staff can prepare codes meanwhile
	if err := e.svc.SetSetting(ctx, e.admin, SettingGiftCodes, ""); err != nil {
		t.Fatal(err)
	}
	if !e.svc.GiftCodesOn(ctx) {
		t.Fatal("unset, gift codes are on")
	}
	if got := outcome(e.redeem(cust, c.Code)); got != "ok" {
		t.Fatalf("switched on again: %s", got)
	}

	// No master key: nothing is made and nothing redeemed, and nothing is stored in clear instead.
	bare := New(e.s, Config{})
	if bare.GiftCodesAvailable() {
		t.Fatal("a service without a key says gift codes are available")
	}
	if _, err := bare.CreateGiftCodes(ctx, e.admin, spec()); !errors.Is(err, ErrState) {
		t.Fatalf("create without a key: %v", err)
	}
	if got := outcome(bare.RedeemGiftCode(ctx, RedeemGiftCodeParams{UserID: cust.ID, Code: c.Code, RequestKey: "k"})); got != GiftOff {
		t.Fatalf("redeem without a key: %s", got)
	}
	if !e.svc.GiftCodesAvailable() {
		t.Fatal("a service with a key says gift codes are not available")
	}
}

// A wallet that cannot take the amount refuses cleanly: nothing is credited, recorded or counted.
func TestRedeemRefusesABalanceThatWouldOverflow(t *testing.T) {
	e := newGiftEnv(t)
	cust := seedUser(t, e.s, 9480)
	c := e.one(GiftCodeSpec{Amount: 5000, Currency: "IRT", MaxRedemptions: 2})
	e.sql(`INSERT INTO core.wallets (user_id, currency, balance) VALUES ($1, 'IRT', $2)`, cust.ID, int64(math.MaxInt64-100))
	_, err := e.redeem(cust, c.Code)
	if !errors.Is(err, store.ErrBalanceOverflow) {
		t.Fatalf("a balance near the largest int64: %v", err)
	}
	if e.balance(cust, "IRT") != math.MaxInt64-100 || e.giftCredits() != 0 || e.count(`SELECT count(*) FROM core.gift_code_redemptions`) != 0 {
		t.Fatal("a refused credit left something behind")
	}
	if g, _ := e.s.GetGiftCode(context.Background(), e.s.Conn(), c.ID); g.Redeemed != 0 {
		t.Fatal("a refused credit used the code")
	}
	// The code is as it was: another customer redeems it.
	if got := outcome(e.redeem(seedUser(t, e.s, 9481), c.Code)); got != "ok" {
		t.Fatalf("another customer: %s", got)
	}
}

// A code in USDT is credited to the USDT wallet, in its own scale, and never to the Toman one.
func TestRedeemCreditsTheCodesCurrency(t *testing.T) {
	e := newGiftEnv(t)
	cust := seedUser(t, e.s, 9490)
	fund(t, e.s, cust.ID, 500)
	c := e.one(GiftCodeSpec{Amount: 2_500_000, Currency: "USDT", MaxRedemptions: 1})
	r, err := e.redeem(cust, c.Code)
	if err != nil || r.Currency != "USDT" || r.Amount != 2_500_000 || r.Balance != 2_500_000 {
		t.Fatalf("redeem: %+v %v", r, err)
	}
	if e.balance(cust, "USDT") != 2_500_000 || e.balance(cust, "IRT") != 500 {
		t.Fatalf("USDT %d, IRT %d", e.balance(cust, "USDT"), e.balance(cust, "IRT"))
	}
}

func TestDisablingKeepsWhatWasPaid(t *testing.T) {
	e := newGiftEnv(t)
	ctx := context.Background()
	c := e.one(GiftCodeSpec{Amount: 80, Currency: "IRT", MaxRedemptions: 5})
	cust := seedUser(t, e.s, 9500)
	if got := outcome(e.redeem(cust, c.Code)); got != "ok" {
		t.Fatal(got)
	}
	got, err := e.svc.SetGiftCodeEnabled(ctx, e.admin, c.ID, false)
	if err != nil || got.Enabled {
		t.Fatalf("disable: %+v %v", got, err)
	}
	// Past redemptions stay: the wallet, the ledger and the list.
	if e.balance(cust, "IRT") != 80 || e.giftCredits() != 1 {
		t.Fatal("disabling took money back")
	}
	if _, rows, total, err := e.svc.GiftCodeRedemptions(ctx, c.ID, store.Page{Limit: 10}); err != nil || total != 1 || len(rows) != 1 || rows[0].UserID != cust.ID {
		t.Fatalf("redemptions of a disabled code: %v %d %v", err, total, rows)
	}
	// Disabling twice is one entry; each change has its own, with the before and the after.
	if _, err := e.svc.SetGiftCodeEnabled(ctx, e.admin, c.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetGiftCodeEnabled(ctx, e.admin, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM core.audit_log WHERE action = 'gift_code.disable' AND entity_id = $1`, c.ID); n != 1 {
		t.Fatalf("%d disable entries", n)
	}
	var before, after json.RawMessage
	if err := e.s.DB().QueryRow(ctx, `SELECT before, after FROM core.audit_log WHERE action = 'gift_code.enable' AND entity_id = $1`, c.ID).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	var was, now map[string]any
	if err := json.Unmarshal(before, &was); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &now); err != nil {
		t.Fatal(err)
	}
	if was["enabled"] != false || now["enabled"] != true || was["tail"] != c.Tail || now["tail"] != c.Tail {
		t.Fatalf("enable entry: %s -> %s", before, after)
	}
	if _, err := e.svc.SetGiftCodeEnabled(ctx, e.admin, "00000000-0000-0000-0000-00000000beef", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a code that is not there: %v", err)
	}
}

func TestListGiftCodesFiltersAndCounts(t *testing.T) {
	e := newGiftEnv(t)
	ctx := context.Background()
	active := e.one(GiftCodeSpec{Amount: 1, Currency: "IRT", MaxRedemptions: 5, Note: "Spring Giveaway"})
	off := e.one(GiftCodeSpec{Amount: 1, Currency: "IRT", MaxRedemptions: 5, Note: "autumn"})
	expired := e.one(GiftCodeSpec{Amount: 1, Currency: "IRT", MaxRedemptions: 5})
	usedUp := e.one(GiftCodeSpec{Amount: 1, Currency: "IRT", MaxRedemptions: 1, Note: "100%_off"})
	if _, err := e.svc.SetGiftCodeEnabled(ctx, e.admin, off.ID, false); err != nil {
		t.Fatal(err)
	}
	e.sql(`UPDATE core.gift_codes SET expires_at = now() - interval '1 second' WHERE id = $1`, expired.ID)
	if got := outcome(e.redeem(seedUser(t, e.s, 9510), usedUp.Code)); got != "ok" {
		t.Fatal(got)
	}
	// An expired code that is also switched off is disabled; a used up one that expired is expired.
	both := e.one(spec())
	if _, err := e.svc.SetGiftCodeEnabled(ctx, e.admin, both.ID, false); err != nil {
		t.Fatal(err)
	}
	e.sql(`UPDATE core.gift_codes SET expires_at = now() - interval '1 second' WHERE id = $1`, both.ID)

	statusOf := func(id string) string {
		g, err := e.s.GetGiftCode(ctx, e.s.Conn(), id)
		if err != nil {
			t.Fatal(err)
		}
		return GiftCodeStatus(g, time.Now())
	}
	for id, want := range map[string]string{active.ID: "active", off.ID: "disabled", expired.ID: "expired", usedUp.ID: "used_up", both.ID: "disabled"} {
		if got := statusOf(id); got != want {
			t.Errorf("status of %s: %s, want %s", id, got, want)
		}
	}
	// The list's filter is the same rule as the status shown (it is SQL, the status Go).
	for status, want := range map[string][]string{"active": {active.ID}, "disabled": {off.ID, both.ID}, "expired": {expired.ID}, "used_up": {usedUp.ID}} {
		items, total, err := e.svc.ListGiftCodes(ctx, store.GiftCodeFilter{Status: status}, store.Page{Limit: 50})
		if err != nil || int(total) != len(want) || len(items) != len(want) {
			t.Fatalf("status %s: %d items of %d (%v)", status, len(items), total, err)
		}
		for _, it := range items {
			if statusOf(it.ID) != status {
				t.Errorf("%s listed under %s", it.ID, status)
			}
		}
	}
	all, total, err := e.svc.ListGiftCodes(ctx, store.GiftCodeFilter{}, store.Page{Limit: 3})
	if err != nil || total != 5 || len(all) != 3 || all[0].ID != both.ID {
		t.Fatalf("a page: %d of %d, first %v (%v)", len(all), total, all[0].ID, err)
	}
	page2, _, _ := e.svc.ListGiftCodes(ctx, store.GiftCodeFilter{}, store.Page{Limit: 3, Offset: 3})
	if len(page2) != 2 {
		t.Fatalf("the second page: %d", len(page2))
	}
	// Search: a note (any case, with LIKE's own characters meaning themselves) or a code's last four characters.
	for q, want := range map[string]string{"spring": active.ID, "GIVEAWAY": active.ID, "100%_off": usedUp.ID, active.Tail: active.ID, strings.ToLower(off.Tail): off.ID} {
		items, _, err := e.svc.ListGiftCodes(ctx, store.GiftCodeFilter{Query: q}, store.Page{Limit: 50})
		if err != nil || !containsGift(items, want) {
			t.Errorf("search %q: %v %v", q, items, err)
		}
	}
	for _, q := range []string{"%", "_", "100%", "nothing like it"} {
		items, _, err := e.svc.ListGiftCodes(ctx, store.GiftCodeFilter{Query: q}, store.Page{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		if q == "%" || q == "_" { // a symbol is not a wildcard: only a note that has it
			for _, it := range items {
				if !strings.Contains(it.Note, q) {
					t.Errorf("search %q found %q", q, it.Note)
				}
			}
		}
		if q == "nothing like it" && len(items) != 0 {
			t.Errorf("search %q found %d", q, len(items))
		}
	}
	if _, _, err := e.svc.ListGiftCodes(ctx, store.GiftCodeFilter{Status: "bogus"}, store.Page{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a status that is none: %v", err)
	}
}

func containsGift(items []store.GiftCode, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func TestGiftCodeRedemptionsListWhoAndWhen(t *testing.T) {
	e := newGiftEnv(t)
	ctx := context.Background()
	c := e.one(GiftCodeSpec{Amount: 60, Currency: "IRT", MaxRedemptions: 10})
	for i := range 3 {
		if got := outcome(e.redeem(seedUser(t, e.s, int64(9520+i)), c.Code)); got != "ok" {
			t.Fatal(got)
		}
	}
	g, rows, total, err := e.svc.GiftCodeRedemptions(ctx, c.ID, store.Page{Limit: 2})
	if err != nil || total != 3 || len(rows) != 2 || g.ID != c.ID {
		t.Fatalf("a page of redemptions: %d of %d (%v)", len(rows), total, err)
	}
	if rows[0].TelegramID != 9522 || rows[1].TelegramID != 9521 || rows[0].Amount != 60 || rows[0].Seq != 1 || rows[0].CreatedAt.IsZero() {
		t.Fatalf("newest first: %+v", rows)
	}
	if _, _, _, err := e.svc.GiftCodeRedemptions(ctx, "00000000-0000-0000-0000-00000000beef", store.Page{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a code that is not there: %v", err)
	}
}

// The largest amount times the most uses fits an int64 (checkGiftSpec checks it too, should a limit be raised).
func TestGiftLimitsCannotOverflow(t *testing.T) {
	if maxTopupMinor*int64(maxGiftRedemptions) > math.MaxInt64 || maxTopupMinor*int64(maxGiftRedemptions)/int64(maxGiftRedemptions) != maxTopupMinor {
		t.Fatalf("the largest amount times the most uses overflows an int64: update checkGiftSpec's limits together")
	}
}
