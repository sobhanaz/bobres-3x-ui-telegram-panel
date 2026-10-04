package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const kid = "v1-2026"

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func ring(pub ed25519.PublicKey) KeyRing { return KeyRing{kid: pub} }

func sample(now time.Time) Token {
	return Token{
		KeyID: kid, LicenseID: "lic_1", Customer: "cust_1", Domain: "panel.example.com", InstallID: "inst_1",
		Tier: TierStandard, IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), GraceDays: 14,
		Entitlements: Entitlements{MaxServers: 1, MaxActiveServices: Unlimited, DashboardStaffSeat: 3,
			Gateways: []string{"manual", "Zarinpal"}, UpdatesUntil: "2027-09-30"},
	}
}

func sign(t *testing.T, priv ed25519.PrivateKey, tok Token) string {
	t.Helper()
	raw, err := Sign(priv, tok)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

var okOpts = Options{Domain: "PANEL.example.com:443", InstallID: "inst_1", RequireBound: true}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv := keypair(t)
	got, err := Verify(ring(pub), sign(t, priv, sample(time.Now().UTC())), okOpts)
	if err != nil {
		t.Fatal(err)
	}
	if got.LicenseID != "lic_1" || got.V != Version || got.KeyID != kid {
		t.Fatalf("bad token: %+v", got)
	}
}

func TestTamperedPayloadRejected(t *testing.T) {
	pub, priv := keypair(t)
	raw := sign(t, priv, sample(time.Now().UTC()))
	forged := sample(time.Now().UTC())
	forged.Tier = TierPro
	forged.Entitlements.WhiteLabel = true
	payload, _ := json.Marshal(forged)
	bad := base64.RawURLEncoding.EncodeToString(payload) + "." + strings.Split(raw, ".")[1]
	if _, err := Verify(ring(pub), bad, okOpts); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestWrongKeyAndNoKeys(t *testing.T) {
	_, priv := keypair(t)
	otherPub, _ := keypair(t)
	raw := sign(t, priv, sample(time.Now().UTC()))
	if _, err := Verify(ring(otherPub), raw, okOpts); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v", err)
	}
	if _, err := Verify(nil, raw, okOpts); err == nil {
		t.Fatal("empty key ring must fail")
	}
	if _, err := Verify(KeyRing{kid: ed25519.PublicKey("short")}, raw, okOpts); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("malformed key must not verify: %v", err)
	}
}

func TestKeyRotationAndKidMismatch(t *testing.T) {
	oldPub, oldPriv := keypair(t)
	newPub, newPriv := keypair(t)
	both := KeyRing{"old": oldPub, "new": newPub}

	tokOld := sample(time.Now().UTC())
	tokOld.KeyID = "old"
	tokNew := sample(time.Now().UTC())
	tokNew.KeyID = "new"
	for name, raw := range map[string]string{"old": sign(t, oldPriv, tokOld), "new": sign(t, newPriv, tokNew)} {
		if _, err := Verify(both, raw, okOpts); err != nil {
			t.Errorf("%s key token should verify: %v", name, err)
		}
	}
	// retire the old key: old tokens stop verifying
	if _, err := Verify(KeyRing{"new": newPub}, sign(t, oldPriv, tokOld), okOpts); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("retired key must not verify: %v", err)
	}
	// a token signed by the new key but claiming kid "old" is rejected
	liar := sample(time.Now().UTC())
	liar.KeyID = "old"
	if _, err := Verify(both, sign(t, newPriv, liar), okOpts); !errors.Is(err, ErrInvalid) {
		t.Fatalf("kid mismatch must be rejected: %v", err)
	}
}

func TestMalformedInputs(t *testing.T) {
	pub, _ := keypair(t)
	for _, raw := range []string{"", "abc", "a.b.c", "!!!.???", "YQ.YQ"} {
		if _, err := Verify(ring(pub), raw, okOpts); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: want ErrMalformed, got %v", raw, err)
		}
	}
}

func TestDomainBindingFailsClosed(t *testing.T) {
	pub, priv := keypair(t)
	raw := sign(t, priv, sample(time.Now().UTC()))
	cases := []struct {
		name string
		opts Options
		want error
	}{
		{"other domain", Options{Domain: "evil.example.org", InstallID: "inst_1"}, ErrDomain},
		{"empty domain must NOT skip the check", Options{Domain: "", InstallID: "inst_1"}, ErrDomain},
		{"trailing dot and case normalized", Options{Domain: "Panel.Example.com.", InstallID: "inst_1"}, nil},
		{"port stripped", Options{Domain: "panel.example.com:8443", InstallID: "inst_1"}, nil},
		{"wrong install id", Options{Domain: "panel.example.com", InstallID: "other"}, ErrInstallID},
		{"empty install id must NOT skip the check", Options{Domain: "panel.example.com"}, ErrInstallID},
	}
	for _, c := range cases {
		_, err := Verify(ring(pub), raw, c.opts)
		if !errors.Is(err, c.want) && (c.want != nil || err != nil) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func TestUnboundTokenAndRequireBound(t *testing.T) {
	pub, priv := keypair(t)
	tok := sample(time.Now().UTC())
	tok.Domain, tok.InstallID = "", ""
	raw := sign(t, priv, tok)
	if _, err := Verify(ring(pub), raw, Options{RequireBound: true}); !errors.Is(err, ErrUnbound) {
		t.Fatalf("production must reject unbound tokens: %v", err)
	}
	if _, err := Verify(ring(pub), raw, Options{}); err != nil {
		t.Fatalf("dev may accept unbound tokens: %v", err)
	}
}

func TestValidateRejectsUnsafeTokens(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Now().UTC()
	mut := map[string]func(*Token){
		"no license id":        func(t *Token) { t.LicenseID = "" },
		"unknown tier":         func(t *Token) { t.Tier = "enterprise" },
		"expires before issue": func(t *Token) { t.ExpiresAt = t.IssuedAt.Add(-time.Hour) },
		"zero expiry":          func(t *Token) { t.ExpiresAt = time.Time{} },
		"huge grace":           func(t *Token) { t.GraceDays = 1 << 40 },
		"negative grace":       func(t *Token) { t.GraceDays = -1 },
		"limit below -1":       func(t *Token) { t.Entitlements.MaxServers = -5 },
		"wrong version":        func(t *Token) { t.V = 9 },
	}
	for name, f := range mut {
		tok := sample(now)
		tok.V = Version
		f(&tok)
		raw, err := signRaw(priv, tok)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(ring(pub), raw, okOpts); err == nil {
			t.Errorf("%s: correctly signed but unsafe token was accepted", name)
		}
	}
}

// signRaw signs without defaulting V, so the wrong-version case can be tested.
func signRaw(priv ed25519.PrivateKey, tok Token) (string, error) {
	payload, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(ed25519.Sign(priv, payload)), nil
}

func TestCheckCombinesVerifyAndExpiry(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Now().UTC()
	raw := sign(t, priv, sample(now))
	if _, st, err := Check(ring(pub), raw, okOpts, now); err != nil || st != Active {
		t.Fatalf("active: %v %v", st, err)
	}
	if _, st, err := Check(ring(pub), raw, okOpts, now.Add(48*time.Hour)); err != nil || st != Grace {
		t.Fatalf("grace: %v %v", st, err)
	}
	if _, st, err := Check(ring(pub), raw, okOpts, now.Add(24*time.Hour+15*24*time.Hour)); err != nil || st != Expired {
		t.Fatalf("expired: %v %v", st, err)
	}
	if _, st, err := Check(ring(pub), "garbage", okOpts, now); err == nil || st != Expired {
		t.Fatalf("bad token must be Expired+error: %v %v", st, err)
	}
}

func TestStateTransitions(t *testing.T) {
	now := time.Now().UTC()
	tok := sample(now)
	cases := []struct {
		name string
		at   time.Time
		want State
	}{
		{"active", now, Active},
		{"grace start", tok.ExpiresAt.Add(time.Minute), Grace},
		{"grace end", tok.ExpiresAt.Add(14*24*time.Hour - time.Minute), Grace},
		{"expired", tok.ExpiresAt.Add(14*24*time.Hour + time.Minute), Expired},
	}
	for _, c := range cases {
		if got, err := tok.State(c.at); err != nil || got != c.want {
			t.Errorf("%s: got %v %v want %v", c.name, got, err, c.want)
		}
	}
	future := sample(now)
	future.IssuedAt = now.Add(2 * time.Hour)
	if _, err := future.State(now); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("future-issued: %v", err)
	}
}

func TestEntitlementsAndLimits(t *testing.T) {
	e := sample(time.Now()).Entitlements
	if e.HasFeature("white_label") || e.HasFeature("nonsense") {
		t.Fatal("must fail closed")
	}
	if !e.HasGateway("zarinpal") || e.HasGateway("stars") {
		t.Fatal("gateway matching")
	}
	if !e.UpdatesAllowed("2027-09-30") || e.UpdatesAllowed("2027-10-01") || !(Entitlements{}).UpdatesAllowed("2099-01-01") {
		t.Fatal("updates_until")
	}
	// zero means NONE, -1 means unlimited: an omitted limit must not grant unlimited use
	if WithinLimit(0, 0) || !WithinLimit(Unlimited, 1_000_000) || !WithinLimit(5, 4) || WithinLimit(5, 5) {
		t.Fatal("limit semantics")
	}
	if WithinLimit((Entitlements{}).MaxServers, 1) {
		t.Fatal("token omitting max_servers must allow zero servers, not unlimited")
	}
}
