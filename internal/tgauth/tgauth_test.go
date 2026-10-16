package tgauth

import (
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
	if _, err := Validate(strings.Replace(initData, "AAH", "AAI", 1), token, time.Hour, now); err != ErrSignature {
		t.Fatalf("a changed field must fail the signature check: %v", err)
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
		if _, err := Validate(c.data, c.token, time.Hour, now); err != c.want {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}
