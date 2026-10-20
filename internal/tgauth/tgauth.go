// Package tgauth checks the initData a Telegram Mini App is opened with
// (https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app):
// a query string Telegram signs with a key derived from the bot's token. Only
// the bot holds that token, so only the bot calls Validate (core asks it, see
// internal/bot/runner/miniapp.go).
package tgauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxInitData bounds the string Validate reads (real ones are under 2 KiB).
const MaxInitData = 8 << 10

// Errors Validate returns.
var (
	ErrMalformed = errors.New("tgauth: malformed init data")
	ErrSignature = errors.New("tgauth: bad signature")
	ErrExpired   = errors.New("tgauth: init data too old")
)

// User is the Telegram user in the init data.
type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

// Data is validated init data.
type Data struct {
	User     User
	AuthDate time.Time
}

// Validate checks initData against the bot token: the hash is the HMAC-SHA256
// of the sorted "key=value" lines (without hash) under HMAC-SHA256("WebAppData",
// token), compared in constant time; auth_date may not be older than maxAge
// (or in the future by more than a minute).
func Validate(initData, botToken string, maxAge time.Duration, now time.Time) (*Data, error) {
	if botToken == "" || initData == "" || len(initData) > MaxInitData {
		return nil, ErrMalformed
	}
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return nil, ErrMalformed
	}
	hash := vals.Get("hash")
	if len(vals["hash"]) != 1 || len(hash) != sha256.Size*2 {
		return nil, ErrMalformed
	}
	got, err := hex.DecodeString(hash)
	if err != nil {
		return nil, ErrMalformed
	}
	lines := make([]string, 0, len(vals))
	for k, v := range vals {
		// Every received field but "hash" (the value being checked) is in the
		// data-check-string, "signature" too: Telegram leaves it out only of the
		// Ed25519 check for third parties, which is not what runs here.
		if k == "hash" || len(v) != 1 {
			if k != "hash" {
				return nil, ErrMalformed // a repeated key is not what Telegram sends
			}
			continue
		}
		lines = append(lines, k+"="+v[0])
	}
	sort.Strings(lines)
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	want := hmacSHA256(secret, []byte(strings.Join(lines, "\n")))
	if !hmac.Equal(want, got) {
		return nil, ErrSignature
	}
	sec, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil || sec <= 0 {
		return nil, ErrMalformed
	}
	issued := time.Unix(sec, 0)
	if now.Sub(issued) > maxAge || issued.Sub(now) > time.Minute {
		return nil, ErrExpired
	}
	var u User
	if err := json.Unmarshal([]byte(vals.Get("user")), &u); err != nil || u.ID <= 0 || u.IsBot {
		return nil, ErrMalformed
	}
	return &Data{User: u, AuthDate: issued}, nil
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// Sign makes init data the way Telegram does; tests (here and in the services
// that verify) use it as the client.
func Sign(botToken string, user User, authDate time.Time, extra url.Values) string {
	ub, _ := json.Marshal(user)
	vals := url.Values{}
	for k, v := range extra {
		vals[k] = v
	}
	vals.Set("user", string(ub))
	vals.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	lines := make([]string, 0, len(vals))
	for k, v := range vals {
		lines = append(lines, k+"="+v[0])
	}
	sort.Strings(lines)
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	vals.Set("hash", hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(lines, "\n")))))
	return vals.Encode()
}
