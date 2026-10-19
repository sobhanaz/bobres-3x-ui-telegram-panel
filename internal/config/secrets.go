package config

import (
	"errors"
	"fmt"
	"slices"
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

// MaxAdminTelegramIDs is how many root admins (the store owners a server's
// settings name) one install may have.
const MaxAdminTelegramIDs = 10

// ParseTelegramIDs parses a list of Telegram user ids separated by commas
// ("111,222, 333"): each a positive integer and nothing else (no "123abc",
// and no space inside one: "12 34" is a mistake, not two people), none
// twice, at most MaxAdminTelegramIDs. An empty or blank string is an empty
// list and returns nil.
func ParseTelegramIDs(raw string) ([]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ids []int64
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%q is not a positive numeric Telegram user id", f)
		}
		if slices.Contains(ids, id) {
			return nil, fmt.Errorf("the Telegram user id %d is listed twice", id)
		}
		ids = append(ids, id)
	}
	if len(ids) > MaxAdminTelegramIDs {
		return nil, fmt.Errorf("at most %d Telegram user ids may be listed, got %d", MaxAdminTelegramIDs, len(ids))
	}
	return ids, nil
}

// telegramIDs reads an optional list of Telegram user ids (see
// ParseTelegramIDs) from the environment; nil when unset.
func telegramIDs(name string) ([]int64, error) {
	ids, err := ParseTelegramIDs(getenv(name, ""))
	if err != nil {
		return nil, fmt.Errorf("%s must list positive numeric Telegram user ids separated by commas: %w", name, err)
	}
	return ids, nil
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
