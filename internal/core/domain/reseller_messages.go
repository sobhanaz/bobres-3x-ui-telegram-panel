package domain

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Messages to resellers only (docs/04-dashboard.md, Resellers). Staff write one text per language with placeholders; it goes out through the
// broadcast machinery (the same queue, pace and counts as a broadcast to customers) to the active resellers they pick: every one, those in debt,
// or one tier. When it starts, each reseller's text is written with their own figures and kept with them as a recipient, so the bot sends what
// was written for them. The weekly settlement reminder (reseller_reminder.go) is such a message, queued by core.

// ResellerPlaceholders are the values a message to resellers may use, as {name}.
var ResellerPlaceholders = []string{"name", "debt", "balance", "credit_limit", "tier", "customers"}

// resellerPlaceholderRe finds what looks like a placeholder: a word in braces.
var resellerPlaceholderRe = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// resellerValueRoom is what each placeholder may grow a text by when it is filled in (a name, an amount in up to three currencies): a template
// is that much shorter than Telegram's limit for each placeholder in it, so the texts written from it fit.
const resellerValueRoom = 64

// CheckResellerText checks a message to resellers in lang: Telegram's HTML, short enough for the values it will get, and only known
// placeholders. It returns the text normalised; a problem is a *TextError keyed key.
func CheckResellerText(lang, key, text string) (string, error) {
	n := len(resellerPlaceholderRe.FindAllStringIndex(text, -1))
	max := max(i18n.MaxMessage-n*resellerValueRoom, 0)
	v, probs := i18n.ValidateMessage(text, max)
	seen := map[string]bool{}
	for _, m := range resellerPlaceholderRe.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(ResellerPlaceholders, m[1]) && !seen[m[1]] {
			seen[m[1]] = true
			probs = append(probs, i18n.Problem{Code: i18n.ProblemUnknown, Arg: m[0]})
		}
	}
	if len(probs) > 0 {
		return "", &TextError{Lang: lang, Key: key, Problems: probs}
	}
	return v, nil
}

// ResellerTextProblems is the live check of the composer: the problems of a text, none when it can be sent.
func ResellerTextProblems(lang, text string) (string, []i18n.Problem) {
	v, err := CheckResellerText(lang, "texts."+lang, text)
	var te *TextError
	if errors.As(err, &te) {
		return i18n.NormalizeText(text), te.Problems
	}
	return v, nil
}

// resellerValues are a reseller's values in lang, ready for an HTML message (escaped). irt holds the store's names for the Toman.
func resellerValues(c *store.ResellerContact, lang string, irt map[string]string) map[string]string {
	lang = i18n.Normalize(lang)
	sep := ", "
	if lang == "fa" {
		sep = "، "
	}
	cash := func(minor int64, cur string) string { return i18n.MoneyNamed(irt, lang, minor, cur) }
	// IRT first (always listed, 0 when the reseller has no wallet yet), then the other currencies in order.
	wallets := slices.Clone(c.Wallets)
	if !slices.ContainsFunc(wallets, func(w store.Wallet) bool { return w.Currency == "IRT" }) {
		wallets = append(wallets, store.Wallet{Currency: "IRT"})
	}
	slices.SortFunc(wallets, func(a, b store.Wallet) int {
		switch {
		case a.Currency == b.Currency:
			return 0
		case a.Currency == "IRT":
			return -1
		case b.Currency == "IRT":
			return 1
		}
		return strings.Compare(a.Currency, b.Currency)
	})
	var debts, balances, limits []string
	for _, w := range wallets {
		if w.Balance < 0 {
			debts = append(debts, cash(-w.Balance, w.Currency))
		}
		if w.Currency == "IRT" || w.Balance != 0 {
			balances = append(balances, cash(w.Balance, w.Currency))
		}
		if w.Currency == "IRT" || w.CreditLimit != 0 {
			limits = append(limits, cash(w.CreditLimit, w.Currency))
		}
	}
	debt := cash(0, "IRT")
	if len(debts) > 0 {
		debt = strings.Join(debts, " + ")
	}
	name := strconv.FormatInt(c.TelegramID, 10)
	switch {
	case c.Username != "":
		name = "@" + c.Username
	case c.PanelGroup != "":
		name = c.PanelGroup
	}
	return map[string]string{
		"name": html.EscapeString(name), "debt": html.EscapeString(debt), "balance": html.EscapeString(strings.Join(balances, sep)),
		"credit_limit": html.EscapeString(strings.Join(limits, sep)), "tier": html.EscapeString(c.TierName),
		"customers": i18n.Number(lang, c.Customers),
	}
}

// RenderResellerText fills a checked text's placeholders with a reseller's values.
func RenderResellerText(text, lang string, c *store.ResellerContact, irt map[string]string) string {
	vals := resellerValues(c, lang, irt)
	return resellerPlaceholderRe.ReplaceAllStringFunc(text, func(m string) string {
		if v, ok := vals[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// sampleReseller stands in for a reseller in a preview when the store has none yet.
func sampleReseller() *store.ResellerContact {
	return &store.ResellerContact{TelegramID: 1, Username: "reseller", TierName: "Gold", Customers: 12,
		Wallets: []store.Wallet{{Currency: "IRT", Balance: -1_250_000, CreditLimit: 5_000_000}}}
}

// irtNames are the store's names for the Toman (branding.currency.fa and .en), read in q.
func (s *Service) irtNames(ctx context.Context, q pgx.Tx) (map[string]string, error) {
	out := map[string]string{}
	for _, l := range i18n.Languages {
		raw, err := s.st.GetSetting(ctx, q, "branding.currency."+l)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var v string
		if json.Unmarshal(raw, &v) == nil {
			out[l] = v
		}
	}
	return out, nil
}

// personalize writes the recipients of a message to resellers as it starts: its audience now, each with the text in their language (or the
// other one) filled in with their figures now.
func (s *Service) personalize(ctx context.Context, tx pgx.Tx, b *store.Broadcast) ([]store.Recipient, error) {
	contacts, err := s.st.ResellerContacts(ctx, tx, store.ResellerAudience{DebtOnly: b.ResellerDebtOnly, TierID: b.ResellerTierID, Language: b.Language})
	if err != nil {
		return nil, err
	}
	irt, err := s.irtNames(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Recipient, 0, len(contacts))
	for i := range contacts {
		c := &contacts[i]
		lang := i18n.Normalize(c.Language)
		text := b.Texts[lang]
		if text == "" { // a language the message has no text in: the other one, in its own words
			for _, l := range i18n.Languages {
				if b.Texts[l] != "" {
					text, lang = b.Texts[l], l
					break
				}
			}
		}
		out = append(out, store.Recipient{UserID: c.UserID, TelegramID: c.TelegramID, Language: c.Language,
			Text: RenderResellerText(text, lang, c, irt)})
	}
	return out, nil
}

// ResellerMessageInput is a message to resellers as staff write it.
type ResellerMessageInput struct {
	DebtOnly     bool
	TierID       string
	Language     string // "" (everyone) | fa | en
	Texts        map[string]string
	ButtonURL    string
	ButtonLabels map[string]string
	SettleButton bool
}

// normalizeResellerMessage checks a message to resellers: its tier (it must exist), its texts (placeholders included) and its buttons.
func (s *Service) normalizeResellerMessage(ctx context.Context, q pgx.Tx, in ResellerMessageInput) (*store.Broadcast, error) {
	texts := map[string]string{}
	if in.Language != "" && !slices.Contains(i18n.Languages, in.Language) {
		return nil, &FieldError{"language", "empty (everyone), fa or en"}
	}
	for _, l := range broadcastLangs(in.Language) {
		v, err := CheckResellerText(l, "texts."+l, in.Texts[l])
		if err != nil {
			return nil, err
		}
		texts[l] = v
	}
	b, err := normalizeBroadcast(BroadcastInput{Segment: "all", Language: in.Language, Texts: texts, ButtonURL: in.ButtonURL, ButtonLabels: in.ButtonLabels})
	if err != nil {
		return nil, err
	}
	if in.TierID != "" {
		if _, err := buuid.Parse(in.TierID); err != nil {
			return nil, &FieldError{"tier_id", "pick a tier"}
		}
		if _, err := s.st.GetTier(ctx, q, in.TierID, false); errors.Is(err, store.ErrNotFound) {
			return nil, &FieldError{"tier_id", "pick a tier"}
		} else if err != nil {
			return nil, err
		}
	}
	b.Segment, b.ResellerDebtOnly, b.ResellerTierID, b.SettleButton, b.Kind = store.SegmentResellers, in.DebtOnly, in.TierID, in.SettleButton, "manual"
	return b, nil
}

func resellerMessageAudit(b *store.Broadcast) map[string]any {
	out := broadcastAudit(b)
	out["debt_only"], out["tier_id"], out["settle_button"], out["kind"] = b.ResellerDebtOnly, b.ResellerTierID, b.SettleButton, b.Kind
	return out
}

// ResellerAudience counts who a message to resellers would reach now.
func (s *Service) ResellerAudience(ctx context.Context, debtOnly bool, tierID, language string) (total, fa, en int64, err error) {
	if language != "" && !slices.Contains(i18n.Languages, language) {
		return 0, 0, 0, &FieldError{"language", "empty (everyone), fa or en"}
	}
	if tierID != "" {
		if _, err := buuid.Parse(tierID); err != nil {
			return 0, 0, 0, &FieldError{"tier_id", "pick a tier"}
		}
	}
	return s.st.CountResellerAudience(ctx, s.st.Conn(), store.ResellerAudience{DebtOnly: debtOnly, TierID: tierID, Language: language})
}

// SendResellerMessage writes a message to resellers and puts it in line to go now; the broadcast worker starts it (and writes each
// reseller's text) within seconds.
func (s *Service) SendResellerMessage(ctx context.Context, actor *store.User, in ResellerMessageInput) (*store.Broadcast, error) {
	var id string
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		b, err := s.normalizeResellerMessage(ctx, tx, in)
		if err != nil {
			return err
		}
		b.CreatedBy = actor.ID
		if err := s.queueResellerBroadcast(ctx, tx, b); err != nil {
			return err
		}
		id = b.ID
		total, _, _, err := s.st.CountResellerAudience(ctx, tx, store.ResellerAudience{DebtOnly: b.ResellerDebtOnly, TierID: b.ResellerTierID, Language: b.Language})
		if err != nil {
			return err
		}
		after := resellerMessageAudit(b)
		after["audience_now"] = total
		return s.audit(ctx, tx, actor, "reseller_message.send", "broadcast", b.ID, after, "")
	})
	if err != nil {
		return nil, err
	}
	s.KickBroadcasts()
	return s.st.GetBroadcast(ctx, s.st.Conn(), id, false)
}

// queueResellerBroadcast saves a message to resellers already scheduled for now.
func (s *Service) queueResellerBroadcast(ctx context.Context, tx pgx.Tx, b *store.Broadcast) error {
	if err := s.st.InsertBroadcast(ctx, tx, b); err != nil {
		return err
	}
	now := time.Now() // the broadcast worker's clock: it starts at once
	ok, err := s.st.SetBroadcastStatus(ctx, tx, b.ID, []string{"draft"}, "scheduled", &now)
	if err != nil {
		return err
	}
	if !ok {
		return stateErr("the message could not be queued")
	}
	return nil
}

// ResellerPreview is a text written for one reseller (the sample one when the store has none).
type ResellerPreview struct {
	Text     string
	Reseller *store.ResellerContact // nil: the sample values
}

// PreviewResellerText checks text (in lang) and writes it for the reseller resellerID; "" takes the first reseller of the audience (debtOnly,
// tierID), or the sample values when it is empty.
func (s *Service) PreviewResellerText(ctx context.Context, resellerID, lang, text string, debtOnly bool, tierID string) (*ResellerPreview, error) {
	if !slices.Contains(i18n.Languages, lang) {
		return nil, &FieldError{"lang", "fa or en"}
	}
	v, err := CheckResellerText(lang, "texts."+lang, text)
	if err != nil {
		return nil, err
	}
	var out *ResellerPreview
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		c, err := s.previewReseller(ctx, tx, resellerID, debtOnly, tierID)
		if err != nil {
			return err
		}
		irt, err := s.irtNames(ctx, tx)
		if err != nil {
			return err
		}
		out = &ResellerPreview{Reseller: c}
		if c == nil {
			c = sampleReseller()
		}
		out.Text = RenderResellerText(v, lang, c, irt)
		return nil
	})
	return out, err
}

func (s *Service) previewReseller(ctx context.Context, tx pgx.Tx, resellerID string, debtOnly bool, tierID string) (*store.ResellerContact, error) {
	if resellerID != "" {
		if _, err := buuid.Parse(resellerID); err != nil {
			return nil, &FieldError{"reseller_id", "pick a reseller"}
		}
		c, err := s.st.ResellerContact(ctx, tx, resellerID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, &FieldError{"reseller_id", "pick a reseller"}
		}
		return c, err
	}
	if tierID != "" {
		if _, err := buuid.Parse(tierID); err != nil {
			tierID = ""
		}
	}
	list, err := s.st.ResellerContacts(ctx, tx, store.ResellerAudience{DebtOnly: debtOnly, TierID: tierID})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// SettleLabel is the label of the Settle / Top up button in lang, as the bot shows it (the store's own text when it changed it).
func (s *Service) SettleLabel(ctx context.Context, lang string) string {
	lang = i18n.Normalize(lang)
	if set, err := s.Settings(ctx); err == nil {
		if v := set["texts."+lang+"."+SettleButtonText]; v != "" {
			return v
		}
	}
	return Texts().T(lang, SettleButtonText)
}

// SettleButtonText is the bot text of the Settle / Top up button.
const SettleButtonText = "btn.rs_settle"
