package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func settings(kv map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	return out
}

func TestCardInstructions(t *testing.T) {
	p, err := For(ManualCard)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := p.Instructions(context.Background(),
		settings(map[string]string{"payments.card_number": "6104-3377", "payments.card_holder": "A. Operator"}),
		50000, "IRT", "ORD-123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ins.Text, "6104-3377") || !strings.Contains(ins.Text, "50000") {
		t.Errorf("bad instructions: %q", ins.Text)
	}
}

func TestCardNotConfigured(t *testing.T) {
	p, _ := For(ManualCard)
	if _, err := p.Instructions(context.Background(), nil, 1, "IRT", "x"); err == nil {
		t.Fatal("instructions rendered without card config")
	}
}

func TestCryptoInstructions(t *testing.T) {
	p, _ := For(ManualCrypto)
	ins, err := p.Instructions(context.Background(),
		settings(map[string]string{"payments.usdt_trc20": "TXYZ..."}),
		12, "USDT", "ORD-9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ins.Text, "TXYZ...") {
		t.Errorf("missing address: %q", ins.Text)
	}
}

func TestUnknownProvider(t *testing.T) {
	if _, err := For("zarinpal"); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
