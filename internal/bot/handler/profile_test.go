package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// fakeProfileAPI records the profile calls; fail makes the calls whose
// description contains a key fail with that error.
type fakeProfileAPI struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
	cmds  map[string][]tg.BotCommand // "chat/lang" -> menu
	text  map[string]string          // "description/lang", "about/lang"
	done  chan struct{}
}

func newFakeProfileAPI() *fakeProfileAPI {
	return &fakeProfileAPI{fail: map[string]error{}, cmds: map[string][]tg.BotCommand{}, text: map[string]string{}, done: make(chan struct{}, 64)}
}

func (f *fakeProfileAPI) record(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, err := range f.fail {
		if strings.Contains(call, k) {
			return err
		}
	}
	f.calls = append(f.calls, call)
	select {
	case f.done <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeProfileAPI) SetMyCommands(_ context.Context, cmds []tg.BotCommand, chatID int64, lang string) error {
	if err := f.record(fmt.Sprintf("commands %d/%s", chatID, lang)); err != nil {
		return err
	}
	f.mu.Lock()
	f.cmds[fmt.Sprintf("%d/%s", chatID, lang)] = cmds
	f.mu.Unlock()
	return nil
}

func (f *fakeProfileAPI) DeleteMyCommands(_ context.Context, chatID int64, lang string) error {
	if err := f.record(fmt.Sprintf("delete %d/%s", chatID, lang)); err != nil {
		return err
	}
	f.mu.Lock()
	delete(f.cmds, fmt.Sprintf("%d/%s", chatID, lang))
	f.mu.Unlock()
	return nil
}

func (f *fakeProfileAPI) SetMyDescription(_ context.Context, text, lang string) error {
	if err := f.record("description " + lang); err != nil {
		return err
	}
	f.mu.Lock()
	f.text["description/"+lang] = text
	f.mu.Unlock()
	return nil
}

func (f *fakeProfileAPI) SetMyShortDescription(_ context.Context, text, lang string) error {
	if err := f.record("about " + lang); err != nil {
		return err
	}
	f.mu.Lock()
	f.text["about/"+lang] = text
	f.mu.Unlock()
	return nil
}

// take returns the calls made since the last take.
func (f *fakeProfileAPI) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

type memMemo struct{ v string }

func (m *memMemo) Get(context.Context) (string, error) { return m.v, nil }
func (m *memMemo) Set(_ context.Context, v string) error {
	m.v = v
	return nil
}

func names(cmds []tg.BotCommand) string {
	var out []string
	for _, c := range cmds {
		out = append(out, c.Command)
	}
	return strings.Join(out, " ")
}

func TestWantedProfile(t *testing.T) {
	cat := i18n.MustLoad()
	set := settings.New()
	set.SetStaff([]settings.Contact{
		{TelegramID: 1, Language: "en", Role: "owner"},
		{TelegramID: 2, Language: "", Role: "support"},
		{TelegramID: 3, Language: "fa", Role: "admin"},
	})
	p, on := wantedProfile(cat, set)
	if !on {
		t.Fatal("the profile is on by default")
	}
	if len(p.Langs) != 3 {
		t.Fatalf("languages: %v", p.Langs)
	}
	def, fa, en := p.Langs[""], p.Langs["fa"], p.Langs["en"]
	if names(fa.Commands) != "start menu paysupport" {
		t.Fatalf("customer menu without terms links: %q", names(fa.Commands))
	}
	if def.Description != fa.Description || def.About != fa.About || def.Commands[0].Description != fa.Commands[0].Description {
		t.Fatal("other languages must get the first language's profile")
	}
	if en.Description != cat.Default("en", "profile.description") || en.About != cat.Default("en", "profile.about") ||
		en.Commands[0].Description != cat.Default("en", "profile.cmd.start") {
		t.Fatalf("English profile: %+v", en)
	}
	if fa.About == en.About {
		t.Fatal("the Persian profile is in English")
	}
	if got := names(p.Staff[1]); got != "start menu paysupport admin dashboard" {
		t.Fatalf("owner menu: %q", got)
	}
	if p.Staff[1][3].Description != cat.Default("en", "profile.cmd.admin") {
		t.Fatalf("the owner's menu is not in their language: %+v", p.Staff[1])
	}
	if got := names(p.Staff[2]); got != "start menu paysupport dashboard" {
		t.Fatalf("support menu: %q", got)
	}
	if p.Staff[2][0].Description != cat.Default("fa", "profile.cmd.start") {
		t.Fatal("staff without a language get the first language")
	}
	if got := names(p.Staff[3]); got != "start menu paysupport admin dashboard" {
		t.Fatalf("admin menu: %q", got)
	}

	set.Replace(map[string]string{"branding.privacy_url": "https://example.com/privacy", "texts.en.profile.about": "Best VPN in town"})
	if dropped := cat.SetOverrides(map[string]string{"texts.en.profile.about": "Best VPN in town"}); len(dropped) > 0 {
		t.Fatalf("override dropped: %v", dropped)
	}
	p, _ = wantedProfile(cat, set)
	if names(p.Langs["en"].Commands) != "start menu terms paysupport" || p.Langs["en"].About != "Best VPN in town" {
		t.Fatalf("with a privacy link and an override: %+v", p.Langs["en"])
	}
	if p.Langs["fa"].About == "Best VPN in town" {
		t.Fatal("an English override changed the Persian profile")
	}

	set.Replace(map[string]string{"general.bot_profile": "false"})
	if _, on := wantedProfile(cat, set); on {
		t.Fatal("general.bot_profile=false must leave the profile alone")
	}
}

func TestProfileSyncSetsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	cat := i18n.MustLoad()
	set := settings.New()
	set.SetStaff([]settings.Contact{{TelegramID: 7, Language: "en", Role: "owner"}, {TelegramID: 8, Role: "admin"}})
	api, memo := newFakeProfileAPI(), &memMemo{}
	s := &profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler)}
	apply := func() {
		t.Helper()
		p, on := wantedProfile(cat, set)
		if err := s.sync(ctx, p, on); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	apply()
	want := "commands 0/ description  about  commands 0/en description en about en commands 0/fa description fa about fa commands 7/ commands 8/"
	if got := strings.Join(api.take(), " "); got != want {
		t.Fatalf("first sync:\n%s\nwant:\n%s", got, want)
	}
	if api.text["description/en"] != cat.Default("en", "profile.description") || names(api.cmds["7/"]) != "start menu paysupport admin dashboard" {
		t.Fatalf("set: %v %v", api.text, api.cmds)
	}
	apply()
	if got := api.take(); len(got) != 0 {
		t.Fatalf("nothing changed, yet: %v", got)
	}

	// A restart: what was set is remembered.
	s = &profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler)}
	apply()
	if got := api.take(); len(got) != 0 {
		t.Fatalf("after a restart: %v", got)
	}

	// An English text changes: only the English part is set again.
	cat.SetOverrides(map[string]string{"texts.en.profile.cmd.menu": "Home"})
	t.Cleanup(func() { cat.SetOverrides(nil) })
	apply()
	if got := strings.Join(api.take(), " "); got != "commands 0/en description en about en commands 7/" {
		t.Fatalf("after an English override: %s", got)
	}

	// A staff member leaves: their chat gets the customer menu back.
	set.SetStaff([]settings.Contact{{TelegramID: 7, Language: "en", Role: "owner"}})
	apply()
	if got := strings.Join(api.take(), " "); got != "delete 8/" {
		t.Fatalf("after staff left: %s", got)
	}

	// Switched off: the bot takes back the Persian and English values and the
	// staff menus, which would hide what is set in @BotFather (that sets
	// only the values for every language, which stay); back on, everything is
	// set again.
	set.Replace(map[string]string{"general.bot_profile": "false"})
	apply()
	if got := strings.Join(api.take(), " "); got != "delete 7/ delete 0/en description en about en delete 0/fa description fa about fa" {
		t.Fatalf("switched off: %s", got)
	}
	if api.text["description/en"] != "" || api.text["description/"] == "" || api.cmds["0/"] == nil || api.cmds["7/"] != nil {
		t.Fatalf("after switching off: %v %v", api.text, api.cmds)
	}
	apply()
	if got := api.take(); len(got) != 0 {
		t.Fatalf("switched off, again: %v", got)
	}
	set.Replace(nil)
	apply()
	if got := api.take(); len(got) != 10 {
		t.Fatalf("switched on again: %v", got)
	}
}

type failingMemo struct {
	memMemo
	err error
}

func (m *failingMemo) Get(ctx context.Context) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.memMemo.Get(ctx)
}

// A memo that cannot be read is never overwritten with less than it holds:
// the staff menus it lists would have nothing left to remove them.
func TestProfileSyncMemoUnreadable(t *testing.T) {
	ctx := context.Background()
	cat := i18n.MustLoad()
	set := settings.New()
	set.SetStaff([]settings.Contact{{TelegramID: 7, Role: "owner"}})
	api, memo := newFakeProfileAPI(), &failingMemo{}
	p, _ := wantedProfile(cat, set)
	if err := (&profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler)}).sync(ctx, p, true); err != nil {
		t.Fatal(err)
	}
	api.take()
	before := memo.v

	set.SetStaff(nil) // the owner left while the bot was down
	p, _ = wantedProfile(cat, set)
	s := &profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler)}
	memo.err = errors.New("redis: connection refused")
	if err := s.sync(ctx, p, true); err == nil {
		t.Fatal("an unreadable memo must stop the sync")
	}
	if memo.v != before || len(api.take()) != 0 {
		t.Fatalf("the memo was overwritten or Telegram called: %q", memo.v)
	}
	memo.err = nil
	if err := s.sync(ctx, p, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(api.take(), " "); got != "delete 7/" {
		t.Fatalf("once readable: %s", got)
	}
}

func TestProfileSyncFailures(t *testing.T) {
	ctx := context.Background()
	cat := i18n.MustLoad()
	set := settings.New()
	set.SetStaff([]settings.Contact{{TelegramID: 7, Role: "owner"}, {TelegramID: 9, Role: "admin"}})
	api := newFakeProfileAPI()
	s := &profileSync{api: api, log: slog.New(slog.DiscardHandler)}
	p, _ := wantedProfile(cat, set)

	// Telegram is down for the English part: the parts before it are kept,
	// and the rest is tried at the next refresh.
	api.fail["description en"] = &tg.APIError{Code: 502, Description: "Bad Gateway"}
	if err := s.sync(ctx, p, true); err == nil {
		t.Fatal("a failed call must be reported")
	}
	if got := strings.Join(api.take(), " "); got != "commands 0/ description  about  commands 0/en" {
		t.Fatalf("before the failure: %s", got)
	}
	// An admin who never started the bot: logged, not retried every minute.
	delete(api.fail, "description en")
	api.fail["commands 9/"] = &tg.APIError{Code: 400, Description: "Bad Request: chat not found"}
	if err := s.sync(ctx, p, true); err != nil {
		t.Fatalf("a chat the bot cannot reach is no failure: %v", err)
	}
	if got := strings.Join(api.take(), " "); got != "commands 0/en description en about en commands 0/fa description fa about fa commands 7/" {
		t.Fatalf("the rest: %s", got)
	}
	if err := s.sync(ctx, p, true); err != nil || len(api.take()) != 0 {
		t.Fatalf("retried the unreachable chat: %v", err)
	}
	// An hour later (perhaps they have started the bot now) it is tried again.
	delete(api.fail, "commands 9/")
	s.now = func() time.Time { return time.Now().Add(goneRetry + time.Minute) }
	if err := s.sync(ctx, p, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(api.take(), " "); got != "commands 9/" {
		t.Fatalf("an hour later: %s", got)
	}
	if !chatGone(&tg.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}) || chatGone(errors.New("timeout")) {
		t.Fatal("chatGone")
	}
}

// After a 429 RunProfile waits as long as Telegram asks, refresh or not.
func TestRunProfileWaitsAfterFlood(t *testing.T) {
	h := New(nil, nil, nil, i18n.MustLoad(), nil, Config{}, nil)
	api := newFakeProfileAPI()
	api.fail["commands 0/"] = &tg.APIError{Code: 429, Description: "Too Many Requests: retry after 600", RetryAfter: 600}
	var calls sync.WaitGroup
	tried := make(chan struct{}, 8)
	flaky := &countingAPI{ProfileAPI: api, tried: tried}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls.Add(1)
	go func() { defer calls.Done(); _ = h.RunProfile(ctx, flaky, nil) }()
	h.profileKick <- struct{}{}
	select {
	case <-tried:
	case <-time.After(5 * time.Second):
		t.Fatal("the first refresh did not try")
	}
	h.profileKick <- struct{}{}
	h.profileKick <- struct{}{} // the second kick is taken only once the first is done
	select {
	case <-tried:
		t.Fatal("tried again during the flood wait")
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	calls.Wait()
}

// countingAPI signals every call it passes on.
type countingAPI struct {
	ProfileAPI
	tried chan struct{}
}

func (c *countingAPI) SetMyCommands(ctx context.Context, cmds []tg.BotCommand, chatID int64, lang string) error {
	c.tried <- struct{}{}
	return c.ProfileAPI.SetMyCommands(ctx, cmds, chatID, lang)
}

// RunProfile sets the profile after each settings refresh (the kick).
func TestRunProfileFollowsRefreshes(t *testing.T) {
	h := New(nil, nil, nil, i18n.MustLoad(), nil, Config{}, nil)
	api := newFakeProfileAPI()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.RunProfile(ctx, api, nil) }()
	h.profileKick <- struct{}{}
	deadline := time.After(5 * time.Second)
	for n := 0; n < 9; n++ {
		select {
		case <-api.done:
		case <-deadline:
			t.Fatalf("the profile was not set: %v", api.take())
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
