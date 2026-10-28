package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The token of the Prometheus endpoint (docs/runbooks/monitoring.md, "Prometheus and Grafana"). GET /metrics answers
// only while the setting below holds a token's hash, and then only a request that carries the token.
//
// The token is write-only. What is stored is the SHA-256 of the token in hex, never the token, so a copy of the
// database cannot be used to read the metrics; the dashboard learns only whether one is set; a token that core makes
// is returned once, in the answer that made it; core's gRPC settings leave the key out (the bot reads every other
// setting); and the audit entries carry no value. The key is not one of SettingSpecs, so neither the Settings page
// nor the bot's /set can read or write it.

// SettingMetricsToken is the setting that holds the hash of the metrics token.
const SettingMetricsToken = "monitor.metrics_token"

// The length of a token the owner types (a token core makes is 46 characters). The characters are those of a bearer
// token (RFC 6750): letters, digits and . _ ~ + / - and =.
const (
	MetricsTokenMin = 24
	MetricsTokenMax = 128
)

var metricsTokenRe = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

// metricsTokenHash is the SHA-256 of a token. A token made by core has 256 bits of its own, so an unsalted hash
// cannot be searched; a token of the owner's own is only as good as its owner's choice, which the dashboard says.
func metricsTokenHash(token string) [sha256.Size]byte { return sha256.Sum256([]byte(token)) }

// metricsHash reads the stored hash. ok is false when none is set; a stored value that is not a hash counts as
// none (it opens nothing).
func (s *Service) metricsHash(ctx context.Context) (hash [sha256.Size]byte, ok bool, err error) {
	raw, err := s.st.GetSetting(ctx, s.st.Conn(), SettingMetricsToken)
	if errors.Is(err, store.ErrNotFound) {
		return hash, false, nil
	}
	if err != nil {
		return hash, false, err
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return hash, false, nil
	}
	b, derr := hex.DecodeString(strings.TrimSpace(v))
	if derr != nil || len(b) != sha256.Size {
		return hash, false, nil
	}
	copy(hash[:], b)
	return hash, true, nil
}

// MetricsEnabled says whether a metrics token is set, which is whether /metrics answers at all.
func (s *Service) MetricsEnabled(ctx context.Context) (bool, error) {
	_, ok, err := s.metricsHash(ctx)
	return ok, err
}

// CheckMetricsToken tells /metrics what to do with a request: enabled is false when no token is set (the endpoint
// does not exist), ok when the presented token is the one. The comparison is of two hashes of the same length, in
// constant time.
func (s *Service) CheckMetricsToken(ctx context.Context, presented string) (enabled, ok bool, err error) {
	stored, set, err := s.metricsHash(ctx)
	if err != nil || !set {
		return false, false, err
	}
	sum := metricsTokenHash(presented)
	return true, subtle.ConstantTimeCompare(sum[:], stored[:]) == 1, nil
}

// saveMetricsToken stores a token's hash and writes the audit entry, in one transaction.
func (s *Service) saveMetricsToken(ctx context.Context, actor *store.User, token, action string) error {
	sum := metricsTokenHash(token)
	raw, err := json.Marshal(hex.EncodeToString(sum[:]))
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetSetting(ctx, tx, SettingMetricsToken, raw); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, action, "metrics", "token", map[string]any{"enabled": true}, "")
	})
}

// GenerateMetricsToken makes a token of 256 random bits ("bm_" and 43 characters), stores its hash and returns it. This is
// the only time it can be read. It replaces any token there was, which stops working at once.
func (s *Service) GenerateMetricsToken(ctx context.Context, actor *store.User) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("metrics token: %w", err)
	}
	token := "bm_" + base64.RawURLEncoding.EncodeToString(b[:])
	if err := s.saveMetricsToken(ctx, actor, token, "metrics.token.generate"); err != nil {
		return "", err
	}
	return token, nil
}

// SetMetricsToken stores a token the owner chose, replacing any there was.
func (s *Service) SetMetricsToken(ctx context.Context, actor *store.User, token string) error {
	token = strings.TrimSpace(token)
	switch {
	case len(token) < MetricsTokenMin || len(token) > MetricsTokenMax:
		return &FieldError{"token", fmt.Sprintf("%d to %d characters", MetricsTokenMin, MetricsTokenMax)}
	case !metricsTokenRe.MatchString(token):
		return &FieldError{"token", "only letters, digits and . _ ~ + / = - are allowed"}
	}
	return s.saveMetricsToken(ctx, actor, token, "metrics.token.set")
}

// ClearMetricsToken removes the token: /metrics answers 404 again. Removing none is not an error and writes no entry.
func (s *Service) ClearMetricsToken(ctx context.Context, actor *store.User) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.st.GetSetting(ctx, tx, SettingMetricsToken); errors.Is(err, store.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		if err := s.st.DeleteSetting(ctx, tx, SettingMetricsToken); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "metrics.token.clear", "metrics", "token", map[string]any{"enabled": false}, "")
	})
}
