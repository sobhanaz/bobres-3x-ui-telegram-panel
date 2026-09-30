package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func sample(now time.Time) Token {
	return Token{
		LicenseID: "lic_1", Customer: "cust_1", Domain: "panel.example.com", Tier: TierStandard,
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), GraceDays: 14,
		Entitlements: Entitlements{MaxServers: 1, Gateways: []string{"manual", "Zarinpal"}, UpdatesUntil: "2027-09-30"},
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv := keys(t)
	now := time.Now().UTC()
	raw, err := Sign(priv, sample(now))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(pub, raw, "PANEL.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.LicenseID != "lic_1" || got.V != Version {
		t.Fatalf("bad token: %+v", got)
	}
}

func TestTamperedPayloadRejected(t *testing.T) {
	pub, priv := keys(t)
	raw, _ := Sign(priv, sample(time.Now().UTC()))
	parts := strings.Split(raw, ".")
	forged := Token{V: Version, LicenseID: "lic_1", Tier: TierPro, ExpiresAt: time.Now().Add(999 * time.Hour), Entitlements: Entitlements{WhiteLabel: true}}
	payload, _ := jsonMarshal(forged)
	bad := base64.RawURLEncoding.EncodeToString(payload) + "." + parts[1]
	if _, err := Verify(pub, bad, ""); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestWrongKeyRejected(t *testing.T) {
	_, priv := keys(t)
	otherPub, _ := keys(t)
	raw, _ := Sign(priv, sample(time.Now().UTC()))
	if _, err := Verify(otherPub, raw, ""); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v", err)
	}
}

func TestMalformedInputs(t *testing.T) {
	pub, _ := keys(t)
	for _, raw := range []string{"", "abc", "a.b.c", "!!!.???", "YQ.YQ"} {
		if _, err := Verify(pub, raw, ""); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: want ErrMalformed, got %v", raw, err)
		}
	}
	if _, err := Verify(nil, "a.b", ""); err == nil {
		t.Error("nil public key must fail")
	}
}

func TestDomainBinding(t *testing.T) {
	pub, priv := keys(t)
	raw, _ := Sign(priv, sample(time.Now().UTC()))
	if _, err := Verify(pub, raw, "evil.example.org"); !errors.Is(err, ErrDomain) {
		t.Fatalf("got %v", err)
	}
	if _, err := Verify(pub, raw, ""); err != nil {
		t.Fatalf("empty domain skips the check: %v", err)
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
		got, err := tok.State(c.at)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v %v want %v", c.name, got, err, c.want)
		}
	}
}

func TestFutureIssuedRejected(t *testing.T) {
	now := time.Now().UTC()
	tok := sample(now)
	tok.IssuedAt = now.Add(2 * time.Hour)
	if _, err := tok.State(now); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("got %v", err)
	}
}

func TestEntitlements(t *testing.T) {
	e := sample(time.Now()).Entitlements
	if e.HasFeature("white_label") || e.HasFeature("nonsense") {
		t.Fatal("must fail closed")
	}
	if !e.HasGateway("zarinpal") || e.HasGateway("stars") {
		t.Fatal("gateway matching")
	}
	if !e.UpdatesAllowed("2027-09-30") || e.UpdatesAllowed("2027-10-01") {
		t.Fatal("updates_until boundary")
	}
	if !(Entitlements{}).UpdatesAllowed("2099-01-01") {
		t.Fatal("empty means always")
	}
	if !WithinLimit(0, 1_000_000) || !WithinLimit(5, 4) || WithinLimit(5, 5) {
		t.Fatal("limit logic")
	}
}
