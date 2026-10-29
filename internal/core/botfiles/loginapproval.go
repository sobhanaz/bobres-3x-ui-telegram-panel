package botfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/loginwire"
)

// The approval of dashboard sign-ins needs the bot because core has no Telegram token: it asks the bot to message the approvers, to write
// the end of a sign-in into those messages, and to tell the owners that the switch changed (internal/loginwire has the JSON). These make
// Client a domain.LoginApprovalBot.

var _ domain.LoginApprovalBot = (*Client)(nil)

// loginCall posts in to a login-approval endpoint of the bot and reads its answer into out (nil: none expected). An error is the bot being
// unreachable, refusing core's token or answering something else than 200; it never holds a message's contents or a nonce.
func (c *Client) loginCall(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("botfiles: %w", plainErr(err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("botfiles: %s: %w", path, plainErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, loginwire.MaxBody+1))
	if err != nil || len(raw) > loginwire.MaxBody {
		return fmt.Errorf("botfiles: %s: the answer cannot be read", path)
	}
	if out == nil && resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("botfiles: %s: the bot answered %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("botfiles: %s: the answer is not what was expected", path)
	}
	return nil
}

func wireAccount(a domain.ApprovalAccount) loginwire.Account {
	return loginwire.Account{Username: a.Username, TelegramID: a.TelegramID, Role: a.Role}
}

func wireAccountPtr(a *domain.ApprovalAccount) *loginwire.Account {
	if a == nil {
		return nil
	}
	w := wireAccount(*a)
	return &w
}

func promptResults(res []loginwire.Result) []domain.PromptResult {
	out := make([]domain.PromptResult, 0, len(res))
	for _, r := range res {
		out = append(out, domain.PromptResult{TelegramID: r.TelegramID, MessageID: r.MessageID, Error: clip(r.Error, 40)})
	}
	return out
}

// SendLoginApproval has the bot message the approvers of a sign-in.
func (c *Client) SendLoginApproval(ctx context.Context, p domain.LoginPrompt) ([]domain.PromptResult, error) {
	in := loginwire.Request{ApprovalID: p.ApprovalID, Account: wireAccount(p.Account), Method: p.Method, IP: p.IP, Browser: p.Browser, System: p.System,
		NewAddress: p.NewAddress, At: p.At.Unix(), ExpiresAt: p.ExpiresAt.Unix()}
	for _, t := range p.Targets {
		in.Targets = append(in.Targets, loginwire.Target{TelegramID: t.TelegramID, Language: t.Language, Role: t.Role, Nonce: t.Nonce})
	}
	var out loginwire.Response
	if err := c.loginCall(ctx, loginwire.PathRequest, in, &out); err != nil {
		return nil, err
	}
	return promptResults(out.Results), nil
}

// ResolveLoginApproval has the bot write how a sign-in ended into the messages sent for it.
func (c *Client) ResolveLoginApproval(ctx context.Context, r domain.LoginResolution) error {
	in := loginwire.Resolution{ApprovalID: r.ApprovalID, Result: r.Result, Account: wireAccount(r.Account), By: wireAccountPtr(r.By), At: r.At.Unix()}
	for _, t := range r.Targets {
		in.Targets = append(in.Targets, loginwire.Target{TelegramID: t.TelegramID, Language: t.Language, MessageID: t.MessageID})
	}
	return c.loginCall(ctx, loginwire.PathResolve, in, nil)
}

// NoticeLoginApproval has the bot tell the owners that the switch was changed.
func (c *Client) NoticeLoginApproval(ctx context.Context, n domain.ApprovalNotice) ([]domain.PromptResult, error) {
	in := loginwire.Notice{Kind: n.Kind, By: wireAccountPtr(n.By), Source: n.Source}
	for _, t := range n.Targets {
		in.Targets = append(in.Targets, loginwire.Target{TelegramID: t.TelegramID, Language: t.Language})
	}
	var out loginwire.Response
	if err := c.loginCall(ctx, loginwire.PathNotice, in, &out); err != nil {
		return nil, err
	}
	return promptResults(out.Results), nil
}
