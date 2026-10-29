package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonaddr"
)

// Automatic TON needs the crypto watcher, as automatic USDT does; a license that names automatic USDT (issued before
// TON existed) covers TON too; one that names neither does not.
func TestTONLicenseGate(t *testing.T) {
	eff := func(watcher bool, gateways ...string) *EffectiveLicense {
		return &EffectiveLicense{Effective: license.Effective{Tier: "standard", Basis: license.BasisLicense,
			Entitlements: license.Entitlements{CryptoWatcher: watcher, Gateways: gateways}}}
	}
	if err := gatewayRefusal(eff(false, TONAuto), TONAuto); err == nil || !strings.Contains(err.Error(), "crypto_watcher") {
		t.Fatalf("no watcher: %v", err)
	}
	for name, e := range map[string]*EffectiveLicense{"names TON": eff(true, TONAuto), "names USDT": eff(true, USDTAuto)} {
		if err := gatewayRefusal(e, TONAuto); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := gatewayRefusal(eff(true, Zarinpal), TONAuto); err == nil || !strings.Contains(err.Error(), "ton_auto") {
		t.Fatalf("a license naming neither: %v", err)
	}
	// TON covers nothing else: a license naming TON does not switch automatic USDT on.
	if err := gatewayRefusal(eff(true, TONAuto), USDTAuto); err == nil {
		t.Fatal("TON covered USDT")
	}
}

// The details the bot and the Mini App show: the address, the TON network and the comment; the hash once paid.
func TestTONGatewayDetails(t *testing.T) {
	addr := tonaddr.Address{Hash: [32]byte{1, 2, 3}}.Canonical()
	d := GatewayDetails(&GatewayIntent{Provider: TONAuto, Status: "pending", PayAddress: addr, PayMemo: "BOB-0000ABCD", ExpiresAt: 1_800_000_000})
	if d["pay_address"] != addr || d["network"] != "TON" || d["memo"] != "BOB-0000ABCD" || d["expires_at"] != "1800000000" || d["txid"] != "" {
		t.Fatalf("pending: %v", d)
	}
	d = GatewayDetails(&GatewayIntent{Provider: TONAuto, Status: "succeeded", PayAddress: addr, ExternalID: strings.Repeat("ab", 32) + "#4000"})
	if d["txid"] != strings.Repeat("ab", 32) || d["memo"] != "" {
		t.Fatalf("paid: %v", d)
	}
	// A USDT payment never carries a memo.
	if d := GatewayDetails(&GatewayIntent{Provider: USDTAuto, PayAddress: "T", PayMemo: "BOB-0000ABCD"}); d["memo"] != "" || d["network"] != "TRC20" {
		t.Fatalf("usdt: %v", d)
	}
}

// The owner's input: any wallet form of the address comes back in the form customers are shown; the key is a TON
// Center key; nothing a TON card has not is accepted; no message repeats the key.
func TestCheckTONSecrets(t *testing.T) {
	a := tonaddr.Address{Hash: [32]byte{9, 8, 7}}
	for _, in := range []string{a.Friendly(true), a.Canonical(), a.Raw(), " \u200c" + a.Friendly(true) + "\n"} {
		got, key, err := checkTONSecrets(in, " ton-test-key ", false, "", false)
		if err != nil || got != a.Canonical() || key != "ton-test-key" {
			t.Errorf("%q: %q %q %v", in, got, key, err)
		}
	}
	if got, _, err := checkTONSecrets("", "", true, "", false); err != nil || got != "" {
		t.Fatalf("keep the stored address: %q %v", got, err)
	}
	for name, c := range map[string]struct {
		addr, key    string
		clear, sbx   bool
		merch, field string
	}{
		"bad checksum": {addr: a.Canonical()[:47] + "A", field: "address"},
		"TRON":         {addr: "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r", field: "address"},
		"key space":    {key: "s3cr3t key", field: "api_key"},
		"key short":    {key: "s3cr3t", field: "api_key"},
		"key + clear":  {key: "ton-test-key", clear: true, field: "api_key"},
		"merchant":     {merch: "x", field: "merchant_id"},
		"sandbox":      {sbx: true, field: "sandbox"},
	} {
		_, _, err := checkTONSecrets(c.addr, c.key, c.clear, c.merch, c.sbx)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != c.field || strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
