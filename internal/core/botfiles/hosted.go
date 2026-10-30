package botfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxHostedAnswer bounds what the bot passes back of a hosted store's /metrics (a few kilobytes in practice).
const maxHostedAnswer = 2 << 20

// FetchHostedMetrics reads a hosted store's /metrics through the bot (domain.HostedTransport): the bot calls https://<domain>/metrics, on a
// public address only, with the token as a bearer credential, and passes back the store's status and body. The token is in the request to
// the bot only, never in an error.
func (c *Client) FetchHostedMetrics(ctx context.Context, domain, token string) (int, []byte, error) {
	data, err := json.Marshal(map[string]string{"domain": domain, "token": token})
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/hosted/metrics", bytes.NewReader(data))
	if err != nil {
		return 0, nil, fmt.Errorf("botfiles: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, errors.New("the bot did not answer")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHostedAnswer+1))
	if err != nil || len(raw) > maxHostedAnswer {
		return 0, nil, errors.New("the bot's answer is unreadable")
	}
	var out struct {
		Status  int    `json:"status"`
		Body    string `json:"body"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return 0, nil, fmt.Errorf("the bot answered %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Message != "" {
			return 0, nil, errors.New(clip(out.Message, 200))
		}
		return 0, nil, fmt.Errorf("the bot answered %d", resp.StatusCode)
	}
	return out.Status, []byte(out.Body), nil
}
