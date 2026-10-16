package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgauth"
)

func TestMiniAppVerify(t *testing.T) {
	const botToken, core = "123456:BOT", "core-token"
	now := time.Unix(1_800_000_000, 0)
	h := MiniAppVerify(botToken, core, func() time.Time { return now })
	call := func(auth, initData string) (*httptest.ResponseRecorder, map[string]any) {
		body, _ := json.Marshal(map[string]string{"init_data": initData})
		req := httptest.NewRequest(http.MethodPost, "/internal/miniapp/verify", strings.NewReader(string(body)))
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec, out
	}
	good := tgauth.Sign(botToken, tgauth.User{ID: 9, Username: "u", LanguageCode: "en"}, now.Add(-time.Minute), nil)

	if rec, _ := call("", good); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec, _ := call("other", good); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	_, out := call(core, good)
	user, _ := out["user"].(map[string]any)
	if out["ok"] != true || user["id"] != float64(9) || user["language_code"] != "en" {
		t.Fatalf("good: %v", out)
	}
	for name, c := range map[string]struct{ data, problem string }{
		"signed for another bot": {tgauth.Sign("1:other", tgauth.User{ID: 9}, now, nil), "signature"},
		"expired":                {tgauth.Sign(botToken, tgauth.User{ID: 9}, now.Add(-2*time.Hour), nil), "expired"},
		"garbage":                {"x", "malformed"},
	} {
		if _, out := call(core, c.data); out["ok"] != false || out["problem"] != c.problem {
			t.Errorf("%s: %v", name, out)
		}
	}
	if strings.Contains(good, botToken) {
		t.Fatal("the init data must not carry the token")
	}
}
