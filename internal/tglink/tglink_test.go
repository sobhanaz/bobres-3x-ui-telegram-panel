package tglink

import (
	"errors"
	"testing"
)

func TestParsePost(t *testing.T) {
	ok := []struct {
		raw  string
		want Post
		url  string
	}{
		{"https://t.me/heisen_reviews/2", Post{Chat: "@heisen_reviews", ID: 2, Username: "heisen_reviews"}, "https://t.me/heisen_reviews/2"},
		{"  https://t.me/heisen_reviews/2/  ", Post{Chat: "@heisen_reviews", ID: 2, Username: "heisen_reviews"}, "https://t.me/heisen_reviews/2"},
		{"https://t.me/heisen_reviews/2?single", Post{Chat: "@heisen_reviews", ID: 2, Username: "heisen_reviews"}, "https://t.me/heisen_reviews/2"},
		{"https://t.me/heisen_reviews/2#top", Post{Chat: "@heisen_reviews", ID: 2, Username: "heisen_reviews"}, "https://t.me/heisen_reviews/2"},
		{"https://t.me/s/heisen_reviews/17", Post{Chat: "@heisen_reviews", ID: 17, Username: "heisen_reviews"}, "https://t.me/heisen_reviews/17"},
		{"https://telegram.me/Heisen_Reviews/9", Post{Chat: "@Heisen_Reviews", ID: 9, Username: "Heisen_Reviews"}, "https://t.me/Heisen_Reviews/9"},
		{"https://T.ME/heisen_reviews/2", Post{Chat: "@heisen_reviews", ID: 2, Username: "heisen_reviews"}, "https://t.me/heisen_reviews/2"},
		{"https://t.me/c/1234567890/45", Post{Chat: "-1001234567890", ID: 45}, "https://t.me/c/1234567890/45"},
		{"https://t.me/abcde/2147483647", Post{Chat: "@abcde", ID: 2147483647, Username: "abcde"}, "https://t.me/abcde/2147483647"},
	}
	for _, tc := range ok {
		got, err := ParsePost(tc.raw)
		if err != nil {
			t.Errorf("ParsePost(%q): %v", tc.raw, err)
			continue
		}
		if got != tc.want || got.URL() != tc.url {
			t.Errorf("ParsePost(%q) = %+v (%s), want %+v (%s)", tc.raw, got, got.URL(), tc.want, tc.url)
		}
	}

	bad := []string{
		"",
		"   ",
		"heisen_reviews/2",
		"http://t.me/heisen_reviews/2",
		"https://example.com/heisen_reviews/2",
		"https://t.me.evil.com/heisen_reviews/2",
		"https://t.me:8443/heisen_reviews/2",
		"https://t.me:/heisen_reviews/2",     // an empty port
		"https://t.me/heisen_reviews/2#\x01", // a control character after the #
		"https://user@t.me/heisen_reviews/2",
		"https://t.me/",
		"https://t.me/heisen_reviews",     // a channel, not a post
		"https://t.me/heisen_reviews/0",   // there is no post 0
		"https://t.me/heisen_reviews/-3",  //
		"https://t.me/heisen_reviews/abc", //
		"https://t.me/heisen_reviews/2147483648",
		"https://t.me/heisen_reviews/12345678901",
		"https://t.me/abcd/2",               // names have 5 characters at least
		"https://t.me/1abcde/2",             // and start with a letter
		"https://t.me/heisen-reviews/2",     // no dashes
		"https://t.me/heisen_reviews/2/3",   // a post of a topic
		"https://t.me/c/123/4/5",            // likewise, private
		"https://t.me/c/abc/4",              //
		"https://t.me/c/12345678901234/4",   // more digits than a channel has
		"https://t.me/x/heisen_reviews/2",   // not the web preview's form
		"https://t.me/s/heisen_reviews",     // a channel's preview page
		"https://t.me/heisen_reviews/2 x",   // a space
		"https://t.me/heisen reviews/2",     //
		"https://t.me/+AbCdEfGh12/2",        // an invite link
		"tg://resolve?domain=heisen&post=2", //
	}
	for _, raw := range bad {
		if p, err := ParsePost(raw); err == nil {
			t.Errorf("ParsePost(%q) = %+v, want an error", raw, p)
		} else if !errors.Is(err, ErrPost) {
			t.Errorf("ParsePost(%q) error = %v, want ErrPost", raw, err)
		}
	}
}

func TestPostChannelURL(t *testing.T) {
	pub, _ := ParsePost("https://t.me/heisen_reviews/2")
	if got := pub.ChannelURL(); got != "https://t.me/heisen_reviews" {
		t.Errorf("public ChannelURL = %q", got)
	}
	priv, _ := ParsePost("https://t.me/c/1234567890/45")
	if got := priv.ChannelURL(); got != "" {
		t.Errorf("private ChannelURL = %q, want none: its invite link is the owner's to give", got)
	}
}

func TestParseChannel(t *testing.T) {
	ok := map[string]string{
		"https://t.me/heisen_reviews":           "https://t.me/heisen_reviews",
		" https://t.me/heisen_reviews/ ":        "https://t.me/heisen_reviews",
		"https://telegram.me/Heisen_Reviews":    "https://t.me/Heisen_Reviews",
		"https://t.me/heisen_reviews?start=abc": "https://t.me/heisen_reviews",
		"https://t.me/+AbCdEfGh12_-":            "https://t.me/+AbCdEfGh12_-",
		"https://t.me/joinchat/AbCdEfGh12":      "https://t.me/joinchat/AbCdEfGh12",
	}
	for raw, want := range ok {
		got, err := ParseChannel(raw)
		if err != nil || got != want {
			t.Errorf("ParseChannel(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	bad := []string{
		"",
		"https://t.me/",
		"https://t.me/heisen_reviews/2", // a post
		"https://t.me/abcd",
		"http://t.me/heisen_reviews",
		"https://example.com/heisen_reviews",
		"https://t.me/+abc",           // token too short
		"https://t.me/+abc$defghij",   //
		"https://t.me/joinchat/ab",    //
		"https://t.me/joinchat",       // Telegram's own path, not a channel
		"https://t.me/addstickers",    //
		"https://t.me/c/1234567890/4", //
		"@heisen_reviews",
	}
	for _, raw := range bad {
		if got, err := ParseChannel(raw); err == nil {
			t.Errorf("ParseChannel(%q) = %q, want an error", raw, got)
		} else if !errors.Is(err, ErrChannel) {
			t.Errorf("ParseChannel(%q) error = %v, want ErrChannel", raw, err)
		}
	}
}
