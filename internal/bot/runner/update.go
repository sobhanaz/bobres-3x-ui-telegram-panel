package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

// UpdateCheck tells the System page whether a newer release is out, at GET
// /internal/update-check: the last check's outcome (the bot looks every 12
// hours, and when the channel changes; core has no internet access), as
// release.Status JSON.
func UpdateCheck(status func() release.Status, coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeStatus(w, status())
	})
}

// UpdateCheckNow is the System page's "Check again", at POST
// /internal/update-check/refresh: the bot reloads the store settings (so a
// channel just saved in the dashboard is the one it looks at), looks at GitHub
// now, verifies what it finds, and answers with the outcome (release.Status
// JSON, as GET /internal/update-check does). A failed check is an answer too:
// it says in its error and detail why. It is protected as every /internal/
// endpoint is: core's service token.
//
// The look is bounded (Checker.ManualTimeout) and one look serves everyone
// who asks while it runs (Checker.CheckNow), so repeated clicks cost GitHub
// one request set, not one each.
func UpdateCheckNow(refresh func(context.Context) error, checkNow func(context.Context) release.Status, coreToken string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if refresh != nil {
			// Best effort: when core does not answer, the check follows the
			// channel the bot already has (the minute reload keeps trying).
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			if err := refresh(ctx); err != nil {
				log.Info("update check asked by core: settings not reloaded; using the channel the bot has", "err", err)
			}
			cancel()
		}
		writeStatus(w, checkNow(r.Context()))
	})
}

func writeStatus(w http.ResponseWriter, st release.Status) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(st)
}

// ChannelSetter is what follows the update channel (release.Checker).
type ChannelSetter interface {
	SetChannel(channel string) bool
}

// FollowUpdateChannel returns a function that makes the update check follow
// the channel the store has chosen: the updates.channel setting (read with
// setting, the bot's copy of core's settings) when it names one, else
// fallback (BOBRES_UPDATE_CHANNEL). Call it after every settings reload: a
// change of channel makes the check look again at once.
func FollowUpdateChannel(c ChannelSetter, setting func(key string) string, fallback string) func() {
	return func() {
		c.SetChannel(release.EffectiveChannel(setting(release.SettingChannel), fallback))
	}
}
