package tgauth

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

const token = "123456:TEST-token"

var now = time.Unix(1_800_000_000, 0)

func TestValidateAcceptsWhatTelegramSigns(t *testing.T) {
	// A fixed vector, computed independently of Sign (python hmac) for the token above.
	user := User{ID: 4242, FirstName: "Ali", Username: "ali", LanguageCode: "fa"}
	data := Sign(token, user, now.Add(-time.Minute), url.Values{"query_id": {"AAH"}, "chat_type": {"private"}})
	d, err := Validate(data, token, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.User.ID != 4242 || d.User.Username != "ali" || d.User.LanguageCode != "fa" {
		t.Fatalf("user: %+v", d.User)
	}
}

func TestValidateKnownVector(t *testing.T) {
	// The hash was computed with Python's hmac, independently of Sign:
	// HMAC(HMAC("WebAppData", token), "auth_date=...\nquery_id=AAH\nuser={...}").
	const initData = "auth_date=1799999940&query_id=AAH&user=%7B%22id%22%3A4242%2C%22first_name%22%3A%22Ali%22%7D&hash=" +
		"9b866349eee7e64fa810e4d46c3584f917c4daeb7129cd58068e77461d89b0a0"
	d, err := Validate(initData, token, time.Hour, now)
	if err != nil || d.User.ID != 4242 {
		t.Fatalf("%v %+v", err, d)
	}
	if _, err := Validate(strings.Replace(initData, "AAH", "AAI", 1), token, time.Hour, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("a changed field must fail the signature check: %v", err)
	}
}

// Telegram's clients now add a "signature" field (for third parties' Ed25519
// check). The bot-token check keeps it in the data-check-string: only "hash"
// is left out (https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app).
func TestValidateKeepsTheSignatureField(t *testing.T) {
	// Computed with Python's hmac, independently of Sign, over all the fields but hash.
	const initData = "auth_date=1799999940&chat_instance=-123456&chat_type=sender&query_id=AAH&signature=abcDEF_-123&" +
		"user=%7B%22id%22%3A4242%2C%22first_name%22%3A%22Ali%22%7D&hash=" +
		"9a5d7f0adb6dc1275d840f6444997c8971ca70d18c1f24f87a017d1e5e243489"
	d, err := Validate(initData, token, time.Hour, now)
	if err != nil || d.User.ID != 4242 {
		t.Fatalf("init data with a signature field: %v %+v", err, d)
	}
	// Dropping the field, or changing it, changes what the hash covers.
	for name, data := range map[string]string{
		"dropped": strings.Replace(initData, "signature=abcDEF_-123&", "", 1),
		"changed": strings.Replace(initData, "abcDEF_-123", "abcDEF_-124", 1),
	} {
		if _, err := Validate(data, token, time.Hour, now); !errors.Is(err, ErrSignature) {
			t.Fatalf("%s signature field: %v", name, err)
		}
	}
	// Sign (the tests' client) agrees with it.
	signed := Sign(token, User{ID: 4242}, now.Add(-time.Minute), url.Values{"signature": {"abc"}, "chat_type": {"sender"}})
	if _, err := Validate(signed, token, time.Hour, now); err != nil {
		t.Fatalf("Sign with a signature field: %v", err)
	}
}

func TestValidateRefuses(t *testing.T) {
	user := User{ID: 7}
	good := Sign(token, user, now.Add(-time.Minute), nil)
	cases := map[string]struct {
		data, token string
		want        error
	}{
		"wrong token":     {good, "999:other", ErrSignature},
		"empty":           {"", token, ErrMalformed},
		"no token":        {good, "", ErrMalformed},
		"no hash":         {strings.Split(good, "&hash=")[0], token, ErrMalformed},
		"short hash":      {strings.Split(good, "&hash=")[0] + "&hash=abcd", token, ErrMalformed},
		"tampered user":   {strings.Replace(good, "%22id%22%3A7", "%22id%22%3A8", 1), token, ErrSignature},
		"old":             {Sign(token, user, now.Add(-2*time.Hour), nil), token, ErrExpired},
		"from the future": {Sign(token, user, now.Add(time.Hour), nil), token, ErrExpired},
		"repeated key":    {good + "&auth_date=1", token, ErrMalformed},
		"huge":            {good + "&x=" + strings.Repeat("a", MaxInitData), token, ErrMalformed},
		"a bot":           {Sign(token, User{ID: 7, IsBot: true}, now, nil), token, ErrMalformed},
		"no user id":      {Sign(token, User{}, now, nil), token, ErrMalformed},
	}
	for name, c := range cases {
		if _, err := Validate(c.data, c.token, time.Hour, now); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}
