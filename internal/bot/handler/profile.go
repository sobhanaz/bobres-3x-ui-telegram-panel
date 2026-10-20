package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/branding"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProfileAPI is the part of the Bot API that sets the bot's own profile
// (and reads its profile photos).
type ProfileAPI interface {
	SetMyCommands(ctx context.Context, cmds []tg.BotCommand, chatID int64, lang string) error
	DeleteMyCommands(ctx context.Context, chatID int64, lang string) error
	SetMyDescription(ctx context.Context, text, lang string) error
	SetMyShortDescription(ctx context.Context, text, lang string) error
	SetMyProfilePhoto(ctx context.Context, jpeg []byte) error
	RemoveMyProfilePhoto(ctx context.Context) error
	// SetChatMenuButton sets the menu button next to the message box for every
	// chat: a Mini App, or (nil) back to the default.
	SetChatMenuButton(ctx context.Context, b *tg.MenuButtonWebApp) error
	GetUserProfilePhotos(ctx context.Context, userID int64, limit int) (*tg.UserProfilePhotos, error)
}

// ProfileMemo keeps what the bot last set in Telegram, so a restart does
// not set it all again ("" when nothing is kept).
type ProfileMemo interface {
	Get(ctx context.Context) (string, error)
	Set(ctx context.Context, v string) error
}

// profileLang is what Telegram shows the users of one language.
type profileLang struct {
	Commands    []tg.BotCommand `json:"commands"`
	Description string          `json:"description"`
	About       string          `json:"about"`
}

// profile is the whole profile the bot wants: per language ("" for the
// users of any other language, in the bot's first language), the menus of
// staff chats, which add the staff commands, the version of the bot
// photo uploaded on the Branding page ("" none, photoUnknown when core could
// not read it), and the menu button that opens the Mini App (nil: the
// default one).
type profile struct {
	Langs map[string]profileLang
	Staff map[int64][]tg.BotCommand
	Photo string
	Menu  *menuButton
}

// menuButton is the Mini App button next to the message box: Telegram has one
// text for everyone, so it is the bot's first language's.
type menuButton struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// photoUnknown is the photo version core sends when it cannot read the
// photo, and the bot's own until the first settings refresh: the bot leaves
// its photo as it is.
const photoUnknown = "?"

// commands of the customer menu; terms only when the store links its terms
// or privacy policy (otherwise /terms only says there are none).
var customerCommands = []string{"start", "menu", "terms", "paysupport"}

// wantedProfile is the profile the current texts, branding, staff and bot
// photo (its version) call for, with the vendor credit in the about text
// unless the license is white label; false when general.bot_profile is off (the
// operator sets the profile in @BotFather).
func wantedProfile(cat *i18n.Catalog, set *settings.Store, photo string, whiteLabel bool, miniApp string) (profile, bool) {
	if !set.Bool("general.bot_profile", true) {
		return profile{}, false
	}
	terms := set.Get("branding.terms_url") != "" || set.Get("branding.privacy_url") != ""
	menu := func(lang string, extra ...string) []tg.BotCommand {
		var out []tg.BotCommand
		for _, c := range append(slices.Clone(customerCommands), extra...) {
			if c == "terms" && !terms {
				continue
			}
			out = append(out, tg.BotCommand{Command: c, Description: cat.T(lang, "profile.cmd."+c)})
		}
		return out
	}
	p := profile{Langs: map[string]profileLang{}, Staff: map[int64][]tg.BotCommand{}, Photo: photo}
	if miniApp != "" {
		p.Menu = &menuButton{Text: cat.T(i18n.Normalize(""), "profile.menu_button"), URL: miniApp}
	}
	for _, code := range append([]string{""}, i18n.Languages...) {
		lang := i18n.Normalize(code) // "" is the first language
		p.Langs[code] = profileLang{Commands: menu(lang), Description: cat.T(lang, "profile.description"), About: branding.AboutWithCredit(cat.T(lang, "profile.about"), whiteLabel)}
	}
	for _, c := range set.Staff() {
		switch lang := i18n.Normalize(c.Language); c.Role {
		case "owner", "admin":
			p.Staff[c.TelegramID] = menu(lang, "admin", "dashboard")
		case "support":
			p.Staff[c.TelegramID] = menu(lang, "dashboard")
		}
	}
	return p, true
}

// fingerprint tells two versions of a part of the profile apart.
func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// profileSync sets in Telegram the parts of the profile that differ from
// what it set last (applied: part key -> fingerprint; the photo's version
// and identity).
type profileSync struct {
	api  ProfileAPI
	memo ProfileMemo
	log  *slog.Logger
	// botID is the bot's own user id (getMe), whose profile photos tell
	// which photo is the current one.
	botID int64
	// photo fetches the bot photo from core: its version and JPEG, or ""
	// when there is none (nil: the photo is left alone).
	photo   func(ctx context.Context) (version string, jpeg []byte, err error)
	applied map[string]string
	loaded  bool
	// gone: staff chats the bot could not reach (never started, blocked),
	// tried again after goneRetry, when their menu changes, or after a
	// restart (it is not remembered).
	gone map[string]goneChat
	// refused: the photo Telegram refused for good, tried again after
	// goneRetry, when the photo changes, or after a restart.
	refused *refusedPhoto
	now     func() time.Time
}

type goneChat struct {
	fp string
	at time.Time
}

type refusedPhoto struct {
	version string // "" for a removal
	at      time.Time
}

// goneRetry is how long a staff chat the bot could not reach waits before
// its menu is tried again.
const goneRetry = time.Hour

// menuPart is the key of the Mini App menu button in applied: the
// fingerprint of what the bot set (missing: the default button).
const menuPart = "menu_button"

// photoPart is the key of the profile photo in applied: the version of the
// photo the bot set (missing: none of its own). photoIDPart is the
// identity Telegram gave that photo, read back when it was set (missing:
// Telegram did not say): the bot takes off only a photo that still has it,
// not one set since in @BotFather.
const (
	photoPart   = "photo"
	photoIDPart = "photo_id"
)

func langPart(code string) string { return "lang:" + code }
func chatPart(chat int64) string  { return "chat:" + strconv.FormatInt(chat, 10) }
func partChat(k string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(k, "chat:"), 10, 64)
	return n, err == nil && strings.HasPrefix(k, "chat:")
}

// load reads what was set before a restart, once. An error leaves it for
// the next sync, so the memo is never overwritten with less than it holds
// (which would leave staff menus behind that nothing removes); an
// unreadable memo means everything is set again.
func (s *profileSync) load(ctx context.Context) error {
	if s.loaded {
		return nil
	}
	applied := map[string]string{}
	if s.memo != nil {
		raw, err := s.memo.Get(ctx)
		if err != nil {
			return fmt.Errorf("read what was set before: %w", err)
		}
		if raw != "" && json.Unmarshal([]byte(raw), &applied) != nil {
			applied = map[string]string{}
		}
	}
	s.applied, s.loaded = applied, true
	if s.gone == nil {
		s.gone = map[string]goneChat{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return nil
}

func (s *profileSync) save(ctx context.Context) {
	if s.memo == nil {
		return
	}
	b, _ := json.Marshal(s.applied)
	if err := s.memo.Set(ctx, string(b)); err != nil {
		s.log.Warn("bot profile: could not remember what was set", "err", err)
	}
}

// sync brings Telegram in line with want. An error stops it; what was done
// so far is kept, and the rest is tried at the next refresh. With on false
// it takes back what it set (clear).
func (s *profileSync) sync(ctx context.Context, want profile, on bool) error {
	if err := s.load(ctx); err != nil {
		return err
	}
	changed := false
	defer func() {
		if changed {
			s.save(ctx)
		}
	}()
	if !on {
		return s.clear(ctx, &changed)
	}
	codes := make([]string, 0, len(want.Langs))
	for code := range want.Langs {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	for _, code := range codes {
		l, key := want.Langs[code], langPart(code)
		fp := fingerprint(l)
		if s.applied[key] == fp {
			continue
		}
		if err := s.api.SetMyCommands(ctx, l.Commands, 0, code); err != nil {
			return fmt.Errorf("commands (%q): %w", code, err)
		}
		if err := s.api.SetMyDescription(ctx, l.Description, code); err != nil {
			return fmt.Errorf("description (%q): %w", code, err)
		}
		if err := s.api.SetMyShortDescription(ctx, l.About, code); err != nil {
			return fmt.Errorf("short description (%q): %w", code, err)
		}
		s.applied[key], changed = fp, true
	}

	chats := make([]int64, 0, len(want.Staff))
	for chat := range want.Staff {
		chats = append(chats, chat)
	}
	slices.Sort(chats)
	for _, chat := range chats {
		key, fp := chatPart(chat), fingerprint(want.Staff[chat])
		if s.applied[key] == fp {
			continue
		}
		if g, ok := s.gone[key]; ok && g.fp == fp && s.now().Sub(g.at) < goneRetry {
			continue
		}
		if err := s.api.SetMyCommands(ctx, want.Staff[chat], chat, ""); err != nil {
			if !chatGone(err) {
				return fmt.Errorf("staff menu of %d: %w", chat, err)
			}
			s.log.Info("bot profile: no staff menu for a chat the bot cannot reach yet", "telegram_id", chat, "err", err)
			s.gone[key] = goneChat{fp: fp, at: s.now()}
			continue
		}
		delete(s.gone, key)
		s.applied[key], changed = fp, true
	}
	// Staff who left get the customer menu back.
	for key := range s.gone {
		if chat, _ := partChat(key); want.Staff[chat] == nil {
			delete(s.gone, key)
		}
	}
	for key := range s.applied {
		chat, ok := partChat(key)
		if !ok {
			continue
		}
		if _, still := want.Staff[chat]; still {
			continue
		}
		if err := s.api.DeleteMyCommands(ctx, chat, ""); err != nil && !chatGone(err) {
			return fmt.Errorf("staff menu of %d: %w", chat, err)
		}
		delete(s.applied, key)
		changed = true
	}
	if err := s.syncMenuButton(ctx, want.Menu, &changed); err != nil {
		return err
	}
	// The photo last: a photo Telegram refuses never holds up the rest.
	return s.syncPhoto(ctx, want.Photo, &changed)
}

// syncMenuButton sets the menu button to the Mini App when one is wanted and
// is not set yet, and gives the default one back when the bot set one and
// none is wanted now (the app was switched off, or the store lost its
// public address).
func (s *profileSync) syncMenuButton(ctx context.Context, want *menuButton, changed *bool) error {
	have, had := s.applied[menuPart]
	switch {
	case want != nil:
		fp := fingerprint(want)
		if have == fp {
			return nil
		}
		if err := s.api.SetChatMenuButton(ctx, &tg.MenuButtonWebApp{Text: want.Text, URL: want.URL}); err != nil {
			return fmt.Errorf("menu button: %w", err)
		}
		s.applied[menuPart], *changed = fp, true
	case had:
		if err := s.api.SetChatMenuButton(ctx, nil); err != nil {
			return fmt.Errorf("menu button: %w", err)
		}
		delete(s.applied, menuPart)
		*changed = true
	}
	return nil
}

// syncPhoto sets the photo core has or, when core has none, takes off the
// photo the bot set, only while it is still the bot's current photo: one
// set later in @BotFather stays. Nothing changes while core cannot tell.
func (s *profileSync) syncPhoto(ctx context.Context, want string, changed *bool) error {
	have := s.applied[photoPart]
	if want == photoUnknown || want == have || s.refusedRecently(want) {
		return nil
	}
	var jpeg []byte
	if want != "" {
		if s.photo == nil {
			return nil
		}
		version, data, err := s.photo(ctx)
		if err != nil {
			return fmt.Errorf("profile photo from core: %w", err)
		}
		// What core has now: it may have changed since the settings were
		// read ("" when it was removed).
		want, jpeg = version, data
		if want == have || s.refusedRecently(want) {
			return nil
		}
	}
	if want == "" { // none: take back the bot's own (have is not "")
		// removeMyProfilePhoto takes off the current photo, whoever set it.
		ours, err := s.ownPhotoCurrent(ctx)
		if err != nil {
			return err
		}
		if ours {
			if err := s.api.RemoveMyProfilePhoto(ctx); err != nil {
				return s.photoFailed(want, "removing the profile photo", err)
			}
		}
		delete(s.applied, photoPart)
		delete(s.applied, photoIDPart)
	} else {
		if err := s.api.SetMyProfilePhoto(ctx, jpeg); err != nil {
			return s.photoFailed(want, "the profile photo", err)
		}
		s.applied[photoPart] = want
		delete(s.applied, photoIDPart)
		// Which photo Telegram made of it, read back at once: the one the
		// bot may take off later. Unknown, the bot never takes it off.
		if id, err := s.currentPhoto(ctx); err != nil {
			s.log.Warn("bot profile: the photo is set, but Telegram did not say which it is now; the bot will not take it off", "err", err)
		} else if id != "" {
			s.applied[photoIDPart] = id
		}
	}
	s.refused = nil
	*changed = true
	return nil
}

// ownPhotoCurrent tells whether the photo the bot set is still its current
// profile photo, as Telegram says now: false when it was replaced (in
// @BotFather) or removed since, or when Telegram did not say which photo it
// was when it was set. A read that fails stops the sync, to be tried again
// at the next refresh: nothing is taken off blindly.
func (s *profileSync) ownPhotoCurrent(ctx context.Context) (bool, error) {
	id := s.applied[photoIDPart]
	if id == "" {
		return false, nil
	}
	current, err := s.currentPhoto(ctx)
	if err != nil {
		return false, fmt.Errorf("the current profile photo: %w", err)
	}
	return current == id, nil
}

// currentPhoto is the identity of the bot's current profile photo, the
// first getUserProfilePhotos returns: the file_unique_id of its largest
// size ("" when the bot has none).
func (s *profileSync) currentPhoto(ctx context.Context) (string, error) {
	p, err := s.api.GetUserProfilePhotos(ctx, s.botID, 1)
	if err != nil {
		return "", err
	}
	if p == nil || len(p.Photos) == 0 {
		return "", nil
	}
	var largest tg.PhotoSize
	for _, size := range p.Photos[0] {
		if size.Width*size.Height >= largest.Width*largest.Height {
			largest = size
		}
	}
	return largest.FileUniqueID, nil
}

// refusedRecently: Telegram refused this photo version for good less than
// goneRetry ago.
func (s *profileSync) refusedRecently(version string) bool {
	return s.refused != nil && s.refused.version == version && s.now().Sub(s.refused.at) < goneRetry
}

// photoFailed handles a photo call Telegram did not take. A refusal for
// good is logged and kept for that version, to be tried again after
// goneRetry or when the photo changes; anything else (a flood wait,
// Telegram down) stops the sync until the next refresh.
func (s *profileSync) photoFailed(version, what string, err error) error {
	if !photoRefused(err) {
		return fmt.Errorf("%s: %w", what, err)
	}
	s.log.Warn("bot profile: Telegram refused "+what+"; trying again in an hour or when the photo changes", "err", err)
	s.refused = &refusedPhoto{version: version, at: s.now()}
	return nil
}

// photoRefused: Telegram refused a photo call for good: a 400 (the picture
// itself, or too many profile photos), or a 404 from a local Bot API server
// older than 9.4, which does not know the method.
func photoRefused(err error) bool {
	var ae *tg.APIError
	return errors.As(err, &ae) && (ae.Code == 400 || ae.Code == 404)
}

// clear takes back what the bot set, so what is set in @BotFather shows
// again: @BotFather sets only the values for every language, which the
// bot's Persian and English values and the staff menus would hide. The
// values for every language stay as they are (they are @BotFather's too),
// to be edited there. So does the photo, which hides nothing of
// @BotFather's: its keys are kept, so switching back on does not upload the
// same photo again.
func (s *profileSync) clear(ctx context.Context, changed *bool) error {
	keys := make([]string, 0, len(s.applied))
	for key := range s.applied {
		if key != photoPart && key != photoIDPart && key != menuPart {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		if chat, ok := partChat(key); ok {
			if err := s.api.DeleteMyCommands(ctx, chat, ""); err != nil && !chatGone(err) {
				return fmt.Errorf("staff menu of %d: %w", chat, err)
			}
		} else if code, ok := strings.CutPrefix(key, "lang:"); ok && code != "" {
			if err := s.api.DeleteMyCommands(ctx, 0, code); err != nil {
				return fmt.Errorf("commands (%q): %w", code, err)
			}
			if err := s.api.SetMyDescription(ctx, "", code); err != nil { // "": no description of its own
				return fmt.Errorf("description (%q): %w", code, err)
			}
			if err := s.api.SetMyShortDescription(ctx, "", code); err != nil {
				return fmt.Errorf("short description (%q): %w", code, err)
			}
		}
		delete(s.applied, key)
		*changed = true
	}
	if _, ok := s.applied[menuPart]; ok { // the Mini App button goes, so what @BotFather sets shows again
		if err := s.api.SetChatMenuButton(ctx, nil); err != nil {
			return fmt.Errorf("menu button: %w", err)
		}
		delete(s.applied, menuPart)
		*changed = true
	}
	clear(s.gone)
	return nil
}

// chatGone: Telegram refused a chat the bot cannot reach (never started,
// blocked, deleted), as opposed to a failure worth retrying.
func chatGone(err error) bool {
	var ae *tg.APIError
	return tg.IsBlocked(err) || errors.As(err, &ae) && ae.Code == 400 && strings.Contains(strings.ToLower(ae.Description), "chat not found")
}

// RunProfile keeps the bot's Telegram profile (menu commands, description,
// short description, profile photo) in line with its texts, the branding,
// the staff and the bot photo: after each settings refresh it sets what
// changed, until ctx ends. botID is the bot's own user id (getMe), whose
// profile photos tell whether the photo it set is still the current one.
// memo may be nil.
func (h *Handler) RunProfile(ctx context.Context, api ProfileAPI, botID int64, memo ProfileMemo) error {
	s := &profileSync{api: api, memo: memo, log: h.log, botID: botID}
	if h.core != nil {
		s.photo = h.botPhoto
	}
	var notBefore time.Time // after a 429, until Telegram allows these calls again
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-h.profileKick:
		}
		if time.Now().Before(notBefore) {
			continue
		}
		want, on := wantedProfile(h.cat, h.set, h.botPhotoVersion(), h.whiteLabel.Load(), h.miniAppURL())
		err := s.sync(ctx, want, on)
		if err == nil || ctx.Err() != nil {
			continue
		}
		if wait, ok := tg.IsFlood(err); ok {
			notBefore = time.Now().Add(wait)
		}
		h.log.Warn("bot profile not updated in Telegram; trying again at a later refresh", "err", err)
	}
}

// miniAppURL is where the Mini App opens from, as core last said ("" when it
// is off, has no public HTTPS address, or settings are not loaded yet).
func (h *Handler) miniAppURL() string {
	if u := h.miniApp.Load(); u != nil {
		return *u
	}
	return ""
}

// botPhotoVersion is the bot photo's version from the last settings
// refresh (photoUnknown before the first).
func (h *Handler) botPhotoVersion() string {
	if v := h.photoVersion.Load(); v != nil {
		return *v
	}
	return photoUnknown
}

// botPhoto fetches the bot photo from core: its version and JPEG, or ""
// when the store has none.
func (h *Handler) botPhoto(ctx context.Context) (string, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := h.core.GetBotPhoto(ctx, &corev1.GetBotPhotoRequest{})
	if status.Code(err) == codes.NotFound {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return p.GetVersion(), p.GetJpeg(), nil
}
