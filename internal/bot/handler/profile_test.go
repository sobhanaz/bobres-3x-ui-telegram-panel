package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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
	photo string                     // the profile photo's bytes ("" none)
	menu  string                     // the menu button now: "" never set, "default", or "<text> <url>"
	// photoID is the current photo's identity ("" none): each photo set,
	// by the bot or in @BotFather, is a new one (photos counts them).
	photoID string
	photos  int
	// me is the bot's own id: only its photos are known. reads counts the
	// reads of its photos (not recorded as calls), which fail with
	// fail[readPhotos].
	me    int64
	reads int
	done  chan struct{}
}

// readPhotos is the fail key of getUserProfilePhotos.
const readPhotos = "read photos"

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

// SetChatMenuButton records the Mini App button ("menu <url>") or the default one ("menu default").
func (f *fakeProfileAPI) SetChatMenuButton(_ context.Context, b *tg.MenuButtonWebApp) error {
	what := "default"
	if b != nil {
		what = b.Text + " " + b.URL
	}
	if err := f.record("menu " + what); err != nil {
		return err
	}
	f.mu.Lock()
	f.menu = what
	f.mu.Unlock()
	return nil
}

func (f *fakeProfileAPI) SetMyProfilePhoto(_ context.Context, jpeg []byte) error {
	if err := f.record("photo " + string(jpeg)); err != nil {
		return err
	}
	f.newPhoto(string(jpeg))
	return nil
}

// RemoveMyProfilePhoto takes off the current photo, whoever set it, like
// Telegram.
func (f *fakeProfileAPI) RemoveMyProfilePhoto(context.Context) error {
	if err := f.record("remove photo"); err != nil {
		return err
	}
	f.mu.Lock()
	f.photo, f.photoID = "", ""
	f.mu.Unlock()
	return nil
}

// GetUserProfilePhotos gives the bot's current photo in three sizes, the
// largest in the middle; another user has none.
func (f *fakeProfileAPI) GetUserProfilePhotos(_ context.Context, userID int64, limit int) (*tg.UserProfilePhotos, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[readPhotos]; err != nil {
		return nil, err
	}
	f.reads++
	if f.photoID == "" || userID != f.me || limit < 1 {
		return &tg.UserProfilePhotos{Photos: [][]tg.PhotoSize{}}, nil
	}
	return &tg.UserProfilePhotos{TotalCount: f.photos, Photos: [][]tg.PhotoSize{{
		{FileUniqueID: f.photoID + "-160", Width: 160, Height: 160},
		{FileUniqueID: f.photoID, Width: 640, Height: 640},
		{FileUniqueID: f.photoID + "-320", Width: 320, Height: 320},
	}}}, nil
}

// newPhoto makes jpeg the current photo, a new one, and returns its
// identity (the largest size's).
func (f *fakeProfileAPI) newPhoto(jpeg string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.photos++
	f.photo, f.photoID = jpeg, fmt.Sprintf("uid-%d", f.photos)
	return f.photoID
}

// botFather sets a photo the way the owner does in @BotFather.
func (f *fakeProfileAPI) botFather() string { return f.newPhoto("botfather") }

func (f *fakeProfileAPI) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
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
	p, on := wantedProfile(cat, set, "1760000000000000000", true, "")
	if !on {
		t.Fatal("the profile is on by default")
	}
	if len(p.Langs) != 3 || p.Photo != "1760000000000000000" {
		t.Fatalf("languages %v, photo %q", p.Langs, p.Photo)
	}
	def, fa, en := p.Langs[""], p.Langs["fa"], p.Langs["en"]
	if names(fa.Commands) != "start menu new renew status paysupport" {
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
	if got := names(p.Staff[1]); got != "start menu new renew status paysupport admin dashboard" {
		t.Fatalf("owner menu: %q", got)
	}
	if p.Staff[1][6].Description != cat.Default("en", "profile.cmd.admin") {
		t.Fatalf("the owner's menu is not in their language: %+v", p.Staff[1])
	}
	if got := names(p.Staff[2]); got != "start menu new renew status paysupport dashboard" {
		t.Fatalf("support menu: %q", got)
	}
	if p.Staff[2][0].Description != cat.Default("fa", "profile.cmd.start") {
		t.Fatal("staff without a language get the first language")
	}
	if got := names(p.Staff[3]); got != "start menu new renew status paysupport admin dashboard" {
		t.Fatalf("admin menu: %q", got)
	}

	set.Replace(map[string]string{"branding.privacy_url": "https://example.com/privacy", "texts.en.profile.about": "Best VPN in town"})
	if dropped := cat.SetOverrides(map[string]string{"texts.en.profile.about": "Best VPN in town"}); len(dropped) > 0 {
		t.Fatalf("override dropped: %v", dropped)
	}
	p, _ = wantedProfile(cat, set, "", true, "")
	if names(p.Langs["en"].Commands) != "start menu new renew status terms paysupport" || p.Langs["en"].About != "Best VPN in town" {
		t.Fatalf("with a privacy link and an override: %+v", p.Langs["en"])
	}
	if p.Langs["fa"].About == "Best VPN in town" {
		t.Fatal("an English override changed the Persian profile")
	}

	set.Replace(map[string]string{"general.bot_profile": "false"})
	if _, on := wantedProfile(cat, set, "", true, ""); on {
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
		p, on := wantedProfile(cat, set, "", true, "")
		if err := s.sync(ctx, p, on); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	apply()
	want := "commands 0/ description  about  commands 0/en description en about en commands 0/fa description fa about fa commands 7/ commands 8/"
	if got := strings.Join(api.take(), " "); got != want {
		t.Fatalf("first sync:\n%s\nwant:\n%s", got, want)
	}
	if api.text["description/en"] != cat.Default("en", "profile.description") || names(api.cmds["7/"]) != "start menu new renew status paysupport admin dashboard" {
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
	p, _ := wantedProfile(cat, set, "", true, "")
	if err := (&profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler)}).sync(ctx, p, true); err != nil {
		t.Fatal(err)
	}
	api.take()
	before := memo.v

	set.SetStaff(nil) // the owner left while the bot was down
	p, _ = wantedProfile(cat, set, "", true, "")
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
	p, _ := wantedProfile(cat, set, "", true, "")

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
	go func() { defer calls.Done(); _ = h.RunProfile(ctx, flaky, 0, nil) }()
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
	go func() { done <- h.RunProfile(ctx, api, 0, nil) }()
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

// fakePhotos is core's bot photo (version "" is none) and counts fetches.
type fakePhotos struct {
	mu      sync.Mutex
	version string
	jpeg    string
	err     error
	fetched int
}

func (f *fakePhotos) get(context.Context) (string, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched++
	if f.err != nil {
		return "", nil, f.err
	}
	return f.version, []byte(f.jpeg), nil
}

func (f *fakePhotos) set(version, jpeg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version, f.jpeg = version, jpeg
}

func (f *fakePhotos) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetched
}

// profileBotID is the bot's own id in the photo tests.
const profileBotID = 1000

// photoSync is a profile sync with core's photo, and a function that syncs
// the profile the settings and a photo version call for.
func photoSync(t *testing.T, set *settings.Store) (*profileSync, *fakeProfileAPI, *memMemo, *fakePhotos, func(string) error) {
	t.Helper()
	cat := i18n.MustLoad()
	api, memo, core := newFakeProfileAPI(), &memMemo{}, &fakePhotos{}
	api.me = profileBotID
	s := &profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler), botID: profileBotID, photo: core.get}
	return s, api, memo, core, func(version string) error {
		p, on := wantedProfile(cat, set, version, true, "")
		return s.sync(context.Background(), p, on)
	}
}

func mustSync(t *testing.T, sync func(string) error, version string) {
	t.Helper()
	if err := sync(version); err != nil {
		t.Fatalf("sync %q: %v", version, err)
	}
}

// The photo comes last and is fetched only when its version changes; the
// bot sets what core has now and takes back only a photo it set.
func TestProfileSyncPhoto(t *testing.T) {
	set := settings.New()
	set.SetStaff([]settings.Contact{{TelegramID: 7, Role: "owner"}})
	s, api, memo, core, sync := photoSync(t, set)

	// No photo on the Branding page: whatever is set in @BotFather stays.
	mustSync(t, sync, "")
	if got := api.take(); len(got) != 10 || slices.Contains(got, "remove photo") || core.count() != 0 {
		t.Fatalf("first sync without a photo (%d fetches): %v", core.count(), got)
	}

	core.set("v1", "jpeg-1")
	mustSync(t, sync, "v1")
	if got := strings.Join(api.take(), " "); got != "photo jpeg-1" || core.count() != 1 {
		t.Fatalf("a photo (%d fetches): %s", core.count(), got)
	}
	mustSync(t, sync, "v1")
	if got := api.take(); len(got) != 0 || core.count() != 1 {
		t.Fatalf("the same version (%d fetches): %v", core.count(), got)
	}
	// A restart: the version the bot set is remembered, with the identity
	// Telegram gave the photo (its largest size's).
	s2 := &profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler), botID: profileBotID, photo: core.get}
	p, _ := wantedProfile(i18n.MustLoad(), set, "v1", true, "")
	if err := s2.sync(context.Background(), p, true); err != nil {
		t.Fatal(err)
	}
	if got := api.take(); len(got) != 0 || core.count() != 1 || !strings.Contains(memo.v, `"photo":"v1"`) ||
		!strings.Contains(memo.v, `"photo_id":"uid-1"`) {
		t.Fatalf("after a restart (%d fetches, memo %s): %v", core.count(), memo.v, got)
	}

	// Core could not read the photo: nothing is fetched, set or removed.
	mustSync(t, sync, photoUnknown)
	if got := api.take(); len(got) != 0 || core.count() != 1 {
		t.Fatalf("unknown version (%d fetches): %v", core.count(), got)
	}
	// Replaced again since the settings were read: what core has now is set
	// and remembered, so the next refresh sets nothing.
	core.set("v3", "jpeg-3")
	mustSync(t, sync, "v2")
	mustSync(t, sync, "v3")
	if got := strings.Join(api.take(), " "); got != "photo jpeg-3" || core.count() != 2 || api.photo != "jpeg-3" {
		t.Fatalf("replaced since the read (%d fetches): %s", core.count(), got)
	}

	// Removed on the Branding page: the bot takes back the photo it set.
	core.set("", "")
	mustSync(t, sync, "")
	if got := strings.Join(api.take(), " "); got != "remove photo" || strings.Contains(memo.v, `"photo`) {
		t.Fatalf("removed (memo %s): %s", memo.v, got)
	}
	mustSync(t, sync, "")
	if got := api.take(); len(got) != 0 {
		t.Fatalf("removed, again: %v", got)
	}
	// Removed since the settings were read (core answers NotFound): no photo,
	// and none of the bot's own to take back.
	mustSync(t, sync, "v4")
	if got := api.take(); len(got) != 0 || core.count() != 3 {
		t.Fatalf("gone before the fetch (%d fetches): %v", core.count(), got)
	}
	core.set("v5", "jpeg-5")
	mustSync(t, sync, "v5")
	core.set("", "")
	mustSync(t, sync, "v6")
	if got := strings.Join(api.take(), " "); got != "photo jpeg-5 remove photo" || api.photo != "" {
		t.Fatalf("set, then gone before the fetch: %s", got)
	}

	// Without a way to fetch it (no core) the photo is left alone.
	s.photo = nil
	mustSync(t, sync, "v7")
	if got := api.take(); len(got) != 0 {
		t.Fatalf("no fetcher: %v", got)
	}
}

// A photo Telegram refuses for good is not tried again at every refresh:
// after an hour, or once the photo changes. Other failures stop the sync.
func TestProfileSyncPhotoRefused(t *testing.T) {
	s, api, _, core, sync := photoSync(t, settings.New())
	mustSync(t, sync, "")
	api.take()

	api.fail["photo "] = &tg.APIError{Code: 400, Description: "Bad Request: PHOTO_CROP_SIZE_SMALL"}
	core.set("v1", "jpeg-1")
	mustSync(t, sync, "v1") // logged, no failure: the rest of the profile is set
	mustSync(t, sync, "v1")
	if core.count() != 1 || len(api.take()) != 0 {
		t.Fatalf("a refused photo was fetched again within the hour: %d fetches", core.count())
	}
	// An hour later it is tried again.
	s.now = func() time.Time { return time.Now().Add(goneRetry + time.Minute) }
	mustSync(t, sync, "v1")
	if core.count() != 2 {
		t.Fatalf("an hour later: %d fetches", core.count())
	}
	// Another photo is tried at once.
	delete(api.fail, "photo ")
	core.set("v2", "jpeg-2")
	mustSync(t, sync, "v2")
	if got := strings.Join(api.take(), " "); got != "photo jpeg-2" {
		t.Fatalf("a new photo after a refusal: %s", got)
	}

	// A local Bot API server older than 9.4 does not know the method.
	for _, method := range []string{"photo ", "remove photo"} {
		api.fail[method] = &tg.APIError{Code: 404, Description: "Not Found: method not found"}
	}
	core.set("v3", "jpeg-3")
	mustSync(t, sync, "v3")
	mustSync(t, sync, "v3")
	if core.count() != 4 || len(api.take()) != 0 {
		t.Fatalf("method not found, tried again within the hour: %d fetches", core.count())
	}
	// A removal it refuses is kept the same way.
	mustSync(t, sync, "")
	mustSync(t, sync, "")
	if got := api.take(); len(got) != 0 || s.applied[photoPart] != "v2" || s.refused == nil || s.refused.version != "" {
		t.Fatalf("a refused removal: %v %+v %+v", got, s.applied, s.refused)
	}
	delete(api.fail, "photo ")
	delete(api.fail, "remove photo")

	// A flood wait or Telegram down is no refusal: the sync fails (RunProfile
	// waits as Telegram asks), to be tried at the next refresh.
	api.fail["photo "] = &tg.APIError{Code: 429, Description: "Too Many Requests: retry after 30", RetryAfter: 30}
	core.set("v4", "jpeg-4")
	if wait, ok := tg.IsFlood(sync("v4")); !ok || wait != 30*time.Second {
		t.Fatalf("flood: %v %v", wait, ok)
	}
	api.fail["photo "] = &tg.APIError{Code: 502, Description: "Bad Gateway"}
	if err := sync("v4"); err == nil || s.refused != nil && s.refused.version == "v4" {
		t.Fatalf("Telegram down: %v %+v", err, s.refused)
	}
	delete(api.fail, "photo ")
	// Core does not answer: the sync fails and nothing is set.
	core.err = errors.New("core: unavailable")
	if err := sync("v4"); err == nil || len(api.take()) != 0 {
		t.Fatalf("core down: %v", err)
	}
	core.err = nil
	mustSync(t, sync, "v4")
	if got := strings.Join(api.take(), " "); got != "photo jpeg-4" {
		t.Fatalf("once core answers: %s", got)
	}
}

// general.bot_profile off leaves the photo, and keeps its keys: switching
// back on does not upload the same photo again.
func TestProfileClearKeepsThePhoto(t *testing.T) {
	set := settings.New()
	set.SetStaff([]settings.Contact{{TelegramID: 7, Role: "owner"}})
	_, api, memo, core, sync := photoSync(t, set)
	core.set("v1", "jpeg-1")
	mustSync(t, sync, "v1")
	want := "commands 0/ description  about  commands 0/en description en about en commands 0/fa description fa about fa commands 7/ photo jpeg-1"
	if got := strings.Join(api.take(), " "); got != want {
		t.Fatalf("first sync, the photo last:\n%s\nwant:\n%s", got, want)
	}

	set.Replace(map[string]string{"general.bot_profile": "false"})
	mustSync(t, sync, "v1")
	if got := strings.Join(api.take(), " "); strings.Contains(got, "photo") || !strings.Contains(got, "delete 7/") ||
		!strings.Contains(memo.v, `"photo":"v1"`) || !strings.Contains(memo.v, `"photo_id":"uid-1"`) || api.photo != "jpeg-1" {
		t.Fatalf("switched off (memo %s): %s", memo.v, got)
	}
	// While off, a new or removed photo is left alone too.
	core.set("v2", "jpeg-2")
	mustSync(t, sync, "v2")
	mustSync(t, sync, "")
	if got := api.take(); len(got) != 0 || core.count() != 1 {
		t.Fatalf("off (%d fetches): %v", core.count(), got)
	}

	set.Replace(nil)
	core.set("v1", "jpeg-1")
	mustSync(t, sync, "v1")
	if got := api.take(); len(got) != 10 || slices.Contains(got, "photo jpeg-1") || core.count() != 1 {
		t.Fatalf("switched on again (%d fetches): %v", core.count(), got)
	}
	core.set("v2", "jpeg-2")
	mustSync(t, sync, "v2")
	if got := strings.Join(api.take(), " "); got != "photo jpeg-2" {
		t.Fatalf("a new photo once on: %s", got)
	}
}

// The bot takes off only the photo it set, and only while it is still the
// current one: removeMyProfilePhoto takes off whatever photo is current,
// such as one the owner set later in @BotFather.
func TestProfileSyncPhotoTakesOffOnlyItsOwn(t *testing.T) {
	set := settings.New()
	_, api, memo, core, sync := photoSync(t, set)
	mustSync(t, sync, "")
	api.take()

	// Replaced in @BotFather, then removed on the Branding page: the bot
	// reads which photo is current, forgets its own and leaves the owner's.
	core.set("v1", "jpeg-1")
	mustSync(t, sync, "v1")
	owners := api.botFather()
	core.set("", "")
	reads := api.readCount()
	mustSync(t, sync, "")
	if got := api.take(); !slices.Equal(got, []string{"photo jpeg-1"}) || api.photoID != owners || api.readCount() != reads+1 ||
		strings.Contains(memo.v, `"photo`) {
		t.Fatalf("replaced in @BotFather (memo %s, current %s): %v", memo.v, api.photoID, got)
	}
	mustSync(t, sync, "")
	if got := api.take(); len(got) != 0 || api.readCount() != reads+1 {
		t.Fatalf("forgotten, yet: %v", got)
	}

	// Replaced in @BotFather while "keep the profile up to date" is off,
	// and removed on the Branding page: nothing changes in Telegram, then or
	// once it is on again.
	core.set("v2", "jpeg-2")
	mustSync(t, sync, "v2")
	set.Replace(map[string]string{"general.bot_profile": "false"})
	mustSync(t, sync, "v2")
	owners = api.botFather()
	core.set("", "")
	mustSync(t, sync, "")
	set.Replace(nil)
	mustSync(t, sync, "")
	if got := api.take(); slices.Contains(got, "remove photo") || api.photoID != owners || strings.Contains(memo.v, `"photo`) {
		t.Fatalf("replaced while off (memo %s, current %s): %v", memo.v, api.photoID, got)
	}

	// Removed in Telegram already (no photo at all): only forgotten.
	core.set("v3", "jpeg-3")
	mustSync(t, sync, "v3")
	api.mu.Lock()
	api.photo, api.photoID = "", ""
	api.mu.Unlock()
	core.set("", "")
	mustSync(t, sync, "")
	if got := api.take(); !slices.Equal(got, []string{"photo jpeg-3"}) || strings.Contains(memo.v, `"photo`) {
		t.Fatalf("no photo left (memo %s): %v", memo.v, got)
	}

	// Its own photo, still the current one: taken off.
	core.set("v4", "jpeg-4")
	mustSync(t, sync, "v4")
	core.set("", "")
	mustSync(t, sync, "")
	if got := strings.Join(api.take(), " "); got != "photo jpeg-4 remove photo" || api.photo != "" {
		t.Fatalf("its own photo: %s", got)
	}
}

// Nothing is removed blindly: a read of the current photo that fails stops
// the sync, to be tried at the next refresh, and a photo Telegram did not
// identify when it was set is never taken off.
func TestProfileSyncPhotoNeverRemovesBlindly(t *testing.T) {
	var logs bytes.Buffer
	s, api, memo, core, sync := photoSync(t, settings.New())
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	core.set("v1", "jpeg-1")
	mustSync(t, sync, "v1")
	api.take()

	core.set("", "")
	api.fail[readPhotos] = &tg.APIError{Code: 502, Description: "Bad Gateway"}
	if err := sync(""); err == nil {
		t.Fatal("a failed read must stop the sync")
	}
	if got := api.take(); len(got) != 0 || api.photo != "jpeg-1" || !strings.Contains(memo.v, `"photo_id":"uid-1"`) {
		t.Fatalf("after a failed read (memo %s): %v", memo.v, got)
	}
	// A flood wait is RunProfile's to wait out.
	api.fail[readPhotos] = &tg.APIError{Code: 429, Description: "Too Many Requests: retry after 30", RetryAfter: 30}
	if wait, ok := tg.IsFlood(sync("")); !ok || wait != 30*time.Second || len(api.take()) != 0 {
		t.Fatalf("flood: %v %v", wait, ok)
	}
	delete(api.fail, readPhotos)
	mustSync(t, sync, "")
	if got := strings.Join(api.take(), " "); got != "remove photo" || strings.Contains(memo.v, `"photo`) {
		t.Fatalf("once Telegram answers (memo %s): %s", memo.v, got)
	}

	// The read after an upload fails: the version is kept (the photo is not
	// uploaded again), and that photo is never taken off.
	api.fail[readPhotos] = errors.New("telegram: getUserProfilePhotos: connection reset by peer")
	core.set("v2", "jpeg-2")
	mustSync(t, sync, "v2")
	if got := strings.Join(api.take(), " "); got != "photo jpeg-2" || !strings.Contains(memo.v, `"photo":"v2"`) ||
		strings.Contains(memo.v, `"photo_id"`) || !strings.Contains(logs.String(), "will not take it off") {
		t.Fatalf("no identity (memo %s): %s\n%s", memo.v, got, logs.String())
	}
	delete(api.fail, readPhotos)
	mustSync(t, sync, "v2")
	if got := api.take(); len(got) != 0 {
		t.Fatalf("uploaded again: %v", got)
	}
	core.set("", "")
	mustSync(t, sync, "")
	if got := api.take(); len(got) != 0 || api.photo != "jpeg-2" || strings.Contains(memo.v, `"photo`) {
		t.Fatalf("removed without its identity (memo %s): %v", memo.v, got)
	}
}

// The vendor credit is in every language's about text, and gone under a
// white label license.
func TestWantedProfileVendorCredit(t *testing.T) {
	cat, set := i18n.MustLoad(), settings.New()
	for _, white := range []bool{false, true} {
		p, _ := wantedProfile(cat, set, "", white, "")
		for code, l := range p.Langs {
			if got := strings.Contains(l.About, "Made by TECSO-DEV"); got == white {
				t.Fatalf("white label %v, language %q: about %q", white, code, l.About)
			}
			if n := utf8.RuneCountInString(l.About); n > 120 {
				t.Fatalf("language %q: about is %d characters, Telegram takes 120", code, n)
			}
		}
	}
}

// The menu button next to the message box opens the Mini App while the app
// is on and the store has a public address, is set once, follows an address
// that changes, and gives the default back when the app goes (or the profile
// is handed back to @BotFather).
func TestProfileSyncMenuButton(t *testing.T) {
	ctx := context.Background()
	cat, set := i18n.MustLoad(), settings.New()
	api, memo := newFakeProfileAPI(), &memMemo{}
	newSync := func() *profileSync { return &profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler)} }
	s := newSync()
	menuCalls := func() int {
		n := 0
		for _, c := range api.calls {
			if strings.HasPrefix(c, "menu ") {
				n++
			}
		}
		return n
	}
	apply := func(s *profileSync, url string, botProfile bool) {
		t.Helper()
		if !botProfile {
			set.Replace(map[string]string{"general.bot_profile": "false"})
		} else {
			set.Replace(nil)
		}
		p, on := wantedProfile(cat, set, "", true, url)
		if err := s.sync(ctx, p, on); err != nil {
			t.Fatal(err)
		}
	}
	const app = "https://shop.example.com/app/"
	want := cat.T("fa", "profile.menu_button") + " " + app // the first language's text, one for everyone

	apply(s, "", true)
	if menuCalls() != 0 || api.menu != "" {
		t.Fatalf("no Mini App: nothing is set (%v)", api.calls)
	}
	apply(s, app, true)
	if api.menu != want || menuCalls() != 1 {
		t.Fatalf("the Mini App appears: %q after %d calls", api.menu, menuCalls())
	}
	apply(s, app, true)
	apply(newSync(), app, true) // after a restart: the memo says it is set
	if menuCalls() != 1 {
		t.Fatalf("an unchanged button is not set again: %d calls", menuCalls())
	}
	apply(s, "https://other.example.org/app/", true)
	if api.menu != cat.T("fa", "profile.menu_button")+" https://other.example.org/app/" || menuCalls() != 2 {
		t.Fatalf("a new address: %q after %d calls", api.menu, menuCalls())
	}
	apply(s, "", true) // switched off, or no public address any more
	if api.menu != "default" || menuCalls() != 3 {
		t.Fatalf("the app goes: %q after %d calls", api.menu, menuCalls())
	}
	apply(s, "", true)
	if menuCalls() != 3 {
		t.Fatalf("the default is not set again: %d calls", menuCalls())
	}
	// Handing the profile back to @BotFather takes the button back too.
	apply(s, app, true)
	apply(s, app, false)
	if api.menu != "default" {
		t.Fatalf("general.bot_profile off: %q", api.menu)
	}
	// A refusal stops the sync, to be tried again at the next refresh.
	api.fail["menu "] = errors.New("flood")
	set.Replace(nil)
	p, on := wantedProfile(cat, set, "", true, app)
	if err := s.sync(ctx, p, on); err == nil {
		t.Fatal("a refused menu button must stop the sync")
	}
	delete(api.fail, "menu ")
	if err := s.sync(ctx, p, on); err != nil || api.menu != want {
		t.Fatalf("retried at the next refresh: %v, %q", err, api.menu)
	}
}
