package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// fakePoster answers by chat: @blocked refuses (not an admin), @gonechan does not exist, @slow does not answer, others are posted.
type fakePoster struct {
	chats []string
	kbs   []*tg.Keyboard
	texts []string
}

func (f *fakePoster) SendChatMessage(_ context.Context, chat, text string, kb *tg.Keyboard) (*tg.Message, error) {
	switch chat {
	case "@blocked":
		return nil, &tg.APIError{Code: 403, Description: "Forbidden: bot is not a member of the channel chat"}
	case "@norights":
		return nil, &tg.APIError{Code: 400, Description: "Bad Request: need administrator rights in the channel chat"}
	case "@gonechan":
		return nil, &tg.APIError{Code: 400, Description: "Bad Request: chat not found"}
	case "@slowchan":
		return nil, errors.New("telegram: sendMessage: context deadline exceeded")
	}
	f.chats, f.texts, f.kbs = append(f.chats, chat), append(f.texts, text), append(f.kbs, kb)
	return &tg.Message{MessageID: 900}, nil
}

// The channel post of an announcement: the card with a Buy button that opens the product in this bot (t.me/<bot>?start=<payload>); a
// refusal of Telegram is an answer, not an error, and only core may ask, with a chat and a payload of the right shapes.
func TestAnnounceChannel(t *testing.T) {
	bot := &fakePoster{}
	h := AnnounceChannel(bot, "shop_bot", coreTok, nil)
	post := func(token, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/internal/announce/channel", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		raw, _ := io.ReadAll(rec.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return rec.Code, out
	}
	body := func(chat, text, label, start string) string {
		b, _ := json.Marshal(map[string]string{"chat": chat, "text": text, "label": label, "start": start})
		return string(b)
	}
	good := body("@mystore", "🆕 <b>Gold</b>", "🛒 Buy", "p_0123456789abcdef0123456789abcdef")
	if code, _ := post("", good); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	code, out := post(coreTok, good)
	if code != http.StatusOK || out["ok"] != true || out["message_id"] != float64(900) {
		t.Fatalf("posted: %d %v", code, out)
	}
	kb := bot.kbs[0]
	if bot.chats[0] != "@mystore" || bot.texts[0] != "🆕 <b>Gold</b>" || len(kb.InlineKeyboard) != 1 ||
		kb.InlineKeyboard[0][0].URL != "https://t.me/shop_bot?start=p_0123456789abcdef0123456789abcdef" || kb.InlineKeyboard[0][0].Text != "🛒 Buy" {
		t.Fatalf("the post: %v %v %+v", bot.chats, bot.texts, kb)
	}
	for _, start := range []string{"s_abcdefghij", "l_pro_12", "l_standard_1"} {
		if code, out := post(coreTok, body("-1001234567890", "x", "Buy", start)); code != http.StatusOK || out["ok"] != true {
			t.Errorf("start %s: %d %v", start, code, out)
		}
	}
	for chat, problem := range map[string]string{"@blocked": ProblemNotAdmin, "@norights": ProblemNotAdmin, "@gonechan": ProblemNotFoundChat, "@slowchan": ProblemError} {
		if code, out := post(coreTok, body(chat, "x", "Buy", "l_pro_12")); code != http.StatusOK || out["ok"] != false || out["problem"] != problem {
			t.Errorf("%s: %d %v, want %s", chat, code, out, problem)
		}
	}
	if _, out := post(coreTok, body("@mystore", "<b>broken", "Buy", "l_pro_12")); out["problem"] != ProblemBadRequest {
		t.Errorf("a broken text: %v", out)
	}
	n := len(bot.chats)
	for _, bad := range []string{
		body("mystore", "x", "Buy", "l_pro_12"),
		body("@mystore", "x", "Buy", "r_abc"),
		body("@mystore", "x", "Buy", "p_123"),
		body("@mystore", "x", "Buy", "l_gold_3"),
		`{"chat":`,
	} {
		if code, _ := post(coreTok, bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if len(bot.chats) != n {
		t.Fatal("telegram was asked for a refused request")
	}
	noName := AnnounceChannel(bot, "", coreTok, nil)
	req := httptest.NewRequest(http.MethodPost, "/internal/announce/channel", strings.NewReader(good))
	req.Header.Set("Authorization", "Bearer "+coreTok)
	rec := httptest.NewRecorder()
	noName.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("without the bot's username: %s", rec.Body.String())
	}
}

// "Send to me" of an announcement carries its Buy button as a customer gets it; other callback data is refused.
func TestBroadcastTestBuyButton(t *testing.T) {
	bot := &fakeSender{}
	h := BroadcastTest(bot, coreTok, nil)
	post := func(body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/internal/broadcasts/test", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+coreTok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if out := post(`{"chat_id":5,"text":"card","buy":{"text":"🛒 Buy","data":"an:b:l:pro:12"}}`); out["ok"] != true {
		t.Fatalf("sent: %v", out)
	}
	if b := bot.sent[0].InlineKeyboard[0][0]; b.Text != "🛒 Buy" || b.CallbackData != "an:b:l:pro:12" {
		t.Fatalf("the button: %+v", b)
	}
	for _, bad := range []string{`{"text":"🛒 Buy","data":"adm:pend"}`, `{"text":"<b>x</b>","data":"an:b:l:pro:12"}`} {
		if out := post(`{"chat_id":5,"text":"card","buy":` + bad + `}`); out["problem"] != ProblemBadRequest {
			t.Errorf("%s: %v", bad, out)
		}
	}
	if len(bot.sent) != 1 {
		t.Fatalf("refused buttons were sent: %d", len(bot.sent))
	}
}
