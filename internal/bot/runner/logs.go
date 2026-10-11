package runner

import (
	"encoding/json"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

// Logs gives core the bot's kept warnings and errors (already redacted) for
// the System page, at GET /internal/logs: {"lines": [...]}, oldest first.
// They carry Telegram ids, so core shows them to the owner only.
func Logs(recent func() []logging.Line, coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		lines := recent()
		if lines == nil {
			lines = []logging.Line{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"lines": lines})
	})
}
