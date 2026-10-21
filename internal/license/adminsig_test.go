package license

import (
	"errors"
	"testing"
	"time"
)

func TestAdminSignVerify(t *testing.T) {
	privS, pubS, err := NewAdminKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParseAdminPrivateKey(privS)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParseAdminPublicKey(pubS)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	body := []byte(`{"ref":"abcdefgh"}`)
	ts, nonce, sig, err := AdminSign(priv, "POST", "/admin/v1/licenses/create", body, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := AdminVerify(pub, "POST", "/admin/v1/licenses/create", ts, nonce, sig, body, now.Add(AdminSkew)); err != nil {
		t.Fatalf("a good request: %v", err)
	}
	bad := map[string]func() error{
		"another method": func() error { return AdminVerify(pub, "GET", "/admin/v1/licenses/create", ts, nonce, sig, body, now) },
		"another path":   func() error { return AdminVerify(pub, "POST", "/admin/v1/licenses/revoke", ts, nonce, sig, body, now) },
		"another query": func() error {
			return AdminVerify(pub, "POST", "/admin/v1/licenses/create?x=1", ts, nonce, sig, body, now)
		},
		"another body": func() error {
			return AdminVerify(pub, "POST", "/admin/v1/licenses/create", ts, nonce, sig, []byte("{}"), now)
		},
		"another nonce": func() error {
			return AdminVerify(pub, "POST", "/admin/v1/licenses/create", ts, nonce+"x", sig, body, now)
		},
		"another time": func() error {
			return AdminVerify(pub, "POST", "/admin/v1/licenses/create", ts+"1", nonce, sig, body, now)
		},
		"too late": func() error {
			return AdminVerify(pub, "POST", "/admin/v1/licenses/create", ts, nonce, sig, body, now.Add(AdminSkew+time.Second))
		},
		"too early": func() error {
			return AdminVerify(pub, "POST", "/admin/v1/licenses/create", ts, nonce, sig, body, now.Add(-AdminSkew-time.Second))
		},
		"no signature": func() error { return AdminVerify(pub, "POST", "/admin/v1/licenses/create", ts, nonce, "", body, now) },
		"short nonce": func() error {
			return AdminVerify(pub, "POST", "/admin/v1/licenses/create", ts, "short", sig, body, now)
		},
		"no key": func() error { return AdminVerify(nil, "POST", "/admin/v1/licenses/create", ts, nonce, sig, body, now) },
	}
	for name, f := range bad {
		if err := f(); !errors.Is(err, ErrAdminSignature) {
			t.Errorf("%s: %v, want ErrAdminSignature", name, err)
		}
	}
	// Two signatures of one request differ (the nonce), so a recorded one is not another's.
	_, n2, _, _ := AdminSign(priv, "POST", "/x", body, now)
	if n2 == nonce {
		t.Fatal("nonces repeat")
	}
	if _, err := ParseAdminPublicKey("not a key"); err == nil {
		t.Fatal("a bad public key was accepted")
	}
	if _, err := ParseAdminPrivateKey(pubS + pubS); err == nil {
		t.Fatal("a bad private key was accepted")
	}
}
