package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// minTokenLen is the minimum service token length per environment.
func minTokenLen(env string) int {
	if env == "prod" {
		return 32
	}
	return 16
}

// requireToken reads a service token (env or *_FILE) and enforces length and
// the absence of the CHANGE_ME placeholder.
func requireToken(name, env string) (string, error) {
	v, err := Secret(name)
	if err != nil {
		return "", err
	}
	if min := minTokenLen(env); len(v) < min {
		return "", fmt.Errorf("%s must be at least %d characters in %s", name, min, env)
	}
	if strings.Contains(v, placeholderMarker) {
		return "", fmt.Errorf("%s still contains the %s placeholder", name, placeholderMarker)
	}
	return v, nil
}

// requireDistinct rejects configurations that reuse one token for two
// identities: a shared token would let one service impersonate another.
func requireDistinct(tokens map[string]string) error {
	seen := map[string]string{}
	for name, v := range tokens {
		if v == "" {
			continue
		}
		if other, dup := seen[v]; dup {
			return fmt.Errorf("%s and %s must be different tokens", other, name)
		}
		seen[v] = name
	}
	return nil
}

// optionalMasterKey reads BOBRES_MASTER_KEY, enforcing 32+ characters when set.
func optionalMasterKey() (string, error) {
	v, err := Secret("BOBRES_MASTER_KEY")
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", nil
	}
	if len(v) < 32 {
		return "", errors.New("BOBRES_MASTER_KEY must be at least 32 characters")
	}
	if strings.Contains(v, placeholderMarker) {
		return "", fmt.Errorf("BOBRES_MASTER_KEY still contains the %s placeholder", placeholderMarker)
	}
	return v, nil
}

// telegramID parses an optional Telegram user id; when set it must be a
// positive integer and nothing else (no "123abc").
func telegramID(name string) (int64, error) {
	raw := strings.TrimSpace(getenv(name, ""))
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%s must be a positive numeric Telegram user id, got %q", name, raw)
	}
	return id, nil
}

func requireNonEmpty(name, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

// optionalToken is requireToken for a token that may be left unset ("").
func optionalToken(name, env string) (string, error) {
	if v, err := Secret(name); err != nil || v == "" {
		return "", err
	}
	return requireToken(name, env)
}
