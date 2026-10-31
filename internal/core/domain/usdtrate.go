package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// The live price of one USDT in Toman, for crypto payments. Core has no internet access, so it asks the bot (RateTransport), which reads a few
// fixed public price pages and answers one number. The number is kept as internal state (state.usdt_rate.live) and is the rate the store uses,
// plus the owner's margin, while it is fresh and the owner has left automatic rates on (payments.usdt_rate_auto, on by default); otherwise
// the rate the owner typed (payments.usdt_rate) is used, as before. Every place that turns a Toman price into USDT asks UsdtRate.

const (
	// SettingUSDTAuto switches the live rate on (default) or off.
	SettingUSDTAuto = "payments.usdt_rate_auto"
	// SettingUSDTMargin is the percent added on top of the live rate, to cover the exchange's spread and fees.
	SettingUSDTMargin = "payments.usdt_rate_margin"
	// MaxUSDTMargin is the largest margin an owner may set (percent).
	MaxUSDTMargin = 30

	liveRateKey = "state.usdt_rate.live"
	// liveRateFresh is how old a live rate may be and still be used; older, the owner's own rate takes over.
	liveRateFresh = 30 * time.Minute
	// liveRateEvery is how often the worker reads it.
	liveRateEvery = 2 * time.Minute
)

// RateTransport asks the bot for the live rate (botfiles.Client.PostUSDTRate): the HTTP status and body of its answer.
type RateTransport interface {
	PostUSDTRate(ctx context.Context) (status int, answer []byte, err error)
}

// SetRateTransport sets the way to the bot's rate endpoint (cmd/core).
func (s *Service) SetRateTransport(tr RateTransport) { s.cfg.RateTransport = tr }

// LiveRate is the last live price read: Toman per USDT, when (unix seconds) and from which sources.
type LiveRate struct {
	Rate    int64    `json:"rate"`
	At      int64    `json:"at"`
	Sources []string `json:"sources"`
}

// Fresh reports whether the rate is recent enough to use at now.
func (l LiveRate) Fresh(now time.Time) bool {
	return l.Rate > 0 && l.At > 0 && now.Sub(time.Unix(l.At, 0)) <= liveRateFresh
}

// LastLiveRate returns the last live price read, fresh or not (ok false when none was ever read).
func (s *Service) LastLiveRate(ctx context.Context) (LiveRate, bool) {
	m, err := s.st.GetSettings(ctx, s.st.Conn())
	if err != nil {
		return LiveRate{}, false
	}
	var l LiveRate
	if raw, ok := m[liveRateKey]; !ok || json.Unmarshal(raw, &l) != nil || l.Rate <= 0 {
		return LiveRate{}, false
	}
	return l, true
}

// UsdtAutoOn reports whether the live rate is switched on.
func (s *Service) UsdtAutoOn(ctx context.Context) bool {
	return s.setting(ctx, SettingUSDTAuto) != "false"
}

// UsdtMargin is the margin percent on top of the live rate (0 to MaxUSDTMargin).
func (s *Service) UsdtMargin(ctx context.Context) int64 {
	return min(s.settingInt(ctx, SettingUSDTMargin), MaxUSDTMargin)
}

// UsdtRateInfo is what the store charges at the moment and where it comes from.
type UsdtRateInfo struct {
	// Rate is Toman per USDT, 0 when none is known (no live rate and no rate typed).
	Rate int64
	// Source is "live" (the live price plus the margin), "manual" (the owner's own rate) or "" (none).
	Source string
	Auto   bool
	Margin int64
	Manual int64
	Live   LiveRate
	// HasLive says a live price was ever read; LiveFresh that it is recent enough to use.
	HasLive, LiveFresh bool
}

// UsdtRateInfo works out the rate the store charges now, from one read of the settings (a balance is shown on every wallet call).
func (s *Service) UsdtRateInfo(ctx context.Context) UsdtRateInfo {
	m, _ := s.st.GetSettings(ctx, s.st.Conn())
	return usdtRateInfoFrom(m, time.Now())
}

// usdtRateInfoFrom is UsdtRateInfo worked out from settings already read (nil when they could not be: no live rate, no rate typed).
func usdtRateInfoFrom(m map[string]json.RawMessage, now time.Time) UsdtRateInfo {
	str := func(key string) string {
		var v string
		if raw, ok := m[key]; ok && json.Unmarshal(raw, &v) == nil {
			return v
		}
		return ""
	}
	info := UsdtRateInfo{Auto: str(SettingUSDTAuto) != "false", Margin: min(parseWholeSetting(str(SettingUSDTMargin)), MaxUSDTMargin),
		Manual: parseWholeSetting(str("payments.usdt_rate"))}
	if raw, ok := m[liveRateKey]; ok && json.Unmarshal(raw, &info.Live) == nil && info.Live.Rate > 0 {
		info.HasLive = true
	} else {
		info.Live = LiveRate{}
	}
	info.LiveFresh = info.HasLive && info.Live.Fresh(now)
	switch {
	case info.Auto && info.LiveFresh:
		// Rounded up to a whole Toman, so that a margin never rounds away.
		info.Rate = int64(math.Ceil(float64(info.Live.Rate) * float64(100+info.Margin) / 100))
		info.Source = "live"
	case info.Manual > 0:
		info.Rate, info.Source = info.Manual, "manual"
	}
	return info
}

// UsdtRate is the Toman price of one USDT the store charges now (0: none), the one every conversion uses.
func (s *Service) UsdtRate(ctx context.Context) int64 { return s.UsdtRateInfo(ctx).Rate }

// RefreshUsdtRate asks the bot for the live price and keeps it. It fails when the bot is not there, answers badly, or no price source did.
func (s *Service) RefreshUsdtRate(ctx context.Context) (LiveRate, error) {
	if s.cfg.RateTransport == nil {
		return LiveRate{}, errors.New("the live rate needs the bot, and none is connected")
	}
	status, body, err := s.cfg.RateTransport.PostUSDTRate(ctx)
	if err != nil {
		return LiveRate{}, err
	}
	var in struct {
		Rate    int64    `json:"rate"`
		Sources []string `json:"sources"`
		Message string   `json:"message"`
	}
	if json.Unmarshal(body, &in) != nil || status != 200 {
		msg := in.Message
		if msg == "" {
			msg = fmt.Sprintf("the bot answered %d", status)
		}
		return LiveRate{}, errors.New(msg)
	}
	if in.Rate < 1_000 || in.Rate > 100_000_000 {
		return LiveRate{}, errors.New("the live rate is out of range")
	}
	l := LiveRate{Rate: in.Rate, At: time.Now().Unix(), Sources: in.Sources}
	raw, _ := json.Marshal(l)
	if err := s.st.SetSetting(ctx, s.st.Conn(), liveRateKey, raw); err != nil {
		return LiveRate{}, err
	}
	return l, nil
}

// RunUsdtRateWorker keeps the live rate fresh while the store uses it: every two minutes when automatic rates are on and the store takes
// crypto (an address is set) or sells licenses.
func (s *Service) RunUsdtRateWorker(ctx context.Context) {
	t := time.NewTicker(liveRateEvery)
	defer t.Stop()
	first := time.After(10 * time.Second)
	read := func() {
		// It runs while the store takes crypto (an address is set) or sells licenses (priced in USDT): both need the rate.
		if !s.UsdtAutoOn(ctx) || (s.setting(ctx, "payments.usdt_trc20") == "" && s.setting(ctx, "payments.usdt_erc20") == "" && !s.VendorOn(ctx) &&
			!s.autoCrypto(ctx)) {
			return
		}
		if _, err := s.RefreshUsdtRate(ctx); err != nil && ctx.Err() == nil {
			s.cfg.Log.Warn("live USDT rate", "err", err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
			read()
		case <-t.C:
			read()
		}
	}
}

// SetAutoCrypto sets how core asks whether the store takes automatic crypto (automatic USDT or TON switched on in
// payments), which needs the live rate too (cmd/core).
func (s *Service) SetAutoCrypto(f func(ctx context.Context) bool) { s.cfg.AutoCrypto = f }

func (s *Service) autoCrypto(ctx context.Context) bool {
	return s.cfg.AutoCrypto != nil && s.cfg.AutoCrypto(ctx)
}

// UsdtSettingRaw is the effective rate as the raw JSON the bot reads in place of the stored setting ("" when there is none).
func (s *Service) UsdtSettingRaw(ctx context.Context) string {
	if r := s.UsdtRate(ctx); r > 0 {
		b, _ := json.Marshal(fmt.Sprint(r))
		return strings.TrimSpace(string(b))
	}
	return ""
}
