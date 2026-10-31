package domain

import (
	"context"
	"errors"
	"testing"
)

// languages.enabled is a list of the catalog's languages, the store's
// default first, stored as "fa,en,ru"; the bot's admin menu leaves it to the
// dashboard. Customers may be saved in any language of the catalog.
func TestLanguagesSetting(t *testing.T) {
	sp, ok := SpecOf(SettingLanguages)
	if !ok || sp.Kind != KindLangs || sp.Default != "fa,en" {
		t.Fatalf("spec: %+v", sp)
	}
	for in, want := range map[string]string{
		"fa,en": "fa,en", " RU , fa ": "ru,fa", "en ar tr": "en,ar,tr", "tr;ru": "tr,ru", "fa،en": "fa,en", "": "",
	} {
		if got, err := NormalizeSetting(SettingLanguages, in); err != nil || got != want {
			t.Errorf("%q: %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, bad := range []string{"de", "fa,fa", "fa,xx", ",", "persian"} {
		var fe *FieldError
		if _, err := NormalizeSetting(SettingLanguages, bad); !errors.As(err, &fe) || fe.Field != SettingLanguages {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}

	svc, _ := testService(t)
	o := owner(t, svc)
	for _, lang := range []string{"ar", "ru", "tr"} {
		if _, err := svc.UpsertUser(context.Background(), UpsertUserParams{TelegramID: 9100, Language: lang}); err != nil {
			t.Errorf("%s: %v", lang, err)
		}
	}
	if _, err := svc.UpsertUser(context.Background(), UpsertUserParams{TelegramID: 9100, Language: "de"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unknown language: %v", err)
	}
	views, err := svc.AdminSettingSpecs(context.Background(), o.TelegramID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Key == SettingLanguages {
			t.Error("the bot's admin menu lists the languages")
		}
	}
}
