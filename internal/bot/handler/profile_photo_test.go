package handler

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgfake"
)

// The photo uploaded on the Branding page reaches Telegram: each settings
// refresh keeps core's version of it, and the sync fetches it from core.
func TestProfilePhotoFromCore(t *testing.T) {
	if v := New(nil, nil, nil, i18n.MustLoad(), nil, Config{}, nil).botPhotoVersion(); v != photoUnknown {
		t.Fatalf("version before the first refresh: %q", v)
	}
	w := newWorld(t)
	ctx := context.Background()
	st := w.stack.Store
	api := newFakeProfileAPI()
	s := &profileSync{api: api, log: slog.New(slog.DiscardHandler), photo: w.h.botPhoto}
	sync := func() {
		t.Helper()
		if err := w.h.RefreshSettings(ctx); err != nil {
			t.Fatal(err)
		}
		p, on := wantedProfile(w.h.cat, w.h.set, w.h.botPhotoVersion(), true, "")
		if err := s.sync(ctx, p, on); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	sync()
	if v := w.h.botPhotoVersion(); v != "" || slices.Contains(api.take(), "remove photo") {
		t.Fatalf("no photo in core: version %q", v)
	}

	jpeg := []byte("\xff\xd8\xff the store's photo")
	if err := st.PutAsset(ctx, st.Conn(), corestore.AssetBotPhoto, "image/jpeg", jpeg); err != nil {
		t.Fatal(err)
	}
	a, err := st.AssetInfo(ctx, st.Conn(), corestore.AssetBotPhoto)
	if err != nil {
		t.Fatal(err)
	}
	sync()
	if v := w.h.botPhotoVersion(); v != strconv.FormatInt(a.UpdatedAt.UnixNano(), 10) {
		t.Fatalf("version %q, uploaded at %v", v, a.UpdatedAt)
	}
	if got := api.take(); !slices.Equal(got, []string{"photo " + string(jpeg)}) || s.applied[photoPart] != w.h.botPhotoVersion() {
		t.Fatalf("with a photo in core: %q %v", got, s.applied)
	}

	// Removed on the Branding page: the bot takes back the photo it set.
	if _, err := st.DeleteAsset(ctx, st.Conn(), corestore.AssetBotPhoto); err != nil {
		t.Fatal(err)
	}
	sync()
	if got := api.take(); !slices.Equal(got, []string{"remove photo"}) || api.photo != "" {
		t.Fatalf("after the removal: %q", got)
	}
	// Core's NotFound is no photo, not a failure.
	if v, b, err := w.h.botPhoto(ctx); v != "" || b != nil || err != nil {
		t.Fatalf("no photo: %q %q %v", v, b, err)
	}
}

// Through the Bot API client, against the fake Telegram: the bot reads back
// the photo it set and takes off only that one, while it is current.
func TestProfilePhotoThroughTheBotAPI(t *testing.T) {
	fake := tgfake.New()
	srv := httptest.NewServer(fake.Handler())
	t.Cleanup(srv.Close)
	ctx := context.Background()
	core, memo := &fakePhotos{}, &memMemo{}
	api := tg.New("123:abc", tg.WithAPIRoot(srv.URL), tg.WithNoRetry())
	s := &profileSync{api: api, memo: memo, log: slog.New(slog.DiscardHandler), botID: tgfake.BotID, photo: core.get}
	set := settings.New()
	sync := func(version string) {
		t.Helper()
		p, on := wantedProfile(i18n.MustLoad(), set, version, true, "")
		if err := s.sync(ctx, p, on); err != nil {
			t.Fatalf("sync %q: %v", version, err)
		}
	}
	removals := func() int {
		n := 0
		for _, c := range fake.Profile() {
			if c.Method == "removeMyProfilePhoto" {
				n++
			}
		}
		return n
	}

	core.set("v1", "jpeg-1")
	sync("v1")
	if own := fake.ProfilePhoto(); own == "" || !strings.Contains(memo.v, `"photo_id":"`+own+`"`) {
		t.Fatalf("set: current %q, memo %s", own, memo.v)
	}
	// Replaced in @BotFather: removing it on the Branding page leaves that one.
	owners := fake.SetProfilePhoto()
	core.set("", "")
	sync("")
	if fake.ProfilePhoto() != owners || removals() != 0 {
		t.Fatalf("replaced in @BotFather: current %q, %d removals", fake.ProfilePhoto(), removals())
	}
	// Its own photo, still the current one: taken off.
	core.set("v2", "jpeg-2")
	sync("v2")
	core.set("", "")
	sync("")
	if fake.ProfilePhoto() != "" || removals() != 1 {
		t.Fatalf("its own photo: current %q, %d removals", fake.ProfilePhoto(), removals())
	}
}
