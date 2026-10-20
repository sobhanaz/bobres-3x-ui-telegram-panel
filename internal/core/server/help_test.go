package server

import (
	"context"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// helpFixture is the gRPC server with no help entries, the service that writes
// them (the dashboard's side) and the bot's client.
func helpFixture(t *testing.T) (*fixture, *domain.Service, corev1.CoreServiceClient) {
	t.Helper()
	f := newFixture(t)
	if err := testdb.Truncate(context.Background(), f.st.DB(), `core.help_entries`); err != nil {
		t.Fatal(err)
	}
	return f, domain.New(f.st, domain.Config{}), f.client(t, botToken)
}

func TestHelpEntriesOverGRPC(t *testing.T) {
	f, dom, c := helpFixture(t)
	ctx := context.Background()
	fa, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 6101, Language: "fa"})
	if err != nil {
		t.Fatal(err)
	}
	en, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 6102, Language: "en"})
	owner, err := f.st.UpsertUser(ctx, f.st.Conn(), 6199, "", "en", "")
	if err != nil {
		t.Fatal(err)
	}

	// Nothing yet: empty lists, and the settings say there is nothing to show.
	if got, err := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: fa.GetId(), Kind: "faq"}); err != nil || len(got.GetEntries()) != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
	if s, err := c.GetSettings(ctx, &corev1.GetSettingsRequest{}); err != nil || s.GetHelpFaq() || s.GetHelpTutorials() {
		t.Fatalf("settings of an empty store: %+v %v", s, err)
	}

	mk := func(in domain.HelpInput) *store.HelpEntry {
		t.Helper()
		e, err := dom.CreateHelp(ctx, owner, in)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	both := mk(domain.HelpInput{Kind: "faq", Enabled: true, Title: map[string]string{"fa": "سوال", "en": "Question"}, Body: map[string]string{"fa": "پاسخ <b>۱</b>", "en": "Answer <b>1</b>"}})
	onlyEN := mk(domain.HelpInput{Kind: "faq", Enabled: true, Title: map[string]string{"en": "English only"}, Body: map[string]string{"en": "Text"}})
	off := mk(domain.HelpInput{Kind: "faq", Enabled: false, Title: map[string]string{"en": "Hidden"}, Body: map[string]string{"en": "Text"}})
	tut := mk(domain.HelpInput{Kind: "tutorial", Enabled: true, Title: map[string]string{"fa": "آموزش"}, Body: map[string]string{"fa": "متن"},
		Buttons: []store.HelpButton{{URL: "https://example.com/get", Label: map[string]string{"en": "Download", "fa": "دانلود"}}, {URL: "https://example.com/two", Label: map[string]string{"en": "Second"}}}})

	got, err := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: fa.GetId(), Kind: "faq", Language: "fa"})
	if err != nil || len(got.GetEntries()) != 2 {
		t.Fatalf("persian: %+v %v", got, err)
	}
	e0, e1 := got.GetEntries()[0], got.GetEntries()[1]
	if e0.GetId() != both.ID || e0.GetKind() != "faq" || e0.GetTitle() != "سوال" || e0.GetBody() != "پاسخ <b>۱</b>" || e0.GetLanguage() != "fa" ||
		e1.GetId() != onlyEN.ID || e1.GetTitle() != "English only" || e1.GetLanguage() != "en" {
		t.Fatalf("persian entries: %+v", got.GetEntries())
	}
	// The customer's own language when none is asked for; the asked one otherwise.
	if got, _ := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: en.GetId(), Kind: "faq"}); got.GetEntries()[0].GetTitle() != "Question" {
		t.Fatalf("english customer: %+v", got.GetEntries())
	}
	if got, _ := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: en.GetId(), Kind: "faq", Language: "fa"}); got.GetEntries()[0].GetTitle() != "سوال" {
		t.Fatalf("asked for Persian: %+v", got.GetEntries())
	}
	tuts, err := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: en.GetId(), Kind: "tutorial", Language: "en"})
	if err != nil || len(tuts.GetEntries()) != 1 {
		t.Fatalf("tutorials: %+v %v", tuts, err)
	}
	te := tuts.GetEntries()[0]
	if te.GetTitle() != "آموزش" || te.GetLanguage() != "fa" || len(te.GetButtons()) != 2 || te.GetButtons()[0].GetLabel() != "Download" ||
		te.GetButtons()[0].GetUrl() != "https://example.com/get" || te.GetButtons()[1].GetLabel() != "Second" {
		t.Fatalf("a tutorial read in English with only Persian text: %+v", te)
	}

	// One entry.
	one, err := c.GetHelpEntry(ctx, &corev1.GetHelpEntryRequest{UserId: fa.GetId(), Id: tut.ID, Language: "fa"})
	if err != nil || one.GetTitle() != "آموزش" || one.GetButtons()[0].GetLabel() != "دانلود" || one.GetId() != tut.ID {
		t.Fatalf("one: %+v %v", one, err)
	}
	for what, id := range map[string]string{"a switched-off entry": off.ID, "an unknown entry": "00000000-0000-7000-8000-00000000dead"} {
		if _, err := c.GetHelpEntry(ctx, &corev1.GetHelpEntryRequest{UserId: fa.GetId(), Id: id}); status.Code(err) != codes.NotFound {
			t.Errorf("%s: %v", what, err)
		}
	}

	// What the settings say follows what is enabled.
	if s, err := c.GetSettings(ctx, &corev1.GetSettingsRequest{}); err != nil || !s.GetHelpFaq() || !s.GetHelpTutorials() {
		t.Fatalf("settings: %+v %v", s, err)
	}
	if _, err := dom.SetHelpEnabled(ctx, owner, tut.ID, false, 1); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.GetSettings(ctx, &corev1.GetSettingsRequest{}); !s.GetHelpFaq() || s.GetHelpTutorials() {
		t.Fatalf("settings with the tutorial off: %+v", s)
	}
}

func TestHelpEntriesRefuseWhoAndWhatTheyShould(t *testing.T) {
	f, dom, c := helpFixture(t)
	ctx := context.Background()
	u, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 6201, Language: "en"})
	owner, _ := f.st.UpsertUser(ctx, f.st.Conn(), 6299, "", "en", "")
	e, err := dom.CreateHelp(ctx, owner, domain.HelpInput{Kind: "faq", Enabled: true, Title: map[string]string{"en": "q"}, Body: map[string]string{"en": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	for what, c := range map[string]struct {
		call func() error
		want codes.Code
	}{
		"a kind that does not exist": {func() error {
			_, err := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: u.GetId(), Kind: "news"})
			return err
		}, codes.InvalidArgument},
		"no kind": {func() error {
			_, err := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: u.GetId()})
			return err
		}, codes.InvalidArgument},
		"no customer": {func() error {
			_, err := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{Kind: "faq"})
			return err
		}, codes.InvalidArgument},
		"a customer that is not an id": {func() error {
			_, err := c.GetHelpEntry(ctx, &corev1.GetHelpEntryRequest{UserId: "x", Id: e.ID})
			return err
		}, codes.InvalidArgument},
		"an unknown customer": {func() error {
			_, err := c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: "00000000-0000-7000-8000-00000000dead", Kind: "faq"})
			return err
		}, codes.NotFound},
	} {
		if got := status.Code(c.call()); got != c.want {
			t.Errorf("%s: %v, want %v", what, got, c.want)
		}
	}
	// A banned customer is refused as everywhere else.
	if err := f.st.SetUserStatus(ctx, f.st.Conn(), u.GetId(), "banned"); err != nil {
		t.Fatal(err)
	}
	_, err = c.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: u.GetId(), Kind: "faq"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("banned: %v", err)
	}
	if _, err = c.GetHelpEntry(ctx, &corev1.GetHelpEntryRequest{UserId: u.GetId(), Id: e.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("banned, one entry: %v", err)
	}
	// Only the bot calls.
	other := f.client(t, "a-different-services-token")
	if _, err := other.ListHelpEntries(ctx, &corev1.ListHelpEntriesRequest{UserId: u.GetId(), Kind: "faq"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("another peer: %v", err)
	}
}
