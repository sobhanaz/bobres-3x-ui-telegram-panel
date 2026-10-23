package domain

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type fakeRates struct {
	status int
	body   string
	err    error
	calls  int
}

func (f *fakeRates) PostUSDTRate(context.Context) (int, []byte, error) {
	f.calls++
	return f.status, []byte(f.body), f.err
}

func setRateSetting(t *testing.T, e *namingEnv, key, value string) {
	t.Helper()
	raw, _ := json.Marshal(value)
	if err := e.s.SetSetting(context.Background(), e.s.Conn(), key, raw); err != nil {
		t.Fatal(err)
	}
}

func clearRate(t *testing.T, e *namingEnv) {
	t.Helper()
	for _, k := range []string{liveRateKey, SettingUSDTAuto, SettingUSDTMargin, "payments.usdt_rate", "payments.usdt_trc20"} {
		if _, err := e.s.DB().Exec(context.Background(), `DELETE FROM core.settings WHERE key = $1`, k); err != nil {
			t.Fatal(err)
		}
	}
}

// The store charges the live USDT price (plus the margin) while it is fresh and automatic rates are on, and the owner's own rate otherwise.
func TestUsdtRateLiveAndFallback(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	clearRate(t, e)
	if got := e.svc.UsdtRate(ctx); got != 0 {
		t.Fatalf("no rate at all: %d", got)
	}
	setRateSetting(t, e, "payments.usdt_rate", "60000")
	if info := e.svc.UsdtRateInfo(ctx); info.Rate != 60000 || info.Source != "manual" {
		t.Fatalf("the owner's rate: %+v", info)
	}

	tr := &fakeRates{status: 200, body: `{"rate":271600,"sources":["tetherland","wallex"],"at":1}`}
	e.svc.SetRateTransport(tr)
	l, err := e.svc.RefreshUsdtRate(ctx)
	if err != nil || l.Rate != 271600 || len(l.Sources) != 2 || !l.Fresh(time.Now()) {
		t.Fatalf("refresh: %+v %v", l, err)
	}
	if info := e.svc.UsdtRateInfo(ctx); info.Rate != 271600 || info.Source != "live" || !info.LiveFresh {
		t.Fatalf("the live rate: %+v", info)
	}
	// A margin is added and rounded up.
	setRateSetting(t, e, SettingUSDTMargin, "2")
	if got := e.svc.UsdtRate(ctx); got != 277032 {
		t.Fatalf("with a 2%% margin: %d", got)
	}
	// Every conversion uses it: 1,000,000 Toman is ceil(1000000*100/277032) cents of USDT.
	if micro, ok := e.svc.ManualCryptoAmount(ctx, 1_000_000, "IRT"); !ok || micro != 3_610_000 {
		t.Fatalf("manual crypto amount: %d %v", micro, ok)
	}
	// Automatic rates off: the owner's own rate.
	setRateSetting(t, e, SettingUSDTAuto, "false")
	if info := e.svc.UsdtRateInfo(ctx); info.Rate != 60000 || info.Source != "manual" {
		t.Fatalf("automatic off: %+v", info)
	}
	setRateSetting(t, e, SettingUSDTAuto, "true")
	// A live rate that is too old is not used.
	old, _ := json.Marshal(LiveRate{Rate: 271600, At: time.Now().Add(-2 * time.Hour).Unix(), Sources: []string{"x"}})
	if err := e.s.SetSetting(ctx, e.s.Conn(), liveRateKey, old); err != nil {
		t.Fatal(err)
	}
	if info := e.svc.UsdtRateInfo(ctx); info.Rate != 60000 || info.Source != "manual" || info.LiveFresh || !info.HasLive {
		t.Fatalf("a stale live rate: %+v", info)
	}
	// What the bot is told is the effective rate, in the form of the stored setting.
	if got := e.svc.UsdtSettingRaw(ctx); got != `"60000"` {
		t.Fatalf("setting raw: %q", got)
	}
}

func TestRefreshUsdtRateRefusesWhatItShould(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	clearRate(t, e)
	if _, err := e.svc.RefreshUsdtRate(ctx); err == nil {
		t.Error("a refresh without a transport")
	}
	for name, tr := range map[string]*fakeRates{
		"the bot is down":   {err: errors.New("the bot did not answer")},
		"no source":         {status: 502, body: `{"error":"unreachable","message":"no price source answered"}`},
		"an absurd rate":    {status: 200, body: `{"rate":5,"sources":["x"]}`},
		"an unreadable one": {status: 200, body: `<html>`},
	} {
		e.svc.SetRateTransport(tr)
		if _, err := e.svc.RefreshUsdtRate(ctx); err == nil {
			t.Errorf("%s: refreshed", name)
		}
	}
	if _, ok := e.svc.LastLiveRate(ctx); ok {
		t.Error("a failed refresh stored a rate")
	}
	e.svc.SetRateTransport(&fakeRates{status: 502, body: `{"error":"unreachable","message":"no price source answered"}`})
	if _, err := e.svc.RefreshUsdtRate(ctx); err == nil || err.Error() != "no price source answered" {
		t.Errorf("the bot's message: %v", err)
	}
}
