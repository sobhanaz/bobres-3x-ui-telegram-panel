package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/channelcards"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// bot.channel_cards is a list of the owner's channels as buttons of the bot's menu: stored as compact JSON in the order given, "" for none; the
// bot's admin menu leaves it to the dashboard.
func TestChannelCardsSetting(t *testing.T) {
	sp, ok := SpecOf(channelcards.Setting)
	if !ok || sp.Kind != KindList || sp.Group != "channels" || sp.Perm != permSettings || sp.Default != "" {
		t.Fatalf("spec: %+v", sp)
	}
	const reviews = `[{"label":{"fa":"کانال نظرات","en":"Reviews"},"post":"https://t.me/heisen_reviews/2"}]`
	for in, want := range map[string]string{
		"":   "",
		"[]": "",
		// whatever the order of the languages or the form of the links, what is stored is canonical
		` [{"post":"https://t.me/s/heisen_reviews/2?single","label":{"en":" Reviews ","fa":"کانال نظرات"}}] `:   reviews,
		`[{"label":{"fa":"خصوصی"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/45"}]`: `[{"label":{"fa":"خصوصی"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/45"}]`,
	} {
		if got, err := NormalizeSetting(channelcards.Setting, in); err != nil || got != want {
			t.Errorf("%q: %q, %v (want %q)", in, got, err, want)
		}
	}
	for name, bad := range map[string]string{
		"not json":         `[{`,
		"no text":          `[{"post":"https://t.me/heisen_reviews/2"}]`,
		"not a post":       `[{"label":{"fa":"x"},"post":"https://t.me/heisen_reviews"}]`,
		"private, no link": `[{"label":{"fa":"x"},"post":"https://t.me/c/1234567890/45"}]`,
		"seven buttons":    "[" + strings.TrimSuffix(strings.Repeat(`{"label":{"fa":"x"},"post":"https://t.me/channel1/2"},`, 7), ",") + "]",
	} {
		var fe *FieldError
		if _, err := NormalizeSetting(channelcards.Setting, bad); !errors.As(err, &fe) || fe.Field != channelcards.Setting {
			t.Errorf("%s accepted: %v", name, err)
		}
	}

	// Six buttons with the longest texts in every language fit: the list is not held to the 1000 bytes of an ordinary setting.
	text := strings.Repeat("ک", channelcards.LabelMax)
	var rows []string
	for _, ch := range []string{"channel1", "channel2", "channel3", "channel4", "channel5", "channel6"} {
		rows = append(rows, `{"label":{"fa":"`+text+`","en":"`+text+`","ar":"`+text+`","ru":"`+text+`","tr":"`+text+`"},"channel":"https://t.me/+`+strings.Repeat("A", 64)+`","post":"https://t.me/c/1234567890123/`+strings.TrimPrefix(ch, "channel")+`"}`)
	}
	biggest := "[" + strings.Join(rows, ",") + "]"
	if got, err := NormalizeSetting(channelcards.Setting, biggest); err != nil || got != biggest {
		t.Errorf("six full buttons (%d bytes): %v", len(biggest), err)
	}
	if n := len(biggest); n > channelCardsMaxBytes {
		t.Errorf("the longest list is %d bytes, over the limit %d", n, channelCardsMaxBytes)
	}

	svc, _ := testService(t)
	o := owner(t, svc)
	views, err := svc.AdminSettingSpecs(context.Background(), o.TelegramID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Key == channelcards.Setting {
			t.Error("the bot's admin menu lists the channel buttons")
		}
	}
}

// The extras a delivered service brings: two switches (on unless the owner says no), a channel post with the video, a tutorial's id.
func TestDeliverySettings(t *testing.T) {
	for _, key := range []string{SettingDeliveryBundle, SettingDeliveryBundleTrial} {
		sp, ok := SpecOf(key)
		if !ok || sp.Kind != KindBool || sp.Group != "delivery" || sp.Perm != permSettings || sp.Default != "true" {
			t.Errorf("%s: %+v", key, sp)
		}
		if got, err := NormalizeSetting(key, "off"); err != nil || got != "false" {
			t.Errorf("%s off: %q, %v", key, got, err)
		}
	}

	if sp, ok := SpecOf(SettingDeliveryVideo); !ok || sp.Kind != KindURL || sp.Group != "delivery" || sp.Default != "" {
		t.Fatalf("video spec: %+v", sp)
	}
	for in, want := range map[string]string{
		"https://t.me/heisen_tutorial/12":            "https://t.me/heisen_tutorial/12",
		" https://t.me/s/heisen_tutorial/12?single ": "https://t.me/heisen_tutorial/12",
		"https://telegram.me/heisen_tutorial/12":     "https://t.me/heisen_tutorial/12",
		"https://t.me/c/1234567890/45":               "https://t.me/c/1234567890/45",
		"":                                           "",
	} {
		if got, err := NormalizeSetting(SettingDeliveryVideo, in); err != nil || got != want {
			t.Errorf("video %q: %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, bad := range []string{"https://t.me/heisen_tutorial", "https://example.com/video.mp4", "http://t.me/heisen_tutorial/12", "https://youtu.be/abc", "not a link"} {
		var fe *FieldError
		if _, err := NormalizeSetting(SettingDeliveryVideo, bad); !errors.As(err, &fe) || fe.Field != SettingDeliveryVideo {
			t.Errorf("video %q accepted: %v", bad, err)
		}
	}

	if sp, ok := SpecOf(SettingDeliveryTutorial); !ok || sp.Kind != KindText || sp.Group != "delivery" || sp.Default != "" {
		t.Fatalf("tutorial spec: %+v", sp)
	}
	const id = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	for in, want := range map[string]string{id: id, " " + strings.ToUpper(id) + " ": id, "": ""} {
		if got, err := NormalizeSetting(SettingDeliveryTutorial, in); err != nil || got != want {
			t.Errorf("tutorial %q: %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, bad := range []string{"my tutorial", "0190a1b2", id + "0", "g190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} {
		var fe *FieldError
		if _, err := NormalizeSetting(SettingDeliveryTutorial, bad); !errors.As(err, &fe) || fe.Field != SettingDeliveryTutorial {
			t.Errorf("tutorial %q accepted: %v", bad, err)
		}
	}
}

// The delivery event tells the bot whether the service is a free test and whether its buyer is a reseller: they decide who gets the direct
// config, the tutorial video and the tutorial after the delivery message.
func TestProvisionedEventTellsTheBotWhoItIsFor(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	last := func(n int) events.SubscriptionProvisionedEvent {
		t.Helper()
		evs := outboxEvents(t, w.s, events.SubscriptionProvisioned)
		var e events.SubscriptionProvisionedEvent
		if len(evs) != n || json.Unmarshal(evs[n-1], &e) != nil {
			t.Fatalf("%d delivery events, want %d", len(evs), n)
		}
		return e
	}

	w.buy(w.reseller(), "for Ali")
	if e := last(1); !e.Reseller || e.Trial {
		t.Errorf("a reseller's purchase: %+v", e)
	}

	u := seedUser(t, w.s, 5901)
	fund(t, w.s, u.ID, w.plan.Price)
	w.buy(u, "")
	if e := last(2); e.Reseller || e.Trial {
		t.Errorf("a customer's purchase: %+v", e)
	}

	tp := seedPlan(t, w.s, true, 0)
	if _, err := w.svc.StartTrial(ctx, seedUser(t, w.s, 5902).ID, tp.ID, "bundle-trial"); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	if e := last(3); !e.Trial || e.Reseller {
		t.Errorf("a free test: %+v", e)
	}
}
