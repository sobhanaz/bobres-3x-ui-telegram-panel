// Package channelcards is the list of channel buttons in the bot's menu (the setting bot.channel_cards). Core checks and writes it when the owner
// saves it; the bot reads it to draw the buttons and to show what a tap opens. A card is a button's text in each language, the post of a channel
// that the bot copies into the customer's chat when the button is tapped, and the link of the channel, which is the View channel button under that
// copy.
package channelcards

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tglink"
)

const (
	// Setting is the setting that holds the list.
	Setting = "bot.channel_cards"
	// Max is the most buttons the menu takes: each is a row of its own, under Buy and Free trial.
	Max = 6
	// LabelMax is the longest button text, in characters.
	LabelMax = 40
)

// Card is one button of the menu.
type Card struct {
	// ID names the card in the callback data of its button: eight hex digits made from the post's link, so it stays when the list is reordered
	// and a menu sent before a change finds its card (or finds nothing, if the post was changed).
	ID string
	// Label is the button's text by language, at least one.
	Label map[string]string
	// Channel is the link behind the View channel button: the one the owner gave, or the channel of a public post.
	Channel string
	// Post is the post the bot copies when the button is tapped.
	Post tglink.Post
}

// Text is the button's text in a language, else in English, else in Persian, else in any other the owner wrote.
func (c Card) Text(lang string) string {
	for _, l := range slices.Concat([]string{lang, "en", "fa"}, i18n.All) {
		if t := c.Label[l]; t != "" {
			return t
		}
	}
	return ""
}

// ID is the identifier of the card whose post has this link.
func ID(postURL string) string {
	sum := sha256.Sum256([]byte(postURL))
	return hex.EncodeToString(sum[:4])
}

// Find returns the card with this id.
func Find(cards []Card, id string) (Card, bool) {
	for _, c := range cards {
		if c.ID == id {
			return c, true
		}
	}
	return Card{}, false
}

// labels is a card's text by language, written in the order of i18n.All so that the stored list does not depend on map order.
type labels map[string]string

func (l labels) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for _, lang := range i18n.All {
		t, ok := l[lang]
		if !ok {
			continue
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + lang + `":`)
		b.WriteString(jsonString(t))
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// jsonString writes a string as JSON without HTML escaping, so a text with an & or a < stays readable in the setting.
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string cannot fail
	return strings.TrimSpace(b.String())
}

// row is a card as the setting stores it.
type row struct {
	Label   labels `json:"label"`
	Channel string `json:"channel,omitempty"`
	Post    string `json:"post"`
}

var errNotList = errors.New(`not a list of channel buttons, like [{"label":{"fa":"…"},"post":"https://t.me/…/2"}]`)

// Parse reads the list as the setting holds it and checks it: at most Max cards, each with a text (LabelMax characters, on one line), a post
// link that Telegram's Copy Link gives, and a channel link where the post's channel is private; no post twice. "" and "null" are no cards.
func Parse(raw string) ([]Card, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var rows []row
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	// Nothing may follow the list, not even a stray bracket (Decoder.More lets a ] or a } through).
	if err := dec.Decode(&rows); err != nil {
		return nil, errNotList
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errNotList
	}
	if len(rows) > Max {
		return nil, fmt.Errorf("at most %d channel buttons", Max)
	}
	var out []Card
	posts := map[string]bool{}
	for i, r := range rows {
		n := i + 1
		label := map[string]string{}
		for lang, t := range r.Label {
			if !slices.Contains(i18n.All, lang) {
				return nil, fmt.Errorf("button %d: unknown language %q: use %s", n, lang, strings.Join(i18n.All, ", "))
			}
			if !utf8.ValidString(t) || strings.ContainsFunc(t, unicode.IsControl) {
				return nil, fmt.Errorf("button %d: the text must be one line of text", n)
			}
			t = strings.Join(strings.Fields(t), " ")
			if utf8.RuneCountInString(t) > LabelMax {
				return nil, fmt.Errorf("button %d: the text is at most %d characters", n, LabelMax)
			}
			if t != "" {
				label[lang] = t
			}
		}
		if len(label) == 0 {
			return nil, fmt.Errorf("button %d: it needs a text", n)
		}
		post, err := tglink.ParsePost(r.Post)
		if err != nil {
			return nil, fmt.Errorf("button %d: the post link %w", n, err)
		}
		channel := post.ChannelURL()
		switch {
		case strings.TrimSpace(r.Channel) != "":
			if channel, err = tglink.ParseChannel(r.Channel); err != nil {
				return nil, fmt.Errorf("button %d: the channel link %w", n, err)
			}
		case channel == "":
			return nil, fmt.Errorf("button %d: a private channel needs its invite link (https://t.me/+…)", n)
		}
		if posts[post.URL()] {
			return nil, fmt.Errorf("button %d: the post %s is on another button", n, post.URL())
		}
		posts[post.URL()] = true
		out = append(out, Card{ID: ID(post.URL()), Label: label, Channel: channel, Post: post})
	}
	return out, nil
}

// Format writes the cards the way the setting stores them: compact JSON in the order given, "" for none. A channel link that the post already
// implies is left out.
func Format(cards []Card) string {
	if len(cards) == 0 {
		return ""
	}
	rows := make([]row, 0, len(cards))
	for _, c := range cards {
		r := row{Label: c.Label, Post: c.Post.URL()}
		if c.Channel != c.Post.ChannelURL() {
			r.Channel = c.Channel
		}
		rows = append(rows, r)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(rows) // plain strings and maps cannot fail
	return strings.TrimSpace(b.String())
}

// Normalize checks a list and returns it as it is stored.
func Normalize(raw string) (string, error) {
	cards, err := Parse(raw)
	if err != nil {
		return "", err
	}
	return Format(cards), nil
}
