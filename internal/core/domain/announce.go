package domain

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// New product announcements (docs/04-dashboard.md, "Announcing a product"; docs/03-bot-ux.md, "Product news"). Staff announce one product
// (a VPN plan, a supplier's digital account, a BOBRES license the store sells) by hand from its page in the dashboard: a card with its name,
// kind, price and what it gives, and a 🛒 Buy button that opens that product in the bot. It goes to the store's channel (a link button:
// t.me/<bot>?start=<payload>) and/or in private messages to the customers who said yes to product news (a callback button), in each
// customer's language.
//
// The private messages are a broadcast of the segment announce, sent by the broadcast machinery (the same queue, pace, counts, pause and
// cancel); the channel post is made by the bot when core asks (core has no internet access). Nothing is automatic, and the store's limits
// hold for every send: at most announce.daily_max announcements a store day, none in the quiet hours (one sent then waits queued until they
// end), and never the same product twice within 24 hours unless staff force it. The Buy button checks the product when it is tapped (an old
// card is never edited): a product sold out or switched off since says so.

// The settings (SettingSpecs) and their defaults.
const (
	SettingAnnounceEnabled      = "announce.enabled"
	SettingAnnounceChannel      = "announce.channel"
	SettingAnnounceChannelLangs = "announce.channel_langs"
	SettingAnnounceDailyMax     = "announce.daily_max"
	SettingAnnounceQuietStart   = "announce.quiet_start"
	SettingAnnounceQuietEnd     = "announce.quiet_end"

	AnnounceLangsDefault = "default"
	AnnounceLangsBoth    = "both"

	DefaultAnnounceDailyMax   = 3
	DefaultAnnounceQuietStart = "23:00"
	DefaultAnnounceQuietEnd   = "08:00"
)

// Product kinds an announcement is about.
const (
	AnnouncePlan     = "plan"
	AnnounceSupplier = "supplier"
	AnnounceLicense  = "license"
)

// AnnounceKinds are the kinds of product that can be announced.
var AnnounceKinds = []string{AnnouncePlan, AnnounceSupplier, AnnounceLicense}

// AnnouncePlaceholders are the values a card may use, as {name}.
var AnnouncePlaceholders = []string{"name", "kind", "price", "days", "traffic", "stock", "store"}

const (
	// announceRepeat is how long the same product is not announced again unless forced.
	announceRepeat = 24 * time.Hour
	// announceValueRoom is what each placeholder may grow a text by (a name is clipped to announceNameMax).
	announceValueRoom = 160
	announceNameMax   = 120
	// AnnounceBuyText is the bot text of the Buy button.
	AnnounceBuyText = "btn.an_buy"
	// channelPostGiveUp is how long a channel post the bot did not answer for is tried again.
	channelPostGiveUp = time.Hour
	// announceSeparator stands between the languages of a channel post in both languages.
	announceSeparator = "\n\n➖➖➖\n\n"
)

// AnnounceRepeatError refuses to announce a product announced less than 24 hours ago (unless forced): the dashboard asks whether to send anyway.
type AnnounceRepeatError struct{ Last time.Time }

func (e *AnnounceRepeatError) Error() string {
	return ErrState.Error() + ": this product was announced less than 24 hours ago"
}
func (e *AnnounceRepeatError) Unwrap() error { return ErrState }

// AnnounceLimitError refuses an announcement past the store's daily limit (for the store day it would go out on).
type AnnounceLimitError struct {
	Max int
	Day string // yyyy-mm-dd, the store's day
}

func (e *AnnounceLimitError) Error() string {
	return fmt.Sprintf("%s: the store's limit of %d announcements a day is reached for %s", ErrState.Error(), e.Max, e.Day)
}
func (e *AnnounceLimitError) Unwrap() error { return ErrState }

// AnnounceProduct is a product as a card shows it, read now.
type AnnounceProduct struct {
	Kind, ID string
	Names    map[string]string // per language
	Kinds    map[string]string // the kind or category, per language
	Currency string            // IRT or USDT
	Price    int64             // in minor units of Currency
	Toman    int64             // what a price in another currency costs in Toman now (0: unknown, or the price is in Toman)
	Days     *int32            // a plan's duration (nil or 0: no time limit); a license's term in months is Months
	Months   int
	Traffic  *int64 // a plan's traffic (nil or 0: unlimited)
	Stock    *int32 // a digital account's stock, when the supplier says
	Start    string // the payload of the bot's start link that opens it (t.me/<bot>?start=<Start>)
	Callback string // the callback data of the Buy button in a private chat
}

// announceWords are the words of the values in each language (the other languages read the English ones).
var announceWords = map[string]map[string]string{
	"fa": {"plan": "سرویس VPN", "supplier": "اکانت دیجیتال", "license": "لایسنس BOBRES", "days": "{n} روز", "no_time": "بدون محدودیت زمانی",
		"unlimited": "نامحدود", "month": "{n} ماه", "standard": "استاندارد", "pro": "پرو", "none": "—"},
	"en": {"plan": "VPN plan", "supplier": "Digital account", "license": "BOBRES license", "days": "{n} days", "no_time": "No time limit",
		"unlimited": "Unlimited", "month": "{n} months", "standard": "Standard", "pro": "Pro", "none": "—"},
}

func announceWord(lang, key string) string {
	if w, ok := announceWords[lang]; ok && w[key] != "" {
		return w[key]
	}
	return announceWords["en"][key]
}

// AnnounceEnabled reports whether announcements are on (announce.enabled).
func (s *Service) AnnounceEnabled(ctx context.Context) bool {
	return s.setting(ctx, SettingAnnounceEnabled) == "true"
}

// AnnounceConfig is the announcement settings, with their defaults.
type AnnounceConfig struct {
	Enabled      bool
	Channel      string // the channel posts go to ("" none: neither announce.channel nor join.channel is set)
	ChannelLangs string
	DailyMax     int
	QuietStart   int // minutes since midnight, store time
	QuietEnd     int
	Location     *time.Location
}

func (s *Service) announceConfig(set map[string]string) AnnounceConfig {
	c := AnnounceConfig{Enabled: set[SettingAnnounceEnabled] == "true", Channel: set[SettingAnnounceChannel], ChannelLangs: set[SettingAnnounceChannelLangs],
		DailyMax: DefaultAnnounceDailyMax, Location: s.storeLocation(set)}
	if c.Channel == "" {
		c.Channel = set["join.channel"]
	}
	if c.ChannelLangs != AnnounceLangsBoth {
		c.ChannelLangs = AnnounceLangsDefault
	}
	if n, err := strconv.Atoi(set[SettingAnnounceDailyMax]); err == nil && n >= 1 && n <= 50 {
		c.DailyMax = n
	}
	c.QuietStart, _ = clockMinutes(DefaultAnnounceQuietStart)
	c.QuietEnd, _ = clockMinutes(DefaultAnnounceQuietEnd)
	if m, ok := clockMinutes(set[SettingAnnounceQuietStart]); ok {
		c.QuietStart = m
	}
	if m, ok := clockMinutes(set[SettingAnnounceQuietEnd]); ok {
		c.QuietEnd = m
	}
	return c
}

// AnnounceSettings reads the announcement settings.
func (s *Service) AnnounceSettings(ctx context.Context) (AnnounceConfig, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return AnnounceConfig{}, err
	}
	return s.announceConfig(set), nil
}

// QuietUntil is when the quiet hours around t end (t itself when t is outside them).
func (c AnnounceConfig) QuietUntil(t time.Time) time.Time {
	if c.QuietStart == c.QuietEnd {
		return t
	}
	l := t.In(c.Location)
	m := l.Hour()*60 + l.Minute()
	at := func(dayOffset int) time.Time {
		return time.Date(l.Year(), l.Month(), l.Day()+dayOffset, c.QuietEnd/60, c.QuietEnd%60, 0, 0, c.Location)
	}
	if c.QuietStart < c.QuietEnd { // within one day, e.g. 01:00 to 07:00
		if m >= c.QuietStart && m < c.QuietEnd {
			return at(0)
		}
		return t
	}
	switch { // over midnight, e.g. 23:00 to 08:00
	case m >= c.QuietStart:
		return at(1)
	case m < c.QuietEnd:
		return at(0)
	}
	return t
}

// storeDay is the store's day t falls on: its first instant, the next day's and its date.
func (c AnnounceConfig) storeDay(t time.Time) (from, to time.Time, day string) {
	l := t.In(c.Location)
	from = time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, c.Location)
	to = time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, c.Location)
	return from, to, from.Format(time.DateOnly)
}

// DefaultLanguage is the store's default language: the language of a channel post that is not in both languages.
func DefaultLanguage() string { return i18n.Languages[0] }

// AnnounceProductOf reads a product as a card shows it now. A product that cannot be bought now (switched off, deleted, a trial or a traffic
// package, a supplier product without a price, license sales off) is refused with a *FieldError.
func (s *Service) AnnounceProductOf(ctx context.Context, kind, id string) (*AnnounceProduct, error) {
	id = strings.TrimSpace(id)
	switch kind {
	case AnnouncePlan:
		return s.announcePlan(ctx, id)
	case AnnounceSupplier:
		return s.announceSupplier(ctx, id)
	case AnnounceLicense:
		return s.announceLicense(ctx, id)
	}
	return nil, &FieldError{"kind", "plan, supplier or license"}
}

func (s *Service) announcePlan(ctx context.Context, id string) (*AnnounceProduct, error) {
	u, err := buuid.Parse(id)
	if err != nil {
		return nil, &FieldError{"id", "not a plan"}
	}
	p, err := s.st.GetPlan(ctx, s.st.Conn(), u.String())
	if errors.Is(err, store.ErrNotFound) {
		return nil, &FieldError{"id", "not a plan"}
	} else if err != nil {
		return nil, err
	}
	switch {
	case p.DeletedAt != nil:
		return nil, &FieldError{"id", "this plan was deleted"}
	case p.IsTrial || p.IsTopup:
		return nil, &FieldError{"id", "a free trial or a traffic package cannot be announced: only a plan customers buy"}
	case !p.Enabled:
		return nil, &FieldError{"id", "this plan is switched off: switch it on first"}
	}
	hexID := strings.ReplaceAll(p.ID, "-", "")
	out := &AnnounceProduct{Kind: AnnouncePlan, ID: p.ID, Names: map[string]string{}, Kinds: map[string]string{}, Currency: p.Currency,
		Price: p.Price, Days: p.DurationDays, Traffic: p.TrafficBytes, Start: "p_" + hexID, Callback: "an:b:p:" + hexID}
	for _, l := range i18n.Languages {
		out.Names[l] = i18nPick(p.NameI18n, l)
		out.Kinds[l] = announceWord(l, "plan")
	}
	if p.Currency != tomanCurrency && p.Currency == "USDT" {
		if toman, _, err := s.TomanForUsdt(ctx, p.Price); err == nil {
			out.Toman = toman
		}
	}
	return out, nil
}

// i18nPick is a text in lang, else in another language.
func i18nPick(names map[string]string, lang string) string {
	if v := names[lang]; v != "" {
		return v
	}
	for _, l := range i18n.Languages {
		if v := names[l]; v != "" {
			return v
		}
	}
	return ""
}

func (s *Service) announceSupplier(ctx context.Context, id string) (*AnnounceProduct, error) {
	if !s.SupplierOn(ctx) {
		return nil, &FieldError{"id", "digital accounts are not on sale: switch them on first"}
	}
	list, err := s.ListSupplierProducts(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(list, func(p store.SupplierProduct) bool { return p.ID == id })
	if i < 0 {
		return nil, &FieldError{"id", "not a product of the supplier's catalog"}
	}
	p := &list[i]
	if !p.Enabled || p.SellPrice == nil || !p.Listed {
		return nil, &FieldError{"id", "this product is not on sale: give it a price and switch it on first"}
	}
	sid := SupplierSID(p.ID)
	out := &AnnounceProduct{Kind: AnnounceSupplier, ID: p.ID, Names: map[string]string{}, Kinds: map[string]string{}, Currency: tomanCurrency,
		Price: *p.SellPrice, Stock: p.Stock, Start: "s_" + sid, Callback: "an:b:s:" + sid}
	cat := SupplierCategory(p)
	for _, l := range i18n.Languages {
		out.Names[l] = SupplierName(p, l)
		out.Kinds[l] = announceWord(l, "supplier")
		if cat != "" {
			out.Kinds[l] = cat
		}
	}
	return out, nil
}

// licenseIDRe is a license offer: a tier and a term, tier:months.
var licenseIDRe = regexp.MustCompile(`^(standard|pro):(1|12)$`)

func (s *Service) announceLicense(ctx context.Context, id string) (*AnnounceProduct, error) {
	m := licenseIDRe.FindStringSubmatch(id)
	if m == nil {
		return nil, &FieldError{"id", "a license offer: standard or pro, for 1 or 12 months (standard:12)"}
	}
	if !s.VendorOn(ctx) {
		return nil, &FieldError{"id", "license sales are off: switch them on first"}
	}
	tiers, err := s.VendorTiers()
	if err != nil {
		return nil, err
	}
	months, _ := strconv.Atoi(m[2])
	i := slices.IndexFunc(tiers, func(t VendorTier) bool { return t.Name == m[1] })
	if i < 0 || tiers[i].Price(months) <= 0 {
		return nil, &FieldError{"id", "this license is not for sale"}
	}
	t := &tiers[i]
	price := t.Price(months)
	out := &AnnounceProduct{Kind: AnnounceLicense, ID: id, Names: map[string]string{}, Kinds: map[string]string{}, Currency: t.Currency,
		Price: price, Months: months, Start: "l_" + m[1] + "_" + m[2], Callback: "an:b:l:" + m[1] + ":" + m[2]}
	if t.Currency != tomanCurrency {
		if toman, _, err := s.VendorToman(ctx, t, price); err == nil {
			out.Toman = toman
		}
	}
	for _, l := range i18n.Languages {
		out.Names[l] = "BOBRES " + announceWord(l, m[1]) + " · " + i18n.Digits(l, strings.ReplaceAll(announceWord(l, "month"), "{n}", m[2]))
		out.Kinds[l] = announceWord(l, "license")
	}
	return out, nil
}

// announcePlaceholderRe finds what looks like a placeholder: a word in braces.
var announcePlaceholderRe = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// CheckAnnounceText checks a card in lang: Telegram's HTML, short enough for the values it gets, and only known placeholders. It returns the
// text normalised; a problem is a *TextError keyed key.
func CheckAnnounceText(lang, key, text string) (string, error) {
	n := len(announcePlaceholderRe.FindAllStringIndex(text, -1))
	v, probs := i18n.ValidateMessage(text, max(i18n.MaxMessage-n*announceValueRoom, 0))
	seen := map[string]bool{}
	for _, m := range announcePlaceholderRe.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(AnnouncePlaceholders, m[1]) && !seen[m[1]] {
			seen[m[1]] = true
			probs = append(probs, i18n.Problem{Code: i18n.ProblemUnknown, Arg: m[0]})
		}
	}
	if len(probs) > 0 {
		return "", &TextError{Lang: lang, Key: key, Problems: probs}
	}
	return v, nil
}

// AnnounceTextProblems is the composer's live check: the problems of a card, none when it can be sent.
func AnnounceTextProblems(lang, text string) (string, []i18n.Problem) {
	v, err := CheckAnnounceText(lang, "texts."+lang, text)
	var te *TextError
	if errors.As(err, &te) {
		return i18n.NormalizeText(text), te.Problems
	}
	return v, nil
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// announceValues are a product's values in lang, escaped for an HTML message.
func announceValues(p *AnnounceProduct, lang, brand string, irt map[string]string) map[string]string {
	price := i18n.MoneyNamed(irt, lang, p.Price, p.Currency)
	if p.Currency != tomanCurrency && p.Toman > 0 {
		price += " ≈ " + i18n.MoneyNamed(irt, lang, p.Toman, tomanCurrency)
	}
	days := announceWord(lang, "no_time")
	switch {
	case p.Months > 0:
		days = i18n.Digits(lang, strings.ReplaceAll(announceWord(lang, "month"), "{n}", strconv.Itoa(p.Months)))
	case p.Days != nil && *p.Days > 0:
		days = strings.ReplaceAll(announceWord(lang, "days"), "{n}", i18n.Number(lang, int64(*p.Days)))
	}
	traffic := announceWord(lang, "unlimited")
	if p.Traffic != nil && *p.Traffic > 0 {
		traffic = i18n.Bytes(lang, *p.Traffic)
	}
	stock := announceWord(lang, "none")
	if p.Stock != nil {
		stock = i18n.Number(lang, int64(max(*p.Stock, 0)))
	}
	if brand == "" {
		brand = announceWord(lang, "none")
	}
	name := i18nPick(p.Names, lang)
	kind := i18nPick(p.Kinds, lang)
	return map[string]string{
		"name": html.EscapeString(clipRunes(name, announceNameMax)), "kind": html.EscapeString(clipRunes(kind, 60)), "price": html.EscapeString(price),
		"days": html.EscapeString(days), "traffic": html.EscapeString(traffic), "stock": html.EscapeString(stock),
		"store": html.EscapeString(clipRunes(brand, 64)),
	}
}

// RenderAnnounceText fills a checked card's placeholders with a product's values in lang.
func RenderAnnounceText(text, lang string, p *AnnounceProduct, brand string, irt map[string]string) string {
	vals := announceValues(p, lang, brand, irt)
	return announcePlaceholderRe.ReplaceAllStringFunc(text, func(m string) string {
		if v, ok := vals[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// DefaultAnnounceTexts are the cards a product's announcement starts from, per language: a line for each value the product has.
func DefaultAnnounceTexts(p *AnnounceProduct) map[string]string {
	out := map[string]string{}
	for _, l := range i18n.Languages {
		fa := l == "fa"
		line := func(faText, enText string) string {
			if fa {
				return faText
			}
			return enText
		}
		lines := []string{line("🆕 <b>محصول تازه: {name}</b>", "🆕 <b>New: {name}</b>"), "🏷 {kind}"}
		switch p.Kind {
		case AnnouncePlan:
			lines = append(lines, line("⏳ مدت: {days}", "⏳ Duration: {days}"), line("📶 حجم: {traffic}", "📶 Traffic: {traffic}"))
		case AnnounceLicense:
			lines = append(lines, line("⏳ مدت: {days}", "⏳ Term: {days}"))
		case AnnounceSupplier:
			if p.Stock != nil {
				lines = append(lines, line("📦 موجودی: {stock}", "📦 In stock: {stock}"))
			}
		}
		lines = append(lines, line("💰 قیمت: {price}", "💰 Price: {price}"), "",
			line("برای خرید، دکمهٔ «🛒 خرید» را بزنید.", "Tap 🛒 Buy below to get it."))
		out[l] = strings.Join(lines, "\n")
	}
	return out
}

// irtFromSettings are the store's names for the Toman (branding.currency.<lang>).
func irtFromSettings(set map[string]string) map[string]string {
	out := map[string]string{}
	for _, l := range i18n.Languages {
		if v := set["branding.currency."+l]; v != "" {
			out[l] = v
		}
	}
	return out
}

// brandName is the store's name (branding.name; "" when unset).
func brandName(set map[string]string) string { return set["branding.name"] }

// AnnounceBuyLabel is the label of the Buy button in lang, as the bot shows it (the store's own text when it changed it).
func (s *Service) AnnounceBuyLabel(ctx context.Context, lang string) string {
	lang = i18n.Normalize(lang)
	if set, err := s.Settings(ctx); err == nil {
		if v := set["texts."+lang+"."+AnnounceBuyText]; v != "" {
			return v
		}
	}
	return Texts().T(lang, AnnounceBuyText)
}

// AnnouncePreview is a card written for one language, and the channel post it makes.
type AnnouncePreview struct {
	Text        string // the private message in lang
	Channel     string // the channel post (in the channel's language, or both), "" when the other texts are not given
	ChannelLang string // the channel post's language: a language, or "both"
	BuyLabel    string
	Product     *AnnounceProduct
}

// PreviewAnnouncement checks a card (lang's text, and the others when given) and writes it with the product's values now: the private message
// in lang and the channel post.
func (s *Service) PreviewAnnouncement(ctx context.Context, kind, id, lang string, texts map[string]string) (*AnnouncePreview, error) {
	if !slices.Contains(i18n.Languages, lang) {
		return nil, &FieldError{"lang", "one of " + strings.Join(i18n.Languages, ", ")}
	}
	p, err := s.AnnounceProductOf(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	checked := map[string]string{}
	for l, t := range texts {
		if !slices.Contains(i18n.Languages, l) || strings.TrimSpace(t) == "" {
			continue
		}
		v, err := CheckAnnounceText(l, "texts."+l, t)
		if err != nil {
			return nil, err
		}
		checked[l] = v
	}
	if checked[lang] == "" {
		return nil, &FieldError{"texts." + lang, "write the card in this language"}
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	irt := irtFromSettings(set)
	cfg := s.announceConfig(set)
	out := &AnnouncePreview{Product: p, Text: RenderAnnounceText(checked[lang], lang, p, brandName(set), irt), BuyLabel: s.AnnounceBuyLabel(ctx, lang)}
	out.Channel, out.ChannelLang, _ = channelPost(cfg, checked, p, brandName(set), irt)
	return out, nil
}

// channelPost is the channel post of a card: its text in the store's default language, or in every language one under the other, and its
// language ("both" for every one). ok is false when a language it needs has no text.
func channelPost(cfg AnnounceConfig, texts map[string]string, p *AnnounceProduct, brand string, irt map[string]string) (text, lang string, ok bool) {
	if cfg.ChannelLangs == AnnounceLangsBoth {
		parts := make([]string, 0, len(i18n.Languages))
		for _, l := range i18n.Languages {
			if texts[l] == "" {
				return "", AnnounceLangsBoth, false
			}
			parts = append(parts, RenderAnnounceText(texts[l], l, p, brand, irt))
		}
		return strings.Join(parts, announceSeparator), AnnounceLangsBoth, true
	}
	l := DefaultLanguage()
	if texts[l] == "" {
		return "", l, false
	}
	return RenderAnnounceText(texts[l], l, p, brand, irt), l, true
}

// channelLabel is the Buy button of a channel post: in its language, or every language's label joined.
func (s *Service) channelLabel(ctx context.Context, lang string) string {
	if lang != AnnounceLangsBoth {
		return s.AnnounceBuyLabel(ctx, lang)
	}
	var labels []string
	for _, l := range i18n.Languages {
		if v := s.AnnounceBuyLabel(ctx, l); !slices.Contains(labels, v) {
			labels = append(labels, v)
		}
	}
	label := strings.Join(labels, " | ")
	if _, probs := i18n.ValidateLabel(label); len(probs) > 0 {
		return s.AnnounceBuyLabel(ctx, DefaultLanguage())
	}
	return label
}

// AnnounceInput is an announcement as staff send it.
type AnnounceInput struct {
	Kind, ProductID string
	Texts           map[string]string // the card per language, with placeholders: every language is needed
	Channel         bool
	Private         bool
	Force           bool // also when the product was announced in the last 24 hours
}

// AnnounceResult is a sent announcement: queued for the end of the quiet hours (Queued) or going out now.
type AnnounceResult struct {
	Announcement *store.Announcement
	Queued       bool
	Audience     int64 // the customers who would get the private message now (0 without one)
}

// SendAnnouncement checks an announcement and puts it in line: now, or at the end of the quiet hours. It is refused while announcements are
// off, past the daily limit (*AnnounceLimitError), and for a product announced in the last 24 hours unless forced (*AnnounceRepeatError).
// The caller has checked that the bot may post in the channel.
func (s *Service) SendAnnouncement(ctx context.Context, actor *store.User, in AnnounceInput) (*AnnounceResult, error) {
	if !in.Channel && !in.Private {
		return nil, &FieldError{"targets", "pick the channel, private messages, or both"}
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	cfg := s.announceConfig(set)
	if !cfg.Enabled {
		return nil, stateErr("product announcements are off: switch them on in Settings (Announcements)")
	}
	if in.Channel && cfg.Channel == "" {
		return nil, &FieldError{"channel", "no channel is set: set the announcement channel (or the required channel) in Settings"}
	}
	p, err := s.AnnounceProductOf(ctx, in.Kind, in.ProductID)
	if err != nil {
		return nil, err
	}
	texts := map[string]string{}
	for _, l := range i18n.Languages {
		v, err := CheckAnnounceText(l, "texts."+l, in.Texts[l])
		if err != nil {
			return nil, err
		}
		texts[l] = v
	}
	irt := irtFromSettings(set)
	for _, l := range i18n.Languages { // what goes out must fit in one message
		if _, probs := i18n.ValidateMessage(RenderAnnounceText(texts[l], l, p, brandName(set), irt), i18n.MaxMessage); len(probs) > 0 {
			return nil, &TextError{Lang: l, Key: "texts." + l, Problems: probs}
		}
	}
	if in.Channel {
		post, _, _ := channelPost(cfg, texts, p, brandName(set), irt)
		if _, probs := i18n.ValidateMessage(post, i18n.MaxMessage); len(probs) > 0 {
			return nil, &FieldError{"channel_langs", "the channel post in every language is too long for one message: shorten the texts, or post the default language only"}
		}
	}
	now := s.now()
	due := cfg.QuietUntil(now)
	res := &AnnounceResult{Queued: due.After(now)}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.LockAnnouncements(ctx, tx); err != nil {
			return err
		}
		from, to, day := cfg.storeDay(due)
		n, err := s.st.CountAnnouncementsDue(ctx, tx, from, to)
		if err != nil {
			return err
		}
		if n >= cfg.DailyMax {
			return &AnnounceLimitError{Max: cfg.DailyMax, Day: day}
		}
		last, err := s.st.LastAnnouncementDue(ctx, tx, p.Kind, p.ID)
		if err != nil {
			return err
		}
		if last != nil && due.Sub(*last) < announceRepeat && !in.Force {
			return &AnnounceRepeatError{Last: *last}
		}
		a := &store.Announcement{ProductKind: p.Kind, ProductID: p.ID, ProductName: i18nPick(p.Names, DefaultLanguage()), Texts: texts,
			ToChannel: in.Channel, ToPrivate: in.Private, ChannelLangs: cfg.ChannelLangs, Forced: in.Force && last != nil && due.Sub(*last) < announceRepeat,
			DueAt: due, CreatedBy: actor.ID}
		if err := s.st.InsertAnnouncement(ctx, tx, a); err != nil {
			return err
		}
		if in.Private {
			if res.Audience, _, _, err = s.st.AudienceCount(ctx, tx, store.SegmentAnnounce, ""); err != nil {
				return err
			}
		}
		res.Announcement = a
		after := announcementAudit(a)
		after["audience_now"], after["queued"] = res.Audience, res.Queued
		if in.Channel {
			after["channel"] = cfg.Channel
		}
		return s.audit(ctx, tx, actor, "announcement.send", "announcement", a.ID, after, "")
	})
	if err != nil {
		return nil, err
	}
	if !res.Queued {
		s.KickAnnouncements()
	}
	return res, nil
}

func announcementAudit(a *store.Announcement) map[string]any {
	return map[string]any{"product_kind": a.ProductKind, "product_id": a.ProductID, "product": a.ProductName, "texts": a.Texts,
		"to_channel": a.ToChannel, "to_private": a.ToPrivate, "channel_langs": a.ChannelLangs, "forced": a.Forced, "due_at": a.DueAt.Unix()}
}

// CancelAnnouncement cancels an announcement still waiting for the end of the quiet hours.
func (s *Service) CancelAnnouncement(ctx context.Context, actor *store.User, id string) (*store.Announcement, error) {
	if _, err := buuid.Parse(id); err != nil {
		return nil, store.ErrNotFound
	}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		a, err := s.st.GetAnnouncement(ctx, tx, id, true)
		if err != nil {
			return err
		}
		ok, err := s.st.CancelAnnouncement(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			return stateErr("only an announcement that is still waiting can be cancelled")
		}
		return s.auditChange(ctx, tx, actor, "announcement.cancel", "announcement", id, map[string]string{"status": a.Status},
			map[string]string{"status": "cancelled"}, "")
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetAnnouncement(ctx, s.st.Conn(), id, false)
}

// Announcements lists announcements, newest first (of one product, of a kind, or all).
func (s *Service) Announcements(ctx context.Context, kind, productID string, p store.Page) ([]store.Announcement, int64, error) {
	return s.st.ListAnnouncements(ctx, s.st.Conn(), kind, productID, p)
}

// AnnounceAudience counts who the private message would reach now: every language, and per language.
func (s *Service) AnnounceAudience(ctx context.Context) (total, fa, en int64, err error) {
	return s.st.AudienceCount(ctx, s.st.Conn(), store.SegmentAnnounce, "")
}

// AskAnnounce reports whether the bot asks the customer now whether they want news of new products, and records that it does: once per
// customer, and only while announcements are on.
func (s *Service) AskAnnounce(ctx context.Context, telegramID int64) (bool, error) {
	if telegramID <= 0 {
		return false, invalid("telegram id required")
	}
	if !s.AnnounceEnabled(ctx) {
		return false, nil
	}
	return s.st.ClaimAnnounceAsk(ctx, s.st.Conn(), telegramID)
}

// SetAnnounceOptIn stores a customer's answer about product news (by user id, or by Telegram id when userID is "").
func (s *Service) SetAnnounceOptIn(ctx context.Context, userID string, telegramID int64, optIn bool) (*store.User, error) {
	if userID == "" && telegramID <= 0 {
		return nil, invalid("user required")
	}
	if userID != "" {
		if _, err := buuid.Parse(userID); err != nil {
			return nil, store.ErrNotFound
		}
	}
	return s.st.SetAnnounceOptIn(ctx, s.st.Conn(), userID, telegramID, optIn)
}

// ChannelPost is a card posted in a channel by the bot: its text, the Buy button's label and the start payload of its link.
type ChannelPost struct {
	Chat, Text, Label, Start string
}

// ChannelPostResult is how a channel post went: OK with the message's id, or Telegram's refusal (Problem, Detail).
type ChannelPostResult struct {
	OK        bool
	MessageID int64
	Problem   string
	Detail    string
}

// AnnounceTransport makes the channel posts (the bot, in production: core has no Telegram token).
type AnnounceTransport interface {
	PostChannel(ctx context.Context, p ChannelPost) (*ChannelPostResult, error)
}

// SetAnnounceTransport sets the way to the channel (the bot, in cmd/core).
func (s *Service) SetAnnounceTransport(tr AnnounceTransport) { s.announce = tr }

// KickAnnouncements wakes the announcement worker (an announcement to send now).
func (s *Service) KickAnnouncements() {
	select {
	case s.akick <- struct{}{}:
	default:
	}
}

// AnnouncementWorker sends the announcements whose time has come: it writes each card with the product's values now, puts the private
// messages in line as a broadcast and has the bot post in the channel.
type AnnouncementWorker struct {
	s     *Service
	log   *slog.Logger
	every time.Duration
}

// NewAnnouncementWorker builds the worker (every 15 seconds, and when kicked).
func (s *Service) NewAnnouncementWorker(log *slog.Logger) *AnnouncementWorker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &AnnouncementWorker{s: s, log: log, every: 15 * time.Second}
}

// Run works until ctx ends.
func (w *AnnouncementWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		if err := w.Step(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("announcement worker", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-w.s.akick:
		}
	}
}

// Step starts the announcements that are due and makes the channel posts that wait.
func (w *AnnouncementWorker) Step(ctx context.Context) error {
	due, err := w.s.st.DueAnnouncements(ctx, w.s.st.Conn(), w.s.now())
	if err != nil {
		return err
	}
	for _, id := range due {
		if err := w.s.startAnnouncement(ctx, id); err != nil {
			return err
		}
	}
	return w.s.postChannels(ctx)
}

// startAnnouncement sends one due announcement: the private messages go in line as a broadcast with each language's card written now; the
// channel post waits for postChannels. A product that cannot be bought any more cancels it.
func (s *Service) startAnnouncement(ctx context.Context, id string) error {
	set, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	irt := irtFromSettings(set)
	cfg := s.announceConfig(set)
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		a, err := s.st.GetAnnouncement(ctx, tx, id, true)
		if err != nil || a.Status != "queued" {
			return err
		}
		p, err := s.AnnounceProductOf(ctx, a.ProductKind, a.ProductID)
		var fe *FieldError
		if errors.As(err, &fe) || (err == nil && !cfg.Enabled) {
			reason := "announcements were switched off"
			if fe != nil {
				reason = fe.Msg
			}
			if _, err := s.st.CancelAnnouncement(ctx, tx, id); err != nil {
				return err
			}
			return s.audit(ctx, tx, nil, "announcement.cancel", "announcement", id, map[string]string{"status": "cancelled", "reason": reason}, "")
		}
		if err != nil {
			return err
		}
		var broadcastID, chat string
		if a.ToPrivate {
			b := &store.Broadcast{Segment: store.SegmentAnnounce, Texts: map[string]string{}, BuyCallback: p.Callback, CreatedBy: a.CreatedBy, Kind: "manual"}
			for _, l := range i18n.Languages {
				if t := a.Texts[l]; t != "" {
					b.Texts[l] = RenderAnnounceText(t, l, p, brandName(set), irt)
				}
			}
			if err := s.queueResellerBroadcast(ctx, tx, b); err != nil {
				return err
			}
			broadcastID = b.ID
		}
		if a.ToChannel {
			chat = cfg.Channel
			if chat == "" {
				if err := s.st.SetAnnouncementChannel(ctx, tx, id, "failed", 0, "no channel is set"); err != nil {
					return err
				}
			}
		}
		if _, err := s.st.StartAnnouncement(ctx, tx, id, broadcastID, chat); err != nil {
			return err
		}
		if broadcastID != "" {
			s.KickBroadcasts()
		}
		return s.audit(ctx, tx, nil, "announcement.start", "announcement", id, map[string]any{"broadcast_id": broadcastID, "channel": chat}, "")
	})
}

// postChannels has the bot post the cards whose channel post waits. A bot that does not answer is tried again at the next step for an hour.
func (s *Service) postChannels(ctx context.Context) error {
	list, err := s.st.PendingChannelPosts(ctx, s.st.Conn())
	if err != nil || len(list) == 0 {
		return err
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	irt := irtFromSettings(set)
	cfg := s.announceConfig(set)
	cfg.ChannelLangs = "" // each post keeps the language it was sent with
	for i := range list {
		a := &list[i]
		fail := func(reason string) error {
			return s.finishChannel(ctx, a.ID, "failed", 0, reason)
		}
		if a.ChannelChat == "" {
			if err := fail("no channel is set"); err != nil {
				return err
			}
			continue
		}
		p, err := s.AnnounceProductOf(ctx, a.ProductKind, a.ProductID)
		var fe *FieldError
		if errors.As(err, &fe) {
			if err := fail(fe.Msg); err != nil {
				return err
			}
			continue
		} else if err != nil {
			return err
		}
		cfg.ChannelLangs = a.ChannelLangs
		text, lang, ok := channelPost(cfg, a.Texts, p, brandName(set), irt)
		if !ok {
			if err := fail("the card has no text in the channel's language"); err != nil {
				return err
			}
			continue
		}
		if s.announce == nil {
			if err := fail("the bot's address is not configured (BOBRES_BOT_URL)"); err != nil {
				return err
			}
			continue
		}
		res, err := s.announce.PostChannel(ctx, ChannelPost{Chat: a.ChannelChat, Text: text, Label: s.channelLabel(ctx, lang), Start: p.Start})
		switch {
		case err != nil:
			if a.SentAt != nil && s.now().Sub(*a.SentAt) > channelPostGiveUp {
				if err := fail("the bot did not answer"); err != nil {
					return err
				}
				continue
			}
			s.cfg.Log.Warn("announcement channel post: the bot did not answer; trying again", "announcement", a.ID, "err", err)
		case res.OK:
			if err := s.finishChannel(ctx, a.ID, "sent", res.MessageID, ""); err != nil {
				return err
			}
		default:
			reason := res.Detail
			if res.Problem != "" {
				reason = res.Problem + ": " + res.Detail
			}
			if err := fail(reason); err != nil {
				return err
			}
		}
	}
	return nil
}

// finishChannel records how a channel post went, with its audit entry.
func (s *Service) finishChannel(ctx context.Context, id, status string, messageID int64, reason string) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetAnnouncementChannel(ctx, tx, id, status, messageID, reason); err != nil {
			return err
		}
		after := map[string]any{"status": status}
		if messageID != 0 {
			after["message_id"] = messageID
		}
		if reason != "" {
			after["reason"] = reason
		}
		return s.audit(ctx, tx, nil, "announcement.channel", "announcement", id, after, "")
	})
}
