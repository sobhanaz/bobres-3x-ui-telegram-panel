package handler

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
)

// The store chooses the languages customers may pick (languages.enabled):
// Persian and English by default, so nothing changes for a store that sets
// nothing; the beta ones (Arabic, Russian, Turkish) when it enables them, in
// its order. A language switched off later moves its customers to the
// store's default.
func TestLanguagePickerFollowsTheEnabledLanguages(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	defer i18n.SetEnabled(nil)

	first := w.person(3101, "first")
	first.text("/start")
	if got := first.last().labels(); !slices.Equal(got, []string{"فارسی 🇮🇷", "English 🇬🇧"}) {
		t.Fatalf("default picker: %v", got)
	}

	w.wheelSettings(map[string]string{"languages.enabled": "ru, fa,en,ar,tr"})
	ru := w.person(3102, "ivan")
	ru.text("/start")
	if got := ru.last().labels(); !slices.Equal(got, []string{"Русский 🇷🇺", "فارسی 🇮🇷", "English 🇬🇧", "العربية", "Türkçe 🇹🇷"}) {
		t.Fatalf("picker of five: %v", got)
	}
	ru.press("lang:ru")
	ru.sees("Язык изменён на русский.")
	ru.sees("Добро пожаловать в <b>TestVPN</b>")
	if u := w.storeUser(3102); u.Language != "ru" {
		t.Fatalf("stored language %q", u.Language)
	}

	ar := w.person(3103, "")
	ar.text("/start")
	ar.press("lang:ar")
	ar.sees("تم تعيين اللغة إلى العربية.")
	ar.text("/menu")
	ar.sees("القائمة")

	ru.press("lang") // a picker with Russian on it, kept in the chat

	// Switched off: the Russian customer reads the store's default, now Persian.
	w.wheelSettings(map[string]string{"languages.enabled": "fa,en"})
	ru.text("/menu")
	ru.sees("منوی اصلی")
	ru.press("lang") // the picker
	if got := ru.last().labels(); slices.Contains(got, "Русский 🇷🇺") {
		t.Fatalf("a language switched off is still offered: %v", got)
	}
	ru.press("lang:ru") // the old picker's button cannot choose it again
	if u := w.storeUser(3102); u.Language != "fa" {
		t.Fatalf("a switched-off language chosen again: %q", u.Language)
	}
}

// An Arabic line keeps a left-to-right value (a plan's English name) in its
// place with the same isolates the Persian one uses.
func TestArabicKeepsIsolatesLikePersian(t *testing.T) {
	cat := i18n.MustLoad()
	for _, lang := range []string{"fa", "ar"} {
		got := cat.T(lang, "codes.from_plan", "plan", isolated("Monthly 50GB"), "date", i18n.Date(lang, time.Date(2026, 11, 2, 12, 0, 0, 0, i18n.CurrentLocation())))
		if !strings.Contains(got, "\u2066Monthly 50GB\u2069") {
			t.Errorf("%s: %q", lang, got)
		}
	}
	if !i18n.RTL("ar") {
		t.Error("Arabic reads right to left")
	}
}

// The bot's Telegram profile has the enabled beta languages too, and takes
// back one the store switched off.
func TestProfileFollowsTheEnabledLanguages(t *testing.T) {
	ctx := context.Background()
	cat := i18n.MustLoad()
	defer i18n.SetEnabled(nil)
	set := settings.New()
	set.Replace(map[string]string{"languages.enabled": "fa,en,tr"})
	i18n.SetEnabled(i18n.ParseEnabled("fa,en,tr"))
	api, memo := newFakeProfileAPI(), &memMemo{}
	s := &profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler)}
	p, on := wantedProfile(cat, set, "", true, "")
	if len(p.Langs) != 4 || p.Langs["tr"].Description != cat.Default("tr", "profile.description") {
		t.Fatalf("languages: %v", p.Langs)
	}
	if err := s.sync(ctx, p, on); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(api.take(), " "); !strings.Contains(got, "description tr") {
		t.Fatalf("Turkish not set: %s", got)
	}
	set.Replace(map[string]string{"languages.enabled": "fa,en"})
	i18n.SetEnabled(i18n.ParseEnabled("fa,en"))
	p, on = wantedProfile(cat, set, "", true, "")
	if err := s.sync(ctx, p, on); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(api.take(), " "); got != "delete 0/tr description tr about tr" {
		t.Fatalf("Turkish not taken back: %s", got)
	}
	if api.text["description/tr"] != "" {
		t.Fatalf("Turkish description left: %q", api.text["description/tr"])
	}
}
