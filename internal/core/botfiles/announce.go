package botfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// PostChannel asks the bot to post a product announcement in the store's channel (domain.AnnounceTransport). An error means the bot did not
// answer (core tries again); Telegram's refusal is a result that is not OK.
func (c *Client) PostChannel(ctx context.Context, p domain.ChannelPost) (*domain.ChannelPostResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]string{"chat": p.Chat, "text": p.Text, "label": p.Label, "start": p.Start})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/announce/channel", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("botfiles: channel post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest: // the bot refused what core sent: trying again cannot help
		return &domain.ChannelPostResult{Problem: "bad_request", Detail: "the bot refused the post"}, nil
	default:
		return nil, fmt.Errorf("botfiles: channel post: the bot answered %d", resp.StatusCode)
	}
	var out struct {
		OK        bool   `json:"ok"`
		MessageID int64  `json:"message_id"`
		Problem   string `json:"problem"`
		Detail    string `json:"detail"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, fmt.Errorf("botfiles: channel post: %w", err)
	}
	return &domain.ChannelPostResult{OK: out.OK, MessageID: out.MessageID, Problem: out.Problem, Detail: out.Detail}, nil
}
