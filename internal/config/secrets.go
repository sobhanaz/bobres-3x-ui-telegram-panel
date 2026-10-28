package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
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

// KeyExtras are the two optional values beside BOBRES_MASTER_KEY that a master
// key rotation (`bobres rotate-key`) puts in .env. They are set only on an
// install that has rotated (or is rotating); a fresh install has neither.
type KeyExtras struct {
	// Previous is the master key before the rotation (BOBRES_MASTER_KEY_PREVIOUS):
	// still accepted for reading while values sealed with it remain.
	Previous string
	// Tag is the key of crypto.Envelope.Tag (BOBRES_TAG_KEY, 64 hex characters):
	// set so that the identifiers Tag made stay the same under the new master key.
	Tag string
}

// keyExtras reads the extras: each empty when unset, and checked when set.
func keyExtras() (KeyExtras, error) {
	prev, err := Secret("BOBRES_MASTER_KEY_PREVIOUS")
	if err != nil {
		return KeyExtras{}, err
	}
	if prev != "" {
		if len(prev) < 32 {
			return KeyExtras{}, errors.New("BOBRES_MASTER_KEY_PREVIOUS must be at least 32 characters")
		}
		if strings.Contains(prev, placeholderMarker) {
			return KeyExtras{}, fmt.Errorf("BOBRES_MASTER_KEY_PREVIOUS still contains the %s placeholder", placeholderMarker)
		}
	}
	tag, err := Secret("BOBRES_TAG_KEY")
	if err != nil {
		return KeyExtras{}, err
	}
	if tag != "" && !tagKeyRe.MatchString(tag) {
		return KeyExtras{}, errors.New("BOBRES_TAG_KEY must be 64 hexadecimal characters")
	}
	return KeyExtras{Previous: prev, Tag: tag}, nil
}

var tagKeyRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// KeyConfig is all the `rekey` command of a service needs: its database and its keys.
type KeyConfig struct {
	DatabaseURL string
	// MasterKey is empty when the service has none (an install from before payments had one).
	MasterKey string
	KeyExtras KeyExtras
}

// LoadKeyConfig reads the database URL and the keys of the service it runs in, and nothing else, so
// a re-encryption does not depend on the rest of the service's settings.
func LoadKeyConfig() (KeyConfig, error) {
	var errs []error
	db, err := Secret("BOBRES_DATABASE_URL")
	errs = append(errs, err)
	if db == "" && err == nil {
		errs = append(errs, errors.New("BOBRES_DATABASE_URL is required"))
	}
	if strings.Contains(db, placeholderMarker) {
		errs = append(errs, fmt.Errorf("BOBRES_DATABASE_URL still contains the %s placeholder", placeholderMarker))
	}
	master, err := optionalMasterKey()
	errs = append(errs, err)
	extras, err := keyExtras()
	errs = append(errs, err)
	return KeyConfig{DatabaseURL: db, MasterKey: master, KeyExtras: extras}, errors.Join(errs...)
}

// Keyring is the set of keys a service's envelope works with: its master key and what a rotation added.
func (k KeyExtras) Keyring(master string) crypto.Keys {
	keys := crypto.Keys{Master: master, Tag: strings.ToLower(k.Tag)}
	if k.Previous != "" {
		keys.Previous = []string{k.Previous}
	}
	return keys
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
