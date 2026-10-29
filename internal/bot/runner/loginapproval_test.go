package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/loginwire"
)

type fakeApprovals struct {
	mu          sync.Mutex
	requests    []loginwire.Request
	resolutions []loginwire.Resolution
	notices     []loginwire.Notice
	resolveErr  error
}

func (f *fakeApprovals) SendLoginApproval(_ context.Context, r loginwire.Request) []loginwire.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	var out []loginwire.Result
	for i, t := range r.Targets {
		out = append(out, loginwire.Result{TelegramID: t.TelegramID, MessageID: int64(40 + i)})
	}
	return out
}

func (f *fakeApprovals) ResolveLoginApproval(_ context.Context, r loginwire.Resolution) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolutions = append(f.resolutions, r)
	return f.resolveErr
}

func (f *fakeApprovals) NoticeLoginApproval(_ context.Context, n loginwire.Notice) []loginwire.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, n)
	var out []loginwire.Result
	for _, t := range n.Targets {
		out = append(out, loginwire.Result{TelegramID: t.TelegramID, Error: loginwire.ErrBlocked})
	}
	return out
}

const approvalTestToken = "core-token-0123456789abcdef0123456789"

func approvalPost(h http.Handler, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/login-approval/x", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const approvalRequestBody = `{"approval_id":"0192f3a41b2c7d3e8f40516273849abc","account":{"username":"adam","telegram_id":871,"role":"admin"},
"method":"link","ip":"203.0.113.7","browser":"chrome","system":"windows","at":1792500000,"expires_at":1792500300,
"targets":[{"telegram_id":871,"language":"en","role":"self","nonce":"Zm9vYmFyYmF6Zm9v"},{"telegram_id":9000,"language":"fa","role":"owner","nonce":"AAAAAAAAAAAAAAAA"}]}`

// Core's call to message the approvers: its token only, a body of the agreed shape only, one result for each person.
func TestLoginApprovalRequestEndpoint(t *testing.T) {
	f := &fakeApprovals{}
	h := LoginApprovalRequest(f, approvalTestToken, nil)

	for name, token := range map[string]string{"no token": "", "a wrong token": "nope", "a token with a suffix": approvalTestToken + "x"} {
		if rec := approvalPost(h, token, approvalRequestBody); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if len(f.requests) != 0 {
		t.Fatal("an unauthenticated call reached the handler")
	}
	rec := approvalPost(h, approvalTestToken, approvalRequestBody)
	var out loginwire.Response
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Results) != 2 || out.Results[0].TelegramID != 871 || out.Results[1].MessageID != 41 {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	if len(f.requests) != 1 || f.requests[0].Account.Username != "adam" || f.requests[0].Targets[1].Nonce != "AAAAAAAAAAAAAAAA" {
		t.Fatalf("handler saw: %+v", f.requests)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the answer may be cached")
	}

	f.requests = nil
	for name, body := range map[string]string{
		"not JSON":                `{`,
		"an unknown field":        strings.Replace(approvalRequestBody, `"method":"link"`, `"method":"link","extra":1`, 1),
		"a bad method":            strings.Replace(approvalRequestBody, `"method":"link"`, `"method":"sms"`, 1),
		"an id of 31 digits":      strings.Replace(approvalRequestBody, "0192f3a41b2c7d3e8f40516273849abc", "0192f3a41b2c7d3e8f40516273849ab", 1),
		"a nonce that is short":   strings.Replace(approvalRequestBody, "Zm9vYmFyYmF6Zm9v", "Zm9v", 1),
		"no targets":              `{"approval_id":"0192f3a41b2c7d3e8f40516273849abc","account":{"telegram_id":871,"role":"admin"},"method":"link","at":1,"expires_at":2,"targets":[]}`,
		"trailing text":           approvalRequestBody + `{}`,
		"a body that is a number": `7`,
	} {
		rec := approvalPost(h, approvalTestToken, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if len(f.requests) != 0 {
		t.Fatalf("a refused body reached the handler: %+v", f.requests)
	}

	// A body larger than the limit is refused before it is read in full.
	huge := `{"approval_id":"` + strings.Repeat("a", loginwire.MaxBody) + `"}`
	if rec := approvalPost(h, approvalTestToken, huge); rec.Code != http.StatusBadRequest {
		t.Errorf("a huge body: %d", rec.Code)
	}
}

func TestLoginApprovalResolveEndpoint(t *testing.T) {
	f := &fakeApprovals{}
	h := LoginApprovalResolve(f, approvalTestToken, nil)
	body := `{"approval_id":"0192f3a41b2c7d3e8f40516273849abc","result":"expired","account":{"username":"adam","telegram_id":871,"role":"admin"},
"at":1792500300,"targets":[{"telegram_id":871,"language":"en","message_id":40},{"telegram_id":9000,"language":"fa","message_id":41}]}`
	if rec := approvalPost(h, "", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := approvalPost(h, approvalTestToken, body); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	if len(f.resolutions) != 1 || f.resolutions[0].Result != "expired" || len(f.resolutions[0].Targets) != 2 {
		t.Fatalf("handler saw: %+v", f.resolutions)
	}
	// A message that could not be written over yet is a 503, for core to try again.
	f.resolveErr = errors.New("telegram is down")
	if rec := approvalPost(h, approvalTestToken, body); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a failed edit: %d", rec.Code)
	}
	for name, bad := range map[string]string{
		"a result that is no result": strings.Replace(body, `"expired"`, `"deleted"`, 1),
		"a target with no message":   strings.Replace(body, `"message_id":40`, `"message_id":0`, 1),
	} {
		if rec := approvalPost(h, approvalTestToken, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestLoginApprovalNoticeEndpoint(t *testing.T) {
	f := &fakeApprovals{}
	h := LoginApprovalNotice(f, approvalTestToken, nil)
	body := `{"kind":"enabled","source":"dashboard","by":{"username":"boss","telegram_id":9000,"role":"owner"},"targets":[{"telegram_id":9000,"language":"en"}]}`
	if rec := approvalPost(h, "wrong", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %d", rec.Code)
	}
	rec := approvalPost(h, approvalTestToken, body)
	var out loginwire.Response
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Results) != 1 || out.Results[0].Error != loginwire.ErrBlocked {
		t.Fatalf("notice: %d %s", rec.Code, rec.Body)
	}
	if rec := approvalPost(h, approvalTestToken, strings.Replace(body, `"enabled"`, `"hacked"`, 1)); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown kind: %d", rec.Code)
	}
}
