package runner

import (
	"encoding/json"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

// UpdateCheck tells the System page whether a newer release is out, at GET
// /internal/update-check: the last check's outcome (the bot looks every 12
// hours; core has no internet access), as release.Status JSON.
func UpdateCheck(status func() release.Status, coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(status())
	})
}
