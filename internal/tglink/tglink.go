// Package tglink reads the Telegram links the owner pastes into the dashboard: the link of a post in a channel (the bot copies that post into a
// customer's chat) and the link of a channel (behind a View channel button). Core checks a link when a setting is saved and the bot reads it
// again when it uses it, so both mean the same by it.
package tglink

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ErrPost and ErrChannel are what a link that is not of the kind is refused with (in English, for the dashboard to show as core's reason).
var (
	ErrPost    = errors.New("must be a link to a post in a Telegram channel, like https://t.me/yourchannel/12 (in Telegram: open the post, then Copy Link)")
	ErrChannel = errors.New("must be a https://t.me/… link to a channel: https://t.me/yourchannel, or a private channel's invite link (https://t.me/+…)")
)

var (
	// A public channel's or group's @name: 5 to 32 letters, digits and underscores, starting with a letter.
	nameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{4,31}$`)
	// The number of a private channel in a t.me/c/<number>/<post> link: the id without its -100.
	privateRe = regexp.MustCompile(`^[0-9]{1,13}$`)
	// The number of a post in its channel.
	numberRe = regexp.MustCompile(`^[0-9]{1,10}$`)
	// What follows t.me/+ or t.me/joinchat/ in an invite link.
	inviteRe = regexp.MustCompile(`^[A-Za-z0-9_-]{5,64}$`)
)

// reserved are the first parts of t.me paths that Telegram uses for something other than a channel's name.
var reserved = map[string]bool{
	"joinchat": true, "addstickers": true, "addemoji": true, "addtheme": true, "addlist": true, "share": true, "proxy": true,
	"socks": true, "login": true, "confirmphone": true, "setlanguage": true, "invoice": true, "giftcode": true, "boost": true,
}

// validName reports whether s can be the @name of a channel or group.
func validName(s string) bool { return nameRe.MatchString(s) && !reserved[strings.ToLower(s)] }

// maxMessageID is the largest message number Telegram gives out (32 bits).
const maxMessageID = 1<<31 - 1

// Post is a message of a channel (or group): where the bot copies a post from.
type Post struct {
	// Chat is the channel as the Bot API names it: "@name" for a public one, "-100<number>" for a private one.
	Chat string
	// ID is the post's number in that channel.
	ID int64
	// Username is a public channel's name without the @; "" for a private one.
	Username string
}

// URL is the post's link as it is stored: https://t.me/<name>/<n>, or https://t.me/c/<number>/<n> for a private channel.
func (p Post) URL() string {
	n := strconv.FormatInt(p.ID, 10)
	if p.Username != "" {
		return "https://t.me/" + p.Username + "/" + n
	}
	return "https://t.me/c/" + strings.TrimPrefix(p.Chat, "-100") + "/" + n
}

// ChannelURL is the link of the channel the post is in when it can be told from the post: a public channel's. A private channel's link is its
// invite link, which only the owner has: "" here.
func (p Post) ChannelURL() string {
	if p.Username == "" {
		return ""
	}
	return "https://t.me/" + p.Username
}

// segments reads a https://t.me/… (or telegram.me) link as the parts of its path, without the query and the fragment, which Telegram's own
// links carry now and then (?single). Anything else about the address is refused: another scheme or host, a port, a user, a space.
func segments(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return nil, ErrPost
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || strings.Contains(u.Host, ":") { // no port, not even an empty one
		return nil, ErrPost
	}
	if h := strings.ToLower(u.Hostname()); h != "t.me" && h != "telegram.me" {
		return nil, ErrPost
	}
	path := strings.Trim(u.EscapedPath(), "/")
	if path == "" {
		return nil, nil
	}
	return strings.Split(path, "/"), nil
}

// messageNumber reads the number of a post.
func messageNumber(s string) (int64, bool) {
	if !numberRe.MatchString(s) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 1 && n <= maxMessageID
}

// ParsePost reads the link of a post: https://t.me/<name>/<n> for a public channel, https://t.me/c/<number>/<n> for a private one. The web
// preview's form (t.me/s/<name>/<n>) and telegram.me are taken too.
func ParsePost(raw string) (Post, error) {
	segs, err := segments(raw)
	if err != nil {
		return Post{}, ErrPost
	}
	if len(segs) == 3 && segs[0] == "s" {
		segs = segs[1:]
	}
	switch len(segs) {
	case 2: // t.me/<name>/<n>
		id, ok := messageNumber(segs[1])
		if !validName(segs[0]) || !ok {
			return Post{}, ErrPost
		}
		return Post{Chat: "@" + segs[0], ID: id, Username: segs[0]}, nil
	case 3: // t.me/c/<number>/<n>
		id, ok := messageNumber(segs[2])
		if segs[0] != "c" || !privateRe.MatchString(segs[1]) || !ok {
			return Post{}, ErrPost
		}
		return Post{Chat: "-100" + segs[1], ID: id}, nil
	}
	return Post{}, ErrPost
}

// ParseChannel reads the link of a channel and returns it as it is stored: https://t.me/<name>, or an invite link (https://t.me/+<token>,
// https://t.me/joinchat/<token>).
func ParseChannel(raw string) (string, error) {
	segs, err := segments(raw)
	if err != nil {
		return "", ErrChannel
	}
	switch {
	case len(segs) == 1 && validName(segs[0]):
		return "https://t.me/" + segs[0], nil
	case len(segs) == 1 && strings.HasPrefix(segs[0], "+") && inviteRe.MatchString(segs[0][1:]):
		return "https://t.me/" + segs[0], nil
	case len(segs) == 2 && segs[0] == "joinchat" && inviteRe.MatchString(segs[1]):
		return "https://t.me/joinchat/" + segs[1], nil
	}
	return "", ErrChannel
}
