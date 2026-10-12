package runner

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

func TestLogsOnlyForCore(t *testing.T) {
	kept := []logging.Line{{Time: 1760000000, Level: "WARN", Msg: "slow", Attrs: `{"user_id":7}`}}
	h := Logs(func() []logging.Line { return kept }, coreTok)
	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if code, _ := call(t, h, http.MethodGet, "/internal/logs", tok); code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, code)
		}
	}
	code, body := call(t, h, http.MethodGet, "/internal/logs", coreTok)
	var got struct{ Lines []logging.Line }
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if len(got.Lines) != 1 || got.Lines[0] != kept[0] {
		t.Fatalf("bad lines: %s", body)
	}
	if _, body := call(t, Logs(func() []logging.Line { return nil }, coreTok), http.MethodGet, "/internal/logs", coreTok); body != "{\"lines\":[]}\n" {
		t.Fatalf("empty: %q", body)
	}
}
