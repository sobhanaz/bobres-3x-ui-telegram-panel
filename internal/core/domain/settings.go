package domain

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Setting kinds: how a value is checked and stored.
const (
	KindText    = "text"
	KindInt     = "int"
	KindBool    = "bool"
	KindEnum    = "enum"
	KindTZ      = "tz"
	KindURL     = "url"
	KindChannel = "channel"
	KindColor   = "color"
)

// Dashboard permissions a setting needs (the web package's names).
const (
	permSettings  = "settings.write"
	permGateways  = "gateways.write" // owner only: where customers' money goes
	permBranding  = "branding.write"
	permDiscounts = "discounts.write"
)

// SettingSpec describes one setting: where it is edited, who may change it
// and what values it takes. Every value is stored as a JSON string; "" (or
// no row) means the default.
type SettingSpec struct {
	Key     string
	Group   string
	Kind    string
	Perm    string
	Default string
	Min     int64 // KindInt
	Max     int64 // KindInt
	Options []string
	MaxLen  int // KindText, in characters
	Help    string
}

// SettingSpecs lists every setting in the order the dashboard shows them.
// Bot texts (texts.<lang>.<key>) are settings too, checked by the bot's
// catalog instead of a spec.
var SettingSpecs = []SettingSpec{
	{Key: "general.timezone", Group: "general", Kind: KindTZ, Perm: permSettings,
		Help: "time zone of the dates the bot shows, e.g. Asia/Tehran (empty = the server's BOBRES_TIMEZONE)"},
	{Key: "maintenance.enabled", Group: "maintenance", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "true = customers only get the maintenance message; staff and payments in progress still work"},
	{Key: "join.channel", Group: "join", Kind: KindChannel, Perm: permSettings,
		Help: "channel customers must join to use the bot: @name or -100… id (the bot must be its admin; empty = off)"},
	{Key: "join.link", Group: "join", Kind: KindURL, Perm: permSettings,
		Help: "https://t.me/… link of the join button (needed for a private channel)"},
	{Key: "notify.new_payment", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "message staff when a payment needs review"},
	{Key: "notify.new_ticket", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "message staff when a customer writes to support"},
	{Key: "notify.provision_failed", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "message staff when a service could not be created on the panel"},
	{Key: "notify.sales", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "message staff on every paid order"},
	{Key: "notify.recipients", Group: "notify", Kind: KindEnum, Perm: permSettings, Default: "owner",
		Options: []string{"owner", "staff"}, Help: "owner = the server's admin Telegram account; staff = every owner and admin"},
	{Key: "limits.bot_per_10s", Group: "limits", Kind: KindInt, Perm: permSettings, Default: "20", Min: 5, Max: 200,
		Help: "messages and taps a customer may send in 10 seconds"},
	{Key: "limits.tickets_per_day", Group: "limits", Kind: KindInt, Perm: permSettings, Default: "10", Min: 0, Max: 100,
		Help: "support messages a customer may send in a day (0 = no limit)"},
	{Key: "payments.card_number", Group: "payments", Kind: KindText, Perm: permGateways, MaxLen: 40,
		Help: "card number shown for card-to-card payments (16 digits, or an IR sheba)"},
	{Key: "payments.card_holder", Group: "payments", Kind: KindText, Perm: permGateways, MaxLen: 64,
		Help: "card holder name"},
	{Key: "payments.usdt_trc20", Group: "payments", Kind: KindText, Perm: permGateways, MaxLen: 34,
		Help: "USDT TRC20 deposit address"},
	{Key: "payments.usdt_erc20", Group: "payments", Kind: KindText, Perm: permGateways, MaxLen: 42,
		Help: "USDT ERC20 deposit address"},
	{Key: "payments.usdt_rate", Group: "payments", Kind: KindInt, Perm: permGateways, Min: 1, Max: 1_000_000_000_000,
		Help: "Toman per 1 USDT, to quote Toman prices in USDT"},
	{Key: "payments.stars_rate", Group: "payments", Kind: KindInt, Perm: permGateways, Min: 1, Max: 1_000_000_000,
		Help: "Toman per Telegram Star, to price plans in Stars (empty = no Stars)"},
	{Key: "payments.zarinpal_link", Group: "payments", Kind: KindURL, Perm: permGateways,
		Help: "your Zarinpal payment link (https://zarinp.al/...); customers pay there and send the receipt screenshot for approval"},
	{Key: "referral.reward_percent", Group: "referral", Kind: KindInt, Perm: permDiscounts, Min: 0, Max: 100,
		Help: "percent of an invited user's first purchase credited to the inviter's wallet (empty or 0 = no referral program)"},
	{Key: "branding.name", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: 64,
		Help: "store name shown to users"},
	{Key: "branding.support", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: 64,
		Help: "support contact (e.g. @support)"},
	{Key: "branding.color", Group: "branding", Kind: KindColor, Perm: permBranding,
		Help: "brand colour of the dashboard, #rrggbb"},
	{Key: "branding.terms_url", Group: "branding", Kind: KindURL, Perm: permBranding,
		Help: "https link to the store's terms"},
	{Key: "branding.privacy_url", Group: "branding", Kind: KindURL, Perm: permBranding,
		Help: "https link to the store's privacy policy"},
	{Key: "branding.currency.fa", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: 16,
		Help: "Persian name of the Toman in prices (empty = تومان)"},
	{Key: "branding.currency.en", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: 16,
		Help: "English name of the Toman in prices (empty = Toman)"},
}

// SettingKeys maps each setting to its help text (the bot's /set list).
var SettingKeys = func() map[string]string {
	m := make(map[string]string, len(SettingSpecs))
	for _, sp := range SettingSpecs {
		m[sp.Key] = sp.Help
	}
	return m
}()

// SpecOf finds a setting's spec.
func SpecOf(key string) (SettingSpec, bool) {
	for _, sp := range SettingSpecs {
		if sp.Key == key {
			return sp, true
		}
	}
	return SettingSpec{}, false
}

// SettingPerm is the dashboard permission needed to change key; "" when key
// is not a setting.
func SettingPerm(key string) string {
	if sp, ok := SpecOf(key); ok {
		return sp.Perm
	}
	if lang, k, ok := i18n.SplitTextKey(key); ok && slices.Contains(i18n.Languages, lang) {
		return TextPerm(k)
	}
	return ""
}

// TextPerm is the permission needed to change a bot text: the payment
// screens (where customers are told where to send money) are the owner's,
// like the payment details themselves; "" for an unknown text.
func TextPerm(key string) string {
	info, ok := Texts().Info(key)
	switch {
	case !ok:
		return ""
	case info.Group == "pay":
		return permGateways
	}
	return permBranding
}

// Texts is the bot's text catalog, which core uses to check overrides.
var Texts = sync.OnceValue(i18n.MustLoad)

const maxSettingLen = 1000

// FieldError is a value a setting does not take. It is an ErrInvalid.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return ErrInvalid.Error() + ": " + e.Field + ": " + e.Msg }
func (e *FieldError) Unwrap() error { return ErrInvalid }

// TextError is a bot text that would break the messages it is used in.
type TextError struct {
	Lang, Key string
	Problems  []i18n.Problem
}

func (e *TextError) Error() string {
	codes := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		codes[i] = p.Code
		if p.Arg != "" {
			codes[i] += " (" + p.Arg + ")"
		}
	}
	return ErrInvalid.Error() + ": text " + e.Lang + "." + e.Key + ": " + strings.Join(codes, ", ")
}
func (e *TextError) Unwrap() error { return ErrInvalid }

var (
	channelRe = regexp.MustCompile(`^(@[A-Za-z][A-Za-z0-9_]{4,31}|-100[0-9]{5,15})$`)
	colorRe   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	trc20Re   = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
	erc20Re   = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	cardRe    = regexp.MustCompile(`^([0-9]{16}|IR[0-9]{24})$`)
)

// latinDigits turns Persian and Arabic digits into ASCII ones.
func latinDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '۰' && r <= '۹':
			return '0' + r - '۰'
		case r >= '٠' && r <= '٩':
			return '0' + r - '٠'
		}
		return r
	}, s)
}

// NormalizeSetting checks value for the setting key and returns what is
// stored. Bot texts get the catalog's checks (a *TextError).
func NormalizeSetting(key, value string) (string, error) {
	if lang, k, ok := i18n.SplitTextKey(key); ok {
		if !slices.Contains(i18n.Languages, lang) {
			return "", &FieldError{key, "unknown language"}
		}
		v, probs := Texts().ValidateOverride(lang, k, value)
		if len(probs) > 0 {
			if probs[0].Code == i18n.ProblemUnknownKey {
				return "", &FieldError{key, "unknown setting"}
			}
			return "", &TextError{Lang: lang, Key: k, Problems: probs}
		}
		return v, nil
	}
	sp, ok := SpecOf(key)
	if !ok {
		return "", &FieldError{key, "unknown setting"}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	bad := func(msg string) (string, error) { return "", &FieldError{key, msg} }
	if !utf8.ValidString(value) || len(value) > maxSettingLen {
		return bad("too long")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return bad("must be one line of text")
		}
	}
	switch sp.Kind {
	case KindText:
		if utf8.RuneCountInString(value) > sp.MaxLen {
			return bad("at most " + strconv.Itoa(sp.MaxLen) + " characters")
		}
		value = strings.Join(strings.Fields(value), " ")
		switch key {
		case "branding.currency.fa", "branding.currency.en":
			// It goes into every price, also inside HTML messages and buttons.
			if strings.ContainsAny(value, "<>&") {
				return bad("may not contain <, > or &")
			}
		case "payments.card_number":
			value = latinDigits(value)
			if !cardRe.MatchString(strings.NewReplacer(" ", "", "-", "").Replace(strings.ToUpper(value))) {
				return bad("a card number has 16 digits (spaces and dashes are fine), a sheba is IR and 24 digits")
			}
		case "payments.usdt_trc20":
			if !trc20Re.MatchString(value) {
				return bad("not a TRC20 address (T followed by 33 letters and digits)")
			}
		case "payments.usdt_erc20":
			if !erc20Re.MatchString(value) {
				return bad("not an ERC20 address (0x followed by 40 hex digits)")
			}
		}
	case KindInt:
		digits := strings.NewReplacer(",", "", "_", "", " ", "", "٬", "", "،", "").Replace(latinDigits(value))
		n, err := strconv.ParseInt(digits, 10, 64)
		if err != nil || n < sp.Min || n > sp.Max {
			return bad("a whole number from " + strconv.FormatInt(sp.Min, 10) + " to " + strconv.FormatInt(sp.Max, 10))
		}
		value = strconv.FormatInt(n, 10)
	case KindBool:
		switch strings.ToLower(value) {
		case "true", "yes", "on", "1":
			value = "true"
		case "false", "no", "off", "0":
			value = "false"
		default:
			return bad("true or false")
		}
	case KindEnum:
		if !slices.Contains(sp.Options, value) {
			return bad("one of " + strings.Join(sp.Options, ", "))
		}
	case KindTZ:
		if value == "Local" {
			return bad("name a time zone, e.g. Asia/Tehran")
		}
		if _, err := time.LoadLocation(value); err != nil {
			return bad("unknown time zone; use a name such as Asia/Tehran or Europe/Berlin")
		}
	case KindURL:
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return bad("must be an https:// link")
		}
		if key == "join.link" && u.Host != "t.me" && u.Host != "telegram.me" {
			return bad("must be a https://t.me/… link")
		}
	case KindChannel:
		if !channelRe.MatchString(value) {
			return bad("a public channel's @name, or a private channel's id starting with -100")
		}
	case KindColor:
		if !colorRe.MatchString(value) {
			return bad("a colour like #14b8a6")
		}
		value = strings.ToLower(value)
	}
	return value, nil
}

// checkTogether checks settings that depend on each other, given the
// values they will have after a change.
func checkTogether(final map[string]string) error {
	if ch := final["join.channel"]; strings.HasPrefix(ch, "-100") && final["join.link"] == "" {
		return &FieldError{"join.link", "a private channel needs its invite link"}
	}
	return nil
}

// SettingChange is one changed setting in an audit entry.
type SettingChange struct {
	Key, Before, After string
}

// AdminSetSetting changes one setting from the bot. Payment details are the
// owner's: an admin may not redirect customers' money.
func (s *Service) AdminSetSetting(ctx context.Context, actorTelegramID int64, key, value string) error {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return err
	}
	if SettingPerm(key) == permGateways && actor.Role != "owner" {
		return &FieldError{key, "only the store owner can change payment details"}
	}
	return s.SetSetting(ctx, actor, key, value)
}

// SetSetting changes one setting for an actor the caller has checked.
func (s *Service) SetSetting(ctx context.Context, actor *store.User, key, value string) error {
	_, err := s.SetSettings(ctx, actor, map[string]string{key: value})
	return err
}

// SetSettings checks every value, then writes them in one transaction with
// one audit entry per setting that changed (with its value before). A value
// of "" removes the setting, so its default applies. The caller has checked
// that the actor may change these keys (SettingPerm).
func (s *Service) SetSettings(ctx context.Context, actor *store.User, values map[string]string) ([]SettingChange, error) {
	if len(values) == 0 {
		return nil, nil
	}
	norm := make(map[string]string, len(values))
	keys := make([]string, 0, len(values))
	for k, v := range values {
		nv, err := NormalizeSetting(k, v)
		if err != nil {
			return nil, err
		}
		norm[k] = nv
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var changes []SettingChange
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		changes = nil
		current, err := s.st.LockSettings(ctx, tx, append(slices.Clone(keys), "join.channel", "join.link"))
		if err != nil {
			return err
		}
		final := map[string]string{}
		for k, raw := range current {
			var v string
			if json.Unmarshal(raw, &v) == nil {
				final[k] = v
			}
		}
		before := make(map[string]string, len(keys))
		for _, k := range keys {
			before[k] = final[k]
			final[k] = norm[k]
		}
		if err := checkTogether(final); err != nil {
			return err
		}
		for _, k := range keys {
			if before[k] == norm[k] {
				continue
			}
			if norm[k] == "" {
				err = s.st.DeleteSetting(ctx, tx, k)
			} else {
				raw, _ := json.Marshal(norm[k])
				err = s.st.SetSetting(ctx, tx, k, raw)
			}
			if err != nil {
				return err
			}
			action, entity, entityID := "setting.set", "settings", k
			if lang, tk, ok := i18n.SplitTextKey(k); ok {
				action, entity, entityID = "text.set", "text", lang+"."+tk
				if norm[k] == "" {
					action = "text.reset"
				}
			}
			if err := s.auditChange(ctx, tx, actor, action, entity, entityID,
				map[string]string{"value": before[k]}, map[string]string{"value": norm[k]}, ""); err != nil {
				return err
			}
			changes = append(changes, SettingChange{Key: k, Before: before[k], After: norm[k]})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return changes, nil
}

// ImportTexts replaces the overrides of many texts of one language at once:
// all are checked first and nothing is written when one is wrong (a
// *TextsError lists every problem). Texts left out keep their override.
func (s *Service) ImportTexts(ctx context.Context, actor *store.User, lang string, texts map[string]string) (int, error) {
	if !slices.Contains(i18n.Languages, lang) {
		return 0, invalid("unknown language %q", lang)
	}
	var all []*TextError
	values := make(map[string]string, len(texts))
	for k, v := range texts {
		key := "texts." + lang + "." + k
		nv, err := NormalizeSetting(key, v)
		var te *TextError
		switch {
		case errors.As(err, &te):
			all = append(all, te)
			continue
		case err != nil:
			all = append(all, &TextError{Lang: lang, Key: k, Problems: []i18n.Problem{{Code: i18n.ProblemUnknownKey, Arg: k}}})
			continue
		}
		values[key] = nv
	}
	if len(all) > 0 {
		slices.SortFunc(all, func(a, b *TextError) int { return strings.Compare(a.Key, b.Key) })
		return 0, &TextsError{Texts: all}
	}
	var changed []string
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		changed = nil
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		current, err := s.st.LockSettings(ctx, tx, keys)
		if err != nil {
			return err
		}
		for _, k := range keys {
			var old string
			_ = json.Unmarshal(current[k], &old)
			if old == values[k] {
				continue
			}
			if values[k] == "" {
				err = s.st.DeleteSetting(ctx, tx, k)
			} else {
				raw, _ := json.Marshal(values[k])
				err = s.st.SetSetting(ctx, tx, k, raw)
			}
			if err != nil {
				return err
			}
			_, tk, _ := i18n.SplitTextKey(k)
			changed = append(changed, tk)
		}
		if len(changed) == 0 {
			return nil
		}
		return s.auditChange(ctx, tx, actor, "text.import", "text", lang, nil,
			map[string]any{"lang": lang, "keys": changed}, "")
	})
	if err != nil {
		return 0, err
	}
	return len(changed), nil
}

// TextsError lists the texts of an import that cannot be used.
type TextsError struct{ Texts []*TextError }

func (e *TextsError) Error() string {
	return ErrInvalid.Error() + ": " + strconv.Itoa(len(e.Texts)) + " texts cannot be used"
}
func (e *TextsError) Unwrap() error { return ErrInvalid }

// Settings reads every setting as a string (unset ones are missing).
func (s *Service) Settings(ctx context.Context) (map[string]string, error) {
	raw, err := s.st.GetSettings(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, r := range raw {
		var v string
		if json.Unmarshal(r, &v) == nil {
			out[k] = v
		}
	}
	return out, nil
}
