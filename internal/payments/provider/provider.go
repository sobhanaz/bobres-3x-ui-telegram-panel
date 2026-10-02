// Package provider implements the Phase 1 manual payment providers. Every
// provider shares one interface; automated gateways plug in later behind the
// same contract (chain watcher, Zarinpal, Telegram Stars).
package provider

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	// Wallet is handled inline by the wallet ledger (no instructions).
	Wallet = "wallet"
	// ManualCard renders card-to-card transfer instructions.
	ManualCard = "manual_card"
	// ManualCrypto renders crypto address instructions.
	ManualCrypto = "manual_crypto"
)

// Instructions renders what the user must do to pay.
type Instructions struct {
	Text    string `json:"text"`
	RefNote string `json:"ref_note,omitempty"`
}

// Provider renders payment instructions from settings values.
type Provider interface {
	Name() string
	Instructions(ctx context.Context, settings map[string]json.RawMessage, amount int64, currency, reference string) (Instructions, error)
}

var registry = map[string]Provider{
	ManualCard:   card{},
	ManualCrypto: cryptoP{},
}

// For returns the provider for a name.
func For(name string) (Provider, error) {
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("provider: unknown %q", name)
	}
	return p, nil
}

func settingText(settings map[string]json.RawMessage, key string) string {
	raw, ok := settings[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

type card struct{}

func (card) Name() string { return ManualCard }

func (card) Instructions(_ context.Context, settings map[string]json.RawMessage, amount int64, currency, reference string) (Instructions, error) {
	cardNo := settingText(settings, "payments.card_number")
	holder := settingText(settings, "payments.card_holder")
	if cardNo == "" {
		return Instructions{}, fmt.Errorf("provider: card number not configured")
	}
	return Instructions{
		Text:    fmt.Sprintf("Transfer %d %s to card %s (%s).\nReference: %s\nThen send the receipt photo and reference number here.", amount, currency, cardNo, holder, reference),
		RefNote: reference,
	}, nil
}

type cryptoP struct{}

func (cryptoP) Name() string { return ManualCrypto }

func (cryptoP) Instructions(_ context.Context, settings map[string]json.RawMessage, amount int64, currency, reference string) (Instructions, error) {
	trc := settingText(settings, "payments.usdt_trc20")
	erc := settingText(settings, "payments.usdt_erc20")
	if trc == "" && erc == "" {
		return Instructions{}, fmt.Errorf("provider: no crypto address configured")
	}
	addr := trc
	if addr == "" {
		addr = erc
	}
	return Instructions{
		Text:    fmt.Sprintf("Send %d %s to:\nTRC20: %s\nERC20: %s\nThen send the TXID and network here.", amount, currency, trc, erc),
		RefNote: addr,
	}, nil
}
