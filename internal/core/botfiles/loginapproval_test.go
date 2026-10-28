package botfiles

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/loginwire"
)

// loginServer plays the bot's approval endpoints: it checks core's token, remembers what it was sent and answers as told.
type loginServer struct {
	t      *testing.T
	path   string
	body   []byte
	status int
	answer string
}

func (s *loginServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer core-token" || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.path = r.URL.Path
	s.body, _ = io.ReadAll(r.Body)
	if s.status != 0 {
		w.WriteHeader(s.status)
	}
	_, _ = io.WriteString(w, s.answer)
}

func newLoginClient(t *testing.T, s *loginServer) *Client {
	t.Helper()
	s.t = t
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return New(srv.URL, "core-token")
}

func TestSendLoginApproval(t *testing.T) {
	s := &loginServer{answer: `{"results":[{"telegram_id":871,"message_id":55},{"telegram_id":9000,"error":"blocked"}]}`}
	c := newLoginClient(t, s)
	at := time.Unix(1792500000, 0)
	res, err := c.SendLoginApproval(context.Background(), domain.LoginPrompt{
		ApprovalID: "0192f3a41b2c7d3e8f40516273849abc", Account: domain.ApprovalAccount{Username: "adam", TelegramID: 871, Role: "admin"},
		Method: "link", IP: "203.0.113.7", Browser: "chrome", System: "windows", NewAddress: true, At: at, ExpiresAt: at.Add(5 * time.Minute),
		Targets: []domain.PromptTarget{{TelegramID: 871, Language: "en", Role: "self", Nonce: "Zm9vYmFyYmF6Zm9v"}, {TelegramID: 9000, Language: "fa", Role: "owner", Nonce: "AAAAAAAAAAAAAAAA"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].TelegramID != 871 || res[0].MessageID != 55 || res[0].Error != "" || res[1].Error != "blocked" {
		t.Fatalf("results: %+v", res)
	}
	if s.path != loginwire.PathRequest {
		t.Fatalf("path %q", s.path)
	}
	var sent loginwire.Request
	if err := json.Unmarshal(s.body, &sent); err != nil {
		t.Fatal(err)
	}
	if err := sent.Valid(); err != nil {
		t.Fatalf("what core sent is not what the bot takes: %v\n%s", err, s.body)
	}
	if sent.Account.Username != "adam" || sent.IP != "203.0.113.7" || !sent.NewAddress || sent.At != 1792500000 || sent.ExpiresAt != 1792500300 ||
		len(sent.Targets) != 2 || sent.Targets[1].Nonce != "AAAAAAAAAAAAAAAA" || sent.Targets[1].Role != "owner" {
		t.Fatalf("request: %s", s.body)
	}
	// The browser's own words never travel: only the codes.
	if strings.Contains(string(s.body), "Mozilla") {
		t.Fatalf("a User-Agent went to the bot: %s", s.body)
	}
}

func TestResolveAndNoticeLoginApproval(t *testing.T) {
	s := &loginServer{status: http.StatusNoContent}
	c := newLoginClient(t, s)
	at := time.Unix(1792500100, 0)
	if err := c.ResolveLoginApproval(context.Background(), domain.LoginResolution{ApprovalID: "0192f3a41b2c7d3e8f40516273849abc", Result: domain.ResolutionApproved,
		Account: domain.ApprovalAccount{Username: "adam", TelegramID: 871, Role: "admin"}, By: &domain.ApprovalAccount{TelegramID: 9000, Role: "owner"}, At: at,
		Targets: []domain.ResolveTarget{{TelegramID: 871, Language: "en", MessageID: 55}}}); err != nil {
		t.Fatal(err)
	}
	var res loginwire.Resolution
	if s.path != loginwire.PathResolve || json.Unmarshal(s.body, &res) != nil || res.Valid() != nil || res.By == nil || res.By.Role != "owner" || res.Targets[0].MessageID != 55 {
		t.Fatalf("resolve: %s %s", s.path, s.body)
	}

	s.status, s.answer = 0, `{"results":[{"telegram_id":9000}]}`
	results, err := c.NoticeLoginApproval(context.Background(), domain.ApprovalNotice{Kind: domain.NoticeDisabled, Source: "server",
		Targets: []domain.ResolveTarget{{TelegramID: 9000, Language: "fa"}}})
	if err != nil || len(results) != 1 || results[0].Error != "" {
		t.Fatalf("notice: %+v %v", results, err)
	}
	var n loginwire.Notice
	if s.path != loginwire.PathNotice || json.Unmarshal(s.body, &n) != nil || n.Valid() != nil || n.By != nil || n.Kind != "disabled" {
		t.Fatalf("notice body: %s %s", s.path, s.body)
	}
}

// A bot that is down, refuses core's token, or answers nonsense is an error, which never holds the nonce or the address.
func TestLoginApprovalCallsFailWithoutLeaking(t *testing.T) {
	prompt := domain.LoginPrompt{ApprovalID: "0192f3a41b2c7d3e8f40516273849abc", Account: domain.ApprovalAccount{Username: "adam", TelegramID: 871, Role: "admin"},
		Method: "link", IP: "203.0.113.77", At: time.Unix(1, 0), ExpiresAt: time.Unix(301, 0),
		Targets: []domain.PromptTarget{{TelegramID: 871, Language: "en", Role: "self", Nonce: "SECRETNONCE12345"}}}
	for name, s := range map[string]*loginServer{
		"the bot refuses":     {status: http.StatusServiceUnavailable, answer: "not done SECRETNONCE12345"},
		"not JSON":            {answer: "<html>203.0.113.77</html>"},
		"an answer too large": {answer: strings.Repeat("a", loginwire.MaxBody+10)},
	} {
		c := newLoginClient(t, s)
		_, err := c.SendLoginApproval(context.Background(), prompt)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if strings.Contains(err.Error(), "SECRETNONCE12345") || strings.Contains(err.Error(), "203.0.113.77") {
			t.Errorf("%s: the error holds a secret: %v", name, err)
		}
	}
	// A wrong token is the bot's 401.
	srv := httptest.NewServer(&loginServer{})
	defer srv.Close()
	if _, err := New(srv.URL, "wrong").SendLoginApproval(context.Background(), prompt); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a wrong token: %v", err)
	}
	// Nothing listens.
	srv.Close()
	if _, err := New(srv.URL, "core-token").SendLoginApproval(context.Background(), prompt); err == nil || strings.Contains(err.Error(), "SECRETNONCE12345") {
		t.Errorf("a bot that is gone: %v", err)
	}
}
