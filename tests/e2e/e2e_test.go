//go:build e2e

// Package e2e drives an installed BOBRES stack end to end, the way a store
// owner and a customer use it: through the fake Telegram Bot API
// (tools/tgfake) the bot polls, with VPN accounts created on a REAL 3x-ui
// panel. CI runs it against the compose stack after `bobres install`.
//
// Environment: BOBRES_E2E_TELEGRAM (the fake Telegram's URL), BOBRES_E2E_ADMIN_ID,
// BOBRES_E2E_SUB_BASE (the panel's subscription prefix), and XUI_TEST_URL,
// XUI_TEST_TOKEN and XUI_TEST_ALLOW_PRIVATE to check the panel. The "End-to-end
// install" step of .github/workflows/ci.yml is a complete run.
//
//	go test -tags e2e -count=1 -v ./tests/e2e/
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

const wait = 45 * time.Second

type sent struct {
	Method    string          `json:"method"`
	ChatID    int64           `json:"chat_id"`
	MessageID int             `json:"message_id"`
	Text      string          `json:"text"`
	Markup    json.RawMessage `json:"reply_markup"`
}

type button struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type message struct {
	id      int
	text    string
	buttons [][]button
}

type world struct {
	t    *testing.T
	base string
	hc   *http.Client
	seq  atomic.Int64
}

type person struct {
	w    *world
	id   int64
	name string
}

// env reads a required variable. Without it the test skips, unless
// BOBRES_E2E_REQUIRE=1 (set in CI), so a renamed variable cannot turn the job
// green without running anything.
func env(t *testing.T, k string) string {
	t.Helper()
	v := os.Getenv(k)
	if v == "" {
		if os.Getenv("BOBRES_E2E_REQUIRE") == "1" {
			t.Fatalf("%s not set and BOBRES_E2E_REQUIRE=1", k)
		}
		t.Skipf("%s not set: run against an installed stack (see the package comment)", k)
	}
	return v
}

// dump prints a person's chat and the toasts when the test failed, so a CI
// failure shows what the bot actually said.
func (p *person) dump() {
	if !p.w.t.Failed() {
		return
	}
	p.w.t.Logf("---- chat with %s (%d) ----", p.name, p.id)
	for _, m := range p.messages() {
		var labels []string
		for _, row := range m.buttons {
			for _, b := range row {
				labels = append(labels, b.Text+" ["+b.CallbackData+"]")
			}
		}
		p.w.t.Logf("#%d %q buttons=%v", m.id, m.text, labels)
	}
	for _, s := range p.w.log() {
		if s.Method == "answerCallbackQuery" && s.Text != "" {
			p.w.t.Logf("toast: %q", s.Text)
		}
	}
}

func (w *world) person(id int64, name string) *person { return &person{w: w, id: id, name: name} }

func (w *world) post(path string, body any) {
	w.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := w.hc.Post(w.base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		w.t.Fatalf("inject: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		w.t.Fatalf("inject: HTTP %d", resp.StatusCode)
	}
}

func (w *world) log() []sent {
	w.t.Helper()
	resp, err := w.hc.Get(w.base + "/_sent")
	if err != nil {
		w.t.Fatalf("read sent log: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out []sent
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		w.t.Fatalf("decode sent log: %v", err)
	}
	return out
}

// messages replays the log into each message's current state, oldest first.
func (p *person) messages() []message {
	byID := map[int]*message{}
	var order []int
	for _, s := range p.w.log() {
		if s.ChatID != p.id || s.MessageID == 0 {
			continue
		}
		m, ok := byID[s.MessageID]
		if !ok {
			m = &message{id: s.MessageID}
			byID[s.MessageID] = m
			order = append(order, s.MessageID)
		}
		if s.Method != "editMessageReplyMarkup" {
			m.text = s.Text
		}
		m.buttons = nil
		var mk struct {
			InlineKeyboard [][]button `json:"inline_keyboard"`
		}
		if len(s.Markup) > 0 && json.Unmarshal(s.Markup, &mk) == nil {
			m.buttons = mk.InlineKeyboard
		}
	}
	out := make([]message, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

func (p *person) from() map[string]any {
	return map[string]any{"id": p.id, "is_bot": false, "first_name": p.name, "username": p.name, "language_code": "en"}
}

func (p *person) text(s string) {
	p.w.t.Helper()
	p.w.post("/_inject", map[string]any{"message": map[string]any{
		"message_id": p.w.seq.Add(1), "date": time.Now().Unix(),
		"chat": map[string]any{"id": p.id, "type": "private"}, "from": p.from(), "text": s,
	}})
}

// press taps the newest button whose callback data equals data, or else
// starts with it, waiting for it to appear.
func (p *person) press(data string) {
	p.w.t.Helper()
	deadline := time.Now().Add(wait)
	for {
		ms := p.messages()
		for _, exact := range []bool{true, false} {
			for i := len(ms) - 1; i >= 0; i-- {
				for _, row := range ms[i].buttons {
					for _, b := range row {
						if (exact && b.CallbackData == data) || (!exact && strings.HasPrefix(b.CallbackData, data)) {
							p.w.post("/_inject", map[string]any{"callback_query": map[string]any{
								"id": strconv.FormatInt(p.w.seq.Add(1), 10), "from": p.from(), "data": b.CallbackData,
								"message": map[string]any{"message_id": ms[i].id, "date": time.Now().Unix(),
									"chat": map[string]any{"id": p.id, "type": "private"}},
							}})
							return
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			last := ""
			if len(ms) > 0 {
				last = ms[len(ms)-1].text
			}
			p.w.t.Fatalf("%s: no button %q within %s; last message: %q", p.name, data, wait, last)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// sees waits until a message to this person contains sub and returns its text.
func (p *person) sees(sub string) string {
	p.w.t.Helper()
	deadline := time.Now().Add(wait)
	for {
		ms := p.messages()
		for i := len(ms) - 1; i >= 0; i-- {
			if strings.Contains(ms[i].text, sub) {
				return ms[i].text
			}
		}
		if time.Now().After(deadline) {
			p.w.t.Fatalf("%s: never saw %q within %s", p.name, sub, wait)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func TestStoreOwnerAndCustomer(t *testing.T) {
	w := &world{t: t, base: strings.TrimRight(env(t, "BOBRES_E2E_TELEGRAM"), "/"), hc: &http.Client{Timeout: 10 * time.Second}}
	adminID, err := strconv.ParseInt(env(t, "BOBRES_E2E_ADMIN_ID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	subBase := env(t, "BOBRES_E2E_SUB_BASE")
	panelURL, panelToken := env(t, "XUI_TEST_URL"), env(t, "XUI_TEST_TOKEN")
	w.seq.Store(time.Now().UnixMilli())

	// The owner sets up a plan and the free trial from the bot.
	owner := w.person(adminID, "owner")
	t.Cleanup(owner.dump)
	owner.text("/start")
	owner.press("lang:en")
	owner.sees("Main menu")
	owner.text("/plan_add 150000 IRT 30 50 Monthly 50GB | ماهانه ۵۰ گیگ")
	owner.sees("Plan saved: Monthly 50GB")
	owner.text("/trial 1 1")
	owner.sees("Plan saved: Free trial")

	// A customer takes the trial: the account is created on the real panel.
	customerID := adminID + 1000
	carol := w.person(customerID, "carol")
	t.Cleanup(carol.dump)
	carol.text("/start")
	carol.press("lang:en")
	carol.press("trial")
	carol.sees("Your service is ready")

	// The owner credits the customer's wallet; the customer buys with it.
	owner.press("adm")
	owner.press("adm:find")
	owner.text(strconv.FormatInt(customerID, 10))
	owner.sees("@carol")
	owner.press("adm:adj:")
	owner.text("200000 welcome gift")
	owner.sees("New balance")
	carol.sees("Support added 200,000 Toman")

	carol.press("home")
	carol.press("buy")
	carol.press("plan:")
	carol.press("pay:w:")
	carol.sees("Paid from your wallet")

	// Two distinct subscriptions are delivered (events are at-least-once, so a
	// repeated notification must not count twice) and both exist on the panel.
	re := regexp.MustCompile(regexp.QuoteMeta(subBase) + `([A-Za-z0-9_-]+)`)
	subIDs := map[string]bool{}
	deadline := time.Now().Add(wait)
	for len(subIDs) < 2 {
		for _, m := range carol.messages() {
			for _, mm := range re.FindAllStringSubmatch(m.text, -1) {
				subIDs[mm[1]] = true
			}
		}
		if len(subIDs) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want 2 delivered subscriptions, got %v", subIDs)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(subIDs) != 2 {
		t.Fatalf("want exactly 2 subscription links in the chat, got %v", subIDs)
	}
	var opts []xui.Option
	if os.Getenv("XUI_TEST_ALLOW_PRIVATE") == "1" {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	c, err := xui.New(panelURL, panelToken, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for id := range subIDs {
		links, err := c.SubLinks(ctx, id)
		if err != nil || len(links) == 0 {
			t.Fatalf("subscription %s on the panel: %v (%d links)", id, err, len(links))
		}
		fmt.Printf("panel has subscription %s with %d link(s)\n", id, len(links))
	}
}
