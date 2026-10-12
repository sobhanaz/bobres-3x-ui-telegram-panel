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

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// ProfileAPI is the part of the Bot API that sets the bot's own profile.
type ProfileAPI interface {
	SetMyCommands(ctx context.Context, cmds []tg.BotCommand, chatID int64, lang string) error
	DeleteMyCommands(ctx context.Context, chatID int64, lang string) error
	SetMyDescription(ctx context.Context, text, lang string) error
	SetMyShortDescription(ctx context.Context, text, lang string) error
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
// users of any other language, in the bot's first language) and the menus
// of staff chats, which add the staff commands.
type profile struct {
	Langs map[string]profileLang
	Staff map[int64][]tg.BotCommand
}

// commands of the customer menu; terms only when the store links its terms
// or privacy policy (otherwise /terms only says there are none).
var customerCommands = []string{"start", "menu", "terms", "paysupport"}

// wantedProfile is the profile the current texts, branding and staff call
// for; false when general.bot_profile is off (the operator sets the
// profile in @BotFather).
func wantedProfile(cat *i18n.Catalog, set *settings.Store) (profile, bool) {
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
	p := profile{Langs: map[string]profileLang{}, Staff: map[int64][]tg.BotCommand{}}
	for _, code := range append([]string{""}, i18n.Languages...) {
		lang := i18n.Normalize(code) // "" is the first language
		p.Langs[code] = profileLang{Commands: menu(lang), Description: cat.T(lang, "profile.description"), About: cat.T(lang, "profile.about")}
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
// what it set last (applied: part key -> fingerprint).
type profileSync struct {
	api     ProfileAPI
	memo    ProfileMemo
	log     *slog.Logger
	applied map[string]string
	loaded  bool
	// gone: staff chats the bot could not reach (never started, blocked),
	// tried again after goneRetry, when their menu changes, or after a
	// restart (it is not remembered).
	gone map[string]goneChat
	now  func() time.Time
}

type goneChat struct {
	fp string
	at time.Time
}

// goneRetry is how long a staff chat the bot could not reach waits before
// its menu is tried again.
const goneRetry = time.Hour

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
	return nil
}

// clear takes back what the bot set, so what is set in @BotFather shows
// again: @BotFather sets only the values for every language, which the
// bot's Persian and English values and the staff menus would hide. The
// values for every language stay as they are (they are @BotFather's too),
// to be edited there.
func (s *profileSync) clear(ctx context.Context, changed *bool) error {
	keys := make([]string, 0, len(s.applied))
	for key := range s.applied {
		keys = append(keys, key)
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
// short description) in line with its texts, the branding and the staff:
// after each settings refresh it sets what changed, until ctx ends. memo
// may be nil.
func (h *Handler) RunProfile(ctx context.Context, api ProfileAPI, memo ProfileMemo) error {
	s := &profileSync{api: api, memo: memo, log: h.log}
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
		want, on := wantedProfile(h.cat, h.set)
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
