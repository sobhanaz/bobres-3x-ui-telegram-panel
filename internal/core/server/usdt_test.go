package server

import (
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// The bot's "check" screen gets automatic USDT's address, network, window
// and (once paid) the transaction again; other gateways' answers are as
// before.
func TestGatewayRefCarriesUSDTDetails(t *testing.T) {
	const txid = "8f0c1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7"
	ref := gatewayRef(&domain.GatewayIntent{ID: "i1", Status: "succeeded", Provider: domain.USDTAuto, Amount: 150_000, Currency: "IRT",
		GatewayAmount: 2_500_007, GatewayCurrency: "USDT", PayAddress: "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r", ExpiresAt: 1_800_000_000,
		ExternalID: txid + "#2"})
	d := ref.GetDetails()
	if ref.GetGatewayAmount().GetAmount() != 2_500_007 || ref.GetGatewayAmount().GetCurrency() != "USDT" ||
		d["pay_address"] != "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r" || d["network"] != "TRC20" || d["expires_at"] != "1800000000" || d["txid"] != txid {
		t.Fatalf("usdt ref: %+v", ref)
	}
	zp := gatewayRef(&domain.GatewayIntent{ID: "i2", Status: "pending", Provider: domain.Zarinpal, PayURL: "https://x", ExpiresAt: 5})
	if zp.GetDetails() != nil || zp.GetPayUrl() != "https://x" {
		t.Fatalf("zarinpal ref: %+v", zp)
	}
}
