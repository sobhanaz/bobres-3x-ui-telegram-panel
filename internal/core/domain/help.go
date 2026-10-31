package domain

import (
	"context"
	_ "embed" // the default help content
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/helpcontent"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Help content: the questions of the FAQ under Support and the tutorials behind
// the bot's Tutorials button, written by the owner in the dashboard in Persian
// and English (docs/03-bot-ux.md, "Help content"). A question or tutorial is
// stored with its text by language; the bot asks for the enabled ones in a
// customer's language and gets the other language where a translation is
// missing.

// Problem codes of a help entry beyond the ones of the bot's texts
// (i18n.Problem*, which the text checks use and which are reused here).
const (
	// HelpProblemUnknownKind: the kind is neither faq nor tutorial.
	HelpProblemUnknownKind = "unknown_kind"
	// HelpProblemPairMissing: a language has a title without a text, or a text
	// without a title; Arg is the one that is empty ("title" or "body").
	HelpProblemPairMissing = "pair_missing"
	// HelpProblemNeedLanguage: nothing is written in any language.
	HelpProblemNeedLanguage = "need_one_language"
	// HelpProblemBadURL: a button's address is not an https link the bot can use.
	HelpProblemBadURL = "bad_url"
	// HelpProblemURLMissing: a button has no address.
	HelpProblemURLMissing = "url_missing"
	// HelpProblemLabelMissing: a button has no label in any language.
	HelpProblemLabelMissing = "label_missing"
	// HelpProblemTooManyButtons: Arg is the most a tutorial may have.
	HelpProblemTooManyButtons = "too_many_buttons"
	// HelpProblemNoButtons: only a tutorial has buttons.
	HelpProblemNoButtons = "buttons_not_allowed"
)

// helpDefaultMark records that the default help content was offered once. It
// is internal state with no SettingSpec, so the dashboard never lists or edits
// it.
const helpDefaultMark = "state.help.default_applied"

// helpExtrasMark records that the tutorials added to the defaults after the first release (Karing, NPV Tunnel) were offered once to a
// store that already had its own.
const helpExtrasMark = "state.help.extra_tutorials_1"

// extraTutorialWords name the default tutorials that came later, by a word of their English title.
var extraTutorialWords = []string{"karing", "npv tunnel"}

// HelpInput is what the owner writes for one entry.
type HelpInput struct {
	Kind string
	// Title is the question or the tutorial's title (plain text on one line) and
	// Body the answer or the tutorial's text (Telegram's HTML subset), each by
	// language.
	Title   map[string]string
	Body    map[string]string
	Buttons []store.HelpButton // tutorials only
	Enabled bool
}

// HelpProblem is one reason an entry cannot be saved: the field it concerns
// ("title.fa", "body.en", "buttons.1.url", "buttons.0.label.fa", "buttons";
// "" for the entry as a whole), a code and what it names (a limit, a tag, a
// link).
type HelpProblem struct {
	Field string `json:"field"`
	Code  string `json:"code"`
	Arg   string `json:"arg"`
}

// HelpError lists what is wrong with an entry. It is an ErrInvalid.
type HelpError struct{ Problems []HelpProblem }

func (e *HelpError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Code
		if p.Field != "" {
			parts[i] = p.Field + ": " + p.Code
		}
		if p.Arg != "" {
			parts[i] += " (" + p.Arg + ")"
		}
	}
	return ErrInvalid.Error() + ": help entry: " + strings.Join(parts, ", ")
}

func (e *HelpError) Unwrap() error { return ErrInvalid }

// helpProblems collects distinct problems.
type helpProblems []HelpProblem

func (ps *helpProblems) add(field, code, arg string) {
	p := HelpProblem{field, code, arg}
	if !slices.Contains(*ps, p) {
		*ps = append(*ps, p)
	}
}

func (ps *helpProblems) addAll(field string, list []i18n.Problem) {
	for _, p := range list {
		ps.add(field, p.Code, p.Arg)
	}
}

// helpHrefRe finds the addresses of the links in a text (the checks of the
// bot's texts have let only <a href="..."> through).
var helpHrefRe = regexp.MustCompile(`(?i)<a\s+href\s*=\s*"([^"<>]*)"\s*>`)

// helpURL accepts an address the bot may put on a button or in a link: https,
// with a host that has a dot, no user name or password, plain ASCII with no
// space, at most MaxURL long. (Telegram refuses a whole message for one button
// address it does not like, so what is not plainly fine is not let in.)
func helpURL(raw string) bool {
	if raw == "" || len(raw) > helpcontent.MaxURL {
		return false
	}
	for _, r := range raw {
		if r <= ' ' || r >= 0x7f {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := u.Hostname()
	return strings.Contains(host, ".") && !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".")
}

// helpLine checks a title or a button label: one line of plain text of at
// most max units. It comes back with its white space gathered into single
// spaces ("" for a text with nothing in it). Markup characters are fine: the
// bot escapes them where a title goes into HTML, and a button's text is plain.
func helpLine(s string, max int) (string, []i18n.Problem) {
	if !utf8.ValidString(s) {
		return "", []i18n.Problem{{Code: i18n.ProblemBadUTF8}}
	}
	v := strings.Join(strings.Fields(i18n.NormalizeText(s)), " ")
	var ps []i18n.Problem
	for _, r := range v {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			ps = append(ps, i18n.Problem{Code: i18n.ProblemControlChar, Arg: strconv.QuoteRune(r)})
		}
	}
	if helpcontent.Units(v) > max {
		ps = append(ps, i18n.Problem{Code: i18n.ProblemTooLong, Arg: strconv.Itoa(max)})
	}
	return v, ps
}

// helpText checks the answer or text of an entry the way the bot's own texts
// are checked (Telegram's HTML subset and nothing else: i18n.ValidateMessage),
// with the one rule of its own that the links in it are https. It comes back
// normalised ("" for a text with nothing in it).
func helpText(s string) (string, []i18n.Problem) {
	if strings.TrimSpace(s) == "" {
		return "", nil
	}
	v, ps := i18n.ValidateMessage(s, helpcontent.MaxBody)
	for _, m := range helpHrefRe.FindAllStringSubmatch(v, -1) {
		if !helpURL(html.UnescapeString(m[1])) {
			ps = append(ps, i18n.Problem{Code: i18n.ProblemBadLink, Arg: m[1]})
		}
	}
	return v, ps
}

// NormalizeHelp checks what the owner wrote for an entry and returns it as it
// is stored: titles on one line, texts normalised, only the languages that
// have something. An entry must be readable in at least one language: a title
// and a text. The problems name the fields, for the dashboard to show.
func NormalizeHelp(in HelpInput) (HelpInput, []HelpProblem) {
	var ps helpProblems
	out := HelpInput{Kind: in.Kind, Enabled: in.Enabled, Title: map[string]string{}, Body: map[string]string{}, Buttons: []store.HelpButton{}}
	if !helpcontent.IsKind(in.Kind) {
		ps.add("kind", HelpProblemUnknownKind, in.Kind)
		return out, ps
	}
	for _, f := range []struct {
		name string
		text map[string]string
	}{{"title", in.Title}, {"body", in.Body}} {
		for _, l := range slices.Sorted(maps.Keys(f.text)) {
			if !slices.Contains(i18n.Languages, l) {
				ps.add(f.name+"."+l, i18n.ProblemLangUnused, l)
			}
		}
	}
	written := 0
	for _, l := range i18n.Languages {
		title, tp := helpLine(in.Title[l], helpcontent.MaxTitle(in.Kind))
		body, bp := helpText(in.Body[l])
		ps.addAll("title."+l, tp)
		ps.addAll("body."+l, bp)
		switch {
		case title == "" && body == "":
			continue
		case title == "":
			ps.add("title."+l, HelpProblemPairMissing, "title")
		case body == "":
			ps.add("body."+l, HelpProblemPairMissing, "body")
		}
		written++
		if title != "" {
			out.Title[l] = title
		}
		if body != "" {
			out.Body[l] = body
		}
	}
	if written == 0 {
		ps.add("", HelpProblemNeedLanguage, "")
	}
	out.Buttons = normalizeHelpButtons(in, &ps)
	if len(ps) > 0 {
		return out, ps
	}
	return out, nil
}

// normalizeHelpButtons checks a tutorial's URL buttons.
func normalizeHelpButtons(in HelpInput, ps *helpProblems) []store.HelpButton {
	out := []store.HelpButton{}
	switch {
	case len(in.Buttons) == 0:
		return out
	case in.Kind != helpcontent.KindTutorial:
		ps.add("buttons", HelpProblemNoButtons, "")
		return out
	case len(in.Buttons) > helpcontent.MaxButtons:
		ps.add("buttons", HelpProblemTooManyButtons, strconv.Itoa(helpcontent.MaxButtons))
		return out
	}
	for i, b := range in.Buttons {
		field := "buttons." + strconv.Itoa(i)
		nb := store.HelpButton{URL: strings.TrimSpace(b.URL), Label: map[string]string{}}
		switch {
		case nb.URL == "":
			ps.add(field+".url", HelpProblemURLMissing, "")
		case !helpURL(nb.URL):
			ps.add(field+".url", HelpProblemBadURL, nb.URL)
		}
		for _, l := range slices.Sorted(maps.Keys(b.Label)) {
			if !slices.Contains(i18n.Languages, l) {
				ps.add(field+".label."+l, i18n.ProblemLangUnused, l)
			}
		}
		for _, l := range i18n.Languages {
			label, lp := helpLine(b.Label[l], helpcontent.MaxButtonLabel)
			ps.addAll(field+".label."+l, lp)
			if label != "" {
				nb.Label[l] = label
			}
		}
		if len(nb.Label) == 0 {
			ps.add(field+".label."+i18n.Languages[0], HelpProblemLabelMissing, "")
		}
		out = append(out, nb)
	}
	return out
}

func helpActorID(actor *store.User) *string {
	if actor == nil {
		return nil
	}
	return &actor.ID
}

// helpAudit is an entry as the audit log keeps it.
func helpAudit(e *store.HelpEntry) map[string]any {
	return map[string]any{"kind": e.Kind, "title": e.Title, "body": e.Body, "buttons": e.Buttons, "enabled": e.Enabled}
}

// CreateHelp adds an entry after the last one of its kind.
func (s *Service) CreateHelp(ctx context.Context, actor *store.User, in HelpInput) (*store.HelpEntry, error) {
	norm, probs := NormalizeHelp(in)
	if len(probs) > 0 {
		return nil, &HelpError{probs}
	}
	e := &store.HelpEntry{Kind: norm.Kind, Title: norm.Title, Body: norm.Body, Buttons: norm.Buttons, Enabled: norm.Enabled,
		UpdatedBy: helpActorID(actor)}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		n, err := s.st.CountHelp(ctx, tx, e.Kind)
		if err != nil {
			return err
		}
		if limit := helpcontent.MaxEntries(e.Kind); n >= limit {
			return stateErr("a store keeps at most %d entries of this kind", limit)
		}
		if err := s.st.InsertHelp(ctx, tx, e); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "help.create", "help_entry", e.ID, helpAudit(e), "")
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}

// UpdateHelp saves an entry read at version: its texts, buttons and whether it
// is shown. Its kind and its place in the list stay. Saving what is stored
// already changes nothing.
func (s *Service) UpdateHelp(ctx context.Context, actor *store.User, id string, in HelpInput, version int) (*store.HelpEntry, error) {
	var out *store.HelpEntry
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetHelp(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if in.Kind != "" && in.Kind != old.Kind {
			return invalid("the kind of an entry cannot be changed")
		}
		in.Kind = old.Kind
		norm, probs := NormalizeHelp(in)
		if len(probs) > 0 {
			return &HelpError{probs}
		}
		if old.Version != version {
			return stateErr("someone changed this entry meanwhile; reload it")
		}
		e := *old
		e.Title, e.Body, e.Buttons, e.Enabled = norm.Title, norm.Body, norm.Buttons, norm.Enabled
		if reflect.DeepEqual(helpAudit(old), helpAudit(&e)) {
			out = old
			return nil
		}
		e.UpdatedBy = helpActorID(actor)
		if err := s.st.UpdateHelp(ctx, tx, &e, version); errors.Is(err, store.ErrStale) {
			return stateErr("someone changed this entry meanwhile; reload it")
		} else if err != nil {
			return err
		}
		out = &e
		return s.auditChange(ctx, tx, actor, "help.update", "help_entry", id, helpAudit(old), helpAudit(&e), "")
	})
	return out, err
}

// SetHelpEnabled shows or hides an entry read at version. Asking for the state
// it is in changes nothing.
func (s *Service) SetHelpEnabled(ctx context.Context, actor *store.User, id string, enabled bool, version int) (*store.HelpEntry, error) {
	var out *store.HelpEntry
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetHelp(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if old.Version != version {
			return stateErr("someone changed this entry meanwhile; reload it")
		}
		if old.Enabled == enabled {
			out = old
			return nil
		}
		e := *old
		e.Enabled, e.UpdatedBy = enabled, helpActorID(actor)
		if err := s.st.UpdateHelp(ctx, tx, &e, version); errors.Is(err, store.ErrStale) {
			return stateErr("someone changed this entry meanwhile; reload it")
		} else if err != nil {
			return err
		}
		out = &e
		action := "help.disable"
		if enabled {
			action = "help.enable"
		}
		return s.auditChange(ctx, tx, actor, action, "help_entry", id,
			map[string]any{"kind": old.Kind, "enabled": old.Enabled}, map[string]any{"kind": e.Kind, "enabled": e.Enabled}, "")
	})
	return out, err
}

// DeleteHelp removes an entry for good.
func (s *Service) DeleteHelp(ctx context.Context, actor *store.User, id string) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetHelp(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if err := s.st.DeleteHelp(ctx, tx, id); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "help.delete", "help_entry", id, helpAudit(old), nil, "")
	})
}

// ReorderHelp puts the entries of a kind in the order of ids, which must name
// each of them once: the list the editor showed. One that came or went
// meanwhile makes it a conflict (ErrState), so no entry is put in a wrong place.
func (s *Service) ReorderHelp(ctx context.Context, actor *store.User, kind string, ids []string) error {
	if !helpcontent.IsKind(kind) {
		return invalid("kind must be faq or tutorial")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return invalid("an entry is listed twice")
		}
		seen[id] = true
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.st.SetHelpOrder(ctx, tx, kind, ids)
		if errors.Is(err, store.ErrStale) {
			return stateErr("the list changed meanwhile; reload it")
		}
		if err != nil || slices.Equal(before, ids) {
			return err
		}
		return s.auditChange(ctx, tx, actor, "help.reorder", "help_entry", kind,
			map[string]any{"kind": kind, "order": before}, map[string]any{"kind": kind, "order": ids}, "")
	})
}

// HelpEntries lists every entry, shown or not, for the dashboard's editor.
func (s *Service) HelpEntries(ctx context.Context) ([]store.HelpEntry, error) {
	return s.st.ListHelp(ctx, s.st.Conn(), "", false)
}

// HelpAvailable reports whether there is something to show: an enabled
// question, an enabled tutorial. The bot shows its FAQ and Tutorials buttons
// by it.
func (s *Service) HelpAvailable(ctx context.Context) (faq, tutorials bool, err error) {
	return s.st.HelpEnabledKinds(ctx, s.st.Conn())
}

// HelpView is an entry as one customer reads it: its text in one language,
// the customer's own, or the other one when that translation is missing.
type HelpView struct {
	ID       string
	Kind     string
	Title    string // plain text
	Body     string // Telegram's HTML subset
	Buttons  []HelpViewButton
	Language string // the language Title and Body are in
}

// HelpViewButton is a URL button as a customer sees it.
type HelpViewButton struct {
	Label string
	URL   string
}

// ResolveHelp reads an entry in a language, falling back to the other where it
// has no title and text in it; false when it has none in any. A button whose
// label is missing in the language takes the other one's; one with no label
// or no usable address is left out.
func ResolveHelp(e *store.HelpEntry, lang string) (HelpView, bool) {
	lang = i18n.Normalize(lang)
	order := i18n.ContentOrder(lang) // a beta language reads English before Persian
	for _, l := range order {
		if e.Title[l] == "" || e.Body[l] == "" {
			continue
		}
		v := HelpView{ID: e.ID, Kind: e.Kind, Title: e.Title[l], Body: e.Body[l], Language: l}
		for _, b := range e.Buttons {
			if !helpURL(b.URL) {
				continue
			}
			for _, bl := range order {
				if b.Label[bl] != "" {
					v.Buttons = append(v.Buttons, HelpViewButton{Label: b.Label[bl], URL: b.URL})
					break
				}
			}
		}
		return v, true
	}
	return HelpView{}, false
}

// helpUserLanguage is the language to read an entry in for a customer: the one
// asked for, else the customer's own.
func helpUserLanguage(asked string, u *store.User) string {
	if asked != "" {
		return i18n.Normalize(asked)
	}
	return i18n.Normalize(u.Language)
}

// HelpFor lists the enabled entries of a kind, in the owner's order, as one
// customer reads them. The customer is refused the way they are everywhere
// else when banned (ErrUserBanned), and unknown ones are ErrNotFound.
func (s *Service) HelpFor(ctx context.Context, userID, kind, lang string) ([]HelpView, error) {
	if !helpcontent.IsKind(kind) {
		return nil, invalid("kind must be faq or tutorial")
	}
	if _, err := buuid.Parse(userID); err != nil {
		return nil, invalid("user_id must be a uuid")
	}
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	lang = helpUserLanguage(lang, u)
	entries, err := s.st.ListHelp(ctx, s.st.Conn(), kind, true)
	if err != nil {
		return nil, err
	}
	out := make([]HelpView, 0, len(entries))
	for i := range entries {
		if v, ok := ResolveHelp(&entries[i], lang); ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// HelpEntryFor reads one enabled entry as a customer does; ErrNotFound for one
// that is gone or switched off.
func (s *Service) HelpEntryFor(ctx context.Context, userID, id, lang string) (*HelpView, error) {
	for _, f := range []struct{ name, value string }{{"user_id", userID}, {"id", id}} {
		if _, err := buuid.Parse(f.value); err != nil {
			return nil, invalid("%s must be a uuid", f.name)
		}
	}
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	e, err := s.st.GetHelp(ctx, s.st.Conn(), id, false)
	if err != nil {
		return nil, err
	}
	if !e.Enabled {
		return nil, store.ErrNotFound
	}
	v, ok := ResolveHelp(e, helpUserLanguage(lang, u))
	if !ok {
		return nil, store.ErrNotFound
	}
	return &v, nil
}

// FAQLayout says how the bot shows the enabled FAQ in a language: "message"
// (every question with its answer in one message) or "list" (a numbered list of
// the questions, each opening its answer); "" when there is nothing to show.
// The bot decides by the same rule (helpcontent.FitsOneMessage), so the
// dashboard can say what customers will see.
func FAQLayout(entries []store.HelpEntry, lang string) string {
	n, units := 0, 0
	for i := range entries {
		e := &entries[i]
		if e.Kind != helpcontent.KindFAQ || !e.Enabled {
			continue
		}
		if v, ok := ResolveHelp(e, lang); ok {
			n++
			units += helpcontent.Units(v.Title) + i18n.TextLength(v.Body)
		}
	}
	switch {
	case n == 0:
		return ""
	case helpcontent.FitsOneMessage(n, units):
		return "message"
	}
	return "list"
}

// --- The default content ---

//go:embed help_defaults.json
var helpDefaultsJSON []byte

type helpDefault struct {
	Title   map[string]string  `json:"title"`
	Body    map[string]string  `json:"body"`
	Buttons []store.HelpButton `json:"buttons"`
}

// helpDefaults parses the default content once.
var helpDefaults = sync.OnceValue(func() map[string][]helpDefault {
	var file struct {
		FAQ       []helpDefault `json:"faq"`
		Tutorials []helpDefault `json:"tutorials"`
	}
	if err := json.Unmarshal(helpDefaultsJSON, &file); err != nil {
		panic(fmt.Sprintf("domain: help_defaults.json: %v", err))
	}
	return map[string][]helpDefault{helpcontent.KindFAQ: file.FAQ, helpcontent.KindTutorial: file.Tutorials}
})

// DefaultHelp is the default content of a kind, as the inputs it is stored
// from: a generic FAQ and the tutorials for the apps the subscription page
// knows (and Happ), for the owner to edit.
func DefaultHelp(kind string) []HelpInput {
	var out []HelpInput
	for _, d := range helpDefaults()[kind] {
		out = append(out, HelpInput{Kind: kind, Title: d.Title, Body: d.Body, Buttons: d.Buttons, Enabled: true})
	}
	return out
}

// addDefaultHelp stores the default entries of a kind, in order.
func (s *Service) addDefaultHelp(ctx context.Context, tx pgx.Tx, actor *store.User, kind string) ([]store.HelpEntry, error) {
	var out []store.HelpEntry
	for _, in := range DefaultHelp(kind) {
		norm, probs := NormalizeHelp(in)
		if len(probs) > 0 {
			return nil, fmt.Errorf("domain: the default %s content is not valid: %w", kind, &HelpError{probs})
		}
		e := store.HelpEntry{Kind: kind, Title: norm.Title, Body: norm.Body, Buttons: norm.Buttons, Enabled: true, UpdatedBy: helpActorID(actor)}
		if err := s.st.InsertHelp(ctx, tx, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// AddDefaultHelp fills an empty list with the default content of its kind, for
// the owner who emptied it (or switched it off for good) and wants a start.
// A list with entries in it is left alone (ErrState).
func (s *Service) AddDefaultHelp(ctx context.Context, actor *store.User, kind string) ([]store.HelpEntry, error) {
	if !helpcontent.IsKind(kind) {
		return nil, invalid("kind must be faq or tutorial")
	}
	var out []store.HelpEntry
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		n, err := s.st.CountHelp(ctx, tx, kind)
		if err != nil {
			return err
		}
		if n > 0 {
			return stateErr("this list has entries already: the defaults only fill an empty one")
		}
		if out, err = s.addDefaultHelp(ctx, tx, actor, kind); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "help.defaults", "help_entry", kind, map[string]any{"kind": kind, "entries": len(out)}, "")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// EnsureDefaultHelp gives a store the default FAQ and tutorials, once: when
// it has no help entries yet and the default content was never offered. After
// that the owner owns the lists: entries they deleted are not put back (they can
// ask for the defaults again with AddDefaultHelp, on an empty list). applied is
// true when this call stored them.
func (s *Service) EnsureDefaultHelp(ctx context.Context) (applied bool, err error) {
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		applied = false
		cur, err := s.st.LockSettings(ctx, tx, []string{helpDefaultMark})
		if err != nil {
			return err
		}
		if _, done := cur[helpDefaultMark]; done {
			return nil
		}
		existing, err := s.st.ListHelp(ctx, tx, "", false)
		if err != nil {
			return err
		}
		if len(existing) == 0 {
			counts := map[string]int{}
			for _, kind := range helpcontent.Kinds {
				added, err := s.addDefaultHelp(ctx, tx, nil, kind)
				if err != nil {
					return err
				}
				counts[kind] = len(added)
			}
			if err := s.audit(ctx, tx, nil, "help.defaults", "help_entry", "", counts, ""); err != nil {
				return err
			}
			applied = true
		}
		raw, _ := json.Marshal("1")
		return s.st.SetSetting(ctx, tx, helpDefaultMark, raw)
	})
	return applied && err == nil, err
}

// EnsureExtraTutorials gives a store that already uses the tutorials the default ones that came later (Karing and NPV Tunnel), once, at the
// end of its list, unless a tutorial for that app is there already. A store with no tutorials gets nothing (the owner emptied the list or
// never used it: AddDefaultHelp gives them all), and what the owner deletes afterwards is never put back. added is how many were stored.
func (s *Service) EnsureExtraTutorials(ctx context.Context) (added int, err error) {
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		added = 0
		cur, err := s.st.LockSettings(ctx, tx, []string{helpExtrasMark})
		if err != nil {
			return err
		}
		if _, done := cur[helpExtrasMark]; done {
			return nil
		}
		existing, err := s.st.ListHelp(ctx, tx, helpcontent.KindTutorial, false)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			has := func(word string) bool {
				for _, e := range existing {
					for _, title := range e.Title {
						if strings.Contains(strings.ToLower(title), word) {
							return true
						}
					}
				}
				return false
			}
			for _, in := range DefaultHelp(helpcontent.KindTutorial) {
				word := ""
				for _, w := range extraTutorialWords {
					if strings.Contains(strings.ToLower(in.Title["en"]), w) {
						word = w
					}
				}
				if word == "" || has(word) {
					continue
				}
				norm, probs := NormalizeHelp(in)
				if len(probs) > 0 {
					return fmt.Errorf("domain: the default tutorial %q is not valid: %w", in.Title["en"], &HelpError{probs})
				}
				e := store.HelpEntry{Kind: helpcontent.KindTutorial, Title: norm.Title, Body: norm.Body, Buttons: norm.Buttons, Enabled: true}
				if err := s.st.InsertHelp(ctx, tx, &e); err != nil {
					return err
				}
				added++
			}
			if added > 0 {
				if err := s.audit(ctx, tx, nil, "help.defaults", "help_entry", "tutorial", map[string]any{"kind": "tutorial", "entries": added, "extra": true}, ""); err != nil {
					return err
				}
			}
		}
		raw, _ := json.Marshal("1")
		return s.st.SetSetting(ctx, tx, helpExtrasMark, raw)
	})
	return added, err
}
