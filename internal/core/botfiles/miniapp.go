package botfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrMiniAppAuth: the init data is not valid (wrong signature, too old or
// malformed). Anything else the bot or the network did is a plain error.
var ErrMiniAppAuth = errors.New("botfiles: mini app init data refused")

// MiniAppUser is who the init data says opened the Mini App.
type MiniAppUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	FirstName    string `json:"first_name"`
	LanguageCode string `json:"language_code"`
	// BotUsername is the bot's own @username, as the bot told with its answer
	// ("" from a bot that does not say).
	BotUsername string `json:"-"`
}

// VerifyMiniApp asks the bot to check a Mini App's init data: only the bot
// holds the token it is signed with.
func (c *Client) VerifyMiniApp(ctx context.Context, initData string) (*MiniAppUser, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]string{"init_data": initData})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/miniapp/verify", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("botfiles: mini app verify: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("botfiles: mini app verify: the bot answered %d", resp.StatusCode)
	}
	var out struct {
		OK          bool        `json:"ok"`
		Problem     string      `json:"problem"`
		BotUsername string      `json:"bot_username"`
		User        MiniAppUser `json:"user"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, fmt.Errorf("botfiles: mini app verify: %w", err)
	}
	if !out.OK {
		return nil, fmt.Errorf("%w: %s", ErrMiniAppAuth, out.Problem)
	}
	if out.User.ID <= 0 {
		return nil, errors.New("botfiles: mini app verify: the bot sent no user")
	}
	out.User.BotUsername = out.BotUsername
	return &out.User, nil
}

// ChannelMember asks the bot whether a Telegram user is a member of the
// channel or group the store requires (join.channel or one of its extra
// required chats: an @name or a -100… id; one chat for each call), for the
// Mini App's sign-in. Only the bot can ask Telegram: core has no token, and
// only the channel's administrators see its members, which the bot is. An error
// means the bot could not tell (Telegram did not answer, the bot is not the
// channel's administrator, the bot is an older one without the endpoint): the
// caller decides what that lets through.
func (c *Client) ChannelMember(ctx context.Context, channel string, telegramID int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]any{"chat": channel, "user_id": telegramID})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/miniapp/channel-member", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return false, fmt.Errorf("botfiles: channel member: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		OK      *bool  `json:"ok"`
		Member  *bool  `json:"member"`
		Problem string `json:"problem"`
		Detail  string `json:"detail"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	decodeErr := json.Unmarshal(raw, &out) // not JSON (a refused token, an older bot): the status says it
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("botfiles: channel member: the bot answered %d %s: %s", resp.StatusCode, clip(out.Problem, 40), clip(out.Detail, 200))
	}
	// A "no" is only an answer that says so: a field of the wrong type decodes as false.
	if decodeErr != nil || out.OK == nil || !*out.OK || out.Member == nil {
		return false, errors.New("botfiles: channel member: the bot gave no answer")
	}
	return *out.Member, nil
}
