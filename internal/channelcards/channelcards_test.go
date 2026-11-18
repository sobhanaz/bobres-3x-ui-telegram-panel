package channelcards

import (
	"strings"
	"testing"
)

const reviews = `[{"label":{"fa":"کانال نظرات","en":"Reviews"},"post":"https://t.me/heisen_reviews/2"}]`

func TestParseAndFormat(t *testing.T) {
	cards, err := Parse(reviews)
	if err != nil || len(cards) != 1 {
		t.Fatalf("Parse = %+v, %v", cards, err)
	}
	c := cards[0]
	if c.Label["fa"] != "کانال نظرات" || c.Label["en"] != "Reviews" || c.Channel != "https://t.me/heisen_reviews" ||
		c.Post.Chat != "@heisen_reviews" || c.Post.ID != 2 || len(c.ID) != 8 {
		t.Errorf("card = %+v", c)
	}
	if c.ID != ID("https://t.me/heisen_reviews/2") {
		t.Errorf("id = %q is not made from the post's link", c.ID)
	}
	if got := Format(cards); got != reviews {
		t.Errorf("Format = %s, want %s", got, reviews)
	}
}

func TestNormalize(t *testing.T) {
	for raw, want := range map[string]string{
		"":     "",
		"  ":   "",
		"null": "",
		"[]":   "",
		// fa then en whatever the order typed, spaces collapsed, an unneeded channel dropped, the post made canonical.
		` [ {"post":" https://t.me/s/heisen_reviews/2?single ","channel":"https://telegram.me/heisen_reviews","label":{"en":"  Reviews  ","fa":"کانال   نظرات"}} ] `: reviews,
		// a channel other than the post's is kept; the text of another language and an empty one
		`[{"label":{"tr":"Yorumlar","fa":""},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/45"}]`: `[{"label":{"tr":"Yorumlar"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/45"}]`,
		// an HTML-looking text stays as it is
		`[{"label":{"en":"A & B <3"},"post":"https://t.me/heisen_reviews/2"}]`: `[{"label":{"en":"A & B <3"},"post":"https://t.me/heisen_reviews/2"}]`,
	} {
		got, err := Normalize(raw)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", raw, got, err, want)
		}
		if again, err := Normalize(got); err != nil || again != got {
			t.Errorf("Normalize is not stable on %q: %q, %v", got, again, err)
		}
	}
}

func TestOrderKept(t *testing.T) {
	raw := `[{"label":{"fa":"ب"},"post":"https://t.me/bbbbbb/1"},{"label":{"fa":"آ"},"post":"https://t.me/aaaaaa/1"}]`
	cards, err := Parse(raw)
	if err != nil || len(cards) != 2 || cards[0].Label["fa"] != "ب" || cards[1].Label["fa"] != "آ" {
		t.Fatalf("Parse = %+v, %v", cards, err)
	}
	if Format(cards) != raw {
		t.Errorf("Format = %s", Format(cards))
	}
}

func TestParseRefusals(t *testing.T) {
	long := strings.Repeat("ا", LabelMax+1)
	var many []string
	for i := 0; i < Max+1; i++ {
		many = append(many, `{"label":{"fa":"x"},"post":"https://t.me/channel`+strings.Repeat("a", i)+`/1"}`)
	}
	for name, raw := range map[string]string{
		"not json":          `[{`,
		"an object":         `{"label":{"fa":"x"},"post":"https://t.me/heisen_reviews/2"}`,
		"a string":          `"x"`,
		"trailing text":     reviews + ` x`,
		"two lists":         reviews + reviews,
		"stray bracket":     `[]]`,
		"stray brace":       reviews + `}`,
		"stray bracket 2":   reviews + `]`,
		"unknown field":     `[{"label":{"fa":"x"},"post":"https://t.me/heisen_reviews/2","extra":1}]`,
		"label not object":  `[{"label":"x","post":"https://t.me/heisen_reviews/2"}]`,
		"no label":          `[{"post":"https://t.me/heisen_reviews/2"}]`,
		"blank label":       `[{"label":{"fa":"  "},"post":"https://t.me/heisen_reviews/2"}]`,
		"unknown language":  `[{"label":{"xx":"x"},"post":"https://t.me/heisen_reviews/2"}]`,
		"label too long":    `[{"label":{"fa":"` + long + `"},"post":"https://t.me/heisen_reviews/2"}]`,
		"label line break":  `[{"label":{"fa":"a\nb"},"post":"https://t.me/heisen_reviews/2"}]`,
		"no post":           `[{"label":{"fa":"x"}}]`,
		"post not a post":   `[{"label":{"fa":"x"},"post":"https://t.me/heisen_reviews"}]`,
		"post http":         `[{"label":{"fa":"x"},"post":"http://t.me/heisen_reviews/2"}]`,
		"private no invite": `[{"label":{"fa":"x"},"post":"https://t.me/c/1234567890/45"}]`,
		"bad channel":       `[{"label":{"fa":"x"},"channel":"https://example.com/x","post":"https://t.me/heisen_reviews/2"}]`,
		"channel is a post": `[{"label":{"fa":"x"},"channel":"https://t.me/heisen_reviews/2","post":"https://t.me/heisen_reviews/2"}]`,
		"same post twice":   `[{"label":{"fa":"x"},"post":"https://t.me/heisen_reviews/2"},{"label":{"fa":"y"},"post":"https://t.me/s/heisen_reviews/2?single"}]`,
		"too many":          "[" + strings.Join(many, ",") + "]",
	} {
		if cards, err := Parse(raw); err == nil {
			t.Errorf("%s: Parse = %+v, want an error", name, cards)
		}
	}
	// exactly Max is fine
	if cards, err := Parse("[" + strings.Join(many[:Max], ",") + "]"); err != nil || len(cards) != Max {
		t.Errorf("Max cards: %d, %v", len(cards), err)
	}
}

func TestTextFallsBack(t *testing.T) {
	c := Card{Label: map[string]string{"en": "Reviews", "tr": "Yorumlar"}}
	for lang, want := range map[string]string{"en": "Reviews", "fa": "Reviews", "tr": "Yorumlar", "ru": "Reviews", "xx": "Reviews"} {
		if got := c.Text(lang); got != want {
			t.Errorf("Text(%q) = %q, want %q", lang, got, want)
		}
	}
	if got := (Card{Label: map[string]string{"ru": "Отзывы"}}).Text("fa"); got != "Отзывы" {
		t.Errorf("only Russian written: %q", got)
	}
	if got := (Card{Label: map[string]string{"fa": "نظرات", "en": "Reviews"}}).Text("fa"); got != "نظرات" {
		t.Errorf("own language first: %q", got)
	}
}

func TestFind(t *testing.T) {
	cards, _ := Parse(reviews)
	if c, ok := Find(cards, cards[0].ID); !ok || c.ID != cards[0].ID {
		t.Errorf("Find by id: %+v, %v", c, ok)
	}
	if _, ok := Find(cards, "deadbeef"); ok {
		t.Error("Find of an id that is not there")
	}
}
