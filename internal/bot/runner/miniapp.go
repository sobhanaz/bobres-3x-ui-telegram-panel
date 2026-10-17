package runner

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgauth"
)

// MiniAppMaxAge is how old the init data of a Mini App login may be.
const MiniAppMaxAge = time.Hour

// MiniAppVerify checks a Telegram Mini App's init data for core, at POST
// /internal/miniapp/verify with {"init_data"}. Only the bot holds the bot
// token (core keeps none), so core, which serves the Mini App, asks it. It
// answers 200 with {"ok":true,"user":{"id","username","first_name",
// "language_code"},"auth_date"} or {"ok":false,"problem":"malformed"|
// "signature"|"expired"}; the init data is not logged.
func MiniAppVerify(botToken, coreToken string, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in struct {
			InitData string `json:"init_data"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, tgauth.MaxInitData+1024)).Decode(&in); err != nil {
			http.Error(w, `body must be {"init_data"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		d, err := tgauth.Validate(in.InitData, botToken, MiniAppMaxAge, now())
		if err != nil {
			problem := "malformed"
			switch {
			case errors.Is(err, tgauth.ErrSignature):
				problem = "signature"
			case errors.Is(err, tgauth.ErrExpired):
				problem = "expired"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "problem": problem})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "auth_date": d.AuthDate.Unix(), "user": map[string]any{
			"id": d.User.ID, "username": d.User.Username, "first_name": d.User.FirstName, "language_code": d.User.LanguageCode,
		}})
	})
}
